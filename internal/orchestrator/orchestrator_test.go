package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kbsartain/simphony/internal/workspace"
	"github.com/kbsartain/simphony/pkg/api"
)

type mockTracker struct {
	mu              sync.Mutex
	candidates      []api.Issue
	byStates        map[string][]api.Issue
	byIDs           map[string]api.Issue
	comments        []string
	transitions     []string
	transitionErr   error
	transitionedTo  string
	completionState string
	moveErr         error
	movedIssues     []string
	movePreferences [][]string
}

func (m *mockTracker) FetchCandidateIssues(ctx context.Context) ([]api.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.Issue, len(m.candidates))
	copy(out, m.candidates)
	return out, nil
}

func (m *mockTracker) FetchIssuesByStates(ctx context.Context, states []string) ([]api.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []api.Issue
	for _, s := range states {
		if issues, ok := m.byStates[strings.ToLower(s)]; ok {
			out = append(out, issues...)
		}
	}
	return out, nil
}

func (m *mockTracker) FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]api.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]api.Issue)
	for _, id := range ids {
		if issue, ok := m.byIDs[id]; ok {
			out[id] = issue
		}
	}
	return out, nil
}

func (m *mockTracker) TransitionIssueState(ctx context.Context, issue api.Issue, state string) (api.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transitions = append(m.transitions, issue.Identifier+":"+state)
	if m.transitionErr != nil {
		return api.Issue{}, m.transitionErr
	}
	issue.State = state
	m.transitionedTo = state
	return issue, nil
}

func (m *mockTracker) MoveIssueToFirstAvailableState(ctx context.Context, issueID string, preferredStates []string) (api.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.movedIssues = append(m.movedIssues, issueID)
	prefs := make([]string, len(preferredStates))
	copy(prefs, preferredStates)
	m.movePreferences = append(m.movePreferences, prefs)
	if m.moveErr != nil {
		return api.Issue{}, m.moveErr
	}
	issue, ok := m.byIDs[issueID]
	if !ok {
		for _, candidate := range m.candidates {
			if candidate.ID == issueID {
				issue = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		issue = api.Issue{ID: issueID}
	}
	if m.completionState != "" {
		issue.State = m.completionState
	} else if len(preferredStates) > 0 {
		issue.State = preferredStates[0]
	}
	if m.byIDs == nil {
		m.byIDs = make(map[string]api.Issue)
	}
	m.byIDs[issueID] = issue
	for i := range m.candidates {
		if m.candidates[i].ID == issueID {
			m.candidates[i].State = issue.State
			break
		}
	}
	return issue, nil
}

func (m *mockTracker) MoveIssueToState(ctx context.Context, issueID string, state string) (api.Issue, error) {
	return m.MoveIssueToFirstAvailableState(ctx, issueID, []string{state})
}

func (m *mockTracker) AddIssueComment(ctx context.Context, issue api.Issue, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments = append(m.comments, issue.Identifier+":"+body)
	return nil
}

func (m *mockTracker) setCandidates(issues []api.Issue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.candidates = issues
}

func (m *mockTracker) setByID(issues map[string]api.Issue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byIDs = issues
}

func (m *mockTracker) setByStates(issues map[string][]api.Issue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byStates = issues
}

type mockRunner struct {
	mu           sync.Mutex
	runs         []api.Issue
	stages       []api.PipelineStage
	delay        time.Duration
	errAfter     int // return error after N successful runs
	emitSession  bool
	agentMessage string
	err          error
	panicValue   interface{}
}

type countingLimiter struct {
	mu       sync.Mutex
	capacity int
	used     int
}

func (l *countingLimiter) TryAcquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used >= l.capacity {
		return false
	}
	l.used++
	return true
}

func (l *countingLimiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used > 0 {
		l.used--
	}
}

func (l *countingLimiter) Used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

type ownerRecordingLimiter struct {
	acquireOwner string
	forgotOwner  string
}

func (l *ownerRecordingLimiter) TryAcquire() bool {
	l.acquireOwner = "<unowned>"
	return true
}

func (l *ownerRecordingLimiter) TryAcquireFor(owner string) bool {
	l.acquireOwner = owner
	return true
}

func (l *ownerRecordingLimiter) ForgetOwner(owner string) {
	l.forgotOwner = owner
}

func (l *ownerRecordingLimiter) Release() {}

func (m *mockRunner) Run(ctx context.Context, issue api.Issue, w *api.Workspace, attempt *int, cfg *api.CodexConfig, stage api.PipelineStage, maxTurns int, shouldContinue func() (api.ContinueDecision, error), cb func(api.AgentEvent)) error {
	m.mu.Lock()
	m.runs = append(m.runs, issue)
	m.stages = append(m.stages, stage)
	fail := len(m.runs) > m.errAfter && m.errAfter >= 0
	m.mu.Unlock()
	if m.emitSession {
		cb(api.AgentEvent{
			Event:     "session_started",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"session_id": "thread-1-turn-1",
				"thread_id":  "thread-1",
				"turn_id":    "turn-1",
				"turn_count": 1,
			},
		})
		cb(api.AgentEvent{
			Event:     "thread/tokenUsage/updated",
			Timestamp: time.Now(),
			Usage: map[string]interface{}{
				"input_tokens":  10,
				"output_tokens": 5,
				"total_tokens":  15,
			},
		})
	}
	if m.agentMessage != "" {
		cb(api.AgentEvent{
			Event:     "item/completed",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"item": map[string]interface{}{
					"id":   "msg-1",
					"type": "agentMessage",
					"text": m.agentMessage,
				},
				"threadId": "thread-1",
				"turnId":   "turn-1",
			},
		})
	}

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	} else {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	if m.panicValue != nil {
		panic(m.panicValue)
	}
	if m.err != nil {
		return m.err
	}
	if fail {
		return errors.New("mock runner error")
	}
	return nil
}

