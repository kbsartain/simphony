package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"simphony/internal/workspace"
	"simphony/pkg/api"
)

// AgentRunner is the interface for launching coding-agent sessions.
type AgentRunner interface {
	Run(ctx context.Context, issue api.Issue, workspace *api.Workspace, attempt *int, cfg *api.CodexConfig, maxTurns int, shouldContinue func() (api.ContinueDecision, error), eventCallback func(api.AgentEvent)) error
}

type workerResult struct {
	issueID string
	err     error
}

type runtimeSnapshot struct {
	cfg          *api.WorkflowConfig
	tracker      api.Tracker
	workspaceMgr *workspace.Manager
	runner       AgentRunner
}

// Orchestrator owns the poll loop, dispatch, retries, and reconciliation.
type Orchestrator struct {
	state        api.OrchestratorState
	cfg          *api.WorkflowConfig
	tracker      api.Tracker
	workspaceMgr *workspace.Manager
	runner       AgentRunner

	mu            sync.Mutex
	ticker        *time.Ticker
	stopCh        chan struct{}
	wg            sync.WaitGroup
	workerWg      sync.WaitGroup
	workerCancels map[string]context.CancelFunc
	completedCh   chan workerResult
	retryCh       chan string
	refreshCh     chan struct{}
}

// New creates a new Orchestrator. Call Start() to begin polling.
func New(cfg *api.WorkflowConfig, tracker api.Tracker, workspaceMgr *workspace.Manager, runner AgentRunner) *Orchestrator {
	return &Orchestrator{
		cfg:          cfg,
		tracker:      tracker,
		workspaceMgr: workspaceMgr,
		runner:       runner,
		stopCh:       make(chan struct{}),
	}
}

// Start initializes state, performs startup cleanup, and begins the poll loop.
func (o *Orchestrator) Start() {
	o.mu.Lock()
	o.state.Running = make(map[string]*api.RunningEntry)
	o.state.Claimed = make(map[string]struct{})
	o.state.RetryAttempts = make(map[string]*api.RetryEntry)
	o.state.Completed = make(map[string]struct{})
	o.state.CodexTotals = api.CodexTotals{}
	o.state.CodexRateLimits = make(map[string]interface{})
	o.state.PollIntervalMs = o.cfg.Polling.IntervalMs
	o.state.MaxConcurrentAgents = o.cfg.Agent.MaxConcurrentAgents
	o.workerCancels = make(map[string]context.CancelFunc)
	o.completedCh = make(chan workerResult, 32)
	o.retryCh = make(chan string, 32)
	o.refreshCh = make(chan struct{}, 1)
	o.mu.Unlock()

	o.cleanupTerminalWorkspaces()

	o.ticker = time.NewTicker(time.Duration(o.cfg.Polling.IntervalMs) * time.Millisecond)
	o.wg.Add(1)
	go o.loop()
}

// Stop signals shutdown and waits for workers to finish.
func (o *Orchestrator) Stop() {
	if o.ticker != nil {
		o.ticker.Stop()
	}
	close(o.stopCh)

	o.mu.Lock()
	for _, cancel := range o.workerCancels {
		cancel()
	}
	o.mu.Unlock()

	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		log.Println("warning: orchestrator stop timed out waiting for loop")
	}

	// Wait for all worker goroutines to finish.
	workerDone := make(chan struct{})
	go func() {
		o.workerWg.Wait()
		close(workerDone)
	}()

	select {
	case <-workerDone:
	case <-time.After(30 * time.Second):
		log.Println("warning: orchestrator stop timed out waiting for workers")
	}

	// Drain any pending worker results.
	for {
		select {
		case result := <-o.completedCh:
			o.handleWorkerResult(result)
		default:
			return
		}
	}
}

func (o *Orchestrator) loop() {
	defer o.wg.Done()

	// Immediate first tick.
	o.tick()

	for {
		select {
		case <-o.stopCh:
			return
		case <-o.ticker.C:
			o.tick()
		case <-o.refreshCh:
			o.tick()
		case result := <-o.completedCh:
			o.handleWorkerResult(result)
		case issueID := <-o.retryCh:
			o.handleRetry(issueID)
		}
	}
}

func (o *Orchestrator) tick() {
	o.reconcile()
	o.dispatchEligibleIssues()
}

