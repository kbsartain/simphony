import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  IssueDetailResponse,
  ProjectSummary,
  RegistryBootstrapResponse,
  RegistryProjectCreateResponse,
  RegistryProjectDeleteResponse,
  RegistryProjectUpdateResponse,
  RegistryResponse,
  RegistryUpdateResponse,
  RefreshResponse,
  RetrySnapshot,
  RunningSnapshot,
  RuntimeModeResponse,
  SettingsResponse,
  SettingsValidationResponse,
  StateSnapshot,
  SupervisorConcurrency,
} from './api/types'
import {
  bootstrapRegistry,
  createRegistryProject,
  deleteRegistryProject,
  fetchIssueDetail,
  fetchProjects,
  fetchRegistry,
  fetchRuntimeMode,
  fetchSettings,
  fetchState,
  requestRefresh as requestRefreshAPI,
  saveSettings,
  updateRegistrySettings,
  updateRegistryProject,
  validateTrackerSettings,
} from './api/client'
import './App.css'

const DEFAULT_POLL_INTERVAL_MS = 5000
const MIN_UI_POLL_INTERVAL_MS = 1000

type QueueFilter = 'all' | 'running' | 'retrying'
type Page = 'runtime' | 'settings' | 'setup'
type ActivityItem = {
  id: string
  label: string
  tone: 'running' | 'retrying'
  title: string
  body: string
  at: string
}
type DetailState =
  | { status: 'idle' }
  | { status: 'loading'; identifier: string }
  | { status: 'ready'; detail: IssueDetailResponse }
  | { status: 'error'; identifier: string; message: string }
type ModelOption = {
  id: string
  label: string
  model: string
  modelProvider: string
  reasoning?: ReasoningOption[]
}
type ProviderOption = {
  id: string
  label: string
  models: ModelOption[]
  reasoning: ReasoningOption[]
}
type ReasoningOption = {
  value: string
  label: string
}
type SkillStageOption = {
  id: string
  label: string
}
type ProjectOverview = {
  runningProjects: number
  disabledProjects: number
  errorProjects: number
  waitingProjects: number
  runningIssues: number
  retryingIssues: number
  supervisorCapacity: number
  supervisorUsed: number
  supervisorAvailable: number
}
type RegistryProjectDraft = {
  id: string
  name: string
  workflowPath: string
  enabled: boolean
  maxConcurrentAgents: string
}
type RegistrySettingsDraft = {
  bindAddress: string
  port: string
  dashboardEnabled: boolean
  apiPrefix: string
  maxConcurrentAgents: string
  defaultProjectMaxConcurrentAgents: string
  allowWorkspaceOverlap: boolean
  allowWorkspaceUnderRegistryDir: boolean
  allowRemoteDashboard: boolean
}
type RegistryRuntimeDraft = {
  sdkProvider: string
  command: string
  modelProvider: string
  model: string
  reasoningEffort: string
  endpointURL: string
  apiKey: string
  authToken: string
  permissionMode: string
  allowedTools: string
  disallowedTools: string
  settingSources: string
}

const DEFAULT_REASONING_OPTIONS: ReasoningOption[] = [{ value: '', label: 'Provider default' }]
const CODEX_REASONING_OPTIONS: ReasoningOption[] = [
  { value: '', label: 'Provider default' },
  { value: 'none', label: 'None' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'X-High' },
]
const THINKING_REASONING_OPTIONS: ReasoningOption[] = [
  { value: '', label: 'Provider default' },
  { value: 'low', label: 'Thinking budget: low' },
  { value: 'medium', label: 'Thinking budget: medium' },
  { value: 'high', label: 'Thinking budget: high' },
]
const GEMINI_3_PRO_REASONING_OPTIONS: ReasoningOption[] = [
  { value: '', label: 'Gemini default' },
  { value: 'low', label: 'Thinking level: low' },
  { value: 'high', label: 'Thinking level: high' },
]
const GEMINI_25_PRO_REASONING_OPTIONS: ReasoningOption[] = [
  { value: '', label: 'Dynamic thinking' },
  { value: 'minimal', label: 'Thinking budget: minimal' },
  { value: 'low', label: 'Thinking budget: low' },
  { value: 'medium', label: 'Thinking budget: medium' },
  { value: 'high', label: 'Thinking budget: high' },
]
const GEMINI_25_FLASH_REASONING_OPTIONS: ReasoningOption[] = [
  { value: '', label: 'Dynamic thinking' },
  { value: 'none', label: 'Thinking off' },
  { value: 'minimal', label: 'Thinking budget: minimal' },
  { value: 'low', label: 'Thinking budget: low' },
  { value: 'medium', label: 'Thinking budget: medium' },
  { value: 'high', label: 'Thinking budget: high' },
]
const REASONER_MODEL_OPTIONS: ReasoningOption[] = [
  { value: '', label: 'Model default' },
  { value: 'high', label: 'Reasoner mode' },
]
const PROVIDER_OPTIONS: ProviderOption[] = [
  {
    id: 'openai',
    label: 'OpenAI',
    reasoning: CODEX_REASONING_OPTIONS,
    models: [
      { id: 'openai:gpt-5.5', label: 'GPT-5.5', model: 'gpt-5.5', modelProvider: 'openai' },
      { id: 'openai:gpt-5.4', label: 'GPT-5.4', model: 'gpt-5.4', modelProvider: 'openai' },
      { id: 'openai:gpt-5.4-mini', label: 'GPT-5.4 Mini', model: 'gpt-5.4-mini', modelProvider: 'openai' },
      { id: 'openai:gpt-5.3-codex', label: 'GPT-5.3 Codex', model: 'gpt-5.3-codex', modelProvider: 'openai' },
    ],
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    reasoning: THINKING_REASONING_OPTIONS,
    models: [
      { id: 'anthropic:claude-opus-4.7', label: 'Claude Opus 4.7', model: 'claude-opus-4.7', modelProvider: 'anthropic' },
      { id: 'anthropic:claude-sonnet-4.6', label: 'Claude Sonnet 4.6', model: 'claude-sonnet-4.6', modelProvider: 'anthropic' },
    ],
  },
  {
    id: 'google',
    label: 'Google Gemini',
    reasoning: THINKING_REASONING_OPTIONS,
    models: [
      {
        id: 'google:gemini-3-pro',
        label: 'Gemini 3 Pro',
        model: 'gemini-3-pro',
        modelProvider: 'google',
        reasoning: GEMINI_3_PRO_REASONING_OPTIONS,
      },
      {
        id: 'google:gemini-2.5-pro',
        label: 'Gemini 2.5 Pro',
        model: 'gemini-2.5-pro',
        modelProvider: 'google',
        reasoning: GEMINI_25_PRO_REASONING_OPTIONS,
      },
      {
        id: 'google:gemini-2.5-flash',
        label: 'Gemini 2.5 Flash',
        model: 'gemini-2.5-flash',
        modelProvider: 'google',
        reasoning: GEMINI_25_FLASH_REASONING_OPTIONS,
      },
    ],
  },
  {
    id: 'moonshot',
    label: 'Moonshot',
    reasoning: THINKING_REASONING_OPTIONS,
    models: [{ id: 'moonshot:kimi-k2-2.6', label: 'Kimi K2 2.6', model: 'kimi-k2-2.6', modelProvider: 'moonshot' }],
  },
  {
    id: 'zai',
    label: 'Z.ai',
    reasoning: THINKING_REASONING_OPTIONS,
    models: [
      { id: 'zai:glm-5.1', label: 'GLM 5.1', model: 'glm-5.1', modelProvider: 'zai' },
      { id: 'zai:glm-4.7', label: 'GLM 4.7', model: 'glm-4.7', modelProvider: 'zai' },
    ],
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    reasoning: DEFAULT_REASONING_OPTIONS,
    models: [
      { id: 'deepseek:deepseek-v4-pro', label: 'DeepSeek V4 Pro', model: 'deepseek-v4-pro', modelProvider: 'deepseek' },
      { id: 'deepseek:deepseek-v4-flash', label: 'DeepSeek V4 Flash', model: 'deepseek-v4-flash', modelProvider: 'deepseek' },
      { id: 'deepseek:deepseek-chat', label: 'DeepSeek Chat (legacy)', model: 'deepseek-chat', modelProvider: 'deepseek' },
      {
        id: 'deepseek:deepseek-reasoner',
        label: 'DeepSeek Reasoner (legacy)',
        model: 'deepseek-reasoner',
        modelProvider: 'deepseek',
        reasoning: REASONER_MODEL_OPTIONS,
      },
    ],
  },
]
const MODEL_OPTIONS = PROVIDER_OPTIONS.flatMap(provider => provider.models)
const SKILL_STAGE_OPTIONS: SkillStageOption[] = [
  { id: 'coding', label: 'Coding' },
  { id: 'review', label: 'In Review' },
  { id: 'review_resolution', label: 'Review Resolution' },
  { id: 'merge', label: 'Merge' },
]

