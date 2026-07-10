package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kbsartain/simphony/internal/prompt"
	"github.com/kbsartain/simphony/pkg/api"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SIM_TEST_TRACKER_KEY", "test-key")
	os.Exit(m.Run())
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

func TestResolveConfig_FullDefaults(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Tracker.Kind != "linear" {
		t.Errorf("tracker.kind = %q, want linear", cfg.Tracker.Kind)
	}
	if cfg.Tracker.Endpoint != "https://api.linear.app/graphql" {
		t.Errorf("tracker.endpoint = %q, want default", cfg.Tracker.Endpoint)
	}
	if cfg.Tracker.APIKey != "test-key" {
		t.Errorf("tracker.api_key = %q, want test-key", cfg.Tracker.APIKey)
	}
	if cfg.Tracker.ProjectSlug != "proj" {
		t.Errorf("tracker.project_slug = %q, want proj", cfg.Tracker.ProjectSlug)
	}
	wantActive := []string{"Todo", "In Progress", "Approved"}
	if !slicesEqual(cfg.Tracker.ActiveStates, wantActive) {
		t.Errorf("tracker.active_states = %v, want %v", cfg.Tracker.ActiveStates, wantActive)
	}
	if cfg.Tracker.WorkingState != "" {
		t.Errorf("tracker.working_state = %q, want empty", cfg.Tracker.WorkingState)
	}
	wantTerminal := []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"}
	if !slicesEqual(cfg.Tracker.TerminalStates, wantTerminal) {
		t.Errorf("tracker.terminal_states = %v, want %v", cfg.Tracker.TerminalStates, wantTerminal)
	}
	if cfg.Pipeline.ReviewState != "In Review" {
		t.Errorf("pipeline.review_state = %q, want In Review", cfg.Pipeline.ReviewState)
	}
	if cfg.Pipeline.MergeState != "Approved" {
		t.Errorf("pipeline.merge_state = %q, want Approved", cfg.Pipeline.MergeState)
	}
	if cfg.Pipeline.DoneState != "Done" {
		t.Errorf("pipeline.done_state = %q, want Done", cfg.Pipeline.DoneState)
	}
	if !slicesEqual(cfg.Pipeline.CodingStates, []string{"Todo", "In Progress"}) {
		t.Errorf("pipeline.coding_states = %v, want [Todo In Progress]", cfg.Pipeline.CodingStates)
	}

	if cfg.Polling.IntervalMs != 30000 {
		t.Errorf("polling.interval_ms = %d, want 30000", cfg.Polling.IntervalMs)
	}

	if !strings.Contains(cfg.Workspace.Root, "symphony_workspaces") {
		t.Errorf("workspace.root = %q, should contain symphony_workspaces", cfg.Workspace.Root)
	}
	if !filepath.IsAbs(cfg.Workspace.Root) {
		t.Errorf("workspace.root = %q, want absolute", cfg.Workspace.Root)
	}
	if cfg.Workspace.Mode != "directory" {
		t.Errorf("workspace.mode = %q, want directory", cfg.Workspace.Mode)
	}
	if cfg.Workspace.BaseBranch != "main" {
		t.Errorf("workspace.base_branch = %q, want main", cfg.Workspace.BaseBranch)
	}
	if cfg.Workspace.BranchPrefix != "github.com/kbsartain/simphony/" {
		t.Errorf("workspace.branch_prefix = %q, want simphony/", cfg.Workspace.BranchPrefix)
	}
	if cfg.Workspace.CleanupWorktrees {
		t.Error("workspace.cleanup_worktrees = true, want false")
	}

	if cfg.Hooks.AfterCreate != nil {
		t.Errorf("hooks.after_create = %v, want nil", cfg.Hooks.AfterCreate)
	}
	if cfg.Hooks.BeforeRun != nil {
		t.Errorf("hooks.before_run = %v, want nil", cfg.Hooks.BeforeRun)
	}
	if cfg.Hooks.AfterRun != nil {
		t.Errorf("hooks.after_run = %v, want nil", cfg.Hooks.AfterRun)
	}
	if cfg.Hooks.BeforeRemove != nil {
		t.Errorf("hooks.before_remove = %v, want nil", cfg.Hooks.BeforeRemove)
	}
	if cfg.Hooks.TimeoutMs != 60000 {
		t.Errorf("hooks.timeout_ms = %d, want 60000", cfg.Hooks.TimeoutMs)
	}

	if cfg.Agent.MaxConcurrentAgents != 10 {
		t.Errorf("agent.max_concurrent_agents = %d, want 10", cfg.Agent.MaxConcurrentAgents)
	}
	if cfg.Agent.MaxTurns != 20 {
		t.Errorf("agent.max_turns = %d, want 20", cfg.Agent.MaxTurns)
	}
	if cfg.Agent.MaxRetryBackoffMs != 300000 {
		t.Errorf("agent.max_retry_backoff_ms = %d, want 300000", cfg.Agent.MaxRetryBackoffMs)
	}
	if len(cfg.Agent.MaxConcurrentAgentsByState) != 0 {
		t.Errorf("agent.max_concurrent_agents_by_state = %v, want empty", cfg.Agent.MaxConcurrentAgentsByState)
	}

	if cfg.Codex.Command != "codex app-server" {
		t.Errorf("codex.command = %q, want default", cfg.Codex.Command)
	}
	if cfg.AgentRuntime.Provider != "codex" {
		t.Errorf("agent_runtime.provider = %q, want codex", cfg.AgentRuntime.Provider)
	}
	if cfg.AgentRuntime.Command != "codex app-server" {
		t.Errorf("agent_runtime.command = %q, want default codex command", cfg.AgentRuntime.Command)
	}
	if cfg.Codex.ApprovalPolicy != "auto" {
		t.Errorf("codex.approval_policy = %q, want auto", cfg.Codex.ApprovalPolicy)
	}
	if cfg.Codex.ReasoningEffort != "" {
		t.Errorf("codex.reasoning_effort = %q, want empty", cfg.Codex.ReasoningEffort)
	}
	if len(cfg.Codex.StageOverrides) != 0 {
		t.Errorf("codex.stage_overrides = %v, want empty", cfg.Codex.StageOverrides)
	}
	if len(cfg.Codex.Skills) != 0 {
		t.Errorf("codex.skills = %v, want empty", cfg.Codex.Skills)
	}
	if cfg.Codex.ThreadSandbox != "none" {
		t.Errorf("codex.thread_sandbox = %q, want none", cfg.Codex.ThreadSandbox)
	}
	if cfg.Codex.TurnSandboxPolicy != "none" {
		t.Errorf("codex.turn_sandbox_policy = %q, want none", cfg.Codex.TurnSandboxPolicy)
	}
	if cfg.Codex.TurnTimeoutMs != 3600000 {
		t.Errorf("codex.turn_timeout_ms = %d, want 3600000", cfg.Codex.TurnTimeoutMs)
	}
	if cfg.Codex.ReadTimeoutMs != 5000 {
		t.Errorf("codex.read_timeout_ms = %d, want 5000", cfg.Codex.ReadTimeoutMs)
	}
	if cfg.Codex.StallTimeoutMs != 300000 {
		t.Errorf("codex.stall_timeout_ms = %d, want 300000", cfg.Codex.StallTimeoutMs)
	}

	if cfg.Server != nil {
		t.Errorf("server = %v, want nil", cfg.Server)
	}
}