func (o *Orchestrator) runtimeSnapshot() runtimeSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return runtimeSnapshot{
		cfg:          o.cfg,
		tracker:      o.tracker,
		workspaceMgr: o.workspaceMgr,
		runner:       o.runner,
	}
}

// reconcile performs stall detection and tracker state refresh.
func (o *Orchestrator) reconcile() {
	runtime := o.runtimeSnapshot()
	o.mu.Lock()

	// Part A: Stall detection.
	type stallInfo struct{ id, identifier string }
	var stalls []stallInfo
	if runtime.cfg.Codex.StallTimeoutMs > 0 {
		for id, running := range o.state.Running {
			var elapsedMs int64
			if running.Session.LastCodexTimestamp != nil {
				elapsedMs = time.Since(*running.Session.LastCodexTimestamp).Milliseconds()
			} else {
				elapsedMs = time.Since(running.StartedAt).Milliseconds()
			}
			if elapsedMs > int64(runtime.cfg.Codex.StallTimeoutMs) {
				log.Printf("issue_id=%s issue_identifier=%s action=stall_detected elapsed_ms=%d stall_timeout_ms=%d", id, running.Issue.Identifier, elapsedMs, runtime.cfg.Codex.StallTimeoutMs)
				o.terminateWorkerLocked(id)
				o.removeRunningLocked(id)
				stalls = append(stalls, stallInfo{id: id, identifier: running.Issue.Identifier})
			}
		}
	}

	runningIDs := make([]string, 0, len(o.state.Running))
	for id := range o.state.Running {
		runningIDs = append(runningIDs, id)
	}
	o.mu.Unlock()

	// Schedule retries outside the lock to avoid deadlock with Stop().
	for _, s := range stalls {
		o.scheduleFailureRetry(s.id, s.identifier, "stalled")
	}

	if len(runningIDs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	states, err := runtime.tracker.FetchIssueStatesByIDs(ctx, runningIDs)
	if err != nil {
		log.Printf("action=reconciliation error=%v", err)
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	for id, running := range o.state.Running {
		issue, found := states[id]
		if !found {
			log.Printf("issue_id=%s action=terminate reason=missing_from_tracker", id)
			o.terminateWorkerLocked(id)
			o.removeRunningLocked(id)
			delete(o.state.Claimed, id)
			continue
		}

		stateNorm := strings.ToLower(issue.State)
		if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
			log.Printf("issue_id=%s issue_identifier=%s action=terminate reason=terminal_state state=%s", id, running.Issue.Identifier, issue.State)
			o.terminateWorkerLocked(id)
			o.removeRunningLocked(id)
			delete(o.state.Claimed, id)
			if runtime.cfg.Hooks.BeforeRemove != nil {
				_ = runtime.workspaceMgr.RunHook("before_remove", *runtime.cfg.Hooks.BeforeRemove, running.WorkspacePath, runtime.cfg.Hooks.TimeoutMs)
			}
			_ = runtime.workspaceMgr.RemoveWorkspace(running.Issue.Identifier)
		} else if o.isActiveWithConfig(runtime.cfg, stateNorm) {
			// Update in-memory snapshot.
			running.Issue = issue
		} else {
			log.Printf("issue_id=%s issue_identifier=%s action=terminate reason=non_active_state state=%s", id, running.Issue.Identifier, issue.State)
			o.terminateWorkerLocked(id)
			o.removeRunningLocked(id)
			delete(o.state.Claimed, id)
		}
	}
}

func (o *Orchestrator) isTerminal(state string) bool {
	return o.isTerminalWithConfig(o.runtimeSnapshot().cfg, state)
}

func (o *Orchestrator) isTerminalWithConfig(cfg *api.WorkflowConfig, state string) bool {
	for _, s := range cfg.Tracker.TerminalStates {
		if strings.ToLower(s) == state {
			return true
		}
	}
	return false
}

func (o *Orchestrator) isActive(state string) bool {
	return o.isActiveWithConfig(o.runtimeSnapshot().cfg, state)
}

func (o *Orchestrator) isActiveWithConfig(cfg *api.WorkflowConfig, state string) bool {
	for _, s := range cfg.Tracker.ActiveStates {
		if strings.ToLower(s) == state {
			return true
		}
	}
	return false
}

func (o *Orchestrator) terminateWorkerLocked(issueID string) {
	if cancel, ok := o.workerCancels[issueID]; ok {
		cancel()
		delete(o.workerCancels, issueID)
	}
}

// removeRunningLocked removes a running entry and updates cumulative totals.
// Callers must hold o.mu.
func (o *Orchestrator) removeRunningLocked(id string) {
	entry, ok := o.state.Running[id]
	if !ok {
		return
	}
	delete(o.state.Running, id)
	elapsedSec := time.Since(entry.StartedAt).Seconds()
	o.state.CodexTotals.SecondsRunning += elapsedSec
	o.state.CodexTotals.InputTokens += entry.Session.CodexInputTokens
	o.state.CodexTotals.OutputTokens += entry.Session.CodexOutputTokens
	o.state.CodexTotals.TotalTokens += entry.Session.CodexTotalTokens
}

func (o *Orchestrator) dispatchEligibleIssues() {
	runtime := o.runtimeSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := runtime.tracker.FetchCandidateIssues(ctx)
	if err != nil {
		log.Printf("action=candidate_fetch error=%v", err)
		return
	}

	candidateCount := len(candidates)
	candidates = o.filterEligible(candidates)
	eligibleCount := len(candidates)
	log.Printf("action=candidate_fetch result_count=%d eligible_count=%d", candidateCount, eligibleCount)
	candidates = o.sortIssues(candidates)

	for _, issue := range candidates {
		if !o.canDispatch() {
			log.Printf("action=dispatch_deferred reason=no_global_slots")
			break
		}
		o.dispatch(issue)
	}
}

func (o *Orchestrator) filterEligible(candidates []api.Issue) []api.Issue {
	o.mu.Lock()
	defer o.mu.Unlock()
	cfg := o.cfg

	var eligible []api.Issue
	for _, issue := range candidates {
		if issue.ID == "" || issue.Identifier == "" || issue.Title == "" || issue.State == "" {
			continue
		}
		stateNorm := strings.ToLower(issue.State)
		if !o.isActiveWithConfig(cfg, stateNorm) || o.isTerminalWithConfig(cfg, stateNorm) {
			continue
		}
		if _, running := o.state.Running[issue.ID]; running {
			continue
		}
		if _, claimed := o.state.Claimed[issue.ID]; claimed {
			continue
		}
		if _, completed := o.state.Completed[issue.ID]; completed {
			continue
		}

		// Blocker rule for Todo.
		if strings.ToLower(issue.State) == "todo" {
			blocked := false
			for _, b := range issue.BlockedBy {
				if b.State != nil && !o.isTerminalWithConfig(cfg, strings.ToLower(*b.State)) {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
		}

		eligible = append(eligible, issue)
	}
	return eligible
}

func (o *Orchestrator) canDispatch() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	globalAvailable := o.cfg.Agent.MaxConcurrentAgents - len(o.state.Running)
	return globalAvailable > 0
}

func (o *Orchestrator) perStateSlots(state string) int {
	o.mu.Lock()
	defer o.mu.Unlock()

	stateNorm := strings.ToLower(state)
	if limit, ok := o.cfg.Agent.MaxConcurrentAgentsByState[stateNorm]; ok && limit > 0 {
		count := 0
		for _, r := range o.state.Running {
			if strings.ToLower(r.Issue.State) == stateNorm {
				count++
			}
		}
		return limit - count
	}
	return o.cfg.Agent.MaxConcurrentAgents - len(o.state.Running)
}

func (o *Orchestrator) sortIssues(issues []api.Issue) []api.Issue {
	sort.SliceStable(issues, func(i, j int) bool {
		// Priority ascending (null sorts last).
		pi, pj := issues[i].Priority, issues[j].Priority
		if pi != nil && pj == nil {
			return true
		}
		if pi == nil && pj != nil {
			return false
		}
		if pi != nil && pj != nil && *pi != *pj {
			return *pi < *pj
		}

		// Created_at oldest first.
		ci, cj := issues[i].CreatedAt, issues[j].CreatedAt
		if ci != nil && cj != nil && !ci.Equal(*cj) {
			return ci.Before(*cj)
		}

		// Identifier lexicographic tie-breaker.
		return issues[i].Identifier < issues[j].Identifier
	})
	return issues
}

func (o *Orchestrator) dispatch(issue api.Issue) {
	runtime := o.runtimeSnapshot()
	if o.perStateSlots(issue.State) <= 0 {
		log.Printf("issue_id=%s issue_identifier=%s action=dispatch_deferred reason=no_state_slots state=%q", issue.ID, issue.Identifier, issue.State)
		return
	}

	o.mu.Lock()
	o.state.Claimed[issue.ID] = struct{}{}
	o.mu.Unlock()

	workspace, err := runtime.workspaceMgr.PrepareWorkspace(issue)
	if err != nil {
		log.Printf("issue_id=%s issue_identifier=%s action=workspace_prepare failed=%v", issue.ID, issue.Identifier, err)
		o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("workspace prepare: %v", err))
		return
	}

	// after_create hook (fatal to workspace creation).
	if workspace.CreatedNow && runtime.cfg.Hooks.AfterCreate != nil {
		if err := runtime.workspaceMgr.RunHook("after_create", *runtime.cfg.Hooks.AfterCreate, workspace.Path, runtime.cfg.Hooks.TimeoutMs); err != nil {
			log.Printf("issue_id=%s issue_identifier=%s action=after_create failed=%v", issue.ID, issue.Identifier, err)
			_ = runtime.workspaceMgr.RemoveWorkspace(issue.Identifier)
			o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("after_create hook: %v", err))
			return
		}
	}

	// before_run hook (fatal to attempt).
	if runtime.cfg.Hooks.BeforeRun != nil {
		if err := runtime.workspaceMgr.RunHook("before_run", *runtime.cfg.Hooks.BeforeRun, workspace.Path, runtime.cfg.Hooks.TimeoutMs); err != nil {
			log.Printf("issue_id=%s issue_identifier=%s action=before_run failed=%v", issue.ID, issue.Identifier, err)
			o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("before_run hook: %v", err))
			return
		}
	}

	o.mu.Lock()
	o.state.Running[issue.ID] = &api.RunningEntry{
		Issue:         issue,
		StartedAt:     time.Now(),
		WorkspacePath: workspace.Path,
	}
	o.mu.Unlock()
	log.Printf("issue_id=%s issue_identifier=%s action=dispatch_started state=%q workspace=%q", issue.ID, issue.Identifier, issue.State, workspace.Path)

	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.workerCancels[issue.ID] = cancel
	o.mu.Unlock()

	o.workerWg.Add(1)
	go func() {
		defer o.workerWg.Done()
		o.runWorker(ctx, issue, workspace, runtime)
	}()
}

