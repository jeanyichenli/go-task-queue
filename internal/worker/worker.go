package worker

import (
	"context"
	"go-task-queue/internal/job"
	"go-task-queue/internal/queue"
	"sync"
)

type Handler func(ctx context.Context, job *job.Job) error

type WorkerPool struct { // WorkerPool is a pool of workers that can run jobs
	ctx    context.Context
	cancel context.CancelFunc
	queue.Queue
	handlers        map[string]Handler // handler function for each job type
	numberOfWorkers int                // number of workers to run
	wg              sync.WaitGroup
}

func NewWorkerPool(ctx context.Context, q queue.Queue, handlers map[string]Handler, numberOfWorkers int) *WorkerPool {
	return &WorkerPool{
		ctx:             ctx,
		Queue:           q,
		handlers:        handlers,
		numberOfWorkers: numberOfWorkers,
		wg:              sync.WaitGroup{},
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
				continue
			}
			if j == nil {
				continue
			}
			if h, ok := wp.handlers[j.Type]; ok { // execute the handler function for the job type
				_ = h(wp.ctx, j)
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