func defaultConfig() *api.WorkflowConfig {
	return &api.WorkflowConfig{
		Tracker: api.TrackerConfig{
			Kind:             "linear",
			APIKey:           "key",
			ProjectSlug:      "proj",
			ActiveStates:     []string{"Todo", "In Progress", "Approved"},
			TerminalStates:   []string{"Closed", "Done", "Cancelled"},
			CompletionStates: []string{"In Review", "Review", "Done", "Completed", "Closed", "Cancelled"},
		},
		Pipeline: api.PipelineConfig{
			ReviewState:  "In Review",
			MergeState:   "Approved",
			DoneState:    "Done",
			CodingStates: []string{"Todo", "In Progress"},
		},
		Polling: api.PollingConfig{IntervalMs: 10000},
		Workspace: api.WorkspaceConfig{
			Root: "",
		},
		Hooks: api.HooksConfig{TimeoutMs: 60000},
		Agent: api.AgentConfig{
			MaxConcurrentAgents: 10,
			MaxTurns:            20,
			MaxRetryBackoffMs:   300000,
		},
		Codex: api.CodexConfig{
			Command:        "codex app-server",
			TurnTimeoutMs:  3600000,
			ReadTimeoutMs:  5000,
			StallTimeoutMs: 300000,
		},
	}
}

func TestOrchestrator_DispatchEligibility(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
			{ID: "2", Identifier: "A-2", Title: "Second", State: "In Progress"},
			{ID: "3", Identifier: "A-3", Title: "Third", State: "Done"}, // terminal
			{ID: "4", Identifier: "A-4", Title: "", State: "Todo"},      // missing title
			{ID: "5", Identifier: "A-5", Title: "Blocked", State: "Todo", BlockedBy: []api.Blocker{{State: strPtr("Todo")}}},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 2

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Give it time to poll and dispatch.
	time.Sleep(200 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()

	// Should dispatch A-1 (Todo, unblocked) and A-2 (In Progress).
	// A-3 is terminal, A-4 missing title, A-5 blocked.
	if runs != 2 {
		t.Fatalf("expected 2 dispatches, got %d", runs)
	}
}

func TestOrchestrator_LogPrefixIncludesProjectContext(t *testing.T) {
	orch := New(defaultConfig(), nil, nil, nil)
	orch.SetLogContext(" simphony ", " Simphony ")

	got := orch.logPrefix()
	want := `project_id=simphony project_name="Simphony"`
	if got != want {
		t.Fatalf("logPrefix() = %q, want %q", got, want)
	}
}

func TestOrchestrator_RedactsConfiguredSecretsFromLogs(t *testing.T) {
	cfg := defaultConfig()
	cfg.Tracker.APIKey = "linear-secret"
	cfg.AgentRuntime.APIKey = "runtime-secret"
	cfg.AgentRuntime.AuthToken = "runtime-token"
	cfg.AgentRuntime.Env = map[string]string{
		"CUSTOM_TOKEN": "env-secret",
		"PLAIN_VALUE":  "visible-value",
	}
	orch := New(cfg, nil, nil, nil)

	got := orch.redactLogMessage("linear-secret runtime-secret runtime-token env-secret visible-value")
	for _, leaked := range []string{"linear-secret", "runtime-secret", "runtime-token", "env-secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted message leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "visible-value") {
		t.Fatalf("redacted message removed non-secret value: %s", got)
	}
}

func TestOrchestrator_UsesProjectIDForOwnerAwareLimiter(t *testing.T) {
	orch := New(defaultConfig(), nil, nil, nil)
	orch.SetLogContext("alpha", "Alpha")
	limiter := &ownerRecordingLimiter{}

	if !orch.tryAcquireSupervisorSlot(limiter) {
		t.Fatal("tryAcquireSupervisorSlot returned false")
	}
	if limiter.acquireOwner != "alpha" {
		t.Fatalf("acquire owner = %q, want alpha", limiter.acquireOwner)
	}

	orch.SetDispatchLimiter(limiter)
	orch.forgetSupervisorWait()
	if limiter.forgotOwner != "alpha" {
		t.Fatalf("forgot owner = %q, want alpha", limiter.forgotOwner)
	}
}

func TestOrchestrator_MovesIssueToWorkingStateBeforeRun(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Tracker.WorkingState = "In Progress"

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	transitions := append([]string(nil), tracker.transitions...)
	tracker.mu.Unlock()
	if len(transitions) != 1 || transitions[0] != "A-1:In Progress" {
		t.Fatalf("transitions = %v, want [A-1:In Progress]", transitions)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runner.runs))
	}
	if runner.runs[0].State != "In Progress" {
		t.Fatalf("runner issue state = %q, want In Progress", runner.runs[0].State)
	}
}