func (o *Orchestrator) runWorker(ctx context.Context, issue api.Issue, workspace *api.Workspace, runtime runtimeSnapshot) {
	eventCallback := func(event api.AgentEvent) {
		o.mu.Lock()
		if running, ok := o.state.Running[issue.ID]; ok {
			running.Session.LastCodexEvent = &event.Event
			running.Session.LastCodexTimestamp = &event.Timestamp
			running.Session.LastCodexMessage = summarizeEvent(event)
			if event.CodexPID != nil {
				running.Session.CodexAppServerPID = event.CodexPID
			}
			if event.Payload != nil {
				if sessionID, ok := event.Payload["session_id"].(string); ok {
					running.Session.SessionID = sessionID
				}
				if threadID, ok := event.Payload["thread_id"].(string); ok {
					running.Session.ThreadID = threadID
				}
				if turnID, ok := event.Payload["turn_id"].(string); ok {
					running.Session.TurnID = turnID
				}
				if turnCount, ok := toInt64(event.Payload["turn_count"]); ok {
					running.Session.TurnCount = int(turnCount)
					running.TurnCount = int(turnCount)
				}
				if state, ok := event.Payload["issue_state"].(string); ok {
					running.Issue.State = state
				}
			}
			// Token accounting from usage payload.
			if event.Usage != nil {
				updateTokens(&running.Session, event.Usage)
			}
		}
		o.mu.Unlock()
	}

	shouldContinue := func() (api.ContinueDecision, error) {
		return o.continueDecision(ctx, issue.ID)
	}

	err := runtime.runner.Run(ctx, issue, workspace, nil, &runtime.cfg.Codex, runtime.cfg.Agent.MaxTurns, shouldContinue, eventCallback)

	// after_run hook (logged but ignored).
	if runtime.cfg.Hooks.AfterRun != nil {
		_ = runtime.workspaceMgr.RunHook("after_run", *runtime.cfg.Hooks.AfterRun, workspace.Path, runtime.cfg.Hooks.TimeoutMs)
	}

	o.completedCh <- workerResult{issueID: issue.ID, err: err}
}

