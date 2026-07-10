package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kbsartain/simphony/internal/agentruntime"
	"github.com/kbsartain/simphony/internal/preflight"
	"github.com/kbsartain/simphony/internal/workspace"
	"github.com/kbsartain/simphony/pkg/api"
)

// AgentRunner is the interface for launching coding-agent sessions.
type AgentRunner interface {
	Run(ctx context.Context, issue api.Issue, workspace *api.Workspace, attempt *int, cfg *api.AgentRuntimeConfig, stage api.PipelineStage, maxTurns int, shouldContinue func() (api.ContinueDecision, error), eventCallback func(api.AgentEvent)) error
}

// DispatchLimiter optionally coordinates agent launch capacity across orchestrators.
type DispatchLimiter interface {
	TryAcquire() bool
	Release()
}

type ownerAwareDispatchLimiter interface {
	TryAcquireFor(owner string) bool
	ForgetOwner(owner string)
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
	limiter      DispatchLimiter
}

const (
	retryKindAgent                = "agent"
	retryKindCompletionTransition = "completion_transition"
)

var controllablePipelineStages = map[string]struct{}{
	"coding":            {},
	"review":            {},
	"review_resolution": {},
	"merge":             {},
}

// Orchestrator owns the poll loop, dispatch, retries, and reconciliation.
type Orchestrator struct {
	state        api.OrchestratorState
	cfg          *api.WorkflowConfig
	tracker      api.Tracker
	workspaceMgr *workspace.Manager
	runner       AgentRunner
	limiter      DispatchLimiter

	logMu          sync.RWMutex
	logProjectID   string
	logProjectName string
	logSecrets     []string

	mu             sync.Mutex
	ticker         *time.Ticker
	stopCh         chan struct{}
	wg             sync.WaitGroup
	workerWg       sync.WaitGroup
	workerCancels  map[string]context.CancelFunc
	terminalDone   map[string]struct{}
	reviewAttempts map[string]int
	completedCh    chan workerResult
	retryCh        chan string
	refreshCh      chan struct{}

	lastDispatchDeferredReason string
	lastDispatchDeferredAt     time.Time
	preflightHealth            api.ProjectHealth
}

// New creates a new Orchestrator. Call Start() to begin polling.
func New(cfg *api.WorkflowConfig, tracker api.Tracker, workspaceMgr *workspace.Manager, runner AgentRunner) *Orchestrator {
	o := &Orchestrator{
		cfg:          cfg,
		tracker:      tracker,
		workspaceMgr: workspaceMgr,
		runner:       runner,
		stopCh:       make(chan struct{}),
	}
	o.SetLogSecrets(logSecretsFromConfig(cfg))
	return o
}

// SetDispatchLimiter configures an optional shared dispatch limiter.
func (o *Orchestrator) SetDispatchLimiter(limiter DispatchLimiter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.limiter = limiter
}

// SetProjectPaused enables or disables a project-wide soft pause. Running
// workers are not cancelled; only future dispatch and retry work is gated.
func (o *Orchestrator) SetProjectPaused(paused bool) api.ControlState {
	o.mu.Lock()
	o.state.Paused = paused
	state := o.controlStateLocked()
	if !paused && o.lastDispatchDeferredReason == "project_paused" {
		o.lastDispatchDeferredReason = ""
		o.lastDispatchDeferredAt = time.Time{}
	}
	o.mu.Unlock()

	if !paused {
		o.wakeAfterResume()
	}
	return state
}

// SetStagePaused enables or disables a soft pause for one pipeline stage.
// The accepted stages are coding, review, review_resolution, and merge.
func (o *Orchestrator) SetStagePaused(stage string, paused bool) (api.ControlState, error) {
	stage = normalizeControlStage(stage)
	if _, ok := controllablePipelineStages[stage]; !ok {
		return o.ControlState(), fmt.Errorf("unknown pipeline stage %q", stage)
	}

	o.mu.Lock()
	if o.state.PausedStages == nil {
		o.state.PausedStages = make(map[string]struct{})
	}
	if paused {
		o.state.PausedStages[stage] = struct{}{}
	} else {
		delete(o.state.PausedStages, stage)
		if o.lastDispatchDeferredReason == "stage_paused:"+stage {
			o.lastDispatchDeferredReason = ""
			o.lastDispatchDeferredAt = time.Time{}
		}
	}
	state := o.controlStateLocked()
	o.mu.Unlock()

	if !paused {
		o.wakeAfterResume()
	}
	return state, nil
}

// ControlState returns the current project and stage soft-pause state.
func (o *Orchestrator) ControlState() api.ControlState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.controlStateLocked()
}

func (o *Orchestrator) controlStateLocked() api.ControlState {
	stages := make([]string, 0, len(o.state.PausedStages))
	for stage := range o.state.PausedStages {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	return api.ControlState{Paused: o.state.Paused, PausedStages: stages}
}

func normalizeControlStage(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	stage = strings.ReplaceAll(stage, "-", "_")
	stage = strings.ReplaceAll(stage, " ", "_")
	return stage
}

func (o *Orchestrator) pauseReason(stage string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pauseReasonLocked(stage)
}

func (o *Orchestrator) pauseReasonLocked(stage string) string {
	if o.state.Paused {
		return "project_paused"
	}
	stage = normalizeControlStage(stage)
	if _, paused := o.state.PausedStages[stage]; paused {
		return "stage_paused:" + stage
	}
	return ""
}

func (o *Orchestrator) wakeAfterResume() {
	o.mu.Lock()
	retryIDs := make([]string, 0, len(o.state.RetryAttempts))
	for issueID := range o.state.RetryAttempts {
		retryIDs = append(retryIDs, issueID)
	}
	retryCh := o.retryCh
	refreshCh := o.refreshCh
	stopCh := o.stopCh
	o.mu.Unlock()

	if retryCh != nil {
		for _, issueID := range retryIDs {
			issueID := issueID
			go func() {
				select {
				case retryCh <- issueID:
				case <-stopCh:
				}
			}()
		}
	}
	if refreshCh != nil {
		select {
		case refreshCh <- struct{}{}:
		default:
		}
	}
}

// SetLogContext configures project metadata added to orchestrator log messages.
func (o *Orchestrator) SetLogContext(projectID string, projectName string) {
	o.logMu.Lock()
	defer o.logMu.Unlock()
	o.logProjectID = strings.TrimSpace(projectID)
	o.logProjectName = strings.TrimSpace(projectName)
}

// SetLogSecrets configures known secret values redacted from orchestrator logs.
func (o *Orchestrator) SetLogSecrets(secrets []string) {
	o.logMu.Lock()
	defer o.logMu.Unlock()
	o.logSecrets = normalizeLogSecrets(secrets)
}

func (o *Orchestrator) logf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)

	o.logMu.RLock()
	prefix := o.logPrefixLocked()
	secrets := append([]string(nil), o.logSecrets...)
	o.logMu.RUnlock()

	if prefix != "" {
		message = prefix + " " + message
	}
	log.Print(redactLogMessage(message, secrets))
}

