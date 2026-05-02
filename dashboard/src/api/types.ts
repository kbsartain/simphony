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
}

export interface TokenSnapshot {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface RunningSnapshot {
  issue_id: string;
  issue_identifier: string;
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
  counts: StateCounts;
  running: RunningSnapshot[];
  retrying: RetrySnapshot[];
  codex_totals: CodexTotals;
  rate_limits: Record<string, unknown> | null;
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
