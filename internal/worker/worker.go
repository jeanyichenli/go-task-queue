package worker

import (
	"context"
	"go-task-queue/internal/backoff"
	"go-task-queue/internal/job"
	"go-task-queue/internal/queue"
	"sync"
	"time"
)

type Handler func(ctx context.Context, job *job.Job) error

type WorkerPool struct { // WorkerPool is a pool of workers that can run jobs
	ctx    context.Context
	cancel context.CancelFunc
	queue.Queue
	handlers        map[string]Handler // handler function for each job type
	numberOfWorkers int                // number of workers to run
	wg              sync.WaitGroup

	// backoff is used when dequeueing jobs or running handlers fails.
	backoff *backoff.Exponential
}

func NewWorkerPool(ctx context.Context, q queue.Queue, handlers map[string]Handler, numberOfWorkers int) *WorkerPool {
	return &WorkerPool{
		ctx:             ctx,
		Queue:           q,
		handlers:        handlers,
		numberOfWorkers: numberOfWorkers,
		wg:              sync.WaitGroup{},
		backoff:         backoff.NewExponential(100*time.Millisecond, 5*time.Second),
	}
}

func (wp *WorkerPool) Start() {
	wp.ctx, wp.cancel = context.WithCancel(wp.ctx)
	for i := 0; i < wp.numberOfWorkers; i++ {
		wp.wg.Add(1)
		go wp.runWorker()
	}
}

func (wp *WorkerPool) runWorker() {
	defer wp.wg.Done()

	var lastBackoff time.Duration

	for {
		select {
		case <-wp.ctx.Done():
			return
		default:
			j, err := wp.Dequeue(wp.ctx) // dequeue a job from the queue
			if err != nil {
				if wp.ctx.Err() != nil {
					return
				}
				// transient dequeue error: apply exponential backoff before retrying.
				lastBackoff = wp.backoff.Next()
				if !backoff.Sleep(wp.ctx, lastBackoff) {
					return
				}
				continue
			}
			if j == nil {
				// Not an error condition, just no work; reset backoff.
				wp.backoff.Reset()
				lastBackoff = 0
				continue
			}
			if h, ok := wp.handlers[j.Type]; ok { // execute the handler function for the job type
				if err := h(wp.ctx, j); err != nil {
					// Handler error: update retry metadata and optionally re-enqueue.

					// Increment attempt count and persist it along with the last error.
					j.Attempt++
					_ = wp.UpdateAttempt(wp.ctx, j.ID, j.Attempt)
					_ = wp.UpdateLastError(wp.ctx, j.ID, err.Error())

					// If we've hit the maximum number of attempts (when set), mark
					// the job as failed and do not re-enqueue it.
					if j.MaxAttempts > 0 && j.Attempt >= j.MaxAttempts {
						_ = wp.UpdateStatus(wp.ctx, j.ID, job.StatusFailed)
					} else {
						// Otherwise, move the job back to pending and re-enqueue
						// it for another attempt later.
						j.Status = job.StatusPending
						_ = wp.UpdateStatus(wp.ctx, j.ID, job.StatusPending)
						_ = wp.Enqueue(wp.ctx, j)
					}

					// Back off before fetching the next job to avoid tight retry loops.
					lastBackoff = wp.backoff.Next()
					if !backoff.Sleep(wp.ctx, lastBackoff) {
						return
					}
					continue
				}
				// successful execution: reset backoff sequence.
				wp.backoff.Reset()
				lastBackoff = 0
			}
		}
	}
}

// Stop gracefully shuts down the pool: it signals all workers to stop
// and blocks until they have finished (e.g. after completing in-flight jobs).
func (wp *WorkerPool) Stop() {
	if wp.cancel != nil {
		wp.cancel()
	}
	wp.wg.Wait()
}

func (wp *WorkerPool) Dequeue(ctx context.Context) (*job.Job, error) {
	return wp.Queue.Dequeue(ctx)
}

func (wp *WorkerPool) Enqueue(ctx context.Context, j *job.Job) error {
	return wp.Queue.Enqueue(ctx, j)
}