func (o *Orchestrator) logPrefix() string {
	o.logMu.RLock()
	defer o.logMu.RUnlock()
	return o.logPrefixLocked()
}

func (o *Orchestrator) logPrefixLocked() string {
	parts := make([]string, 0, 2)
	if o.logProjectID != "" {
		parts = append(parts, fmt.Sprintf("project_id=%s", o.logProjectID))
	}
	if o.logProjectName != "" {
		parts = append(parts, fmt.Sprintf("project_name=%q", o.logProjectName))
	}
	return strings.Join(parts, " ")
}

func (o *Orchestrator) redactLogMessage(message string) string {
	o.logMu.RLock()
	secrets := append([]string(nil), o.logSecrets...)
	o.logMu.RUnlock()
	return redactLogMessage(message, secrets)
}

func redactLogMessage(message string, secrets []string) string {
	for _, secret := range secrets {
		message = strings.ReplaceAll(message, secret, "********")
	}
	return message
}

func normalizeLogSecrets(secrets []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < 4 {
			continue
		}
		if _, exists := seen[secret]; exists {
			continue
		}
		seen[secret] = struct{}{}
		out = append(out, secret)
	}
	return out
}

func logSecretsFromConfig(cfg *api.WorkflowConfig) []string {
	if cfg == nil {
		return nil
	}
	secrets := []string{cfg.Tracker.APIKey}
	for _, runtime := range []api.AgentRuntimeConfig{cfg.AgentRuntime, cfg.Codex, cfg.Claude} {
		secrets = append(secrets, runtime.APIKey, runtime.AuthToken)
		for key, value := range runtime.Env {
			if isSecretLogEnvName(key) {
				secrets = append(secrets, value)
			}
		}
	}
	return secrets
}

func isSecretLogEnvName(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// Start initializes state, performs startup cleanup, and begins the poll loop.
func (o *Orchestrator) Start() {
	o.mu.Lock()
	o.state.Paused = false
	o.state.PausedStages = make(map[string]struct{})
	o.state.Running = make(map[string]*api.RunningEntry)
	o.state.Claimed = make(map[string]struct{})
	o.state.RetryAttempts = make(map[string]*api.RetryEntry)
	o.state.Completed = make(map[string]struct{})
	o.state.CodexTotals = api.CodexTotals{}
	o.state.CodexRateLimits = make(map[string]interface{})
	o.state.PollIntervalMs = o.cfg.Polling.IntervalMs
	o.state.MaxConcurrentAgents = o.cfg.Agent.MaxConcurrentAgents
	o.preflightHealth = preflight.Check(o.cfg)
	o.workerCancels = make(map[string]context.CancelFunc)
	o.terminalDone = make(map[string]struct{})
	o.reviewAttempts = make(map[string]int)
	o.completedCh = make(chan workerResult, 32)
	o.retryCh = make(chan string, 32)
	o.refreshCh = make(chan struct{}, 1)
	o.mu.Unlock()

	o.cleanupTerminalWorkspaces()

	o.ticker = time.NewTicker(time.Duration(o.cfg.Polling.IntervalMs) * time.Millisecond)
	o.wg.Add(1)
	go o.loop()
}

func (o *Orchestrator) runPreflight(runtime runtimeSnapshot) api.ProjectHealth {
	health := preflight.Check(runtime.cfg)
	o.mu.Lock()
	o.preflightHealth = health
	o.mu.Unlock()
	return health
}

// Stop signals shutdown and waits for workers to finish.
func (o *Orchestrator) Stop() {
	if o.ticker != nil {
		o.ticker.Stop()
	}
	o.forgetSupervisorWait()
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
		o.logf("warning: orchestrator stop timed out waiting for loop")
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
		o.logf("warning: orchestrator stop timed out waiting for workers")
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
	o.refreshTerminalIssues()
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
		limiter:      o.limiter,
	}
}

