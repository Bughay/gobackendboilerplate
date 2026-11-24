// Package config loads runtime configuration from environment variables,
// including an optional .env file in the working directory.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	// minJWTSecretBytes is the shortest signing secret accepted, matching the HMAC key size we want.
	minJWTSecretBytes = 32

	// defaultHTTPAddr is used when HTTP_ADDR is unset or empty.
	defaultHTTPAddr = ":8080"

	// dotEnvFile is the conventional local-development environment file. It is
	// optional: containers receive their configuration from the environment.
	dotEnvFile = ".env"

	// defaultWorkerConcurrency is how many tasks one worker processes at once
	// when WORKER_CONCURRENCY is unset.
	defaultWorkerConcurrency = 10

	// defaultWorkerQueues is the queue/priority map used when WORKER_QUEUES is
	// unset: critical work outweighs default work, which outweighs low work.
	defaultWorkerQueues = "default:10,critical:20,low:3"

	// defaultWorkerRedisDB keeps the job queue out of the cache's database.
	// Cache keys live in REDIS_DB (default 0) and asynq keys in WORKER_REDIS_DB.
	defaultWorkerRedisDB = 2
)

// Config is the complete runtime configuration for the service. Every other
// package receives the values it needs from this struct; only this package
// reads the process environment.
type Config struct {
	Database  Database
	JWT       JWT
	Log       LogConfig
	Redis     RedisConfig
	Worker    WorkerConfig
	RateLimit RateLimitConfig
	CORS      CORSConfig
	Cookie    Cookie
	Proxy     Proxy
	Server    Server
}

// Database holds the PostgreSQL connection settings.
type Database struct {
	// DSN is the full connection string read from DATABASE_URL.
	DSN string
}

// JWT holds the access/refresh token settings used by the auth manager.
type JWT struct {
	// Secret is the HS256 signing key. It is a string because NewManager takes
	// a string and converts it to bytes once.
	Secret string
	// AccessExpiryHours is how long an access token lives. Env: JWT_EXPIRY_HOURS.
	AccessExpiryHours int
	// RefreshExpiryDays is how long a refresh token lives. Env: JWT_REFRESH_EXPIRY_DAYS.
	RefreshExpiryDays int
}

// LogConfig controls the process logger built by internal/platform/log.
type LogConfig struct {
	// Format is "json" for production or "text" (default) for local development.
	Format string
	// Level is "debug", "info" (default), "warn" or "error".
	Level string
}

// RedisConfig holds the Redis connection settings used by the cache package.
type RedisConfig struct {
	// Addr is host:port, default "localhost:6379".
	Addr string
	// Password is optional; empty means no AUTH.
	Password string
	// DB selects a logical database, default 0.
	DB int
}

// WorkerConfig controls the asynq background worker and the queue client.
type WorkerConfig struct {
	// Concurrency is how many tasks one worker processes at the same time.
	// Env: WORKER_CONCURRENCY, default 10.
	Concurrency int

	// Queues maps each queue name to its priority weight. Env: WORKER_QUEUES,
	// a comma-separated "name:weight" list such as "default:10,critical:20".
	// Weights are relative: asynq uses them to decide how often a queue is
	// polled, so a higher weight means more attention, not strictly first.
	Queues map[string]int

	// RedisDB is the logical Redis database asynq uses. Env: WORKER_REDIS_DB,
	// default 2. It must differ from Redis.DB because the cache and the job
	// queue are separate concerns and must never share a keyspace.
	RedisDB int
}

// RateLimitConfig controls the per-IP fixed-window request limiter.
type RateLimitConfig struct {
	// Enabled turns the limiter on or off; default true.
	Enabled bool
	// Requests is the maximum number of requests allowed per window and IP.
	Requests int
	// WindowSec is the window length in seconds.
	WindowSec int
}

// CORSConfig holds the extra browser origins allowed besides localhost.
type CORSConfig struct {
	// AllowedOrigins is a list of absolute origins such as
	// "https://app.example.com". Localhost is always allowed on top of these.
	AllowedOrigins []string
}

