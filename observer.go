package leaderelection

import (
	"context"
	"fmt"
	"sync"
	"time"

	clocks "github.com/vimeo/go-clocks"
	retry "github.com/vimeo/go-retry"

	"github.com/vimeo/leaderelection/entry"
)

// WatchConfig configures the watcher
type WatchConfig struct {
	// Decider used to lookup the current leader
	Decider RaceDecider
	// Clock implementation to use when scheduling sleeps and comparing leader-terms.
	// The nil-value falls back to a sane default implementation that simply wraps
	// the `time` package's functions.
	Clock clocks.Clock
}

// Watch provides a way for an observer to watch for changes to the identity of
// the current leader
func (w WatchConfig) Watch(ctx context.Context, cb func(ctx context.Context, entry entry.RaceEntry)) error {
	b := retry.DefaultBackoff()
	// Use the default backoff, but bump the minimum because we're likely
	// waiting much longer than a few ms for a new leader when we do
	// backoff.
	b.MinBackoff = time.Second
	// Track the latest election number and term expiration times,
	// initializing to empty values.
	highestElection := entry.NoElections
	latestTermEnd := time.Time{}
	// Set an insanely short initial lease duration interval estimate
	// (it'll get clobbered by the first lease-extension we find).
	leaseIntervalEstimate := time.Millisecond * 20

	clock := w.Clock
	if clock == nil {
		clock = clocks.DefaultClock()
	}

	cbwg := sync.WaitGroup{}
	defer cbwg.Wait()

	cbRunCh := make(chan *entry.RaceEntry, 128)
	defer close(cbRunCh)
	cbwg.Add(1)
	go func() {
		defer cbwg.Done()
		for e := range cbRunCh {
			cb(ctx, *e)
		}
	}()
	for {
		latestEntry, initReadErr := w.Decider.ReadCurrent(ctx)
		if initReadErr != nil {
			return fmt.Errorf("failed to do initial read of values: %w", initReadErr)
		}
		if highestElection == latestEntry.ElectionNumber && latestTermEnd.Before(latestEntry.TermExpiry) {
			extendedBy := latestEntry.TermExpiry.Sub(latestTermEnd)
			leaseIntervalEstimate = extendedBy * 2
		}
		if highestElection <= latestEntry.ElectionNumber || latestTermEnd.Before(latestEntry.TermExpiry) {
			cbRunCh <- latestEntry

			highestElection = latestEntry.ElectionNumber
			latestTermEnd = latestEntry.TermExpiry
		}
		// Set the next wakeup for either a bit before the estimated
		// time of a lease-extension or the next backoff interval from
		// the retry package's Backoff object
		// subtracting 3/4ths of the estimated interval so we wake up
		// roughly twice per renewal.
		nextPoll := clock.Until(latestEntry.TermExpiry) - ((leaseIntervalEstimate * 3) / 4)
		if nextPoll > 0 {
			b.Reset()
		} else {
			nextPoll = b.Next()
		}
		if !clock.SleepFor(ctx, nextPoll) {
			return ctx.Err()
		}
	}
}
