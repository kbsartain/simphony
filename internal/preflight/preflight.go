// Package preflight validates local project environment readiness before dispatch.
package preflight

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kbsartain/simphony/internal/agentruntime"
	"github.com/kbsartain/simphony/internal/codexcmd"
	"github.com/kbsartain/simphony/pkg/api"
)

const (
	StatusReady   = "ready"
	StatusWarning = "warning"
	StatusBlocked = "blocked"

	SeverityWarning = "warning"
	SeverityBlocker = "blocker"
)

// Check validates local configuration and filesystem constraints that can be
// determined without touching the tracker or claiming work.
func Check(cfg *api.WorkflowConfig) api.ProjectHealth {
	now := time.Now()
	health := api.ProjectHealth{
		Status:    StatusReady,
		CheckedAt: &now,
		Summary:   "Project environment is ready",
	}
	if cfg == nil {
		return blocked(now, "config_missing", "Project configuration is not loaded", "", "Resolve the project workflow before dispatching work.")
	}

	addRuntimeCommandCheck(&health, cfg.AgentRuntime, "")
	addRuntimeCredentialCheck(&health, cfg.AgentRuntime, "")
	stages := make([]string, 0, len(cfg.AgentRuntime.StageOverrides))
	for stage := range cfg.AgentRuntime.StageOverrides {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	for _, stage := range stages {
		override := cfg.AgentRuntime.StageOverrides[stage]
		if strings.TrimSpace(override.Provider) == "" && strings.TrimSpace(override.Command) == "" && !override.APIKeyConfigured && !override.AuthTokenConfigured {
			continue
		}
		effective := agentruntime.EffectiveConfig(&cfg.AgentRuntime, api.PipelineStage{Kind: stage})
		addRuntimeCommandCheck(&health, effective, stage)
		addRuntimeCredentialCheck(&health, effective, stage)
	}
	addVerifyCommandsCheck(&health, cfg.Verify.Commands)
	addGitHubCLICheck(&health, &cfg.GitHub)
	addWorkspaceCheck(&health, &cfg.Workspace)
	finalize(&health)
	return health
}

func addVerifyCommandsCheck(health *api.ProjectHealth, commands []string) {
	seen := make(map[string]struct{})
	for _, command := range commands {
		trimmed := strings.TrimSpace(command)
		if trimmed == "" || containsShellOperators(trimmed) {
			continue
		}
		executable := commandExecutable(trimmed)
		if executable == "" || isShellBuiltin(executable) {
			continue
		}
		if _, ok := seen[executable]; ok {
			continue
		}
		seen[executable] = struct{}{}
		if _, err := exec.LookPath(executable); err != nil {
			addIssue(health, api.HealthIssue{
				Code:       "verify_command_not_found",
				Severity:   SeverityBlocker,
				Message:    "A verify.commands executable was not found on PATH",
				Detail:     executable,
				Suggestion: fmt.Sprintf("Install %s or make it available to the Simphony process before enabling merge verification.", executable),
			})
		}
	}
}

func containsShellOperators(command string) bool {
	for _, operator := range []string{"&&", "||", "|", ";", "$(", "`", ">", "<"} {
		if strings.Contains(command, operator) {
			return true
		}
	}
	return false
}

func isShellBuiltin(name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case "cd", "echo", "set", "export", "source", "exit", "if", "for", "while", "test", "[":
		return true
	default:
		return false
	}
}

func addGitHubCLICheck(health *api.ProjectHealth, cfg *api.GitHubConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		addIssue(health, api.HealthIssue{
			Code:       "github_cli_not_found",
			Severity:   SeverityBlocker,
			Message:    "github.enabled is true but the gh CLI was not found on PATH",
			Suggestion: "Install GitHub CLI, or disable the GitHub PR merge gate.",
		})
		return
	}
	var out bytes.Buffer
	cmd := exec.Command("gh", "auth", "status")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		addIssue(health, api.HealthIssue{
			Code:       "github_cli_not_authenticated",
			Severity:   SeverityBlocker,
			Message:    "gh CLI is installed but not authenticated",
			Detail:     strings.TrimSpace(out.String()),
			Suggestion: "Run `gh auth login` for the account used by Simphony.",
		})
	}
}

