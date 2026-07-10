package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/kbsartain/simphony/pkg/api"
	"gopkg.in/yaml.v3"
)

// LoadWorkflow reads and parses a WORKFLOW.md file.
// It extracts YAML front matter and the Markdown prompt template body.
func LoadWorkflow(path string) (*api.WorkflowDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", api.ErrMissingWorkflowFile, err)
		}
		return nil, fmt.Errorf("%s: %w", api.ErrWorkflowParseError, err)
	}

	content := string(data)
	var frontMatter string
	var promptTemplate string

	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content[3:], "---", 2)
		if len(parts) == 2 {
			frontMatter = strings.TrimSpace(parts[0])
			promptTemplate = strings.TrimSpace(parts[1])
		} else {
			return nil, fmt.Errorf("%s: unclosed front matter", api.ErrWorkflowParseError)
		}
	} else {
		promptTemplate = strings.TrimSpace(content)
	}

	var configMap map[string]interface{}
	if frontMatter != "" {
		if err := yaml.Unmarshal([]byte(frontMatter), &configMap); err != nil {
			return nil, fmt.Errorf("%s: %w", api.ErrWorkflowParseError, err)
		}
		if configMap == nil {
			configMap = make(map[string]interface{})
		}
	} else {
		configMap = make(map[string]interface{})
	}

	return &api.WorkflowDefinition{
		Config:         configMap,
		PromptTemplate: promptTemplate,
	}, nil
}

