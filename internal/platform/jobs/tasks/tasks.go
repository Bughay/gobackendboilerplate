// Package tasks holds the type identifiers and JSON payloads of every
// background job.
//
// It is a leaf package on purpose: it must never import a domain module, the
// auth package, or the jobs package. Payloads carry primitive IDs only, never
// domain structs, so a queued job stays readable after a domain type changes.
package tasks

// ── DEMO JOBS START ─────────────────────────────────────────────────────────
// These two task types belong to the removable internal/modules/demo module.
// To remove the demo, delete this block, internal/modules/demo, the demo worker
// adapter at internal/platform/jobs/handlers/demo.go, and the blocks marked
// "DEMO JOBS" in cmd/api/main.go, cmd/worker/main.go and
// internal/platform/jobs/handlers/register.go.

// Task type constants. The convention is "<domain>:<action>[:v<n>]".
const (
	// TypeDemoHello is the simple demo job.
	TypeDemoHello = "demo:hello"
	// TypeDemoReport is the complex, multi-step demo job.
	TypeDemoReport = "demo:report"
)

// DemoHelloPayload is the JSON body of the simple demo task.
type DemoHelloPayload struct {
	Name string `json:"name"`
}

// DemoReportPayload is the JSON body of the complex demo task. It is a plain
// struct with primitives so it stays valid even if the module's API models
// change later.
type DemoReportPayload struct {
	Steps         int `json:"steps"`
	FailOnAttempt int `json:"fail_on_attempt"`
}

// ── DEMO JOBS END ───────────────────────────────────────────────────────────