func TestResolveConfig_ClaudeAgentRuntime(t *testing.T) {
	t.Setenv("TEST_ANTHROPIC_KEY", "resolved-anthropic-key")
	t.Setenv("TEST_ANTHROPIC_BASE_URL", "https://anthropic-compatible.example/v1")

	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"agent_runtime": map[string]interface{}{
				"provider":        "claude",
				"model":           "claude-sonnet-4",
				"endpoint_url":    "$TEST_ANTHROPIC_BASE_URL",
				"api_key":         "$TEST_ANTHROPIC_KEY",
				"permission_mode": "acceptEdits",
				"allowed_tools":   []interface{}{"Read", "Edit", "Bash"},
				"env": map[string]interface{}{
					"ANTHROPIC_SMALL_FAST_MODEL": "claude-haiku",
				},
			},
			"claude": map[string]interface{}{
				"command":         "node ./simphony-claude-shim.mjs",
				"setting_sources": []interface{}{"project", "local"},
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentRuntime.Provider != "claude" {
		t.Fatalf("provider = %q, want claude", cfg.AgentRuntime.Provider)
	}
	if cfg.AgentRuntime.Command != "node ./simphony-claude-shim.mjs" {
		t.Fatalf("command = %q, want claude command", cfg.AgentRuntime.Command)
	}
	if cfg.AgentRuntime.EndpointURL != "https://anthropic-compatible.example/v1" {
		t.Fatalf("endpoint_url = %q, want env-resolved endpoint", cfg.AgentRuntime.EndpointURL)
	}
	if cfg.AgentRuntime.APIKey != "resolved-anthropic-key" || !cfg.AgentRuntime.APIKeyConfigured {
		t.Fatalf("api_key was not resolved/configured")
	}
	if cfg.AgentRuntime.PermissionMode != "acceptEdits" {
		t.Fatalf("permission_mode = %q, want acceptEdits", cfg.AgentRuntime.PermissionMode)
	}
	if !slicesEqual(cfg.AgentRuntime.AllowedTools, []string{"Read", "Edit", "Bash"}) {
		t.Fatalf("allowed_tools = %v, want Read/Edit/Bash", cfg.AgentRuntime.AllowedTools)
	}
	if !slicesEqual(cfg.AgentRuntime.SettingSources, []string{"project", "local"}) {
		t.Fatalf("setting_sources = %v, want project/local", cfg.AgentRuntime.SettingSources)
	}
	if cfg.AgentRuntime.Env["ANTHROPIC_SMALL_FAST_MODEL"] != "claude-haiku" {
		t.Fatalf("env override not preserved: %v", cfg.AgentRuntime.Env)
	}
}

func TestResolveConfig_EnvVarResolution(t *testing.T) {
	os.Setenv("TEST_LINEAR_KEY", "resolved-key")
	defer os.Unsetenv("TEST_LINEAR_KEY")

	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$TEST_LINEAR_KEY",
				"project_slug": "proj",
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tracker.APIKey != "resolved-key" {
		t.Errorf("tracker.api_key = %q, want resolved-key", cfg.Tracker.APIKey)
	}
}

func TestResolveConfig_ModelAndPipeline(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":          "linear",
				"api_key":       "$SIM_TEST_TRACKER_KEY",
				"project_slug":  "proj",
				"active_states": []interface{}{"Ready", "Coding"},
			},
			"pipeline": map[string]interface{}{
				"review_state":            "Reviewing",
				"review_resolution_state": "Review Resolution",
				"merge_state":             "Approved",
				"done_state":              "Shipped",
				"coding_states":           []interface{}{"Ready", "Coding"},
			},
			"review_resolution": map[string]interface{}{
				"enabled":                      true,
				"escalation_state":             "Needs Human",
				"max_attempts":                 4,
				"require_checks_green":         true,
				"require_code_review_approval": true,
				"unresolved_comment_policy":    "fix_or_explain",
				"escalate_on":                  []interface{}{"security_risk", "conflicting_reviews"},
			},
			"codex": map[string]interface{}{
				"model":            "gpt-5.4",
				"model_provider":   "openai",
				"reasoning_effort": "x-high",
				"skills":           []interface{}{"architecture-review", map[string]interface{}{"name": "repo-skill", "path": "C:\\skills\\repo-skill\\SKILL.md"}},
				"stage_overrides": map[string]interface{}{
					"coding": map[string]interface{}{
						"reasoning_effort": "medium",
						"skills":           []interface{}{"conjit-product-ui"},
					},
					"review": map[string]interface{}{
						"model":            "claude-opus-4.1",
						"model_provider":   "anthropic",
						"endpoint_url":     "https://anthropic-stage.example",
						"api_key":          "$TEST_STAGE_API_KEY",
						"auth_token":       "$TEST_STAGE_AUTH_TOKEN",
						"reasoning_effort": "x_high",
						"env": map[string]interface{}{
							"STAGE_ROUTER": "review",
						},
					},
				},
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Codex.Model != "gpt-5.4" {
		t.Fatalf("codex.model = %q, want gpt-5.4", cfg.Codex.Model)
	}
	if cfg.Codex.ModelProvider != "openai" {
		t.Fatalf("codex.model_provider = %q, want openai", cfg.Codex.ModelProvider)
	}
	if cfg.Codex.ReasoningEffort != "xhigh" {
		t.Fatalf("codex.reasoning_effort = %q, want xhigh", cfg.Codex.ReasoningEffort)
	}
	if len(cfg.Codex.Skills) != 2 || cfg.Codex.Skills[0].Name != "architecture-review" || cfg.Codex.Skills[1].Path == "" {
		t.Fatalf("codex.skills = %+v, want string and map skill refs", cfg.Codex.Skills)
	}
	if cfg.Codex.StageOverrides["coding"].ReasoningEffort != "medium" {
		t.Fatalf("coding reasoning_effort = %q, want medium", cfg.Codex.StageOverrides["coding"].ReasoningEffort)
	}
	if len(cfg.Codex.StageOverrides["coding"].Skills) != 1 || cfg.Codex.StageOverrides["coding"].Skills[0].Name != "conjit-product-ui" {
		t.Fatalf("coding skills = %+v, want conjit-product-ui", cfg.Codex.StageOverrides["coding"].Skills)
	}
	reviewOverride := cfg.Codex.StageOverrides["review"]
	if reviewOverride.Model != "claude-opus-4.1" || reviewOverride.ModelProvider != "anthropic" || reviewOverride.ReasoningEffort != "xhigh" {
		t.Fatalf("review override = %+v, want model/provider/xhigh", reviewOverride)
	}
	if reviewOverride.EndpointURL != "https://anthropic-stage.example" || reviewOverride.APIKey != "" || reviewOverride.AuthToken != "" || !reviewOverride.APIKeyConfigured || !reviewOverride.AuthTokenConfigured {
		t.Fatalf("review routing override = %+v, want endpoint and configured empty env secrets", reviewOverride)
	}
	if reviewOverride.Env["STAGE_ROUTER"] != "review" {
		t.Fatalf("review env override = %+v, want STAGE_ROUTER", reviewOverride.Env)
	}
	if cfg.Pipeline.ReviewState != "Reviewing" || cfg.Pipeline.ReviewResolutionState != "Review Resolution" || cfg.Pipeline.MergeState != "Approved" || cfg.Pipeline.DoneState != "Shipped" {
		t.Fatalf("pipeline = %+v, want custom states", cfg.Pipeline)
	}
	if !slicesEqual(cfg.Tracker.ActiveStates, []string{"Ready", "Coding", "Approved", "Review Resolution"}) {
		t.Fatalf("tracker.active_states = %v, want [Ready Coding Approved Review Resolution]", cfg.Tracker.ActiveStates)
	}
	if !cfg.ReviewResolution.Enabled || cfg.ReviewResolution.MaxAttempts != 4 || cfg.ReviewResolution.EscalationState != "Needs Human" {
		t.Fatalf("review_resolution = %+v, want enabled with custom max attempts and escalation state", cfg.ReviewResolution)
	}
	if !containsFold(cfg.Tracker.TerminalStates, "Shipped") {
		t.Fatalf("tracker.terminal_states = %v, want Shipped included", cfg.Tracker.TerminalStates)
	}
}

func TestResolveConfig_ReasoningEffortSupportsMaxAlias(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"agent_runtime": map[string]interface{}{
				"provider":         "codex",
				"reasoning_effort": "max",
				"stage_overrides": map[string]interface{}{
					"review": map[string]interface{}{
						"provider":            "claude",
						"command":             "node ./review-claude.mjs",
						"permission_mode":     "acceptEdits",
						"allowed_tools":       []interface{}{"Read", "Bash"},
						"disallowed_tools":    []interface{}{"WebSearch"},
						"setting_sources":     []interface{}{"project"},
						"approval_policy":     "auto",
						"thread_sandbox":      "none",
						"turn_sandbox_policy": "workspace-write",
					},
				},
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentRuntime.ReasoningEffort != "xhigh" {
		t.Fatalf("agent_runtime.reasoning_effort = %q, want xhigh", cfg.AgentRuntime.ReasoningEffort)
	}
	review := cfg.AgentRuntime.StageOverrides["review"]
	if review.Provider != "claude" || review.Command != "node ./review-claude.mjs" || review.PermissionMode != "acceptEdits" {
		t.Fatalf("review stage runtime = %+v, want Claude command and permission mode", review)
	}
	if !slicesEqual(review.AllowedTools, []string{"Read", "Bash"}) || !slicesEqual(review.DisallowedTools, []string{"WebSearch"}) || !slicesEqual(review.SettingSources, []string{"project"}) {
		t.Fatalf("review stage tools/settings = %+v", review)
	}
	if review.ApprovalPolicy != "auto" || review.ThreadSandbox != "none" || review.TurnSandboxPolicy != "workspace-write" {
		t.Fatalf("review stage sandbox settings = %+v", review)
	}
}

func TestResolveConfig_RejectsInvalidStageRuntimeProvider(t *testing.T) {
	def := &api.WorkflowDefinition{Config: map[string]interface{}{
		"tracker": map[string]interface{}{"kind": "linear", "api_key": "$SIM_TEST_TRACKER_KEY", "project_slug": "proj"},
		"agent_runtime": map[string]interface{}{
			"stage_overrides": map[string]interface{}{
				"review": map[string]interface{}{"provider": "unknown-sdk"},
			},
		},
	}}

	_, err := ResolveConfig(def, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "provider must be codex or claude") {
		t.Fatalf("ResolveConfig error = %v, want invalid stage provider", err)
	}
}

func TestResolveConfig_EnvVarEmpty(t *testing.T) {
	os.Setenv("TEST_LINEAR_KEY", "")
	defer os.Unsetenv("TEST_LINEAR_KEY")

	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$TEST_LINEAR_KEY",
				"project_slug": "proj",
			},
		},
	}

	_, err := ResolveConfig(def, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty env var, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrMissingTrackerAPIKey) {
		t.Errorf("expected error containing %q, got %v", api.ErrMissingTrackerAPIKey, err)
	}
}

