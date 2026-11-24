// Package jobs owns the asynq client and server shared by the API and worker
// binaries. It is deliberately free of domain imports: modules depend on the
// small Enqueuer interface and option helpers below, never on asynq's client or
// server internals.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Enqueuer is the interface modules depend on. Keeping it to the single method
// they actually use makes a test double trivial and stops asynq's API from
// leaking into the domain layer.
type Enqueuer interface {
	Enqueue(ctx context.Context, task *asynq.Task, opts ...Option) (*asynq.TaskInfo, error)
}

// Option is one enqueue setting. It is an alias for asynq.Option so modules can
// build an option slice through the helpers below without importing asynq.
type Option = asynq.Option

// InQueue selects the queue that will receive the task.
func InQueue(name string) Option { return asynq.Queue(name) }

// TaskID sets an explicit task ID. It is useful when the caller needs a stable
// ID to correlate the task with its own records, and it also makes enqueueing
// idempotent for that ID.
func TaskID(id string) Option { return asynq.TaskID(id) }

// MaxRetry sets how many times asynq may retry a failed task.
func MaxRetry(n int) Option { return asynq.MaxRetry(n) }

// Timeout bounds one execution of the task.
func Timeout(d time.Duration) Option { return asynq.Timeout(d) }

// Retention sets how long asynq keeps the completed task record.
func Retention(d time.Duration) Option { return asynq.Retention(d) }

// After schedules the task to run after d instead of immediately.
func After(d time.Duration) Option { return asynq.ProcessIn(d) }

// Client is the enqueue-only side of asynq. It is owned by the API binary;
// workers never construct one.
type Client struct {
	c *asynq.Client
}

// NewClient returns a client for the Redis instance described by opt. The
// connection is opened on the first enqueue, so construction cannot fail at
// boot even when Redis is temporarily unavailable.
func NewClient(opt asynq.RedisConnOpt) *Client {
	return &Client{c: asynq.NewClient(opt)}
}

// Close releases the client's Redis connection pool. The owner must call it on
// shutdown.
func (c *Client) Close() error {
	return c.c.Close()
}

// Enqueue pushes task onto its queue. Options select the queue, retry count,
// timeout, retention or delay; passing none uses asynq's defaults.
func (c *Client) Enqueue(ctx context.Context, task *asynq.Task, opts ...Option) (*asynq.TaskInfo, error) {
	return c.c.EnqueueContext(ctx, task, opts...)
}

// NewTask marshals payload into an *asynq.Task of the given type. A nil payload
// produces an empty body, which is valid for jobs that carry no arguments.
// Marshalling lives here so callers never hand-build JSON.
func NewTask(taskType string, payload any) (*asynq.Task, error) {
	var data []byte
	var err error
	if payload != nil {
		if data, err = json.Marshal(payload); err != nil {
			return nil, fmt.Errorf("jobs: marshal %s: %w", taskType, err)
		}
	}
	return asynq.NewTask(taskType, data), nil
}