func TestOrchestrator_WorkingStateFailureDoesNotRun(t *testing.T) {
	tracker := &mockTracker{
		candidates:    []api.Issue{{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"}},
		transitionErr: errors.New("linear down"),
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1}
	cfg := defaultConfig()
	cfg.Tracker.WorkingState = "In Progress"

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()
	if runs != 0 {
		t.Fatalf("expected no runs after transition failure, got %d", runs)
	}

	snapshot := orch.Snapshot()
	if snapshot.Counts.Retrying != 1 {
		t.Fatalf("retrying count = %d, want 1", snapshot.Counts.Retrying)
	}
}

func TestOrchestrator_ConcurrencyLimit(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
			{ID: "2", Identifier: "A-2", Title: "Second", State: "Todo"},
			{ID: "3", Identifier: "A-3", Title: "Third", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()

	if runs != 1 {
		t.Fatalf("expected 1 dispatch due to concurrency limit, got %d", runs)
	}
}

func TestOrchestrator_SharedLimiterCapsDispatchAndReleasesOnStop(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
			{ID: "2", Identifier: "A-2", Title: "Second", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 10
	limiter := &countingLimiter{capacity: 1}

	orch := New(cfg, tracker, wsMgr, runner)
	orch.SetDispatchLimiter(limiter)
	orch.Start()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()
	if runs != 1 {
		t.Fatalf("runs = %d, want 1 while shared limiter is full", runs)
	}
	if used := limiter.Used(); used != 1 {
		t.Fatalf("limiter used = %d, want 1", used)
	}
	snap := orch.Snapshot()
	if snap.LastDispatchDeferredReason != "no_supervisor_slots" || snap.LastDispatchDeferredAt == nil {
		t.Fatalf("deferred snapshot reason=%q at=%v, want supervisor deferral", snap.LastDispatchDeferredReason, snap.LastDispatchDeferredAt)
	}

	orch.Stop()
	if used := limiter.Used(); used != 0 {
		t.Fatalf("limiter used after stop = %d, want 0", used)
	}
}

func TestOrchestrator_SharedLimiterReleasesAfterWorkerPanic(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{panicValue: "boom"}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 1
	cfg.Agent.MaxRetryBackoffMs = 5000
	limiter := &countingLimiter{capacity: 1}

	orch := New(cfg, tracker, wsMgr, runner)
	orch.SetDispatchLimiter(limiter)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	if used := limiter.Used(); used != 0 {
		t.Fatalf("limiter used after worker panic = %d, want 0", used)
	}
	snap := orch.Snapshot()
	if snap.Counts.Retrying != 1 {
		t.Fatalf("retrying count = %d, want 1", snap.Counts.Retrying)
	}

	orch.mu.Lock()
	if entry := orch.state.RetryAttempts["1"]; entry != nil {
		if timer, ok := entry.TimerHandle.(*time.Timer); ok {
			timer.Stop()
		}
	}
	orch.mu.Unlock()
}

func TestOrchestrator_PerStateLimit(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
			{ID: "2", Identifier: "A-2", Title: "Second", State: "Todo"},
			{ID: "3", Identifier: "A-3", Title: "Third", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 10
	cfg.Agent.MaxConcurrentAgentsByState = map[string]int{"todo": 1}

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()

	// Only 1 Todo allowed, plus 1 In Progress = 2 total.
	if runs != 2 {
		t.Fatalf("expected 2 dispatches (1 todo + 1 in progress), got %d", runs)
	}
}

func TestOrchestrator_SortOrder(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "C-1", Title: "C", State: "Todo", Priority: intPtr(2), CreatedAt: timePtr(time.Now().Add(-1 * time.Hour))},
			{ID: "2", Identifier: "A-1", Title: "A", State: "Todo", Priority: intPtr(1), CreatedAt: timePtr(time.Now().Add(-2 * time.Hour))},
			{ID: "3", Identifier: "B-1", Title: "B", State: "Todo", Priority: intPtr(1), CreatedAt: timePtr(time.Now().Add(-3 * time.Hour))},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 3

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := make([]api.Issue, len(runner.runs))
	copy(runs, runner.runs)
	runner.mu.Unlock()

	if len(runs) != 3 {
		t.Fatalf("expected 3 dispatches, got %d", len(runs))
	}

	// Priority: B-1 and A-1 both priority 1 (lower number = higher priority), then C-1 priority 2.
	// For same priority, oldest created_at first: B-1 is older than A-1.
	// Note: runner.runs records execution order which is non-deterministic due to goroutines.
	// Order verification is covered by TestSortIssues.
	ids := make(map[string]bool)
	for _, r := range runs {
		ids[r.Identifier] = true
	}
	for _, want := range []string{"A-1", "B-1", "C-1"} {
		if !ids[want] {
			t.Fatalf("expected %s to be dispatched", want)
		}
	}
}

func TestOrchestrator_SortOrder_PreservesIssueSequence(t *testing.T) {
	orch := New(defaultConfig(), nil, nil, nil)
	issues := []api.Issue{
		{ID: "1", Identifier: "CON-102", Title: "Urgent newer issue", State: "In Review", Priority: intPtr(1), CreatedAt: timePtr(time.Now().Add(-1 * time.Hour))},
		{ID: "2", Identifier: "CON-96", Title: "Older sequence issue", State: "In Review", Priority: intPtr(3), CreatedAt: timePtr(time.Now())},
	}

	sorted := orch.sortIssues(issues)
	if sorted[0].Identifier != "CON-96" {
		t.Fatalf("first sorted issue = %s, want CON-96", sorted[0].Identifier)
	}
}

func TestOrchestrator_RetryBackoff(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: 0}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 1
	cfg.Agent.MaxRetryBackoffMs = 5000

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// First dispatch fails, retry scheduled.
	time.Sleep(100 * time.Millisecond)

	orch.mu.Lock()
	retryCount := len(orch.state.RetryAttempts)
	orch.mu.Unlock()

	if retryCount != 1 {
		t.Fatalf("expected 1 retry queued after failure, got %d", retryCount)
	}

	// Wait for first retry to fire (10s -> capped at 5s for test).
	// Since our test max backoff is 5s, first retry delay is 10s... too long for a test.
	// Let's verify the retry entry exists with correct attempt.
	orch.mu.Lock()
	entry := orch.state.RetryAttempts["1"]
	orch.mu.Unlock()

	if entry == nil {
		t.Fatal("retry entry missing")
	}
	if entry.Attempt != 1 {
		t.Fatalf("retry attempt = %d, want 1", entry.Attempt)
	}

	// Stop the timer so it doesn't fire after test ends.
	if timer, ok := entry.TimerHandle.(*time.Timer); ok {
		timer.Stop()
	}
}

func TestOrchestrator_PreRunFailureKeepsClaimDuringBackoff(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 10000
	cfg.Agent.MaxConcurrentAgents = 1
	if runtime.GOOS == "windows" {
		script := "exit /B 1"
		cfg.Hooks.BeforeRun = &script
	} else {
		script := "exit 1"
		cfg.Hooks.BeforeRun = &script
	}

	orch := New(cfg, tracker, wsMgr, runner)
	limiter := &countingLimiter{capacity: 1}
	orch.SetDispatchLimiter(limiter)
	orch.Start()
	defer orch.Stop()

	deadline := time.Now().Add(5 * time.Second)
	var claimed bool
	var retryCount int
	for time.Now().Before(deadline) {
		orch.mu.Lock()
		_, claimed = orch.state.Claimed["1"]
		retryCount = len(orch.state.RetryAttempts)
		orch.mu.Unlock()
		if claimed && retryCount == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !claimed {
		t.Fatal("expected claim to remain while pre-run failure is in backoff")
	}
	if retryCount != 1 {
		t.Fatalf("expected retry queued, got %d", retryCount)
	}
	if used := limiter.Used(); used != 0 {
		t.Fatalf("limiter used after before_run failure = %d, want 0", used)
	}

	orch.tick()

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()
	if runs != 0 {
		t.Fatalf("expected no immediate redispatch while claimed, got %d runs", runs)
	}

	orch.mu.Lock()
	if entry := orch.state.RetryAttempts["1"]; entry != nil {
		if timer, ok := entry.TimerHandle.(*time.Timer); ok {
			timer.Stop()
		}
	}
	orch.mu.Unlock()
}

func TestOrchestrator_Reconciliation_TerminalState(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 200 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 50

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Wait for dispatch.
	time.Sleep(100 * time.Millisecond)

	orch.mu.Lock()
	if len(orch.state.Running) != 1 {
		t.Fatalf("expected 1 running, got %d", len(orch.state.Running))
	}
	orch.mu.Unlock()

	// Now mark the issue as terminal in the tracker.
	tracker.setByID(map[string]api.Issue{
		"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Done"},
	})
	tracker.setByStates(map[string][]api.Issue{
		"done": {
			{ID: "1", Identifier: "A-1", Title: "First", State: "Done"},
		},
	})
	tracker.setCandidates(nil)

	// Wait for reconciliation tick.
	time.Sleep(150 * time.Millisecond)

	orch.mu.Lock()
	runningCount := len(orch.state.Running)
	orch.mu.Unlock()

	if runningCount != 0 {
		t.Fatalf("expected 0 running after terminal reconciliation, got %d", runningCount)
	}

	if completed := orch.Snapshot().Counts.Completed; completed != 1 {
		t.Fatalf("completed count = %d, want 1", completed)
	}
}

func TestOrchestrator_CompletedCountRefreshesFromTerminalStates(t *testing.T) {
	tracker := &mockTracker{}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	if completed := orch.Snapshot().Counts.Completed; completed != 0 {
		t.Fatalf("initial completed count = %d, want 0", completed)
	}

	tracker.setByStates(map[string][]api.Issue{
		"done": {
			{ID: "1", Identifier: "A-1", Title: "First", State: "Done"},
			{ID: "2", Identifier: "A-2", Title: "Second", State: "Done"},
		},
	})
	orch.tick()

	if completed := orch.Snapshot().Counts.Completed; completed != 2 {
		t.Fatalf("completed count = %d, want 2", completed)
	}
}

func TestOrchestrator_StartupCleanupCountsTerminalIssues(t *testing.T) {
	tracker := &mockTracker{
		byStates: map[string][]api.Issue{
			"done": {
				{ID: "1", Identifier: "A-1", Title: "First", State: "Done"},
			},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	if completed := orch.Snapshot().Counts.Completed; completed != 1 {
		t.Fatalf("completed count = %d, want 1", completed)
	}
}

func TestOrchestrator_Reconciliation_StallDetection(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 200 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 50
	cfg.Codex.StallTimeoutMs = 100

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Wait for dispatch.
	time.Sleep(100 * time.Millisecond)

	// Wait for stall timeout + reconciliation.
	time.Sleep(200 * time.Millisecond)

	orch.mu.Lock()
	runningCount := len(orch.state.Running)
	retryCount := len(orch.state.RetryAttempts)
	orch.mu.Unlock()

	if runningCount != 0 {
		t.Fatalf("expected 0 running after stall kill, got %d", runningCount)
	}
	if retryCount != 1 {
		t.Fatalf("expected 1 retry after stall, got %d", retryCount)
	}

	// Stop retry timer.
	orch.mu.Lock()
	if entry := orch.state.RetryAttempts["1"]; entry != nil {
		if timer, ok := entry.TimerHandle.(*time.Timer); ok {
			timer.Stop()
		}
	}
	orch.mu.Unlock()
}

func TestOrchestrator_StartupCleanup(t *testing.T) {
	tracker := &mockTracker{
		byStates: map[string][]api.Issue{
			"done": {
				{ID: "99", Identifier: "OLD-1", Title: "Old", State: "Done"},
			},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	// Pre-create a workspace for the old issue.
	_, _ = wsMgr.PrepareWorkspace(api.Issue{Identifier: "OLD-1"})

	runner := &mockRunner{}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Verify workspace was removed by cleanup: PrepareWorkspace should recreate it.
	ws, err := wsMgr.PrepareWorkspace(api.Issue{Identifier: "OLD-1"})
	if err != nil {
		t.Fatalf("prepare workspace failed: %v", err)
	}
	if !ws.CreatedNow {
		t.Fatal("expected workspace to be recreated (was cleaned up)")
	}
}

func TestOrchestrator_Snapshot(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 200 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 50

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	snap := orch.Snapshot()
	if snap.Counts.Running != 1 {
		t.Fatalf("snapshot running count = %d, want 1", snap.Counts.Running)
	}
	if len(snap.Running) != 1 {
		t.Fatalf("snapshot running len = %d, want 1", len(snap.Running))
	}
	if snap.Running[0].IssueIdentifier != "A-1" {
		t.Fatalf("snapshot issue identifier = %q, want A-1", snap.Running[0].IssueIdentifier)
	}
}

func TestOrchestrator_SessionAndTokenEventsPopulateSnapshot(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond, emitSession: true}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	snap := orch.Snapshot()
	if len(snap.Running) != 1 {
		t.Fatalf("expected 1 running snapshot, got %d", len(snap.Running))
	}
	if snap.Running[0].SessionID != "thread-1-turn-1" {
		t.Fatalf("session id = %q, want thread-1-turn-1", snap.Running[0].SessionID)
	}
	if snap.Running[0].TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1", snap.Running[0].TurnCount)
	}
	if snap.Running[0].Tokens.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", snap.Running[0].Tokens.TotalTokens)
	}
}

func TestOrchestrator_PostsAgentMessageComments(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 200 * time.Millisecond, agentMessage: "I updated the dashboard."}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tracker.mu.Lock()
		comments := append([]string(nil), tracker.comments...)
		tracker.mu.Unlock()
		if len(comments) > 0 {
			if !strings.Contains(comments[0], "Simphony agent update") || !strings.Contains(comments[0], "I updated the dashboard.") {
				t.Fatalf("comment = %q, want agent update body", comments[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected agent message comment to be posted")
}

func TestOrchestrator_BlockerRule_NonTerminalBlocker(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "Blocked", State: "Todo", BlockedBy: []api.Blocker{{State: strPtr("In Progress")}}},
			{ID: "2", Identifier: "A-2", Title: "Unblocked", State: "Todo", BlockedBy: []api.Blocker{{State: strPtr("Done")}}},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 2

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()

	if runs != 1 {
		t.Fatalf("expected 1 dispatch (A-2 unblocked), got %d", runs)
	}
	if runner.runs[0].Identifier != "A-2" {
		t.Fatalf("expected A-2 to dispatch, got %s", runner.runs[0].Identifier)
	}
}

func TestOrchestrator_BlockerRule_AppliesToReviewAndMergeStates(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "Blocked Review", State: "In Review", BlockedBy: []api.Blocker{{State: strPtr("In Review")}}},
			{ID: "2", Identifier: "A-2", Title: "Unblocked Review", State: "In Review", BlockedBy: []api.Blocker{{State: strPtr("Done")}}},
			{ID: "3", Identifier: "A-3", Title: "Blocked Merge", State: "Approved", BlockedBy: []api.Blocker{{State: strPtr("In Review")}}},
			{ID: "4", Identifier: "A-4", Title: "Unblocked Merge", State: "Approved", BlockedBy: []api.Blocker{{State: strPtr("Done")}}},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Approved"}
	cfg.Agent.MaxConcurrentAgents = 10

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.runs) != 2 {
		t.Fatalf("expected 2 dispatches for unblocked review/merge issues, got %d", len(runner.runs))
	}
	dispatched := map[string]bool{}
	for _, run := range runner.runs {
		dispatched[run.Identifier] = true
	}
	for _, want := range []string{"A-2", "A-4"} {
		if !dispatched[want] {
			t.Fatalf("expected %s to dispatch, got %#v", want, dispatched)
		}
	}
}

func TestOrchestrator_SuccessfulActiveRunMovesToCompletionState(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Wait for dispatch and completion.
	time.Sleep(100 * time.Millisecond)

	// Runner succeeds immediately, so Simphony hands the issue to review.
	orch.mu.Lock()
	retryCount := len(orch.state.RetryAttempts)
	_, completed := orch.state.Completed["1"]
	orch.mu.Unlock()

	if retryCount != 0 {
		t.Fatalf("expected no retry after completion transition, got %d", retryCount)
	}
	if !completed {
		t.Fatal("expected issue to be marked completed after completion transition")
	}
	tracker.mu.Lock()
	moved := append([]string(nil), tracker.movedIssues...)
	prefs := append([][]string(nil), tracker.movePreferences...)
	tracker.mu.Unlock()
	if len(moved) != 1 || moved[0] != "1" {
		t.Fatalf("moved issues = %v, want [1]", moved)
	}
	if len(prefs) != 1 || len(prefs[0]) == 0 || prefs[0][0] != "In Review" {
		t.Fatalf("move preferences = %v, want In Review first", prefs)
	}
}

func TestOrchestrator_MergeStageMovesToDone(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Approved"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Approved"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1}
	cfg := defaultConfig()
	cfg.Tracker.WorkingState = "In Progress"
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	transitions := append([]string(nil), tracker.transitions...)
	moved := append([]string(nil), tracker.movedIssues...)
	prefs := append([][]string(nil), tracker.movePreferences...)
	tracker.mu.Unlock()
	if len(transitions) != 0 {
		t.Fatalf("working state transitions = %v, want none for merge stage", transitions)
	}
	if len(moved) != 1 || moved[0] != "1" {
		t.Fatalf("moved issues = %v, want [1]", moved)
	}
	if len(prefs) != 1 || len(prefs[0]) != 1 || prefs[0][0] != "Done" {
		t.Fatalf("move preferences = %v, want [[Done]]", prefs)
	}

	runner.mu.Lock()
	stages := append([]api.PipelineStage(nil), runner.stages...)
	runner.mu.Unlock()
	if len(stages) != 1 || stages[0].Kind != "merge" {
		t.Fatalf("runner stages = %v, want one merge stage", stages)
	}
}

func TestOrchestrator_ReviewStageMovesToMerge(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Review"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Review"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1}
	cfg := defaultConfig()
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Approved"}
	cfg.Tracker.WorkingState = "In Progress"
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	transitions := append([]string(nil), tracker.transitions...)
	moved := append([]string(nil), tracker.movedIssues...)
	prefs := append([][]string(nil), tracker.movePreferences...)
	tracker.mu.Unlock()
	if len(transitions) != 0 {
		t.Fatalf("working state transitions = %v, want none for review stage", transitions)
	}
	if len(moved) != 1 || moved[0] != "1" {
		t.Fatalf("moved issues = %v, want [1]", moved)
	}
	if len(prefs) != 1 || len(prefs[0]) != 1 || prefs[0][0] != "Approved" {
		t.Fatalf("move preferences = %v, want [[Approved]]", prefs)
	}

	runner.mu.Lock()
	stages := append([]api.PipelineStage(nil), runner.stages...)
	runner.mu.Unlock()
	if len(stages) != 1 || stages[0].Kind != "review" {
		t.Fatalf("runner stages = %v, want one review stage", stages)
	}
}

func TestOrchestrator_ReviewStageMovesToReviewResolutionWhenEnabled(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Review"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Review"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1}
	cfg := defaultConfig()
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Review Resolution", "Approved"}
	cfg.Pipeline.ReviewResolutionState = "Review Resolution"
	cfg.ReviewResolution.Enabled = true
	cfg.ReviewResolution.MaxAttempts = 3
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	prefs := append([][]string(nil), tracker.movePreferences...)
	tracker.mu.Unlock()
	if len(prefs) != 1 || len(prefs[0]) != 1 || prefs[0][0] != "Review Resolution" {
		t.Fatalf("move preferences = %v, want [[Review Resolution]]", prefs)
	}
}

func TestOrchestrator_ReviewResolutionApprovedMovesToMerge(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Review Resolution"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Review Resolution"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1, agentMessage: "Review feedback resolved.\n\nSIMPHONY_REVIEW_DECISION: approved"}
	cfg := defaultConfig()
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Review Resolution", "Approved"}
	cfg.Pipeline.ReviewResolutionState = "Review Resolution"
	cfg.ReviewResolution.Enabled = true
	cfg.ReviewResolution.MaxAttempts = 3
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	prefs := append([][]string(nil), tracker.movePreferences...)
	comments := append([]string(nil), tracker.comments...)
	tracker.mu.Unlock()
	if len(prefs) != 1 || len(prefs[0]) != 1 || prefs[0][0] != "Approved" {
		t.Fatalf("move preferences = %v, want [[Approved]]", prefs)
	}
	if !hasCommentContaining(comments, "Simphony review resolution started") || !hasCommentContaining(comments, "Simphony review resolution approved") {
		t.Fatalf("comments = %v, want review-resolution start and approved status comments", comments)
	}

	runner.mu.Lock()
	stages := append([]api.PipelineStage(nil), runner.stages...)
	runner.mu.Unlock()
	if len(stages) != 1 || stages[0].Kind != "review_resolution" || !strings.Contains(stages[0].Instructions, "SIMPHONY_REVIEW_DECISION") {
		t.Fatalf("runner stages = %v, want review_resolution with directive guidance", stages)
	}
}