func TestResolveConfig_WorkingStateAppendedToActiveStates(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":          "linear",
				"api_key":       "$SIM_TEST_TRACKER_KEY",
				"project_slug":  "proj",
				"active_states": []interface{}{"Todo"},
				"working_state": "In Progress",
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tracker.WorkingState != "In Progress" {
		t.Fatalf("tracker.working_state = %q, want In Progress", cfg.Tracker.WorkingState)
	}
	wantActive := []string{"Todo", "In Progress", "Approved"}
	if !slicesEqual(cfg.Tracker.ActiveStates, wantActive) {
		t.Fatalf("tracker.active_states = %v, want %v", cfg.Tracker.ActiveStates, wantActive)
	}
}

func TestResolveConfig_PathResolution(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir:", err)
	}
	workflowDir := t.TempDir()

	tests := []struct {
		name       string
		rawRoot    string
		wantPrefix string
		wantAbs    bool
	}{
		{
			name:    "relative",
			rawRoot: "./workspaces",
			wantAbs: true,
		},
		{
			name:       "tilde",
			rawRoot:    "~/workspaces",
			wantPrefix: filepath.Join(home, "workspaces"),
			wantAbs:    true,
		},
		{
			name:    "absolute",
			rawRoot: filepath.Join(workflowDir, "absolute", "workspaces"),
			wantAbs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &api.WorkflowDefinition{
				Config: map[string]interface{}{
					"tracker": map[string]interface{}{
						"kind":         "linear",
						"api_key":      "$SIM_TEST_TRACKER_KEY",
						"project_slug": "proj",
					},
					"workspace": map[string]interface{}{
						"root": tt.rawRoot,
					},
				},
			}

			cfg, err := ResolveConfig(def, workflowDir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantAbs && !filepath.IsAbs(cfg.Workspace.Root) {
				t.Errorf("workspace.root = %q, want absolute", cfg.Workspace.Root)
			}

			if tt.name == "relative" {
				want := filepath.Join(workflowDir, "workspaces")
				if cfg.Workspace.Root != want {
					t.Errorf("workspace.root = %q, want %q", cfg.Workspace.Root, want)
				}
			}

			if tt.wantPrefix != "" && cfg.Workspace.Root != tt.wantPrefix {
				t.Errorf("workspace.root = %q, want %q", cfg.Workspace.Root, tt.wantPrefix)
			}
		})
	}
}