// reconcile performs stall detection and tracker state refresh.
func (o *Orchestrator) reconcile() {
	runtime := o.runtimeSnapshot()
	o.mu.Lock()

	// Part A: Stall detection.
	type stallInfo struct{ id, identifier string }
	var stalls []stallInfo
	if runtime.cfg.AgentRuntime.StallTimeoutMs > 0 {
		for id, running := range o.state.Running {
			var elapsedMs int64
			if running.Session.LastCodexTimestamp != nil {
				elapsedMs = time.Since(*running.Session.LastCodexTimestamp).Milliseconds()
			} else {
				elapsedMs = time.Since(running.StartedAt).Milliseconds()
			}
			if elapsedMs > int64(runtime.cfg.AgentRuntime.StallTimeoutMs) {
				o.logf("issue_id=%s issue_identifier=%s action=stall_detected provider=%s elapsed_ms=%d stall_timeout_ms=%d", id, running.Issue.Identifier, runtime.cfg.AgentRuntime.Provider, elapsedMs, runtime.cfg.AgentRuntime.StallTimeoutMs)
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
		o.logf("action=reconciliation error=%v", err)
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	for id, running := range o.state.Running {
		issue, found := states[id]
		if !found {
			o.logf("issue_id=%s action=terminate reason=missing_from_tracker", id)
			o.terminateWorkerLocked(id)
			o.removeRunningLocked(id)
			delete(o.state.Claimed, id)
			continue
		}

		stateNorm := strings.ToLower(issue.State)
		if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
			o.logf("issue_id=%s issue_identifier=%s action=terminate reason=terminal_state state=%s", id, running.Issue.Identifier, issue.State)
			o.terminateWorkerLocked(id)
			o.removeRunningLocked(id)
			delete(o.state.Claimed, id)
			o.terminalDone[id] = struct{}{}
			if runtime.cfg.Hooks.BeforeRemove != nil {
				_ = runtime.workspaceMgr.RunHook("before_remove", *runtime.cfg.Hooks.BeforeRemove, running.WorkspacePath, runtime.cfg.Hooks.TimeoutMs)
			}
			_ = runtime.workspaceMgr.RemoveWorkspace(running.Issue.Identifier)
		} else if o.isActiveWithConfig(runtime.cfg, stateNorm) {
			// Update in-memory snapshot.
			running.Issue = issue
			delete(o.terminalDone, id)
		} else {
			o.logf("issue_id=%s issue_identifier=%s action=terminate reason=non_active_state state=%s", id, running.Issue.Identifier, issue.State)
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

func (o *Orchestrator) releaseSupervisorSlot(acquired bool) {
	if !acquired {
		return
	}
	o.mu.Lock()
	limiter := o.limiter
	o.mu.Unlock()
	if limiter != nil {
		limiter.Release()
	}
}

func (o *Orchestrator) tryAcquireSupervisorSlot(limiter DispatchLimiter) bool {
	if limiter == nil {
		return true
	}
	if ownerAware, ok := limiter.(ownerAwareDispatchLimiter); ok {
		if owner := o.projectLogID(); owner != "" {
			return ownerAware.TryAcquireFor(owner)
		}
	}
	return limiter.TryAcquire()
}

func (o *Orchestrator) forgetSupervisorWait() {
	o.mu.Lock()
	limiter := o.limiter
	o.mu.Unlock()
	ownerAware, ok := limiter.(ownerAwareDispatchLimiter)
	if !ok {
		return
	}
	if owner := o.projectLogID(); owner != "" {
		ownerAware.ForgetOwner(owner)
	}
}

func (o *Orchestrator) projectLogID() string {
	o.logMu.RLock()
	defer o.logMu.RUnlock()
	return o.logProjectID
}

func (o *Orchestrator) setDispatchDeferred(reason string, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastDispatchDeferredReason = reason
	o.lastDispatchDeferredAt = at
}

func (o *Orchestrator) clearDispatchDeferred(reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if reason == "" || o.lastDispatchDeferredReason == reason {
		o.lastDispatchDeferredReason = ""
		o.lastDispatchDeferredAt = time.Time{}
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
	if entry.SupervisorSlotAcquired && o.limiter != nil {
		entry.SupervisorSlotAcquired = false
		o.limiter.Release()
	}
	elapsedSec := time.Since(entry.StartedAt).Seconds()
	o.state.CodexTotals.SecondsRunning += elapsedSec
	o.state.CodexTotals.InputTokens += entry.Session.CodexInputTokens
	o.state.CodexTotals.OutputTokens += entry.Session.CodexOutputTokens
	o.state.CodexTotals.TotalTokens += entry.Session.CodexTotalTokens
}

func (o *Orchestrator) dispatchEligibleIssues() {
	runtime := o.runtimeSnapshot()
	health := o.runPreflight(runtime)
	if health.Status == preflight.StatusBlocked {
		o.logf("action=preflight status=blocked summary=%q", health.Summary)
		o.setDispatchDeferred("project_preflight_blocked", time.Now())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := runtime.tracker.FetchCandidateIssues(ctx)
	if err != nil {
		o.logf("action=candidate_fetch error=%v", err)
		return
	}

	candidateCount := len(candidates)
	candidates = o.filterEligible(candidates)
	eligibleCount := len(candidates)
	o.logf("action=candidate_fetch result_count=%d eligible_count=%d", candidateCount, eligibleCount)
	candidates = o.sortIssues(candidates)
	if eligibleCount == 0 {
		o.forgetSupervisorWait()
	}

	for _, issue := range candidates {
		if !o.canDispatch() {
			o.logf("action=dispatch_deferred reason=no_global_slots")
			o.forgetSupervisorWait()
			break
		}
		if !o.dispatch(issue) {
			break
		}
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
		delete(o.terminalDone, issue.ID)
		if _, running := o.state.Running[issue.ID]; running {
			continue
		}
		if _, claimed := o.state.Claimed[issue.ID]; claimed {
			continue
		}

		if strings.EqualFold(stateNorm, "todo") && o.hasOpenBlocker(cfg, issue) {
			continue
		}

		eligible = append(eligible, issue)
	}
	return eligible
}

func (o *Orchestrator) hasOpenBlocker(cfg *api.WorkflowConfig, issue api.Issue) bool {
	for _, b := range issue.BlockedBy {
		if b.State == nil || strings.TrimSpace(*b.State) == "" {
			return true
		}
		if !o.isTerminalWithConfig(cfg, strings.ToLower(*b.State)) {
			return true
		}
	}
	return false
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
		prefixI, seqI, okI := issueSequence(issues[i].Identifier)
		prefixJ, seqJ, okJ := issueSequence(issues[j].Identifier)
		if okI && okJ && prefixI == prefixJ && seqI != seqJ {
			return seqI < seqJ
		}

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

func issueSequence(identifier string) (string, int, bool) {
	identifier = strings.TrimSpace(identifier)
	idx := strings.LastIndex(identifier, "-")
	if idx <= 0 || idx == len(identifier)-1 {
		return "", 0, false
	}
	seq, err := strconv.Atoi(identifier[idx+1:])
	if err != nil {
		return "", 0, false
	}
	return strings.ToUpper(identifier[:idx]), seq, true
}

func (o *Orchestrator) dispatch(issue api.Issue) bool {
	runtime := o.runtimeSnapshot()
	stage := o.pipelineStage(issue, runtime.cfg)
	if reason := o.pauseReason(stage.Kind); reason != "" {
		o.logf("issue_id=%s issue_identifier=%s action=dispatch_deferred reason=%s stage=%s", issue.ID, issue.Identifier, reason, stage.Kind)
		o.setDispatchDeferred(reason, time.Now())
		return reason != "project_paused"
	}
	if o.perStateSlots(issue.State) <= 0 {
		o.logf("issue_id=%s issue_identifier=%s action=dispatch_deferred reason=no_state_slots state=%q", issue.ID, issue.Identifier, issue.State)
		return true
	}

	supervisorSlotAcquired := false
	if runtime.limiter != nil {
		if !o.tryAcquireSupervisorSlot(runtime.limiter) {
			o.logf("issue_id=%s issue_identifier=%s action=dispatch_deferred reason=no_supervisor_slots", issue.ID, issue.Identifier)
			o.setDispatchDeferred("no_supervisor_slots", time.Now())
			return false
		}
		supervisorSlotAcquired = true
	}

	o.mu.Lock()
	o.state.Claimed[issue.ID] = struct{}{}
	o.mu.Unlock()

	workspace, err := runtime.workspaceMgr.PrepareWorkspace(issue)
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=workspace_prepare failed=%v", issue.ID, issue.Identifier, err)
		o.releaseSupervisorSlot(supervisorSlotAcquired)
		o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("workspace prepare: %v", err))
		return true
	}

	if runtime.cfg.Tracker.WorkingState != "" && stage.Kind == "coding" && !strings.EqualFold(issue.State, runtime.cfg.Tracker.WorkingState) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		updated, err := runtime.tracker.TransitionIssueState(ctx, issue, runtime.cfg.Tracker.WorkingState)
		cancel()
		if err != nil {
			o.logf("issue_id=%s issue_identifier=%s action=working_state_transition failed=%v", issue.ID, issue.Identifier, err)
			o.releaseSupervisorSlot(supervisorSlotAcquired)
			o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("working state transition: %v", err))
			return true
		}
		o.logf("issue_id=%s issue_identifier=%s action=working_state_transition from=%q to=%q", issue.ID, issue.Identifier, issue.State, updated.State)
		issue = updated
	}

	// after_create hook (fatal to workspace creation).
	if workspace.CreatedNow && runtime.cfg.Hooks.AfterCreate != nil {
		if err := runtime.workspaceMgr.RunHook("after_create", *runtime.cfg.Hooks.AfterCreate, workspace.Path, runtime.cfg.Hooks.TimeoutMs); err != nil {
			o.logf("issue_id=%s issue_identifier=%s action=after_create failed=%v", issue.ID, issue.Identifier, err)
			_ = runtime.workspaceMgr.RemoveWorkspace(issue.Identifier)
			o.releaseSupervisorSlot(supervisorSlotAcquired)
			o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("after_create hook: %v", err))
			return true
		}
	}

	// before_run hook (fatal to attempt).
	if runtime.cfg.Hooks.BeforeRun != nil {
		if err := runtime.workspaceMgr.RunHook("before_run", *runtime.cfg.Hooks.BeforeRun, workspace.Path, runtime.cfg.Hooks.TimeoutMs); err != nil {
			o.logf("issue_id=%s issue_identifier=%s action=before_run failed=%v", issue.ID, issue.Identifier, err)
			o.releaseSupervisorSlot(supervisorSlotAcquired)
			o.scheduleFailureRetry(issue.ID, issue.Identifier, fmt.Sprintf("before_run hook: %v", err))
			return true
		}
	}

	// Pause may have been requested while workspace preparation or hooks were
	// running. Re-check immediately before launching the worker so a completed
	// pause request never starts a new agent process.
	if reason := o.pauseReason(stage.Kind); reason != "" {
		o.logf("issue_id=%s issue_identifier=%s action=dispatch_deferred reason=%s stage=%s checkpoint=before_worker", issue.ID, issue.Identifier, reason, stage.Kind)
		o.setDispatchDeferred(reason, time.Now())
		o.mu.Lock()
		delete(o.state.Claimed, issue.ID)
		o.mu.Unlock()
		o.releaseSupervisorSlot(supervisorSlotAcquired)
		return reason != "project_paused"
	}

	effectiveRuntime := agentruntime.EffectiveConfig(&runtime.cfg.AgentRuntime, stage)
	o.mu.Lock()
	o.state.Running[issue.ID] = &api.RunningEntry{
		Issue:                  issue,
		Stage:                  stage.Kind,
		ExecutionProvider:      effectiveRuntime.Provider,
		Model:                  effectiveRuntime.Model,
		ModelProvider:          effectiveRuntime.ModelProvider,
		StartedAt:              time.Now(),
		WorkspacePath:          workspace.Path,
		SupervisorSlotAcquired: supervisorSlotAcquired,
	}
	o.mu.Unlock()
	o.clearDispatchDeferred("no_supervisor_slots")
	o.logf("issue_id=%s issue_identifier=%s action=dispatch_started state=%q stage=%s execution_provider=%s model_provider=%s model=%s workspace=%q", issue.ID, issue.Identifier, issue.State, stage.Kind, effectiveRuntime.Provider, effectiveRuntime.ModelProvider, effectiveRuntime.Model, workspace.Path)
	if stage.Kind == "review_resolution" {
		o.postStatusComment(issue, runtime, "Simphony review resolution started", fmt.Sprintf("Autonomous PR/code-review resolution is running.\n\nPolicy:\n- Require checks green: %t\n- Require review approval: %t\n- Unresolved comments: %s",
			runtime.cfg.ReviewResolution.RequireChecksGreen,
			runtime.cfg.ReviewResolution.RequireCodeReviewApproval,
			runtime.cfg.ReviewResolution.UnresolvedCommentPolicy,
		))
	}

	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.workerCancels[issue.ID] = cancel
	o.mu.Unlock()

	o.workerWg.Add(1)
	go func() {
		defer o.workerWg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				o.completedCh <- workerResult{issueID: issue.ID, err: fmt.Errorf("worker panic: %v", recovered)}
			}
		}()
		o.runWorker(ctx, issue, workspace, runtime)
	}()
	return true
}

func (o *Orchestrator) runWorker(ctx context.Context, issue api.Issue, workspace *api.Workspace, runtime runtimeSnapshot) {
	eventCallback := func(event api.AgentEvent) {
		o.mu.Lock()
		if running, ok := o.state.Running[issue.ID]; ok {
			running.Session.LastCodexEvent = &event.Event
			running.Session.LastCodexTimestamp = &event.Timestamp
			running.Session.LastCodexMessage = summarizeEvent(event)
			running.RecentEvents = appendRecentEvent(running.RecentEvents, event)
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
			if msg := commentableAgentMessage(event); msg != "" {
				running.Session.LastCodexMessage = msg
				go o.postAgentComment(issue, runtime, msg)
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

	stage := o.pipelineStage(issue, runtime.cfg)
	err := runtime.runner.Run(ctx, issue, workspace, nil, &runtime.cfg.AgentRuntime, stage, runtime.cfg.Agent.MaxTurns, shouldContinue, eventCallback)

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

func (o *Orchestrator) postAgentComment(issue api.Issue, runtime runtimeSnapshot, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := fmt.Sprintf("**Simphony agent update**\n\n%s", message)
	if err := runtime.tracker.AddIssueComment(ctx, issue, body); err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=agent_comment failed=%v", issue.ID, issue.Identifier, err)
		return
	}
	o.logf("issue_id=%s issue_identifier=%s action=agent_comment posted", issue.ID, issue.Identifier)
}

func (o *Orchestrator) postStatusComment(issue api.Issue, runtime runtimeSnapshot, title string, message string) {
	title = strings.TrimSpace(title)
	message = strings.TrimSpace(message)
	if title == "" || message == "" || runtime.tracker == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := fmt.Sprintf("**%s**\n\n%s", title, message)
	if err := runtime.tracker.AddIssueComment(ctx, issue, body); err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=status_comment title=%q failed=%v", issue.ID, issue.Identifier, title, err)
		return
	}
	o.logf("issue_id=%s issue_identifier=%s action=status_comment title=%q posted", issue.ID, issue.Identifier, title)
}

func summarizeEvent(event api.AgentEvent) string {
	if event.Payload == nil {
		return ""
	}
	if msg := commentableAgentMessage(event); msg != "" {
		return msg
	}
	for _, key := range []string{"message", "text", "status", "reason"} {
		if v, ok := event.Payload[key].(string); ok {
			return v
		}
	}
	return ""
}

func commentableAgentMessage(event api.AgentEvent) string {
	if event.Event != "item/completed" || event.Payload == nil {
		return ""
	}
	item, ok := event.Payload["item"].(map[string]interface{})
	if !ok {
		return ""
	}
	if itemType, ok := item["type"].(string); !ok || itemType != "agentMessage" {
		return ""
	}
	text, _ := item["text"].(string)
	return strings.TrimSpace(text)
}

func appendRecentEvent(events []api.EventDetail, event api.AgentEvent) []api.EventDetail {
	events = append(events, api.EventDetail{
		At:      event.Timestamp,
		Event:   event.Event,
		Message: summarizeEvent(event),
	})
	if len(events) > 20 {
		return events[len(events)-20:]
	}
	return events
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneInterfaceMap(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	out := make(map[string]interface{}, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
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
		Continue: false,
	}
	if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
		decision.Reason = "terminal_state"
	} else if !o.isActiveWithConfig(runtime.cfg, stateNorm) {
		decision.Reason = "non_active_state"
	} else {
		decision.Reason = "completed_turn"
	}

	o.mu.Lock()
	if running, ok := o.state.Running[issueID]; ok {
		running.Issue = issue
	}
	o.mu.Unlock()

	return decision, nil
}

func (o *Orchestrator) pipelineStage(issue api.Issue, cfg *api.WorkflowConfig) api.PipelineStage {
	if cfg != nil && o.reviewResolutionEnabled(cfg) && equalState(issue.State, cfg.Pipeline.ReviewResolutionState) {
		return api.PipelineStage{
			Kind: "review_resolution",
			Instructions: fmt.Sprintf(
				"Resolve formal PR/code-review feedback for issue %s autonomously. Inspect the PR, unresolved review comments, review decision, and CI/check results using the repository's configured GitHub tooling. Fix actionable feedback, reply to comments when appropriate, rerun relevant checks, and push updates. Require checks green: %t. Require review approval: %t. Unresolved comment policy: %s. Escalate on: %s. End your final response with exactly one directive line: SIMPHONY_REVIEW_DECISION: approved, SIMPHONY_REVIEW_DECISION: retry, or SIMPHONY_REVIEW_DECISION: escalate.",
				issue.Identifier,
				cfg.ReviewResolution.RequireChecksGreen,
				cfg.ReviewResolution.RequireCodeReviewApproval,
				cfg.ReviewResolution.UnresolvedCommentPolicy,
				strings.Join(cfg.ReviewResolution.EscalateOn, ", "),
			),
		}
	}
	if cfg != nil && equalState(issue.State, cfg.Pipeline.ReviewState) {
		return api.PipelineStage{
			Kind:         "review",
			Instructions: fmt.Sprintf("Perform an internal high-confidence review for issue %s before approval. Inspect the workspace for correctness, security, architecture consistency, and test coverage. Fix concrete issues, run appropriate checks, and summarize the review outcome.", issue.Identifier),
		}
	}
	if cfg != nil && equalState(issue.State, cfg.Pipeline.MergeState) {
		return api.PipelineStage{
			Kind:         "merge",
			Instructions: fmt.Sprintf("Human review for issue %s has been approved. Evaluate the existing workspace changes for merging, resolve merge-related issues, run appropriate checks, and commit the final changes to the repository.", issue.Identifier),
		}
	}
	return api.PipelineStage{Kind: "coding"}
}

func (o *Orchestrator) reviewResolutionEnabled(cfg *api.WorkflowConfig) bool {
	return cfg != nil && cfg.ReviewResolution.Enabled && strings.TrimSpace(cfg.Pipeline.ReviewResolutionState) != ""
}

func reviewResolutionDecision(message string) string {
	normalized := strings.ToLower(message)
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "simphony_review_decision:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "simphony_review_decision:"))
		switch value {
		case "approved", "retry", "escalate":
			return value
		}
	}
	return "approved"
}

func equalState(a string, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
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
	identifier := entry.Issue.Identifier
	o.removeRunningLocked(result.issueID)
	delete(o.workerCancels, result.issueID)
	o.mu.Unlock()

	if result.err != nil {
		if strings.Contains(result.err.Error(), api.ErrMaxTurnsReached) {
			o.logf("issue_id=%s issue_identifier=%s action=worker_exit status=max_turns_reached", result.issueID, identifier)
			o.completeIssueAfterRun(runtime, result.issueID, identifier, entry)
			return
		}
		o.logf("issue_id=%s issue_identifier=%s action=worker_exit status=failed error=%v", result.issueID, identifier, result.err)
		o.scheduleFailureRetry(result.issueID, identifier, result.err.Error())
	} else {
		o.logf("issue_id=%s issue_identifier=%s action=worker_exit status=success", result.issueID, identifier)
		stateNorm := strings.ToLower(entry.Issue.State)
		if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
			o.mu.Lock()
			delete(o.state.Claimed, result.issueID)
			o.mu.Unlock()
			o.removeTerminalWorkspace(runtime, entry.Issue, entry.WorkspacePath)
			return
		}
		if !o.isActiveWithConfig(runtime.cfg, stateNorm) {
			o.releaseClaim(result.issueID)
			return
		}
		o.completeIssueAfterRun(runtime, result.issueID, identifier, entry)
	}
}

func (o *Orchestrator) completeIssueAfterRun(runtime runtimeSnapshot, issueID string, identifier string, entry *api.RunningEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	states, err := runtime.tracker.FetchIssueStatesByIDs(ctx, []string{issueID})
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=completion_state_check status=failed error=%v", issueID, identifier, err)
		o.scheduleRetry(issueID, identifier, fmt.Sprintf("completion state check: %v", err), retryKindCompletionTransition)
		return
	}

	currentIssue, found := states[issueID]
	if !found {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition skipped=missing_from_tracker", issueID, identifier)
		o.releaseClaim(issueID)
		return
	}
	if currentIssue.Identifier != "" {
		identifier = currentIssue.Identifier
	}
	stateNorm := strings.ToLower(currentIssue.State)
	if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition skipped=terminal_state state=%q", issueID, identifier, currentIssue.State)
		o.releaseClaim(issueID)
		o.removeTerminalWorkspace(runtime, currentIssue, entry.WorkspacePath)
		return
	}
	if !o.isActiveWithConfig(runtime.cfg, stateNorm) {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition skipped=non_active_state state=%q", issueID, identifier, currentIssue.State)
		o.releaseClaim(issueID)
		return
	}
	if equalState(currentIssue.State, runtime.cfg.Pipeline.MergeState) {
		o.transitionMergeIssueToDone(ctx, runtime, currentIssue, identifier, entry.WorkspacePath)
		return
	}
	if o.reviewResolutionEnabled(runtime.cfg) && equalState(currentIssue.State, runtime.cfg.Pipeline.ReviewResolutionState) {
		o.resolveReviewResolutionCompletion(ctx, runtime, currentIssue, identifier, entry)
		return
	}
	if equalState(currentIssue.State, runtime.cfg.Pipeline.ReviewState) {
		o.transitionReviewIssueToMerge(ctx, runtime, currentIssue, identifier, entry.WorkspacePath)
		return
	}

	updatedIssue, err := runtime.tracker.MoveIssueToFirstAvailableState(ctx, issueID, completionStatePreferences(runtime.cfg))
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition status=failed error=%v", issueID, identifier, err)
		o.scheduleRetry(issueID, identifier, fmt.Sprintf("completion transition: %v", err), retryKindCompletionTransition)
		return
	}
	if updatedIssue.Identifier != "" {
		identifier = updatedIssue.Identifier
	}

	o.logf("issue_id=%s issue_identifier=%s action=completion_transition status=success state=%q", issueID, identifier, updatedIssue.State)
	o.markCompletionTransitioned(runtime, updatedIssue.State, issueID, identifier, entry.WorkspacePath)
}

