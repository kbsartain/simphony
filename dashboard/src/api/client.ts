import {
  APIErrorResponse,
  IssueDetailResponse,
  RegistryBootstrapResponse,
  RegistryProjectCreateRequest,
  RegistryProjectCreateResponse,
  RegistryProjectDeleteResponse,
  RegistryProjectUpdateRequest,
  RegistryProjectUpdateResponse,
  RegistryUpdateRequest,
  RegistryUpdateResponse,
  ProjectSummary,
  ProjectsResponse,
  RegistryResponse,
  RefreshResponse,
  RetrySnapshot,
  RunningSnapshot,
  RuntimeModeResponse,
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

export async function fetchRegistry(): Promise<RegistryResponse> {
  return normalizeRegistry(await fetchJSON<RegistryResponse>('/api/v1/registry'))
}

export async function fetchRuntimeMode(): Promise<RuntimeModeResponse> {
  const mode = await fetchJSON<RuntimeModeResponse>('/api/v1/runtime-mode')
  return {
    mode: mode.mode || 'single_workflow',
    workflow_path: mode.workflow_path || '',
    registry_path: mode.registry_path || '',
    change_requires_restart: Boolean(mode.change_requires_restart),
  }
}

export async function bootstrapRegistry(): Promise<RegistryBootstrapResponse> {
  const response = await fetchJSON<RegistryBootstrapResponse>('/api/v1/registry/bootstrap', { method: 'POST' })
  return {
    registry_path: response.registry_path || '',
    workflow_path: response.workflow_path || '',
    project_id: response.project_id || '',
    project_name: response.project_name || '',
    command: response.command || '',
    created: Boolean(response.created),
  }
}

export async function createRegistryProject(request: RegistryProjectCreateRequest): Promise<RegistryProjectCreateResponse> {
  const response = await fetchJSON<RegistryProjectCreateResponse>('/api/v1/registry/projects', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  return {
    registry: normalizeRegistry(response.registry),
    project: {
      id: response.project?.id || '',
      name: response.project?.name || response.project?.id || 'Unnamed project',
      workflow_path: response.project?.workflow_path || '',
      enabled: Boolean(response.project?.enabled),
      max_concurrent_agents: response.project?.max_concurrent_agents || 0,
    },
    command: response.command || '',
    change_requires_restart: Boolean(response.change_requires_restart),
  }
}

export async function updateRegistryProject(projectID: string, request: RegistryProjectUpdateRequest): Promise<RegistryProjectUpdateResponse> {
  const response = await fetchJSON<RegistryProjectUpdateResponse>(`/api/v1/registry/projects/${encodeURIComponent(projectID)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  return {
    registry: normalizeRegistry(response.registry),
    project: normalizeRegistryProject(response.project),
    command: response.command || '',
    change_requires_restart: Boolean(response.change_requires_restart),
  }
}

export async function deleteRegistryProject(projectID: string): Promise<RegistryProjectDeleteResponse> {
  const response = await fetchJSON<RegistryProjectDeleteResponse>(`/api/v1/registry/projects/${encodeURIComponent(projectID)}`, {
    method: 'DELETE',
  })
  return {
    registry: normalizeRegistry(response.registry),
    project_id: response.project_id || projectID,
    project_name: response.project_name || response.project_id || projectID,
    command: response.command || '',
    change_requires_restart: Boolean(response.change_requires_restart),
  }
}

export async function updateRegistrySettings(request: RegistryUpdateRequest): Promise<RegistryUpdateResponse> {
  const response = await fetchJSON<RegistryUpdateResponse>('/api/v1/registry', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  return {
    registry: normalizeRegistry(response.registry),
    command: response.command || '',
    change_requires_restart: Boolean(response.change_requires_restart),
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
    workflow_watcher_running: Boolean(project.workflow_watcher_running),
    workflow_watcher_error: project.workflow_watcher_error || '',
  }
}

function normalizeRegistry(registry: RegistryResponse): RegistryResponse {
  return {
    ...registry,
    generated_at: registry.generated_at || '',
    source_path: registry.source_path || '',
    server: registry.server
      ? {
          bind_address: registry.server.bind_address || '',
          port: registry.server.port || 0,
          dashboard_enabled: Boolean(registry.server.dashboard_enabled),
          api_prefix: registry.server.api_prefix || '/api/v1',
        }
      : undefined,
    concurrency: {
      max_concurrent_agents: registry.concurrency?.max_concurrent_agents || 0,
      default_project_max_concurrent_agents: registry.concurrency?.default_project_max_concurrent_agents || 0,
    },
    security: {
      allow_workspace_overlap: Boolean(registry.security?.allow_workspace_overlap),
      allow_workspace_under_registry_dir: Boolean(registry.security?.allow_workspace_under_registry_dir),
      allow_remote_dashboard: Boolean(registry.security?.allow_remote_dashboard),
    },
    agent_runtime: {
      ...registry.agent_runtime,
      configured: Boolean(registry.agent_runtime?.configured),
      env_keys: registry.agent_runtime?.env_keys || [],
      stage_override_keys: registry.agent_runtime?.stage_override_keys || [],
      allowed_tools: registry.agent_runtime?.allowed_tools || [],
      disallowed_tools: registry.agent_runtime?.disallowed_tools || [],
      setting_sources: registry.agent_runtime?.setting_sources || [],
    },
    projects: (registry.projects || []).map(normalizeRegistryProject),
    warnings: registry.warnings || [],
  }
}

function normalizeRegistryProject(project: RegistryResponse['projects'][number]) {
  return {
    id: project?.id || '',
    name: project?.name || project?.id || 'Unnamed project',
    workflow_path: project?.workflow_path || '',
    enabled: Boolean(project?.enabled),
    max_concurrent_agents: project?.max_concurrent_agents || 0,
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
