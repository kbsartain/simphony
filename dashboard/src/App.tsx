import { useCallback, useEffect, useState } from 'react'
import { RefreshResponse, StateSnapshot } from './api/types'

const POLL_INTERVAL_MS = 5000

function App() {
  const [state, setState] = useState<StateSnapshot | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [refreshResult, setRefreshResult] = useState<RefreshResponse | null>(null)

  const loadState = useCallback(async () => {
    const response = await fetch('/api/v1/state', { cache: 'no-store' })
    if (!response.ok) {
      throw new Error(`State request failed: ${response.status}`)
    }
    const data = (await response.json()) as StateSnapshot
    setState(data)
    setLastUpdated(new Date())
    setError(null)
  }, [])

  useEffect(() => {
    loadState().catch(err => setError(err.message))

    const interval = window.setInterval(() => {
      loadState().catch(err => setError(err.message))
    }, POLL_INTERVAL_MS)

    return () => window.clearInterval(interval)
  }, [loadState])

  const requestRefresh = async () => {
    setRefreshing(true)
    setRefreshResult(null)
    try {
      const response = await fetch('/api/v1/refresh', {
        method: 'POST',
        cache: 'no-store',
      })
      if (!response.ok) {
        throw new Error(`Refresh request failed: ${response.status}`)
      }
      setRefreshResult((await response.json()) as RefreshResponse)
      await loadState()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setRefreshing(false)
    }
  }

  if (error) return <div style={{ padding: 20 }}>Error: {error}</div>
  if (!state) return <div style={{ padding: 20 }}>Loading...</div>

  return (
    <div style={{ padding: 20, fontFamily: 'system-ui, sans-serif' }}>
      <h1>Simphony Dashboard</h1>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <p style={{ margin: 0 }}>Running: {state.counts.running} | Retrying: {state.counts.retrying}</p>
        <button type="button" onClick={requestRefresh} disabled={refreshing}>
          {refreshing ? 'Refreshing...' : 'Refresh now'}
        </button>
      </div>
      <p style={{ color: '#555' }}>
        API snapshot: {formatDate(state.generated_at)} | UI updated: {lastUpdated ? lastUpdated.toLocaleTimeString() : 'never'} | Polling every {POLL_INTERVAL_MS / 1000}s
      </p>
      {refreshResult && (
        <p style={{ color: '#555' }}>
          Refresh queued: {String(refreshResult.queued)} | Coalesced: {String(refreshResult.coalesced)}
        </p>
      )}
      <h2>Running Sessions</h2>
      {state.running.length === 0 && <p>No active sessions</p>}
      {state.running.map(r => (
        <div key={r.session_id} style={{ border: '1px solid #ccc', padding: 10, marginBottom: 10 }}>
          <strong>{r.issue_identifier}</strong> - {r.state}<br />
          Session: {r.session_id} | Turns: {r.turn_count}<br />
          Last event: {r.last_event} - {r.last_message}
        </div>
      ))}
      <h2>Retry Queue</h2>
      {state.retrying.length === 0 && <p>No retries queued</p>}
      {state.retrying.map(r => (
        <div key={r.issue_id} style={{ border: '1px solid #ccc', padding: 10, marginBottom: 10 }}>
          <strong>{r.issue_identifier}</strong> - attempt {r.attempt}<br />
          Due: {r.due_at}<br />
          {r.error && <span>Error: {r.error}</span>}
        </div>
      ))}
      <h2>Totals</h2>
      <p>
        Input: {state.codex_totals.input_tokens} | Output: {state.codex_totals.output_tokens} | Total: {state.codex_totals.total_tokens}
      </p>
      <p>Runtime: {state.codex_totals.seconds_running}s</p>
    </div>
  )
}

function formatDate(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleTimeString()
}

export default App