func completionStatePreferences(cfg *api.WorkflowConfig) []string {
	if cfg == nil {
		return []string{"In Review", "Review", "Done", "Completed"}
	}
	if len(cfg.Tracker.CompletionStates) > 0 {
		return appendPreferenceUnique([]string{cfg.Pipeline.ReviewState}, cfg.Tracker.CompletionStates...)
	}

	states := appendPreferenceUnique([]string{cfg.Pipeline.ReviewState}, "In Review", "Review", "Done", "Completed")
	for _, state := range cfg.Tracker.TerminalStates {
		states = appendPreferenceUnique(states, state)
	}
	return states
}

func appendPreferenceUnique(states []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(states)+len(extras))
	out := make([]string, 0, len(states)+len(extras))
	for _, state := range append(states, extras...) {
		state = strings.TrimSpace(state)
		if state == "" {
			continue
		}
		key := strings.ToLower(state)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, state)
	}
	return out
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
		Kind:        retryKindAgent,
		Attempt:     1,
		DueAtMs:     time.Now().UnixMilli() + 1000,
		TimerHandle: timer,
	}
}

func (o *Orchestrator) scheduleFailureRetry(issueID string, identifier string, errorMsg string) {
	o.scheduleRetry(issueID, identifier, errorMsg, retryKindAgent)
}

