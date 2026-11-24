package demo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs/tasks"
)

// Service contains the demo's business rules. It is deliberately shaped like
// the products and orders services:
//
//   - the API side validates input, creates a job record, and enqueues a task;
//   - the worker side receives TaskMeta plus a decoded payload, updates the
//     job record, and returns an error when asynq should retry.
//
// The service depends on jobs.Enqueuer, never on asynq's client or server.
type Service struct {
	repo     Repository
	enqueuer jobs.Enqueuer
	logger   *slog.Logger
}

// NewService returns a demo service backed by repo. The enqueuer is required on
// the API side. It may be nil in cmd/worker, where only the Run* methods are
// used, and those never enqueue anything.
func NewService(repo Repository, enqueuer jobs.Enqueuer, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, enqueuer: enqueuer, logger: logger}
}

// EnqueueHello validates the simple job, records a queued Job, and pushes the
// task onto the default queue. The API returns as soon as the enqueue succeeds;
// the actual greeting happens later in the worker.
func (s *Service) EnqueueHello(ctx context.Context, in HelloInput) (*Job, error) {
	clean, err := cleanHello(in)
	if err != nil {
		return nil, err
	}
	return s.enqueue(ctx, tasks.TypeDemoHello, QueueDefault,
		tasks.DemoHelloPayload{Name: clean.Name},
		2, 10*time.Second, 1, clean.DelaySeconds,
	)
}

// EnqueueReport validates the complex job and pushes it onto the critical
// queue. Three retries and a 30-second timeout demonstrate the per-task policy.
func (s *Service) EnqueueReport(ctx context.Context, in ReportInput) (*Job, error) {
	clean, err := cleanReport(in)
	if err != nil {
		return nil, err
	}
	return s.enqueue(ctx, tasks.TypeDemoReport, QueueCritical,
		tasks.DemoReportPayload{
			Steps:         clean.Steps,
			FailOnAttempt: clean.FailOnAttempt,
		},
		3, 30*time.Second, clean.Steps, clean.DelaySeconds,
	)
}

// Get returns the current state of one demo job.
func (s *Service) Get(ctx context.Context, id string) (*Job, error) {
	return s.repo.Get(ctx, id)
}

// RunHello is the worker-side implementation of the simple job. It updates the
// job record before and after a short simulated workload, so polling the API
// shows the queued -> running -> completed transition.
func (s *Service) RunHello(ctx context.Context, meta TaskMeta, payload tasks.DemoHelloPayload) error {
	log := s.taskLogger(meta, tasks.TypeDemoHello)

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "world"
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return ErrInvalidInput
	}

	s.updateState(ctx, meta.ID, func(job *Job) {
		job.Status = StatusRunning
		job.Attempt = meta.Attempt
		job.MaxRetry = meta.MaxRetry
		job.Progress = 10
		job.Step = 1
		job.TotalSteps = 1
		job.Error = ""
	})
	log.InfoContext(ctx, "demo hello started")

	// A short sleep makes asynchrony visible: the HTTP request returns long
	// before this work finishes.
	if err := sleepContext(ctx, time.Second); err != nil {
		s.markFailure(ctx, meta, err)
		return err
	}

	result := "hello, " + name + "!"
	s.updateState(ctx, meta.ID, func(job *Job) {
		job.Status = StatusCompleted
		job.Progress = 100
		job.Result = result
		job.Error = ""
	})
	log.InfoContext(ctx, "demo hello completed", "result", result)
	return nil
}

// RunReport is the worker-side implementation of the complex job. It writes
// progress at every step and, when FailOnAttempt matches the current attempt,
// returns an error halfway through so asynq's retry/backoff path is exercised.
func (s *Service) RunReport(ctx context.Context, meta TaskMeta, payload tasks.DemoReportPayload) error {
	log := s.taskLogger(meta, tasks.TypeDemoReport)

	steps := payload.Steps
	if steps == 0 {
		steps = defaultReportSteps
	}
	if steps < 1 || steps > maxReportSteps ||
		payload.FailOnAttempt < 0 || payload.FailOnAttempt > maxFailAttempt {
		return ErrInvalidInput
	}

	// Starting a retry resets the visible progress so the polling client can
	// see that a new attempt began.
	s.updateState(ctx, meta.ID, func(job *Job) {
		job.Status = StatusRunning
		job.Attempt = meta.Attempt
		job.MaxRetry = meta.MaxRetry
		job.Progress = 0
		job.Step = 0
		job.TotalSteps = steps
		job.Error = ""
	})
	log.InfoContext(ctx, "demo report started",
		"steps", steps,
		"fail_on_attempt", payload.FailOnAttempt,
	)

	// Fail halfway through the requested attempt. Attempt 1 is the first run,
	// so fail_on_attempt=1 fails once and succeeds on the first retry.
	failAtStep := 0
	if payload.FailOnAttempt == meta.Attempt {
		failAtStep = (steps + 1) / 2
	}

	for step := 1; step <= steps; step++ {
		if err := sleepContext(ctx, 300*time.Millisecond); err != nil {
			s.markFailure(ctx, meta, err)
			return err
		}

		progress := step * 100 / steps
		s.updateState(ctx, meta.ID, func(job *Job) {
			job.Status = StatusRunning
			job.Step = step
			job.Progress = progress
		})
		log.DebugContext(ctx, "demo report progress", "step", step, "progress", progress)

		if step == failAtStep {
			cause := fmt.Errorf("demo: simulated failure on attempt %d", meta.Attempt)
			s.markFailure(ctx, meta, cause)
			log.WarnContext(ctx, "demo report failed on purpose",
				"attempt", meta.Attempt,
				"max_retry", meta.MaxRetry,
			)
			return cause
		}
	}

	result := fmt.Sprintf("report finished after %d steps on attempt %d", steps, meta.Attempt)
	s.updateState(ctx, meta.ID, func(job *Job) {
		job.Status = StatusCompleted
		job.Progress = 100
		job.Result = result
		job.Error = ""
	})
	log.InfoContext(ctx, "demo report completed", "result", result)
	return nil
}