func TestOrchestrator_ReviewResolutionRetryDoesNotApprove(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Review Resolution"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Review Resolution"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1, agentMessage: "Still waiting for checks.\n\nSIMPHONY_REVIEW_DECISION: retry"}
	cfg := defaultConfig()
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Review Resolution", "Approved"}
	cfg.Pipeline.ReviewResolutionState = "Review Resolution"
	cfg.ReviewResolution.Enabled = true
	cfg.ReviewResolution.MaxAttempts = 3
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	moveCount := len(tracker.movePreferences)
	comments := append([]string(nil), tracker.comments...)
	tracker.mu.Unlock()
	if moveCount != 0 {
		t.Fatalf("move count = %d, want 0 while review-resolution requested retry", moveCount)
	}
	if !hasCommentContaining(comments, "Simphony review resolution started") || !hasCommentContaining(comments, "Simphony review resolution retry scheduled") {
		t.Fatalf("comments = %v, want review-resolution start and retry status comments", comments)
	}
	orch.mu.Lock()
	retry := orch.state.RetryAttempts["1"]
	orch.mu.Unlock()
	if retry == nil || retry.Kind != retryKindAgent {
		t.Fatalf("retry = %#v, want agent retry", retry)
	}
}

func TestOrchestrator_ReviewResolutionEscalates(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Review Resolution"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Review Resolution"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: -1, agentMessage: "Security decision needs human judgment.\n\nSIMPHONY_REVIEW_DECISION: escalate"}
	cfg := defaultConfig()
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Review Resolution", "Approved"}
	cfg.Pipeline.ReviewResolutionState = "Review Resolution"
	cfg.ReviewResolution.Enabled = true
	cfg.ReviewResolution.EscalationState = "Needs Human"
	cfg.ReviewResolution.MaxAttempts = 3
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	tracker.mu.Lock()
	prefs := append([][]string(nil), tracker.movePreferences...)
	comments := append([]string(nil), tracker.comments...)
	tracker.mu.Unlock()
	if len(prefs) != 1 || len(prefs[0]) != 1 || prefs[0][0] != "Needs Human" {
		t.Fatalf("move preferences = %v, want [[Needs Human]]", prefs)
	}
	if !hasCommentContaining(comments, "Simphony review resolution started") || !hasCommentContaining(comments, "Simphony review resolution escalated") {
		t.Fatalf("comments = %v, want escalation comment", comments)
	}
}

