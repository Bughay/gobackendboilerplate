package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// loadWith sets the two required variables, applies the overrides for one test,
// and runs Load. Using "" for an override means "unset" because every reader in
// this package treats an empty value as absent.
func loadWith(t *testing.T, overrides map[string]string) (*Config, error) {
	t.Helper()

	// Satisfy the mandatory settings first so a failure points at the override.
	t.Setenv("JWT_SECRET", strings.Repeat("s", minJWTSecretBytes))
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/app?sslmode=disable")

	for key, value := range overrides {
		t.Setenv(key, value)
	}
	return Load()
}

// TestCookieSecureDefaultsToTrue proves the refresh cookie is HTTPS-only unless
// an operator explicitly opts out, so the insecure mode is never accidental.
func TestCookieSecureDefaultsToTrue(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"COOKIE_SECURE": ""})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Cookie.Secure {
		t.Fatal("Cookie.Secure = false, want true by default")
	}
}

// TestCookieSecureCanBeDisabled proves local plain-HTTP testing is still possible.
func TestCookieSecureCanBeDisabled(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"COOKIE_SECURE": "false"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Cookie.Secure {
		t.Fatal("Cookie.Secure = true, want false when COOKIE_SECURE=false")
	}
}

// TestCookieSecureRejectsGarbage proves a typo fails at boot instead of silently
// falling back to a default.
func TestCookieSecureRejectsGarbage(t *testing.T) {
	if _, err := loadWith(t, map[string]string{"COOKIE_SECURE": "yes-please"}); err == nil {
		t.Fatal("Load() error = nil, want an error for an invalid boolean")
	}
}

// TestTrustedProxiesDefaultsToEmpty proves the shipped default trusts no proxy,
// which is what makes a forged X-Forwarded-For header harmless.
func TestTrustedProxiesDefaultsToEmpty(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"TRUSTED_PROXIES": ""})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Proxy.TrustedProxies) != 0 {
		t.Fatalf("TrustedProxies = %v, want empty", cfg.Proxy.TrustedProxies)
	}
}

// TestTrustedProxiesParsesList proves CIDRs and bare IPs are both accepted and
// that blank entries from a trailing comma are tolerated.
func TestTrustedProxiesParsesList(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"TRUSTED_PROXIES": " 10.0.0.0/8 , 192.0.2.10 ,",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := []string{"10.0.0.0/8", "192.0.2.10"}
	if len(cfg.Proxy.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.Proxy.TrustedProxies, want)
	}
	for i, entry := range want {
		if cfg.Proxy.TrustedProxies[i] != entry {
			t.Fatalf("TrustedProxies[%d] = %q, want %q", i, cfg.Proxy.TrustedProxies[i], entry)
		}
	}
}

// TestTrustedProxiesRejectsInvalid proves a malformed entry fails at boot.
func TestTrustedProxiesRejectsInvalid(t *testing.T) {
	if _, err := loadWith(t, map[string]string{"TRUSTED_PROXIES": "not-an-ip"}); err == nil {
		t.Fatal("Load() error = nil, want an error for an invalid entry")
	}
}

// TestTrustedProxiesRejectsTrustEveryone proves the most dangerous misconfiguration
// — trusting every possible peer — is refused rather than accepted.
func TestTrustedProxiesRejectsTrustEveryone(t *testing.T) {
	for _, entry := range []string{"0.0.0.0/0", "::/0"} {
		t.Run(entry, func(t *testing.T) {
			if _, err := loadWith(t, map[string]string{"TRUSTED_PROXIES": entry}); err == nil {
				t.Fatalf("Load() error = nil, want an error for %q", entry)
			}
		})
	}
}

// TestHTTPAddrDefaultsTo8080 proves a missing HTTP_ADDR falls back to the
// documented default instead of failing boot.
func TestHTTPAddrDefaultsTo8080(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"HTTP_ADDR": ""})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Fatalf("Server.Addr = %q, want %q", cfg.Server.Addr, ":8080")
	}
}

// TestHTTPAddrFromEnv proves the listen address is configurable through the
// environment.
func TestHTTPAddrFromEnv(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"HTTP_ADDR": "127.0.0.1:9090"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:9090" {
		t.Fatalf("Server.Addr = %q, want %q", cfg.Server.Addr, "127.0.0.1:9090")
	}
}

// TestHTTPAddrRejectsInvalid proves a malformed listen address fails at boot
// instead of inside ListenAndServe.
func TestHTTPAddrRejectsInvalid(t *testing.T) {
	for _, addr := range []string{"8080", "localhost:not-a-port", "127.0.0.1:70000"} {
		t.Run(addr, func(t *testing.T) {
			if _, err := loadWith(t, map[string]string{"HTTP_ADDR": addr}); err == nil {
				t.Fatalf("Load() error = nil, want an error for HTTP_ADDR=%q", addr)
			}
		})
	}
}

// unsetEnv removes variables for the duration of a test and restores them
// afterwards. t.Setenv(key, "") is not enough because godotenv treats a present
// but empty variable as already set and refuses to fill it from .env.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		old, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() { _ = os.Setenv(key, old) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(key) })
		}
		_ = os.Unsetenv(key)
	}
}

// writeDotEnv creates a temporary working directory containing a .env file and
// returns its path. The caller changes into it so godotenv.Load picks it up.
func writeDotEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	return dir
}