// MarkFailed records a permanent failure. The platform handler uses it when a
// payload cannot be decoded; such a task must not be retried.
func (s *Service) MarkFailed(ctx context.Context, meta TaskMeta, cause error) {
	s.updateState(ctx, meta.ID, func(job *Job) {
		job.Status = StatusFailed
		job.Attempt = meta.Attempt
		job.MaxRetry = meta.MaxRetry
		job.Error = cause.Error()
	})
}

// enqueue is the shared path for both jobs. It writes the initial state before
// enqueueing so a fast worker can never update a row that does not exist yet.
// The random task ID is also the Redis key, which makes logs and API polling
// easy to correlate.
func (s *Service) enqueue(
	ctx context.Context,
	taskType string,
	queue string,
	payload any,
	maxRetry int,
	timeout time.Duration,
	totalSteps int,
	delaySeconds int,
) (*Job, error) {
	if s.enqueuer == nil {
		return nil, errors.New("demo: job enqueuer is not configured")
	}

	id, err := newJobID()
	if err != nil {
		return nil, err
	}

	job := &Job{
		ID:           id,
		Type:         taskType,
		Queue:        queue,
		Status:       StatusQueued,
		MaxRetry:     maxRetry,
		TotalSteps:   totalSteps,
		DelaySeconds: delaySeconds,
	}
	if err := s.repo.Save(ctx, job); err != nil {
		return nil, err
	}

	task, err := jobs.NewTask(taskType, payload)
	if err != nil {
		// The state row was written first, so remove it if the task cannot be
		// built; otherwise the API would expose a job that will never run.
		_ = s.repo.Delete(ctx, id)
		return nil, err
	}

	opts := []jobs.Option{
		jobs.InQueue(queue),
		jobs.TaskID(id),
		jobs.MaxRetry(maxRetry),
		jobs.Timeout(timeout),
		jobs.Retention(jobStateTTL),
	}
	if delaySeconds > 0 {
		opts = append(opts, jobs.After(time.Duration(delaySeconds)*time.Second))
	}

	info, err := s.enqueuer.Enqueue(ctx, task, opts...)
	if err != nil {
		_ = s.repo.Delete(ctx, id)
		return nil, fmt.Errorf("demo: enqueue %s: %w", taskType, err)
	}
	if info != nil {
		job.Queue = info.Queue
	}

	s.logger.InfoContext(ctx, "demo job enqueued",
		"job_id", job.ID,
		"type", job.Type,
		"queue", job.Queue,
		"delay_seconds", delaySeconds,
	)
	return job, nil
}

// taskLogger attaches the same task fields to every worker log line.
func (s *Service) taskLogger(meta TaskMeta, taskType string) *slog.Logger {
	return s.logger.With(
		"component", "demo-worker",
		"task_type", taskType,
		"task_id", meta.ID,
		"queue", meta.Queue,
		"attempt", meta.Attempt,
	)
}

// updateState applies a mutation to the stored job. A missing row is ignored:
// the state has a one-hour TTL and losing telemetry must not fail the task.
func (s *Service) updateState(ctx context.Context, id string, mutate func(*Job)) {
	if id == "" {
		return
	}
	if err := s.repo.Update(ctx, id, mutate); err != nil && !errors.Is(err, ErrNotFound) {
		s.logger.WarnContext(ctx, "demo: could not update job state",
			"job_id", id,
			"error", err,
		)
	}
}

// markFailure records a retryable failure. Attempt numbers are 1-based, so the
// task is out of retries when the attempt exceeds the configured budget.
func (s *Service) markFailure(ctx context.Context, meta TaskMeta, cause error) {
	status := StatusRetrying
	if meta.Attempt > meta.MaxRetry {
		status = StatusFailed
	}
	s.updateState(ctx, meta.ID, func(job *Job) {
		job.Status = status
		job.Attempt = meta.Attempt
		job.MaxRetry = meta.MaxRetry
		job.Error = cause.Error()
	})
}

// sleepContext waits for d or until ctx is cancelled, which happens on worker
// shutdown or when the per-task timeout expires.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// newJobID returns a 128-bit random hexadecimal identifier used as both the
// asynq task ID and the Redis key suffix.
func newJobID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("demo: generate job id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