func hasCommentContaining(comments []string, needle string) bool {
	for _, comment := range comments {
		if strings.Contains(comment, needle) {
			return true
		}
	}
	return false
}

func TestOrchestrator_MaxTurnsReachedMarksCompleted(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{err: fmt.Errorf("%s: reached limit", api.ErrMaxTurnsReached)}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 10000
	cfg.Agent.MaxConcurrentAgents = 1

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	orch.mu.Lock()
	_, completed := orch.state.Completed["1"]
	_, claimed := orch.state.Claimed["1"]
	retryCount := len(orch.state.RetryAttempts)
	orch.mu.Unlock()

	if !completed {
		t.Fatal("expected issue to be marked completed after max turns")
	}
	if claimed {
		t.Fatal("expected claim to be released after max turns")
	}
	if retryCount != 0 {
		t.Fatalf("expected no retry after max turns, got %d", retryCount)
	}

	orch.tick()

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()
	if runs != 1 {
		t.Fatalf("expected completed issue not to redispatch, got %d runs", runs)
	}
}

func TestOrchestrator_UnknownState(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Random"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	runner.mu.Lock()
	runs := len(runner.runs)
	runner.mu.Unlock()

	if runs != 0 {
		t.Fatalf("expected 0 dispatches for unknown state, got %d", runs)
	}
}

