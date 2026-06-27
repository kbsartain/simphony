export interface Blocker {
  id: string | null;
  identifier: string | null;
  state: string | null;
}

export interface Issue {
  id: string;
  identifier: string;
  title: string;
  description: string | null;
  priority: number | null;
  state: string;
  branch_name: string | null;
  url: string | null;
  labels: string[];
  blocked_by: Blocker[];
  created_at: string | null;
  updated_at: string | null;
}

export interface StateCounts {
  running: number;
  retrying: number;
  claimed: number;
  completed: number;
}

export interface TokenSnapshot {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface RunningSnapshot {
  issue_id: string;
  issue_identifier: string;
  issue_title: string;
  issue_url: string | null;
  priority: number | null;
  labels: string[];
  state: string;
  session_id: string;
  turn_count: number;
  last_event: string;
  last_message: string;
  started_at: string;
  last_event_at: string;
  tokens: TokenSnapshot;
}

export interface RetrySnapshot {
  issue_id: string;
  issue_identifier: string;
  kind: string;
  attempt: number;
  due_at: string;
  error: string | null;
}

export interface CodexTotals {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  seconds_running: number;
}

export interface StateSnapshot {
  generated_at: string;
  poll_interval_ms: number;
  max_concurrent_agents: number;
  counts: StateCounts;
  running: RunningSnapshot[];
  retrying: RetrySnapshot[];
  codex_totals: CodexTotals;
  rate_limits: Record<string, unknown> | null;
  last_dispatch_deferred_reason?: string;
  last_dispatch_deferred_at?: string;
}

export interface APIError {
  code: string;
  message: string;
}

export interface APIErrorResponse {
  error: APIError;
}

export interface RefreshResponse {
  queued: boolean;
  coalesced: boolean;
  requested_at: string;
  operations: string[];
}

export interface ProjectSummary {
  id: string;
  name: string;
  workflow_path: string;
  enabled: boolean;
  running: boolean;
  last_error?: string;
  max_concurrent_agents?: number;
  counts: StateCounts;
  waiting_on_supervisor?: boolean;
  last_supervisor_deferred_at?: string;
}

export interface SupervisorConcurrency {
  max_concurrent_agents: number;
  used_agents: number;
  available_agents: number;
}

export interface ProjectsResponse {
  generated_at: string;
  projects: ProjectSummary[];
  concurrency: SupervisorConcurrency;
}

export interface RuntimeModeResponse {
  mode: 'single_workflow' | 'project_registry' | string;
  workflow_path?: string;
  registry_path?: string;
  change_requires_restart: boolean;
}

export interface RegistryBootstrapResponse {
  registry_path: string;
  workflow_path: string;
  project_id: string;
  project_name: string;
  command: string;
  created: boolean;
}

export interface RegistryProjectCreateRequest {
  id: string;
  name?: string;
  workflow_path: string;
  enabled?: boolean;
  max_concurrent_agents?: number;
}

export interface RegistryProjectCreateResponse {
  registry: RegistryResponse;
  project: RegistryProjectSummary;
  command: string;
  change_requires_restart: boolean;
}

export interface RegistryProjectUpdateRequest {
  name?: string;
  workflow_path: string;
  enabled?: boolean;
  max_concurrent_agents?: number;
}

export interface RegistryProjectUpdateResponse {
  registry: RegistryResponse;
  project: RegistryProjectSummary;
  command: string;
  change_requires_restart: boolean;
}

export interface RegistryProjectDeleteResponse {
  registry: RegistryResponse;
  project_id: string;
  project_name: string;
  command: string;
  change_requires_restart: boolean;
}

export interface RegistryServerSummary {
  bind_address: string;
  port: number;
  dashboard_enabled: boolean;
  api_prefix: string;
}

export interface RegistryConcurrencySummary {
  max_concurrent_agents: number;
  default_project_max_concurrent_agents: number;
}

export interface RegistrySecuritySummary {
  allow_workspace_overlap: boolean;
  allow_workspace_under_registry_dir: boolean;
  allow_remote_dashboard: boolean;
}

export interface RegistryAgentRuntimeSummary {
  configured: boolean;
  provider?: string;
  command?: string;
  model?: string;
  model_provider?: string;
  reasoning_effort?: string;
  endpoint_url?: string;
  api_key_configured?: boolean;
  auth_token_configured?: boolean;
  env_keys?: string[];
  stage_override_keys?: string[];
  permission_mode?: string;
  allowed_tools?: string[];
  disallowed_tools?: string[];
  setting_sources?: string[];
}

export interface RegistryProjectSummary {
  id: string;
  name: string;
  workflow_path: string;
  enabled: boolean;
  max_concurrent_agents?: number;
}

export interface RegistryWarningSummary {
  code: string;
  message: string;
  project_ids?: string[];
}

export interface RegistryResponse {
  generated_at: string;
  source_path: string;
  server?: RegistryServerSummary;
  concurrency: RegistryConcurrencySummary;
  security: RegistrySecuritySummary;
  agent_runtime: RegistryAgentRuntimeSummary;
  projects: RegistryProjectSummary[];
  warnings?: RegistryWarningSummary[];
}

export interface IssueDetailResponse {
  issue_identifier: string;
  issue_id: string;
  status: string;
  workspace: { path: string };
  attempts: { restart_count: number; current_retry_attempt: number };
  running: RunningSnapshot | null;
  retry: RetrySnapshot | null;
  logs: { codex_session_logs: { label: string; path: string; url: string | null }[] };
  recent_events: { at: string; event: string; message: string }[];
  last_error: string | null;
  tracked: Record<string, unknown>;
}

export interface TrackerConfig {
  kind: string;
  endpoint: string;
  api_key: string;
  project_slug: string;
  active_states: string[];
  working_state: string;
  terminal_states: string[];
  completion_states: string[];
}

export interface PipelineConfig {
  review_state: string;
  review_resolution_state?: string;
  merge_state: string;
  done_state: string;
  coding_states: string[];
}

export interface ReviewResolutionConfig {
  enabled: boolean;
  escalation_state?: string;
  max_attempts: number;
  require_checks_green: boolean;
  require_code_review_approval: boolean;
  unresolved_comment_policy: string;
  escalate_on: string[];
}

export interface PollingConfig {
  interval_ms: number;
}

export interface WorkspaceConfig {
  root: string;
  mode: string;
  repo: string;
  base_branch: string;
  branch_prefix: string;
  cleanup_worktrees: boolean;
}

export interface HooksConfig {
  after_create: string | null;
  before_run: string | null;
  after_run: string | null;
  before_remove: string | null;
  timeout_ms: number;
}

export interface AgentConfig {
  max_concurrent_agents: number;
  max_concurrent_agents_by_state: Record<string, number>;
  max_turns: number;
  max_retry_backoff_ms: number;
}

export interface AgentRuntimeConfig {
  provider: string;
  command: string;
  model?: string;
  model_provider?: string;
  reasoning_effort?: string;
  endpoint_url?: string;
  api_key_configured?: boolean;
  auth_token_configured?: boolean;
  env?: Record<string, string>;
  skills?: CodexSkillRef[];
  allowed_tools?: string[];
  disallowed_tools?: string[];
  permission_mode?: string;
  setting_sources?: string[];
  approval_policy: string;
  thread_sandbox: string;
  turn_sandbox_policy: string;
  turn_timeout_ms: number;
  read_timeout_ms: number;
  stall_timeout_ms: number;
  stage_overrides?: Record<string, CodexStageOverride>;
}

export type CodexConfig = AgentRuntimeConfig;
export type ClaudeConfig = AgentRuntimeConfig;

export interface CodexStageOverride {
  model?: string;
  model_provider?: string;
  reasoning_effort?: string;
  skills?: CodexSkillRef[];
}

export interface CodexSkillRef {
  name: string;
  path?: string;
}

export interface ServerConfig {
  port: number;
}

export interface WorkflowConfig {
  tracker: TrackerConfig;
  pipeline: PipelineConfig;
  review_resolution: ReviewResolutionConfig;
  polling: PollingConfig;
  workspace: WorkspaceConfig;
  hooks: HooksConfig;
  agent: AgentConfig;
  agent_runtime: AgentRuntimeConfig;
  codex: CodexConfig;
  claude?: ClaudeConfig;
  server?: ServerConfig;
}

export interface SettingsResponse {
  workflow_path: string;
  config: Record<string, unknown>;
  resolved_config: WorkflowConfig;
  prompt_template: string;
  validation_error?: string;
}

export interface SettingsUpdateRequest {
  config: Record<string, unknown>;
  prompt_template?: string;
}

export interface SettingsValidationResponse {
  ok: boolean;
  project_slug?: string;
  active_states?: string[];
  candidate_count: number;
  message?: string;
}
