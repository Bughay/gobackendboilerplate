package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	platformcache "github.com/Bughay/ego-backend-boilerplate/internal/platform/cache"
)

const (
	// jobKeyPrefix namespaces demo job state inside the cache Redis database.
	// asynq's own keys live in WORKER_REDIS_DB, so the two never collide.
	jobKeyPrefix = "demo:job:"

	// jobStateTTL keeps a finished job inspectable for an hour, then lets Redis
	// reclaim it automatically.
	jobStateTTL = time.Hour
)

// Repository is the persistence contract for demo jobs. It mirrors the products
// Repository: the service depends on the interface, and the concrete
// implementation is the only code that knows about the storage engine.
type Repository interface {
	// Save writes the whole job document.
	Save(ctx context.Context, job *Job) error
	// Get loads one job by ID. A missing key is ErrNotFound.
	Get(ctx context.Context, id string) (*Job, error)
	// Update applies mutate to the stored job and saves the result.
	Update(ctx context.Context, id string, mutate func(*Job)) error
	// Delete removes a job document.
	Delete(ctx context.Context, id string) error
}

// RedisRepository stores job documents through the cache interface. Redis is
// the right store here because job state is operational data with a TTL, unlike
// the durable rows in the products and orders modules.
type RedisRepository struct {
	cache platformcache.Cache
}

// NewRedisRepository returns a job repository backed by c.
func NewRedisRepository(c platformcache.Cache) *RedisRepository {
	return &RedisRepository{cache: c}
}

// Save JSON-encodes the job (the cache package does the encoding) and writes it
// with the demo TTL. Timestamps are maintained here so every caller gets the
// same behaviour.
func (r *RedisRepository) Save(ctx context.Context, job *Job) error {
	if job == nil {
		return errors.New("demo: cannot save a nil job")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.UpdatedAt = time.Now().UTC()

	if err := r.cache.Set(ctx, jobKeyPrefix+job.ID, job, jobStateTTL); err != nil {
		return fmt.Errorf("demoRepository.Save %s: %w", job.ID, err)
	}
	return nil
}

// Get loads and decodes one job document. Cache misses become ErrNotFound so
// the HTTP layer never has to know about Redis sentinels.
func (r *RedisRepository) Get(ctx context.Context, id string) (*Job, error) {
	// A malformed ID cannot be a key this module wrote.
	if !validJobID(id) {
		return nil, ErrNotFound
	}

	raw, err := r.cache.Get(ctx, jobKeyPrefix+id)
	if errors.Is(err, platformcache.ErrCacheMiss) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("demoRepository.Get %s: %w", id, err)
	}

	var job Job
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return nil, fmt.Errorf("demoRepository.Get %s: decode: %w", id, err)
	}
	return &job, nil
}

// Update reads the current document, applies mutate, and writes it back. Jobs
// are processed by one worker at a time (asynq guarantees a single delivery),
// so a read-modify-write is sufficient here; it avoids a Lua script for a demo.
func (r *RedisRepository) Update(ctx context.Context, id string, mutate func(*Job)) error {
	job, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	mutate(job)
	return r.Save(ctx, job)
}

// Delete removes a job document.
func (r *RedisRepository) Delete(ctx context.Context, id string) error {
	if err := r.cache.Delete(ctx, jobKeyPrefix+id); err != nil {
		return fmt.Errorf("demoRepository.Delete %s: %w", id, err)
	}
	return nil
}

// Compile-time proof that the concrete type satisfies the interface.
var _ Repository = (*RedisRepository)(nil)