func (o *Orchestrator) scheduleRetry(issueID string, identifier string, errorMsg string, kind string) {
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
		Kind:        kind,
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
	isCompletionTransitionRetry := false
	dueAtMs := int64(0)
	if entry := o.state.RetryAttempts[issueID]; entry != nil {
		identifier = entry.Identifier
		isCompletionTransitionRetry = entry.Kind == retryKindCompletionTransition
		dueAtMs = entry.DueAtMs
	} else {
		o.mu.Unlock()
		o.logf("issue_id=%s action=retry_stale skipped=missing_retry_entry", issueID)
		return
	}
	o.mu.Unlock()

	if dueAtMs > time.Now().UnixMilli() {
		o.logf("issue_id=%s issue_identifier=%s action=retry_stale skipped=not_due due_at_ms=%d", issueID, identifier, dueAtMs)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if isCompletionTransitionRetry {
		o.handleCompletionTransitionRetry(ctx, runtime, issueID, identifier)
		return
	}

	candidates, err := runtime.tracker.FetchCandidateIssues(ctx)
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=retry_fetch_failed error=%v", issueID, identifier, err)
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
		o.logf("issue_id=%s issue_identifier=%s action=retry_issue_missing releasing_claim", issueID, identifier)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		return
	}

	stateNorm := strings.ToLower(issue.State)
	if !o.isActiveWithConfig(runtime.cfg, stateNorm) || o.isTerminalWithConfig(runtime.cfg, stateNorm) {
		o.logf("issue_id=%s issue_identifier=%s action=retry_issue_inactive state=%s releasing_claim", issueID, issue.Identifier, issue.State)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		return
	}

	stage := o.pipelineStage(*issue, runtime.cfg)
	if reason := o.pauseReason(stage.Kind); reason != "" {
		o.logf("issue_id=%s issue_identifier=%s action=retry_deferred reason=%s stage=%s", issueID, issue.Identifier, reason, stage.Kind)
		o.setDispatchDeferred(reason, time.Now())
		return
	}

	if !o.canDispatch() || o.perStateSlots(issue.State) <= 0 {
		o.logf("issue_id=%s issue_identifier=%s action=retry_no_slots requeuing", issueID, issue.Identifier)
		o.scheduleFailureRetry(issueID, issue.Identifier, "no available orchestrator slots")
		return
	}

	o.mu.Lock()
	delete(o.state.RetryAttempts, issueID)
	o.mu.Unlock()
	o.dispatch(*issue)
}

