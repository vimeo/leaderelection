package clocks

import (
	"time"

	"github.com/vimeo/go-clocks/fake"
)

// FakeClock implements the Clock interface, with helpful primitives for
// testing and skipping through timestamps without having to actually sleep in
// the test. (wrapping github.com/vimeo/go-clocks/fake.Clock)
type FakeClock = fake.Clock

// NewFakeClock returns an initialized FakeClock instance.
func NewFakeClock(initialTime time.Time) *FakeClock {
	return fake.NewClock(initialTime)
}