func addRuntimeCommandCheck(health *api.ProjectHealth, runtime api.AgentRuntimeConfig, stage string) {
	command := runtime.Command
	if strings.EqualFold(strings.TrimSpace(runtime.Provider), "claude") && strings.TrimSpace(command) == "" {
		// The Claude runner materializes an embedded SDK shim and launches it
		// through Node when no custom wrapper command is configured.
		command = "node"
	}
	before := len(health.Issues)
	addCommandCheck(health, command)
	annotateStageIssues(health, before, stage)
}

func addRuntimeCredentialCheck(health *api.ProjectHealth, runtime api.AgentRuntimeConfig, stage string) {
	before := len(health.Issues)
	if runtime.APIKeyConfigured && strings.TrimSpace(runtime.APIKey) == "" {
		addIssue(health, api.HealthIssue{
			Code:       "agent_api_key_unresolved",
			Severity:   SeverityBlocker,
			Message:    "Configured agent API key resolved to an empty value",
			Suggestion: "Set the referenced environment variable, or remove api_key to use an authenticated local SDK session.",
		})
	}
	if runtime.AuthTokenConfigured && strings.TrimSpace(runtime.AuthToken) == "" {
		addIssue(health, api.HealthIssue{
			Code:       "agent_auth_token_unresolved",
			Severity:   SeverityBlocker,
			Message:    "Configured agent auth token resolved to an empty value",
			Suggestion: "Set the referenced environment variable, or remove auth_token to use an authenticated local SDK session.",
		})
	}
	annotateStageIssues(health, before, stage)
}

func annotateStageIssues(health *api.ProjectHealth, before int, stage string) {
	if stage == "" {
		return
	}
	for i := before; i < len(health.Issues); i++ {
		health.Issues[i].Message = fmt.Sprintf("Stage %s: %s", stage, health.Issues[i].Message)
		detail := strings.TrimSpace(health.Issues[i].Detail)
		if detail == "" {
			health.Issues[i].Detail = "stage=" + stage
		} else {
			health.Issues[i].Detail = "stage=" + stage + " " + detail
		}
	}
}

func addCommandCheck(health *api.ProjectHealth, command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		addIssue(health, api.HealthIssue{
			Code:       "agent_command_missing",
			Severity:   SeverityBlocker,
			Message:    "Agent runtime command is not configured",
			Suggestion: "Set agent_runtime.command, codex.command, or claude.command for this project.",
		})
		return
	}
	resolvedCommand, err := codexcmd.Resolve(command)
	if err != nil {
		addIssue(health, api.HealthIssue{
			Code:       "agent_command_not_executable",
			Severity:   SeverityBlocker,
			Message:    "Agent runtime executable is not usable",
			Detail:     err.Error(),
			Suggestion: "Open the ChatGPT/Codex app once, install the standalone CLI, or configure an accessible absolute command path.",
		})
		return
	}
	command = resolvedCommand
	executable := commandExecutable(command)
	if executable == "" {
		addIssue(health, api.HealthIssue{
			Code:       "agent_command_invalid",
			Severity:   SeverityBlocker,
			Message:    "Agent runtime command could not be parsed",
			Detail:     command,
			Suggestion: "Use an executable path followed by arguments.",
		})
		return
	}
	if filepath.IsAbs(executable) {
		if _, err := os.Stat(executable); err != nil {
			addIssue(health, api.HealthIssue{
				Code:       "agent_command_not_found",
				Severity:   SeverityBlocker,
				Message:    "Agent runtime executable was not found",
				Detail:     executable,
				Suggestion: "Install the selected SDK or update the project runtime command.",
			})
		}
		return
	}
	if _, err := exec.LookPath(executable); err != nil {
		addIssue(health, api.HealthIssue{
			Code:       "agent_command_not_found",
			Severity:   SeverityBlocker,
			Message:    "Agent runtime executable was not found on PATH",
			Detail:     executable,
			Suggestion: "Install the selected SDK or use an absolute command path.",
		})
	}
}

func addWorkspaceCheck(health *api.ProjectHealth, cfg *api.WorkspaceConfig) {
	if strings.TrimSpace(cfg.Root) == "" {
		addIssue(health, api.HealthIssue{
			Code:       "workspace_root_missing",
			Severity:   SeverityBlocker,
			Message:    "Workspace root is not configured",
			Suggestion: "Set workspace.root to a writable project workspace folder.",
		})
	}
	if strings.EqualFold(cfg.Mode, "git_worktree") {
		addGitWorktreeCheck(health, cfg)
	}
}