// TestLoadReadsDotEnvFile proves config.Load picks up values from .env in the
// working directory, which is how local development runs.
func TestLoadReadsDotEnvFile(t *testing.T) {
	secret := strings.Repeat("e", minJWTSecretBytes)
	dir := writeDotEnv(t, "JWT_SECRET="+secret+
		"\nDATABASE_URL=postgres://app:app@localhost:5432/app?sslmode=disable"+
		"\nHTTP_ADDR=127.0.0.1:9091\n")
	t.Chdir(dir)
	unsetEnv(t, "JWT_SECRET", "DATABASE_URL", "HTTP_ADDR")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.JWT.Secret != secret {
		t.Fatalf("JWT.Secret = %q, want the value from .env", cfg.JWT.Secret)
	}
	if cfg.Server.Addr != "127.0.0.1:9091" {
		t.Fatalf("Server.Addr = %q, want %q from .env", cfg.Server.Addr, "127.0.0.1:9091")
	}
}

// TestEnvOverridesDotEnv proves an exported variable always wins over .env, so
// production/CI can override a developer's local file without editing it.
func TestEnvOverridesDotEnv(t *testing.T) {
	dir := writeDotEnv(t, "JWT_SECRET="+strings.Repeat("d", minJWTSecretBytes)+
		"\nDATABASE_URL=postgres://app:app@localhost:5432/app?sslmode=disable"+
		"\nHTTP_ADDR=:9091\n")
	t.Chdir(dir)
	unsetEnv(t, "JWT_SECRET", "DATABASE_URL", "HTTP_ADDR")
	t.Setenv("HTTP_ADDR", ":9092")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Addr != ":9092" {
		t.Fatalf("Server.Addr = %q, want the exported %q", cfg.Server.Addr, ":9092")
	}
}

// TestBrokenDotEnvFailsBoot proves a .env that exists but cannot be read is a
// configuration error rather than being silently ignored.
func TestBrokenDotEnvFailsBoot(t *testing.T) {
	dir := t.TempDir()
	// A directory named .env makes the read fail on every platform, without
	// depending on file-permission behaviour that differs for root users.
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o700); err != nil {
		t.Fatalf("create .env directory: %v", err)
	}
	t.Chdir(dir)
	unsetEnv(t, "JWT_SECRET", "DATABASE_URL")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error for an unreadable .env")
	}
}

// TestWorkerDefaults proves the worker can boot with no WORKER_* variables and
// that its Redis database is separate from the cache's database.
func TestWorkerDefaults(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"WORKER_CONCURRENCY": "",
		"WORKER_QUEUES":      "",
		"WORKER_REDIS_DB":    "",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Worker.Concurrency != 10 {
		t.Errorf("Worker.Concurrency = %d, want 10", cfg.Worker.Concurrency)
	}
	wantQueues := map[string]int{"default": 10, "critical": 20, "low": 3}
	if !reflect.DeepEqual(cfg.Worker.Queues, wantQueues) {
		t.Errorf("Worker.Queues = %v, want %v", cfg.Worker.Queues, wantQueues)
	}
	if cfg.Worker.RedisDB != 2 {
		t.Errorf("Worker.RedisDB = %d, want 2", cfg.Worker.RedisDB)
	}
	if cfg.Redis.DB != 0 {
		t.Errorf("Redis.DB = %d, want the cache database 0", cfg.Redis.DB)
	}
}

// TestWorkerQueuesParsesList proves "name:weight" entries are parsed, surrounding
// whitespace is trimmed, and a trailing comma is harmless.
func TestWorkerQueuesParsesList(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"WORKER_QUEUES": " critical:20 , default:5, low:1, ",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := map[string]int{"critical": 20, "default": 5, "low": 1}
	if !reflect.DeepEqual(cfg.Worker.Queues, want) {
		t.Fatalf("Worker.Queues = %v, want %v", cfg.Worker.Queues, want)
	}
}

// TestWorkerConfigRejectsInvalid proves every malformed worker setting fails at
// boot instead of silently producing a worker that listens to the wrong queues.
func TestWorkerConfigRejectsInvalid(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
	}{
		{"concurrency zero", map[string]string{"WORKER_CONCURRENCY": "0"}},
		{"concurrency negative", map[string]string{"WORKER_CONCURRENCY": "-1"}},
		{"concurrency not a number", map[string]string{"WORKER_CONCURRENCY": "many"}},
		{"queue missing weight", map[string]string{"WORKER_QUEUES": "default"}},
		{"queue weight not a number", map[string]string{"WORKER_QUEUES": "default:abc"}},
		{"queue weight zero", map[string]string{"WORKER_QUEUES": "default:0"}},
		{"queue weight negative", map[string]string{"WORKER_QUEUES": "default:-3"}},
		{"queue empty name", map[string]string{"WORKER_QUEUES": ":10"}},
		{"queue duplicate name", map[string]string{"WORKER_QUEUES": "default:1,default:2"}},
		{"redis db negative", map[string]string{"WORKER_REDIS_DB": "-1"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadWith(t, tc.overrides); err == nil {
				t.Fatalf("Load() error = nil, want an error for %v", tc.overrides)
			}
		})
	}
}

// TestWorkerRedisDBMustDifferFromCache proves the cache/queue separation is
// enforced rather than only documented: both client types would otherwise share
// one keyspace and could disturb each other's keys.
func TestWorkerRedisDBMustDifferFromCache(t *testing.T) {
	_, err := loadWith(t, map[string]string{
		"REDIS_DB":        "2",
		"WORKER_REDIS_DB": "2",
	})
	if err == nil {
		t.Fatal("Load() error = nil, want an error when cache and queue share a Redis database")
	}
}