func (o *Orchestrator) handleCompletionTransitionRetry(ctx context.Context, runtime runtimeSnapshot, issueID string, identifier string) {
	states, err := runtime.tracker.FetchIssueStatesByIDs(ctx, []string{issueID})
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry state_check=failed error=%v", issueID, identifier, err)
		o.scheduleRetry(issueID, identifier, fmt.Sprintf("completion state check: %v", err), retryKindCompletionTransition)
		return
	}

	issue, found := states[issueID]
	if !found {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry skipped=missing_from_tracker", issueID, identifier)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		return
	}
	if issue.Identifier != "" {
		identifier = issue.Identifier
	}
	if issue.ID == "" {
		issue.ID = issueID
	}

	stateNorm := strings.ToLower(issue.State)
	if o.isTerminalWithConfig(runtime.cfg, stateNorm) {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry skipped=terminal_state state=%q", issueID, identifier, issue.State)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		o.removeTerminalWorkspace(runtime, issue, "")
		return
	}
	if !o.isActiveWithConfig(runtime.cfg, stateNorm) {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry skipped=non_active_state state=%q", issueID, identifier, issue.State)
		o.mu.Lock()
		delete(o.state.RetryAttempts, issueID)
		o.mu.Unlock()
		o.releaseClaim(issueID)
		return
	}
	stage := o.pipelineStage(issue, runtime.cfg)
	if reason := o.pauseReason(stage.Kind); reason != "" {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry deferred=%s stage=%s", issueID, identifier, reason, stage.Kind)
		o.setDispatchDeferred(reason, time.Now())
		return
	}
	if equalState(issue.State, runtime.cfg.Pipeline.MergeState) {
		o.transitionMergeIssueToDone(ctx, runtime, issue, identifier, "")
		return
	}
	if o.reviewResolutionEnabled(runtime.cfg) && equalState(issue.State, runtime.cfg.Pipeline.ReviewResolutionState) {
		o.resolveReviewResolutionCompletion(ctx, runtime, issue, identifier, &api.RunningEntry{Issue: issue})
		return
	}
	if equalState(issue.State, runtime.cfg.Pipeline.ReviewState) {
		o.transitionReviewIssueToMerge(ctx, runtime, issue, identifier, "")
		return
	}

	updatedIssue, err := runtime.tracker.MoveIssueToFirstAvailableState(ctx, issue.ID, completionStatePreferences(runtime.cfg))
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry status=failed error=%v", issue.ID, identifier, err)
		o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("completion transition: %v", err), retryKindCompletionTransition)
		return
	}
	if updatedIssue.Identifier != "" {
		identifier = updatedIssue.Identifier
	}

	o.logf("issue_id=%s issue_identifier=%s action=completion_transition_retry status=success state=%q", issue.ID, identifier, updatedIssue.State)
	o.markCompletionTransitioned(runtime, updatedIssue.State, issue.ID, identifier, "")
}

func (o *Orchestrator) transitionMergeIssueToDone(ctx context.Context, runtime runtimeSnapshot, issue api.Issue, identifier string, workspacePath string) {
	if runtime.workspaceMgr != nil {
		if strings.TrimSpace(workspacePath) == "" {
			workspacePath = runtime.workspaceMgr.GetWorkspacePath(issue.Identifier)
		}
		if err := runtime.workspaceMgr.MergeWorkspaceToBaseBranch(issue, workspacePath); err != nil {
			o.logf("issue_id=%s issue_identifier=%s action=merge_completion_transition status=failed error=%v", issue.ID, identifier, err)
			o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("merge completion: %v", err), retryKindCompletionTransition)
			return
		}
	}

	updatedIssue, err := runtime.tracker.MoveIssueToState(ctx, issue.ID, runtime.cfg.Pipeline.DoneState)
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=merge_completion_transition status=failed error=%v", issue.ID, identifier, err)
		o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("merge completion transition: %v", err), retryKindCompletionTransition)
		return
	}
	if updatedIssue.Identifier != "" {
		identifier = updatedIssue.Identifier
	}
	o.logf("issue_id=%s issue_identifier=%s action=merge_completion_transition status=success state=%q", issue.ID, identifier, updatedIssue.State)
	o.markCompletionTransitioned(runtime, updatedIssue.State, issue.ID, identifier, workspacePath)
}