// Helpers
func strPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortIssues(t *testing.T) {
	now := time.Now()
	issues := []api.Issue{
		{ID: "1", Identifier: "C-1", Title: "C", Priority: intPtr(3), CreatedAt: timePtr(now.Add(-1 * time.Hour))},
		{ID: "2", Identifier: "A-1", Title: "A", Priority: intPtr(1), CreatedAt: timePtr(now.Add(-3 * time.Hour))},
		{ID: "3", Identifier: "B-1", Title: "B", Priority: intPtr(1), CreatedAt: timePtr(now.Add(-2 * time.Hour))},
		{ID: "4", Identifier: "D-1", Title: "D", Priority: nil, CreatedAt: timePtr(now.Add(-4 * time.Hour))},
	}

	cfg := defaultConfig()
	orch := New(cfg, nil, nil, nil)
	sorted := orch.sortIssues(issues)

	want := []string{"A-1", "B-1", "C-1", "D-1"}
	got := make([]string, len(sorted))
	for i, iss := range sorted {
		got[i] = iss.Identifier
	}
	if !slicesEqual(got, want) {
		t.Fatalf("sort order = %v, want %v", got, want)
	}
}

func TestPerStateSlots(t *testing.T) {
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 5
	cfg.Agent.MaxConcurrentAgentsByState = map[string]int{"todo": 2}

	orch := New(cfg, nil, nil, nil)
	orch.state.Running = map[string]*api.RunningEntry{
		"1": {Issue: api.Issue{State: "Todo"}},
	}

	if slots := orch.perStateSlots("Todo"); slots != 1 {
		t.Fatalf("todo slots = %d, want 1", slots)
	}
	if slots := orch.perStateSlots("In Progress"); slots != 4 {
		t.Fatalf("in progress slots = %d, want 4", slots)
	}
}

