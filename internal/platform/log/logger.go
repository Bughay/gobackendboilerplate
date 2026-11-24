// Package log builds the single process logger used by every layer.
//
// The directory and the package are both named log, so an importer that also
// needs the standard library's log package must alias one of them; both
// cmd/api/main.go and cmd/worker/main.go import it as platformlog for exactly
// that reason.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// sensitiveKeys lists attribute keys whose values must never reach the log
// stream. Every entry is lowercase so the lookup can be case-insensitive.
//
// The list covers credentials the auth flow handles as well as common HTTP
// credential headers. Handlers should still never log secrets on purpose; this
// map is the last line of defence against a mistake.
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"passwd":        {},
	"password_hash": {},
	"token":         {},
	"access_token":  {},
	"refresh_token": {},
	"authorization": {},
	"cookie":        {},
	"set-cookie":    {},
	"secret":        {},
	"api_key":       {},
}

// redact is a slog.ReplaceAttr hook that masks values whose attribute key looks
// like a credential. It keeps the key visible and replaces only the value, so a
// reviewer can see that something was intentionally withheld rather than absent.
func redact(_ []string, a slog.Attr) slog.Attr {
	// Lowercase the key so "Password", "PASSWORD" and "password" all match.
	if _, ok := sensitiveKeys[strings.ToLower(a.Key)]; ok {
		return slog.String(a.Key, "[REDACTED]")
	}
	// Anything that is not credential-shaped is left exactly as the caller wrote it.
	return a
}

// NewLogger creates a *slog.Logger configured by format and level that writes
// to stdout.
//   - format "json" selects JSONHandler (production log shippers); anything else
//     selects TextHandler (human-readable local development).
//   - level "debug", "warn" or "error"; anything else, including "", is Info.
//
// The caller owns the returned logger; this package stores no global state.
func NewLogger(format, level string) *slog.Logger {
	// Production writes one stream to stdout so the process manager can collect it.
	return newLogger(os.Stdout, format, level)
}

// newLogger is NewLogger with an explicit writer. Keeping the writer a parameter
// lets tests capture output without redirecting the process's real stdout.
func newLogger(w io.Writer, format, level string) *slog.Logger {
	// Normalize both inputs so " JSON " and "WARN" behave like their canonical forms.
	format = strings.ToLower(strings.TrimSpace(format))
	level = strings.ToLower(strings.TrimSpace(level))

	// Map the textual level onto slog's numeric levels. Unknown values fall back
	// to Info, which is the only level that is safe for a production default.
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	// One options struct is shared by both handlers so level filtering and
	// redaction match no matter which format is selected.
	opts := &slog.HandlerOptions{
		Level: lvl,
		// ReplaceAttr runs on every attribute before it is encoded, which is the
		// only place where redaction cannot be bypassed by a caller.
		ReplaceAttr: redact,
	}

	// JSON is for machines; text is for humans.
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler)
}