// Cookie holds the refresh-cookie policy.
type Cookie struct {
	// Secure marks the refresh cookie Secure, so browsers send it only over
	// HTTPS. Env: COOKIE_SECURE, default true.
	Secure bool
}

// Proxy holds the reverse-proxy trust settings used to resolve a client IP.
type Proxy struct {
	// TrustedProxies is a list of IPs or CIDRs whose X-Forwarded-For header is
	// believed. Env: TRUSTED_PROXIES. The default is empty, which means the
	// header is ignored and the socket peer is used — the only safe setting
	// when the API is exposed directly, because any client can forge the header.
	TrustedProxies []string
}

// Server holds the HTTP listener settings.
type Server struct {
	// Addr is the listen address in host:port form, read from HTTP_ADDR and
	// defaulting to ":8080". The Compose healthcheck probes localhost:8080, so
	// keep that port in sync when changing this.
	Addr string
}

// loadDotEnv copies .env from the working directory into the process
// environment. Existing environment variables always win because godotenv only
// fills in variables that are not already set. A missing .env is the normal
// container and CI case and is ignored; any other error (such as a malformed
// file) is returned so a broken configuration file never silently degrades to
// defaults.
func loadDotEnv() error {
	if err := godotenv.Load(dotEnvFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: load %s: %w", dotEnvFile, err)
	}
	return nil
}