func TestResolveConfig_PathEnvVar(t *testing.T) {
	os.Setenv("TEST_WS_ROOT", "env_workspaces")
	defer os.Unsetenv("TEST_WS_ROOT")

	workflowDir := t.TempDir()
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"workspace": map[string]interface{}{
				"root": "$TEST_WS_ROOT",
			},
		},
	}

	cfg, err := ResolveConfig(def, workflowDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(workflowDir, "env_workspaces")
	if cfg.Workspace.Root != want {
		t.Errorf("workspace.root = %q, want %q", cfg.Workspace.Root, want)
	}
}

func TestResolveConfig_GitWorktreeWorkspace(t *testing.T) {
	workflowDir := t.TempDir()
	repoDir := filepath.Join(workflowDir, "repo")
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"workspace": map[string]interface{}{
				"root":              "./workspaces",
				"mode":              "git_worktree",
				"repo":              "./repo",
				"base_branch":       "origin/main",
				"branch_prefix":     "work/",
				"cleanup_worktrees": true,
			},
		},
	}

	cfg, err := ResolveConfig(def, workflowDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Workspace.Mode != "git_worktree" {
		t.Errorf("mode = %q, want git_worktree", cfg.Workspace.Mode)
	}
	if cfg.Workspace.Repo != repoDir {
		t.Errorf("repo = %q, want %q", cfg.Workspace.Repo, repoDir)
	}
	if cfg.Workspace.BaseBranch != "origin/main" {
		t.Errorf("base_branch = %q, want origin/main", cfg.Workspace.BaseBranch)
	}
	if cfg.Workspace.BranchPrefix != "work/" {
		t.Errorf("branch_prefix = %q, want work/", cfg.Workspace.BranchPrefix)
	}
	if !cfg.Workspace.CleanupWorktrees {
		t.Error("cleanup_worktrees = false, want true")
	}
}

