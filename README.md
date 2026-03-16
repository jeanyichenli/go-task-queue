<!-- @format -->

## Go Task Queue

This project is a small experimental task-queue service written in Go. It is built around three core internal concepts:

- **Jobs**: units of work to be processed.
- **Queues**: responsible for persisting and dispatching jobs.
- **Worker pools**: concurrent workers that pull jobs from the queue and execute handlers with exponential back-off on errors. Multiple workers can run in parallel, handling different job types via a type-to-handler map.

<!-- This README documents the current internal workflow. Public APIs and service wiring (HTTP endpoints, CLI, etc.) will be added later. -->

---

## Job model

Jobs are defined in `internal/job/job.go`.

- **Fields**
  - `ID string`: unique job identifier.
  - `Type string`: job type, used to route to a handler.
  - `Payload map[string]any`: arbitrary job-specific data.
  - `Status job.Status`: lifecycle status, one of:
    - `pending`
    - `running`
    - `completed`
    - `dead_letter`
  - `CreatedAt`, `UpdatedAt time.Time`: timestamps for bookkeeping.
  - `Attempt int`: how many times this job has been attempted.
  - `MaxAttempts int`: maximum number of attempts allowed.
  - `LastError string`: last error message, if any.

Jobs are serialized to JSON and stored in the backing queue (Redis).

---

## Queue abstraction and Redis implementation

The queue abstraction is defined in `internal/queue/queue.go` as the `Queue` interface:

- **Core methods**
  - `Enqueue(ctx, *job.Job) error`: store a job and push its ID onto the pending queue.
  - `Dequeue(ctx) (*job.Job, error)`: block until a job ID is available, load the job, and mark its status as `running`.
  - `UpdateStatus(ctx, jobID, status) error`: update job status.
  - `UpdateAttempt(ctx, jobID, attempt) error`
  - `UpdateLastError(ctx, jobID, lastError) error`: preserve the final round running error messages.
  - `UpdateCompletedAt(ctx, jobID, completedAt) error`
  - `UpdateStartedAt(ctx, jobID, startedAt) error`
  - `Close(ctx) error`: close underlying resources.
  - `GetJob(ctx, jobID) (*job.Job, error)`: fetch a single job with unique job ID.
  - `ListJobs(ctx) ([]*job.Job, error)`: return all jobs.
  - `ListJobsByStatus(ctx, status) ([]*job.Job, error)`
  - `ListJobsByType(ctx, t string) ([]*job.Job, error)`

The concrete Redis-backed implementation lives in `internal/queue/redis.go`:

- Uses `github.com/redis/go-redis/v9`.
- Stores jobs under keys like `job:<id>`.
- Uses a Redis list `queue:pending` for pending job IDs and `BRPOP` to block until a job is available.
- Maintains basic indexes for status and type via Redis sets.

---

## Worker pool and handlers

Worker logic is in `internal/worker/worker.go`.

- **Handler**
  - `type Handler func(ctx context.Context, job *job.Job) error`
  - Callers register a map `map[string]Handler` keyed by job type.

- **WorkerPool**
  - Holds:
    - a base context and cancel function,
    - a `queue.Queue` implementation,
    - registered handlers,
    - a configurable number of workers,
    - a `sync.WaitGroup` to wait for workers,
    - an exponential back-off instance (see below).
  - `Start()`:
    - Derives a cancellable context.
    - Spins up `numberOfWorkers` goroutines, each running `runWorker`. At startup the service enforces:
      - `WORKERS` is at least `1`.
      - If `WORKERS` is less than the number of configured handler types (from `config/handlers.json`), it is automatically increased to match the number of handler types, and a warning is logged.
  - `Stop()`:
    - Cancels the context and waits for all workers to finish.
  - `runWorker()`:
    - Loops until the context is cancelled.
    - Dequeues a job from the queue.
    - On non-fatal errors or handler failures, it applies exponential back-off before retrying.
    - On success or when there is simply no job to process, it resets the back-off.

---

## Exponential back-off module

Exponential back-off utilities live in `internal/backoff/backoff.go`.

