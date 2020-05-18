// Package leaderelection provides a simple to configure mechanism for electing
// a leader among processes.
//
// There are two real entrypoints within this package: Config.Acquire() and
// WatchConfig.Watch(). Config.Acquire() is used for acquiring leadership,
// while `WatchConfig.Watch` is for observing leadership transitions and
// status.
package leaderelection

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/vimeo/leaderelection/clocks"
	"github.com/vimeo/leaderelection/entry"
)

// RaceDecider is a storage backend that provides transactional semantics
type RaceDecider interface {
	// WriteEntry implementations should write the entry argument to
	// stable-storage in a transactional-way such that only one contender
	// wins per-election/term
	// The interface{} return value is a new token if the write succeeded.
	WriteEntry(ctx context.Context, entry *entry.RaceEntry) (entry.LeaderToken, error)
	// ReadCurrent should provide the latest version of RaceEntry available
	// and put any additional information needed to ensure transactional
	// behavior in the Token-field.
	ReadCurrent(ctx context.Context) (*entry.RaceEntry, error)
}

// TimeView is a value containing an atomically updatable time.Time
type TimeView struct {
	t     atomic.Value
	clock clocks.Clock
}

// NewTimeView constructs a TimeView
func NewTimeView(c clocks.Clock) *TimeView {
	if c == nil {
		c = clocks.DefaultClock()
	}
	now := c.Now()
	tv := TimeView{
		clock: c,
	}
	tv.Set(now)
	return &tv

}

// Clock returns the clocks.Clock instance against-which times are measured.
func (t *TimeView) Clock() clocks.Clock {
	return t.clock
}

// Get provides the current value of the encapsulated timestamp
func (t *TimeView) Get() time.Time {
	return t.t.Load().(time.Time)
}

// Set sets the current value of the encapsulated timestamp
// Exported so clients can change the values in tests
func (t *TimeView) Set(v time.Time) {
	t.t.Store(v)
}

// ValueInFuture compares the currently held timestamp against the current
// timestamp associated with the contained clock. (equivalent to still owning
// leadership as returned by Acquire)
func (t *TimeView) ValueInFuture() bool {
	return t.clock.Now().Before(t.Get())
}

// Config defines the common fields of configs for various leaderelection
// backend implementations.
type Config struct {
	// OnElected is called when the local instance wins an election
	// The context is cancelled when the lock is lost.
	// The expirationTime argument will contain the expiration time and its
	// contents will be updated as the term expiration gets extended.
	OnElected func(ctx context.Context, expirationTime *TimeView)
	// OnOusting is called when leadership is lost.
	OnOusting func(ctx context.Context)
	// LeaderChanged is called when another candidate becomes leader.
	LeaderChanged func(ctx context.Context, entry entry.RaceEntry)

	LeaderID string
	HostPort string

	// Decider is the RaceDecider implementation in use
	Decider RaceDecider

	// TermLength indicates how long a leader is allowed to hold a
	// leadership role before it expires (if it's not extended)
	// This must be at least 2x MaxClockSkew (preferably more).
	TermLength time.Duration

	// Maximum expected clock-skew, so sleeps, time-bounds are adjusted to
	// take this into account.
	MaxClockSkew time.Duration

	// ConnectionParams should be used as a side-channel for
	// leader-election metadata for the legrpc package, e.g. we use it for
	// storing the GRPC ServiceConfig (or nothing).
	ConnectionParams []byte

	// Clock implementation to use when scheduling sleeps, renewals and comparing leader-terms.
	// The nil-value falls back to a sane default implementation that simply wraps
	// the `time` package's functions.
	Clock clocks.Clock
}

// FailedAcquisitionErr types indicate that the error was non-fatal and most
// likely a result of someone else grabbing the lock before us
type FailedAcquisitionErr interface {
	error
	FailedAcquire()
}
