package leaderelection

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	clocks "github.com/vimeo/go-clocks"
	retry "github.com/vimeo/go-retry"

	"github.com/vimeo/leaderelection/entry"
)

var errAcquireFailed = errors.New("failed to acquire lock, lost race")

type campaign struct {
	c     Config
	clock clocks.Clock
}

// Acquire blocks until the context expires or is cancelled.
func (c Config) Acquire(ctx context.Context) error {

	if c.MaxClockSkew < 0 {
		return fmt.Errorf("MaxClockSkew (%s) is < 0; should be non-negative", c.MaxClockSkew)
	}
	if c.TermLength < 0 {
		return fmt.Errorf("TermLength (%s) is < 0; should be non-negative", c.TermLength)
	}
	if c.MaxClockSkew > 2*c.TermLength {
		return fmt.Errorf("incompatible values for MaxClockSkew (%s) and TermLength (%s); TermLength must be at least double MaxClockSkew (%s)",
			c.MaxClockSkew, c.TermLength, 2*c.MaxClockSkew)
	}

	if c.LeaderID == "" {
		return fmt.Errorf("missing LeaderID")
	}

	// Note that this method has a non-pointer receiver so we get a private
	// copy for use with the other methods, thus avoiding possible races
	// with the caller.
	lastEntry, initReadErr := c.Decider.ReadCurrent(ctx)
	if initReadErr != nil {
		return fmt.Errorf("failed to read initial state: %w", initReadErr)
	}

	b := retry.DefaultBackoff()

	cmp := campaign{c: c, clock: c.Clock}

	if c.Clock == nil {
		cmp.clock = clocks.DefaultClock()
	}

	wg := sync.WaitGroup{}
	defer wg.Wait()
	for {
		switch acquiredEntry, err := cmp.acquireOnce(ctx, lastEntry); err {
		case nil:
			b.Reset()
			if c.LeaderChanged != nil {
				wg.Add(1)
				go func(le entry.RaceEntry) {
					defer wg.Done()
					c.LeaderChanged(ctx, le)
				}(*lastEntry)
			}

			le, mgErr := cmp.manageWin(ctx, acquiredEntry, &wg)
			lastEntry = le
			if mgErr != nil {
				return mgErr
			}
			// If it failed because the context expired,
			// verify that the outer-context didn't expire.
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				// If it didn't expire, the
				// inner-refresh timed-out, which is
				// fine. We'll retry-acquisition on the
				// next-loop.
			}
		case errAcquireFailed:
			entry, readErr := c.Decider.ReadCurrent(ctx)
			switch {
			case readErr == nil:
				lastEntry = entry
				if c.LeaderChanged != nil {
					c.LeaderChanged(ctx, *entry)
				}
			case errors.Is(readErr, context.DeadlineExceeded), errors.Is(readErr, context.Canceled):
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					// the read failed with a context error, but our context wasn't canceled (it may
					// have been internal to the RaceDecider) Continue.
				}
			default:
				// failed to read with an error. just
				// fall-through and retry for now.
				// if the lastEntry is stale, the call to
				// WriteEntry should fail immediately.
			}
		default:
			return err
		}
		if lastEntry.ElectionNumber == entry.NoElections {
			// There haven't been any elections yet, just loop and
			// try again.
			if !cmp.clock.SleepFor(ctx, b.Next()) {
				return ctx.Err()
			}
			continue
		}

		// wait until a jittered fraction past the end of the current
		// term before trying to grab the lock.
		if !cmp.clock.SleepUntil(ctx, lastEntry.TermExpiry.Add(cmp.renewalExpirationJitter()+c.MaxClockSkew)) {
			return ctx.Err()
		}
	}
}

// jitterTermMaxFraction is the maximum fraction of the TermLength that an
// instance (without the leader lock) will sleep before trying to acquire the
// lock.
const jitterTermMaxFraction = 0.1

func (c *campaign) manageWin(ctx context.Context, winningEntry *entry.RaceEntry, wg *sync.WaitGroup) (*entry.RaceEntry, error) {
	// we won!
	tv := TimeView{clock: c.clock}
	tv.Set(winningEntry.TermExpiry)
	// run the elected callback in a background goroutine and cancel the
	// context on ousting
	electedCtx, electedCancel := context.WithCancel(ctx)
	// Run the ousting callback after we cancel the OnElected callback's
	// context.
	if c.c.OnOusting != nil {
		defer func() { wg.Add(1); go func() { defer wg.Done(); c.c.OnOusting(ctx) }() }()
	}
	defer electedCancel()
	wg.Add(1)
	go func(ctx context.Context) { defer wg.Done(); c.c.OnElected(ctx, &tv) }(electedCtx)
	finalEntry, refreshErr := c.refreshLock(ctx, winningEntry, &tv)
	// We've lost the lock. We must face the future and
	// acknowledge our failures.
	switch refreshErr {
	case nil, errAcquireFailed:
		if remainingTime := c.clock.Until(finalEntry.TermExpiry); remainingTime > 0 {
			c.clock.SleepUntil(ctx, finalEntry.TermExpiry)
		}
		return finalEntry, nil
	case context.DeadlineExceeded, context.Canceled:
	default:
		return finalEntry, fmt.Errorf("failed to refresh lock: %w", refreshErr)
	}
	return finalEntry, nil
}

