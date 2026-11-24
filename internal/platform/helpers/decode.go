package helpers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxBodyBytes caps request bodies so a hostile client cannot exhaust memory.
const maxBodyBytes = 1 << 20 // 1 MiB

// Decode parses a JSON request body into dst, writing a 400 and returning false on failure.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	// Cap the body first so the decoder can never read more than the limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	// Reject unknown fields so typos surface as 400s instead of being silently ignored.
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		Error(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return false
	}

	// A second decode must hit EOF; anything else means the client sent trailing garbage.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "invalid_request", "request body must contain a single JSON object")
		return false
	}
	return true
}
