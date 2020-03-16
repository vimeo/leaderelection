package clocks

import (
	"context"
	"sync"
	"time"
)

// FakeClock implements the Clock interface, with helpful primitives for
// testing and skipping through timestamps without having to actually sleep in
// the test.
type FakeClock struct {
	mu      sync.Mutex
	current time.Time
	// sleepers contains a map from a channel on which that
	// sleeper is sleeping to a target-time. When time is advanced past a
	// sleeper's wakeup point, its channel should be closed and it should
	// be removed from the map.
	sleepers map[chan<- struct{}]time.Time
	// cond is broadcasted() upon any sleep or wakeup event.
	cond sync.Cond

	// counter tracking the number of wakeups (protected by mu)
	wakeups int
}

// NewFakeClock returns an initialized FakeClock instance.
func NewFakeClock(initialTime time.Time) *FakeClock {
	fc := FakeClock{
		current:  initialTime,
		sleepers: map[chan<- struct{}]time.Time{},
		cond:     sync.Cond{},
	}
	fc.cond.L = &fc.mu
	return &fc
}

// returns the number of sleepers awoken
func (f *FakeClock) setClockLocked(t time.Time) int {
	awoken := 0
	for ch, target := range f.sleepers {
		if target.Sub(t) <= 0 {
			close(ch)
			delete(f.sleepers, ch)
			awoken++
		}
	}
	f.wakeups += awoken
	f.current = t
	f.cond.Broadcast()
	return awoken
}

// SetClock skips the FakeClock to the specified time (forward or backwards)
func (f *FakeClock) SetClock(t time.Time) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setClockLocked(t)
}

// Advance skips the FakeClock forward by the specified duration (backwards if
// negative)
func (f *FakeClock) Advance(dur time.Duration) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.current.Add(dur)
	return f.setClockLocked(t)
}

// NumSleepers returns the number of goroutines waiting in SleepFor and SleepUntil
// calls.
func (f *FakeClock) NumSleepers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sleepers)
}

// Sleepers returns the number of goroutines waiting in SleepFor and SleepUntil
// calls.
func (f *FakeClock) Sleepers() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]time.Time, 0, len(f.sleepers))
	for _, t := range f.sleepers {
		out = append(out, t)
	}
	return out
}

// AwaitSleepers waits until the number of sleepers exceeds its argument
func (f *FakeClock) AwaitSleepers(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.sleepers) < n {
		f.cond.Wait()
	}
}

// Wakeups returns the number of sleepers that have been awoken (useful for
// verifying that nothing was woken up when advancing time)
func (f *FakeClock) Wakeups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wakeups
}

// Now implements Clock.Now(), returning the current time for this FakeClock.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current

}

// Until implements Clock.Now(), returning the time-difference between the
// timestamp argument and the current timestamp for the clock.
func (f *FakeClock) Until(t time.Time) time.Duration {
	return t.Sub(f.Now())
}

func (f *FakeClock) setAbsoluteWaiter(until time.Time) <-chan struct{} {
	ch := make(chan struct{})
	f.mu.Lock()
	defer f.mu.Unlock()
	if until.Sub(f.current) <= 0 {
		close(ch)
		return ch
	}
	f.sleepers[ch] = until

	f.cond.Broadcast()
	return ch
}

// SleepUntil blocks until either ctx expires or until arrives.
// Return value is false if context-cancellation/expiry prompted an
// early return
func (f *FakeClock) SleepUntil(ctx context.Context, until time.Time) bool {
	ch := f.setAbsoluteWaiter(until)
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

func (f *FakeClock) setRelativeWaiter(dur time.Duration) <-chan struct{} {
	ch := make(chan struct{})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleepers[ch] = f.current.Add(dur)
	f.cond.Broadcast()
	return ch
}

// SleepFor is the relative-time equivalent of SleepUntil.
func (f *FakeClock) SleepFor(ctx context.Context, dur time.Duration) bool {
	if dur <= 0 {
		return true
	}
	ch := f.setRelativeWaiter(dur)
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}
