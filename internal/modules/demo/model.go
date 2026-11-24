// Package demo is a removable reference module that shows how a normal feature
// module enqueues background jobs, persists their state, and lets the worker
// process them.
//
// It intentionally follows the same shape as internal/modules/products and
// internal/modules/orders:
//
//	model.go       domain types and request bodies
//	validate.go    domain rules and sentinel errors
//	repository.go  persistence (Redis, because job state is ephemeral)
//	service.go     business rules, enqueueing, and worker-side processing
//	handler.go     HTTP transport
//	errors.go      sentinel error -> HTTP status mapping
//
// The asynq-specific adapters live in internal/platform/jobs:
//
//	tasks/tasks.go      task type identifiers and payload structs
//	handlers/demo.go    worker-side asynq handlers that call this module
//
// DEMO ONLY: delete this package, the two blocks marked "DEMO JOBS" in
// cmd/api/main.go and cmd/worker/main.go, the demo block in
// internal/platform/jobs/tasks/tasks.go, internal/platform/jobs/handlers/demo.go,
// and the demo paths, schemas, and tag in internal/platform/openapi/openapi.json.
package demo

import "time"

// Queue names that exist in the default WORKER_QUEUES setting. The simple job
// uses the default queue; the complex job uses the critical queue so the queue
// weights can be observed.
const (
	QueueDefault  = "default"
	QueueCritical = "critical"
)

// Job lifecycle values stored in Redis.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusRetrying  = "retrying"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// HelloInput is the body for POST /demo/jobs/hello. DelaySeconds is optional
// and demonstrates asynq's scheduled-task support.
type HelloInput struct {
	Name         string `json:"name"`
	DelaySeconds int    `json:"delay_seconds"`
}

// ReportInput is the body for POST /demo/jobs/report. Steps defaults to 5.
// FailOnAttempt intentionally fails that numbered attempt so the retry path is
// visible; 0 means "never fail".
type ReportInput struct {
	Steps         int `json:"steps"`
	FailOnAttempt int `json:"fail_on_attempt"`
	DelaySeconds  int `json:"delay_seconds"`
}

// Job is the Redis-backed record both the API and the worker update. It is the
// demo's equivalent of an order or product row, but it lives in the cache
// because a job's state is short-lived operational data.
type Job struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Queue        string    `json:"queue"`
	Status       string    `json:"status"`
	Attempt      int       `json:"attempt"`
	MaxRetry     int       `json:"max_retry"`
	Progress     int       `json:"progress"`
	Step         int       `json:"step,omitempty"`
	TotalSteps   int       `json:"total_steps,omitempty"`
	DelaySeconds int       `json:"delay_seconds,omitempty"`
	Result       string    `json:"result,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TaskMeta carries the asynq facts the worker adapter extracted from the task
// context. Keeping it a plain struct is what stops this module from importing
// asynq: the platform handler does the asynq-specific work and passes only the
// values the business logic needs.
type TaskMeta struct {
	// ID is the asynq task ID, which is also the Redis key suffix used here.
	ID string
	// Queue is the queue the task was consumed from.
	Queue string
	// Attempt is 1-based: the first execution is attempt 1.
	Attempt int
	// MaxRetry is the configured retry budget.
	MaxRetry int
}