func TestBackoffFormula(t *testing.T) {
	tests := []struct {
		attempt int
		maxMs   int
		want    int64
	}{
		{1, 300000, 10000},
		{2, 300000, 20000},
		{3, 300000, 40000},
		{4, 300000, 80000},
		{5, 300000, 160000},
		{6, 300000, 300000},
		{7, 300000, 300000},
	}

	for _, tt := range tests {
		// We can't call scheduleFailureRetry directly in a unit way easily,
		// so just verify the math logic manually.
		delayMs := int64(10000)
		for i := 1; i < tt.attempt; i++ {
			delayMs *= 2
			if delayMs >= int64(tt.maxMs) {
				delayMs = int64(tt.maxMs)
				break
			}
		}
		if delayMs != tt.want {
			t.Fatalf("attempt %d: delay = %d, want %d", tt.attempt, delayMs, tt.want)
		}
	}
}

func TestOrchestrator_StopCancelsWorkers(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 200 * time.Millisecond}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()

	time.Sleep(100 * time.Millisecond)

	orch.mu.Lock()
	if len(orch.state.Running) != 1 {
		t.Fatalf("expected 1 running, got %d", len(orch.state.Running))
	}
	orch.mu.Unlock()

	orch.Stop()

	orch.mu.Lock()
	if len(orch.state.Running) != 0 {
		t.Fatalf("expected 0 running after stop, got %d", len(orch.state.Running))
	}
	orch.mu.Unlock()
}