- **Exponential**
  - `NewExponential(base, max time.Duration) *Exponential`
  - `Next() time.Duration`: doubles from `base` until `max` (if `max > 0`), then stays capped.
  - `Reset()`: resets the internal state so the next call to `Next()` returns the base duration again.

- **Sleep**
  - `Sleep(ctx context.Context, d time.Duration) bool`:
    - Sleeps for duration `d` or returns early if the context is cancelled.
    - Returns `false` when the context is cancelled, `true` otherwise.

The `WorkerPool` uses `Exponential` plus `Sleep` together with per-job retry
metadata (`Attempt`, `MaxAttempts`, `LastError`) to control retries:

- Back-off when `Dequeue` returns an error (for example, a transient Redis problem).
- When a handler returns an error:
  - Increment the job's `Attempt` counter and persist `LastError`.
  - If `MaxAttempts > 0` and `Attempt >= MaxAttempts`, mark the job as `dead_letter` and move it to the Dead Letter Queue.
  - Otherwise, set the status back to `pending` and re-enqueue the job for another try.
  - In all error cases, apply exponential back-off before fetching the next job, to avoid tight retry loops.
- Reset the back-off after successful processing or when no jobs are available.

---

## Running the service

The repository now includes a runnable service binary in `cmd/go-task-queue`.

### Build

```bash
go build ./cmd/go-task-queue
```

### Configure

Configuration is loaded from **environment variables** first. **Command-line flags** override those values for the same settings (call `flag.Parse()` after flags are bound).

#### Environment variables

| Variable               | Meaning                                                                                                                                                                                                                                                         | Default                     |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| `REDIS_ADDR`           | Redis server address (`host:port`).                                                                                                                                                                                                                             | `localhost:6379`            |
| `WORKERS`              | Number of worker goroutines that dequeue and run jobs. Must be a positive integer.                                                                                                                                                                              | `4`                         |
| `HTTP_ADDR`            | HTTP listen address for the API (e.g. `:8080`).                                                                                                                                                                                                                 | `:8080`                     |
| `HANDLERS_CONFIG`      | Path to the JSON file that defines job type → handler config (targets, timeouts, max attempts).                                                                                                                                                                 | `config/handlers.json`      |
| `LOG_LEVEL`            | Minimum log level: `debug`, `info`, `warn`, or `error`. Messages below this level are omitted (`debug` is most verbose).                                                                                                                                        | `info`                      |
| `LOG_CLASS`            | Comma-separated log **classes** to allow, or empty for **all**. Classes: `cmd` (startup/shutdown), `api` (HTTP), `worker` (pool/dequeue), `queue` (Redis queue ops), `job` (handlers), `system` (e.g. Redis client). Example: `worker,job` only logs those two. | _(empty — all classes)_     |
| `MONGODB_URI`          | MongoDB connection string used for DLQ storage and structured logging.                                                                                                                                                                                          | `mongodb://localhost:27017` |
| `MONGO_DB`             | MongoDB database name.                                                                                                                                                                                                                                          | `go_task_queue`             |
| `MONGO_DLQ_COLLECTION` | Mongo collection name for DLQ jobs.                                                                                                                                                                                                                             | `dlq_jobs`                  |
| `MONGO_LOG_COLLECTION` | Mongo collection name for log entries.                                                                                                                                                                                                                          | `log_entries`               |

#### Flags (override env)

| Flag                | Meaning                    | Default (if unset)      |
| ------------------- | -------------------------- | ----------------------- |
| `--redis-addr`      | Same as `REDIS_ADDR`.      | `localhost:6379`        |
| `--workers`         | Same as `WORKERS`.         | `4`                     |
| `--http-addr`       | Same as `HTTP_ADDR`.       | `:8080`                 |
| `--handlers-config` | Same as `HANDLERS_CONFIG`. | `config/handlers.json`  |
| `--log-level`       | Same as `LOG_LEVEL`.       | `info`                  |
| `--log-class`       | Same as `LOG_CLASS`.       | _(empty — all classes)_ |

