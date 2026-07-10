package api

import "time"

// Issue is a normalized issue record used by orchestration, prompt rendering, and observability.
type Issue struct {
	ID          string     `json:"id"`
	Identifier  string     `json:"identifier"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	Priority    *int       `json:"priority"`
	State       string     `json:"state"`
	BranchName  *string    `json:"branch_name"`
	URL         *string    `json:"url"`
	Labels      []string   `json:"labels"`
	BlockedBy   []Blocker  `json:"blocked_by"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

// Blocker represents a blocking relationship.
type Blocker struct {
	ID         *string `json:"id"`
	Identifier *string `json:"identifier"`
	State      *string `json:"state"`
}

// WorkflowDefinition is the parsed WORKFLOW.md payload.
type WorkflowDefinition struct {
	Config         map[string]interface{} `json:"config"`
	PromptTemplate string                 `json:"prompt_template"`
}

// WorkflowConfig holds typed runtime values derived from WorkflowDefinition.Config.
type WorkflowConfig struct {
	Tracker          TrackerConfig          `json:"tracker"`
	Pipeline         PipelineConfig         `json:"pipeline"`
	ReviewResolution ReviewResolutionConfig `json:"review_resolution"`
	Polling          PollingConfig          `json:"polling"`
	Workspace        WorkspaceConfig        `json:"workspace"`
	Hooks            HooksConfig            `json:"hooks"`
	Agent            AgentConfig            `json:"agent"`
	AgentRuntime     AgentRuntimeConfig     `json:"agent_runtime"`
	Codex            CodexConfig            `json:"codex"`
	Claude           ClaudeConfig           `json:"claude,omitempty"`
	Server           *ServerConfig          `json:"server,omitempty"`
}

// TrackerConfig configures the issue tracker integration.
type TrackerConfig struct {
	Kind             string   `json:"kind"`
	Endpoint         string   `json:"endpoint"`
	APIKey           string   `json:"api_key"`
	ProjectSlug      string   `json:"project_slug"`
	ActiveStates     []string `json:"active_states"`
	WorkingState     string   `json:"working_state"`
	TerminalStates   []string `json:"terminal_states"`
	CompletionStates []string `json:"completion_states"`
}

// PipelineConfig configures the issue states used for coding, review, merge, and completion.
type PipelineConfig struct {
	ReviewState           string   `json:"review_state"`
	ReviewResolutionState string   `json:"review_resolution_state,omitempty"`
	MergeState            string   `json:"merge_state"`
	DoneState             string   `json:"done_state"`
	CodingStates          []string `json:"coding_states"`
}

// ReviewResolutionConfig controls the optional autonomous PR review-resolution stage.
type ReviewResolutionConfig struct {
	Enabled                   bool     `json:"enabled"`
	EscalationState           string   `json:"escalation_state,omitempty"`
	MaxAttempts               int      `json:"max_attempts"`
	RequireChecksGreen        bool     `json:"require_checks_green"`
	RequireCodeReviewApproval bool     `json:"require_code_review_approval"`
	UnresolvedCommentPolicy   string   `json:"unresolved_comment_policy"`
	EscalateOn                []string `json:"escalate_on"`
}

// PipelineStage describes the kind of work the agent should perform for a dispatched issue.
type PipelineStage struct {
	Kind         string `json:"kind"`
	Instructions string `json:"instructions,omitempty"`
}

// PollingConfig configures the orchestrator poll loop.
type PollingConfig struct {
	IntervalMs int `json:"interval_ms"`
}

// WorkspaceConfig configures workspace directories.
type WorkspaceConfig struct {
	Root             string `json:"root"`
	Mode             string `json:"mode"`
	Repo             string `json:"repo"`
	BaseBranch       string `json:"base_branch"`
	BranchPrefix     string `json:"branch_prefix"`
	CleanupWorktrees bool   `json:"cleanup_worktrees"`
}

// HooksConfig configures workspace lifecycle hooks.
type HooksConfig struct {
	AfterCreate  *string `json:"after_create"`
	BeforeRun    *string `json:"before_run"`
	AfterRun     *string `json:"after_run"`
	BeforeRemove *string `json:"before_remove"`
	TimeoutMs    int     `json:"timeout_ms"`
}

// AgentConfig configures agent scheduling and limits.
type AgentConfig struct {
	MaxConcurrentAgents        int            `json:"max_concurrent_agents"`
	MaxTurns                   int            `json:"max_turns"`
	MaxRetryBackoffMs          int            `json:"max_retry_backoff_ms"`
	MaxConcurrentAgentsByState map[string]int `json:"max_concurrent_agents_by_state"`
}

// AgentRuntimeConfig selects and configures the coding-agent provider.
type AgentRuntimeConfig struct {
	Provider            string                        `json:"provider"`
	Command             string                        `json:"command"`
	Model               string                        `json:"model,omitempty"`
	ModelProvider       string                        `json:"model_provider,omitempty"`
	ReasoningEffort     string                        `json:"reasoning_effort,omitempty"`
	EndpointURL         string                        `json:"endpoint_url,omitempty"`
	APIKeyConfigured    bool                          `json:"api_key_configured,omitempty"`
	AuthTokenConfigured bool                          `json:"auth_token_configured,omitempty"`
	APIKey              string                        `json:"-"`
	AuthToken           string                        `json:"-"`
	Env                 map[string]string             `json:"env,omitempty"`
	Skills              []AgentSkillRef               `json:"skills,omitempty"`
	AllowedTools        []string                      `json:"allowed_tools,omitempty"`
	DisallowedTools     []string                      `json:"disallowed_tools,omitempty"`
	PermissionMode      string                        `json:"permission_mode,omitempty"`
	SettingSources      []string                      `json:"setting_sources,omitempty"`
	ApprovalPolicy      string                        `json:"approval_policy"`
	ThreadSandbox       string                        `json:"thread_sandbox"`
	TurnSandboxPolicy   string                        `json:"turn_sandbox_policy"`
	TurnTimeoutMs       int                           `json:"turn_timeout_ms"`
	ReadTimeoutMs       int                           `json:"read_timeout_ms"`
	StallTimeoutMs      int                           `json:"stall_timeout_ms"`
	StageOverrides      map[string]AgentStageOverride `json:"stage_overrides,omitempty"`
}

// AgentStageOverride overrides selected runtime settings for a pipeline stage.
type AgentStageOverride struct {
	Provider            string            `json:"provider,omitempty"`
	Command             string            `json:"command,omitempty"`
	Model               string            `json:"model,omitempty"`
	ModelProvider       string            `json:"model_provider,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	EndpointURL         string            `json:"endpoint_url,omitempty"`
	APIKeyConfigured    bool              `json:"api_key_configured,omitempty"`
	AuthTokenConfigured bool              `json:"auth_token_configured,omitempty"`
	APIKey              string            `json:"-"`
	AuthToken           string            `json:"-"`
	Env                 map[string]string `json:"env,omitempty"`
	Skills              []AgentSkillRef   `json:"skills,omitempty"`
	AllowedTools        []string          `json:"allowed_tools,omitempty"`
	DisallowedTools     []string          `json:"disallowed_tools,omitempty"`
	PermissionMode      string            `json:"permission_mode,omitempty"`
	SettingSources      []string          `json:"setting_sources,omitempty"`
	ApprovalPolicy      string            `json:"approval_policy,omitempty"`
	ThreadSandbox       string            `json:"thread_sandbox,omitempty"`
	TurnSandboxPolicy   string            `json:"turn_sandbox_policy,omitempty"`
}

// AgentSkillRef selects an agent skill by name and, when known, absolute path.
type AgentSkillRef struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// CodexConfig configures the Codex app-server client. It aliases the generic
// runtime config so existing API consumers and workflow tests remain compatible.
type CodexConfig = AgentRuntimeConfig

// CodexStageOverride overrides selected Codex settings for a pipeline stage.
type CodexStageOverride = AgentStageOverride

// CodexSkillRef selects a Codex skill by name and, when known, absolute path.
type CodexSkillRef = AgentSkillRef

// ClaudeConfig configures the Claude Code Agent SDK shim.
type ClaudeConfig = AgentRuntimeConfig

// ServerConfig configures the optional HTTP server extension.
type ServerConfig struct {
	Port int `json:"port"`
}

// Workspace represents a filesystem workspace assigned to one issue.
type Workspace struct {
	Path         string `json:"path"`
	WorkspaceKey string `json:"workspace_key"`
	CreatedNow   bool   `json:"created_now"`
}

// RunAttempt represents one execution attempt for one issue.
type RunAttempt struct {
	IssueID         string    `json:"issue_id"`
	IssueIdentifier string    `json:"issue_identifier"`
	Attempt         *int      `json:"attempt"`
	WorkspacePath   string    `json:"workspace_path"`
	StartedAt       time.Time `json:"started_at"`
	Status          string    `json:"status"`
	Error           *string   `json:"error"`
}

// AgentSession tracks state while a coding-agent subprocess is running.
type AgentSession struct {
	SessionID                string     `json:"session_id"`
	ThreadID                 string     `json:"thread_id"`
	TurnID                   string     `json:"turn_id"`
	CodexAppServerPID        *string    `json:"codex_app_server_pid"`
	LastCodexEvent           *string    `json:"last_codex_event"`
	LastCodexTimestamp       *time.Time `json:"last_codex_timestamp"`
	LastCodexMessage         string     `json:"last_codex_message"`
	CodexInputTokens         int        `json:"codex_input_tokens"`
	CodexOutputTokens        int        `json:"codex_output_tokens"`
	CodexTotalTokens         int        `json:"codex_total_tokens"`
	LastReportedInputTokens  int        `json:"last_reported_input_tokens"`
	LastReportedOutputTokens int        `json:"last_reported_output_tokens"`
	LastReportedTotalTokens  int        `json:"last_reported_total_tokens"`
	TurnCount                int        `json:"turn_count"`
}

// RetryEntry represents scheduled retry state for an issue.
type RetryEntry struct {
	IssueID     string      `json:"issue_id"`
	Identifier  string      `json:"identifier"`
	Kind        string      `json:"kind"`
	Attempt     int         `json:"attempt"`
	DueAtMs     int64       `json:"due_at_ms"`
	TimerHandle interface{} `json:"-"` // runtime-specific timer reference
	Error       *string     `json:"error"`
}

// OrchestratorState is the single authoritative in-memory state.
type OrchestratorState struct {
	PollIntervalMs      int                      `json:"poll_interval_ms"`
	MaxConcurrentAgents int                      `json:"max_concurrent_agents"`
	Paused              bool                     `json:"paused"`
	PausedStages        map[string]struct{}      `json:"paused_stages"`
	Running             map[string]*RunningEntry `json:"running"`
	Claimed             map[string]struct{}      `json:"claimed"`
	RetryAttempts       map[string]*RetryEntry   `json:"retry_attempts"`
	Completed           map[string]struct{}      `json:"completed"`
	CodexTotals         CodexTotals              `json:"codex_totals"`
	CodexRateLimits     map[string]interface{}   `json:"codex_rate_limits"`
}

// ControlState reports the operator-controlled soft-pause state.
type ControlState struct {
	Paused       bool     `json:"paused"`
	PausedStages []string `json:"paused_stages"`
}

// RunningEntry tracks an active worker run.
type RunningEntry struct {
	Issue                  Issue         `json:"issue"`
	Session                AgentSession  `json:"session"`
	Stage                  string        `json:"stage,omitempty"`
	ExecutionProvider      string        `json:"execution_provider,omitempty"`
	Model                  string        `json:"model,omitempty"`
	ModelProvider          string        `json:"model_provider,omitempty"`
	StartedAt              time.Time     `json:"started_at"`
	TurnCount              int           `json:"turn_count"`
	WorkspacePath          string        `json:"workspace_path"`
	RecentEvents           []EventDetail `json:"recent_events"`
	SupervisorSlotAcquired bool          `json:"-"`
}

// CodexTotals holds aggregate token and runtime accounting.
type CodexTotals struct {
	InputTokens    int     `json:"input_tokens"`
	OutputTokens   int     `json:"output_tokens"`
	TotalTokens    int     `json:"total_tokens"`
	SecondsRunning float64 `json:"seconds_running"`
}

// StateSnapshot is returned by the optional HTTP API for dashboard consumption.
type StateSnapshot struct {
	GeneratedAt                time.Time              `json:"generated_at"`
	PollIntervalMs             int                    `json:"poll_interval_ms"`
	MaxConcurrentAgents        int                    `json:"max_concurrent_agents"`
	Control                    ControlState           `json:"control"`
	Counts                     StateCounts            `json:"counts"`
	Health                     ProjectHealth          `json:"health"`
	Running                    []RunningSnapshot      `json:"running"`
	Retrying                   []RetrySnapshot        `json:"retrying"`
	CodexTotals                CodexTotals            `json:"codex_totals"`
	RateLimits                 map[string]interface{} `json:"rate_limits"`
	LastDispatchDeferredReason string                 `json:"last_dispatch_deferred_reason,omitempty"`
	LastDispatchDeferredAt     *time.Time             `json:"last_dispatch_deferred_at,omitempty"`
}

// StateCounts provides summary counts.
type StateCounts struct {
	Running   int `json:"running"`
	Retrying  int `json:"retrying"`
	Claimed   int `json:"claimed"`
	Completed int `json:"completed"`
}

// HealthIssue describes one project environment preflight finding.
type HealthIssue struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Detail     string `json:"detail,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ProjectHealth reports whether the local environment can safely dispatch work.
type ProjectHealth struct {
	Status    string        `json:"status"`
	CheckedAt *time.Time    `json:"checked_at,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Issues    []HealthIssue `json:"issues,omitempty"`
}

// RunningSnapshot represents a single running session for the API.
type RunningSnapshot struct {
	IssueID           string        `json:"issue_id"`
	IssueIdentifier   string        `json:"issue_identifier"`
	IssueTitle        string        `json:"issue_title"`
	IssueURL          *string       `json:"issue_url"`
	Priority          *int          `json:"priority"`
	Labels            []string      `json:"labels"`
	State             string        `json:"state"`
	Stage             string        `json:"stage,omitempty"`
	ExecutionProvider string        `json:"execution_provider,omitempty"`
	Model             string        `json:"model,omitempty"`
	ModelProvider     string        `json:"model_provider,omitempty"`
	SessionID         string        `json:"session_id"`
	TurnCount         int           `json:"turn_count"`
	LastEvent         string        `json:"last_event"`
	LastMessage       string        `json:"last_message"`
	StartedAt         time.Time     `json:"started_at"`
	LastEventAt       time.Time     `json:"last_event_at"`
	Tokens            TokenSnapshot `json:"tokens"`
}

// RetrySnapshot represents a single retry queue entry for the API.
type RetrySnapshot struct {
	IssueID         string    `json:"issue_id"`
	IssueIdentifier string    `json:"issue_identifier"`
	Kind            string    `json:"kind"`
	Attempt         int       `json:"attempt"`
	DueAt           time.Time `json:"due_at"`
	Error           *string   `json:"error"`
}

// TokenSnapshot holds token counts for API responses.
type TokenSnapshot struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// IssueDetailResponse provides per-issue runtime/debug details.
type IssueDetailResponse struct {
	IssueIdentifier string                 `json:"issue_identifier"`
	IssueID         string                 `json:"issue_id"`
	Status          string                 `json:"status"`
	Workspace       WorkspaceDetail        `json:"workspace"`
	Attempts        AttemptDetail          `json:"attempts"`
	Running         *RunningSnapshot       `json:"running"`
	Retry           *RetrySnapshot         `json:"retry"`
	Logs            LogDetail              `json:"logs"`
	RecentEvents    []EventDetail          `json:"recent_events"`
	LastError       *string                `json:"last_error"`
	Tracked         map[string]interface{} `json:"tracked"`
}

// WorkspaceDetail is the workspace path wrapper for API responses.
type WorkspaceDetail struct {
	Path string `json:"path"`
}

// AttemptDetail tracks attempt counters for API responses.
type AttemptDetail struct {
	RestartCount        int `json:"restart_count"`
	CurrentRetryAttempt int `json:"current_retry_attempt"`
}

// LogDetail holds log references for API responses.
type LogDetail struct {
	CodexSessionLogs []LogRef `json:"codex_session_logs"`
}

// LogRef is a single log file reference.
type LogRef struct {
	Label string  `json:"label"`
	Path  string  `json:"path"`
	URL   *string `json:"url"`
}

// EventDetail is a recent event for API responses.
type EventDetail struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	Message string    `json:"message"`
}

// RefreshResponse is returned by the refresh trigger endpoint.
type RefreshResponse struct {
	Queued      bool      `json:"queued"`
	Coalesced   bool      `json:"coalesced"`
	RequestedAt time.Time `json:"requested_at"`
	Operations  []string  `json:"operations"`
}

// ProjectSummary describes one configured project runtime in multi-project mode.
type ProjectSummary struct {
	ID                       string        `json:"id"`
	Name                     string        `json:"name"`
	WorkflowPath             string        `json:"workflow_path"`
	Enabled                  bool          `json:"enabled"`
	Running                  bool          `json:"running"`
	LastError                string        `json:"last_error,omitempty"`
	Health                   ProjectHealth `json:"health"`
	MaxConcurrentAgents      int           `json:"max_concurrent_agents,omitempty"`
	Counts                   StateCounts   `json:"counts"`
	Control                  ControlState  `json:"control"`
	WaitingOnSupervisor      bool          `json:"waiting_on_supervisor,omitempty"`
	LastSupervisorDeferredAt *time.Time    `json:"last_supervisor_deferred_at,omitempty"`
	WorkflowWatcherRunning   bool          `json:"workflow_watcher_running"`
	WorkflowWatcherError     string        `json:"workflow_watcher_error,omitempty"`
}

// SupervisorConcurrency reports shared multi-project agent capacity.
type SupervisorConcurrency struct {
	MaxConcurrentAgents int `json:"max_concurrent_agents"`
	UsedAgents          int `json:"used_agents"`
	AvailableAgents     int `json:"available_agents"`
}

// ProjectsResponse lists configured project runtimes.
type ProjectsResponse struct {
	GeneratedAt time.Time             `json:"generated_at"`
	Projects    []ProjectSummary      `json:"projects"`
	Concurrency SupervisorConcurrency `json:"concurrency"`
}

const (
	// RuntimeModeSingleWorkflow identifies a server started from one WORKFLOW.md.
	RuntimeModeSingleWorkflow = "single_workflow"
	// RuntimeModeProjectRegistry identifies a server started from simphony.yaml.
	RuntimeModeProjectRegistry = "project_registry"
)

// RuntimeModeResponse reports the server startup mode.
type RuntimeModeResponse struct {
	Mode                  string `json:"mode"`
	WorkflowPath          string `json:"workflow_path,omitempty"`
	RegistryPath          string `json:"registry_path,omitempty"`
	ChangeRequiresRestart bool   `json:"change_requires_restart"`
}

// RegistryBootstrapResponse reports a generated starter project registry.
type RegistryBootstrapResponse struct {
	RegistryPath string `json:"registry_path"`
	WorkflowPath string `json:"workflow_path"`
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	Command      string `json:"command"`
	Created      bool   `json:"created"`
}

// RegistryProjectCreateRequest appends a project entry to a registry file.
type RegistryProjectCreateRequest struct {
	ID                  string `json:"id"`
	Name                string `json:"name,omitempty"`
	WorkflowPath        string `json:"workflow_path"`
	Enabled             *bool  `json:"enabled,omitempty"`
	MaxConcurrentAgents int    `json:"max_concurrent_agents,omitempty"`
}

// RegistryProjectCreateResponse reports a persisted registry project entry.
type RegistryProjectCreateResponse struct {
	Registry              RegistryResponse       `json:"registry"`
	Project               RegistryProjectSummary `json:"project"`
	Command               string                 `json:"command"`
	ChangeRequiresRestart bool                   `json:"change_requires_restart"`
}

// RegistryProjectUpdateRequest updates an existing registry project entry.
type RegistryProjectUpdateRequest struct {
	Name                string `json:"name,omitempty"`
	WorkflowPath        string `json:"workflow_path"`
	Enabled             *bool  `json:"enabled,omitempty"`
	MaxConcurrentAgents *int   `json:"max_concurrent_agents,omitempty"`
}

// RegistryProjectUpdateResponse reports a persisted registry project update.
type RegistryProjectUpdateResponse struct {
	Registry              RegistryResponse       `json:"registry"`
	Project               RegistryProjectSummary `json:"project"`
	Command               string                 `json:"command"`
	ChangeRequiresRestart bool                   `json:"change_requires_restart"`
}

// RegistryProjectDeleteResponse reports a removed registry project entry.
type RegistryProjectDeleteResponse struct {
	Registry              RegistryResponse `json:"registry"`
	ProjectID             string           `json:"project_id"`
	ProjectName           string           `json:"project_name"`
	Command               string           `json:"command"`
	ChangeRequiresRestart bool             `json:"change_requires_restart"`
}

// RegistryServerUpdateRequest updates aggregate server registry settings.
type RegistryServerUpdateRequest struct {
	BindAddress      *string `json:"bind_address,omitempty"`
	Port             *int    `json:"port,omitempty"`
	DashboardEnabled *bool   `json:"dashboard_enabled,omitempty"`
	APIPrefix        *string `json:"api_prefix,omitempty"`
}

// RegistryConcurrencyUpdateRequest updates supervisor concurrency registry settings.
type RegistryConcurrencyUpdateRequest struct {
	MaxConcurrentAgents               *int `json:"max_concurrent_agents,omitempty"`
	DefaultProjectMaxConcurrentAgents *int `json:"default_project_max_concurrent_agents,omitempty"`
}

// RegistrySecurityUpdateRequest updates multi-project isolation guardrails.
type RegistrySecurityUpdateRequest struct {
	AllowWorkspaceOverlap          *bool `json:"allow_workspace_overlap,omitempty"`
	AllowWorkspaceUnderRegistryDir *bool `json:"allow_workspace_under_registry_dir,omitempty"`
	AllowRemoteDashboard           *bool `json:"allow_remote_dashboard,omitempty"`
}

// RegistryAgentRuntimeUpdateRequest updates global agent runtime defaults.
type RegistryAgentRuntimeUpdateRequest struct {
	Provider        *string  `json:"provider,omitempty"`
	Command         *string  `json:"command,omitempty"`
	Model           *string  `json:"model,omitempty"`
	ModelProvider   *string  `json:"model_provider,omitempty"`
	ReasoningEffort *string  `json:"reasoning_effort,omitempty"`
	EndpointURL     *string  `json:"endpoint_url,omitempty"`
	APIKey          *string  `json:"api_key,omitempty"`
	AuthToken       *string  `json:"auth_token,omitempty"`
	PermissionMode  *string  `json:"permission_mode,omitempty"`
	AllowedTools    []string `json:"allowed_tools,omitempty"`
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	SettingSources  []string `json:"setting_sources,omitempty"`
}

// RegistryUpdateRequest updates non-secret global registry settings.
type RegistryUpdateRequest struct {
	Server       *RegistryServerUpdateRequest       `json:"server,omitempty"`
	Concurrency  *RegistryConcurrencyUpdateRequest  `json:"concurrency,omitempty"`
	Security     *RegistrySecurityUpdateRequest     `json:"security,omitempty"`
	AgentRuntime *RegistryAgentRuntimeUpdateRequest `json:"agent_runtime,omitempty"`
}

// RegistryUpdateResponse reports persisted global registry settings.
type RegistryUpdateResponse struct {
	Registry              RegistryResponse `json:"registry"`
	Command               string           `json:"command"`
	ChangeRequiresRestart bool             `json:"change_requires_restart"`
}

// RegistryServerSummary describes the aggregate multi-project server config.
type RegistryServerSummary struct {
	BindAddress      string `json:"bind_address"`
	Port             int    `json:"port"`
	DashboardEnabled bool   `json:"dashboard_enabled"`
	APIPrefix        string `json:"api_prefix"`
}

// RegistryConcurrencySummary describes supervisor concurrency defaults.
type RegistryConcurrencySummary struct {
	MaxConcurrentAgents               int `json:"max_concurrent_agents"`
	DefaultProjectMaxConcurrentAgents int `json:"default_project_max_concurrent_agents"`
}

// RegistrySecuritySummary describes registry isolation guardrails.
type RegistrySecuritySummary struct {
	AllowWorkspaceOverlap          bool `json:"allow_workspace_overlap"`
	AllowWorkspaceUnderRegistryDir bool `json:"allow_workspace_under_registry_dir"`
	AllowRemoteDashboard           bool `json:"allow_remote_dashboard"`
}

// RegistryAgentRuntimeSummary describes global runtime defaults without secrets.
type RegistryAgentRuntimeSummary struct {
	Configured           bool              `json:"configured"`
	Provider             string            `json:"provider,omitempty"`
	Command              string            `json:"command,omitempty"`
	Model                string            `json:"model,omitempty"`
	ModelProvider        string            `json:"model_provider,omitempty"`
	ReasoningEffort      string            `json:"reasoning_effort,omitempty"`
	EndpointURL          string            `json:"endpoint_url,omitempty"`
	APIKeyConfigured     bool              `json:"api_key_configured,omitempty"`
	AuthTokenConfigured  bool              `json:"auth_token_configured,omitempty"`
	EnvKeys              []string          `json:"env_keys,omitempty"`
	StageOverrideKeys    []string          `json:"stage_override_keys,omitempty"`
	PermissionMode       string            `json:"permission_mode,omitempty"`
	AllowedTools         []string          `json:"allowed_tools,omitempty"`
	DisallowedTools      []string          `json:"disallowed_tools,omitempty"`
	SettingSources       []string          `json:"setting_sources,omitempty"`
	AdditionalProperties map[string]string `json:"additional_properties,omitempty"`
}

// RegistryProjectSummary describes one configured project entry.
type RegistryProjectSummary struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	WorkflowPath        string `json:"workflow_path"`
	Enabled             bool   `json:"enabled"`
	MaxConcurrentAgents int    `json:"max_concurrent_agents,omitempty"`
}

// RegistryWarningSummary is a non-fatal registry validation warning.
type RegistryWarningSummary struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	ProjectIDs []string `json:"project_ids,omitempty"`
}

// RegistryResponse returns the active multi-project registry without secrets.
type RegistryResponse struct {
	GeneratedAt  time.Time                   `json:"generated_at"`
	SourcePath   string                      `json:"source_path"`
	Server       *RegistryServerSummary      `json:"server,omitempty"`
	Concurrency  RegistryConcurrencySummary  `json:"concurrency"`
	Security     RegistrySecuritySummary     `json:"security"`
	AgentRuntime RegistryAgentRuntimeSummary `json:"agent_runtime"`
	Projects     []RegistryProjectSummary    `json:"projects"`
	Warnings     []RegistryWarningSummary    `json:"warnings,omitempty"`
}

// APIError is a structured API error.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIErrorResponse wraps APIError for JSON responses.
type APIErrorResponse struct {
	Error APIError `json:"error"`
}

// SettingsResponse returns editable and resolved workflow settings.
type SettingsResponse struct {
	WorkflowPath    string                 `json:"workflow_path"`
	Config          map[string]interface{} `json:"config"`
	ResolvedConfig  WorkflowConfig         `json:"resolved_config"`
	PromptTemplate  string                 `json:"prompt_template"`
	ValidationError *string                `json:"validation_error,omitempty"`
}

// SettingsUpdateRequest updates WORKFLOW.md front matter and prompt template.
type SettingsUpdateRequest struct {
	Config         map[string]interface{} `json:"config"`
	PromptTemplate *string                `json:"prompt_template"`
}

// SettingsValidationResponse reports the result of validating editable settings.
type SettingsValidationResponse struct {
	OK             bool     `json:"ok"`
	ProjectSlug    string   `json:"project_slug,omitempty"`
	ActiveStates   []string `json:"active_states,omitempty"`
	CandidateCount int      `json:"candidate_count"`
	Message        string   `json:"message,omitempty"`
}

// ModelCatalogEntry is a provider-advertised model available for selection.
type ModelCatalogEntry struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ModelCatalogResponse reports the latest models returned by a configured provider.
type ModelCatalogResponse struct {
	Provider          string              `json:"provider"`
	ExecutionProvider string              `json:"execution_provider,omitempty"`
	Stage             string              `json:"stage,omitempty"`
	EndpointURL       string              `json:"endpoint_url"`
	RefreshedAt       time.Time           `json:"refreshed_at"`
	Models            []ModelCatalogEntry `json:"models"`
}