func (o *Orchestrator) transitionReviewIssueToMerge(ctx context.Context, runtime runtimeSnapshot, issue api.Issue, identifier string, workspacePath string) {
	if o.reviewResolutionEnabled(runtime.cfg) {
		o.transitionReviewIssueToReviewResolution(ctx, runtime, issue, identifier, workspacePath)
		return
	}

	updatedIssue, err := runtime.tracker.MoveIssueToState(ctx, issue.ID, runtime.cfg.Pipeline.MergeState)
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=review_completion_transition status=failed error=%v", issue.ID, identifier, err)
		o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("review completion transition: %v", err), retryKindCompletionTransition)
		return
	}
	if updatedIssue.Identifier != "" {
		identifier = updatedIssue.Identifier
	}
	o.logf("issue_id=%s issue_identifier=%s action=review_completion_transition status=success state=%q", issue.ID, identifier, updatedIssue.State)
	o.markCompletionTransitioned(runtime, updatedIssue.State, issue.ID, identifier, workspacePath)
}

func (o *Orchestrator) transitionReviewIssueToReviewResolution(ctx context.Context, runtime runtimeSnapshot, issue api.Issue, identifier string, workspacePath string) {
	updatedIssue, err := runtime.tracker.MoveIssueToState(ctx, issue.ID, runtime.cfg.Pipeline.ReviewResolutionState)
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=review_resolution_transition status=failed error=%v", issue.ID, identifier, err)
		o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("review resolution transition: %v", err), retryKindCompletionTransition)
		return
	}
	if updatedIssue.Identifier != "" {
		identifier = updatedIssue.Identifier
	}
	o.logf("issue_id=%s issue_identifier=%s action=review_resolution_transition status=success state=%q", issue.ID, identifier, updatedIssue.State)
	o.markCompletionTransitioned(runtime, updatedIssue.State, issue.ID, identifier, workspacePath)
}

func (o *Orchestrator) resolveReviewResolutionCompletion(ctx context.Context, runtime runtimeSnapshot, issue api.Issue, identifier string, entry *api.RunningEntry) {
	decision := reviewResolutionDecision(entry.Session.LastCodexMessage)
	switch decision {
	case "retry":
		o.scheduleReviewResolutionRetryOrEscalate(ctx, runtime, issue, identifier)
	case "escalate":
		o.transitionReviewResolutionToEscalation(ctx, runtime, issue, identifier, entry.WorkspacePath, "agent requested escalation")
	default:
		updatedIssue, err := runtime.tracker.MoveIssueToState(ctx, issue.ID, runtime.cfg.Pipeline.MergeState)
		if err != nil {
			o.logf("issue_id=%s issue_identifier=%s action=review_resolution_completion status=failed error=%v", issue.ID, identifier, err)
			o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("review resolution completion: %v", err), retryKindCompletionTransition)
			return
		}
		if updatedIssue.Identifier != "" {
			identifier = updatedIssue.Identifier
		}
		o.logf("issue_id=%s issue_identifier=%s action=review_resolution_completion status=success state=%q decision=%q", issue.ID, identifier, updatedIssue.State, decision)
		o.postStatusComment(updatedIssue, runtime, "Simphony review resolution approved", fmt.Sprintf("Autonomous PR/code-review resolution completed successfully.\n\nDecision: %s\nNext state: %s", decision, updatedIssue.State))
		o.markCompletionTransitioned(runtime, updatedIssue.State, issue.ID, identifier, entry.WorkspacePath)
	}
}

func (o *Orchestrator) scheduleReviewResolutionRetryOrEscalate(ctx context.Context, runtime runtimeSnapshot, issue api.Issue, identifier string) {
	o.mu.Lock()
	o.reviewAttempts[issue.ID]++
	attempt := o.reviewAttempts[issue.ID]
	o.mu.Unlock()
	if attempt > runtime.cfg.ReviewResolution.MaxAttempts {
		o.transitionReviewResolutionToEscalation(ctx, runtime, issue, identifier, "", "max review-resolution attempts exceeded")
		return
	}
	o.logf("issue_id=%s issue_identifier=%s action=review_resolution_retry status=scheduled attempt=%d max_attempts=%d", issue.ID, identifier, attempt, runtime.cfg.ReviewResolution.MaxAttempts)
	o.postStatusComment(issue, runtime, "Simphony review resolution retry scheduled", fmt.Sprintf("The review-resolution agent requested another autonomous pass.\n\nAttempt: %d of %d", attempt, runtime.cfg.ReviewResolution.MaxAttempts))
	o.scheduleRetry(issue.ID, identifier, "review resolution requested another pass", retryKindAgent)
}

func (o *Orchestrator) transitionReviewResolutionToEscalation(ctx context.Context, runtime runtimeSnapshot, issue api.Issue, identifier string, workspacePath string, reason string) {
	targetState := strings.TrimSpace(runtime.cfg.ReviewResolution.EscalationState)
	if targetState == "" {
		targetState = runtime.cfg.Pipeline.ReviewState
	}
	updatedIssue, err := runtime.tracker.MoveIssueToState(ctx, issue.ID, targetState)
	if err != nil {
		o.logf("issue_id=%s issue_identifier=%s action=review_resolution_escalation status=failed error=%v", issue.ID, identifier, err)
		o.scheduleRetry(issue.ID, identifier, fmt.Sprintf("review resolution escalation: %v", err), retryKindCompletionTransition)
		return
	}
	if updatedIssue.Identifier != "" {
		identifier = updatedIssue.Identifier
	}
	_ = runtime.tracker.AddIssueComment(ctx, updatedIssue, fmt.Sprintf("**Simphony review resolution escalated**\n\n%s", reason))
	o.logf("issue_id=%s issue_identifier=%s action=review_resolution_escalation status=success state=%q reason=%q", issue.ID, identifier, updatedIssue.State, reason)
	o.markCompletionTransitioned(runtime, updatedIssue.State, issue.ID, identifier, workspacePath)
}

func (o *Orchestrator) markCompletionTransitioned(runtime runtimeSnapshot, state string, issueID string, identifier string, workspacePath string) {
	o.mu.Lock()
	delete(o.state.RetryAttempts, issueID)
	delete(o.reviewAttempts, issueID)
	o.state.Completed[issueID] = struct{}{}
	delete(o.state.Claimed, issueID)
	o.mu.Unlock()

	if !o.isTerminalWithConfig(runtime.cfg, strings.ToLower(state)) || runtime.workspaceMgr == nil {
		return
	}
	if strings.TrimSpace(identifier) == "" {
		o.logf("issue_id=%s action=completion_cleanup skipped=missing_identifier", issueID)
		return
	}
	issue := api.Issue{ID: issueID, Identifier: identifier, State: state}
	o.removeTerminalWorkspace(runtime, issue, workspacePath)
}