func updateTokens(session *api.AgentSession, usage map[string]interface{}) {
	// Prefer absolute totals when available.
	if total, ok := usage["total_tokens"]; ok {
		if n, ok := toInt64(total); ok {
			delta := n - int64(session.LastReportedTotalTokens)
			if delta > 0 {
				session.CodexTotalTokens += int(delta)
				session.LastReportedTotalTokens = int(n)
			}
		}
	}
	if in, ok := usage["input_tokens"]; ok {
		if n, ok := toInt64(in); ok {
			delta := n - int64(session.LastReportedInputTokens)
			if delta > 0 {
				session.CodexInputTokens += int(delta)
				session.LastReportedInputTokens = int(n)
			}
		}
	}
	if out, ok := usage["output_tokens"]; ok {
		if n, ok := toInt64(out); ok {
			delta := n - int64(session.LastReportedOutputTokens)
			if delta > 0 {
				session.CodexOutputTokens += int(delta)
				session.LastReportedOutputTokens = int(n)
			}
		}
	}
}

func summarizeEvent(event api.AgentEvent) string {
	if event.Payload == nil {
		return ""
	}
	for _, key := range []string{"message", "text", "status", "reason"} {
		if v, ok := event.Payload[key].(string); ok {
			return v
		}
	}
	return ""
}

