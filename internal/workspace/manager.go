package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kbsartain/simphony/pkg/api"
)

var sanitizeRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Manager manages per-issue filesystem workspaces and lifecycle hooks.
type Manager struct {
	root             string
	mode             string
	repo             string
	baseBranch       string
	branchPrefix     string
	cleanupWorktrees bool
}

// NewManager creates a new Manager with the given workspace root.
// The root is converted to an absolute path if necessary, and created if it doesn't exist.
func NewManager(root string) (*Manager, error) {
	return NewManagerWithConfig(api.WorkspaceConfig{
		Root: root,
		Mode: "directory",
	})
}

// NewManagerWithConfig creates a new Manager from resolved workspace config.
func NewManagerWithConfig(cfg api.WorkspaceConfig) (*Manager, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = "directory"
	}
	if mode != "directory" && mode != "git_worktree" {
		return nil, fmt.Errorf("unsupported workspace mode %q", mode)
	}

	root := cfg.Root
	if root == "" {
		return nil, fmt.Errorf("workspace root cannot be empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	repo := cfg.Repo
	if repo != "" {
		var err error
		repo, err = filepath.Abs(repo)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace repo: %w", err)
		}
	}
	if mode == "git_worktree" {
		if repo == "" {
			return nil, fmt.Errorf("workspace repo is required for git_worktree mode")
		}
		if err := validateGitRepo(repo); err != nil {
			return nil, err
		}
	}

	baseBranch := cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	branchPrefix := cfg.BranchPrefix
	if branchPrefix == "" {
		branchPrefix = "github.com/kbsartain/simphony/"
	}

	return &Manager{
		root:             absRoot,
		mode:             mode,
		repo:             repo,
		baseBranch:       baseBranch,
		branchPrefix:     branchPrefix,
		cleanupWorktrees: cfg.CleanupWorktrees,
	}, nil
}

// PrepareWorkspace ensures a workspace directory exists for the given issue.
// It returns api.Workspace with CreatedNow set to true only if the directory was created during this call.
func (m *Manager) PrepareWorkspace(issue api.Issue) (*api.Workspace, error) {
	key := sanitizeKey(issue.Identifier)
	if key == "" {
		return nil, fmt.Errorf("workspace key cannot be empty")
	}
	path := filepath.Join(m.root, key)
	if !isInsideRoot(m.root, path) {
		return nil, fmt.Errorf("workspace path %s escapes root %s", path, m.root)
	}
	if m.mode == "git_worktree" {
		return m.prepareGitWorktree(issue, key, path)
	}

	existed := false
	if fi, err := os.Stat(path); err == nil {
		if !fi.IsDir() {
			return nil, fmt.Errorf("workspace path %s exists but is not a directory", path)
		}
		existed = true
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		if !existed {
			_ = os.RemoveAll(path)
		}
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	return &api.Workspace{
		Path:         path,
		WorkspaceKey: key,
		CreatedNow:   !existed,
	}, nil
}

// RemoveWorkspace removes the workspace directory for the given issue identifier.
func (m *Manager) RemoveWorkspace(issueIdentifier string) error {
	key := sanitizeKey(issueIdentifier)
	if key == "" {
		return fmt.Errorf("workspace key cannot be empty")
	}
	path := filepath.Join(m.root, key)
	if !isInsideRoot(m.root, path) {
		return fmt.Errorf("workspace path %s escapes root %s", path, m.root)
	}
	if m.mode == "git_worktree" {
		return m.removeGitWorktree(path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
	return nil
}

// RunHook executes a lifecycle hook script inside the workspace directory.
// Fatal hook failures (after_create, before_run) are returned as errors.
// Non-fatal hook failures (after_run, before_remove) are logged and ignored.
func (m *Manager) RunHook(name string, script string, workspacePath string, timeoutMs int) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	fmt.Printf("hook %s started workspace=%s\n", name, workspacePath)
	err := runScriptWithTimeout(script, workspacePath, timeoutMs)
	if err != nil {
		fmt.Printf("hook %s failed workspace=%s err=%v\n", name, workspacePath, err)
		if isFatalHook(name) {
			return fmt.Errorf("hook %s failed: %w", name, err)
		}
		fmt.Printf("hook %s error ignored (non-fatal)\n", name)
		return nil
	}
	fmt.Printf("hook %s succeeded workspace=%s\n", name, workspacePath)
	return nil
}

func runScriptWithTimeout(script, workspacePath string, timeoutMs int) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", script)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-lc", script)
	}
	cmd.Dir = workspacePath

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("hook timed out after %dms: %w", timeoutMs, err)
		}
		return fmt.Errorf("hook exited with error: %w\noutput: %s", err, out.String())
	}
	return nil
}

