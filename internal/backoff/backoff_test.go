package backoff

import (
	"context"
	"testing"
	"time"
)

func TestExponentialNextAndReset(t *testing.T) {
	b := NewExponential(100*time.Millisecond, 800*time.Millisecond)

	// First call should return base.
	if d := b.Next(); d != 100*time.Millisecond {
		t.Fatalf("expected first backoff to be 100ms, got %v", d)
	}

	// Subsequent calls should double until capped by Max.
	expected := []time.Duration{
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		800 * time.Millisecond, // capped at Max
	}

	for i, want := range expected {
		if d := b.Next(); d != want {
			t.Fatalf("step %d: expected %v, got %v", i, want, d)
		}
	}

	// After Reset, sequence should start from base again.
	b.Reset()
	if d := b.Next(); d != 100*time.Millisecond {
		t.Fatalf("after reset, expected 100ms, got %v", d)
	}
}

func TestExponentialNoBackoffWhenBaseNonPositive(t *testing.T) {
	b := NewExponential(0, 0)
	for i := 0; i < 3; i++ {
		if d := b.Next(); d != 0 {
			t.Fatalf("expected 0 backoff when base <= 0, got %v", d)
		}
	}
}

func TestSleepRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Even with a long duration, Sleep should return false when context is cancelled.
	if ok := Sleep(ctx, 500*time.Millisecond); ok {
		t.Fatalf("expected Sleep to return false when context is cancelled")
	}
}

func TestSleepZeroDurationReturnsImmediately(t *testing.T) {
	ctx := context.Background()

	start := time.Now()
	if ok := Sleep(ctx, 0); !ok {
		t.Fatalf("expected Sleep to return true for zero duration")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("expected Sleep(0) to return immediately, took %v", elapsed)
	}
}