func (o *Orchestrator) continueDecision(ctx context.Context, issueID string) (api.ContinueDecision, error) {
	runtime := o.runtimeSnapshot()
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	states, err := runtime.tracker.FetchIssueStatesByIDs(checkCtx, []string{issueID})
	if err != nil {
		return api.ContinueDecision{}, err
	}

	issue, found := states[issueID]
	if !found {
		return api.ContinueDecision{Continue: false, Reason: "missing_from_tracker"}, nil
	}

	stateNorm := strings.ToLower(issue.State)
	decision := api.ContinueDecision{
		Issue:    issue,
		Continue: o.isActiveWithConfig(runtime.cfg, stateNorm) && !o.isTerminalWithConfig(runtime.cfg, stateNorm),
	}
	if !decision.Continue {
		if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
			decision.Reason = "terminal_state"
		} else {
			decision.Reason = "non_active_state"
		}
	}

	o.mu.Lock()
	if running, ok := o.state.Running[issueID]; ok {
		running.Issue = issue
	}
	o.mu.Unlock()

	return decision, nil
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	default:
		return 0, false
	}
}

func (o *Orchestrator) handleWorkerResult(result workerResult) {
	runtime := o.runtimeSnapshot()
	o.mu.Lock()
	entry, ok := o.state.Running[result.issueID]
	if !ok {
		o.mu.Unlock()
		return
	}
	delete(o.state.Running, result.issueID)
	delete(o.workerCancels, result.issueID)

	// Update cumulative runtime.
	elapsedSec := time.Since(entry.StartedAt).Seconds()
	o.state.CodexTotals.SecondsRunning += elapsedSec
	o.state.CodexTotals.InputTokens += entry.Session.CodexInputTokens
	o.state.CodexTotals.OutputTokens += entry.Session.CodexOutputTokens
	o.state.CodexTotals.TotalTokens += entry.Session.CodexTotalTokens

	identifier := entry.Issue.Identifier
	o.mu.Unlock()

	if result.err != nil {
		if strings.Contains(result.err.Error(), api.ErrMaxTurnsReached) {
			log.Printf("issue_id=%s issue_identifier=%s action=worker_exit status=max_turns_reached", result.issueID, identifier)
			o.mu.Lock()
			o.state.Completed[result.issueID] = struct{}{}
			delete(o.state.Claimed, result.issueID)
			o.mu.Unlock()
			return
		}
		log.Printf("issue_id=%s issue_identifier=%s action=worker_exit status=failed error=%v", result.issueID, identifier, result.err)
		o.scheduleFailureRetry(result.issueID, identifier, result.err.Error())
	} else {
		log.Printf("issue_id=%s issue_identifier=%s action=worker_exit status=success", result.issueID, identifier)
		stateNorm := strings.ToLower(entry.Issue.State)
		if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
			deleteTerminal := false
			o.mu.Lock()
			delete(o.state.Claimed, result.issueID)
			deleteTerminal = true
			o.mu.Unlock()
			if deleteTerminal {
				if runtime.cfg.Hooks.BeforeRemove != nil {
					_ = runtime.workspaceMgr.RunHook("before_remove", *runtime.cfg.Hooks.BeforeRemove, entry.WorkspacePath, runtime.cfg.Hooks.TimeoutMs)
				}
				_ = runtime.workspaceMgr.RemoveWorkspace(identifier)
			}
			return
		}
		if !o.isActiveWithConfig(runtime.cfg, stateNorm) {
			o.releaseClaim(result.issueID)
			return
		}
		// Schedule continuation retry (1s fixed delay).
		o.scheduleContinuationRetry(result.issueID, identifier)
	}
}

