package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go-task-queue/internal/job"

	"github.com/redis/go-redis/v9"
)

const (
	keyJobPrefix    = "job:"
	keyQueuePending = "queue:pending"
	keyStatusPrefix = "jobs:status:"
	keyTypePrefix   = "jobs:type:"
)

func jobKey(jobID string) string {
	return keyJobPrefix + jobID
}

// RedisQueue implements Queue using Redis (go-redis).
type RedisQueue struct {
	client *redis.Client
}

// NewRedisQueue creates a Redis-backed queue. Pass nil to use default options (localhost:6379).
func NewRedisQueue(opt *redis.Options) *RedisQueue {
	if opt == nil {
		opt = &redis.Options{Addr: "localhost:6379"}
	}
	return &RedisQueue{client: redis.NewClient(opt)}
}

// Enqueue adds a job to the pending queue.
func (q *RedisQueue) Enqueue(ctx context.Context, j *job.Job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("enqueue: marshal job: %w", err)
	}

	err = q.client.Set(ctx, jobKey(j.ID), data, 0).Err()
	if err != nil {
		return fmt.Errorf("enqueue: store job: %w", err)
	}

	err = q.client.LPush(ctx, keyQueuePending, j.ID).Err()
	if err != nil {
		return fmt.Errorf("enqueue: push id: %w", err)
	}

	return nil
}

// Dequeue removes and returns the next pending job (highest priority, then FIFO).
func (q *RedisQueue) Dequeue(ctx context.Context) (*job.Job, error) {
	results, err := q.client.BRPop(ctx, 0, keyQueuePending).Result()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, nil
		}
		return nil, fmt.Errorf("dequeue: brpop: %w", err)
	}

	// result[0] is the queue name, result[1] is the job ID
	jobID := results[1]

	// fetch the job from the Redis database
	data, err := q.client.Get(ctx, jobKey(jobID)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("dequeue: fetch job: %w", err)
	}

	// unmarshal the job
	var j job.Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("dequeue: unmarshal job: %w", err)
	}

	// update the job status and processed timestamp to running and now
	j.UpdatedAt = time.Now()
	j.Status = job.StatusRunning
	err = q.UpdateStatus(ctx, jobID, job.StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("dequeue: update status: %w", err)
	}

	return &j, nil
}

func (q *RedisQueue) getJobRaw(ctx context.Context, jobID string) ([]byte, error) {
	data, err := q.client.Get(ctx, keyJobPrefix+jobID).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return data, nil
}

// GetJob returns a job by ID.
func (q *RedisQueue) GetJob(ctx context.Context, jobID string) (*job.Job, error) {
	data, err := q.getJobRaw(ctx, jobID)
	if err != nil || data == nil {
		return nil, err
	}
	var j job.Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}
	return &j, nil
}

// UpdateStatus updates the job status and index sets.
func (q *RedisQueue) UpdateStatus(ctx context.Context, jobID string, status job.Status) error {
	data, err := q.getJobRaw(ctx, jobID)
	if err != nil || data == nil {
		return err
	}
	var j job.Job
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	j.Status = status

	return q.writeJobAndIndex(ctx, &j)
}

// UpdateAttempt sets the job attempt count.
func (q *RedisQueue) UpdateAttempt(ctx context.Context, jobID string, attempt int) error {
	return q.updateJobField(ctx, jobID, func(j *job.Job) { j.Attempt = attempt })
}

// UpdateLastError sets the job last error.
func (q *RedisQueue) UpdateLastError(ctx context.Context, jobID string, lastError string) error {
	return q.updateJobField(ctx, jobID, func(j *job.Job) { j.LastError = lastError })
}

// UpdateCompletedAt sets the job's completion time.
func (q *RedisQueue) UpdateCompletedAt(ctx context.Context, jobID string, completedAt time.Time) error {
	return q.updateJobField(ctx, jobID, func(j *job.Job) { j.UpdatedAt = completedAt })
}

// UpdateStartedAt sets the job's start time.
func (q *RedisQueue) UpdateStartedAt(ctx context.Context, jobID string, startedAt time.Time) error {
	return q.updateJobField(ctx, jobID, func(j *job.Job) { j.UpdatedAt = startedAt })
}

// UpdatePriority updates the job priority (does not reorder the pending queue).
func (q *RedisQueue) UpdatePriority(ctx context.Context, jobID string, priority int) error {
	return q.updateJobField(ctx, jobID, func(j *job.Job) { j.Priority = priority })
}

func (q *RedisQueue) updateJobField(ctx context.Context, jobID string, update func(*job.Job)) error {
	data, err := q.getJobRaw(ctx, jobID)
	if err != nil || data == nil {
		return err
	}
	var j job.Job
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	update(&j)
	return q.writeJobAndIndex(ctx, &j)
}

func (q *RedisQueue) writeJobAndIndex(ctx context.Context, j *job.Job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	err = q.client.Set(ctx, jobKey(j.ID), data, 0).Err()
	if err != nil {
		return fmt.Errorf("store job: %w", err)
	}

	return nil
}

// Close closes the Redis client.
func (q *RedisQueue) Close(ctx context.Context) error {
	return q.client.Close()
}

// ListJobs returns all jobs (keys under job:*). Use with care on large data.
func (q *RedisQueue) ListJobs(ctx context.Context) ([]*job.Job, error) {
	ids, err := q.listJobIDs(ctx)
	if err != nil {
		return nil, err
	}
	return q.getJobsByIDs(ctx, ids)
}

// ListJobsByStatus returns jobs with the given status.
func (q *RedisQueue) ListJobsByStatus(ctx context.Context, status job.Status) ([]*job.Job, error) {
	ids, err := q.client.SMembers(ctx, keyStatusPrefix+string(status)).Result()
	if err != nil {
		return nil, err
	}
	return q.getJobsByIDs(ctx, ids)
}

// ListJobsByType returns jobs with the given type.
func (q *RedisQueue) ListJobsByType(ctx context.Context, t string) ([]*job.Job, error) {
	ids, err := q.client.SMembers(ctx, keyTypePrefix+t).Result()
	if err != nil {
		return nil, err
	}
	return q.getJobsByIDs(ctx, ids)
}

// listJobIDs scans job:* keys and returns their IDs.
func (q *RedisQueue) listJobIDs(ctx context.Context) ([]string, error) {
	var ids []string
	var cursor uint64
	for {
		keys, next, err := q.client.Scan(ctx, cursor, keyJobPrefix+"*", 100).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			ids = append(ids, k[len(keyJobPrefix):])
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return ids, nil
}

func (q *RedisQueue) getJobsByIDs(ctx context.Context, ids []string) ([]*job.Job, error) {
	out := make([]*job.Job, 0, len(ids))
	for _, id := range ids {
		j, err := q.GetJob(ctx, id)
		if err != nil {
			return nil, err
		}
		if j != nil {
			out = append(out, j)
		}
	}
	return out, nil
}