func TestResolveConfig_ValidationFailures(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]interface{}
		wantError string
	}{
		{
			name: "missing tracker kind",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
			},
			wantError: api.ErrUnsupportedTrackerKind,
		},
		{
			name: "unsupported tracker kind",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "jira",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
			},
			wantError: api.ErrUnsupportedTrackerKind,
		},
		{
			name: "missing api_key",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"project_slug": "proj",
				},
			},
			wantError: api.ErrMissingTrackerAPIKey,
		},
		{
			name: "missing project_slug",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":    "linear",
					"api_key": "$SIM_TEST_TRACKER_KEY",
				},
			},
			wantError: api.ErrMissingTrackerProjectSlug,
		},
		{
			name: "invalid max_turns",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"agent": map[string]interface{}{
					"max_turns": 0,
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid polling interval",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"polling": map[string]interface{}{
					"interval_ms": 0,
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid max concurrent agents",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"agent": map[string]interface{}{
					"max_concurrent_agents": 0,
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid max retry backoff",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"agent": map[string]interface{}{
					"max_retry_backoff_ms": 0,
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid hooks timeout",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"hooks": map[string]interface{}{
					"timeout_ms": -1,
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "empty codex command",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"codex": map[string]interface{}{
					"command": "",
				},
			},
			wantError: api.ErrCodexNotFound,
		},
		{
			name: "invalid reasoning effort",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"codex": map[string]interface{}{
					"reasoning_effort": "maximum",
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid stage reasoning effort",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"codex": map[string]interface{}{
					"stage_overrides": map[string]interface{}{
						"review": map[string]interface{}{
							"reasoning_effort": "super-high",
						},
					},
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid codex skills shape",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"codex": map[string]interface{}{
					"skills": "conjit-product-ui",
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "invalid agent runtime provider",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"agent_runtime": map[string]interface{}{
					"provider": "unknown",
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "active completion overlap allowed for review state",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":          "linear",
					"api_key":       "$SIM_TEST_TRACKER_KEY",
					"project_slug":  "proj",
					"active_states": []interface{}{"Todo", "In Review"},
				},
				"pipeline": map[string]interface{}{
					"review_state": "In Review",
				},
			},
			wantError: "",
		},
		{
			name: "active completion overlap rejected outside review state",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":              "linear",
					"api_key":           "$SIM_TEST_TRACKER_KEY",
					"project_slug":      "proj",
					"active_states":     []interface{}{"Todo", "QA"},
					"completion_states": []interface{}{"QA"},
				},
			},
			wantError: api.ErrWorkflowParseError,
		},
		{
			name: "git worktree missing repo",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"workspace": map[string]interface{}{
					"mode": "git_worktree",
				},
			},
			wantError: api.ErrInvalidWorkspaceCWD,
		},
		{
			name: "invalid workspace mode",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{
					"kind":         "linear",
					"api_key":      "$SIM_TEST_TRACKER_KEY",
					"project_slug": "proj",
				},
				"workspace": map[string]interface{}{
					"mode": "magic",
				},
			},
			wantError: api.ErrInvalidWorkspaceCWD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &api.WorkflowDefinition{Config: tt.config}
			_, err := ResolveConfig(def, t.TempDir())
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantError)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestValidateSecretRef(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "environment reference", value: "$API_KEY"},
		{name: "trimmed environment reference", value: "  $API_KEY  "},
		{name: "empty", value: ""},
		{name: "whitespace", value: "   "},
		{name: "literal", value: "sk-retired-literal-key", wantErr: true},
		{name: "dollar not at start", value: "prefix$API_KEY", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSecretRef("test.api_key", tt.value)
			if tt.wantErr && err == nil {
				t.Fatalf("validateSecretRef(%q) expected error", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateSecretRef(%q) returned %v", tt.value, err)
			}
			if err != nil && (!strings.Contains(err.Error(), api.ErrLiteralSecret) || !strings.Contains(err.Error(), "test.api_key")) {
				t.Fatalf("error %q must include code and field path", err)
			}
		})
	}
}

