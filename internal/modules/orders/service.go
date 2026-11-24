package orders

import (
	"context"
	"log/slog"
	"math"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs"
)

// maxTotalCents is the largest value a numeric(12,2) column can hold
// (9,999,999,999.99). Calculating in cents avoids float drift while summing.
const maxTotalCents int64 = 999_999_999_999

// Service applies the order domain rules on top of the repository.
type Service struct {
	repo Repository
	// jobs is the enqueue-only side of the background queue. It is injected
	// now so the first task can be added without rewiring the composition
	// root; no method calls it yet.
	jobs jobs.Enqueuer
	// logger is the process logger, kept for task handlers that need to report
	// a non-fatal enqueue failure. Unused until the first task exists.
	logger *slog.Logger
}

// NewService returns an order service backed by repo that can enqueue
// background work through jobsEnq. The queue dependency is deliberately
// narrow (jobs.Enqueuer) so this module never imports asynq or the handlers.
func NewService(repo Repository, jobsEnq jobs.Enqueuer, logger *slog.Logger) *Service {
	return &Service{repo: repo, jobs: jobsEnq, logger: logger}
}

// Create validates a create request and stores the order for userID. Validation
// runs before storage so a malformed request never opens a transaction.
func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (*Order, error) {
	clean, err := cleanCreate(in)
	if err != nil {
		return nil, err
	}
	return s.repo.Create(ctx, userID, clean)
}

// List returns every order owned by userID.
func (s *Service) List(ctx context.Context, userID string) ([]Order, error) {
	return s.repo.List(ctx, userID)
}

// Get returns one order owned by userID. A malformed ID is rejected before it
// can reach Postgres and fail on a uuid cast.
func (s *Service) Get(ctx context.Context, userID, id string) (*Order, error) {
	if !isUUID(id) {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, userID, id)
}

// calculateTotal returns the sum of unit price times quantity for every line.
// Prices come from the catalog rows loaded inside the order transaction, never
// from the client. A missing price means the product vanished between the
// existence check and this call, and is reported as ErrProductNotFound.
func calculateTotal(items []CreateItemInput, prices map[string]float64) (float64, error) {
	var totalCents int64

	for _, item := range items {
		price, ok := prices[item.ProductID]
		if !ok {
			return 0, ErrProductNotFound
		}
		// Reject values the catalog should never contain so a bad row cannot
		// silently produce a NaN or negative order total.
		if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
			return 0, ErrInvalidInput
		}

		// numeric(12,2) has exactly two decimal places, so this recovers the
		// stored value as an exact number of cents.
		unitCents := int64(math.Round(price * 100))
		lineCents := unitCents * int64(item.Qty)

		// Reject a line or running total that no longer fits the column.
		if lineCents < 0 || lineCents > maxTotalCents-totalCents {
			return 0, ErrInvalidInput
		}
		totalCents += lineCents
	}

	return float64(totalCents) / 100, nil
}
