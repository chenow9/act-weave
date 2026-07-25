package outboundidentity

import "time"

// Clock abstracts time for deterministic vault tests. Production uses WallClock.
//
// Memory safety note: Go's garbage collector may retain copies of byte slices
// after zeroing; the vault still overwrites plaintext on cleanup but cannot
// guarantee absolute erasure. Deployments must keep short TTLs, disable heap
// dumps by default, and isolate processes that hold Vault entries.
type Clock interface {
	Now() time.Time
}

// WallClock uses time.Now().UTC().
type WallClock struct{}

func (WallClock) Now() time.Time { return time.Now().UTC() }

// FakeClock is a mutable clock for unit tests.
type FakeClock struct {
	current time.Time
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{current: start.UTC()}
}

func (c *FakeClock) Now() time.Time {
	if c == nil {
		return time.Now().UTC()
	}
	return c.current
}

func (c *FakeClock) Advance(d time.Duration) {
	if c == nil {
		return
	}
	c.current = c.current.Add(d)
}

func (c *FakeClock) Set(t time.Time) {
	if c == nil {
		return
	}
	c.current = t.UTC()
}
