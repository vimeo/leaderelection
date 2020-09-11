package clocks

import (
	"time"

	"github.com/vimeo/go-clocks/offset"
)

// OffsetClock wraps another clock, adjusting time intervals by a constant
// offset. (useful for simulating clock-skew)
// (forwarding alias for github.com/vimeo/go-clocks/offset.Clock
type OffsetClock = offset.Clock

// NewOffsetClock creates an OffsetClock and returns it
func NewOffsetClock(inner Clock, timeOffset time.Duration) *OffsetClock {
	return offset.NewOffsetClock(inner, timeOffset)
}