// SaveWorkflow writes a WorkflowDefinition back to a WORKFLOW.md file.
func SaveWorkflow(path string, def *api.WorkflowDefinition) error {
	if def == nil {
		return fmt.Errorf("%s: workflow definition is nil", api.ErrWorkflowParseError)
	}
	configMap := def.Config
	if configMap == nil {
		configMap = make(map[string]interface{})
	}

	frontMatter, err := yaml.Marshal(configMap)
	if err != nil {
		return fmt.Errorf("%s: %w", api.ErrWorkflowParseError, err)
	}

	perm := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	content := fmt.Sprintf("---\n%s---\n\n%s\n", string(frontMatter), strings.TrimSpace(def.PromptTemplate))
	if err := writeFileAtomic(path, []byte(content), perm); err != nil {
		return fmt.Errorf("write workflow: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// ResolveConfig builds a typed WorkflowConfig from a WorkflowDefinition.
// It applies defaults, resolves environment variable indirection, and validates values.
func ResolveConfig(def *api.WorkflowDefinition, workflowDir string) (*api.WorkflowConfig, error) {
	if def == nil {
		return nil, fmt.Errorf("%s: workflow definition is nil", api.ErrWorkflowParseError)
	}

	cfg := &api.WorkflowConfig{}

	trackerMap := getSubMap(def.Config, "tracker")
	if err := resolveTracker(trackerMap, cfg); err != nil {
		return nil, err
	}

	pipelineMap := getSubMap(def.Config, "pipeline")
	if err := resolvePipeline(pipelineMap, cfg); err != nil {
		return nil, err
	}
	reviewResolutionMap := getSubMap(def.Config, "review_resolution")
	if err := resolveReviewResolution(reviewResolutionMap, cfg); err != nil {
		return nil, err
	}
	if err := validateTrackerStateOverlaps(cfg); err != nil {
		return nil, err
	}

	pollingMap := getSubMap(def.Config, "polling")
	if err := resolvePolling(pollingMap, cfg); err != nil {
		return nil, err
	}

	workspaceMap := getSubMap(def.Config, "workspace")
	if err := resolveWorkspace(workspaceMap, cfg, workflowDir); err != nil {
		return nil, err
	}

	hooksMap := getSubMap(def.Config, "hooks")
	if err := resolveHooks(hooksMap, cfg); err != nil {
		return nil, err
	}

	agentMap := getSubMap(def.Config, "agent")
	if err := resolveAgent(agentMap, cfg); err != nil {
		return nil, err
	}

	codexMap := getSubMap(def.Config, "codex")
	if err := resolveCodex(codexMap, cfg); err != nil {
		return nil, err
	}

	claudeMap := getSubMap(def.Config, "claude")
	if err := resolveClaude(claudeMap, cfg); err != nil {
		return nil, err
	}

	agentRuntimeMap := getSubMap(def.Config, "agent_runtime")
	if err := resolveAgentRuntime(agentRuntimeMap, cfg); err != nil {
		return nil, err
	}

	serverMap := getSubMap(def.Config, "server")
	if err := resolveServer(serverMap, cfg); err != nil {
		return nil, err
	}

	verifyMap := getSubMap(def.Config, "verify")
	if err := resolveVerify(verifyMap, cfg); err != nil {
		return nil, err
	}

	githubMap := getSubMap(def.Config, "github")
	if err := resolveGitHubConfig(githubMap, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func getSubMap(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	sub, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	return sub
}

func getString(m map[string]interface{}, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

func getInt(m map[string]interface{}, key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func getBool(m map[string]interface{}, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func getStringSlice(m map[string]interface{}, key string) ([]string, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	if arr, ok := v.([]string); ok {
		return arr, true
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result, true
}

func getStringMap(m map[string]interface{}, key string) (map[string]string, bool) {
	if m == nil {
		return nil, false
	}
	raw, ok := m[key]
	if !ok {
		return nil, false
	}
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, false
	}
	result := make(map[string]string, len(rawMap))
	for k, v := range rawMap {
		s, ok := v.(string)
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		result[k] = ResolveEnvVar(strings.TrimSpace(s))
	}
	return result, true
}

func resolveTracker(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	kind, ok := getString(m, "kind")
	if !ok || kind == "" {
		return fmt.Errorf("%s: tracker.kind is required", api.ErrUnsupportedTrackerKind)
	}
	if kind != "linear" {
		return fmt.Errorf("%s: unsupported tracker kind %q", api.ErrUnsupportedTrackerKind, kind)
	}

	apiKeyRaw, ok := getString(m, "api_key")
	if !ok {
		return fmt.Errorf("%s: tracker.api_key is required", api.ErrMissingTrackerAPIKey)
	}
	if err := validateSecretRef("tracker.api_key", apiKeyRaw); err != nil {
		return err
	}
	apiKey := ResolveEnvVar(apiKeyRaw)
	if apiKey == "" {
		return fmt.Errorf("%s: tracker.api_key resolved to empty", api.ErrMissingTrackerAPIKey)
	}

	projectSlug, ok := getString(m, "project_slug")
	if !ok || projectSlug == "" {
		return fmt.Errorf("%s: tracker.project_slug is required for kind %q", api.ErrMissingTrackerProjectSlug, kind)
	}

	endpoint := "https://api.linear.app/graphql"
	if v, ok := getString(m, "endpoint"); ok && v != "" {
		endpoint = v
	}

	activeStates := []string{"Todo", "In Progress"}
	if v, ok := getStringSlice(m, "active_states"); ok && len(v) > 0 {
		activeStates = v
	}
	activeStates = appendUniqueStrings(activeStates)
	if len(activeStates) == 0 {
		return fmt.Errorf("%s: tracker.active_states must contain at least one state", api.ErrWorkflowParseError)
	}

	workingState := ""
	if v, ok := getString(m, "working_state"); ok {
		workingState = strings.TrimSpace(v)
	}
	if workingState != "" && !containsFold(activeStates, workingState) {
		activeStates = append(activeStates, workingState)
	}

	terminalStates := []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"}
	if v, ok := getStringSlice(m, "terminal_states"); ok && len(v) > 0 {
		terminalStates = v
	}
	terminalStates = appendUniqueStrings(terminalStates)
	if len(terminalStates) == 0 {
		return fmt.Errorf("%s: tracker.terminal_states must contain at least one state", api.ErrWorkflowParseError)
	}

	completionStates := []string{"In Review", "Review", "Done", "Completed"}
	if v, ok := getStringSlice(m, "completion_states"); ok && len(v) > 0 {
		completionStates = v
	}
	completionStates = appendUniqueStrings(completionStates, terminalStates...)

	cfg.Tracker = api.TrackerConfig{
		Kind:             kind,
		Endpoint:         endpoint,
		APIKey:           apiKey,
		ProjectSlug:      projectSlug,
		ActiveStates:     activeStates,
		WorkingState:     workingState,
		TerminalStates:   terminalStates,
		CompletionStates: completionStates,
	}
	return nil
}

func validateTrackerStateOverlaps(cfg *api.WorkflowConfig) error {
	if cfg == nil {
		return nil
	}
	for _, activeState := range cfg.Tracker.ActiveStates {
		if containsFold(cfg.Tracker.TerminalStates, activeState) {
			return fmt.Errorf("%s: tracker.active_states must not overlap tracker.terminal_states: %q", api.ErrWorkflowParseError, activeState)
		}
		if containsFold(cfg.Tracker.CompletionStates, activeState) &&
			!strings.EqualFold(strings.TrimSpace(activeState), strings.TrimSpace(cfg.Pipeline.ReviewState)) &&
			!strings.EqualFold(strings.TrimSpace(activeState), strings.TrimSpace(cfg.Pipeline.ReviewResolutionState)) {
			return fmt.Errorf("%s: tracker.completion_states must not overlap tracker.active_states except pipeline.review_state or pipeline.review_resolution_state: %q", api.ErrWorkflowParseError, activeState)
		}
	}
	return nil
}

func resolvePipeline(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	reviewState := "In Review"
	if v, ok := getString(m, "review_state"); ok && strings.TrimSpace(v) != "" {
		reviewState = strings.TrimSpace(v)
	}

	reviewResolutionState := "Review Resolution"
	if v, ok := getString(m, "review_resolution_state"); ok && strings.TrimSpace(v) != "" {
		reviewResolutionState = strings.TrimSpace(v)
	}

	mergeState := "Approved"
	if v, ok := getString(m, "merge_state"); ok && strings.TrimSpace(v) != "" {
		mergeState = strings.TrimSpace(v)
	}

	doneState := "Done"
	if v, ok := getString(m, "done_state"); ok && strings.TrimSpace(v) != "" {
		doneState = strings.TrimSpace(v)
	}

	codingStates := make([]string, 0, len(cfg.Tracker.ActiveStates))
	if v, ok := getStringSlice(m, "coding_states"); ok && len(v) > 0 {
		codingStates = appendUniqueStrings(v)
	} else {
		for _, state := range cfg.Tracker.ActiveStates {
			if strings.EqualFold(state, reviewState) || strings.EqualFold(state, reviewResolutionState) || strings.EqualFold(state, mergeState) {
				continue
			}
			codingStates = append(codingStates, state)
		}
		codingStates = appendUniqueStrings(codingStates)
	}
	if len(codingStates) == 0 {
		return fmt.Errorf("%s: pipeline.coding_states must contain at least one state", api.ErrWorkflowParseError)
	}

	cfg.Pipeline = api.PipelineConfig{
		ReviewState:           reviewState,
		ReviewResolutionState: reviewResolutionState,
		MergeState:            mergeState,
		DoneState:             doneState,
		CodingStates:          codingStates,
	}
	cfg.Tracker.ActiveStates = appendUniqueStrings(cfg.Tracker.ActiveStates, mergeState)
	cfg.Tracker.TerminalStates = appendUniqueStrings(cfg.Tracker.TerminalStates, doneState)
	return nil
}

func resolveReviewResolution(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	reviewResolution := api.ReviewResolutionConfig{
		Enabled:                   false,
		EscalationState:           cfg.Pipeline.ReviewState,
		MaxAttempts:               3,
		RequireChecksGreen:        true,
		RequireCodeReviewApproval: true,
		UnresolvedCommentPolicy:   "fix_or_explain",
		EscalateOn: []string{
			"security_risk",
			"schema_migration_risk",
			"destructive_data_change",
			"conflicting_reviews",
			"max_attempts_exceeded",
		},
	}
	if v, ok := getBool(m, "enabled"); ok {
		reviewResolution.Enabled = v
	}
	if v, ok := getString(m, "escalation_state"); ok && strings.TrimSpace(v) != "" {
		reviewResolution.EscalationState = strings.TrimSpace(v)
	}
	if v, ok := getInt(m, "max_attempts"); ok {
		reviewResolution.MaxAttempts = v
	}
	if reviewResolution.MaxAttempts <= 0 {
		return fmt.Errorf("%s: review_resolution.max_attempts must be positive, got %d", api.ErrWorkflowParseError, reviewResolution.MaxAttempts)
	}
	if v, ok := getBool(m, "require_checks_green"); ok {
		reviewResolution.RequireChecksGreen = v
	}
	if v, ok := getBool(m, "require_code_review_approval"); ok {
		reviewResolution.RequireCodeReviewApproval = v
	}
	if v, ok := getString(m, "unresolved_comment_policy"); ok && strings.TrimSpace(v) != "" {
		reviewResolution.UnresolvedCommentPolicy = strings.TrimSpace(v)
	}
	if v, ok := getStringSlice(m, "escalate_on"); ok && len(v) > 0 {
		reviewResolution.EscalateOn = appendUniqueStrings(v)
	}

	cfg.ReviewResolution = reviewResolution
	if reviewResolution.Enabled {
		cfg.Tracker.ActiveStates = appendUniqueStrings(cfg.Tracker.ActiveStates, cfg.Pipeline.ReviewResolutionState)
		cfg.Tracker.CompletionStates = appendUniqueStrings([]string{cfg.Pipeline.ReviewResolutionState}, cfg.Tracker.CompletionStates...)
	}
	return nil
}

func appendUniqueStrings(base []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(extras))
	out := make([]string, 0, len(base)+len(extras))
	for _, s := range append(base, extras...) {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func firstOverlappingString(left []string, right []string) string {
	seen := make(map[string]string, len(left))
	for _, s := range left {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			continue
		}
		seen[key] = s
	}
	for _, s := range right {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			continue
		}
		if original, ok := seen[key]; ok {
			return original
		}
	}
	return ""
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func resolvePolling(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	interval := 30000
	if v, ok := getInt(m, "interval_ms"); ok {
		interval = v
	}
	if interval <= 0 {
		return fmt.Errorf("%s: polling.interval_ms must be positive, got %d", api.ErrWorkflowParseError, interval)
	}
	cfg.Polling = api.PollingConfig{IntervalMs: interval}
	return nil
}

func resolveWorkspace(m map[string]interface{}, cfg *api.WorkflowConfig, workflowDir string) error {
	root := filepath.Join(os.TempDir(), "symphony_workspaces")
	if v, ok := getString(m, "root"); ok && v != "" {
		var err error
		root, err = resolvePath(v, workflowDir)
		if err != nil {
			return fmt.Errorf("%s: %w", api.ErrInvalidWorkspaceCWD, err)
		}
	} else {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("%s: %w", api.ErrInvalidWorkspaceCWD, err)
		}
	}

	mode := "directory"
	if v, ok := getString(m, "mode"); ok && v != "" {
		mode = v
	}
	if mode != "directory" && mode != "git_worktree" {
		return fmt.Errorf("%s: workspace.mode must be directory or git_worktree, got %q", api.ErrInvalidWorkspaceCWD, mode)
	}

	repo := ""
	if v, ok := getString(m, "repo"); ok && v != "" {
		var err error
		repo, err = resolvePath(v, workflowDir)
		if err != nil {
			return fmt.Errorf("%s: %w", api.ErrInvalidWorkspaceCWD, err)
		}
	}

	baseBranch := "main"
	if v, ok := getString(m, "base_branch"); ok && v != "" {
		baseBranch = v
	}

	branchPrefix := "github.com/kbsartain/simphony/"
	if v, ok := getString(m, "branch_prefix"); ok {
		branchPrefix = v
	}

	cleanupWorktrees := false
	if v, ok := getBool(m, "cleanup_worktrees"); ok {
		cleanupWorktrees = v
	}

	if mode == "git_worktree" && repo == "" {
		return fmt.Errorf("%s: workspace.repo is required when workspace.mode is git_worktree", api.ErrInvalidWorkspaceCWD)
	}

	cfg.Workspace = api.WorkspaceConfig{
		Root:             root,
		Mode:             mode,
		Repo:             repo,
		BaseBranch:       baseBranch,
		BranchPrefix:     branchPrefix,
		CleanupWorktrees: cleanupWorktrees,
	}
	return nil
}

func resolvePath(value string, workflowDir string) (string, error) {
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		rest := value[1:]
		rest = strings.TrimPrefix(rest, "\\")
		rest = strings.TrimPrefix(rest, "/")
		value = filepath.Join(home, rest)
	}

	if strings.Contains(value, "$") {
		value = os.ExpandEnv(value)
	}

	if !filepath.IsAbs(value) {
		value = filepath.Join(workflowDir, value)
	}

	return filepath.Abs(value)
}

func resolveHooks(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	hooks := api.HooksConfig{
		TimeoutMs: 60000,
	}

	if v, ok := getString(m, "after_create"); ok {
		hooks.AfterCreate = &v
	}
	if v, ok := getString(m, "before_run"); ok {
		hooks.BeforeRun = &v
	}
	if v, ok := getString(m, "after_run"); ok {
		hooks.AfterRun = &v
	}
	if v, ok := getString(m, "before_remove"); ok {
		hooks.BeforeRemove = &v
	}

	if v, ok := getInt(m, "timeout_ms"); ok {
		if v <= 0 {
			return fmt.Errorf("%s: hooks.timeout_ms must be positive, got %d", api.ErrWorkflowParseError, v)
		}
		hooks.TimeoutMs = v
	}

	cfg.Hooks = hooks
	return nil
}

func resolveAgent(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	maxConcurrent := 10
	if v, ok := getInt(m, "max_concurrent_agents"); ok {
		maxConcurrent = v
	}
	if maxConcurrent <= 0 {
		return fmt.Errorf("%s: agent.max_concurrent_agents must be positive, got %d", api.ErrWorkflowParseError, maxConcurrent)
	}

	maxTurns := 20
	if v, ok := getInt(m, "max_turns"); ok {
		maxTurns = v
	}
	if maxTurns <= 0 {
		return fmt.Errorf("%s: agent.max_turns must be positive, got %d", api.ErrWorkflowParseError, maxTurns)
	}

	maxRetryBackoff := 300000
	if v, ok := getInt(m, "max_retry_backoff_ms"); ok {
		maxRetryBackoff = v
	}
	if maxRetryBackoff <= 0 {
		return fmt.Errorf("%s: agent.max_retry_backoff_ms must be positive, got %d", api.ErrWorkflowParseError, maxRetryBackoff)
	}

	byState := make(map[string]int)
	if m != nil {
		if raw, ok := m["max_concurrent_agents_by_state"]; ok {
			if rawMap, ok := raw.(map[string]interface{}); ok {
				for k, v := range rawMap {
					val, ok := toInt(v)
					if !ok || val <= 0 {
						continue
					}
					byState[strings.ToLower(k)] = val
				}
			}
		}
	}

	cfg.Agent = api.AgentConfig{
		MaxConcurrentAgents:        maxConcurrent,
		MaxTurns:                   maxTurns,
		MaxRetryBackoffMs:          maxRetryBackoff,
		MaxConcurrentAgentsByState: byState,
	}
	return nil
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func resolveCodex(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	runtime := defaultRuntimeConfig("codex")
	if err := applyRuntimeCommon(m, &runtime, "codex"); err != nil {
		return err
	}
	if runtime.Command == "" {
		return fmt.Errorf("%s: codex.command must be non-empty", api.ErrCodexNotFound)
	}
	skills, err := getSkillRefs(m, "skills")
	if err != nil {
		return fmt.Errorf("%s: codex.skills %w", api.ErrWorkflowParseError, err)
	}
	if len(skills) > 0 {
		runtime.Skills = skills
	}
	stageOverrides, err := resolveCodexStageOverrides(m)
	if err != nil {
		return err
	}
	runtime.StageOverrides = stageOverrides
	cfg.Codex = runtime
	return nil
}

func resolveClaude(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	runtime := defaultRuntimeConfig("claude")
	if err := applyRuntimeCommon(m, &runtime, "claude"); err != nil {
		return err
	}
	if v, ok := getString(m, "permission_mode"); ok && strings.TrimSpace(v) != "" {
		runtime.PermissionMode = strings.TrimSpace(v)
	}
	if v, ok := getStringSlice(m, "allowed_tools"); ok {
		runtime.AllowedTools = v
	}
	if v, ok := getStringSlice(m, "disallowed_tools"); ok {
		runtime.DisallowedTools = v
	}
	if v, ok := getStringSlice(m, "setting_sources"); ok {
		runtime.SettingSources = v
	}
	cfg.Claude = runtime
	return nil
}

func resolveAgentRuntime(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	provider := "codex"
	if v, ok := getString(m, "provider"); ok && strings.TrimSpace(v) != "" {
		provider = strings.ToLower(strings.TrimSpace(v))
	}
	switch provider {
	case "codex":
		cfg.AgentRuntime = cfg.Codex
	case "claude":
		cfg.AgentRuntime = cfg.Claude
	default:
		return fmt.Errorf("%s: agent_runtime.provider must be codex or claude, got %q", api.ErrWorkflowParseError, provider)
	}
	cfg.AgentRuntime.Provider = provider
	if err := applyRuntimeCommon(m, &cfg.AgentRuntime, "agent_runtime"); err != nil {
		return err
	}
	if provider == "claude" {
		if v, ok := getString(m, "permission_mode"); ok && strings.TrimSpace(v) != "" {
			cfg.AgentRuntime.PermissionMode = strings.TrimSpace(v)
		}
		if v, ok := getStringSlice(m, "allowed_tools"); ok {
			cfg.AgentRuntime.AllowedTools = v
		}
		if v, ok := getStringSlice(m, "disallowed_tools"); ok {
			cfg.AgentRuntime.DisallowedTools = v
		}
		if v, ok := getStringSlice(m, "setting_sources"); ok {
			cfg.AgentRuntime.SettingSources = v
		}
	}
	stageOverrides, err := resolveAgentStageOverrides(m, "agent_runtime")
	if err != nil {
		return err
	}
	if stageOverrides != nil {
		cfg.AgentRuntime.StageOverrides = stageOverrides
	}
	return nil
}

func defaultRuntimeConfig(provider string) api.AgentRuntimeConfig {
	runtime := api.AgentRuntimeConfig{
		Provider:          provider,
		ApprovalPolicy:    "auto",
		ThreadSandbox:     "none",
		TurnSandboxPolicy: "none",
		TurnTimeoutMs:     3600000,
		ReadTimeoutMs:     5000,
		StallTimeoutMs:    300000,
	}
	switch provider {
	case "codex":
		runtime.Command = "codex app-server"
	case "claude":
		runtime.PermissionMode = "acceptEdits"
	}
	return runtime
}

func applyRuntimeCommon(m map[string]interface{}, runtime *api.AgentRuntimeConfig, section string) error {
	if runtime == nil {
		return nil
	}
	if v, ok := getString(m, "provider"); ok && strings.TrimSpace(v) != "" {
		runtime.Provider = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := getString(m, "command"); ok {
		runtime.Command = strings.TrimSpace(v)
	}
	if v, ok := getString(m, "model"); ok {
		runtime.Model = strings.TrimSpace(v)
	}
	if v, ok := getString(m, "model_provider"); ok {
		runtime.ModelProvider = strings.TrimSpace(v)
	}
	if v, ok := getString(m, "reasoning_effort"); ok {
		reasoningEffort, err := normalizeReasoningEffort(v)
		if err != nil {
			return fmt.Errorf("%s: %s.reasoning_effort %w", api.ErrWorkflowParseError, section, err)
		}
		runtime.ReasoningEffort = reasoningEffort
	}
	if v, ok := getString(m, "endpoint_url"); ok {
		runtime.EndpointURL = strings.TrimSpace(ResolveEnvVar(v))
	}
	if v, ok := getString(m, "api_key"); ok {
		if err := validateSecretRef(section+".api_key", v); err != nil {
			return err
		}
		runtime.APIKey = ResolveEnvVar(strings.TrimSpace(v))
		runtime.APIKeyConfigured = strings.TrimSpace(v) != ""
	}
	if v, ok := getString(m, "auth_token"); ok {
		if err := validateSecretRef(section+".auth_token", v); err != nil {
			return err
		}
		runtime.AuthToken = ResolveEnvVar(strings.TrimSpace(v))
		runtime.AuthTokenConfigured = strings.TrimSpace(v) != ""
	}
	if env, ok := getStringMap(m, "env"); ok {
		if runtime.Env == nil {
			runtime.Env = make(map[string]string, len(env))
		}
		for k, v := range env {
			runtime.Env[k] = v
		}
	}
	if v, ok := getString(m, "approval_policy"); ok && v != "" {
		runtime.ApprovalPolicy = strings.TrimSpace(v)
	}
	if v, ok := getString(m, "thread_sandbox"); ok && v != "" {
		runtime.ThreadSandbox = strings.TrimSpace(v)
	}
	if v, ok := getString(m, "turn_sandbox_policy"); ok && v != "" {
		runtime.TurnSandboxPolicy = strings.TrimSpace(v)
	}
	if v, ok := getInt(m, "turn_timeout_ms"); ok {
		runtime.TurnTimeoutMs = v
	}
	if runtime.TurnTimeoutMs <= 0 {
		return fmt.Errorf("%s: %s.turn_timeout_ms must be positive, got %d", api.ErrWorkflowParseError, section, runtime.TurnTimeoutMs)
	}
	if v, ok := getInt(m, "read_timeout_ms"); ok {
		runtime.ReadTimeoutMs = v
	}
	if runtime.ReadTimeoutMs <= 0 {
		return fmt.Errorf("%s: %s.read_timeout_ms must be positive, got %d", api.ErrWorkflowParseError, section, runtime.ReadTimeoutMs)
	}
	if v, ok := getInt(m, "stall_timeout_ms"); ok {
		runtime.StallTimeoutMs = v
	}
	if runtime.StallTimeoutMs < 0 {
		return fmt.Errorf("%s: %s.stall_timeout_ms must be non-negative, got %d", api.ErrWorkflowParseError, section, runtime.StallTimeoutMs)
	}
	return nil
}

func resolveCodexStageOverrides(m map[string]interface{}) (map[string]api.CodexStageOverride, error) {
	return resolveAgentStageOverrides(m, "codex")
}

func resolveAgentStageOverrides(m map[string]interface{}, section string) (map[string]api.AgentStageOverride, error) {
	if m == nil {
		return nil, nil
	}
	raw, ok := m["stage_overrides"]
	if !ok {
		return nil, nil
	}
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s: %s.stage_overrides must be a map", api.ErrWorkflowParseError, section)
	}
	overrides := make(map[string]api.AgentStageOverride)
	for stageName, rawStage := range rawMap {
		stageKey := strings.ToLower(strings.TrimSpace(stageName))
		if stageKey == "" {
			continue
		}
		stageMap, ok := rawStage.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%s: %s.stage_overrides.%s must be a map", api.ErrWorkflowParseError, section, stageName)
		}
		override := api.AgentStageOverride{}
		if v, ok := getString(stageMap, "provider"); ok && strings.TrimSpace(v) != "" {
			override.Provider = strings.ToLower(strings.TrimSpace(v))
			switch override.Provider {
			case "codex", "claude":
			default:
				return nil, fmt.Errorf("%s: %s.stage_overrides.%s.provider must be codex or claude, got %q", api.ErrWorkflowParseError, section, stageName, override.Provider)
			}
		}
		if v, ok := getString(stageMap, "command"); ok {
			override.Command = strings.TrimSpace(v)
		}
		if v, ok := getString(stageMap, "model"); ok {
			override.Model = strings.TrimSpace(v)
		}
		if v, ok := getString(stageMap, "model_provider"); ok {
			override.ModelProvider = strings.TrimSpace(v)
		}
		if v, ok := getString(stageMap, "reasoning_effort"); ok {
			reasoningEffort, err := normalizeReasoningEffort(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %s.stage_overrides.%s.reasoning_effort %w", api.ErrWorkflowParseError, section, stageName, err)
			}
			override.ReasoningEffort = reasoningEffort
		}
		if v, ok := getString(stageMap, "endpoint_url"); ok {
			override.EndpointURL = strings.TrimSpace(ResolveEnvVar(v))
		}
		if v, ok := getString(stageMap, "api_key"); ok {
			if err := validateSecretRef(fmt.Sprintf("%s.stage_overrides.%s.api_key", section, stageName), v); err != nil {
				return nil, err
			}
			override.APIKey = ResolveEnvVar(strings.TrimSpace(v))
			override.APIKeyConfigured = strings.TrimSpace(v) != ""
		}
		if v, ok := getString(stageMap, "auth_token"); ok {
			if err := validateSecretRef(fmt.Sprintf("%s.stage_overrides.%s.auth_token", section, stageName), v); err != nil {
				return nil, err
			}
			override.AuthToken = ResolveEnvVar(strings.TrimSpace(v))
			override.AuthTokenConfigured = strings.TrimSpace(v) != ""
		}
		if env, ok := getStringMap(stageMap, "env"); ok {
			override.Env = make(map[string]string, len(env))
			for k, v := range env {
				override.Env[k] = v
			}
		}
		skills, err := getSkillRefs(stageMap, "skills")
		if err != nil {
			return nil, fmt.Errorf("%s: %s.stage_overrides.%s.skills %w", api.ErrWorkflowParseError, section, stageName, err)
		}
		override.Skills = skills
		if v, ok := getStringSlice(stageMap, "allowed_tools"); ok {
			override.AllowedTools = v
		}
		if v, ok := getStringSlice(stageMap, "disallowed_tools"); ok {
			override.DisallowedTools = v
		}
		if v, ok := getString(stageMap, "permission_mode"); ok {
			override.PermissionMode = strings.TrimSpace(v)
		}
		if v, ok := getStringSlice(stageMap, "setting_sources"); ok {
			override.SettingSources = v
		}
		if v, ok := getString(stageMap, "approval_policy"); ok {
			override.ApprovalPolicy = strings.TrimSpace(v)
		}
		if v, ok := getString(stageMap, "thread_sandbox"); ok {
			override.ThreadSandbox = strings.TrimSpace(v)
		}
		if v, ok := getString(stageMap, "turn_sandbox_policy"); ok {
			override.TurnSandboxPolicy = strings.TrimSpace(v)
		}
		if override.Provider == "" && override.Command == "" && override.Model == "" && override.ModelProvider == "" && override.ReasoningEffort == "" && override.EndpointURL == "" && !override.APIKeyConfigured && !override.AuthTokenConfigured && len(override.Env) == 0 && len(override.Skills) == 0 && len(override.AllowedTools) == 0 && len(override.DisallowedTools) == 0 && override.PermissionMode == "" && len(override.SettingSources) == 0 && override.ApprovalPolicy == "" && override.ThreadSandbox == "" && override.TurnSandboxPolicy == "" {
			continue
		}
		overrides[stageKey] = override
	}
	if len(overrides) == 0 {
		return nil, nil
	}
	return overrides, nil
}

func normalizeReasoningEffort(value string) (string, error) {
	raw := strings.TrimSpace(value)
	normalized := strings.ToLower(raw)
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	switch normalized {
	case "":
		return "", nil
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return normalized, nil
	case "max":
		return "xhigh", nil
	default:
		return "", fmt.Errorf("must be one of none, minimal, low, medium, high, xhigh, or max, got %q", raw)
	}
}

func getSkillRefs(m map[string]interface{}, key string) ([]api.CodexSkillRef, error) {
	if m == nil {
		return nil, nil
	}
	raw, ok := m[key]
	if !ok {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("must be a list")
	}
	skills := make([]api.CodexSkillRef, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		skill, err := skillRefFromValue(item)
		if err != nil {
			return nil, err
		}
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Path = strings.TrimSpace(skill.Path)
		if skill.Name == "" && skill.Path == "" {
			continue
		}
		if skill.Name == "" {
			skill.Name = filepath.Base(skill.Path)
		}
		seenKey := strings.ToLower(skill.Name) + "\x00" + strings.ToLower(filepath.Clean(skill.Path))
		if _, ok := seen[seenKey]; ok {
			continue
		}
		seen[seenKey] = struct{}{}
		skills = append(skills, skill)
	}
	return skills, nil
}

func skillRefFromValue(value interface{}) (api.CodexSkillRef, error) {
	switch v := value.(type) {
	case string:
		return api.CodexSkillRef{Name: strings.TrimSpace(v)}, nil
	case map[string]interface{}:
		name := ""
		if rawName, ok := v["name"].(string); ok {
			name = strings.TrimSpace(rawName)
		}
		path := ""
		if rawPath, ok := v["path"].(string); ok {
			path = strings.TrimSpace(rawPath)
		}
		if name == "" && path == "" {
			return api.CodexSkillRef{}, fmt.Errorf("entries must include name or path")
		}
		return api.CodexSkillRef{Name: name, Path: path}, nil
	default:
		return api.CodexSkillRef{}, fmt.Errorf("entries must be strings or maps with name/path")
	}
}

func resolveServer(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	if m == nil {
		return nil
	}
	port, ok := getInt(m, "port")
	if !ok {
		return nil
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s: server.port must be between 1 and 65535, got %d", api.ErrWorkflowParseError, port)
	}
	cfg.Server = &api.ServerConfig{Port: port}
	return nil
}

func resolveVerify(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	verify := api.VerifyConfig{TimeoutMs: 600000}
	if v, ok := getStringSlice(m, "commands"); ok {
		verify.Commands = v
	}
	if v, ok := getInt(m, "timeout_ms"); ok {
		if v <= 0 {
			return fmt.Errorf("%s: verify.timeout_ms must be positive, got %d", api.ErrWorkflowParseError, v)
		}
		verify.TimeoutMs = v
	}
	cfg.Verify = verify
	return nil
}

func resolveGitHubConfig(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	github := api.GitHubConfig{
		MergeMethod:               "squash",
		ChecksTimeoutMs:           1800000,
		ChecksRegistrationGraceMs: 60000,
	}
	if v, ok := getBool(m, "enabled"); ok {
		github.Enabled = v
	}
	if v, ok := getString(m, "merge_method"); ok && strings.TrimSpace(v) != "" {
		method := strings.ToLower(strings.TrimSpace(v))
		if method != "merge" && method != "squash" && method != "rebase" {
			return fmt.Errorf("%s: github.merge_method must be merge, squash, or rebase, got %q", api.ErrWorkflowParseError, method)
		}
		github.MergeMethod = method
	}
	if v, ok := getInt(m, "checks_timeout_ms"); ok {
		if v <= 0 {
			return fmt.Errorf("%s: github.checks_timeout_ms must be positive, got %d", api.ErrWorkflowParseError, v)
		}
		github.ChecksTimeoutMs = v
	}
	if v, ok := getInt(m, "checks_registration_grace_ms"); ok {
		if v < 0 {
			return fmt.Errorf("%s: github.checks_registration_grace_ms must be non-negative, got %d", api.ErrWorkflowParseError, v)
		}
		github.ChecksRegistrationGraceMs = v
	}
	cfg.GitHub = github
	return nil
}

// ResolveEnvVar replaces $VAR_NAME with the environment variable value.
func ResolveEnvVar(value string) string {
	if strings.HasPrefix(value, "$") {
		return os.Getenv(value[1:])
	}
	return value
}

// validateSecretRef rejects literal values in known secret fields. Secrets
// must be referenced through environment variables so credentials cannot be
// silently committed to a workflow or registry file.
func validateSecretRef(fieldPath, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "$") {
		return nil
	}
	return fmt.Errorf("%s: %s must reference an environment variable (start with $), but a literal value was provided", api.ErrLiteralSecret, fieldPath)
}

// DefaultWorkflowPath returns the default WORKFLOW.md path in the current working directory.
func DefaultWorkflowPath() string {
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "WORKFLOW.md")
	}
	return "WORKFLOW.md"
}

// WatchWorkflow watches a WORKFLOW.md file for changes and calls onChange when modified.
// It handles renames/re-creates gracefully (common with editor temp+rename behavior).
func WatchWorkflow(path string, onChange func()) (*fsnotify.Watcher, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = filepath.Clean(absPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return nil, err
	}

	// Best-effort watch the file itself for in-place modifications.
	_ = watcher.Add(path)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				eventPath, err := filepath.Abs(event.Name)
				if err != nil {
					continue
				}
				if filepath.Clean(eventPath) != path {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					onChange()
					_ = watcher.Add(path)
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return watcher, nil
}
