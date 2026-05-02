package api

import "time"

// AgentEvent is emitted by the agent runner to the orchestrator callback.
type AgentEvent struct {
	Event     string                 `json:"event"`
	Timestamp time.Time              `json:"timestamp"`
	CodexPID  *string                `json:"codex_app_server_pid,omitempty"`
	Usage     map[string]interface{} `json:"usage,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// ContinueDecision is returned by the orchestrator after a completed turn.
// It tells the runner whether to start another turn on the same Codex thread.
type ContinueDecision struct {
	Issue    Issue
	Continue bool
	Reason   string
}
