package clocks

import (
	"context"
	"time"
)

// OffsetClock wraps another clock, adjusting time intervals by a constant
// offset. (useful for simulating clock-skew)
type OffsetClock struct {
	inner  Clock
	offset time.Duration
}

// Now implements Clock, returning the current time (according to the captive
// clock adjusted by offset)
func (o *OffsetClock) Now() time.Time {
	return o.inner.Now().Add(o.offset)
}

// Until implements Clock, returning the difference between the current time
// and its argument (according to the captive clock adjusted by offset)
func (o *OffsetClock) Until(t time.Time) time.Duration {
	return o.inner.Until(t) + o.offset
}

// SleepUntil blocks until either ctx expires or until arrives.
// Return value is false if context-cancellation/expiry prompted an
// early return
func (o *OffsetClock) SleepUntil(ctx context.Context, until time.Time) bool {
	return o.inner.SleepUntil(ctx, until.Add(o.offset))
}

// SleepFor is the relative-time equivalent of SleepUntil. In the
// default implementation, this is the lower-level method, but other
// implementations may disagree.
func (o *OffsetClock) SleepFor(ctx context.Context, dur time.Duration) bool {
	// SleepFor is relative, so it doesn't need any adjustment
	return o.inner.SleepFor(ctx, dur)
}

// NewOffsetClock creates an OffsetClock and returns it
func NewOffsetClock(inner Clock, offset time.Duration) *OffsetClock {
	return &OffsetClock{
		inner:  inner,
		offset: offset,
	}
}
