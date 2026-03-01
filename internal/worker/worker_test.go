package worker

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"go-task-queue/internal/job"
	"go-task-queue/internal/queue"
)

// fakeQueue is a minimal in-memory implementation of queue.Queue used for tests.
type fakeQueue struct {
	jobs chan *job.Job
}

func newFakeQueue(buf int) *fakeQueue {
	return &fakeQueue{
		jobs: make(chan *job.Job, buf),
	}
}

func (f *fakeQueue) Enqueue(_ context.Context, j *job.Job) error {
	f.jobs <- j
	return nil
}

func (f *fakeQueue) Dequeue(ctx context.Context) (*job.Job, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case j, ok := <-f.jobs:
		if !ok {
			return nil, nil
		}
		return j, nil
	}
}

// The remaining methods satisfy the queue.Queue interface but are not used in these tests.

func (f *fakeQueue) UpdateStatus(context.Context, string, job.Status) error              { return nil }
func (f *fakeQueue) UpdateAttempt(context.Context, string, int) error                    { return nil }
func (f *fakeQueue) UpdateLastError(context.Context, string, string) error               { return nil }
func (f *fakeQueue) UpdateCompletedAt(context.Context, string, time.Time) error          { return nil }
func (f *fakeQueue) UpdateStartedAt(context.Context, string, time.Time) error            { return nil }
func (f *fakeQueue) UpdatePriority(context.Context, string, int) error                   { return nil }
func (f *fakeQueue) Close(context.Context) error                                         { return nil }
func (f *fakeQueue) GetJob(context.Context, string) (*job.Job, error)                    { return nil, nil }
func (f *fakeQueue) ListJobs(context.Context) ([]*job.Job, error)                        { return nil, nil }
func (f *fakeQueue) ListJobsByStatus(context.Context, job.Status) ([]*job.Job, error)    { return nil, nil }
func (f *fakeQueue) ListJobsByType(context.Context, string) ([]*job.Job, error)          { return nil, nil }

// Compile-time check that fakeQueue implements queue.Queue.
var _ queue.Queue = (*fakeQueue)(nil)

func TestWorkerPoolProcessesJobs(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue(10)

	var handled int32
	handlers := map[string]Handler{
		"test": func(ctx context.Context, j *job.Job) error {
			atomic.AddInt32(&handled, 1)
			return nil
		},
	}

	wp := NewWorkerPool(ctx, q, handlers, 3)
	wp.Start()
	defer wp.Stop()

	const jobsCount = 10
	for i := 0; i < jobsCount; i++ {
		j := &job.Job{
			ID:   fmt.Sprintf("job-%d", i),
			Type: "test",
		}
		if err := q.Enqueue(ctx, j); err != nil {
			t.Fatalf("failed to enqueue job: %v", err)
		}
	}

	deadline := time.After(2 * time.Second)
	for {
		if atomic.LoadInt32(&handled) == jobsCount {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected %d jobs handled, got %d", jobsCount, handled)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestWorkerPoolStopCancelsWorkers(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue(0)

	wp := NewWorkerPool(ctx, q, nil, 1)
	wp.Start()

	done := make(chan struct{})
	go func() {
		wp.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success: Stop returned, meaning workers observed cancellation and exited
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Stop did not return within timeout")
	}
}

