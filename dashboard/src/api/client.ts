import {
  APIErrorResponse,
  IssueDetailResponse,
  RefreshResponse,
  RetrySnapshot,
  RunningSnapshot,
  SettingsResponse,
  SettingsUpdateRequest,
  StateSnapshot,
} from './types'

export async function fetchState(): Promise<StateSnapshot> {
  return normalizeStateSnapshot(await fetchJSON<StateSnapshot>('/api/v1/state'))
}

export async function requestRefresh(): Promise<RefreshResponse> {
  return fetchJSON<RefreshResponse>('/api/v1/refresh', { method: 'POST' })
}

export async function fetchIssueDetail(identifier: string): Promise<IssueDetailResponse> {
  return normalizeIssueDetail(await fetchJSON<IssueDetailResponse>(`/api/v1/${encodeURIComponent(identifier)}`))
}

export async function fetchSettings(): Promise<SettingsResponse> {
  return fetchJSON<SettingsResponse>('/api/v1/settings')
}

export async function saveSettings(request: SettingsUpdateRequest): Promise<SettingsResponse> {
  return fetchJSON<SettingsResponse>('/api/v1/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
}

function normalizeStateSnapshot(snapshot: StateSnapshot): StateSnapshot {
  return {
    ...snapshot,
    poll_interval_ms: snapshot.poll_interval_ms || 0,
    max_concurrent_agents: snapshot.max_concurrent_agents || 0,
    counts: normalizeCounts(snapshot.counts),
    running: (snapshot.running || []).map(normalizeRunningSnapshot),
    retrying: (snapshot.retrying || []).map(normalizeRetrySnapshot),
    codex_totals: snapshot.codex_totals || { input_tokens: 0, output_tokens: 0, total_tokens: 0, seconds_running: 0 },
    rate_limits: snapshot.rate_limits || null,
  }
}

function normalizeCounts(counts: StateSnapshot['counts'] | undefined): StateSnapshot['counts'] {
  return {
    running: counts?.running || 0,
    retrying: counts?.retrying || 0,
    claimed: counts?.claimed || 0,
    completed: counts?.completed || 0,
  }
}

function normalizeIssueDetail(detail: IssueDetailResponse): IssueDetailResponse {
  return {
    ...detail,
    workspace: detail.workspace || { path: '' },
    attempts: detail.attempts || { restart_count: 0, current_retry_attempt: 0 },
    running: detail.running ? normalizeRunningSnapshot(detail.running) : null,
    retry: detail.retry ? normalizeRetrySnapshot(detail.retry) : null,
    logs: { codex_session_logs: detail.logs?.codex_session_logs || [] },
    recent_events: detail.recent_events || [],
    last_error: detail.last_error || null,
    tracked: detail.tracked || {},
  }
}

function normalizeRunningSnapshot(snapshot: RunningSnapshot): RunningSnapshot {
  return {
    ...snapshot,
    issue_title: snapshot.issue_title || '',
    issue_url: snapshot.issue_url || null,
    priority: snapshot.priority ?? null,
    labels: snapshot.labels || [],
    tokens: snapshot.tokens || { input_tokens: 0, output_tokens: 0, total_tokens: 0 },
  }
}

function normalizeRetrySnapshot(snapshot: RetrySnapshot): RetrySnapshot {
  return {
    ...snapshot,
    issue_id: snapshot.issue_id || '',
    issue_identifier: snapshot.issue_identifier || '',
    attempt: snapshot.attempt || 0,
    due_at: snapshot.due_at || '',
    error: snapshot.error || null,
  }
}

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { ...init, cache: 'no-store' })
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response))
  }
  return (await response.json()) as T
}

async function responseErrorMessage(response: Response) {
  const fallback = `Request failed: ${response.status}`
  const contentType = response.headers.get('content-type') || ''
  if (!contentType.includes('application/json')) {
    return fallback
  }

  try {
    const body = (await response.json()) as Partial<APIErrorResponse>
    return body.error?.message || body.error?.code || fallback
  } catch {
    return fallback
  }
}
