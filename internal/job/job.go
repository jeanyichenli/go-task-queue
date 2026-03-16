package job

import "time"

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	// StatusDeadLetter marks jobs that have been moved to the dead letter queue.
	StatusDeadLetter Status = "dead_letter"
)

// Job represents a unit of work in the task queue.
type Job struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// Payload is the job data. It is a map of ID string to any.
	Payload map[string]any `json:"payload"`

	// Status and timing
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Retries
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	LastError   string `json:"last_error"`
}
