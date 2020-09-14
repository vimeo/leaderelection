// Package memory implements an in-memory variant of the Decider to allow for
// quick local/single-process testing
package memory

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/vimeo/leaderelection/entry"
)

// Decider implements leaderelection.Decider
type Decider struct {
	l               sync.Mutex
	currentContents entry.RaceEntry
	incarnation     int64
	writeTS         time.Time
}

// NewDecider returns a new, initialized instance
func NewDecider() *Decider {
	return &Decider{
		currentContents: entry.RaceEntry{
			LeaderID:       "",
			HostPort:       nil,
			TermExpiry:     time.Time{},
			ElectionNumber: entry.NoElections,
			Token:          nil,
		},
		// Pick a random base-generation number so mixed-up tokens or
		// colliding memory-addresses are even less likely to be a problem
		incarnation: rand.Int63n(1 << 20),
	}
}

type token struct {
	self        *Decider
	incarnation int64
	ts          time.Time
}

func (t *token) CommitTS() time.Time {
	return t.ts
}

var _ entry.LeaderToken = (*token)(nil)

// failedAcquisitionErr implements leaderelection.FailedAcquisitionErr
type failedAcquisitionErr struct {
	currentIncarnation, tokenIncarnation int64
}

func (f *failedAcquisitionErr) Error() string {
	return fmt.Sprintf("failed to acquire lock, incorrect incarnation: token has %d, current %d",
		f.tokenIncarnation, f.currentIncarnation)
}

func (f *failedAcquisitionErr) FailedAcquire() {}

// WriteEntry accepts writes with the correct incarnation
// The interface{} return value is a new token if the write succeeded.
func (d *Decider) WriteEntry(ctx context.Context, entry *entry.RaceEntry) (entry.LeaderToken, error) {
	tok := entry.Token.(*token)
	d.l.Lock()
	defer d.l.Unlock()
	now := time.Now()
	if tok.incarnation != d.incarnation {
		return nil, &failedAcquisitionErr{
			currentIncarnation: d.incarnation,
			tokenIncarnation:   tok.incarnation,
		}
	}
	if tok.self != d {
		panic(fmt.Errorf("wrong tok.self passed to Decider: %p, expected %p",
			tok.self, d))
	}

	// Used in tests, so panic at will!
	if entry.ElectionNumber != d.currentContents.ElectionNumber+1 {
		panic(fmt.Errorf("invariant violation: entry.ElectionNumber(%d) != currentContents.ElectionNumber(%d)+1",
			entry.ElectionNumber, d.currentContents.ElectionNumber))
	}

	d.incarnation++
	d.currentContents = *entry
	d.currentContents.Token = nil
	d.writeTS = now

	return &token{incarnation: d.incarnation, ts: now, self: d}, nil
}

// ReadCurrent should provide the latest version of RaceEntry available
// and put any additional information needed to ensure transactional
// behavior in the Token-field.
func (d *Decider) ReadCurrent(ctx context.Context) (*entry.RaceEntry, error) {
	d.l.Lock()
	defer d.l.Unlock()
	ret := d.currentContents
	ret.Token = &token{incarnation: d.incarnation, self: d, ts: d.writeTS}
	return &ret, nil
}
