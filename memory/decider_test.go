package memory

import (
	"context"
	"testing"
	"time"

	"github.com/vimeo/leaderelection"
	"github.com/vimeo/leaderelection/entry"
)

func TestDecider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDecider()
	// Grab the base incarnation so we can check it in the tokens later.
	baseIncarnation := d.incarnation
	initialEntry, readErr := d.ReadCurrent(ctx)
	if readErr != nil {
		t.Fatalf("failed to read \"existing\" entry: %s", readErr)
	}
	if initialEntry.LeaderID != "" {
		t.Errorf("non-empty leaderID: %q", initialEntry.LeaderID)
	}
	if initialEntry.HostPort != "" {
		t.Errorf("non-empty HostPort: %q", initialEntry.HostPort)
	}
	if (initialEntry.TermExpiry != time.Time{}) {
		t.Errorf("non-zero TermExpiry time: %s", initialEntry.TermExpiry)
	}
	if initialEntry.ElectionNumber != entry.NoElections {
		t.Errorf("unexpected ElectionNumber: %d", initialEntry.ElectionNumber)
	}
	{
		tok := initialEntry.Token.(*token)
		if tok.self != d {
			t.Errorf("unexpected pointer value for \"self\": tok.self: %p, d: %p",
				tok.self, d)
		}
		if tok.incarnation != baseIncarnation {
			t.Errorf("unexpected incarnation: got %d; expected %d",
				tok.incarnation, baseIncarnation)
		}
	}

	firstExpire := time.Now().Add(time.Minute)
	firstWrittenEntry := entry.RaceEntry{
		LeaderID:       "foobar",
		HostPort:       "watthat:8080",
		TermExpiry:     firstExpire,
		ElectionNumber: initialEntry.ElectionNumber + 1,
		Token:          initialEntry.Token,
	}
	// first write attempt
	firstWriteTok, firstWriteErr := d.WriteEntry(ctx, &firstWrittenEntry)
	if firstWriteErr != nil {
		t.Fatalf("unexpected error writing entry %+v: %q",
			firstWrittenEntry, firstWriteErr)
	}
	{
		tok := firstWriteTok.(*token)
		if tok.self != d {
			t.Errorf("unexpected pointer value for \"self\": tok.self: %p, d: %p",
				tok.self, d)
		}
		if tok.incarnation != baseIncarnation+1 {
			t.Errorf("unexpected incarnation: got %d; expected %d",
				tok.incarnation, baseIncarnation+1)
		}
	}

	// now we try to write the same entry again, complete with the same
	// token, and expect to see a failed-write error
	conflictingWriteTok, conflictingWriteErr := d.WriteEntry(ctx, &firstWrittenEntry)
	switch conflictingWriteErr.(type) {
	case nil:
		t.Errorf("conflicting write succeeded after succesful write with the same token")
	case leaderelection.FailedAcquisitionErr:
		if conflictingWriteTok != nil {
			t.Errorf("unexpectedly non-nil token after FailedAcquisitionErr: %+v",
				conflictingWriteTok)
		}
	default:
		t.Errorf("unexpected error for conflicting write: %s", conflictingWriteErr)
	}

	secondWrittenEntry := entry.RaceEntry{
		LeaderID:       firstWrittenEntry.LeaderID,
		HostPort:       firstWrittenEntry.HostPort,
		TermExpiry:     time.Now().Add(2 * time.Minute),
		ElectionNumber: firstWrittenEntry.ElectionNumber + 1,
		Token:          firstWriteTok,
	}

	// second write attempt
	secondWriteTok, secondWriteErr := d.WriteEntry(ctx, &secondWrittenEntry)
	if secondWriteErr != nil {
		t.Fatalf("unexpected error writing entry %+v: %q",
			secondWrittenEntry, secondWriteErr)
	}
	{
		tok := secondWriteTok.(*token)
		if tok.self != d {
			t.Errorf("unexpected pointer value for \"self\": tok.self: %p, d: %p",
				tok.self, d)
		}
		if tok.incarnation != baseIncarnation+2 {
			t.Errorf("unexpected incarnation: got %d; expected %d",
				tok.incarnation, baseIncarnation+2)
		}
	}
	// now that we've updated twice, read back the current value (which
	// should be the second one we wrote)
	finalEntry, finalReadErr := d.ReadCurrent(ctx)
	if finalReadErr != nil {
		t.Fatalf("failed to read \"existing\" entry: %s",
			finalReadErr)
	}
	if finalEntry.LeaderID != secondWrittenEntry.LeaderID {
		t.Errorf("unexpected leaderID: %q, expected %q",
			finalEntry.LeaderID, secondWrittenEntry.LeaderID)
	}
	if finalEntry.HostPort != secondWrittenEntry.HostPort {
		t.Errorf("unexpected HostPort: %q, expected %q",
			finalEntry.HostPort, secondWrittenEntry.HostPort)
	}
	if tdiff := finalEntry.TermExpiry.Sub(secondWrittenEntry.TermExpiry); tdiff != 0 {
		t.Errorf("unexpected TermExpiry time: %s, expected %s (difference %s)",
			finalEntry.TermExpiry, secondWrittenEntry.TermExpiry, tdiff)
	}
	if finalEntry.ElectionNumber != secondWrittenEntry.ElectionNumber {
		t.Errorf("unexpected ElectionNumber: %d, expected %d",
			finalEntry.ElectionNumber, secondWrittenEntry.ElectionNumber)
	}
	{
		tok := finalEntry.Token.(*token)
		if tok.self != d {
			t.Errorf("unexpected pointer value for \"self\": tok.self: %p, d: %p", tok.self, d)
		}
		if tok.incarnation != baseIncarnation+2 {
			t.Errorf("unexpected incarnation: got %d; expected %d",
				tok.incarnation, baseIncarnation+2)
		}
	}
}