### Log line format

Each line is UTC RFC3339 nano, level, class, then message, for example:

```text
2025-03-13T12:00:00.123456789Z INFO  [cmd] starting go-task-queue service (redis=...)
2025-03-13T12:00:00.2Z       INFO  [worker] worker 0: dequeued job id=... type=echo
```

### Run

Example:

```bash
REDIS_ADDR=localhost:6379 \
WORKERS=4 \
HTTP_ADDR=:8080 \
MONGODB_URI=mongodb://localhost:27017 \
./go-task-queue
```

This will:

- Connect to Redis.
- Connect to MongoDB for DLQ storage and log persistence.
- Start a `WorkerPool` with the configured number of workers and handlers from `config/handlers.json`.
- Start an HTTP server on the configured address.

### HTTP API

HTTP endpoints are provided by `internal/httpapi/httpapi.go`:

- `GET /health`: basic health check, returns `200 OK` with body `ok`.
- `POST /jobs`: enqueue a new job.
  - Request JSON body:
    - `type` (string, required): job type (e.g. `"echo"` or `"always_fail"`).
    - `payload` (object, optional): arbitrary JSON payload.
    - `max_attempts` (int, optional): maximum retries before moving to the DLQ (overrides handler config).
  - Response JSON:
    - `id`: job ID.
    - `status`: initial status (`pending`).
- `GET /jobs`: list all jobs (uses `ListJobs`).
- `GET /jobs/{id}`: get a single job by ID, returns `404` if not found.
- `GET /dlq/jobs`: list DLQ jobs, with optional query params:
  - `type`: filter by job type.
  - `limit`: maximum number of jobs to return (default 50).
- `GET /dlq/jobs/{id}`: get a single DLQ job by original job ID.
- `POST /dlq/jobs/{id}/requeue`: mark a DLQ job as requeued (and, in future, re-enqueue it).
- `DELETE /dlq/jobs/{id}`: delete a DLQ job document.
- `GET /dlq/metrics`: summary DLQ metrics (total, by type, oldest/newest).

Example usage:

```bash
# Normal job that should complete successfully via the "echo" handler
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{"type":"echo","payload":{"msg":"hello"}}'

curl http://localhost:8080/jobs
curl http://localhost:8080/jobs/<job_id>

# Job that always fails and eventually ends up in the DLQ
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{"type":"always_fail","payload":{"msg":"bad"},"max_attempts":2}'

# After a short delay, inspect DLQ contents
curl http://localhost:8080/dlq/jobs
curl http://localhost:8080/dlq/jobs/<job_id>
curl http://localhost:8080/dlq/metrics
```

## Experiments: normal vs DLQ conditions

- **Normal condition (`echo` handler)**:
  - Start the service as shown above.
  - Enqueue an `echo` job:
    - `POST /jobs` with `{"type":"echo","payload":{"msg":"ok"}}`.
  - Observe:
    - `/jobs/{id}` status transitions from `pending` → `running` → `completed`.
    - `/dlq/jobs` remains empty.

- **DLQ condition (`always_fail` handler)**:
  - `config/handlers.json` includes a handler of type `always_fail` that always returns an error.
  - Enqueue a failing job:
    - `POST /jobs` with `{"type":"always_fail","payload":{"msg":"bad"},"max_attempts":2}`.
  - Observe:
    - The worker retries up to `max_attempts`, then marks the job as `dead_letter`.
    - A corresponding document appears in MongoDB’s DLQ collection.
    - `/dlq/jobs` lists the job, `/dlq/jobs/{id}` shows details (attempts, last error, timestamps).
    - `/dlq/metrics` shows the job counted under its type.
    - `POST /dlq/jobs/{id}/requeue` and `DELETE /dlq/jobs/{id}` can be used to experiment with reprocessing/removal.

Logs for both flows are written to stdout and to the Mongo `log_entries` collection for later inspection.

## Future work

Planned next steps:

- **Additional features**
  - Makefile for binary file building
  - Dockerfile and Docker Compose setup for local and containerized deployment (MongoDB + Redis + service)
