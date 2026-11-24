package demo

import (
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	// ErrNotFound means the job ID is unknown or its Redis key expired.
	ErrNotFound = errors.New("demo: job not found")

	// ErrInvalidInput means the request or task payload violates a demo rule.
	ErrInvalidInput = errors.New("demo: invalid input")
)

const (
	// maxNameRunes bounds the greeting name so a payload cannot grow unbounded.
	maxNameRunes = 80
	// maxReportSteps bounds the simulated work in the complex job.
	maxReportSteps = 20
	// defaultReportSteps is used when steps is omitted.
	defaultReportSteps = 5
	// maxFailAttempt bounds the retry simulation input.
	maxFailAttempt = 10
	// maxDelaySeconds keeps a scheduled demo job within five minutes.
	maxDelaySeconds = 300
)

// cleanHello trims the greeting name, applies a friendly default, and validates
// the optional delay. It is the same "clean then validate" pattern used by the
// products and orders modules.
func cleanHello(in HelloInput) (HelloInput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "world"
	}
	// Count runes, not bytes, so non-ASCII names get the same budget.
	if utf8.RuneCountInString(name) > maxNameRunes {
		return HelloInput{}, ErrInvalidInput
	}
	if err := validateDelay(in.DelaySeconds); err != nil {
		return HelloInput{}, err
	}
	return HelloInput{Name: name, DelaySeconds: in.DelaySeconds}, nil
}

// cleanReport applies the report defaults and validates every numeric field
// before the service stores or enqueues anything.
func cleanReport(in ReportInput) (ReportInput, error) {
	steps := in.Steps
	if steps == 0 {
		steps = defaultReportSteps
	}
	if steps < 1 || steps > maxReportSteps {
		return ReportInput{}, ErrInvalidInput
	}
	if in.FailOnAttempt < 0 || in.FailOnAttempt > maxFailAttempt {
		return ReportInput{}, ErrInvalidInput
	}
	if err := validateDelay(in.DelaySeconds); err != nil {
		return ReportInput{}, err
	}
	return ReportInput{
		Steps:         steps,
		FailOnAttempt: in.FailOnAttempt,
		DelaySeconds:  in.DelaySeconds,
	}, nil
}

// validateDelay rejects a scheduling delay outside the demo's allowed range.
func validateDelay(seconds int) error {
	if seconds < 0 || seconds > maxDelaySeconds {
		return ErrInvalidInput
	}
	return nil
}

// validJobID reports whether id is the 32-character hexadecimal ID produced by
// newJobID. Rejecting malformed IDs early keeps them out of Redis keys.
func validJobID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
