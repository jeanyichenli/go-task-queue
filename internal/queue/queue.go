package queue

import (
	"context"
	"go-task-queue/internal/job"
	"time"
)

type Queue interface {
	Enqueue(ctx context.Context, job *job.Job) error
	Dequeue(ctx context.Context) (*job.Job, error)
	UpdateStatus(ctx context.Context, jobID string, status job.Status) error
	UpdateAttempt(ctx context.Context, jobID string, attempt int) error
	UpdateLastError(ctx context.Context, jobID string, lastError string) error
	UpdateCompletedAt(ctx context.Context, jobID string, completedAt time.Time) error
	UpdateStartedAt(ctx context.Context, jobID string, startedAt time.Time) error
	UpdatePriority(ctx context.Context, jobID string, priority int) error
	Close(ctx context.Context) error
	GetJob(ctx context.Context, jobID string) (*job.Job, error)
	ListJobs(ctx context.Context) ([]*job.Job, error)
	ListJobsByStatus(ctx context.Context, status job.Status) ([]*job.Job, error)
	ListJobsByType(ctx context.Context, t string) ([]*job.Job, error)
}