func TestResolveConfigRejectsLiteralSecretsAtEverySupportedLevel(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]interface{}
		wantField string
	}{
		{
			name: "tracker key",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{"kind": "linear", "api_key": "literal", "project_slug": "proj"},
			},
			wantField: "tracker.api_key",
		},
		{
			name: "runtime key",
			config: map[string]interface{}{
				"tracker":       map[string]interface{}{"kind": "linear", "api_key": "$SIM_TEST_TRACKER_KEY", "project_slug": "proj"},
				"agent_runtime": map[string]interface{}{"provider": "codex", "api_key": "literal"},
			},
			wantField: "agent_runtime.api_key",
		},
		{
			name: "runtime token",
			config: map[string]interface{}{
				"tracker":       map[string]interface{}{"kind": "linear", "api_key": "$SIM_TEST_TRACKER_KEY", "project_slug": "proj"},
				"agent_runtime": map[string]interface{}{"provider": "codex", "auth_token": "literal"},
			},
			wantField: "agent_runtime.auth_token",
		},
		{
			name: "stage key",
			config: map[string]interface{}{
				"tracker": map[string]interface{}{"kind": "linear", "api_key": "$SIM_TEST_TRACKER_KEY", "project_slug": "proj"},
				"agent_runtime": map[string]interface{}{
					"provider": "codex",
					"stage_overrides": map[string]interface{}{
						"review": map[string]interface{}{"api_key": "literal"},
					},
				},
			},
			wantField: "agent_runtime.stage_overrides.review.api_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveConfig(&api.WorkflowDefinition{Config: tt.config}, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), api.ErrLiteralSecret) || !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("error = %v, want %s naming %s", err, api.ErrLiteralSecret, tt.wantField)
			}
		})
	}
}