func (o *Orchestrator) scheduleContinuationRetry(issueID string, identifier string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.Claimed[issueID] = struct{}{}

	// Cancel existing retry timer.
	if existing, ok := o.state.RetryAttempts[issueID]; ok && existing.TimerHandle != nil {
		if timer, ok := existing.TimerHandle.(*time.Timer); ok {
			timer.Stop()
		}
	}

	timer := time.AfterFunc(1000*time.Millisecond, func() {
		select {
		case o.retryCh <- issueID:
		case <-o.stopCh:
		}
	})

	o.state.RetryAttempts[issueID] = &api.RetryEntry{
		IssueID:     issueID,
		Identifier:  identifier,
		Attempt:     1,
		DueAtMs:     time.Now().UnixMilli() + 1000,
		TimerHandle: timer,
	}
}

func (o *Orchestrator) scheduleFailureRetry(issueID string, identifier string, errorMsg string) {
	runtime := o.runtimeSnapshot()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.Claimed[issueID] = struct{}{}

	// Cancel existing retry timer.
	if existing, ok := o.state.RetryAttempts[issueID]; ok && existing.TimerHandle != nil {
		if timer, ok := existing.TimerHandle.(*time.Timer); ok {
			timer.Stop()
		}
	}

	attempt := 1
	if existing, ok := o.state.RetryAttempts[issueID]; ok {
		attempt = existing.Attempt + 1
	}

	delayMs := int64(10000)
	for i := 1; i < attempt; i++ {
		delayMs *= 2
	}
	if delayMs > int64(runtime.cfg.Agent.MaxRetryBackoffMs) {
		delayMs = int64(runtime.cfg.Agent.MaxRetryBackoffMs)
	}

	timer := time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
		select {
		case o.retryCh <- issueID:
		case <-o.stopCh:
		}
	})

	msg := errorMsg
	o.state.RetryAttempts[issueID] = &api.RetryEntry{
		IssueID:     issueID,
		Identifier:  identifier,
		Attempt:     attempt,
		DueAtMs:     time.Now().UnixMilli() + delayMs,
		TimerHandle: timer,
		Error:       &msg,
	}
}

func (o *Orchestrator) handleRetry(issueID string) {
	runtime := o.runtimeSnapshot()
	o.mu.Lock()
	identifier := ""
	if entry := o.state.RetryAttempts[issueID]; entry != nil {
		identifier = entry.Identifier
	}
	o.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := runtime.tracker.FetchCandidateIssues(ctx)
	if err != nil {
		log.Printf("issue_id=%s action=retry_fetch_failed error=%v", issueID, err)
		o.scheduleFailureRetry(issueID, identifier, fmt.Sprintf("retry fetch: %v", err))
		return
	}

	var issue *api.Issue
	for i := range candidates {
		if candidates[i].ID == issueID {
			issue = &candidates[i]
			break
		}
	}

	if issue == nil {
		log.Printf("issue_id=%s action=retry_issue_missing releasing_claim", issueID)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		return
	}

	stateNorm := strings.ToLower(issue.State)
	if !o.isActiveWithConfig(runtime.cfg, stateNorm) || o.isTerminalWithConfig(runtime.cfg, stateNorm) {
		log.Printf("issue_id=%s action=retry_issue_inactive state=%s releasing_claim", issueID, issue.State)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		return
	}

	if !o.canDispatch() || o.perStateSlots(issue.State) <= 0 {
		log.Printf("issue_id=%s action=retry_no_slots requeuing", issueID)
		o.scheduleFailureRetry(issueID, issue.Identifier, "no available orchestrator slots")
		return
	}

	o.mu.Lock()
	delete(o.state.RetryAttempts, issueID)
	o.mu.Unlock()
	o.dispatch(*issue)
}

