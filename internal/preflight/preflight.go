// Package preflight validates local project environment readiness before dispatch.
package preflight

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

	addCommandCheck(&health, cfg.AgentRuntime.Command)
	addWorkspaceCheck(&health, &cfg.Workspace)
	finalize(&health)
	return health
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
