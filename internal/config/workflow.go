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

	serverMap := getSubMap(def.Config, "server")
	if err := resolveServer(serverMap, cfg); err != nil {
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
	if overlap := firstOverlappingString(activeStates, completionStates); overlap != "" {
		return fmt.Errorf("%s: tracker.completion_states must not overlap tracker.active_states: %q", api.ErrWorkflowParseError, overlap)
	}

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

func resolvePipeline(m map[string]interface{}, cfg *api.WorkflowConfig) error {
	reviewState := "In Review"
	if v, ok := getString(m, "review_state"); ok && strings.TrimSpace(v) != "" {
		reviewState = strings.TrimSpace(v)
	}

	mergeState := "Merge and Commit"
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
			if strings.EqualFold(state, reviewState) || strings.EqualFold(state, mergeState) {
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
		ReviewState:  reviewState,
		MergeState:   mergeState,
		DoneState:    doneState,
		CodingStates: codingStates,
	}
	cfg.Tracker.ActiveStates = appendUniqueStrings(cfg.Tracker.ActiveStates, mergeState)
	cfg.Tracker.TerminalStates = appendUniqueStrings(cfg.Tracker.TerminalStates, doneState)
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
	command := "codex app-server"
	if v, ok := getString(m, "command"); ok {
		command = v
	}
	if command == "" {
		return fmt.Errorf("%s: codex.command must be non-empty", api.ErrCodexNotFound)
	}

	model := ""
	if v, ok := getString(m, "model"); ok {
		model = strings.TrimSpace(v)
	}

	modelProvider := ""
	if v, ok := getString(m, "model_provider"); ok {
		modelProvider = strings.TrimSpace(v)
	}

	approvalPolicy := "auto"
	if v, ok := getString(m, "approval_policy"); ok && v != "" {
		approvalPolicy = v
	}

	threadSandbox := "none"
	if v, ok := getString(m, "thread_sandbox"); ok && v != "" {
		threadSandbox = v
	}

	turnSandboxPolicy := "none"
	if v, ok := getString(m, "turn_sandbox_policy"); ok && v != "" {
		turnSandboxPolicy = v
	}

	turnTimeout := 3600000
	if v, ok := getInt(m, "turn_timeout_ms"); ok {
		turnTimeout = v
	}
	if turnTimeout <= 0 {
		return fmt.Errorf("%s: codex.turn_timeout_ms must be positive, got %d", api.ErrWorkflowParseError, turnTimeout)
	}

	readTimeout := 5000
	if v, ok := getInt(m, "read_timeout_ms"); ok {
		readTimeout = v
	}
	if readTimeout <= 0 {
		return fmt.Errorf("%s: codex.read_timeout_ms must be positive, got %d", api.ErrWorkflowParseError, readTimeout)
	}

	stallTimeout := 300000
	if v, ok := getInt(m, "stall_timeout_ms"); ok {
		stallTimeout = v
	}
	if stallTimeout < 0 {
		return fmt.Errorf("%s: codex.stall_timeout_ms must be non-negative, got %d", api.ErrWorkflowParseError, stallTimeout)
	}

	cfg.Codex = api.CodexConfig{
		Command:           command,
		Model:             model,
		ModelProvider:     modelProvider,
		ApprovalPolicy:    approvalPolicy,
		ThreadSandbox:     threadSandbox,
		TurnSandboxPolicy: turnSandboxPolicy,
		TurnTimeoutMs:     turnTimeout,
		ReadTimeoutMs:     readTimeout,
		StallTimeoutMs:    stallTimeout,
	}
	return nil
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

// ResolveEnvVar replaces $VAR_NAME with the environment variable value.
func ResolveEnvVar(value string) string {
	if strings.HasPrefix(value, "$") {
		return os.Getenv(value[1:])
	}
	return value
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
