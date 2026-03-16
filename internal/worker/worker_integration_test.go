package worker

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"go-task-queue/internal/dlq"
	"go-task-queue/internal/job"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestWorkerPoolProcessesJobsIntegration(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue(10)

	var handled int32
	handlers := map[string]Handler{
		"ok": func(ctx context.Context, j *job.Job) error {
			atomic.AddInt32(&handled, 1)
			return nil
		},
	}

	wp := NewWorkerPool(ctx, q, handlers, 3, nil)
	wp.Start()
	defer wp.Stop()

	const jobsCount = 10
	for i := 0; i < jobsCount; i++ {
		j := &job.Job{
			ID:   fmt.Sprintf("job-%d", i),
			Type: "ok",
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

func TestWorkerPoolFailsToProcessJobsIntegration(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue(10)

	// Set up a real Mongo-backed DLQ using a test database/collection.
	mctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := mongo.Connect(mctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Fatalf("failed to connect to mongo: %v", err)
	}
	db := client.Database("go_task_queue_test_worker_dlq")
	coll := db.Collection("dlq_jobs_worker_test")
	if err := coll.Drop(mctx); err != nil {
		t.Fatalf("failed to drop test collection: %v", err)
	}
	dlqStore := dlq.NewMongoDLQWithCollection(coll)
	defer func() {
		_ = coll.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	}()

	var handlerCalls int32
	handlers := map[string]Handler{
		"always_fail": func(ctx context.Context, j *job.Job) error {
			atomic.AddInt32(&handlerCalls, 1)
			return fmt.Errorf("forced failure")
		},
	}

	wp := NewWorkerPool(ctx, q, handlers, 3, dlqStore)
	wp.Start()
	defer wp.Stop()

	const jobsCount = 1
	for i := 0; i < jobsCount; i++ {
		j := &job.Job{
			ID:   fmt.Sprintf("job-%d", i),
			Type: "always_fail",
			// Ensure the worker will immediately move the job to DLQ after first failure.
			MaxAttempts: 1,
		}
		if err := q.Enqueue(ctx, j); err != nil {
			t.Fatalf("failed to enqueue job: %v", err)
		}
	}

	// Wait until at least one job has exceeded MaxAttempts and been moved to DLQ.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("expected at least 1 job in DLQ, but timed out (handlerCalls=%d)", handlerCalls)
		case <-time.After(50 * time.Millisecond):
			// Count documents in the DLQ collection.
			count, err := coll.CountDocuments(context.Background(), struct{}{})
			if err != nil {
				t.Fatalf("CountDocuments error: %v", err)
			}
			if count > 0 {
				return
			}
		}
	}
}