// Load reads configuration from .env and the environment, applying defaults and
// rejecting missing secrets or non-positive lifetimes. It is the only function
// that touches the process environment; the rest of the application receives a
// Config value instead.
func Load() (*Config, error) {
	// .env is the lowest-precedence source. Real environment variables win
	// because godotenv never overrides an existing variable.
	if err := loadDotEnv(); err != nil {
		return nil, err
	}

	// A weak or absent secret would silently break token verification, so fail fast instead.
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < minJWTSecretBytes {
		return nil, fmt.Errorf("config: JWT_SECRET must be at least %d characters", minJWTSecretBytes)
	}

	// Without a database the service can serve nothing, so reject a missing DSN
	// here at boot rather than failing on the first query.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("config: DATABASE_URL is required")
	}

	accessHours, err := intEnv("JWT_EXPIRY_HOURS", 1)
	if err != nil {
		return nil, err
	}
	// A zero or negative access lifetime would mint already-expired tokens.
	if accessHours <= 0 {
		return nil, errors.New("config: JWT_EXPIRY_HOURS must be positive")
	}

	refreshDays, err := intEnv("JWT_REFRESH_EXPIRY_DAYS", 7)
	if err != nil {
		return nil, err
	}
	// A zero or negative refresh lifetime would make every session unusable.
	if refreshDays <= 0 {
		return nil, errors.New("config: JWT_REFRESH_EXPIRY_DAYS must be positive")
	}

	// Logging has safe defaults, so a missing LOG_FORMAT/LOG_LEVEL never blocks boot.
	logFormat := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT")))
	if logFormat == "" {
		logFormat = "text"
	}
	logLevel := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	if logLevel == "" {
		logLevel = "info"
	}

	// Redis defaults allow local development to work without an .env entry; a
	// missing Redis at boot is handled by the NopCache fallback in main.
	redisAddr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisPassword := os.Getenv("REDIS_PASSWORD")
	redisDB, err := intEnv("REDIS_DB", 0)
	if err != nil {
		return nil, err
	}
	// A negative logical database does not exist in Redis.
	if redisDB < 0 {
		return nil, errors.New("config: REDIS_DB must not be negative")
	}

	// Worker settings default to a usable local configuration, so the worker
	// boots without any WORKER_* variables. A bad value still fails fast.
	workerConcurrency, err := intEnv("WORKER_CONCURRENCY", defaultWorkerConcurrency)
	if err != nil {
		return nil, err
	}
	// Zero or negative concurrency would make asynq silently fall back to
	// runtime.NumCPU, which hides a configuration mistake instead of reporting it.
	if workerConcurrency <= 0 {
		return nil, errors.New("config: WORKER_CONCURRENCY must be positive")
	}

	workerQueues, err := queueWeightsEnv("WORKER_QUEUES", defaultWorkerQueues)
	if err != nil {
		return nil, err
	}

	workerRedisDB, err := intEnv("WORKER_REDIS_DB", defaultWorkerRedisDB)
	if err != nil {
		return nil, err
	}
	// A negative logical database does not exist in Redis.
	if workerRedisDB < 0 {
		return nil, errors.New("config: WORKER_REDIS_DB must not be negative")
	}
	// Cache and queue are independent keyspaces. Sharing one database would let
	// a cache flush, SCAN or key collision disturb in-flight jobs (and vice
	// versa), so refuse the configuration instead of documenting the hazard.
	if workerRedisDB == redisDB {
		return nil, errors.New("config: WORKER_REDIS_DB must differ from REDIS_DB: the cache and the job queue never share a Redis database")
	}

	// Rate limiting is on unless explicitly disabled. The defaults follow the
	// project requirement of 20 requests per second per IP.
	rateLimitEnabled, err := boolEnv("RATE_LIMIT_ENABLED", true)
	if err != nil {
		return nil, err
	}
	rateLimitRequests, err := intEnv("RATE_LIMIT_REQUESTS", 20)
	if err != nil {
		return nil, err
	}
	rateLimitWindow, err := intEnv("RATE_LIMIT_WINDOW_SEC", 1)
	if err != nil {
		return nil, err
	}
	// Validate only when the limiter is active: a disabled limiter ignores both values.
	if rateLimitEnabled && (rateLimitRequests <= 0 || rateLimitWindow <= 0) {
		return nil, errors.New("config: RATE_LIMIT_REQUESTS and RATE_LIMIT_WINDOW_SEC must be positive when rate limiting is enabled")
	}

	// CORS origins are optional; localhost is always permitted by the middleware.
	corsOrigins, err := originListEnv("CORS_ALLOWED_ORIGINS")
	if err != nil {
		return nil, err
	}

	// The refresh cookie is Secure by default: an insecure cookie sent over
	// plain HTTP leaks a session token to anyone on the network path.
	cookieSecure, err := boolEnv("COOKIE_SECURE", true)
	if err != nil {
		return nil, err
	}

	// Trusted proxies default to empty, which means X-Forwarded-For is ignored.
	// That is the only safe default for a directly exposed API.
	trustedProxies, err := cidrListEnv("TRUSTED_PROXIES")
	if err != nil {
		return nil, err
	}

	// The listen address is validated at boot so a typo fails here instead of
	// inside ListenAndServe.
	addr := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if addr == "" {
		addr = defaultHTTPAddr
	}
	if err := validateListenAddr(addr); err != nil {
		return nil, err
	}

	return &Config{
		Database: Database{DSN: dsn},
		JWT: JWT{
			Secret:            secret,
			AccessExpiryHours: accessHours,
			RefreshExpiryDays: refreshDays,
		},
		Log: LogConfig{Format: logFormat, Level: logLevel},
		Redis: RedisConfig{
			Addr:     redisAddr,
			Password: redisPassword,
			DB:       redisDB,
		},
		Worker: WorkerConfig{
			Concurrency: workerConcurrency,
			Queues:      workerQueues,
			RedisDB:     workerRedisDB,
		},
		RateLimit: RateLimitConfig{
			Enabled:   rateLimitEnabled,
			Requests:  rateLimitRequests,
			WindowSec: rateLimitWindow,
		},
		CORS:   CORSConfig{AllowedOrigins: corsOrigins},
		Cookie: Cookie{Secure: cookieSecure},
		Proxy:  Proxy{TrustedProxies: trustedProxies},
		Server: Server{Addr: addr},
	}, nil
}

// validateListenAddr checks that addr is a host:port pair with a usable port.
// An empty host (":8080") binds all interfaces, and IPv6 literals must be
// bracketed ("[::]:8080"), which is exactly what net.SplitHostPort accepts.
func validateListenAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: HTTP_ADDR %q must be host:port: %w", addr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("config: HTTP_ADDR %q has an invalid port", addr)
	}
	return nil
}

// intEnv parses an integer from the environment, falling back to def when unset.
func intEnv(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return v, nil
}