func (m *Manager) prepareGitWorktree(issue api.Issue, key string, path string) (*api.Workspace, error) {
	if isGitWorktree(path) {
		return &api.Workspace{
			Path:         path,
			WorkspaceKey: key,
			CreatedNow:   false,
		}, nil
	}

	if exists, err := pathExists(path); err != nil {
		return nil, err
	} else if exists {
		empty, err := isDirEmpty(path)
		if err != nil {
			return nil, err
		}
		if !empty {
			return nil, fmt.Errorf("workspace path %s exists but is not a git worktree or empty directory", path)
		}
	}

	branch := m.branchName(issue, key)
	if branchExists(m.repo, branch) {
		if err := runGit(m.repo, "worktree", "add", path, branch); err != nil {
			return nil, err
		}
	} else {
		if err := runGit(m.repo, "worktree", "add", "-b", branch, path, m.baseBranch); err != nil {
			return nil, err
		}
	}

	return &api.Workspace{
		Path:         path,
		WorkspaceKey: key,
		CreatedNow:   true,
	}, nil
}

func (m *Manager) removeGitWorktree(path string) error {
	if !m.cleanupWorktrees {
		return nil
	}
	if isGitWorktree(path) {
		if err := runGit(m.repo, "worktree", "remove", path); err != nil {
			return err
		}
	}
	return runGit(m.repo, "worktree", "prune")
}

func (m *Manager) branchName(issue api.Issue, key string) string {
	if issue.BranchName != nil && strings.TrimSpace(*issue.BranchName) != "" {
		return sanitizeBranchName(*issue.BranchName)
	}
	return sanitizeBranchName(m.branchPrefix + key)
}

func validateGitRepo(repo string) error {
	if err := runGit(repo, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("workspace repo %s is not a git repository: %w", repo, err)
	}
	return nil
}

func branchExists(repo string, branch string) bool {
	return runGit(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) == nil
}

func isGitWorktree(path string) bool {
	if fi, err := os.Stat(filepath.Join(path, ".git")); err == nil && !fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(filepath.Join(path, ".git")); err == nil && fi.IsDir() {
		return true
	}
	return false
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isDirEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func runGit(repo string, args ...string) error {
	cmdArgs := append([]string{"-C", repo}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s failed: %w\noutput: %s", strings.Join(cmdArgs, " "), err, out.String())
	}
	return nil
}

// GetWorkspacePath returns the absolute workspace path for an issue identifier.
func (m *Manager) GetWorkspacePath(issueIdentifier string) string {
	return filepath.Join(m.root, sanitizeKey(issueIdentifier))
}

func sanitizeKey(identifier string) string {
	return sanitizeRe.ReplaceAllString(identifier, "_")
}

func sanitizeBranchName(branch string) string {
	branch = strings.TrimSpace(branch)
	branch = strings.ReplaceAll(branch, "\\", "/")
	parts := strings.Split(branch, "/")
	for i, part := range parts {
		part = sanitizeRe.ReplaceAllString(part, "-")
		part = strings.Trim(part, ".-")
		if part == "" {
			part = "branch"
		}
		parts[i] = part
	}
	return strings.Join(parts, "/")
}

func isInsideRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rootClean := filepath.Clean(absRoot)
	pathClean := filepath.Clean(absPath)

	if runtime.GOOS == "windows" {
		rootClean = strings.ToLower(rootClean)
		pathClean = strings.ToLower(pathClean)
	}

	prefix := rootClean + string(filepath.Separator)
	return strings.HasPrefix(pathClean, prefix)
}

func isFatalHook(name string) bool {
	switch name {
	case "after_create", "before_run":
		return true
	default:
		return false
	}
}
