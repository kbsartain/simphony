package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
	"simphony/pkg/api"
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
	resolveServer(serverMap, cfg)

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

	terminalStates := []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"}
	if v, ok := getStringSlice(m, "terminal_states"); ok && len(v) > 0 {
		terminalStates = v
	}

	cfg.Tracker = api.TrackerConfig{
		Kind:           kind,
		Endpoint:       endpoint,
		APIKey:         apiKey,
		ProjectSlug:    projectSlug,
		ActiveStates:   activeStates,
		TerminalStates: terminalStates,
	}
	return nil
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
	cfg.Workspace = api.WorkspaceConfig{Root: root}
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

	cfg.Codex = api.CodexConfig{
		Command:           command,
		ApprovalPolicy:    approvalPolicy,
		ThreadSandbox:     threadSandbox,
		TurnSandboxPolicy: turnSandboxPolicy,
		TurnTimeoutMs:     turnTimeout,
		ReadTimeoutMs:     readTimeout,
		StallTimeoutMs:    stallTimeout,
	}
	return nil
}

func resolveServer(m map[string]interface{}, cfg *api.WorkflowConfig) {
	if m == nil {
		return
	}
	port, ok := getInt(m, "port")
	if !ok {
		return
	}
	cfg.Server = &api.ServerConfig{Port: port}
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