// boolEnv parses a boolean from the environment, falling back to def when unset.
// Invalid values are configuration errors rather than a silent default.
func boolEnv(key string, def bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean: %w", key, err)
	}
	return v, nil
}

// originListEnv splits a comma-separated list of browser origins and validates
// each one. Every entry must be an absolute http(s) origin with no path, query
// or fragment, because the middleware compares it against the Origin header.
func originListEnv(key string) ([]string, error) {
	return parseOrigins(key, os.Getenv(key))
}

// cidrListEnv splits a comma-separated list of IPs or CIDRs and validates each
// one. Values are kept as strings so this package stays a plain configuration
// DTO; the client-IP resolver re-parses them when the server is built.
func cidrListEnv(key string) ([]string, error) {
	return parseCIDRs(key, os.Getenv(key))
}

// queueWeightsEnv parses WORKER_QUEUES, falling back to def when the variable
// is unset or empty (an empty value is treated as absent throughout this file).
func queueWeightsEnv(key, def string) (map[string]int, error) {
	return parseQueueWeights(key, os.Getenv(key), def)
}

// parseQueueWeights turns a comma-separated "name:weight" list into the map
// asynq expects. Blank entries are ignored so a trailing comma is harmless, but
// duplicate names are rejected because they would make the effective weight
// ambiguous. Every weight must be positive: asynq silently ignores a queue with
// a non-positive weight, which would leave the worker listening to fewer queues
// than the operator asked for.
func parseQueueWeights(key, raw, def string) (map[string]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = def
	}

	queues := make(map[string]int, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}

		name, weightRaw, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("config: %s entry %q must be name:weight", key, entry)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("config: %s entry %q has an empty queue name", key, entry)
		}
		if _, duplicate := queues[name]; duplicate {
			return nil, fmt.Errorf("config: %s lists queue %q more than once", key, name)
		}

		weight, err := strconv.Atoi(strings.TrimSpace(weightRaw))
		if err != nil {
			return nil, fmt.Errorf("config: %s queue %q weight must be an integer: %w", key, name, err)
		}
		if weight <= 0 {
			return nil, fmt.Errorf("config: %s queue %q weight must be positive", key, name)
		}
		queues[name] = weight
	}

	if len(queues) == 0 {
		return nil, fmt.Errorf("config: %s must list at least one queue", key)
	}
	return queues, nil
}

// parseOrigins validates the raw value of a comma-separated origin list. It is
// separate from originListEnv so the parsing rules can be tested directly.
func parseOrigins(key, raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// Parse and validate each entry, accumulating the normalized form.
	origins := make([]string, 0, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}

		u, err := url.Parse(entry)
		// Require scheme + host and reject anything that is not a bare origin.
		if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("config: %s entry %q must be an origin like https://app.example.com", key, entry)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("config: %s entry %q must use http or https", key, entry)
		}

		// Store a canonical lowercase scheme://host[:port] so matching is exact.
		origins = append(origins, strings.ToLower(u.Scheme+"://"+u.Host))
	}
	return origins, nil
}

// parseCIDRs validates the raw value of a comma-separated IP/CIDR list. It is
// separate from cidrListEnv so the parsing rules can be tested directly.
func parseCIDRs(key, raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// Validate and normalize every entry so a typo fails at boot instead of
	// silently changing which proxies are trusted.
	entries := make([]string, 0, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}

		// A bare IP is accepted as a single-host network.
		if !strings.Contains(entry, "/") {
			if net.ParseIP(entry) == nil {
				return nil, fmt.Errorf("config: %s entry %q must be an IP or CIDR", key, entry)
			}
			entries = append(entries, entry)
			continue
		}

		// A CIDR must parse before it can be trusted.
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("config: %s entry %q is not a valid CIDR: %w", key, entry, err)
		}
		// A zero-length prefix would trust the whole internet, which would let
		// every client forge X-Forwarded-For again. Reject it explicitly.
		if ones, _ := network.Mask.Size(); ones == 0 {
			return nil, fmt.Errorf("config: %s entry %q would trust every client", key, entry)
		}

		entries = append(entries, entry)
	}
	return entries, nil
}
