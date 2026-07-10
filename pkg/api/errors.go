package api

// Error codes used throughout Simphony, aligned with the Symphony spec.
const (
	ErrMissingWorkflowFile       = "missing_workflow_file"
	ErrWorkflowParseError        = "workflow_parse_error"
	ErrMissingProjectRegistry    = "missing_project_registry_file"
	ErrProjectRegistryParseError = "project_registry_parse_error"
	ErrWorkflowFrontMatterNotMap = "workflow_front_matter_not_a_map"
	ErrTemplateParseError        = "template_parse_error"
	ErrTemplateRenderError       = "template_render_error"
	ErrUnsupportedTrackerKind    = "unsupported_tracker_kind"
	ErrMissingTrackerAPIKey      = "missing_tracker_api_key"
	ErrMissingTrackerProjectSlug = "missing_tracker_project_slug"
	ErrInvalidWorkspaceCWD       = "invalid_workspace_cwd"
	ErrLiteralSecret             = "literal_secret_in_config"
	ErrCodexNotFound             = "codex_not_found"
	ErrResponseTimeout           = "response_timeout"
	ErrTurnTimeout               = "turn_timeout"
	ErrPortExit                  = "port_exit"
	ErrResponseError             = "response_error"
	ErrTurnFailed                = "turn_failed"
	ErrTurnCancelled             = "turn_cancelled"
	ErrTurnInputRequired         = "turn_input_required"
	ErrMaxTurnsReached           = "max_turns_reached"

	// Linear-specific error categories.
	ErrLinearAPIRequest       = "linear_api_request"
	ErrLinearAPIStatus        = "linear_api_status"
	ErrLinearGraphQLErrors    = "linear_graphql_errors"
	ErrLinearUnknownPayload   = "linear_unknown_payload"
	ErrLinearMissingEndCursor = "linear_missing_end_cursor"
)
