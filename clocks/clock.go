package clocks

import (
	clocks "github.com/vimeo/go-clocks"
)

// DefaultClock returns a clock that minimally wraps the `time` package
func DefaultClock() Clock {
	return clocks.DefaultClock()
}

// Clock is generally only used for testing, but could be used for userspace
// clock-synchronization as well.
type Clock = clocks.Clock