func (o *Orchestrator) releaseClaim(issueID string) {
	o.mu.Lock()
	delete(o.state.Claimed, issueID)
	o.mu.Unlock()
}

func (o *Orchestrator) cleanupTerminalWorkspaces() {
	runtime := o.runtimeSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := runtime.tracker.FetchIssuesByStates(ctx, runtime.cfg.Tracker.TerminalStates)
	if err != nil {
		log.Printf("action=startup_cleanup warning=%v", err)
		return
	}

	for _, issue := range issues {
		wsPath := runtime.workspaceMgr.GetWorkspacePath(issue.Identifier)
		if runtime.cfg.Hooks.BeforeRemove != nil {
			if info, err := os.Stat(wsPath); err == nil && info.IsDir() {
				_ = runtime.workspaceMgr.RunHook("before_remove", *runtime.cfg.Hooks.BeforeRemove, wsPath, runtime.cfg.Hooks.TimeoutMs)
			}
		}
		_ = runtime.workspaceMgr.RemoveWorkspace(issue.Identifier)
	}
}

// Snapshot returns a read-only view of current orchestrator state for the HTTP API.
func (o *Orchestrator) Snapshot() api.StateSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	seconds := o.state.CodexTotals.SecondsRunning
	inputTokens := o.state.CodexTotals.InputTokens
	outputTokens := o.state.CodexTotals.OutputTokens
	totalTokens := o.state.CodexTotals.TotalTokens

	running := make([]api.RunningSnapshot, 0, len(o.state.Running))
	for _, entry := range o.state.Running {
		elapsed := now.Sub(entry.StartedAt).Seconds()
		seconds += elapsed
		inputTokens += entry.Session.CodexInputTokens
		outputTokens += entry.Session.CodexOutputTokens
		totalTokens += entry.Session.CodexTotalTokens

		var lastEvent, lastMessage string
		var lastEventAt time.Time
		if entry.Session.LastCodexEvent != nil {
			lastEvent = *entry.Session.LastCodexEvent
			lastMessage = entry.Session.LastCodexMessage
		}
		if entry.Session.LastCodexTimestamp != nil {
			lastEventAt = *entry.Session.LastCodexTimestamp
		}

		running = append(running, api.RunningSnapshot{
			IssueID:         entry.Issue.ID,
			IssueIdentifier: entry.Issue.Identifier,
			State:           entry.Issue.State,
			SessionID:       entry.Session.SessionID,
			TurnCount:       entry.Session.TurnCount,
			LastEvent:       lastEvent,
			LastMessage:     lastMessage,
			StartedAt:       entry.StartedAt,
			LastEventAt:     lastEventAt,
			Tokens: api.TokenSnapshot{
				InputTokens:  entry.Session.CodexInputTokens,
				OutputTokens: entry.Session.CodexOutputTokens,
				TotalTokens:  entry.Session.CodexTotalTokens,
			},
		})
	}

	retrying := make([]api.RetrySnapshot, 0, len(o.state.RetryAttempts))
	for _, entry := range o.state.RetryAttempts {
		retrying = append(retrying, api.RetrySnapshot{
			IssueID:         entry.IssueID,
			IssueIdentifier: entry.Identifier,
			Attempt:         entry.Attempt,
			DueAt:           time.UnixMilli(entry.DueAtMs),
			Error:           entry.Error,
		})
	}

	return api.StateSnapshot{
		GeneratedAt: now,
		Counts: api.StateCounts{
			Running:  len(o.state.Running),
			Retrying: len(o.state.RetryAttempts),
		},
		Running:  running,
		Retrying: retrying,
		CodexTotals: api.CodexTotals{
			InputTokens:    inputTokens,
			OutputTokens:   outputTokens,
			TotalTokens:    totalTokens,
			SecondsRunning: seconds,
		},
		RateLimits: o.state.CodexRateLimits,
	}
}

// Refresh triggers an immediate orchestrator tick and returns the result.
func (o *Orchestrator) Refresh() api.RefreshResponse {
	now := time.Now()
	select {
	case o.refreshCh <- struct{}{}:
		return api.RefreshResponse{
			Queued:      true,
			Coalesced:   false,
			RequestedAt: now,
			Operations:  []string{"tick"},
		}
	default:
		return api.RefreshResponse{
			Queued:      false,
			Coalesced:   true,
			RequestedAt: now,
			Operations:  []string{},
		}
	}
}