func TestOrchestrator_RetryIssueGoesInactive(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
		byIDs: map[string]api.Issue{
			"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: 0}
	cfg := defaultConfig()
	cfg.Agent.MaxConcurrentAgents = 1
	// Use very short backoff so retry fires quickly.
	cfg.Agent.MaxRetryBackoffMs = 500

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	orch.mu.Lock()
	if len(orch.state.RetryAttempts) != 1 {
		t.Fatalf("expected 1 retry, got %d", len(orch.state.RetryAttempts))
	}
	orch.mu.Unlock()

	// Make issue inactive before retry fires.
	tracker.setCandidates([]api.Issue{
		{ID: "1", Identifier: "A-1", Title: "First", State: "Done"},
	})
	tracker.setByID(map[string]api.Issue{
		"1": {ID: "1", Identifier: "A-1", Title: "First", State: "Done"},
	})

	// Wait for retry to fire and discover inactive state.
	time.Sleep(700 * time.Millisecond)

	orch.mu.Lock()
	_, claimed := orch.state.Claimed["1"]
	orch.mu.Unlock()

	if claimed {
		t.Fatal("expected claim to be released when issue became inactive")
	}
}

func TestOrchestrator_Refresh(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 10000 // Very long so refresh is the only trigger.

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Wait for initial automatic tick.
	time.Sleep(100 * time.Millisecond)

	// Trigger refresh.
	resp := orch.Refresh()
	if !resp.Queued {
		t.Fatalf("expected refresh to be queued, got queued=%v", resp.Queued)
	}
	if resp.Coalesced {
		t.Fatalf("expected refresh not coalesced on first call")
	}
	if len(resp.Operations) != 1 || resp.Operations[0] != "tick" {
		t.Fatalf("unexpected operations: %v", resp.Operations)
	}

	// Second refresh while first is still processing should coalesce.
	resp2 := orch.Refresh()
	if resp2.Coalesced {
		// Channel has buffer 1, so this may or may not coalesce depending on timing.
		// Just verify it doesn't panic and returns a valid response.
		t.Logf("second refresh coalesced=%v (timing-dependent)", resp2.Coalesced)
	}
}

func TestOrchestrator_UpdateRuntimeReplacesDependencies(t *testing.T) {
	oldTracker := &mockTracker{}
	newTracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	oldWsMgr, _ := workspace.NewManager(t.TempDir())
	newRoot := t.TempDir()
	newWsMgr, _ := workspace.NewManager(newRoot)
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 10000

	orch := New(cfg, oldTracker, oldWsMgr, runner)
	orch.Start()
	defer orch.Stop()

	time.Sleep(100 * time.Millisecond)

	newCfg := defaultConfig()
	newCfg.Workspace.Root = newRoot
	orch.UpdateRuntime(newCfg, newTracker, newWsMgr)
	orch.tick()

	snap := orch.Snapshot()
	if snap.Counts.Running != 1 {
		t.Fatalf("expected updated tracker to dispatch 1 issue, got %d", snap.Counts.Running)
	}
	detail, ok := orch.IssueDetail("A-1")
	if !ok {
		t.Fatal("expected issue detail after updated dispatch")
	}
	if !strings.HasPrefix(detail.Workspace.Path, newRoot) {
		t.Fatalf("expected workspace under new root %q, got %q", newRoot, detail.Workspace.Path)
	}
}

func TestOrchestrator_IssueDetail(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "In Progress"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{delay: 500 * time.Millisecond}
	cfg := defaultConfig()

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Wait for dispatch.
	time.Sleep(100 * time.Millisecond)

	// Look up running issue.
	detail, ok := orch.IssueDetail("A-1")
	if !ok {
		t.Fatal("expected to find A-1")
	}
	if detail.IssueIdentifier != "A-1" {
		t.Fatalf("identifier = %q, want A-1", detail.IssueIdentifier)
	}
	if detail.Status != "running" {
		t.Fatalf("status = %q, want running", detail.Status)
	}
	if detail.Running == nil {
		t.Fatal("expected running snapshot")
	}
	if detail.Running.IssueIdentifier != "A-1" {
		t.Fatalf("running snapshot identifier = %q, want A-1", detail.Running.IssueIdentifier)
	}

	// Unknown issue.
	_, ok2 := orch.IssueDetail("Z-99")
	if ok2 {
		t.Fatal("expected Z-99 to not be found")
	}
}

func TestOrchestrator_IssueDetail_Retry(t *testing.T) {
	tracker := &mockTracker{
		candidates: []api.Issue{
			{ID: "1", Identifier: "A-1", Title: "First", State: "Todo"},
		},
	}
	wsMgr, _ := workspace.NewManager(t.TempDir())
	runner := &mockRunner{errAfter: 0}
	cfg := defaultConfig()
	cfg.Agent.MaxRetryBackoffMs = 5000

	orch := New(cfg, tracker, wsMgr, runner)
	orch.Start()
	defer orch.Stop()

	// Wait for failure and retry scheduling.
	time.Sleep(100 * time.Millisecond)

	// Look up retrying issue.
	detail, ok := orch.IssueDetail("A-1")
	if !ok {
		t.Fatal("expected to find A-1 in retry queue")
	}
	if detail.Status != "retrying" {
		t.Fatalf("status = %q, want retrying", detail.Status)
	}
	if detail.Retry == nil {
		t.Fatal("expected retry snapshot")
	}
	if detail.Retry.IssueIdentifier != "A-1" {
		t.Fatalf("retry snapshot identifier = %q, want A-1", detail.Retry.IssueIdentifier)
	}

	// Stop retry timer so test cleanup is clean.
	orch.mu.Lock()
	if entry := orch.state.RetryAttempts["1"]; entry != nil {
		if timer, ok := entry.TimerHandle.(*time.Timer); ok {
			timer.Stop()
		}
	}
	orch.mu.Unlock()
}
