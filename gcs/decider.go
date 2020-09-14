// Package gcs contains an implementation of RaceDecider (plus helpers) for
// using Google Cloud Storage as a backend in leader-election.
package gcs

import (
	"context"
	"fmt"
	"hash/crc32"
	"io/ioutil"
	"net/http"
	"time"

	"google.golang.org/api/googleapi"

	"cloud.google.com/go/storage"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/protobuf/proto"

	"github.com/vimeo/leaderelection/entry"
	"github.com/vimeo/leaderelection/storagepb"
)

type deciderOptions struct {
	acls []storage.ACLRule
}

// DeciderOpts configures a GCS RaceDecider at construction-time
type DeciderOpts func(*deciderOptions)

// Decider implements leaderelection.RaceDecider using a specific object in a
// specific bucket for persistence
type Decider struct {
	gcsClient *storage.Client
	object    string
	bucket    *storage.BucketHandle
	acls      []storage.ACLRule
}

// NewDecider creates a new gcs.Decider
func NewDecider(client *storage.Client, bucket, object string, opts ...DeciderOpts) *Decider {
	cfg := deciderOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Decider{
		gcsClient: client,
		bucket:    client.Bucket(bucket),
		object:    object,
		acls:      cfg.acls,
	}
}

func (d *Decider) objHandle() *storage.ObjectHandle {
	return d.bucket.Object(d.object)
}

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

func validateRaceEntry(contents []byte, newEntry *storagepb.RaceEntry) error {
	entrypb := storagepb.RaceEntry{}

	if unmarshalErr := proto.Unmarshal(contents, &entrypb); unmarshalErr != nil {
		return unmarshalErr
	}
	if !proto.Equal(newEntry, &entrypb) {
		return fmt.Errorf("mismatched serialized and deserialized mesages: %s vs %s",
			newEntry, &entrypb)
	}
	return nil
}

// WriteEntry implementations should write the entry argument to
// stable-storage in a transactional-way such that only one contender
// wins per-election/term
func (d *Decider) WriteEntry(ctx context.Context, rentry *entry.RaceEntry) (entry.LeaderToken, error) {
	// let it panic!
	tok := rentry.Token.(*token)
	if tok.self != d {
		panic("tok.self != d")
	}

	if rentry.ElectionNumber < 0 {
		panic(fmt.Errorf("invalid election number: %d", rentry.ElectionNumber))
	}

	obj := d.objHandle()
	obj = obj.If(storage.Conditions{
		GenerationMatch: tok.generation,
		DoesNotExist:    rentry.ElectionNumber == 0,
	})
	ts, tsErr := ptypes.TimestampProto(rentry.TermExpiry)
	if tsErr != nil {
		return nil, fmt.Errorf("failed to convert termexpiry timestamp %s into timestamp proto: %w", rentry.TermExpiry, tsErr)
	}

	entrypb := storagepb.RaceEntry{
		ElectionNumber:       uint64(rentry.ElectionNumber),
		LeaderId:             rentry.LeaderID,
		HostPort:             rentry.HostPort,
		TermExpiry:           ts,
		ConnectionParameters: rentry.ConnectionParams,
	}

	newContents, marshalErr := proto.Marshal(&entrypb)
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to serialize entry for writing: %w", marshalErr)
	}

	crc := crc32.Checksum(newContents, crc32cTable)
	if unmarshalEqualErr := validateRaceEntry(newContents, &entrypb); unmarshalEqualErr != nil {
		// This should only ever fail if there's a bit-flip or
		// memory-corruption. Panic to be safe.
		panic(fmt.Errorf("entrypb round-trip failure: %w", unmarshalEqualErr))
	}

	writeCtx, writeCancel := context.WithDeadline(ctx, rentry.TermExpiry)
	defer writeCancel()
	w := obj.NewWriter(writeCtx)
	w.CRC32C = crc
	w.SendCRC32C = true
	w.ACL = d.acls

	_, wrErr := w.Write(newContents)
	if wrErr != nil {
		w.Close()
		return nil, fmt.Errorf("failed to write contents: %w", wrErr)
	}

	switch closeErr := w.Close(); ce := closeErr.(type) {
	case nil:
	case *googleapi.Error:
		if ce.Code == http.StatusPreconditionFailed {
			return nil, &failedAcquisitionErr{err: closeErr}
		}
		return nil, fmt.Errorf("failed to close: %w", closeErr)
	default:
		return nil, fmt.Errorf("failed to close: %w", closeErr)
	}

	return &token{
		self:       d,
		generation: w.Attrs().Generation,
		commitTS:   w.Attrs().Updated,
	}, nil
}

// failedAcquisitionErr implements leaderelection.FailedAcquisitionErr
type failedAcquisitionErr struct {
	err error
}

func (f *failedAcquisitionErr) Error() string {
	return fmt.Sprintf("failed to acquire lock: %s", f.err)
}

func (f *failedAcquisitionErr) Unwrap() error {
	return f.err
}

func (f *failedAcquisitionErr) FailedAcquire() {}

// ReadCurrent should provide the latest version of RaceEntry available
// and put any additional information needed to ensure transactional
// behavior in the Token-field.
func (d *Decider) ReadCurrent(ctx context.Context) (*entry.RaceEntry, error) {
	obj := d.objHandle()
	r, readerErr := obj.NewReader(ctx)
	switch readerErr {
	case nil:
	case storage.ErrObjectNotExist:
		return &entry.RaceEntry{
			LeaderID:       "",
			HostPort:       nil,
			TermExpiry:     time.Time{},
			ElectionNumber: entry.NoElections,
			Token:          &token{self: d, generation: 0},
		}, nil
	default:
		return nil, fmt.Errorf("unexpected error opening object %q: %w", d.object, readerErr)
	}
	defer r.Close()
	gen := r.Attrs.Generation
	tok := token{self: d, generation: gen, commitTS: r.Attrs.LastModified}
	contents, readErr := ioutil.ReadAll(r)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read existing value from object %q: %w", d.object, readErr)
	}

	entrypb := storagepb.RaceEntry{}

	if unmarshalErr := proto.Unmarshal(contents, &entrypb); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to unmarshal contents as protobuf: %w", unmarshalErr)
	}

	te, teErr := ptypes.Timestamp(entrypb.TermExpiry)
	if teErr != nil {
		// TODO: handle this more intelligently (we probably need to
		// increment a counter, log and then return a special error so
		// the caller knows to pick a plausible term expiration time.
		return nil, fmt.Errorf("malformed timestamp %v: %w", entrypb.TermExpiry, teErr)
	}

	return &entry.RaceEntry{
		LeaderID:       entrypb.LeaderId,
		HostPort:       entrypb.HostPort,
		TermExpiry:     te,
		ElectionNumber: entry.ElectionNumber(entrypb.ElectionNumber),
		Token:          &tok,
	}, nil

}

// token is used as the token-type in the RaceEntry objects we return (as a
// pointer)
type token struct {
	self       *Decider
	generation int64
	commitTS   time.Time
}

func (t *token) CommitTS() time.Time {
	return t.commitTS
}

var _ entry.LeaderToken = (*token)(nil)