func (c *campaign) renewalExpirationJitter() time.Duration {
	f := rand.Float64()
	ns := float64(c.c.TermLength.Nanoseconds()) * jitterTermMaxFraction * f
	return time.Duration(ns) * time.Nanosecond
}

func (c *campaign) acquireOnce(ctx context.Context, lastEntry *entry.RaceEntry) (*entry.RaceEntry, error) {
	now := c.clock.Now()
	termBegin := lastEntry.TermExpiry
	if lastEntry.ElectionNumber == entry.NoElections || termBegin.Before(now) {
		termBegin = now
	} else {
		// if the current term hasn't expired yet, wait for it to (with
		// some jitter and the max clock-skew).
		if !c.clock.SleepUntil(ctx, termBegin.Add(c.renewalExpirationJitter()+c.c.MaxClockSkew)) {
			return nil, ctx.Err()
		}
	}
	newEntry := entry.RaceEntry{
		LeaderID:         c.c.LeaderID,
		HostPort:         c.c.HostPort,
		TermExpiry:       termBegin.Add(c.c.TermLength),
		ElectionNumber:   lastEntry.ElectionNumber + 1,
		Token:            lastEntry.Token,
		ConnectionParams: c.c.ConnectionParams,
	}

	// note: the timeout here is using the time.Time clock (fine most of the time; bad for tests)
	writeCtx, writeCtxCancel := context.WithTimeout(ctx, c.clock.Until(newEntry.TermExpiry)-
		c.c.MaxClockSkew)
	defer writeCtxCancel()

	newTok, writeErr := c.c.Decider.WriteEntry(writeCtx, &newEntry)
	switch writeErr.(type) {
	case nil:
		newEntry.Token = newTok
		return &newEntry, nil
	case FailedAcquisitionErr:
		return nil, errAcquireFailed
	default:
		// Check whether this is the result of hitting a deadline/context-cancellation.
		switch {
		case errors.Is(writeErr, context.DeadlineExceeded), errors.Is(writeErr, context.Canceled):
			select {
			case <-ctx.Done():
				return nil, writeErr
			default:
				// if it's a deadline, that isn't ours, just say that we've failed to acquire the lock.
				return nil, errAcquireFailed
			}
		default:
			return nil, fmt.Errorf("failed to write entry: %w", writeErr)
		}
	}
}

func (c *campaign) refreshLock(ctx context.Context, electedEntry *entry.RaceEntry, tv *TimeView) (*entry.RaceEntry, error) {
	entryVal := *electedEntry
	entry := &entryVal
	// we have the lock, we should try to keep it
	for {
		newEntry, refreshErr := c.refreshLockOnce(ctx, entry)
		switch refreshErr {
		case nil:
			tv.Set(newEntry.TermExpiry)
			entry = newEntry
		case context.DeadlineExceeded, context.Canceled, errAcquireFailed:
			return entry, refreshErr
		default:
		}

		// wake up in half the interval until the leadership term
		// expires (minus the max-clock-skew)
		if !c.clock.SleepFor(ctx, c.clock.Until(entry.TermExpiry)/2-c.c.MaxClockSkew) {
			return entry, ctx.Err()
		}
	}
}

func (c *campaign) refreshLockOnce(ctx context.Context, rentry *entry.RaceEntry) (*entry.RaceEntry, error) {
	// we have the lock, we should try to keep it
	newEntry := entry.RaceEntry{
		LeaderID:   c.c.LeaderID,
		HostPort:   c.c.HostPort,
		TermExpiry: c.clock.Now().Add(c.c.TermLength),
		// just extending the term
		ElectionNumber:   rentry.ElectionNumber + 1,
		Token:            rentry.Token,
		ConnectionParams: c.c.ConnectionParams,
	}

	// no point in letting this renewal extend past the end of the term.
	// (subtract off MaxClockSkew because at that point someone may
	// consider the term over already)
	// Note that this assumes that our clock advances at something
	// resembling the real clock used by the time package. (not necessarily valid in tests)
	writeCtx, writeCtxCancel := context.WithTimeout(ctx, c.clock.Until(newEntry.TermExpiry)-c.c.MaxClockSkew)
	defer writeCtxCancel()

	newTok, writeErr := c.c.Decider.WriteEntry(writeCtx, &newEntry)
	switch writeErr.(type) {
	case nil:
		newEntry.Token = newTok
		return &newEntry, nil
	case FailedAcquisitionErr:
		return nil, errAcquireFailed
	default:
		return nil, writeErr
	}
}