func TestResolveConfig_PartialConfig(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"agent": map[string]interface{}{
				"max_concurrent_agents": 5,
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agent.MaxConcurrentAgents != 5 {
		t.Errorf("agent.max_concurrent_agents = %d, want 5", cfg.Agent.MaxConcurrentAgents)
	}
	if cfg.Agent.MaxTurns != 20 {
		t.Errorf("agent.max_turns = %d, want 20", cfg.Agent.MaxTurns)
	}
	if cfg.Tracker.Kind != "linear" {
		t.Errorf("tracker.kind = %q, want linear", cfg.Tracker.Kind)
	}
}

func TestResolveConfig_UnknownKeysIgnored(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":          "linear",
				"api_key":       "$SIM_TEST_TRACKER_KEY",
				"project_slug":  "proj",
				"unknown_field": "should be ignored",
			},
			"future_section": map[string]interface{}{
				"foo": "bar",
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tracker.Kind != "linear" {
		t.Errorf("tracker.kind = %q, want linear", cfg.Tracker.Kind)
	}
}

func TestSaveWorkflow_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"future_section": map[string]interface{}{
				"enabled": true,
			},
		},
		PromptTemplate: "Hello {{ issue.identifier }}",
	}

	if err := SaveWorkflow(path, def); err != nil {
		t.Fatalf("SaveWorkflow returned error: %v", err)
	}

	got, err := LoadWorkflow(path)
	if err != nil {
		t.Fatalf("LoadWorkflow returned error: %v", err)
	}
	if got.PromptTemplate != def.PromptTemplate {
		t.Errorf("prompt template = %q, want %q", got.PromptTemplate, def.PromptTemplate)
	}
	if getSubMap(got.Config, "future_section")["enabled"] != true {
		t.Errorf("future_section.enabled was not preserved: %v", got.Config)
	}
}

func TestSaveWorkflow_ReplacesExistingWorkflow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	if err := os.WriteFile(path, []byte("---\ntracker:\n  kind: linear\n  api_key: old\n  project_slug: old\n---\n\nOld prompt\n"), 0o600); err != nil {
		t.Fatalf("write initial workflow: %v", err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat initial workflow: %v", err)
	}

	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "new",
				"project_slug": "new-proj",
			},
		},
		PromptTemplate: "New prompt",
	}
	if err := SaveWorkflow(path, def); err != nil {
		t.Fatalf("SaveWorkflow returned error: %v", err)
	}

	got, err := LoadWorkflow(path)
	if err != nil {
		t.Fatalf("LoadWorkflow returned error: %v", err)
	}
	if got.PromptTemplate != "New prompt" {
		t.Errorf("prompt template = %q, want New prompt", got.PromptTemplate)
	}
	tracker := getSubMap(got.Config, "tracker")
	if tracker["api_key"] != "new" {
		t.Errorf("tracker.api_key = %v, want new", tracker["api_key"])
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".WORKFLOW.md.tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary workflow files remain: %v", matches)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat saved workflow: %v", err)
	}
	if afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Errorf("mode = %v, want %v", afterInfo.Mode().Perm(), beforeInfo.Mode().Perm())
	}
}