func (o *Orchestrator) removeTerminalWorkspace(runtime runtimeSnapshot, issue api.Issue, workspacePath string) {
	o.mu.Lock()
	o.terminalDone[issue.ID] = struct{}{}
	o.mu.Unlock()

	if runtime.workspaceMgr == nil || strings.TrimSpace(issue.Identifier) == "" {
		return
	}
	if runtime.cfg.Hooks.BeforeRemove != nil && workspacePath != "" {
		_ = runtime.workspaceMgr.RunHook("before_remove", *runtime.cfg.Hooks.BeforeRemove, workspacePath, runtime.cfg.Hooks.TimeoutMs)
	}
	_ = runtime.workspaceMgr.RemoveWorkspace(issue.Identifier)
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
		o.logf("action=startup_cleanup warning=%v", err)
		return
	}
	o.setTerminalDone(issues)

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

func (o *Orchestrator) refreshTerminalIssues() {
	runtime := o.runtimeSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := runtime.tracker.FetchIssuesByStates(ctx, runtime.cfg.Tracker.TerminalStates)
	if err != nil {
		o.logf("action=terminal_count_refresh warning=%v", err)
		return
	}
	o.setTerminalDone(issues)
}

func (o *Orchestrator) setTerminalDone(issues []api.Issue) {
	terminalDone := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue.ID != "" {
			terminalDone[issue.ID] = struct{}{}
		}
	}

	o.mu.Lock()
	o.terminalDone = terminalDone
	o.mu.Unlock()
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
			IssueID:           entry.Issue.ID,
			IssueIdentifier:   entry.Issue.Identifier,
			IssueTitle:        entry.Issue.Title,
			IssueURL:          entry.Issue.URL,
			Priority:          entry.Issue.Priority,
			Labels:            cloneStringSlice(entry.Issue.Labels),
			State:             entry.Issue.State,
			Stage:             entry.Stage,
			ExecutionProvider: entry.ExecutionProvider,
			Model:             entry.Model,
			ModelProvider:     entry.ModelProvider,
			SessionID:         entry.Session.SessionID,
			TurnCount:         entry.Session.TurnCount,
			LastEvent:         lastEvent,
			LastMessage:       lastMessage,
			StartedAt:         entry.StartedAt,
			LastEventAt:       lastEventAt,
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
			Kind:            entry.Kind,
			Attempt:         entry.Attempt,
			DueAt:           time.UnixMilli(entry.DueAtMs),
			Error:           entry.Error,
		})
	}
	sort.SliceStable(running, func(i, j int) bool {
		return running[i].IssueIdentifier < running[j].IssueIdentifier
	})
	sort.SliceStable(retrying, func(i, j int) bool {
		if !retrying[i].DueAt.Equal(retrying[j].DueAt) {
			return retrying[i].DueAt.Before(retrying[j].DueAt)
		}
		return retrying[i].IssueIdentifier < retrying[j].IssueIdentifier
	})

	var lastDeferredAt *time.Time
	if !o.lastDispatchDeferredAt.IsZero() {
		at := o.lastDispatchDeferredAt
		lastDeferredAt = &at
	}

	return api.StateSnapshot{
		GeneratedAt:                now,
		PollIntervalMs:             o.state.PollIntervalMs,
		MaxConcurrentAgents:        o.state.MaxConcurrentAgents,
		Control:                    o.controlStateLocked(),
		LastDispatchDeferredReason: o.lastDispatchDeferredReason,
		LastDispatchDeferredAt:     lastDeferredAt,
		Health:                     o.preflightHealth,
		Counts: api.StateCounts{
			Running:   len(o.state.Running),
			Retrying:  len(o.state.RetryAttempts),
			Claimed:   len(o.state.Claimed),
			Completed: o.completedCountLocked(),
		},
		Running:  running,
		Retrying: retrying,
		CodexTotals: api.CodexTotals{
			InputTokens:    inputTokens,
			OutputTokens:   outputTokens,
			TotalTokens:    totalTokens,
			SecondsRunning: seconds,
		},
		RateLimits: cloneInterfaceMap(o.state.CodexRateLimits),
	}
}

func (o *Orchestrator) completedCountLocked() int {
	count := len(o.state.Completed)
	for id := range o.terminalDone {
		if _, alreadyCounted := o.state.Completed[id]; !alreadyCounted {
			count++
		}
	}
	return count
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
	o.cfg = cfg
	o.state.PollIntervalMs = cfg.Polling.IntervalMs
	o.state.MaxConcurrentAgents = cfg.Agent.MaxConcurrentAgents
	o.preflightHealth = preflight.Check(cfg)

	if o.ticker != nil && cfg.Polling.IntervalMs > 0 {
		o.ticker.Reset(time.Duration(cfg.Polling.IntervalMs) * time.Millisecond)
	}
	o.mu.Unlock()
	o.SetLogSecrets(logSecretsFromConfig(cfg))
}

// UpdateRuntime applies a reloaded config and replaces dependencies derived
// from that config for future scheduler operations.
func (o *Orchestrator) UpdateRuntime(cfg *api.WorkflowConfig, tracker api.Tracker, workspaceMgr *workspace.Manager) {
	o.mu.Lock()
	o.cfg = cfg
	o.tracker = tracker
	o.workspaceMgr = workspaceMgr
	o.state.PollIntervalMs = cfg.Polling.IntervalMs
	o.state.MaxConcurrentAgents = cfg.Agent.MaxConcurrentAgents
	o.preflightHealth = preflight.Check(cfg)

	if o.ticker != nil && cfg.Polling.IntervalMs > 0 {
		o.ticker.Reset(time.Duration(cfg.Polling.IntervalMs) * time.Millisecond)
	}
	o.mu.Unlock()
	o.SetLogSecrets(logSecretsFromConfig(cfg))
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
					IssueID:           entry.Issue.ID,
					IssueIdentifier:   identifier,
					IssueTitle:        entry.Issue.Title,
					IssueURL:          entry.Issue.URL,
					Priority:          entry.Issue.Priority,
					Labels:            cloneStringSlice(entry.Issue.Labels),
					State:             entry.Issue.State,
					Stage:             entry.Stage,
					ExecutionProvider: entry.ExecutionProvider,
					Model:             entry.Model,
					ModelProvider:     entry.ModelProvider,
					SessionID:         entry.Session.SessionID,
					TurnCount:         entry.Session.TurnCount,
					LastEvent:         lastEvent,
					LastMessage:       lastMessage,
					StartedAt:         entry.StartedAt,
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
				RecentEvents: append([]api.EventDetail(nil), entry.RecentEvents...),
				LastError:    nil,
				Tracked:      map[string]interface{}{},
			}, true
		}
	}

	for _, entry := range o.state.RetryAttempts {
		if entry.Identifier == identifier {
			workspacePath := ""
			if o.workspaceMgr != nil {
				workspacePath = o.workspaceMgr.GetWorkspacePath(identifier)
			}
			return api.IssueDetailResponse{
				IssueIdentifier: identifier,
				IssueID:         entry.IssueID,
				Status:          "retrying",
				Workspace:       api.WorkspaceDetail{Path: workspacePath},
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
