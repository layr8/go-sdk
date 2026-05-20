package scenarios

import "time"

// ScenarioContext is provided to both sender and receiver scenario functions.
type ScenarioContext struct {
	NodeURL  string
	APIKey   string
	TestID   string
	Timeout  time.Duration
	AgentDID string // optional — cloud-node assigns ephemeral DID if empty
}

// SenderContext extends ScenarioContext with the receiver's DID.
type SenderContext struct {
	ScenarioContext
	ReceiverDID string
}

// ScenarioResult is the JSON output from a sender scenario.
type ScenarioResult struct {
	Status     string `json:"status"`
	Scenario   string `json:"scenario"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// ElapsedMs returns milliseconds since start.
func ElapsedMs(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}