package dlq

import
(
	"context"
	"testing"
	"time"

	"go-task-queue/internal/job"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func setupTestDLQ(t *testing.T) (*MongoDLQ, func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Fatalf("failed to connect to mongo: %v", err)
	}

	db := client.Database("go_task_queue_test_dlq")
	coll := db.Collection("dlq_jobs_test")

	// Ensure collection is clean before each test.
	if err := coll.Drop(ctx); err != nil {
		t.Fatalf("failed to drop test collection: %v", err)
	}

	d := NewMongoDLQWithCollection(coll)

	cleanup := func() {
		_ = coll.Drop(context.Background())
		_ = client.Disconnect(context.Background())
		cancel()
	}

	return d, cleanup
}

func TestMoveToAndGetDLQJob(t *testing.T) {
	d, cleanup := setupTestDLQ(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()
	j := &job.Job{
		ID:          "job-1",
		Type:        "test-type",
		Payload:     map[string]any{"k": "v"},
		Status:      job.StatusDeadLetter,
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
		Attempt:     3,
		MaxAttempts: 3,
		LastError:   "boom",
	}

	if err := d.MoveToDLQ(ctx, j, "max_attempts_exceeded"); err != nil {
		t.Fatalf("MoveToDLQ error: %v", err)
	}

	got, err := d.GetDLQJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetDLQJob error: %v", err)
	}
	if got == nil {
		t.Fatalf("GetDLQJob returned nil")
	}

	if got.JobID != j.ID || got.Type != j.Type {
		t.Fatalf("unexpected DLQ job: %+v", got)
	}
	if got.LastError != j.LastError || got.Attempt != j.Attempt {
		t.Fatalf("unexpected error/attempt: %+v", got)
	}
	if got.Reason != "max_attempts_exceeded" {
		t.Fatalf("expected reason=max_attempts_exceeded, got %q", got.Reason)
	}
}

func TestListDLQJobsAndFilter(t *testing.T) {
	d, cleanup := setupTestDLQ(t)
	defer cleanup()

	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	jobs := []*job.Job{
		{
			ID:          "job-1",
			Type:        "type-a",
			Payload:     map[string]any{"i": 1},
			Status:      job.StatusDeadLetter,
			CreatedAt:   base,
			UpdatedAt:   base,
			Attempt:     1,
			MaxAttempts: 1,
			LastError:   "err1",
		},
		{
			ID:          "job-2",
			Type:        "type-b",
			Payload:     map[string]any{"i": 2},
			Status:      job.StatusDeadLetter,
			CreatedAt:   base.Add(10 * time.Minute),
			UpdatedAt:   base.Add(10 * time.Minute),
			Attempt:     2,
			MaxAttempts: 2,
			LastError:   "err2",
		},
	}

	for _, j := range jobs {
		if err := d.MoveToDLQ(ctx, j, "max_attempts_exceeded"); err != nil {
			t.Fatalf("MoveToDLQ error: %v", err)
		}
	}

	all, err := d.ListDLQJobs(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("ListDLQJobs error: %v", err)
	}
	if len(all) != len(jobs) {
		t.Fatalf("expected %d jobs, got %d", len(jobs), len(all))
	}

	filtered, err := d.ListDLQJobs(ctx, Filter{Type: "type-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListDLQJobs with type filter error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Type != "type-a" {
		t.Fatalf("expected one job of type-a, got %+v", filtered)
	}
}

func TestRequeueDLQJobUpdatesRequeuedAt(t *testing.T) {
	d, cleanup := setupTestDLQ(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()
	j := &job.Job{
		ID:          "job-1",
		Type:        "type-a",
		Payload:     map[string]any{"x": 1},
		Status:      job.StatusDeadLetter,
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
		Attempt:     2,
		MaxAttempts: 2,
		LastError:   "failed",
	}

	if err := d.MoveToDLQ(ctx, j, "max_attempts_exceeded"); err != nil {
		t.Fatalf("MoveToDLQ error: %v", err)
	}

	dj, err := d.RequeueDLQJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("RequeueDLQJob error: %v", err)
	}
	if dj == nil {
		t.Fatalf("RequeueDLQJob returned nil")
	}

	// Fetch again to see stored state including requeued_at.
	stored, err := d.GetDLQJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetDLQJob error: %v", err)
	}
	if stored == nil || stored.RequeuedAt == nil {
		t.Fatalf("expected RequeuedAt to be set, got %+v", stored)
	}
}

func TestDeleteDLQJobAndMetrics(t *testing.T) {
	d, cleanup := setupTestDLQ(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	j1 := &job.Job{
		ID:          "job-1",
		Type:        "type-a",
		Payload:     map[string]any{"x": 1},
		Status:      job.StatusDeadLetter,
		CreatedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:   now.Add(-2 * time.Minute),
		Attempt:     1,
		MaxAttempts: 1,
		LastError:   "err1",
	}
	j2 := &job.Job{
		ID:          "job-2",
		Type:        "type-b",
		Payload:     map[string]any{"x": 2},
		Status:      job.StatusDeadLetter,
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
		Attempt:     2,
		MaxAttempts: 2,
		LastError:   "err2",
	}

	if err := d.MoveToDLQ(ctx, j1, "max_attempts_exceeded"); err != nil {
		t.Fatalf("MoveToDLQ j1 error: %v", err)
	}
	if err := d.MoveToDLQ(ctx, j2, "max_attempts_exceeded"); err != nil {
		t.Fatalf("MoveToDLQ j2 error: %v", err)
	}

	metricsBefore, err := d.Metrics(ctx)
	if err != nil {
		t.Fatalf("Metrics before delete error: %v", err)
	}
	if metricsBefore.Total != 2 {
		t.Fatalf("expected total=2 before delete, got %d", metricsBefore.Total)
	}

	if err := d.DeleteDLQJob(ctx, j1.ID); err != nil {
		t.Fatalf("DeleteDLQJob error: %v", err)
	}

	metricsAfter, err := d.Metrics(ctx)
	if err != nil {
		t.Fatalf("Metrics after delete error: %v", err)
	}
	if metricsAfter.Total != 1 {
		t.Fatalf("expected total=1 after delete, got %d", metricsAfter.Total)
	}
}

