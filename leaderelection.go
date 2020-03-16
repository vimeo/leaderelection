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

// TimeView is a value containing a time.Time
type TimeView struct {
	t atomic.Value
}

// Get provides the current value of the encapsulated timestamp
func (t *TimeView) Get() time.Time {
	return t.t.Load().(time.Time)
}

// Set sets the current value of hte encapsulated timestamp
// Exported so clients can change the values in tests
func (t *TimeView) Set(v time.Time) {
	t.t.Store(v)
}

// Config defines the common fields of configs for various leaderelection
// backend implementations.
type Config struct {
	// OnElected is called when the local instance wins an election
	// The context is cancelled when the lock is lost.
	// The expirationTime argument will contain the expiration time and its
	// contents will be updated as the term expiration gets extended.
	OnElected     func(ctx context.Context, expirationTime *TimeView)
	OnOusting     func(ctx context.Context)
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