func addGitWorktreeCheck(health *api.ProjectHealth, cfg *api.WorkspaceConfig) {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		addIssue(health, api.HealthIssue{
			Code:       "workspace_repo_missing",
			Severity:   SeverityBlocker,
			Message:    "Git worktree mode requires workspace.repo",
			Suggestion: "Set workspace.repo to the source repository for this project.",
		})
		return
	}
	if err := runGit(repo, "rev-parse", "--git-dir"); err != nil {
		addIssue(health, api.HealthIssue{
			Code:       "workspace_repo_invalid",
			Severity:   SeverityBlocker,
			Message:    "Workspace repository is not a readable Git repository",
			Detail:     err.Error(),
			Suggestion: "Check workspace.repo and make sure the repository is available to this Simphony process.",
		})
		return
	}
	if runtime.GOOS != "windows" {
		return
	}
	branch := strings.TrimSpace(cfg.BaseBranch)
	if branch == "" {
		branch = "main"
	}
	paths, err := gitTrackedPaths(repo, branch)
	if err != nil {
		addIssue(health, api.HealthIssue{
			Code:       "workspace_repo_paths_unreadable",
			Severity:   SeverityBlocker,
			Message:    "Could not inspect tracked repository paths",
			Detail:     err.Error(),
			Suggestion: "Verify workspace.base_branch exists and can be read.",
		})
		return
	}
	for _, path := range paths {
		if reason := windowsPathIssue(path); reason != "" {
			addIssue(health, api.HealthIssue{
				Code:       "windows_incompatible_path",
				Severity:   SeverityBlocker,
				Message:    "Repository contains tracked paths that Windows cannot check out",
				Detail:     fmt.Sprintf("%s: %s", path, reason),
				Suggestion: "Run this project in WSL/Linux or rename/remove the incompatible tracked path upstream.",
			})
			return
		}
	}
}

func commandExecutable(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if command[0] == '"' || command[0] == '\'' {
		quote := command[0]
		for i := 1; i < len(command); i++ {
			if command[i] == quote {
				return command[1:i]
			}
		}
		return strings.Trim(command, `"'`)
	}
	return strings.Fields(command)[0]
}

func gitTrackedPaths(repo string, branch string) ([]string, error) {
	out, err := runGitOutput(repo, "ls-tree", "-rz", "--name-only", branch)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(out, "\x00")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

func windowsPathIssue(path string) string {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" {
			continue
		}
		for _, r := range part {
			if r < 32 {
				return "path component contains an ASCII control character"
			}
		}
		if strings.ContainsAny(part, `<>:"\|?*`) {
			return "path component contains a character reserved by Windows"
		}
		if strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return "path component ends with a space or period"
		}
		upperPart := strings.ToUpper(part)
		name := strings.TrimSuffix(upperPart, strings.ToUpper(filepath.Ext(part)))
		switch name {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
			return "path component uses a reserved Windows device name"
		}
	}
	return ""
}

func runGit(repo string, args ...string) error {
	_, err := runGitOutput(repo, args...)
	return err
}

func runGitOutput(repo string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", repo}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s failed: %w\noutput: %s", strings.Join(cmdArgs, " "), err, out.String())
	}
	return out.String(), nil
}

func blocked(now time.Time, code string, message string, detail string, suggestion string) api.ProjectHealth {
	return api.ProjectHealth{
		Status:    StatusBlocked,
		CheckedAt: &now,
		Summary:   message,
		Issues: []api.HealthIssue{{
			Code:       code,
			Severity:   SeverityBlocker,
			Message:    message,
			Detail:     detail,
			Suggestion: suggestion,
		}},
	}
}

func addIssue(health *api.ProjectHealth, issue api.HealthIssue) {
	health.Issues = append(health.Issues, issue)
}

func finalize(health *api.ProjectHealth) {
	blockers := 0
	warnings := 0
	for _, issue := range health.Issues {
		switch issue.Severity {
		case SeverityBlocker:
			blockers++
		case SeverityWarning:
			warnings++
		}
	}
	switch {
	case blockers > 0:
		health.Status = StatusBlocked
		health.Summary = fmt.Sprintf("%d blocker(s) must be resolved before dispatch", blockers)
	case warnings > 0:
		health.Status = StatusWarning
		health.Summary = fmt.Sprintf("%d environment warning(s)", warnings)
	default:
		health.Status = StatusReady
		health.Summary = "Project environment is ready"
	}
}