func TestCheckedInWorkflowTemplateResolves(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "test-linear-key")

	workflowPath := filepath.Join("..", "..", "WORKFLOW.md")
	def, err := LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("load checked-in WORKFLOW.md: %v", err)
	}

	cfg, err := ResolveConfig(def, filepath.Dir(workflowPath))
	if err != nil {
		t.Fatalf("resolve checked-in WORKFLOW.md: %v", err)
	}

	if strings.TrimSpace(cfg.Tracker.ProjectSlug) == "" {
		t.Fatal("tracker.project_slug resolved empty")
	}
	if cfg.Tracker.APIKey != "test-linear-key" {
		t.Fatalf("tracker.api_key = %q, want env-resolved test value", cfg.Tracker.APIKey)
	}
	if strings.TrimSpace(cfg.Codex.Command) == "" {
		t.Fatal("codex.command resolved empty")
	}
	if strings.TrimSpace(def.PromptTemplate) == "" {
		t.Fatal("checked-in WORKFLOW.md prompt template is empty")
	}

	desc := "Public workflow template render check."
	_, err = prompt.NewRenderer().Render(def.PromptTemplate, api.Issue{
		ID:          "issue-id",
		Identifier:  "SIM-123",
		Title:       "Document the project",
		Description: &desc,
		State:       "In Progress",
		Labels:      []string{"docs", "public"},
	}, nil)
	if err != nil {
		t.Fatalf("render checked-in WORKFLOW.md prompt template: %v", err)
	}
}

func TestDocumentationWorkflowExamplesResolve(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "test-linear-key")

	docsPath := filepath.Join("..", "..", "docs", "workflow-examples.md")
	content, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("read workflow examples: %v", err)
	}

	re := regexp.MustCompile("(?s)```markdown\n(.*?)\n```")
	matches := re.FindAllStringSubmatch(string(content), -1)
	if len(matches) < 2 {
		t.Fatalf("expected at least 2 markdown workflow examples, got %d", len(matches))
	}

	desc := "Example workflow render check."
	issue := api.Issue{
		ID:          "issue-id",
		Identifier:  "SIM-123",
		Title:       "Document the project",
		Description: &desc,
		State:       "In Progress",
		Labels:      []string{"docs", "public"},
	}

	for i, match := range matches {
		t.Run(filepath.Base(docsPath)+" example", func(t *testing.T) {
			workflowPath := filepath.Join(t.TempDir(), "WORKFLOW.md")
			if err := os.WriteFile(workflowPath, []byte(match[1]), 0644); err != nil {
				t.Fatalf("write workflow example: %v", err)
			}

			def, err := LoadWorkflow(workflowPath)
			if err != nil {
				t.Fatalf("load workflow example %d: %v", i+1, err)
			}
			if _, err := ResolveConfig(def, filepath.Dir(workflowPath)); err != nil {
				t.Fatalf("resolve workflow example %d: %v", i+1, err)
			}
			if _, err := prompt.NewRenderer().Render(def.PromptTemplate, issue, nil); err != nil {
				t.Fatalf("render workflow example %d: %v", i+1, err)
			}
		})
	}
}

func TestResolveConfig_InvalidAgentStateEntriesFiltered(t *testing.T) {
	def := &api.WorkflowDefinition{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "$SIM_TEST_TRACKER_KEY",
				"project_slug": "proj",
			},
			"agent": map[string]interface{}{
				"max_concurrent_agents_by_state": map[string]interface{}{
					"todo":       5,
					"inprogress": -1,
					"done":       "not-a-number",
					"Review":     3,
				},
			},
		},
	}

	cfg, err := ResolveConfig(def, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Agent.MaxConcurrentAgentsByState) != 2 {
		t.Fatalf("expected 2 valid entries, got %d", len(cfg.Agent.MaxConcurrentAgentsByState))
	}
	if cfg.Agent.MaxConcurrentAgentsByState["todo"] != 5 {
		t.Errorf("todo = %d, want 5", cfg.Agent.MaxConcurrentAgentsByState["todo"])
	}
	if cfg.Agent.MaxConcurrentAgentsByState["review"] != 3 {
		t.Errorf("review = %d, want 3", cfg.Agent.MaxConcurrentAgentsByState["review"])
	}
	if _, ok := cfg.Agent.MaxConcurrentAgentsByState["inprogress"]; ok {
		t.Error("inprogress should be filtered out")
	}
	if _, ok := cfg.Agent.MaxConcurrentAgentsByState["done"]; ok {
		t.Error("done should be filtered out")
	}
}

func TestWatchWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	changed := make(chan struct{}, 1)
	onChange := func() {
		changed <- struct{}{}
	}

	watcher, err := WatchWorkflow(path, onChange)
	if err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer watcher.Close()

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(path, []byte("world"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	select {
	case <-changed:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for change event")
	}
}

func TestWatchWorkflow_RenameRecreate(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	changed := make(chan struct{}, 2)
	onChange := func() {
		changed <- struct{}{}
	}

	watcher, err := WatchWorkflow(path, onChange)
	if err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer watcher.Close()

	time.Sleep(100 * time.Millisecond)

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte("updated"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		t.Fatalf("failed to rename: %v", err)
	}

	select {
	case <-changed:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for change event after rename")
	}
}