function App() {
  const [page, setPage] = useState<Page>(() => initialDashboardLocation().page)
  const [projectDiscoveryComplete, setProjectDiscoveryComplete] = useState(false)
  const [projectMode, setProjectMode] = useState(false)
  const [projects, setProjects] = useState<ProjectSummary[]>([])
  const [registry, setRegistry] = useState<RegistryResponse | null>(null)
  const [runtimeMode, setRuntimeMode] = useState<RuntimeModeResponse | null>(null)
  const [registryBootstrap, setRegistryBootstrap] = useState<RegistryBootstrapResponse | null>(null)
  const [registryProjectResult, setRegistryProjectResult] = useState<RegistryProjectCreateResponse | null>(null)
  const [registryProjectUpdateResult, setRegistryProjectUpdateResult] = useState<RegistryProjectUpdateResponse | null>(null)
  const [registryProjectDeleteResult, setRegistryProjectDeleteResult] = useState<RegistryProjectDeleteResponse | null>(null)
  const [registrySettingsResult, setRegistrySettingsResult] = useState<RegistryUpdateResponse | null>(null)
  const [registryRuntimeResult, setRegistryRuntimeResult] = useState<RegistryUpdateResponse | null>(null)
  const [editingRegistryProjectId, setEditingRegistryProjectId] = useState<string | null>(null)
  const [registryProjectDraft, setRegistryProjectDraft] = useState<RegistryProjectDraft>({
    id: '',
    name: '',
    workflowPath: '',
    enabled: true,
    maxConcurrentAgents: '',
  })
  const [registrySettingsDraft, setRegistrySettingsDraft] = useState<RegistrySettingsDraft>(emptyRegistrySettingsDraft())
  const [registryRuntimeDraft, setRegistryRuntimeDraft] = useState<RegistryRuntimeDraft>(emptyRegistryRuntimeDraft())
  const [supervisorConcurrency, setSupervisorConcurrency] = useState<SupervisorConcurrency | null>(null)
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(() => initialDashboardLocation().projectId)
  const [state, setState] = useState<StateSnapshot | null>(null)
  const [settings, setSettings] = useState<SettingsResponse | null>(null)
  const [settingsDraft, setSettingsDraft] = useState('')
  const [promptDraft, setPromptDraft] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [bootstrappingRegistry, setBootstrappingRegistry] = useState(false)
  const [creatingRegistryProject, setCreatingRegistryProject] = useState(false)
  const [savingRegistrySettings, setSavingRegistrySettings] = useState(false)
  const [savingRegistryRuntime, setSavingRegistryRuntime] = useState(false)
  const [deletingRegistryProjectId, setDeletingRegistryProjectId] = useState<string | null>(null)
  const [savingSettings, setSavingSettings] = useState(false)
  const [validatingTracker, setValidatingTracker] = useState(false)
  const [trackerValidation, setTrackerValidation] = useState<SettingsValidationResponse | null>(null)
  const [refreshResult, setRefreshResult] = useState<RefreshResponse | null>(null)
  const [filter, setFilter] = useState<QueueFilter>('all')
  const [query, setQuery] = useState('')
  const [detailState, setDetailState] = useState<DetailState>({ status: 'idle' })
  const stateRequestID = useRef(0)
  const detailRequestID = useRef(0)
  const pollIntervalMs = Math.max(state?.poll_interval_ms || DEFAULT_POLL_INTERVAL_MS, MIN_UI_POLL_INTERVAL_MS)
  const selectedProject = projects.find(project => project.id === selectedProjectId) || null
  const selectedAPIProjectId = projectMode ? selectedProjectId || undefined : undefined

  const loadRuntimeMode = useCallback(async () => {
    try {
      setRuntimeMode(await fetchRuntimeMode())
    } catch {
      setRuntimeMode(null)
    }
  }, [])

  const loadProjects = useCallback(async () => {
    try {
      const data = await fetchProjects()
      const nextProjects = data.projects
      setProjects(nextProjects)
      setSupervisorConcurrency(data.concurrency)
      try {
        setRegistry(await fetchRegistry())
      } catch {
        setRegistry(null)
      }
      setProjectMode(true)
      setSelectedProjectId(current => {
        if (current && nextProjects.some(project => project.id === current)) {
          return current
        }
        return defaultProjectID(nextProjects)
      })
    } catch {
      setProjects([])
      setRegistry(null)
      setSupervisorConcurrency(null)
      setProjectMode(false)
      setSelectedProjectId(null)
    } finally {
      setProjectDiscoveryComplete(true)
    }
  }, [])

  const loadState = useCallback(async () => {
    if (!projectDiscoveryComplete) {
      return
    }
    if (projectMode && !selectedProjectId) {
      setState(null)
      setError('No project is available to display.')
      return
    }
    const requestID = stateRequestID.current + 1
    stateRequestID.current = requestID
    try {
      const data = await fetchState(projectMode ? selectedProjectId || undefined : undefined)
      if (stateRequestID.current === requestID) {
        setState(data)
        setLastUpdated(new Date())
        setError(null)
      }
    } catch (err) {
      if (stateRequestID.current === requestID) {
        setError(normalizeError(err))
      }
    }
  }, [projectDiscoveryComplete, projectMode, selectedProjectId])

  const loadSettings = useCallback(async () => {
    const data = await fetchSettings(projectMode ? selectedProjectId || undefined : undefined)
    setSettings(data)
    setSettingsDraft(JSON.stringify(data.config || {}, null, 2))
    setPromptDraft(data.prompt_template || '')
    setTrackerValidation(null)
    setError(null)
  }, [projectMode, selectedProjectId])

  useEffect(() => {
    void loadRuntimeMode()
    void loadProjects()
  }, [loadProjects, loadRuntimeMode])

  useEffect(() => {
    const onPopState = () => {
      const location = initialDashboardLocation()
      setPage(location.page)
      setSelectedProjectId(location.projectId)
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  useEffect(() => {
    if (!projectDiscoveryComplete) {
      return
    }
    syncDashboardLocation(page, projectMode ? selectedProjectId : null)
  }, [page, projectDiscoveryComplete, projectMode, selectedProjectId])

  useEffect(() => {
    if (!projectMode) {
      return
    }
    stateRequestID.current += 1
    detailRequestID.current += 1
    setState(null)
    setSettings(null)
    setTrackerValidation(null)
    setRefreshResult(null)
    setDetailState({ status: 'idle' })
    setError(null)
    setNotice(null)
  }, [projectMode, selectedProjectId])

  useEffect(() => {
    const interval = window.setInterval(() => {
      void loadState()
    }, pollIntervalMs)

    return () => window.clearInterval(interval)
  }, [loadState, pollIntervalMs])

  useEffect(() => {
    if (!projectMode) {
      return undefined
    }
    const interval = window.setInterval(() => {
      void loadProjects()
    }, pollIntervalMs)
    return () => window.clearInterval(interval)
  }, [loadProjects, pollIntervalMs, projectMode])

  useEffect(() => {
    if (projectDiscoveryComplete) {
      void loadState()
    }
  }, [loadState, projectDiscoveryComplete])

  useEffect(() => {
    if (page === 'settings' && !settings) {
      loadSettings().catch(err => setError(normalizeError(err)))
    }
  }, [loadSettings, page, settings])

  useEffect(() => {
    if (!registry) {
      setRegistrySettingsDraft(emptyRegistrySettingsDraft())
      setRegistryRuntimeDraft(emptyRegistryRuntimeDraft())
      return
    }
    setRegistrySettingsDraft(registrySettingsDraftFromRegistry(registry))
    setRegistryRuntimeDraft(registryRuntimeDraftFromRegistry(registry))
    setRegistrySettingsResult(null)
    setRegistryRuntimeResult(null)
  }, [registry?.source_path])

  const requestRefresh = async () => {
    setRefreshing(true)
    setRefreshResult(null)
    try {
      setRefreshResult(await requestRefreshAPI(selectedAPIProjectId))
      if (projectMode) {
        await loadProjects()
      }
      await loadState()
      setNotice(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setRefreshing(false)
    }
  }

  const createStarterRegistry = async () => {
    setBootstrappingRegistry(true)
    setNotice(null)
    try {
      const result = await bootstrapRegistry()
      setRegistryBootstrap(result)
      setNotice(result.created ? 'Starter registry created' : 'Starter registry already exists')
      setError(null)
      await loadRuntimeMode()
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setBootstrappingRegistry(false)
    }
  }

  const saveRegistryProject = async () => {
    setCreatingRegistryProject(true)
    setNotice(null)
    try {
      const maxConcurrentAgents = registryProjectDraft.maxConcurrentAgents.trim()
        ? Number.parseInt(registryProjectDraft.maxConcurrentAgents.trim(), 10)
        : 0
      if (Number.isNaN(maxConcurrentAgents) || maxConcurrentAgents < 0) {
        throw new Error('Project cap must be a positive whole number.')
      }
      const request = {
        name: registryProjectDraft.name.trim(),
        workflow_path: registryProjectDraft.workflowPath.trim(),
        enabled: registryProjectDraft.enabled,
        max_concurrent_agents: editingRegistryProjectId ? maxConcurrentAgents : maxConcurrentAgents || undefined,
      }
      if (editingRegistryProjectId) {
        const result = await updateRegistryProject(editingRegistryProjectId, request)
        setRegistry(result.registry)
        setRegistryProjectResult(null)
        setRegistryProjectUpdateResult(result)
        setRegistryProjectDeleteResult(null)
        setNotice('Project updated in registry')
      } else {
        const result = await createRegistryProject({
          id: registryProjectDraft.id.trim(),
          ...request,
        })
        setRegistry(result.registry)
        setRegistryProjectResult(result)
        setRegistryProjectUpdateResult(null)
        setRegistryProjectDeleteResult(null)
        setNotice('Project added to registry')
      }
      setRegistryProjectDraft({ id: '', name: '', workflowPath: '', enabled: true, maxConcurrentAgents: '' })
      setEditingRegistryProjectId(null)
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setCreatingRegistryProject(false)
    }
  }

  const editRegistryProject = (project: { id: string; name: string; workflow_path: string; enabled: boolean; max_concurrent_agents?: number }) => {
    setEditingRegistryProjectId(project.id)
    setRegistryProjectDraft({
      id: project.id,
      name: project.name || project.id,
      workflowPath: project.workflow_path || '',
      enabled: project.enabled,
      maxConcurrentAgents: project.max_concurrent_agents ? String(project.max_concurrent_agents) : '',
    })
    setRegistryProjectResult(null)
    setRegistryProjectUpdateResult(null)
    setNotice(null)
  }

  const cancelRegistryProjectEdit = () => {
    setEditingRegistryProjectId(null)
    setRegistryProjectDraft({ id: '', name: '', workflowPath: '', enabled: true, maxConcurrentAgents: '' })
  }

  const removeRegistryProject = async (project: { id: string; name: string }) => {
    const label = project.name || project.id
    if (!window.confirm(`Remove ${label} from simphony.yaml? The running server will not change until restart.`)) {
      return
    }
    setDeletingRegistryProjectId(project.id)
    setNotice(null)
    try {
      const result = await deleteRegistryProject(project.id)
      setRegistry(result.registry)
      setRegistryProjectDeleteResult(result)
      setRegistryProjectResult(null)
      setRegistryProjectUpdateResult(null)
      if (editingRegistryProjectId === project.id) {
        cancelRegistryProjectEdit()
      }
      setNotice('Project removed from registry')
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setDeletingRegistryProjectId(null)
    }
  }

  const saveRegistrySettings = async () => {
    setSavingRegistrySettings(true)
    setNotice(null)
    try {
      const port = Number.parseInt(registrySettingsDraft.port.trim(), 10)
      const maxConcurrentAgents = registrySettingsDraft.maxConcurrentAgents.trim()
        ? Number.parseInt(registrySettingsDraft.maxConcurrentAgents.trim(), 10)
        : 0
      const defaultProjectMaxConcurrentAgents = registrySettingsDraft.defaultProjectMaxConcurrentAgents.trim()
        ? Number.parseInt(registrySettingsDraft.defaultProjectMaxConcurrentAgents.trim(), 10)
        : 0
      if (Number.isNaN(port) || port <= 0 || port > 65535) {
        throw new Error('Server port must be between 1 and 65535.')
      }
      if (
        Number.isNaN(maxConcurrentAgents) ||
        Number.isNaN(defaultProjectMaxConcurrentAgents) ||
        maxConcurrentAgents < 0 ||
        defaultProjectMaxConcurrentAgents < 0
      ) {
        throw new Error('Concurrency limits must be zero or positive whole numbers.')
      }
      const result = await updateRegistrySettings({
        server: {
          bind_address: registrySettingsDraft.bindAddress.trim(),
          port,
          dashboard_enabled: registrySettingsDraft.dashboardEnabled,
          api_prefix: registrySettingsDraft.apiPrefix.trim(),
        },
        concurrency: {
          max_concurrent_agents: maxConcurrentAgents,
          default_project_max_concurrent_agents: defaultProjectMaxConcurrentAgents,
        },
        security: {
          allow_workspace_overlap: registrySettingsDraft.allowWorkspaceOverlap,
          allow_workspace_under_registry_dir: registrySettingsDraft.allowWorkspaceUnderRegistryDir,
          allow_remote_dashboard: registrySettingsDraft.allowRemoteDashboard,
        },
      })
      setRegistry(result.registry)
      setRegistrySettingsDraft(registrySettingsDraftFromRegistry(result.registry))
      setRegistrySettingsResult(result)
      setNotice('Registry settings saved')
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setSavingRegistrySettings(false)
    }
  }

  const saveRegistryRuntime = async () => {
    setSavingRegistryRuntime(true)
    setNotice(null)
    try {
      const result = await updateRegistrySettings({
        agent_runtime: {
          provider: registryRuntimeDraft.sdkProvider,
          command: registryRuntimeDraft.command.trim(),
          model: registryRuntimeDraft.model.trim(),
          model_provider: registryRuntimeDraft.modelProvider.trim(),
          reasoning_effort: registryRuntimeDraft.reasoningEffort.trim(),
          endpoint_url: registryRuntimeDraft.endpointURL.trim(),
          ...(registryRuntimeDraft.apiKey.trim() ? { api_key: registryRuntimeDraft.apiKey.trim() } : {}),
          ...(registryRuntimeDraft.authToken.trim() ? { auth_token: registryRuntimeDraft.authToken.trim() } : {}),
          permission_mode: registryRuntimeDraft.permissionMode.trim(),
          allowed_tools: parseStringList(registryRuntimeDraft.allowedTools),
          disallowed_tools: parseStringList(registryRuntimeDraft.disallowedTools),
          setting_sources: parseStringList(registryRuntimeDraft.settingSources),
        },
      })
      setRegistry(result.registry)
      setRegistryRuntimeDraft(registryRuntimeDraftFromRegistry(result.registry))
      setRegistryRuntimeResult(result)
      setNotice('Agent defaults saved')
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setSavingRegistryRuntime(false)
    }
  }

  const saveWorkflowSettings = async () => {
    setSavingSettings(true)
    setNotice(null)
    try {
      const config = JSON.parse(settingsDraft || '{}') as Record<string, unknown>
      if (!isPlainObject(config)) {
        throw new Error('Workflow config must be a JSON object.')
      }
      const data = await saveSettings({ config, prompt_template: promptDraft }, selectedAPIProjectId)
      setSettings(data)
      setSettingsDraft(JSON.stringify(data.config || {}, null, 2))
      setPromptDraft(data.prompt_template || '')
      setTrackerValidation(null)
      setNotice('Settings saved')
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setSavingSettings(false)
    }
  }

  const validateLinearSettings = async () => {
    setValidatingTracker(true)
    setTrackerValidation(null)
    setNotice(null)
    try {
      const config = JSON.parse(settingsDraft || '{}') as Record<string, unknown>
      if (!isPlainObject(config)) {
        throw new Error('Workflow config must be a JSON object.')
      }
      const result = await validateTrackerSettings({ config, prompt_template: promptDraft }, selectedAPIProjectId)
      setTrackerValidation(result)
      setNotice('Linear settings validated')
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setValidatingTracker(false)
    }
  }

  const closeIssue = useCallback(() => {
    detailRequestID.current += 1
    setDetailState({ status: 'idle' })
  }, [])

  const loadIssueDetail = useCallback(async (identifier: string, showLoading: boolean) => {
    const requestID = detailRequestID.current + 1
    detailRequestID.current = requestID
    if (showLoading) {
      setDetailState({ status: 'loading', identifier })
    }
    try {
      const detail = await fetchIssueDetail(identifier, projectMode ? selectedProjectId || undefined : undefined)
      if (detailRequestID.current === requestID) {
        setDetailState({ status: 'ready', detail })
      }
    } catch (err) {
      if (detailRequestID.current === requestID) {
        setDetailState({ status: 'error', identifier, message: normalizeError(err) })
      }
    }
  }, [projectMode, selectedProjectId])

  const openIssue = useCallback((identifier: string) => {
    void loadIssueDetail(identifier, true)
  }, [loadIssueDetail])

  useEffect(() => {
    if (detailState.status !== 'ready' || !lastUpdated) {
      return
    }
    void loadIssueDetail(detailState.detail.issue_identifier, false)
  }, [detailState.status === 'ready' ? detailState.detail.issue_identifier : '', lastUpdated, loadIssueDetail])

  const summary = useMemo(() => {
    if (!state) {
      return null
    }
    return {
      active: state.counts.running + state.counts.retrying,
      running: state.counts.running,
      retrying: state.counts.retrying,
      tokens: state.codex_totals.total_tokens,
      outputShare: percentOf(state.codex_totals.output_tokens, state.codex_totals.total_tokens),
      slotCapacity: state.max_concurrent_agents,
      slotUsage: state.max_concurrent_agents > 0 ? percentOf(state.counts.running, state.max_concurrent_agents) : 0,
      queuePressure:
        state.counts.running + state.counts.retrying === 0
          ? 'Idle'
          : state.max_concurrent_agents > 0 && state.counts.running >= state.max_concurrent_agents
            ? 'At capacity'
            : state.counts.retrying > state.counts.running
              ? 'Backoff heavy'
              : 'Dispatching',
    }
  }, [state])

  const activity = useMemo<ActivityItem[]>(() => {
    if (!state) {
      return []
    }

    const running = state.running.map(item => ({
      id: `running-${entryKey(item)}`,
      label: item.issue_identifier,
      tone: 'running' as const,
      title: item.last_event || 'Running',
      body: item.last_message || item.issue_title || `${item.turn_count} turns in progress`,
      at: displayTimestamp(item.last_event_at, item.started_at),
    }))

    const retrying = state.retrying.map(item => ({
      id: `retry-${entryKey(item)}`,
      label: item.issue_identifier,
      tone: 'retrying' as const,
      title: `Retry attempt ${item.attempt}`,
      body: item.error || 'Waiting for retry backoff',
      at: item.due_at,
    }))

    return [...running, ...retrying]
      .filter(item => item.at)
      .sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime())
      .slice(0, 8)
  }, [state])

  const projectOverview = useMemo<ProjectOverview | null>(() => {
    if (!projectMode || projects.length === 0) {
      return null
    }
    return {
      runningProjects: projects.filter(project => project.running).length,
      disabledProjects: projects.filter(project => !project.enabled).length,
      errorProjects: projects.filter(project => project.enabled && !project.running && project.last_error).length,
      waitingProjects: projects.filter(project => project.waiting_on_supervisor).length,
      runningIssues: projects.reduce((sum, project) => sum + (project.counts?.running || 0), 0),
      retryingIssues: projects.reduce((sum, project) => sum + (project.counts?.retrying || 0), 0),
      supervisorCapacity: supervisorConcurrency?.max_concurrent_agents || 0,
      supervisorUsed: supervisorConcurrency?.used_agents || 0,
      supervisorAvailable: supervisorConcurrency?.available_agents || 0,
    }
  }, [projectMode, projects, supervisorConcurrency])

  if (!state || !summary) {
    return (
      <main className="app-shell loading-shell">
        <section className="boot-panel">
          <div className="brand-mark" aria-hidden="true">
            S
          </div>
          <div>
            <p className="eyebrow">Simphony</p>
            <h1>Loading orchestration state</h1>
            <p className="muted">Connecting to the local dashboard API.</p>
          </div>
          {error && (
            <div className="alert alert-error" role="alert">
              Error: {error}
            </div>
          )}
        </section>
      </main>
    )
  }

  const visibleRunning = (filter === 'retrying' ? [] : state.running).filter(item => matchesIssueQuery(item, query))
  const visibleRetrying = (filter === 'running' ? [] : state.retrying).filter(item => matchesIssueQuery(item, query))
  const showAggregateProjectHealth = projectMode && projects.length > 1

  const changeProject = (projectID: string) => {
    setSelectedProjectId(projectID)
  }

  const activeProjectStatus = selectedProject ? projectStatus(selectedProject) : null
  const pageTitle = page === 'settings' ? 'Project settings' : page === 'setup' ? 'Project setup' : 'Runtime'
  const pageSubtitle =
    page === 'setup'
      ? projectMode
        ? `${projects.length} configured project${projects.length === 1 ? '' : 's'}`
        : 'Single-project mode'
      : selectedProject
        ? selectedProject.name || selectedProject.id
        : 'Local workflow'

  return (
    <main className="app-frame">
      <ProjectSidebar
        page={page}
        projectMode={projectMode}
        projects={projects}
        selectedProjectId={selectedProjectId}
        projectOverview={projectOverview}
        supervisorConcurrency={supervisorConcurrency}
        onPageChange={setPage}
        onProjectSelect={projectID => {
          changeProject(projectID)
          setPage('runtime')
        }}
      />

      <section className="workspace-shell">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">{page === 'setup' ? 'Supervisor' : activeProjectStatus ? activeProjectStatus.label : 'Local project'}</p>
            <h1>{pageTitle}</h1>
            <p className="workspace-subtitle">{pageSubtitle}</p>
          </div>
          <div className="workspace-actions">
            {activeProjectStatus && page !== 'setup' && <span className={`project-status ${activeProjectStatus.tone}`}>{activeProjectStatus.label}</span>}
            <div className="sync-copy">
              <span>Snapshot {formatDateTime(state.generated_at)}</span>
              <span>UI {lastUpdated ? lastUpdated.toLocaleTimeString() : 'never'}</span>
            </div>
            {page !== 'setup' && (
              <button className="icon-button" type="button" onClick={requestRefresh} disabled={refreshing} title="Refresh now">
                <span aria-hidden="true" className={refreshing ? 'refresh-glyph spinning' : 'refresh-glyph'} />
                <span>{refreshing ? 'Syncing' : 'Sync'}</span>
              </button>
            )}
          </div>
        </header>

        {error && (
          <div className="alert alert-error" role="alert">
            <strong>API error</strong>
            <span>{error}</span>
          </div>
        )}

        {notice && (
          <div className="alert alert-info" role="status">
            <strong>{notice}</strong>
          </div>
        )}

        {page === 'runtime' && refreshResult && (
          <div className="alert alert-info" role="status">
            <strong>{refreshResult.coalesced ? 'Refresh coalesced' : 'Refresh queued'}</strong>
            <span>
              {refreshResult.operations.length > 0 ? refreshResult.operations.join(', ') : 'No operations reported'} at{' '}
              {formatDateTime(refreshResult.requested_at)}
            </span>
          </div>
        )}

        {page === 'runtime' && (
          <>
            {showAggregateProjectHealth && projectOverview && (
              <section className="project-overview" aria-label="Project overview">
                <div className="project-overview-heading">
                  <div>
                    <p className="eyebrow">Projects</p>
                    <h2>Runtime overview</h2>
                  </div>
                  <div className="project-overview-stats">
                    <span>{projectOverview.runningProjects} running</span>
                    <span>{projectOverview.runningIssues} active</span>
                    <span>{projectOverview.retryingIssues} retrying</span>
                    {projectOverview.supervisorCapacity > 0 && (
                      <span>
                        {projectOverview.supervisorUsed}/{projectOverview.supervisorCapacity} global slots
                      </span>
                    )}
                    {projectOverview.waitingProjects > 0 && <span>{projectOverview.waitingProjects} waiting</span>}
                    {projectOverview.disabledProjects > 0 && <span>{projectOverview.disabledProjects} disabled</span>}
                    {projectOverview.errorProjects > 0 && <span>{projectOverview.errorProjects} failed</span>}
                  </div>
                </div>
              </section>
            )}
            {showAggregateProjectHealth && projectOverview && (
              <ProjectHealthPanel
                projects={projects}
                supervisorConcurrency={supervisorConcurrency}
                onOpenRuntime={projectID => {
                  changeProject(projectID)
                  setPage('runtime')
                }}
                onOpenSettings={projectID => {
                  changeProject(projectID)
                  setPage('settings')
                }}
              />
            )}

            <section className="metrics-grid" aria-label="Runtime metrics">
              <MetricCard label="Active issues" value={summary.active.toLocaleString()} detail="Running plus retry queue" tone="green" />
              <MetricCard label="Running" value={summary.running.toLocaleString()} detail="Live Codex sessions" tone="blue" />
              <MetricCard label="Retrying" value={summary.retrying.toLocaleString()} detail="Backoff queue" tone="amber" />
              <MetricCard label="Completed" value={state.counts.completed.toLocaleString()} detail={`${state.counts.claimed.toLocaleString()} claimed`} tone="ink" />
            </section>

            <section className="ops-band" aria-label="Operations summary">
              <div>
                <span className="ops-label">Posture</span>
                <strong>{summary.queuePressure}</strong>
              </div>
              <div>
                <span className="ops-label">Output share</span>
                <strong>{summary.tokens > 0 ? `${summary.outputShare}%` : 'No tokens'}</strong>
              </div>
              <div>
                <span className="ops-label">Slot usage</span>
                <strong>{summary.slotCapacity > 0 ? `${summary.slotUsage}%` : 'Unknown'}</strong>
              </div>
              <div>
                <span className="ops-label">Poll interval</span>
                <strong>{formatMilliseconds(state.poll_interval_ms)}</strong>
              </div>
            </section>

            <section className="content-grid">
              <div className="main-column">
                <section className="panel">
                  <div className="panel-heading">
                    <div>
                      <p className="eyebrow">Queue</p>
                      <h2>Issue flow</h2>
                    </div>
                    <div className="segment-control" role="group" aria-label="Queue filter">
                      <FilterButton active={filter === 'all'} label="All" onClick={() => setFilter('all')} />
                      <FilterButton active={filter === 'running'} label="Running" onClick={() => setFilter('running')} />
                      <FilterButton active={filter === 'retrying'} label="Retrying" onClick={() => setFilter('retrying')} />
                    </div>
                  </div>
                  <div className="queue-toolbar">
                    <label className="search-field">
                      <span className="search-icon" aria-hidden="true" />
                      <span className="sr-only">Search queue</span>
                      <input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search issue, session, state, or error" />
                    </label>
                    <span className="queue-count">
                      {visibleRunning.length + visibleRetrying.length} shown of {state.running.length + state.retrying.length}
                    </span>
                  </div>

                  <div className="queue-stack">
                    {visibleRunning.map(item => (
                      <RunningRow key={entryKey(item)} item={item} onOpen={openIssue} />
                    ))}
                    {visibleRetrying.map(item => (
                      <RetryRow key={entryKey(item)} item={item} onOpen={openIssue} />
                    ))}
                    {visibleRunning.length === 0 && visibleRetrying.length === 0 && (
                      <EmptyState title="No issues in this view" body="The orchestrator has no matching running sessions or retry entries." />
                    )}
                  </div>
                </section>
              </div>

              <aside className="side-column">
                <section className="panel compact-panel">
                  <div className="panel-heading">
                    <div>
                      <p className="eyebrow">Activity</p>
                      <h2>Latest signal</h2>
                    </div>
                  </div>
                  <ActivityFeed items={activity} onOpen={openIssue} />
                </section>

                <section className="panel compact-panel">
                  <div className="panel-heading">
                    <div>
                      <p className="eyebrow">Capacity</p>
                      <h2>Runtime mix</h2>
                    </div>
                  </div>
                  <div className="capacity-bars">
                    <CapacityBar label="Slots" value={state.counts.running} total={Math.max(state.max_concurrent_agents, 1)} />
                    <CapacityBar label="Input" value={state.codex_totals.input_tokens} total={Math.max(state.codex_totals.total_tokens, 1)} />
                    <CapacityBar label="Output" value={state.codex_totals.output_tokens} total={Math.max(state.codex_totals.total_tokens, 1)} />
                    <CapacityBar label="Total" value={state.codex_totals.total_tokens} total={Math.max(state.codex_totals.total_tokens, 1)} />
                  </div>
                </section>

                <section className="panel compact-panel">
                  <div className="panel-heading">
                    <div>
                      <p className="eyebrow">Rate limits</p>
                      <h2>Provider signal</h2>
                    </div>
                  </div>
                  <RateLimitPanel rateLimits={state.rate_limits} />
                </section>
              </aside>
            </section>

            <IssueDrawer detailState={detailState} onClose={closeIssue} />
          </>
        )}

        {page === 'settings' && (
          <SettingsView
            settings={settings}
            settingsDraft={settingsDraft}
            promptDraft={promptDraft}
            saving={savingSettings}
            validatingTracker={validatingTracker}
            trackerValidation={trackerValidation}
            onSettingsDraftChange={setSettingsDraft}
            onPromptDraftChange={setPromptDraft}
            onReload={() => loadSettings().catch(err => setError(normalizeError(err)))}
            onSave={saveWorkflowSettings}
            onValidateTracker={validateLinearSettings}
          />
        )}

        {page === 'setup' && (
          <ProjectSetupView
            projectMode={projectMode}
            projects={projects}
            registry={registry}
            runtimeMode={runtimeMode}
            registryBootstrap={registryBootstrap}
            registryProjectDraft={registryProjectDraft}
            registryProjectResult={registryProjectResult}
            registryProjectUpdateResult={registryProjectUpdateResult}
            registryProjectDeleteResult={registryProjectDeleteResult}
            registrySettingsDraft={registrySettingsDraft}
            registrySettingsResult={registrySettingsResult}
            registryRuntimeDraft={registryRuntimeDraft}
            registryRuntimeResult={registryRuntimeResult}
            editingRegistryProjectId={editingRegistryProjectId}
            bootstrappingRegistry={bootstrappingRegistry}
            creatingRegistryProject={creatingRegistryProject}
            savingRegistrySettings={savingRegistrySettings}
            savingRegistryRuntime={savingRegistryRuntime}
            deletingRegistryProjectId={deletingRegistryProjectId}
            projectOverview={projectOverview}
            supervisorConcurrency={supervisorConcurrency}
            onBootstrapRegistry={createStarterRegistry}
            onRegistryProjectDraftChange={setRegistryProjectDraft}
            onSaveRegistryProject={saveRegistryProject}
            onRegistrySettingsDraftChange={setRegistrySettingsDraft}
            onSaveRegistrySettings={saveRegistrySettings}
            onRegistryRuntimeDraftChange={setRegistryRuntimeDraft}
            onSaveRegistryRuntime={saveRegistryRuntime}
            onEditRegistryProject={editRegistryProject}
            onCancelRegistryProjectEdit={cancelRegistryProjectEdit}
            onRemoveRegistryProject={removeRegistryProject}
            onSelectProject={projectID => {
              changeProject(projectID)
              setPage('settings')
            }}
          />
        )}
      </section>
    </main>
  )
}

function MetricCard(props: { label: string; value: string; detail: string; tone: 'green' | 'blue' | 'amber' | 'ink' }) {
  return (
    <article className={`metric-card tone-${props.tone}`}>
      <span className="metric-label">{props.label}</span>
      <strong>{props.value}</strong>
      <span>{props.detail}</span>
    </article>
  )
}

function FilterButton(props: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button className={props.active ? 'segment active' : 'segment'} type="button" onClick={props.onClick} aria-pressed={props.active}>
      {props.label}
    </button>
  )
}

function ProjectHealthPanel(props: {
  projects: ProjectSummary[]
  supervisorConcurrency: SupervisorConcurrency | null
  onOpenRuntime: (projectID: string) => void
  onOpenSettings: (projectID: string) => void
}) {
  return (
    <section className="panel project-health-panel" aria-label="Project health">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">Health</p>
          <h2>Project status</h2>
        </div>
        <span className="health-capacity">
          {props.supervisorConcurrency?.max_concurrent_agents
            ? `${props.supervisorConcurrency.available_agents}/${props.supervisorConcurrency.max_concurrent_agents} slots free`
            : 'Unlimited slots'}
        </span>
      </div>
      <div className="project-health-list">
        {props.projects.map(project => {
          const status = projectStatus(project)
          const activeCount = project.counts.running + project.counts.retrying
          const cap = project.max_concurrent_agents ? project.max_concurrent_agents.toLocaleString() : 'Default'
          return (
            <article key={project.id} className={`project-health-row ${status.tone}`}>
              <span className={`status-dot ${status.tone}`} aria-hidden="true" />
              <div className="project-health-copy">
                <div className="project-health-title">
                  <strong>{project.name || project.id}</strong>
                  <span className={`project-status ${status.tone}`}>{status.label}</span>
                </div>
                <span>{project.workflow_path}</span>
                {project.last_error && <em>{project.last_error}</em>}
                {project.health.summary && <em>{project.health.summary}</em>}
                {project.health.issues?.slice(0, 2).map(issue => (
                  <em key={`${issue.code}-${issue.message}`}>
                    {issue.message}
                    {issue.detail ? `: ${issue.detail}` : ''}
                    {issue.suggestion ? ` ${issue.suggestion}` : ''}
                  </em>
                ))}
                {project.running && (
                  <em>
                    Watcher {project.workflow_watcher_running ? 'active' : 'inactive'}
                    {project.workflow_watcher_error ? `: ${project.workflow_watcher_error}` : ''}
                  </em>
                )}
                {project.waiting_on_supervisor && (
                  <em>
                    Waiting for a global slot
                    {project.last_supervisor_deferred_at ? ` since ${formatDateTime(project.last_supervisor_deferred_at)}` : ''}
                  </em>
                )}
              </div>
              <dl className="project-health-metrics">
                <div>
                  <dt>Active</dt>
                  <dd>{activeCount.toLocaleString()}</dd>
                </div>
                <div>
                  <dt>Retry</dt>
                  <dd>{project.counts.retrying.toLocaleString()}</dd>
                </div>
                <div>
                  <dt>Done</dt>
                  <dd>{project.counts.completed.toLocaleString()}</dd>
                </div>
                <div>
                  <dt>Cap</dt>
                  <dd>{cap}</dd>
                </div>
                <div>
                  <dt>Watch</dt>
                  <dd>{project.workflow_watcher_running ? 'On' : 'Off'}</dd>
                </div>
              </dl>
              <div className="project-health-actions">
                <button className="ghost-button" type="button" onClick={() => props.onOpenRuntime(project.id)}>
                  Runtime
                </button>
                <button className="ghost-button" type="button" onClick={() => props.onOpenSettings(project.id)}>
                  Settings
                </button>
              </div>
            </article>
          )
        })}
      </div>
    </section>
  )
}

function ProjectSidebar(props: {
  page: Page
  projectMode: boolean
  projects: ProjectSummary[]
  selectedProjectId: string | null
  projectOverview: ProjectOverview | null
  supervisorConcurrency: SupervisorConcurrency | null
  onPageChange: (page: Page) => void
  onProjectSelect: (projectID: string) => void
}) {
  const activeIssues = props.projectOverview ? props.projectOverview.runningIssues + props.projectOverview.retryingIssues : 0
  const singleRegistryProject = props.projectMode && props.projects.length === 1
  return (
    <aside className="project-sidebar" aria-label="Project navigation">
      <div className="sidebar-brand">
        <div className="brand-mark" aria-hidden="true">
          S
        </div>
        <div>
          <strong>Simphony</strong>
          <span>{props.projectMode ? 'Multi-project' : 'Single project'}</span>
        </div>
      </div>

      <nav className="primary-nav" aria-label="Workspace">
        <button className={props.page === 'runtime' ? 'nav-item active' : 'nav-item'} type="button" onClick={() => props.onPageChange('runtime')}>
          <span className="nav-glyph nav-glyph-runtime" aria-hidden="true" />
          <span>Runtime</span>
        </button>
        <button className={props.page === 'settings' ? 'nav-item active' : 'nav-item'} type="button" onClick={() => props.onPageChange('settings')}>
          <span className="nav-glyph nav-glyph-settings" aria-hidden="true" />
          <span>Settings</span>
        </button>
        <button className={props.page === 'setup' ? 'nav-item active' : 'nav-item'} type="button" onClick={() => props.onPageChange('setup')}>
          <span className="nav-glyph nav-glyph-setup" aria-hidden="true" />
          <span>Project setup</span>
        </button>
      </nav>

      <div className="sidebar-summary" aria-label="Supervisor summary">
        <span>{activeIssues.toLocaleString()} active issues</span>
        {props.supervisorConcurrency?.max_concurrent_agents ? (
          <strong>
            {props.supervisorConcurrency.used_agents}/{props.supervisorConcurrency.max_concurrent_agents} slots
          </strong>
        ) : (
          <strong>Local capacity</strong>
        )}
      </div>

      <div className="project-nav-section">
        <div className="project-nav-heading">
          <span>{singleRegistryProject ? 'Project context' : 'Projects'}</span>
          <button className="add-project-button" type="button" onClick={() => props.onPageChange('setup')} title="Open project setup">
            +
          </button>
        </div>
        {props.projectMode ? (
          <div className="project-nav-list">
            {props.projects.map(project => (
              <ProjectNavButton
                key={project.id}
                project={project}
                active={project.id === props.selectedProjectId && props.page !== 'setup'}
                onSelect={() => props.onProjectSelect(project.id)}
              />
            ))}
          </div>
        ) : (
          <p className="sidebar-muted">Start with `-config` to manage multiple project contexts.</p>
        )}
      </div>
    </aside>
  )
}

function ProjectNavButton(props: { project: ProjectSummary; active: boolean; onSelect: () => void }) {
  const status = projectStatus(props.project)
  const activeCount = props.project.counts.running + props.project.counts.retrying
  return (
    <button className={props.active ? 'project-nav-item active' : 'project-nav-item'} type="button" onClick={props.onSelect} title={props.project.workflow_path}>
      <span className={`status-dot ${status.tone}`} aria-hidden="true" />
      <span className="project-nav-copy">
        <strong>{props.project.name || props.project.id}</strong>
        <span>
          {status.label}
          {activeCount > 0 ? ` - ${activeCount} active` : ''}
        </span>
      </span>
    </button>
  )
}

function ProjectSetupView(props: {
  projectMode: boolean
  projects: ProjectSummary[]
  registry: RegistryResponse | null
  runtimeMode: RuntimeModeResponse | null
  registryBootstrap: RegistryBootstrapResponse | null
  registryProjectDraft: RegistryProjectDraft
  registryProjectResult: RegistryProjectCreateResponse | null
  registryProjectUpdateResult: RegistryProjectUpdateResponse | null
  registryProjectDeleteResult: RegistryProjectDeleteResponse | null
  registrySettingsDraft: RegistrySettingsDraft
  registrySettingsResult: RegistryUpdateResponse | null
  registryRuntimeDraft: RegistryRuntimeDraft
  registryRuntimeResult: RegistryUpdateResponse | null
  editingRegistryProjectId: string | null
  bootstrappingRegistry: boolean
  creatingRegistryProject: boolean
  savingRegistrySettings: boolean
  savingRegistryRuntime: boolean
  deletingRegistryProjectId: string | null
  projectOverview: ProjectOverview | null
  supervisorConcurrency: SupervisorConcurrency | null
  onBootstrapRegistry: () => void
  onRegistryProjectDraftChange: (draft: RegistryProjectDraft) => void
  onSaveRegistryProject: () => void
  onRegistrySettingsDraftChange: (draft: RegistrySettingsDraft) => void
  onSaveRegistrySettings: () => void
  onRegistryRuntimeDraftChange: (draft: RegistryRuntimeDraft) => void
  onSaveRegistryRuntime: () => void
  onEditRegistryProject: (project: { id: string; name: string; workflow_path: string; enabled: boolean; max_concurrent_agents?: number }) => void
  onCancelRegistryProjectEdit: () => void
  onRemoveRegistryProject: (project: { id: string; name: string }) => void
  onSelectProject: (projectID: string) => void
}) {
  const configuredCount = props.registry?.projects.length ?? props.projects.length
  const registryProjectByID = new Map((props.registry?.projects || []).map(project => [project.id, project]))
  const runtimeProjectByID = new Map(props.projects.map(project => [project.id, project]))
  const setupProjects = props.registry?.projects || props.projects
  const startupMode = props.runtimeMode?.mode || (props.projectMode ? 'project_registry' : 'single_workflow')
  const registryEnabled = startupMode === 'project_registry'
  const startupPath = registryEnabled
    ? props.runtimeMode?.registry_path || props.registry?.source_path || ''
    : props.runtimeMode?.workflow_path || ''
  const registryRuntimeModels = modelOptionsForProvider(props.registryRuntimeDraft.modelProvider, props.registryRuntimeDraft.model)
  const registryRuntimeReasoning = reasoningOptionsForSelection(
    props.registryRuntimeDraft.model,
    props.registryRuntimeDraft.modelProvider,
    props.registryRuntimeDraft.reasoningEffort,
  )
  return (
    <section className="setup-layout">
      <div className="panel setup-main">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">Registry</p>
            <h2>Project contexts</h2>
          </div>
          <span className="setup-badge">Restart required</span>
        </div>
        <div className="setup-intro">
          <h3>{props.projectMode ? `${configuredCount} configured project${configuredCount === 1 ? '' : 's'}` : 'Single-project mode'}</h3>
          <p>
            Project setup manages the registry file, project entries, supervisor defaults, isolation guardrails, and shared agent runtime defaults.
            Registry changes are saved immediately and apply to workers after restart.
          </p>
        </div>
        <div className="startup-mode-panel">
          <div className="startup-mode-copy">
            <span className="mode-pill">{registryEnabled ? 'Project registry' : 'Single workflow'}</span>
            <div>
              <h3>Startup mode</h3>
              <p>
                Simphony currently starts in {registryEnabled ? 'project registry mode' : 'single workflow mode'}. Switching between these modes
                changes the supervisor shape and requires a server restart.
              </p>
            </div>
          </div>
          <div className="startup-mode-control">
            <label className="toggle-label" htmlFor="registry-mode-toggle">
              Use project registry
            </label>
            <button
              id="registry-mode-toggle"
              className={`switch-control ${registryEnabled ? 'on' : ''}`}
              type="button"
              role="switch"
              aria-checked={registryEnabled}
              disabled
              title="Changing startup mode requires restarting Simphony"
            >
              <span />
            </button>
          </div>
          <dl className="startup-mode-facts">
            <div>
              <dt>{registryEnabled ? 'Registry file' : 'Workflow file'}</dt>
              <dd>{startupPath || 'Not reported by server'}</dd>
            </div>
            <div>
              <dt>Mode changes</dt>
              <dd>{props.runtimeMode?.change_requires_restart === false ? 'Live switch supported' : 'Restart required'}</dd>
            </div>
          </dl>
          <p className="restart-note" role="status">
            To change this setting, restart Simphony with {registryEnabled ? '-workflow ./WORKFLOW.md' : '-config ./simphony.yaml'}.
          </p>
          {!registryEnabled && (
            <div className="bootstrap-action-row">
              <button className="secondary-button" type="button" onClick={props.onBootstrapRegistry} disabled={props.bootstrappingRegistry}>
                {props.bootstrappingRegistry ? 'Creating registry' : 'Create starter registry'}
              </button>
              <span>Generates a local `simphony.yaml` for this workflow without changing the running server.</span>
            </div>
          )}
          {props.registryBootstrap && (
            <div className="bootstrap-result" role="status">
              <strong>{props.registryBootstrap.created ? 'Registry created' : 'Registry already exists'}</strong>
              <span>{props.registryBootstrap.registry_path}</span>
              <code>{props.registryBootstrap.command}</code>
            </div>
          )}
        </div>
        {props.registry && (
          <div className="registry-detail-grid">
            <RegistryFact label="Registry file" value={props.registry.source_path || 'Unknown'} />
            <RegistryFact
              label="Server"
              value={props.registry.server ? `${props.registry.server.bind_address}:${props.registry.server.port}${props.registry.server.api_prefix}` : 'Disabled'}
            />
            <RegistryFact
              label="Global concurrency"
              value={
                props.registry.concurrency.max_concurrent_agents > 0
                  ? `${props.registry.concurrency.max_concurrent_agents} total slots`
                  : 'Unlimited'
              }
            />
            <RegistryFact
              label="Project default cap"
              value={
                props.registry.concurrency.default_project_max_concurrent_agents > 0
                  ? props.registry.concurrency.default_project_max_concurrent_agents.toLocaleString()
                  : 'Workflow default'
              }
            />
          </div>
        )}
        {props.registry?.warnings && props.registry.warnings.length > 0 && (
          <div className="registry-warning-list" role="status">
            {props.registry.warnings.map(warning => (
              <div key={`${warning.code}-${warning.project_ids?.join('-') || warning.message}`}>
                <strong>{warning.code}</strong>
                <span>{warning.message}</span>
              </div>
            ))}
          </div>
        )}
        {registryEnabled && props.registry && (
          <form
            className="registry-project-form registry-settings-form"
            onSubmit={event => {
              event.preventDefault()
              props.onSaveRegistrySettings()
            }}
          >
            <div className="registry-project-form-heading">
              <div>
                <p className="eyebrow">Defaults</p>
                <h3>Registry settings</h3>
              </div>
              <button className="secondary-button" type="submit" disabled={props.savingRegistrySettings}>
                {props.savingRegistrySettings ? 'Saving settings' : 'Save settings'}
              </button>
            </div>
            <div className="registry-project-fields">
              <label className="form-field">
                <span>Bind address</span>
                <input
                  value={props.registrySettingsDraft.bindAddress}
                  onChange={event => props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, bindAddress: event.target.value })}
                  placeholder="127.0.0.1"
                />
              </label>
              <label className="form-field">
                <span>Port</span>
                <input
                  value={props.registrySettingsDraft.port}
                  onChange={event => props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, port: event.target.value })}
                  inputMode="numeric"
                  placeholder="8080"
                  required
                />
              </label>
              <label className="form-field">
                <span>API prefix</span>
                <input
                  value={props.registrySettingsDraft.apiPrefix}
                  onChange={event => props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, apiPrefix: event.target.value })}
                  placeholder="/api/v1"
                />
              </label>
              <label className="form-field">
                <span>Global slots</span>
                <input
                  value={props.registrySettingsDraft.maxConcurrentAgents}
                  onChange={event => props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, maxConcurrentAgents: event.target.value })}
                  inputMode="numeric"
                  placeholder="Unlimited"
                />
              </label>
              <label className="form-field">
                <span>Default project cap</span>
                <input
                  value={props.registrySettingsDraft.defaultProjectMaxConcurrentAgents}
                  onChange={event =>
                    props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, defaultProjectMaxConcurrentAgents: event.target.value })
                  }
                  inputMode="numeric"
                  placeholder="Workflow default"
                />
              </label>
              <label className="registry-checkbox">
                <input
                  type="checkbox"
                  checked={props.registrySettingsDraft.dashboardEnabled}
                  onChange={event => props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, dashboardEnabled: event.target.checked })}
                />
                <span>Serve dashboard</span>
              </label>
              <label className="registry-checkbox">
                <input
                  type="checkbox"
                  checked={props.registrySettingsDraft.allowRemoteDashboard}
                  onChange={event => props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, allowRemoteDashboard: event.target.checked })}
                />
                <span>Allow remote dashboard/API</span>
              </label>
              <label className="registry-checkbox">
                <input
                  type="checkbox"
                  checked={props.registrySettingsDraft.allowWorkspaceOverlap}
                  onChange={event =>
                    props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, allowWorkspaceOverlap: event.target.checked })
                  }
                />
                <span>Allow workspace overlap</span>
              </label>
              <label className="registry-checkbox">
                <input
                  type="checkbox"
                  checked={props.registrySettingsDraft.allowWorkspaceUnderRegistryDir}
                  onChange={event =>
                    props.onRegistrySettingsDraftChange({ ...props.registrySettingsDraft, allowWorkspaceUnderRegistryDir: event.target.checked })
                  }
                />
                <span>Allow workspaces under registry folder</span>
              </label>
            </div>
            {props.registrySettingsResult && (
              <div className="bootstrap-result" role="status">
                <strong>Registry settings saved</strong>
                <span>Restart Simphony for server and supervisor changes to take effect.</span>
                <code>{props.registrySettingsResult.command}</code>
              </div>
            )}
          </form>
        )}
        {registryEnabled && props.registry && (
          <form
            className="registry-project-form registry-runtime-form"
            onSubmit={event => {
              event.preventDefault()
              props.onSaveRegistryRuntime()
            }}
          >
            <div className="registry-project-form-heading">
              <div>
                <p className="eyebrow">Defaults</p>
                <h3>Agent defaults</h3>
              </div>
              <button className="secondary-button" type="submit" disabled={props.savingRegistryRuntime}>
                {props.savingRegistryRuntime ? 'Saving defaults' : 'Save defaults'}
              </button>
            </div>
            <div className="registry-project-fields">
              <label className="form-field">
                <span>SDK</span>
                <select
                  value={props.registryRuntimeDraft.sdkProvider}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, sdkProvider: event.target.value })}
                >
                  <option value="codex">Codex</option>
                  <option value="claude">Claude Code</option>
                </select>
              </label>
              <label className="form-field">
                <span>Model provider</span>
                <select
                  value={props.registryRuntimeDraft.modelProvider}
                  onChange={event => {
                    const nextProvider = event.target.value
                    const provider = PROVIDER_OPTIONS.find(item => item.id === nextProvider)
                    const nextModel =
                      provider && props.registryRuntimeDraft.model && !provider.models.some(option => option.model === props.registryRuntimeDraft.model)
                        ? ''
                        : props.registryRuntimeDraft.model
                    props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, modelProvider: nextProvider, model: nextModel })
                  }}
                >
                  <option value="">SDK default</option>
                  {providerOptionsWithCurrent(props.registryRuntimeDraft.modelProvider).map(option => (
                    <option key={option.id} value={option.id}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className="form-field">
                <span>Model</span>
                <select
                  value={selectedModelID(props.registryRuntimeDraft.model, props.registryRuntimeDraft.modelProvider)}
                  onChange={event => {
                    const option = registryRuntimeModels.find(item => item.id === event.target.value)
                    props.onRegistryRuntimeDraftChange({
                      ...props.registryRuntimeDraft,
                      model: option?.model || '',
                      modelProvider: option?.modelProvider || props.registryRuntimeDraft.modelProvider,
                    })
                  }}
                >
                  <option value="">SDK default</option>
                  {registryRuntimeModels.map(option => (
                    <option key={option.id} value={option.id}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className="form-field">
                <span>Reasoning</span>
                <select
                  value={props.registryRuntimeDraft.reasoningEffort}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, reasoningEffort: event.target.value })}
                >
                  {registryRuntimeReasoning.map(option => (
                    <option key={option.value || 'default'} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className="form-field wide">
                <span>Command</span>
                <input
                  value={props.registryRuntimeDraft.command}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, command: event.target.value })}
                  placeholder={props.registryRuntimeDraft.sdkProvider === 'claude' ? 'node ./simphony-claude-shim.mjs' : 'codex app-server'}
                />
              </label>
              <label className="form-field wide">
                <span>Endpoint URL</span>
                <input
                  value={props.registryRuntimeDraft.endpointURL}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, endpointURL: event.target.value })}
                  placeholder="https://api.openai.com/v1"
                />
              </label>
              <label className="form-field">
                <span>API key</span>
                <input
                  value={props.registryRuntimeDraft.apiKey}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, apiKey: event.target.value })}
                  placeholder={props.registry.agent_runtime.api_key_configured ? 'Keep existing' : '$OPENAI_API_KEY'}
                />
              </label>
              <label className="form-field">
                <span>Auth token</span>
                <input
                  value={props.registryRuntimeDraft.authToken}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, authToken: event.target.value })}
                  placeholder={props.registry.agent_runtime.auth_token_configured ? 'Keep existing' : 'Optional'}
                />
              </label>
              <label className="form-field">
                <span>Claude permission</span>
                <input
                  value={props.registryRuntimeDraft.permissionMode}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, permissionMode: event.target.value })}
                  placeholder="acceptEdits"
                />
              </label>
              <label className="form-field">
                <span>Allowed tools</span>
                <textarea
                  className="compact-textarea"
                  value={props.registryRuntimeDraft.allowedTools}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, allowedTools: event.target.value })}
                  placeholder={'Read\nEdit\nBash'}
                  spellCheck={false}
                />
              </label>
              <label className="form-field">
                <span>Disallowed tools</span>
                <textarea
                  className="compact-textarea"
                  value={props.registryRuntimeDraft.disallowedTools}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, disallowedTools: event.target.value })}
                  placeholder="Optional"
                  spellCheck={false}
                />
              </label>
              <label className="form-field">
                <span>Setting sources</span>
                <textarea
                  className="compact-textarea"
                  value={props.registryRuntimeDraft.settingSources}
                  onChange={event => props.onRegistryRuntimeDraftChange({ ...props.registryRuntimeDraft, settingSources: event.target.value })}
                  placeholder={'project\nlocal'}
                  spellCheck={false}
                />
              </label>
            </div>
            {props.registryRuntimeResult && (
              <div className="bootstrap-result" role="status">
                <strong>Agent defaults saved</strong>
                <span>Secrets are only changed when replacement values are entered.</span>
                <code>{props.registryRuntimeResult.command}</code>
              </div>
            )}
          </form>
        )}
        {registryEnabled && props.registry && (
          <form
            className="registry-project-form"
            onSubmit={event => {
              event.preventDefault()
              props.onSaveRegistryProject()
            }}
          >
            <div className="registry-project-form-heading">
              <div>
                <p className="eyebrow">Registry</p>
                <h3>{props.editingRegistryProjectId ? 'Edit project' : 'Add project'}</h3>
              </div>
              <div className="registry-project-actions">
                {props.editingRegistryProjectId && (
                  <button className="ghost-button" type="button" onClick={props.onCancelRegistryProjectEdit}>
                    Cancel
                  </button>
                )}
                <button className="secondary-button" type="submit" disabled={props.creatingRegistryProject}>
                  {props.creatingRegistryProject ? 'Saving project' : props.editingRegistryProjectId ? 'Save project' : 'Add project'}
                </button>
              </div>
            </div>
            <div className="registry-project-fields">
              <label className="form-field">
                <span>Project ID</span>
                <input
                  value={props.registryProjectDraft.id}
                  onChange={event => props.onRegistryProjectDraftChange({ ...props.registryProjectDraft, id: event.target.value })}
                  placeholder="conjit"
                  disabled={Boolean(props.editingRegistryProjectId)}
                  required
                />
              </label>
              <label className="form-field">
                <span>Name</span>
                <input
                  value={props.registryProjectDraft.name}
                  onChange={event => props.onRegistryProjectDraftChange({ ...props.registryProjectDraft, name: event.target.value })}
                  placeholder="Conjit"
                />
              </label>
              <label className="form-field wide">
                <span>Workflow path</span>
                <input
                  value={props.registryProjectDraft.workflowPath}
                  onChange={event => props.onRegistryProjectDraftChange({ ...props.registryProjectDraft, workflowPath: event.target.value })}
                  placeholder="projects/conjit/WORKFLOW.md"
                  required
                />
              </label>
              <label className="form-field">
                <span>Project cap</span>
                <input
                  value={props.registryProjectDraft.maxConcurrentAgents}
                  onChange={event => props.onRegistryProjectDraftChange({ ...props.registryProjectDraft, maxConcurrentAgents: event.target.value })}
                  inputMode="numeric"
                  placeholder="Default"
                />
              </label>
              <label className="registry-checkbox">
                <input
                  type="checkbox"
                  checked={props.registryProjectDraft.enabled}
                  onChange={event => props.onRegistryProjectDraftChange({ ...props.registryProjectDraft, enabled: event.target.checked })}
                />
                <span>Enabled on restart</span>
              </label>
            </div>
            {props.registryProjectResult && (
              <div className="bootstrap-result" role="status">
                <strong>Project saved to registry</strong>
                <span>{props.registryProjectResult.project.workflow_path}</span>
                <code>{props.registryProjectResult.command}</code>
              </div>
            )}
            {props.registryProjectUpdateResult && (
              <div className="bootstrap-result" role="status">
                <strong>Project updated in registry</strong>
                <span>{props.registryProjectUpdateResult.project.workflow_path}</span>
                <code>{props.registryProjectUpdateResult.command}</code>
              </div>
            )}
            {props.registryProjectDeleteResult && (
              <div className="bootstrap-result" role="status">
                <strong>Project removed from registry</strong>
                <span>{props.registryProjectDeleteResult.project_name || props.registryProjectDeleteResult.project_id}</span>
                <code>{props.registryProjectDeleteResult.command}</code>
              </div>
            )}
          </form>
        )}
        {props.projectMode ? (
          <div className="setup-project-list">
            {setupProjects.map(project => {
              const registryProject = registryProjectByID.get(project.id)
              const runtimeProject = runtimeProjectByID.get(project.id)
              return (
                <div key={project.id} className="setup-project-item">
                  {runtimeProject ? (
                    <ProjectCard project={runtimeProject} active={false} onSelect={() => props.onSelectProject(project.id)} />
                  ) : (
                    <div className="pending-project-card">
                      <strong>{project.name || project.id}</strong>
                      <span>{project.id}</span>
                      <em>Pending restart</em>
                    </div>
                  )}
                  <dl>
                    <div>
                      <dt>Registry path</dt>
                      <dd>{registryProject?.workflow_path || project.workflow_path}</dd>
                    </div>
                    <div>
                      <dt>Project cap</dt>
                      <dd>{registryProject?.max_concurrent_agents ? registryProject.max_concurrent_agents.toLocaleString() : 'Default'}</dd>
                    </div>
                  </dl>
                  {registryProject && (
                    <div className="setup-project-actions">
                      <button className="ghost-button" type="button" onClick={() => props.onEditRegistryProject(registryProject)}>
                        Edit
                      </button>
                      <button
                        className="ghost-button danger"
                        type="button"
                        onClick={() => props.onRemoveRegistryProject(registryProject)}
                        disabled={props.deletingRegistryProjectId === registryProject.id}
                      >
                        {props.deletingRegistryProjectId === registryProject.id ? 'Removing' : 'Remove'}
                      </button>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        ) : (
          <EmptyState title="No registry is active" body="Restart Simphony with -config ./simphony.yaml to enable multi-project setup." />
        )}
      </div>

      <aside className="setup-side">
        <section className="panel compact-panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">Supervisor</p>
              <h2>Capacity</h2>
            </div>
          </div>
          <dl className="rate-list">
            <div>
              <dt>Configured Projects</dt>
              <dd>{configuredCount.toLocaleString()}</dd>
            </div>
            <div>
              <dt>Running Projects</dt>
              <dd>{(props.projectOverview?.runningProjects || 0).toLocaleString()}</dd>
            </div>
            <div>
              <dt>Global Slots</dt>
              <dd>
                {props.supervisorConcurrency?.max_concurrent_agents
                  ? `${props.supervisorConcurrency.used_agents}/${props.supervisorConcurrency.max_concurrent_agents}`
                  : 'Unlimited'}
              </dd>
            </div>
          </dl>
        </section>

        <section className="panel compact-panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">Defaults</p>
              <h2>Agent runtime</h2>
            </div>
          </div>
          {props.registry?.agent_runtime.configured ? (
            <dl className="rate-list">
              <div>
                <dt>Provider</dt>
                <dd>{props.registry.agent_runtime.provider || 'Default'}</dd>
              </div>
              <div>
                <dt>Model</dt>
                <dd>{props.registry.agent_runtime.model || 'Default'}</dd>
              </div>
              <div>
                <dt>Endpoint</dt>
                <dd>{props.registry.agent_runtime.endpoint_url || 'Provider default'}</dd>
              </div>
              <div>
                <dt>Secrets</dt>
                <dd>
                  {[
                    props.registry.agent_runtime.api_key_configured ? 'API key' : '',
                    props.registry.agent_runtime.auth_token_configured ? 'Auth token' : '',
                  ]
                    .filter(Boolean)
                    .join(', ') || 'None configured'}
                </dd>
              </div>
              <div>
                <dt>Env keys</dt>
                <dd>{props.registry.agent_runtime.env_keys?.join(', ') || 'None'}</dd>
              </div>
            </dl>
          ) : (
            <p className="muted">No global agent runtime defaults are configured.</p>
          )}
        </section>

        <section className="panel compact-panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">Security</p>
              <h2>Guardrails</h2>
            </div>
          </div>
          {props.registry ? (
            <div className="setup-checklist">
              <span>{props.registry.security.allow_workspace_overlap ? 'Workspace overlap allowed' : 'Workspace overlap blocked'}</span>
              <span>
                {props.registry.security.allow_workspace_under_registry_dir
                  ? 'Registry-contained workspaces allowed'
                  : 'Registry-contained workspaces blocked'}
              </span>
              <span>{props.registry.security.allow_remote_dashboard ? 'Remote dashboard allowed' : 'Remote dashboard requires opt-in'}</span>
            </div>
          ) : (
            <p className="muted">Registry security settings are not available in single-project mode.</p>
          )}
        </section>
      </aside>
    </section>
  )
}

function RegistryFact(props: { label: string; value: string }) {
  return (
    <div className="registry-fact">
      <span>{props.label}</span>
      <strong>{props.value}</strong>
    </div>
  )
}

function ProjectCard(props: { project: ProjectSummary; active: boolean; onSelect: () => void }) {
  const { project } = props
  const status = projectStatus(project)
  const activeCount = project.counts.running + project.counts.retrying
  return (
    <button
      className={`project-card ${props.active ? 'active' : ''} ${status.tone}`}
      type="button"
      onClick={props.onSelect}
      aria-pressed={props.active}
      title={project.workflow_path}
    >
      <span className="project-card-topline">
        <strong>{project.name || project.id}</strong>
        <span className={`project-status ${status.tone}`}>{status.label}</span>
      </span>
      <span className="project-card-id">{project.id}</span>
      <span className="project-card-metrics">
        <span>{activeCount.toLocaleString()} active</span>
        <span>{project.counts.completed.toLocaleString()} done</span>
        {project.max_concurrent_agents ? <span>cap {project.max_concurrent_agents.toLocaleString()}</span> : null}
      </span>
      {project.waiting_on_supervisor && (
        <span className="project-card-waiting">
          Waiting for global slot{project.last_supervisor_deferred_at ? ` since ${formatDateTime(project.last_supervisor_deferred_at)}` : ''}
        </span>
      )}
      {project.last_error && <span className="project-card-error">{project.last_error}</span>}
      {project.health.status === 'blocked' && <span className="project-card-error">{project.health.summary || 'Project preflight blocked'}</span>}
      {project.health.status === 'warning' && <span className="project-card-waiting">{project.health.summary || 'Project preflight warning'}</span>}
    </button>
  )
}

function RunningRow(props: { item: RunningSnapshot; onOpen: (identifier: string) => void }) {
  const { item } = props
  return (
    <article className="issue-row">
      <div className="status-rail running" aria-hidden="true" />
      <div className="issue-core">
        <div className="issue-heading">
          <button className="link-button" type="button" onClick={() => props.onOpen(item.issue_identifier)}>
            {item.issue_identifier}
          </button>
          <span className="pill live">Running</span>
          <span className="pill">{item.state}</span>
          {item.priority !== null && <span className="pill">P{item.priority}</span>}
        </div>
        <p>{item.issue_title || item.last_message || item.last_event || 'Session is active.'}</p>
        {item.labels.length > 0 && (
          <div className="label-strip" aria-label="Issue labels">
            {item.labels.map(label => (
              <span key={label}>{label}</span>
            ))}
          </div>
        )}
        <div className="metadata-line">
          {item.issue_url && (
            <a href={item.issue_url} target="_blank" rel="noreferrer">
              Tracker
            </a>
          )}
          <span>Session {compactID(item.session_id)}</span>
          <span>{item.turn_count} turns</span>
          <span>Started {formatRelative(item.started_at)}</span>
          <span>Last event {formatRelative(item.last_event_at, 'not yet')}</span>
        </div>
      </div>
      <div className="token-box">
        <strong>{item.tokens.total_tokens.toLocaleString()}</strong>
        <span>tokens</span>
      </div>
    </article>
  )
}

function RetryRow(props: { item: RetrySnapshot; onOpen: (identifier: string) => void }) {
  const { item } = props
  return (
    <article className="issue-row">
      <div className="status-rail retrying" aria-hidden="true" />
      <div className="issue-core">
        <div className="issue-heading">
          <button className="link-button" type="button" onClick={() => props.onOpen(item.issue_identifier)}>
            {item.issue_identifier}
          </button>
          <span className="pill warning">Retry</span>
          <span className="pill">Attempt {item.attempt}</span>
        </div>
        <p>{item.error || 'Waiting for retry backoff to expire.'}</p>
        <div className="metadata-line">
          <span>Due {formatDateTime(item.due_at)}</span>
          <span>Issue {compactID(item.issue_id)}</span>
        </div>
      </div>
      <div className="token-box">
        <strong>{formatRelative(item.due_at)}</strong>
        <span>due</span>
      </div>
    </article>
  )
}

function EmptyState(props: { title: string; body: string }) {
  return (
    <div className="empty-state">
      <div className="empty-icon" aria-hidden="true" />
      <h3>{props.title}</h3>
      <p>{props.body}</p>
    </div>
  )
}

function SettingsView(props: {
  settings: SettingsResponse | null
  settingsDraft: string
  promptDraft: string
  saving: boolean
  validatingTracker: boolean
  trackerValidation: SettingsValidationResponse | null
  onSettingsDraftChange: (value: string) => void
  onPromptDraftChange: (value: string) => void
  onReload: () => void
  onSave: () => void
  onValidateTracker: () => void
}) {
  if (!props.settings) {
    return (
      <section className="panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">Settings</p>
            <h2>Loading workflow</h2>
          </div>
        </div>
      </section>
    )
  }

  const draftConfig = parseSettingsConfig(props.settingsDraft)
  const selectedProviderID = draftConfig ? getSelectedProviderID(draftConfig) : ''
  const selectedModelID = draftConfig ? getSelectedModelID(draftConfig) : ''
  const globalModels = modelOptionsForProvider(selectedProviderID, draftConfig ? getCodexStringField(draftConfig, 'model') : '')
  const resolvedRuntime = props.settings.resolved_config.agent_runtime || props.settings.resolved_config.codex
  const trackerValidation = props.trackerValidation

  const changeTrackerField = (field: 'endpoint' | 'api_key' | 'project_slug' | 'working_state', value: string) => {
    const config = draftConfig || {}
    props.onSettingsDraftChange(JSON.stringify(applyTrackerStringField(config, field, value), null, 2))
  }
  const changeTrackerList = (field: 'active_states' | 'completion_states' | 'terminal_states', value: string) => {
    const config = draftConfig || {}
    props.onSettingsDraftChange(JSON.stringify(applyTrackerListField(config, field, value), null, 2))
  }
  const changePipelineField = (field: 'review_state' | 'review_resolution_state' | 'merge_state' | 'done_state', value: string) => {
    const config = draftConfig || {}
    props.onSettingsDraftChange(JSON.stringify(applyPipelineStringField(config, field, value), null, 2))
  }
  const changeProvider = (providerID: string) => {
    const config = draftConfig || {}
    const nextConfig = applyProviderSelection(config, providerID)
    props.onSettingsDraftChange(JSON.stringify(nextConfig, null, 2))
  }
  const changeModel = (optionID: string) => {
    const config = draftConfig || {}
    const nextConfig = applyModelSelection(config, selectedProviderID, getCodexStringField(config, 'model'), optionID)
    props.onSettingsDraftChange(JSON.stringify(nextConfig, null, 2))
  }
  const changeGlobalSkills = (value: string) => {
    const config = draftConfig || {}
    props.onSettingsDraftChange(JSON.stringify(applyGlobalSkills(config, value), null, 2))
  }
  const changeStageSkills = (stage: string, value: string) => {
    const config = draftConfig || {}
    props.onSettingsDraftChange(JSON.stringify(applyStageSkills(config, stage, value), null, 2))
  }
  const changeStageField = (stage: string, field: 'model' | 'model_provider' | 'reasoning_effort', value: string) => {
    const config = draftConfig || {}
    props.onSettingsDraftChange(JSON.stringify(applyStageField(config, stage, field, value), null, 2))
  }

  return (
    <section className="settings-layout">
      <div className="panel settings-panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">Settings</p>
            <h2>Workflow</h2>
          </div>
          <div className="settings-actions">
            <button className="secondary-button" type="button" onClick={props.onReload} disabled={props.saving}>
              Reload
            </button>
            <button className="icon-button" type="button" onClick={props.onSave} disabled={props.saving}>
              <span>{props.saving ? 'Saving' : 'Save'}</span>
            </button>
          </div>
        </div>
        <div className="settings-meta">
          <span>{props.settings.workflow_path}</span>
          {props.settings.validation_error && <strong>{props.settings.validation_error}</strong>}
        </div>
        <div className="settings-section-heading">
          <div>
            <p className="eyebrow">Linear</p>
            <h3>Project and workflow</h3>
          </div>
          <button
            className="secondary-button"
            type="button"
            onClick={props.onValidateTracker}
            disabled={!draftConfig || props.saving || props.validatingTracker}
          >
            {props.validatingTracker ? 'Testing' : 'Test connection'}
          </button>
        </div>
        <div className="settings-grid">
          <label className="form-field">
            <span>Project slug</span>
            <input
              value={draftConfig ? getTrackerStringField(draftConfig, 'project_slug') : ''}
              onChange={event => changeTrackerField('project_slug', event.target.value)}
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>API key</span>
            <input
              value={draftConfig ? getTrackerStringField(draftConfig, 'api_key') : ''}
              onChange={event => changeTrackerField('api_key', event.target.value)}
              placeholder="$LINEAR_API_KEY"
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Endpoint</span>
            <input
              value={draftConfig ? getTrackerStringField(draftConfig, 'endpoint') : ''}
              onChange={event => changeTrackerField('endpoint', event.target.value)}
              placeholder="https://api.linear.app/graphql"
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Working state</span>
            <input
              value={draftConfig ? getTrackerStringField(draftConfig, 'working_state') : ''}
              onChange={event => changeTrackerField('working_state', event.target.value)}
              placeholder="In Progress"
              disabled={!draftConfig || props.saving}
            />
          </label>
        </div>
        <div className="settings-grid three-up">
          <label className="form-field">
            <span>Active states</span>
            <textarea
              className="list-textarea"
              value={draftConfig ? stringListToText(getTrackerStringList(draftConfig, 'active_states')) : ''}
              onChange={event => changeTrackerList('active_states', event.target.value)}
              placeholder={'Todo\nIn Progress\nApproved'}
              spellCheck={false}
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Completion states</span>
            <textarea
              className="list-textarea"
              value={draftConfig ? stringListToText(getTrackerStringList(draftConfig, 'completion_states')) : ''}
              onChange={event => changeTrackerList('completion_states', event.target.value)}
              placeholder={'In Review\nDone'}
              spellCheck={false}
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Terminal states</span>
            <textarea
              className="list-textarea"
              value={draftConfig ? stringListToText(getTrackerStringList(draftConfig, 'terminal_states')) : ''}
              onChange={event => changeTrackerList('terminal_states', event.target.value)}
              placeholder={'Done\nCanceled'}
              spellCheck={false}
              disabled={!draftConfig || props.saving}
            />
          </label>
        </div>
        <div className="settings-grid four-up">
          <label className="form-field">
            <span>Review</span>
            <input
              value={draftConfig ? getPipelineStringField(draftConfig, 'review_state') : ''}
              onChange={event => changePipelineField('review_state', event.target.value)}
              placeholder="In Review"
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Review resolution</span>
            <input
              value={draftConfig ? getPipelineStringField(draftConfig, 'review_resolution_state') : ''}
              onChange={event => changePipelineField('review_resolution_state', event.target.value)}
              placeholder="Review Resolution"
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Merge</span>
            <input
              value={draftConfig ? getPipelineStringField(draftConfig, 'merge_state') : ''}
              onChange={event => changePipelineField('merge_state', event.target.value)}
              placeholder="Approved"
              disabled={!draftConfig || props.saving}
            />
          </label>
          <label className="form-field">
            <span>Done</span>
            <input
              value={draftConfig ? getPipelineStringField(draftConfig, 'done_state') : ''}
              onChange={event => changePipelineField('done_state', event.target.value)}
              placeholder="Done"
              disabled={!draftConfig || props.saving}
            />
          </label>
        </div>
        {trackerValidation && (
          <div className="validation-result" role="status">
            <strong>{trackerValidation.message || 'Linear settings validated'}</strong>
            <span>
              {trackerValidation.project_slug || 'Project'} - {trackerValidation.candidate_count.toLocaleString()} candidate issues
            </span>
          </div>
        )}
        <div className="settings-field">
          <span>Model</span>
          <div className="settings-control-row">
            <label className="select-field">
              <span className="sr-only">Model provider</span>
              <select value={selectedProviderID} onChange={event => changeProvider(event.target.value)} disabled={!draftConfig || props.saving}>
                <option value="">Codex default provider</option>
                {providerOptionsWithCurrent(selectedProviderID).map(option => (
                  <option key={option.id} value={option.id}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
            <label className="select-field">
              <span className="sr-only">Provider model</span>
              <select value={selectedModelID} onChange={event => changeModel(event.target.value)} disabled={!draftConfig || props.saving}>
                <option value="">Provider default model</option>
                {globalModels.map(option => (
                  <option key={option.id} value={option.id}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
            <div className="model-summary">
              <strong>{modelLabel(resolvedRuntime.model, resolvedRuntime.model_provider)}</strong>
              <span>{resolvedRuntime.provider || resolvedRuntime.model_provider || 'default provider'}</span>
            </div>
          </div>
        </div>
        <div className="settings-field">
          <span>Default Skills</span>
          <div className="skill-editor">
            <textarea
              className="compact-textarea"
              value={draftConfig ? skillListToText(getGlobalSkills(draftConfig)) : ''}
              onChange={event => changeGlobalSkills(event.target.value)}
              placeholder={'conjit-product-ui\narchitecture-review'}
              spellCheck={false}
              disabled={!draftConfig || props.saving}
            />
            <p className="field-help">One skill per line. Use a skill name, or name|absolute path when you want to pin a specific SKILL.md.</p>
          </div>
        </div>
        <div className="settings-field">
          <span>Stage Overrides</span>
          <div className="stage-skill-grid">
            {SKILL_STAGE_OPTIONS.map(stage => (
              <label key={stage.id} className="stage-skill-card">
                <strong>{stage.label}</strong>
                <select
                  value={draftConfig ? getStageProviderID(draftConfig, stage.id) : ''}
                  onChange={event => changeStageField(stage.id, 'model_provider', event.target.value)}
                  disabled={!draftConfig || props.saving}
                >
                  <option value="">Default provider</option>
                  {providerOptionsWithCurrent(draftConfig ? getStageProviderID(draftConfig, stage.id) : '').map(option => (
                    <option key={option.id} value={option.id}>
                      {option.label}
                    </option>
                  ))}
                </select>
                <select
                  value={draftConfig ? getStageModelID(draftConfig, stage.id) : ''}
                  onChange={event => changeStageField(stage.id, 'model', event.target.value)}
                  disabled={!draftConfig || props.saving}
                >
                  <option value="">Default model</option>
                  {modelOptionsForProvider(
                    draftConfig ? getStageProviderID(draftConfig, stage.id) : '',
                    draftConfig ? getStageStringField(draftConfig, stage.id, 'model') : '',
                  ).map(option => (
                    <option key={option.id} value={option.id}>
                      {option.label}
                    </option>
                  ))}
                </select>
                <select
                  value={draftConfig ? getStageStringField(draftConfig, stage.id, 'reasoning_effort') : ''}
                  onChange={event => changeStageField(stage.id, 'reasoning_effort', event.target.value)}
                  disabled={!draftConfig || props.saving}
                >
                  {reasoningOptionsForSelection(
                    draftConfig ? getStageStringField(draftConfig, stage.id, 'model') : '',
                    draftConfig ? getStageProviderID(draftConfig, stage.id) : '',
                    draftConfig ? getStageStringField(draftConfig, stage.id, 'reasoning_effort') : '',
                  ).map(option => (
                    <option key={option.value || 'default'} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
                <textarea
                  value={draftConfig ? skillListToText(getStageSkills(draftConfig, stage.id)) : ''}
                  onChange={event => changeStageSkills(stage.id, event.target.value)}
                  placeholder={
                    stage.id === 'review'
                      ? 'code-review\nsecurity-review'
                      : stage.id === 'review_resolution'
                        ? 'github:gh-address-comments\ngithub:gh-fix-ci'
                        : stage.id === 'coding'
                          ? 'conjit-product-ui'
                          : 'github:yeet'
                  }
                  spellCheck={false}
                  disabled={!draftConfig || props.saving}
                />
              </label>
            ))}
          </div>
        </div>
        <label className="settings-field">
          <span>Front Matter JSON</span>
          <textarea value={props.settingsDraft} onChange={event => props.onSettingsDraftChange(event.target.value)} spellCheck={false} />
        </label>
        <label className="settings-field">
          <span>Prompt Template</span>
          <textarea value={props.promptDraft} onChange={event => props.onPromptDraftChange(event.target.value)} spellCheck={false} />
        </label>
      </div>

      <aside className="panel compact-panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">Resolved</p>
            <h2>Runtime</h2>
          </div>
        </div>
        <dl className="rate-list">
          <div>
            <dt>Project</dt>
            <dd>{props.settings.resolved_config.tracker.project_slug || 'None'}</dd>
          </div>
          <div>
            <dt>Active States</dt>
            <dd>{props.settings.resolved_config.tracker.active_states.join(', ') || 'None'}</dd>
          </div>
          <div>
            <dt>Terminal States</dt>
            <dd>{props.settings.resolved_config.tracker.terminal_states.join(', ') || 'None'}</dd>
          </div>
          <div>
            <dt>Workspace</dt>
            <dd>{props.settings.resolved_config.workspace.mode}</dd>
          </div>
          <div>
            <dt>Agent</dt>
            <dd>{resolvedRuntime.provider || 'codex'}</dd>
          </div>
          <div>
            <dt>Command</dt>
            <dd>{resolvedRuntime.command || 'embedded shim'}</dd>
          </div>
          <div>
            <dt>Model</dt>
            <dd>{modelLabel(resolvedRuntime.model, resolvedRuntime.model_provider)}</dd>
          </div>
          <div>
            <dt>Default Skills</dt>
            <dd>{formatSkillSummary(props.settings.resolved_config.codex.skills)}</dd>
          </div>
          <div>
            <dt>Stage Overrides</dt>
            <dd>{formatStageOverrideSummary(props.settings.resolved_config.codex.stage_overrides)}</dd>
          </div>
        </dl>
      </aside>
    </section>
  )
}

function parseSettingsConfig(value: string): Record<string, unknown> | null {
  try {
    const parsed = JSON.parse(value || '{}')
    return isPlainObject(parsed) ? parsed : null
  } catch {
    return null
  }
}

function getTrackerStringField(config: Record<string, unknown>, field: 'endpoint' | 'api_key' | 'project_slug' | 'working_state') {
  const tracker = isPlainObject(config.tracker) ? config.tracker : {}
  return typeof tracker[field] === 'string' ? tracker[field] : ''
}

function getTrackerStringList(config: Record<string, unknown>, field: 'active_states' | 'completion_states' | 'terminal_states') {
  const tracker = isPlainObject(config.tracker) ? config.tracker : {}
  return normalizeStringList(tracker[field])
}

function getPipelineStringField(
  config: Record<string, unknown>,
  field: 'review_state' | 'review_resolution_state' | 'merge_state' | 'done_state',
) {
  const pipeline = isPlainObject(config.pipeline) ? config.pipeline : {}
  return typeof pipeline[field] === 'string' ? pipeline[field] : ''
}

function applyTrackerStringField(
  config: Record<string, unknown>,
  field: 'endpoint' | 'api_key' | 'project_slug' | 'working_state',
  value: string,
) {
  const nextConfig = { ...config }
  const tracker = isPlainObject(nextConfig.tracker) ? { ...nextConfig.tracker } : {}
  const trimmedValue = value.trim()
  if (trimmedValue === '') {
    delete tracker[field]
  } else {
    tracker[field] = trimmedValue
  }
  nextConfig.tracker = tracker
  return nextConfig
}

function applyTrackerListField(config: Record<string, unknown>, field: 'active_states' | 'completion_states' | 'terminal_states', value: string) {
  const nextConfig = { ...config }
  const tracker = isPlainObject(nextConfig.tracker) ? { ...nextConfig.tracker } : {}
  const values = parseStringList(value)
  if (values.length === 0) {
    delete tracker[field]
  } else {
    tracker[field] = values
  }
  nextConfig.tracker = tracker
  return nextConfig
}

function applyPipelineStringField(
  config: Record<string, unknown>,
  field: 'review_state' | 'review_resolution_state' | 'merge_state' | 'done_state',
  value: string,
) {
  const nextConfig = { ...config }
  const pipeline = isPlainObject(nextConfig.pipeline) ? { ...nextConfig.pipeline } : {}
  const trimmedValue = value.trim()
  if (trimmedValue === '') {
    delete pipeline[field]
  } else {
    pipeline[field] = trimmedValue
  }
  nextConfig.pipeline = pipeline
  return nextConfig
}

function getSelectedProviderID(config: Record<string, unknown>) {
  const codex = isPlainObject(config.codex) ? config.codex : {}
  const model = typeof codex.model === 'string' ? codex.model : ''
  const provider = typeof codex.model_provider === 'string' ? codex.model_provider : ''
  return selectedProviderID(model, provider)
}

function getSelectedModelID(config: Record<string, unknown>) {
  const codex = isPlainObject(config.codex) ? config.codex : {}
  const model = typeof codex.model === 'string' ? codex.model : ''
  const provider = typeof codex.model_provider === 'string' ? codex.model_provider : ''
  return selectedModelID(model, provider)
}

function getCodexStringField(config: Record<string, unknown>, field: 'model' | 'model_provider' | 'reasoning_effort') {
  const codex = isPlainObject(config.codex) ? config.codex : {}
  return typeof codex[field] === 'string' ? codex[field] : ''
}

function applyProviderSelection(config: Record<string, unknown>, providerID: string) {
  const nextConfig = { ...config }
  const codex = isPlainObject(nextConfig.codex) ? { ...nextConfig.codex } : {}
  const provider = PROVIDER_OPTIONS.find(item => item.id === providerID)

  if (!provider) {
    delete codex.model_provider
    delete codex.model
  } else {
    codex.model_provider = provider.id
    const currentModel = typeof codex.model === 'string' ? codex.model : ''
    if (currentModel && !provider.models.some(option => option.model === currentModel)) {
      delete codex.model
    }
  }

  nextConfig.codex = codex
  return nextConfig
}

function applyModelSelection(config: Record<string, unknown>, providerID: string, currentModel: string, optionID: string) {
  const nextConfig = { ...config }
  const codex = isPlainObject(nextConfig.codex) ? { ...nextConfig.codex } : {}
  const option = modelOptionsForProvider(providerID, currentModel).find(item => item.id === optionID)

  if (!option) {
    delete codex.model
    if (!providerID) {
      delete codex.model_provider
    }
  } else {
    codex.model = option.model
    codex.model_provider = option.modelProvider
  }

  nextConfig.codex = codex
  return nextConfig
}

function getGlobalSkills(config: Record<string, unknown>) {
  const codex = isPlainObject(config.codex) ? config.codex : {}
  return normalizeSkillRefs(codex.skills)
}

function getStageSkills(config: Record<string, unknown>, stage: string) {
  const codex = isPlainObject(config.codex) ? config.codex : {}
  const overrides = isPlainObject(codex.stage_overrides) ? codex.stage_overrides : {}
  const stageConfig = isPlainObject(overrides[stage]) ? overrides[stage] : {}
  return normalizeSkillRefs(stageConfig.skills)
}

function getStageStringField(config: Record<string, unknown>, stage: string, field: 'model' | 'model_provider' | 'reasoning_effort') {
  const codex = isPlainObject(config.codex) ? config.codex : {}
  const overrides = isPlainObject(codex.stage_overrides) ? codex.stage_overrides : {}
  const stageConfig = isPlainObject(overrides[stage]) ? overrides[stage] : {}
  return typeof stageConfig[field] === 'string' ? stageConfig[field] : ''
}

function getStageProviderID(config: Record<string, unknown>, stage: string) {
  return selectedProviderID(getStageStringField(config, stage, 'model'), getStageStringField(config, stage, 'model_provider'))
}

function getStageModelID(config: Record<string, unknown>, stage: string) {
  return selectedModelID(getStageStringField(config, stage, 'model'), getStageStringField(config, stage, 'model_provider'))
}

function applyGlobalSkills(config: Record<string, unknown>, value: string) {
  const nextConfig = { ...config }
  const codex = isPlainObject(nextConfig.codex) ? { ...nextConfig.codex } : {}
  const skills = parseSkillList(value)
  if (skills.length === 0) {
    delete codex.skills
  } else {
    codex.skills = skills
  }
  nextConfig.codex = codex
  return nextConfig
}

function applyStageField(config: Record<string, unknown>, stage: string, field: 'model' | 'model_provider' | 'reasoning_effort', value: string) {
  const nextConfig = { ...config }
  const codex = isPlainObject(nextConfig.codex) ? { ...nextConfig.codex } : {}
  const overrides = isPlainObject(codex.stage_overrides) ? { ...codex.stage_overrides } : {}
  const stageConfig = isPlainObject(overrides[stage]) ? { ...overrides[stage] } : {}
  const trimmedValue = value.trim()

  if (field === 'model_provider') {
    const provider = PROVIDER_OPTIONS.find(item => item.id === trimmedValue)
    if (!provider) {
      delete stageConfig.model_provider
      delete stageConfig.model
    } else {
      stageConfig.model_provider = provider.id
      const currentModel = typeof stageConfig.model === 'string' ? stageConfig.model : ''
      if (currentModel && !provider.models.some(option => option.model === currentModel)) {
        delete stageConfig.model
      }
    }
  } else if (field === 'model') {
    const currentProvider = typeof stageConfig.model_provider === 'string' ? stageConfig.model_provider : ''
    const currentModel = typeof stageConfig.model === 'string' ? stageConfig.model : ''
    const option = modelOptionsForProvider(currentProvider, currentModel).find(item => item.id === trimmedValue)
    if (!option) {
      delete stageConfig.model
    } else {
      stageConfig.model = option.model
      stageConfig.model_provider = option.modelProvider
    }
  } else {
    if (trimmedValue === '') {
      delete stageConfig[field]
    } else {
      stageConfig[field] = trimmedValue
    }
  }
  return saveStageConfig(nextConfig, codex, overrides, stage, stageConfig)
}

function applyStageSkills(config: Record<string, unknown>, stage: string, value: string) {
  const nextConfig = { ...config }
  const codex = isPlainObject(nextConfig.codex) ? { ...nextConfig.codex } : {}
  const overrides = isPlainObject(codex.stage_overrides) ? { ...codex.stage_overrides } : {}
  const stageConfig = isPlainObject(overrides[stage]) ? { ...overrides[stage] } : {}
  const skills = parseSkillList(value)
  if (skills.length === 0) {
    delete stageConfig.skills
  } else {
    stageConfig.skills = skills
  }
  return saveStageConfig(nextConfig, codex, overrides, stage, stageConfig)
}

function saveStageConfig(
  nextConfig: Record<string, unknown>,
  codex: Record<string, unknown>,
  overrides: Record<string, unknown>,
  stage: string,
  stageConfig: Record<string, unknown>,
) {
  if (Object.keys(stageConfig).length === 0) {
    delete overrides[stage]
  } else {
    overrides[stage] = stageConfig
  }
  if (Object.keys(overrides).length === 0) {
    delete codex.stage_overrides
  } else {
    codex.stage_overrides = overrides
  }
  nextConfig.codex = codex
  return nextConfig
}

function parseSkillList(value: string) {
  return value
    .split(/\r?\n|,/)
    .map(item => item.trim())
    .filter(Boolean)
    .map(item => {
      const [name, ...pathParts] = item.split('|')
      const path = pathParts.join('|').trim()
      return path ? { name: name.trim(), path } : name.trim()
    })
    .filter(item => (typeof item === 'string' ? item.length > 0 : item.name.length > 0 || item.path.length > 0))
}

function parseStringList(value: string) {
  return value
    .split(/\r?\n|,/)
    .map(item => item.trim())
    .filter(Boolean)
}

function normalizeStringList(value: unknown) {
  if (!Array.isArray(value)) {
    return []
  }
  return value.map(item => (typeof item === 'string' ? item.trim() : '')).filter(Boolean)
}

function stringListToText(values: string[]) {
  return values.join('\n')
}

function normalizeSkillRefs(value: unknown): Array<string | { name: string; path?: string }> {
  if (!Array.isArray(value)) {
    return []
  }
  return value
    .map(item => {
      if (typeof item === 'string') {
        return item.trim()
      }
      if (!isPlainObject(item)) {
        return ''
      }
      const name = typeof item.name === 'string' ? item.name.trim() : ''
      const path = typeof item.path === 'string' ? item.path.trim() : ''
      return path ? { name, path } : name
    })
    .filter(item => (typeof item === 'string' ? item.length > 0 : item.name.length > 0 || item.path.length > 0))
}

function skillListToText(skills: Array<string | { name: string; path?: string }>) {
  return skills
    .map(skill => (typeof skill === 'string' ? skill : skill.path ? `${skill.name}|${skill.path}` : skill.name))
    .join('\n')
}

function providerOptionsWithCurrent(providerID: string) {
  if (!providerID || PROVIDER_OPTIONS.some(option => option.id === providerID)) {
    return PROVIDER_OPTIONS
  }
  return [{ id: providerID, label: `${providerID} (custom)`, models: [], reasoning: DEFAULT_REASONING_OPTIONS }, ...PROVIDER_OPTIONS]
}

function modelOptionsForProvider(providerID: string, currentModel: string) {
  const provider = PROVIDER_OPTIONS.find(option => option.id === providerID)
  const options = provider ? provider.models : MODEL_OPTIONS
  if (!currentModel || options.some(option => option.model === currentModel)) {
    return options
  }
  const customProvider = providerID || 'custom'
  return [{ id: `${customProvider}:${currentModel}`, label: `${currentModel} (custom)`, model: currentModel, modelProvider: customProvider }, ...options]
}

function selectedProviderID(model: string, provider: string) {
  if (provider) {
    return provider
  }
  return MODEL_OPTIONS.find(option => option.model === model)?.modelProvider || ''
}

function selectedModelID(model: string, provider: string) {
  if (!model) {
    return ''
  }
  const option = MODEL_OPTIONS.find(item => item.model === model && (!provider || item.modelProvider === provider))
  return option?.id || `${provider || 'custom'}:${model}`
}

function reasoningOptionsForSelection(model: string, provider: string, currentValue: string) {
  const option = MODEL_OPTIONS.find(item => item.model === model && (!provider || item.modelProvider === provider))
  const providerOption = PROVIDER_OPTIONS.find(item => item.id === (provider || option?.modelProvider))
  const options = option?.reasoning || providerOption?.reasoning || DEFAULT_REASONING_OPTIONS
  if (!currentValue || options.some(item => item.value === currentValue)) {
    return options
  }
  return [{ value: currentValue, label: `${currentValue} (custom)` }, ...options]
}

function modelLabel(model?: string, provider?: string) {
  if (!model) {
    return 'Codex default'
  }
  const option = MODEL_OPTIONS.find(item => item.model === model && item.modelProvider === provider)
  return option?.label || model
}

function formatSkillSummary(skills?: Array<{ name: string; path?: string }>) {
  if (!skills || skills.length === 0) {
    return 'None'
  }
  return skills.map(skill => skill.name || skill.path || 'Unnamed').join(', ')
}

function formatStageOverrideSummary(
  overrides?: Record<string, { model?: string; model_provider?: string; reasoning_effort?: string; skills?: Array<{ name: string; path?: string }> }>,
) {
  if (!overrides) {
    return 'None'
  }
  const summaries = Object.entries(overrides)
    .map(([stage, override]) => {
      const skills = override.skills || []
      const values = [
        override.model ? `model ${override.model}` : '',
        override.model_provider ? `provider ${override.model_provider}` : '',
        override.reasoning_effort ? `reasoning ${override.reasoning_effort}` : '',
        skills.length > 0 ? `skills ${skills.map(skill => skill.name || skill.path || 'Unnamed').join(', ')}` : '',
      ].filter(Boolean)
      return values.length > 0 ? `${stage}: ${values.join('; ')}` : ''
    })
    .filter(Boolean)
  return summaries.length > 0 ? summaries.join(' | ') : 'None'
}

function CapacityBar(props: { label: string; value: number; total: number }) {
  const percent = props.total > 0 ? Math.min(100, (props.value / props.total) * 100) : 0
  const width = props.value > 0 ? Math.max(4, percent) : 0
  return (
    <div className="capacity-row">
      <div className="capacity-label">
        <span>{props.label}</span>
        <strong>{props.value.toLocaleString()}</strong>
      </div>
      <div
        className="bar-track"
        role="progressbar"
        aria-label={`${props.label} usage`}
        aria-valuemin={0}
        aria-valuemax={props.total}
        aria-valuenow={Math.min(props.value, props.total)}
      >
        <div className="bar-fill" style={{ width: `${width}%` }} />
      </div>
    </div>
  )
}

function RateLimitPanel(props: { rateLimits: Record<string, unknown> | null }) {
  if (!props.rateLimits || Object.keys(props.rateLimits).length === 0) {
    return <p className="muted">No rate limit data has been reported yet.</p>
  }

  return (
    <dl className="rate-list">
      {Object.entries(props.rateLimits).map(([key, value]) => (
        <div key={key}>
          <dt>{humanizeKey(key)}</dt>
          <dd>{formatUnknown(value)}</dd>
        </div>
      ))}
    </dl>
  )
}

function ActivityFeed(props: { items: ActivityItem[]; onOpen: (identifier: string) => void }) {
  if (props.items.length === 0) {
    return <p className="muted">No active runtime events are available.</p>
  }

  return (
    <ol className="activity-feed">
      {props.items.map(item => (
        <li key={item.id}>
          <span className={`activity-dot ${item.tone}`} aria-hidden="true" />
          <div>
            <button className="link-button" type="button" onClick={() => props.onOpen(item.label)}>
              {item.label}
            </button>
            <strong>{item.title}</strong>
            <p>{item.body}</p>
            <span>{item.tone === 'retrying' ? `Due ${formatRelative(item.at)}` : formatRelative(item.at)}</span>
          </div>
        </li>
      ))}
    </ol>
  )
}

function IssueDrawer(props: { detailState: DetailState; onClose: () => void }) {
  const drawerRef = useRef<HTMLElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const drawerOpen = props.detailState.status !== 'idle'

  useEffect(() => {
    if (!drawerOpen) {
      return undefined
    }

    const previousActiveElement = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const previousBodyOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    closeButtonRef.current?.focus()

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        props.onClose()
        return
      }

      if (event.key === 'Tab') {
        trapFocus(event, drawerRef.current)
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
      document.body.style.overflow = previousBodyOverflow
      previousActiveElement?.focus()
    }
  }, [drawerOpen, props.onClose])

  if (props.detailState.status === 'idle') {
    return null
  }

  const title =
    props.detailState.status === 'ready'
      ? props.detailState.detail.issue_identifier
      : props.detailState.status === 'loading'
        ? props.detailState.identifier
        : props.detailState.identifier

  return (
    <div className="drawer-backdrop" role="presentation" onClick={props.onClose}>
      <aside
        ref={drawerRef}
        className="drawer"
        role="dialog"
        aria-labelledby="issue-drawer-title"
        aria-modal="true"
        tabIndex={-1}
        onClick={event => event.stopPropagation()}
      >
        <header className="drawer-header">
          <div>
            <p className="eyebrow">Issue detail</p>
            <h2 id="issue-drawer-title">{title}</h2>
          </div>
          <button ref={closeButtonRef} className="close-button" type="button" onClick={props.onClose} title="Close details">
            <span aria-hidden="true" />
            <span className="sr-only">Close</span>
          </button>
        </header>

        {props.detailState.status === 'loading' && <EmptyState title="Loading issue" body="Fetching workspace, attempts, logs, and recent events." />}
        {props.detailState.status === 'error' && <div className="alert alert-error">{props.detailState.message}</div>}
        {props.detailState.status === 'ready' && <IssueDetail detail={props.detailState.detail} />}
      </aside>
    </div>
  )
}

function IssueDetail(props: { detail: IssueDetailResponse }) {
  const { detail } = props
  return (
    <div className="drawer-content">
      <div className="detail-grid">
        <DetailStat label="Status" value={detail.status} />
        <DetailStat label="Restarts" value={detail.attempts.restart_count.toLocaleString()} />
        <DetailStat label="Retry attempt" value={detail.attempts.current_retry_attempt.toLocaleString()} />
      </div>

      <section className="detail-section">
        <h3>Workspace</h3>
        <code>{detail.workspace.path || 'No workspace assigned'}</code>
      </section>

      {detail.running && (
        <section className="detail-section">
          <h3>Live session</h3>
          {detail.running.issue_title && <p>{detail.running.issue_title}</p>}
          {detail.running.labels.length > 0 && (
            <div className="label-strip" aria-label="Issue labels">
              {detail.running.labels.map(label => (
                <span key={label}>{label}</span>
              ))}
            </div>
          )}
          <div className="session-card">
            <DetailStat label="Session" value={compactID(detail.running.session_id)} />
            <DetailStat label="Turns" value={detail.running.turn_count.toLocaleString()} />
            <DetailStat label="Tokens" value={detail.running.tokens.total_tokens.toLocaleString()} />
          </div>
        </section>
      )}

      {detail.retry && (
        <section className="detail-section">
          <h3>Retry backoff</h3>
          <div className="session-card">
            <DetailStat label="Attempt" value={detail.retry.attempt.toLocaleString()} />
            <DetailStat label="Due" value={formatRelative(detail.retry.due_at)} />
            <DetailStat label="Issue" value={compactID(detail.retry.issue_id)} />
          </div>
          {detail.retry.error && <p className="error-copy">{detail.retry.error}</p>}
        </section>
      )}

      {detail.last_error && !detail.retry && (
        <section className="detail-section">
          <h3>Last error</h3>
          <p className="error-copy">{detail.last_error}</p>
        </section>
      )}

      <section className="detail-section">
        <h3>Recent events</h3>
        {detail.recent_events.length === 0 ? (
          <p className="muted">No events recorded for this issue.</p>
        ) : (
          <ol className="event-list">
            {detail.recent_events.map(event => (
              <li key={`${event.at}-${event.event}-${event.message}`}>
                <span>{formatDateTime(event.at)}</span>
                <strong>{event.event}</strong>
                <p>{event.message || 'No message'}</p>
              </li>
            ))}
          </ol>
        )}
      </section>

      <section className="detail-section">
        <h3>Codex logs</h3>
        {detail.logs.codex_session_logs.length === 0 ? (
          <p className="muted">No Codex session logs have been attached.</p>
        ) : (
          <div className="log-list">
            {detail.logs.codex_session_logs.map(log =>
              log.url ? (
                <a key={`${log.label}-${log.path}`} href={log.url}>
                  <span>{log.label}</span>
                  <code>{log.path}</code>
                </a>
              ) : (
                <div className="log-item" key={`${log.label}-${log.path}`}>
                  <span>{log.label}</span>
                  <code>{log.path}</code>
                </div>
              ),
            )}
          </div>
        )}
      </section>

      {Object.keys(detail.tracked).length > 0 && (
        <section className="detail-section">
          <h3>Tracked metadata</h3>
          <dl className="rate-list">
            {Object.entries(detail.tracked).map(([key, value]) => (
              <div key={key}>
                <dt>{humanizeKey(key)}</dt>
                <dd>{formatUnknown(value)}</dd>
              </div>
            ))}
          </dl>
        </section>
      )}
    </div>
  )
}

function DetailStat(props: { label: string; value: string }) {
  return (
    <div className="detail-stat">
      <span>{props.label}</span>
      <strong>{props.value}</strong>
    </div>
  )
}

function trapFocus(event: KeyboardEvent, container: HTMLElement | null) {
  if (!container) {
    return
  }

  const focusable = Array.from(
    container.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter(element => element.offsetParent !== null)

  if (focusable.length === 0) {
    event.preventDefault()
    container.focus()
    return
  }

  const first = focusable[0]
  const last = focusable[focusable.length - 1]

  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

function matchesIssueQuery(item: RunningSnapshot | RetrySnapshot, query: string) {
  const normalized = query.trim().toLowerCase()
  if (!normalized) {
    return true
  }

  const values =
    'session_id' in item
      ? [item.issue_identifier, item.issue_id, item.issue_title, item.state, item.session_id, item.last_event, item.last_message, ...item.labels]
      : [item.issue_identifier, item.issue_id, item.error || '', `attempt ${item.attempt}`]

  return values.some(value => value.toLowerCase().includes(normalized))
}

function defaultProjectID(projects: ProjectSummary[]) {
  return (
    projects.find(project => project.running)?.id ||
    projects.find(project => project.enabled)?.id ||
    projects[0]?.id ||
    null
  )
}

function emptyRegistrySettingsDraft(): RegistrySettingsDraft {
  return {
    bindAddress: '127.0.0.1',
    port: '8080',
    dashboardEnabled: true,
    apiPrefix: '/api/v1',
    maxConcurrentAgents: '',
    defaultProjectMaxConcurrentAgents: '',
    allowWorkspaceOverlap: false,
    allowWorkspaceUnderRegistryDir: false,
    allowRemoteDashboard: false,
  }
}

function registrySettingsDraftFromRegistry(registry: RegistryResponse): RegistrySettingsDraft {
  return {
    bindAddress: registry.server?.bind_address || '127.0.0.1',
    port: registry.server?.port ? String(registry.server.port) : '8080',
    dashboardEnabled: registry.server?.dashboard_enabled ?? true,
    apiPrefix: registry.server?.api_prefix || '/api/v1',
    maxConcurrentAgents: registry.concurrency.max_concurrent_agents > 0 ? String(registry.concurrency.max_concurrent_agents) : '',
    defaultProjectMaxConcurrentAgents:
      registry.concurrency.default_project_max_concurrent_agents > 0
        ? String(registry.concurrency.default_project_max_concurrent_agents)
        : '',
    allowWorkspaceOverlap: registry.security.allow_workspace_overlap,
    allowWorkspaceUnderRegistryDir: registry.security.allow_workspace_under_registry_dir,
    allowRemoteDashboard: registry.security.allow_remote_dashboard,
  }
}

function emptyRegistryRuntimeDraft(): RegistryRuntimeDraft {
  return {
    sdkProvider: 'codex',
    command: '',
    modelProvider: '',
    model: '',
    reasoningEffort: '',
    endpointURL: '',
    apiKey: '',
    authToken: '',
    permissionMode: '',
    allowedTools: '',
    disallowedTools: '',
    settingSources: '',
  }
}

function registryRuntimeDraftFromRegistry(registry: RegistryResponse): RegistryRuntimeDraft {
  const runtime = registry.agent_runtime || { configured: false }
  return {
    sdkProvider: runtime.provider || 'codex',
    command: runtime.command || '',
    modelProvider: runtime.model_provider || '',
    model: runtime.model || '',
    reasoningEffort: runtime.reasoning_effort || '',
    endpointURL: runtime.endpoint_url || '',
    apiKey: '',
    authToken: '',
    permissionMode: runtime.permission_mode || '',
    allowedTools: stringListToText(runtime.allowed_tools || []),
    disallowedTools: stringListToText(runtime.disallowed_tools || []),
    settingSources: stringListToText(runtime.setting_sources || []),
  }
}

function projectStatus(project: ProjectSummary) {
  if (!project.enabled) {
    return { label: 'Disabled', tone: 'disabled' as const }
  }
  if (project.health.status === 'blocked') {
    return { label: 'Blocked', tone: 'blocked' as const }
  }
  if (project.waiting_on_supervisor) {
    return { label: 'Waiting', tone: 'waiting' as const }
  }
  if (!project.running) {
    return { label: project.last_error ? 'Failed' : 'Stopped', tone: project.last_error ? 'failed' as const : 'stopped' as const }
  }
  if (project.counts.retrying > 0) {
    return { label: 'Retrying', tone: 'retrying' as const }
  }
  if (project.counts.running > 0) {
    return { label: 'Running', tone: 'running' as const }
  }
  if (project.health.status === 'warning') {
    return { label: 'Warning', tone: 'warning' as const }
  }
  return { label: 'Idle', tone: 'idle' as const }
}

function normalizeError(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function initialDashboardLocation(): { page: Page; projectId: string | null } {
  const params = new URLSearchParams(window.location.search)
  const page = normalizePage(params.get('view') || params.get('page'))
  const projectId = cleanProjectParam(params.get('project'))
  return { page, projectId }
}

function syncDashboardLocation(page: Page, projectId: string | null) {
  const url = new URL(window.location.href)
  if (page === 'runtime') {
    url.searchParams.delete('view')
  } else {
    url.searchParams.set('view', page)
  }
  if (projectId) {
    url.searchParams.set('project', projectId)
  } else {
    url.searchParams.delete('project')
  }
  const next = `${url.pathname}${url.search}${url.hash}`
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`
  if (next !== current) {
    window.history.replaceState(null, '', next)
  }
}

function normalizePage(value: string | null): Page {
  if (value === 'settings' || value === 'setup') {
    return value
  }
  return 'runtime'
}

function cleanProjectParam(value: string | null) {
  const trimmed = value?.trim() || ''
  return trimmed || null
}

function percentOf(value: number, total: number) {
  if (total <= 0) {
    return 0
  }
  return Math.round((value / total) * 100)
}

function formatDateTime(value: string, fallback = value) {
  const date = new Date(value)
  if (!isMeaningfulDate(date)) {
    return fallback
  }
  return date.toLocaleString([], {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatRelative(value: string, fallback = value) {
  const date = new Date(value)
  if (!isMeaningfulDate(date)) {
    return fallback
  }

  const diffSeconds = Math.round((date.getTime() - Date.now()) / 1000)
  const absoluteSeconds = Math.abs(diffSeconds)
  const unit =
    absoluteSeconds < 60
      ? 'second'
      : absoluteSeconds < 3600
        ? 'minute'
        : absoluteSeconds < 86400
          ? 'hour'
          : 'day'
  const divisor = unit === 'second' ? 1 : unit === 'minute' ? 60 : unit === 'hour' ? 3600 : 86400
  const valueInUnit = Math.round(diffSeconds / divisor)

  return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(valueInUnit, unit)
}

function displayTimestamp(primary: string, fallback: string) {
  return isMeaningfulDate(new Date(primary)) ? primary : fallback
}

function isMeaningfulDate(date: Date) {
  return !Number.isNaN(date.getTime()) && date.getFullYear() > 1970
}

function formatDuration(seconds: number) {
  if (seconds < 60) {
    return `${Math.round(seconds)}s`
  }
  if (seconds < 3600) {
    return `${Math.round(seconds / 60)}m`
  }
  return `${(seconds / 3600).toFixed(1)}h`
}

function formatMilliseconds(milliseconds: number) {
  if (milliseconds <= 0) {
    return 'Unknown'
  }
  return formatDuration(milliseconds / 1000)
}

function entryKey(item: RunningSnapshot | RetrySnapshot) {
  if ('session_id' in item) {
    return item.issue_id || item.session_id || item.issue_identifier
  }
  return item.issue_id || item.issue_identifier
}

function compactID(value: string, fallback = 'pending') {
  if (!value) {
    return fallback
  }
  if (value.length <= 12) {
    return value
  }
  return `${value.slice(0, 6)}...${value.slice(-4)}`
}

function humanizeKey(value: string) {
  return value.replace(/[_-]/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function formatUnknown(value: unknown) {
  if (value === null || value === undefined) {
    return 'None'
  }
  if (typeof value === 'object') {
    return JSON.stringify(value)
  }
  return String(value)
}

export default App