// UpdateConfig applies a new workflow config to the running orchestrator.
// It updates polling interval, concurrency limits, and agent/codex/hooks settings
// for future operations without restarting in-flight workers.
func (o *Orchestrator) UpdateConfig(cfg *api.WorkflowConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.cfg = cfg
	o.state.PollIntervalMs = cfg.Polling.IntervalMs
	o.state.MaxConcurrentAgents = cfg.Agent.MaxConcurrentAgents

	if o.ticker != nil && cfg.Polling.IntervalMs > 0 {
		o.ticker.Reset(time.Duration(cfg.Polling.IntervalMs) * time.Millisecond)
	}
}

// UpdateRuntime applies a reloaded config and replaces dependencies derived
// from that config for future scheduler operations.
func (o *Orchestrator) UpdateRuntime(cfg *api.WorkflowConfig, tracker api.Tracker, workspaceMgr *workspace.Manager) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.cfg = cfg
	o.tracker = tracker
	o.workspaceMgr = workspaceMgr
	o.state.PollIntervalMs = cfg.Polling.IntervalMs
	o.state.MaxConcurrentAgents = cfg.Agent.MaxConcurrentAgents

	if o.ticker != nil && cfg.Polling.IntervalMs > 0 {
		o.ticker.Reset(time.Duration(cfg.Polling.IntervalMs) * time.Millisecond)
	}
}

// IssueDetail looks up runtime details for a single issue by its identifier.
func (o *Orchestrator) IssueDetail(identifier string) (api.IssueDetailResponse, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, entry := range o.state.Running {
		if entry.Issue.Identifier == identifier {
			var lastEvent, lastMessage string
			if entry.Session.LastCodexEvent != nil {
				lastEvent = *entry.Session.LastCodexEvent
				lastMessage = entry.Session.LastCodexMessage
			}
			return api.IssueDetailResponse{
				IssueIdentifier: identifier,
				IssueID:         entry.Issue.ID,
				Status:          "running",
				Workspace:       api.WorkspaceDetail{Path: entry.WorkspacePath},
				Attempts:        api.AttemptDetail{RestartCount: entry.Session.TurnCount},
				Running: &api.RunningSnapshot{
					IssueID:         entry.Issue.ID,
					IssueIdentifier: identifier,
					State:           entry.Issue.State,
					SessionID:       entry.Session.SessionID,
					TurnCount:       entry.Session.TurnCount,
					LastEvent:       lastEvent,
					LastMessage:     lastMessage,
					StartedAt:       entry.StartedAt,
					LastEventAt: func() time.Time {
						if entry.Session.LastCodexTimestamp != nil {
							return *entry.Session.LastCodexTimestamp
						}
						return time.Time{}
					}(),
					Tokens: api.TokenSnapshot{
						InputTokens:  entry.Session.CodexInputTokens,
						OutputTokens: entry.Session.CodexOutputTokens,
						TotalTokens:  entry.Session.CodexTotalTokens,
					},
				},
				Logs:         api.LogDetail{CodexSessionLogs: []api.LogRef{}},
				RecentEvents: []api.EventDetail{},
				LastError:    nil,
				Tracked:      map[string]interface{}{},
			}, true
		}
	}

	for _, entry := range o.state.RetryAttempts {
		if entry.Identifier == identifier {
			return api.IssueDetailResponse{
				IssueIdentifier: identifier,
				IssueID:         entry.IssueID,
				Status:          "retrying",
				Workspace:       api.WorkspaceDetail{},
				Attempts:        api.AttemptDetail{CurrentRetryAttempt: entry.Attempt},
				Running:         nil,
				Retry: &api.RetrySnapshot{
					IssueID:         entry.IssueID,
					IssueIdentifier: identifier,
					Attempt:         entry.Attempt,
					DueAt:           time.UnixMilli(entry.DueAtMs),
					Error:           entry.Error,
				},
				Logs:         api.LogDetail{CodexSessionLogs: []api.LogRef{}},
				RecentEvents: []api.EventDetail{},
				LastError:    entry.Error,
				Tracked:      map[string]interface{}{},
			}, true
		}
	}

	return api.IssueDetailResponse{}, false
}
