package backoff

import (
	"context"
	"time"
)

// Exponential implements a simple exponential backoff strategy.
// It is safe to use from a single goroutine.
type Exponential struct {
	Base time.Duration
	Max  time.Duration

	cur time.Duration
}

// NewExponential creates a new exponential backoff with the given base and max.
// If base is <= 0, no backoff is applied. If max is <= 0, it is treated as
// unbounded (i.e. doubles without a cap).
func NewExponential(base, max time.Duration) *Exponential {
	return &Exponential{
		Base: base,
		Max:  max,
	}
}

// Next returns the next backoff duration, growing exponentially from Base
// until it reaches Max. If Base <= 0, this always returns 0.
func (b *Exponential) Next() time.Duration {
	if b.Base <= 0 {
		return 0
	}
	if b.cur <= 0 {
		b.cur = b.Base
		return b.cur
	}

	next := b.cur * 2
	if b.Max > 0 && next > b.Max {
		next = b.Max
	}
	b.cur = next
	return b.cur
}

// Reset sets the backoff sequence back to the initial state.
func (b *Exponential) Reset() {
	b.cur = 0
}

// Sleep waits for the given duration or until ctx is cancelled.
// It returns false if the context was cancelled.
func Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

