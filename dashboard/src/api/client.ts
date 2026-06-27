import {
  APIErrorResponse,
  IssueDetailResponse,
  ProjectSummary,
  ProjectsResponse,
  RefreshResponse,
  RetrySnapshot,
  RunningSnapshot,
  SettingsResponse,
  SettingsUpdateRequest,
  SettingsValidationResponse,
  StateSnapshot,
} from './types'

export async function fetchProjects(): Promise<ProjectsResponse> {
  const response = await fetchJSON<ProjectsResponse>('/api/v1/projects')
  return {
    generated_at: response.generated_at || '',
    projects: (response.projects || []).map(normalizeProjectSummary),
    concurrency: {
      max_concurrent_agents: response.concurrency?.max_concurrent_agents || 0,
      used_agents: response.concurrency?.used_agents || 0,
      available_agents: response.concurrency?.available_agents || 0,
    },
  }
}

export async function fetchState(projectID?: string): Promise<StateSnapshot> {
  return normalizeStateSnapshot(await fetchJSON<StateSnapshot>(projectPath(projectID, 'state')))
}

export async function requestRefresh(projectID?: string): Promise<RefreshResponse> {
  return fetchJSON<RefreshResponse>(projectPath(projectID, 'refresh'), { method: 'POST' })
}

export async function fetchIssueDetail(identifier: string, projectID?: string): Promise<IssueDetailResponse> {
  const path = projectID
    ? `/api/v1/projects/${encodeURIComponent(projectID)}/issues/${encodeURIComponent(identifier)}`
    : `/api/v1/${encodeURIComponent(identifier)}`
  return normalizeIssueDetail(await fetchJSON<IssueDetailResponse>(path))
}

export async function fetchSettings(projectID?: string): Promise<SettingsResponse> {
  return fetchJSON<SettingsResponse>(projectPath(projectID, 'settings'))
}

export async function saveSettings(request: SettingsUpdateRequest, projectID?: string): Promise<SettingsResponse> {
  return fetchJSON<SettingsResponse>(projectPath(projectID, 'settings'), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
}

export async function validateTrackerSettings(request: SettingsUpdateRequest, projectID?: string): Promise<SettingsValidationResponse> {
  return fetchJSON<SettingsValidationResponse>(projectPath(projectID, 'settings/validate-tracker'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
}

function projectPath(projectID: string | undefined, suffix: string) {
  return projectID ? `/api/v1/projects/${encodeURIComponent(projectID)}/${suffix}` : `/api/v1/${suffix}`
}

function normalizeProjectSummary(project: ProjectSummary): ProjectSummary {
  return {
    ...project,
    id: project.id || '',
    name: project.name || project.id || 'Unnamed project',
    workflow_path: project.workflow_path || '',
    enabled: Boolean(project.enabled),
    running: Boolean(project.running),
    last_error: project.last_error || '',
    max_concurrent_agents: project.max_concurrent_agents || 0,
    counts: normalizeCounts(project.counts),
    waiting_on_supervisor: Boolean(project.waiting_on_supervisor),
    last_supervisor_deferred_at: project.last_supervisor_deferred_at || '',
  }
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
    last_dispatch_deferred_reason: snapshot.last_dispatch_deferred_reason || '',
    last_dispatch_deferred_at: snapshot.last_dispatch_deferred_at || '',
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
