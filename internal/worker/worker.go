package worker

import (
	"context"
	"sync"
	"time"

	"go-task-queue/internal/backoff"
	"go-task-queue/internal/dlq"
	"go-task-queue/internal/job"
	"go-task-queue/internal/logger"
	"go-task-queue/internal/queue"
)

// lg is the package logger; set from cmd via SetLogger after logger.SetDefault.
var lg *logger.Logger

// SetLogger assigns the logger used by this package (typically the same instance as main).
func SetLogger(l *logger.Logger) {
	lg = l
}

type Handler func(ctx context.Context, job *job.Job) error

type WorkerPool struct { // WorkerPool is a pool of workers that can run jobs
	ctx    context.Context
	cancel context.CancelFunc
	queue.Queue
	handlers        map[string]Handler // handler function for each job type
	numberOfWorkers int                 // number of workers to run
	wg              sync.WaitGroup

	// backoff is used when dequeueing jobs or running handlers fails.
	backoff *backoff.Exponential

	dlq dlq.DLQ
}

func NewWorkerPool(ctx context.Context, q queue.Queue, handlers map[string]Handler, numberOfWorkers int, d dlq.DLQ) *WorkerPool {
	return &WorkerPool{
		ctx:             ctx,
		Queue:           q,
		handlers:        handlers,
		numberOfWorkers: numberOfWorkers,
		wg:              sync.WaitGroup{},
		backoff:         backoff.NewExponential(100*time.Millisecond, 5*time.Second),
		dlq:             d,
	}
}

func (wp *WorkerPool) Start() {
	wp.ctx, wp.cancel = context.WithCancel(wp.ctx)
	for i := 0; i < wp.numberOfWorkers; i++ {
		wp.wg.Add(1)
		go wp.runWorker(i)
	}
}

func (wp *WorkerPool) runWorker(id int) {
	defer wp.wg.Done()

	var lastBackoff time.Duration

	lg.Info(logger.ClassWorker, "worker %d: starting", id)

	for {
		select {
		case <-wp.ctx.Done():
			lg.Info(logger.ClassWorker, "worker %d: context cancelled, exiting", id)
			return
		default:
			lg.Debug(logger.ClassWorker, "worker %d: attempting dequeue", id)
			j, err := wp.Dequeue(wp.ctx) // dequeue a job from the queue
			if err != nil {
				if wp.ctx.Err() != nil {
					lg.Debug(logger.ClassWorker, "worker %d: dequeue error after context cancel: %v", id, err)
					return
				}
				lg.Warn(logger.ClassWorker, "worker %d: dequeue error: %v; backing off", id, err)
				lastBackoff = wp.backoff.Next()
				if !backoff.Sleep(wp.ctx, lastBackoff) {
					lg.Debug(logger.ClassWorker, "worker %d: backoff sleep interrupted by context cancel", id)
					return
				}
				continue
			}
			if j == nil {
				lg.Debug(logger.ClassWorker, "worker %d: dequeue returned nil job; resetting backoff", id)
				wp.backoff.Reset()
				lastBackoff = 0
				continue
			}
			lg.Info(logger.ClassWorker, "worker %d: dequeued job id=%s type=%s", id, j.ID, j.Type)
			if h, ok := wp.handlers[j.Type]; ok { // execute the handler function for the job type
				lg.Info(logger.ClassJob, "worker %d: running handler for job id=%s type=%s", id, j.ID, j.Type)
				if err := h(wp.ctx, j); err != nil {
					lg.Error(logger.ClassJob, "worker %d: handler error for job id=%s: %v", id, j.ID, err)

					j.Attempt++
					_ = wp.UpdateAttempt(wp.ctx, j.ID, j.Attempt)
					_ = wp.UpdateLastError(wp.ctx, j.ID, err.Error())

					if j.MaxAttempts > 0 && j.Attempt >= j.MaxAttempts {
						_ = wp.UpdateStatus(wp.ctx, j.ID, job.StatusDeadLetter)
						if wp.dlq != nil {
							if err := wp.dlq.MoveToDLQ(wp.ctx, j, "max_attempts_exceeded"); err != nil {
								lg.Error(logger.ClassWorker, "worker %d: failed to move job id=%s to DLQ: %v", id, j.ID, err)
							} else {
								lg.Warn(logger.ClassWorker, "worker %d: moved job id=%s to DLQ after max attempts", id, j.ID)
							}
						}
					} else {
						j.Status = job.StatusPending
						_ = wp.UpdateStatus(wp.ctx, j.ID, job.StatusPending)
						_ = wp.Enqueue(wp.ctx, j)
					}

					lg.Warn(logger.ClassWorker, "worker %d: backing off after error for job id=%s", id, j.ID)
					lastBackoff = wp.backoff.Next()
					if !backoff.Sleep(wp.ctx, lastBackoff) {
						lg.Debug(logger.ClassWorker, "worker %d: backoff sleep interrupted by context cancel", id)
						return
					}
					continue
				}
				lg.Info(logger.ClassJob, "worker %d: handler completed successfully for job id=%s", id, j.ID)
				wp.backoff.Reset()
				lastBackoff = 0
			}
		}
	}
}

func (wp *WorkerPool) Stop() {
	if wp.cancel != nil {
		wp.cancel()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wp.wg.Wait()
	}()

	timeout := 5 * time.Second
	lg.Info(logger.ClassWorker, "worker pool: waiting up to %s for workers to stop", timeout)
	select {
	case <-done:
		lg.Info(logger.ClassWorker, "worker pool: all workers stopped")
	case <-time.After(timeout):
		lg.Warn(logger.ClassWorker, "worker pool: timeout waiting for workers; proceeding with shutdown")
	}
}

func (wp *WorkerPool) Dequeue(ctx context.Context) (*job.Job, error) {
	return wp.Queue.Dequeue(ctx)
}

func (wp *WorkerPool) Enqueue(ctx context.Context, j *job.Job) error {
	return wp.Queue.Enqueue(ctx, j)
}
