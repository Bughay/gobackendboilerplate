package jobs

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hibiken/asynq"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/config"
)

// RedisOpt builds the asynq connection options from the shared Redis address
// and password plus an explicit logical database.
//
// The caller MUST pass cfg.Worker.RedisDB. The cache lives in cfg.Redis.DB and
// the two keyspaces must never be mixed, which is why the database is a
// required argument instead of being read from the config struct here.
func RedisOpt(cfg config.RedisConfig, db int) asynq.RedisConnOpt {
	return asynq.RedisClientOpt{Addr: cfg.Addr, Password: cfg.Password, DB: db}
}

// NewServer builds the asynq worker with the configured concurrency, queue
// priorities and logger. The server is not started until Run is called; the
// caller owns its lifecycle and must call Shutdown.
func NewServer(opt asynq.RedisConnOpt, cfg config.WorkerConfig, logger *slog.Logger) *asynq.Server {
	return asynq.NewServer(opt, asynq.Config{
		Concurrency: cfg.Concurrency,
		Queues:      cfg.Queues,
		Logger:      slogAdapter{logger: logger},
	})
}

// slogAdapter forwards asynq's internal logs into the process slog logger, so
// worker output stays one structured stream instead of mixing slog and asynq's
// default stderr format. It implements asynq.Logger.
type slogAdapter struct {
	logger *slog.Logger
}

// Debug logs asynq's debug messages at debug level.
func (a slogAdapter) Debug(args ...interface{}) {
	a.logger.Debug(fmt.Sprint(args...))
}

// Info logs asynq's informational messages at info level.
func (a slogAdapter) Info(args ...interface{}) {
	a.logger.Info(fmt.Sprint(args...))
}

// Warn logs asynq's warnings at warn level.
func (a slogAdapter) Warn(args ...interface{}) {
	a.logger.Warn(fmt.Sprint(args...))
}

// Error logs asynq's errors at error level.
func (a slogAdapter) Error(args ...interface{}) {
	a.logger.Error(fmt.Sprint(args...))
}

// Fatal logs the message and exits with status 1, matching asynq.Logger's
// contract. asynq only calls it for unrecoverable startup failures.
func (a slogAdapter) Fatal(args ...interface{}) {
	a.logger.Error(fmt.Sprint(args...))
	os.Exit(1)
}
