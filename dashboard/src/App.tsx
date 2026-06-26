import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  IssueDetailResponse,
  RefreshResponse,
  RetrySnapshot,
  RunningSnapshot,
  SettingsResponse,
  StateSnapshot,
} from './api/types'
import { fetchIssueDetail, fetchSettings, fetchState, requestRefresh as requestRefreshAPI, saveSettings } from './api/client'
import './App.css'

const DEFAULT_POLL_INTERVAL_MS = 5000
const MIN_UI_POLL_INTERVAL_MS = 1000

type QueueFilter = 'all' | 'running' | 'retrying'
type Page = 'runtime' | 'settings'
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
  const [page, setPage] = useState<Page>('runtime')
  const [state, setState] = useState<StateSnapshot | null>(null)
  const [settings, setSettings] = useState<SettingsResponse | null>(null)
  const [settingsDraft, setSettingsDraft] = useState('')
  const [promptDraft, setPromptDraft] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [savingSettings, setSavingSettings] = useState(false)
  const [refreshResult, setRefreshResult] = useState<RefreshResponse | null>(null)
  const [filter, setFilter] = useState<QueueFilter>('all')
  const [query, setQuery] = useState('')
  const [detailState, setDetailState] = useState<DetailState>({ status: 'idle' })
  const stateRequestID = useRef(0)
  const detailRequestID = useRef(0)
  const pollIntervalMs = Math.max(state?.poll_interval_ms || DEFAULT_POLL_INTERVAL_MS, MIN_UI_POLL_INTERVAL_MS)

  const loadState = useCallback(async () => {
    const requestID = stateRequestID.current + 1
    stateRequestID.current = requestID
    try {
      const data = await fetchState()
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
  }, [])

  const loadSettings = useCallback(async () => {
    const data = await fetchSettings()
    setSettings(data)
    setSettingsDraft(JSON.stringify(data.config || {}, null, 2))
    setPromptDraft(data.prompt_template || '')
    setError(null)
  }, [])

  useEffect(() => {
    const interval = window.setInterval(() => {
      void loadState()
    }, pollIntervalMs)

    return () => window.clearInterval(interval)
  }, [loadState, pollIntervalMs])

  useEffect(() => {
    void loadState()
  }, [loadState])

  useEffect(() => {
    if (page === 'settings' && !settings) {
      loadSettings().catch(err => setError(normalizeError(err)))
    }
  }, [loadSettings, page, settings])

  const requestRefresh = async () => {
    setRefreshing(true)
    setRefreshResult(null)
    try {
      setRefreshResult(await requestRefreshAPI())
      await loadState()
      setNotice(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setRefreshing(false)
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
      const data = await saveSettings({ config, prompt_template: promptDraft })
      setSettings(data)
      setSettingsDraft(JSON.stringify(data.config || {}, null, 2))
      setPromptDraft(data.prompt_template || '')
      setNotice('Settings saved')
      setError(null)
    } catch (err) {
      setError(normalizeError(err))
    } finally {
      setSavingSettings(false)
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
      const detail = await fetchIssueDetail(identifier)
      if (detailRequestID.current === requestID) {
        setDetailState({ status: 'ready', detail })
      }
    } catch (err) {
      if (detailRequestID.current === requestID) {
        setDetailState({ status: 'error', identifier, message: normalizeError(err) })
      }
    }
  }, [])

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

  return (
    <main className="app-shell">
      <header className="topbar">
        <div className="title-block">
          <div className="brand-mark" aria-hidden="true">
            S
          </div>
          <div>
            <p className="eyebrow">Simphony command center</p>
            <h1>Agent operations</h1>
          </div>
        </div>
        <div className="topbar-actions">
          <div className="segment-control" role="group" aria-label="Dashboard page">
            <FilterButton active={page === 'runtime'} label="Runtime" onClick={() => setPage('runtime')} />
            <FilterButton active={page === 'settings'} label="Settings" onClick={() => setPage('settings')} />
          </div>
          <div className="sync-copy">
            <span>Snapshot {formatDateTime(state.generated_at)}</span>
            <span>UI {lastUpdated ? lastUpdated.toLocaleTimeString() : 'never'}</span>
          </div>
          <button className="icon-button" type="button" onClick={requestRefresh} disabled={refreshing} title="Refresh now">
            <span aria-hidden="true" className={refreshing ? 'refresh-glyph spinning' : 'refresh-glyph'} />
            <span>{refreshing ? 'Syncing' : 'Sync'}</span>
          </button>
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

      {page === 'runtime' ? (
        <>
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
      ) : (
        <SettingsView
          settings={settings}
          settingsDraft={settingsDraft}
          promptDraft={promptDraft}
          saving={savingSettings}
          onSettingsDraftChange={setSettingsDraft}
          onPromptDraftChange={setPromptDraft}
          onReload={() => loadSettings().catch(err => setError(normalizeError(err)))}
          onSave={saveWorkflowSettings}
        />
      )}
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
  onSettingsDraftChange: (value: string) => void
  onPromptDraftChange: (value: string) => void
  onReload: () => void
  onSave: () => void
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
              <strong>{modelLabel(props.settings.resolved_config.codex.model, props.settings.resolved_config.codex.model_provider)}</strong>
              <span>{props.settings.resolved_config.codex.model_provider || 'default provider'}</span>
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
            <dt>Codex</dt>
            <dd>{props.settings.resolved_config.codex.command}</dd>
          </div>
          <div>
            <dt>Model</dt>
            <dd>{modelLabel(props.settings.resolved_config.codex.model, props.settings.resolved_config.codex.model_provider)}</dd>
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

function normalizeError(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
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
