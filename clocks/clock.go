package clocks

import (
	"context"
	"time"
)

type defaultClock struct{}

func (c defaultClock) Now() time.Time {
	return time.Now()
}

func (c defaultClock) Until(other time.Time) time.Duration {
	return time.Until(other)
}

func (c defaultClock) SleepUntil(ctx context.Context, until time.Time) bool {
	d := time.Until(until)
	return c.SleepFor(ctx, d)
}

func (c defaultClock) SleepFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	select {
	case <-t.C:
	case <-ctx.Done():
		if !t.Stop() {
			<-t.C
		}
		return false
	}
	return true
}

// DefaultClock returns a clock that minimally wraps the `time` package
func DefaultClock() Clock {
	return defaultClock{}
}

var _ Clock = defaultClock{}

// Clock is generally only used for testing, but could be used for userspace
// clock-synchronization as well.
type Clock interface {
	Now() time.Time
	Until(time.Time) time.Duration
	// SleepUntil blocks until either ctx expires or until arrives.
	// Return value is false if context-cancellation/expiry prompted an
	// early return
	SleepUntil(ctx context.Context, until time.Time) bool
	// SleepFor is the relative-time equivalent of SleepUntil. In the
	// default implementation, this is the lower-level method, but other
	// implementations may disagree.
	SleepFor(ctx context.Context, dur time.Duration) bool
}
