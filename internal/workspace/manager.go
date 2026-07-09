package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

	// mergeMu serializes MergeIssueBranch calls. Because git_worktree mode
	// shares one .git object database and one main-repo checkout across all
	// issue worktrees, concurrent merges (checkout + merge against m.repo)
	// must not interleave.
	mergeMu sync.Mutex

	// worktreeMu serializes git worktree creation (prepareGitWorktree).
	// Today's dispatch loop happens to call this synchronously per issue, so
	// this isn't currently load-bearing — but `git worktree add` mutates
	// shared .git/worktrees administrative state and is not safe to run
	// concurrently against the same repo, so this is cheap insurance against
	// any future dispatch path that parallelizes workspace creation.
	worktreeMu sync.Mutex
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
	m.worktreeMu.Lock()
	defer m.worktreeMu.Unlock()
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

// MergeIssueBranch merges the issue's branch into the workspace base branch
// (e.g. main) directly in the shared repo, so that work completed by one
// issue becomes visible to worktrees created for later issues. It is a no-op
// in "directory" mode, where there is no per-issue branch to merge.
//
// If verify is non-nil, it is called after the merge commit is created but
// before this function returns success, with the shared repo path — at that
// point the repo is checked out at the tentative merge commit, so verify
// sees exactly what would land on the base branch. This is deliberate: an
// issue's own worktree can be stale relative to base by the time it reaches
// merge (other issues may have merged since it was created), and conflict
// resolution during the merge itself can introduce failures that exist only
// in the merged result, not in either branch alone. If verify returns an
// error, the merge is rolled back (hard reset to the pre-merge commit) and
// the repo is left clean on the base branch.
//
// On any failure (dirty repo, missing branch, merge conflict, failed
// verify) the repo is left in a clean state on the base branch and a
// descriptive error is returned; callers should not treat the issue as done
// in that case.
func (m *Manager) MergeIssueBranch(issue api.Issue, verify func(repoPath string) error) error {
	if m.mode != "git_worktree" {
		return nil
	}

	m.mergeMu.Lock()
	defer m.mergeMu.Unlock()

	key := sanitizeKey(issue.Identifier)
	branch := m.branchName(issue, key)
	if branch == "" {
		return fmt.Errorf("could not resolve branch name for issue %s", issue.Identifier)
	}
	if branch == m.baseBranch {
		return fmt.Errorf("issue %s branch %q is the same as base branch %q", issue.Identifier, branch, m.baseBranch)
	}
	if !branchExists(m.repo, branch) {
		return fmt.Errorf("branch %s does not exist in repo %s", branch, m.repo)
	}

	dirty, err := repoIsDirty(m.repo)
	if err != nil {
		return fmt.Errorf("check repo status before merge: %w", err)
	}
	if dirty {
		return fmt.Errorf("repo %s has uncommitted changes; refusing to merge %s into %s", m.repo, branch, m.baseBranch)
	}

	if err := runGit(m.repo, "checkout", m.baseBranch); err != nil {
		return fmt.Errorf("checkout base branch %s: %w", m.baseBranch, err)
	}

	preMergeSHA, err := currentCommitSHA(m.repo)
	if err != nil {
		return fmt.Errorf("resolve pre-merge commit: %w", err)
	}

	mergeMsg := fmt.Sprintf("Merge %s (%s) into %s", branch, issue.Identifier, m.baseBranch)
	if err := runGit(m.repo, "merge", "--no-ff", "--no-edit", "-m", mergeMsg, branch); err != nil {
		_ = runGit(m.repo, "merge", "--abort")
		return fmt.Errorf("merge %s into %s: %w", branch, m.baseBranch, err)
	}

	if verify != nil {
		if err := verify(m.repo); err != nil {
			_ = runGit(m.repo, "reset", "--hard", preMergeSHA)
			return fmt.Errorf("verify failed on merged result (rolled back): %w", err)
		}
	}

	return nil
}

// MergeIssueBranchViaGitHubPR pushes the issue's branch to origin, opens (or
// reuses) a GitHub PR against the base branch via the `gh` CLI, waits for
// GitHub's checks to complete, and merges via `gh pr merge` if they pass.
// Every step is explicit and deterministic — nothing here depends on the
// coding agent's own judgment (it is separately blocked from running
// `git push` itself via claude.disallowed_tools) or on GitHub repo settings
// like branch protection being configured correctly; this function waits
// for checks itself rather than trusting `gh pr merge --auto`. After a
// successful merge, the local shared repo checkout is fast-forwarded to
// match origin's base branch so subsequently-created worktrees see the
// merged result — mirroring what MergeIssueBranch does for the local-merge
// path.
//
// verify, if non-nil, is run against the issue's own worktree (workspacePath,
// falling back to the shared repo if empty) before the branch is ever
// pushed — a fast, local pre-check. It does not replace GitHub's own CI,
// which runs against the actual proposed merge via the repo's
// pull_request-triggered workflow and is the authoritative gate this
// function waits on.
func (m *Manager) MergeIssueBranchViaGitHubPR(issue api.Issue, workspacePath string, verify func(repoPath string) error, ghCfg api.GitHubConfig) error {
	if m.mode != "git_worktree" {
		return nil
	}

	key := sanitizeKey(issue.Identifier)
	branch := m.branchName(issue, key)
	if branch == "" {
		return fmt.Errorf("could not resolve branch name for issue %s", issue.Identifier)
	}
	if branch == m.baseBranch {
		return fmt.Errorf("issue %s branch %q is the same as base branch %q", issue.Identifier, branch, m.baseBranch)
	}
	if !branchExists(m.repo, branch) {
		return fmt.Errorf("branch %s does not exist in repo %s", branch, m.repo)
	}

	if verify != nil {
		verifyPath := workspacePath
		if strings.TrimSpace(verifyPath) == "" {
			verifyPath = m.repo
		}
		if err := verify(verifyPath); err != nil {
			return fmt.Errorf("local verify failed, not pushing: %w", err)
		}
	}

	m.mergeMu.Lock()
	defer m.mergeMu.Unlock()

	if err := runGit(m.repo, "push", "-u", "origin", branch); err != nil {
		return fmt.Errorf("push branch %s to origin: %w", branch, err)
	}

	prNumber, err := ensureGitHubPR(m.repo, branch, m.baseBranch, issue)
	if err != nil {
		return fmt.Errorf("ensure GitHub PR for %s: %w", branch, err)
	}

	timeout := time.Duration(ghCfg.ChecksTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	if err := waitForGitHubChecks(m.repo, prNumber, timeout); err != nil {
		return fmt.Errorf("PR #%d checks did not pass: %w", prNumber, err)
	}

	if err := mergeGitHubPR(m.repo, prNumber, ghCfg.MergeMethod); err != nil {
		return fmt.Errorf("merge PR #%d: %w", prNumber, err)
	}

	if err := runGit(m.repo, "fetch", "origin", m.baseBranch); err != nil {
		return fmt.Errorf("fetch origin/%s after merge: %w", m.baseBranch, err)
	}
	if err := runGit(m.repo, "checkout", m.baseBranch); err != nil {
		return fmt.Errorf("checkout %s after merge: %w", m.baseBranch, err)
	}
	if err := runGit(m.repo, "reset", "--hard", "origin/"+m.baseBranch); err != nil {
		return fmt.Errorf("fast-forward local %s to match origin: %w", m.baseBranch, err)
	}

	return nil
}

// ensureGitHubPR opens a PR for branch -> base via the gh CLI, or returns
// the existing PR number if one is already open for this branch (e.g. a
// retry after a prior partial attempt left a PR open).
func ensureGitHubPR(repo string, branch string, base string, issue api.Issue) (int, error) {
	if number, err := existingGitHubPR(repo, branch); err == nil && number > 0 {
		return number, nil
	}

	title := fmt.Sprintf("%s: %s", issue.Identifier, issue.Title)
	body := fmt.Sprintf("Automated PR for %s, opened by simphony.", issue.Identifier)
	if issue.URL != nil && strings.TrimSpace(*issue.URL) != "" {
		body = fmt.Sprintf("%s\n\n%s", body, *issue.URL)
	}

	var out bytes.Buffer
	cmd := exec.Command("gh", "pr", "create",
		"--base", base,
		"--head", branch,
		"--title", title,
		"--body", body,
	)
	cmd.Dir = repo
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Another attempt may have created the PR between our check above
		// and this call (or gh reports "already exists" as an error) —
		// check once more before giving up.
		if number, err2 := existingGitHubPR(repo, branch); err2 == nil && number > 0 {
			return number, nil
		}
		return 0, fmt.Errorf("gh pr create failed: %w\noutput: %s", err, out.String())
	}

	return existingGitHubPR(repo, branch)
}

// existingGitHubPR returns the open PR number for branch, or an error if
// none exists.
func existingGitHubPR(repo string, branch string) (int, error) {
	var out bytes.Buffer
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "number")
	cmd.Dir = repo
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("gh pr view failed: %w\noutput: %s", err, out.String())
	}
	var parsed struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		return 0, fmt.Errorf("parse gh pr view output: %w\noutput: %s", err, out.String())
	}
	if parsed.Number == 0 {
		return 0, fmt.Errorf("no PR number in gh pr view output: %s", out.String())
	}
	return parsed.Number, nil
}

// waitForGitHubChecks blocks until all checks on the PR complete, returning
// an error if any failed or the timeout elapsed first.
func waitForGitHubChecks(repo string, prNumber int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "gh", "pr", "checks", strconv.Itoa(prNumber), "--watch", "--fail-fast")
	cmd.Dir = repo
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s waiting for checks: %w\noutput: %s", timeout, err, out.String())
		}
		return fmt.Errorf("checks failed: %w\noutput: %s", err, out.String())
	}
	return nil
}

// mergeGitHubPR merges the PR using the configured method. The branch is
// deliberately left on origin (--delete-branch=false) so it remains
// available for post-hoc inspection; workspace cleanup is handled
// separately by RemoveWorkspace/cleanup_worktrees.
func mergeGitHubPR(repo string, prNumber int, method string) error {
	flag := "--squash"
	switch method {
	case "merge":
		flag = "--merge"
	case "rebase":
		flag = "--rebase"
	}
	var out bytes.Buffer
	cmd := exec.Command("gh", "pr", "merge", strconv.Itoa(prNumber), flag, "--delete-branch=false")
	cmd.Dir = repo
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr merge failed: %w\noutput: %s", err, out.String())
	}
	return nil
}

// RunVerifyCommands runs the project's configured verification commands
// (lint, typecheck, test, build — whatever verify.commands declares) inside
// repoPath, in order, stopping at the first failure. It is a stack-agnostic
// deterministic gate: simphony does not know or care what the commands do,
// only whether they exit zero. No-op if no commands are configured.
func (m *Manager) RunVerifyCommands(repoPath string, commands []string, timeoutMs int) error {
	if len(commands) == 0 {
		return nil
	}
	if timeoutMs <= 0 {
		timeoutMs = 600000
	}
	for i, command := range commands {
		trimmed := strings.TrimSpace(command)
		if trimmed == "" {
			continue
		}
		if err := runScriptWithTimeout(trimmed, repoPath, timeoutMs); err != nil {
			return fmt.Errorf("verify command %d/%d (%q) failed: %w", i+1, len(commands), trimmed, err)
		}
	}
	return nil
}

// currentCommitSHA returns the full SHA of HEAD in repo.
func currentCommitSHA(repo string) (string, error) {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse HEAD failed: %w\noutput: %s", err, out.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// repoIsDirty reports whether the repo has uncommitted changes to tracked
// files (staged or unstaged). Untracked files are deliberately ignored: they
// cannot be silently clobbered by "git checkout <base>" or "git merge" (git
// refuses those operations itself, with its own clear error, if an untracked
// file would actually be overwritten), so treating them as blocking would
// only cause merges to fail on incidental build artifacts (e.g. a stray
// node_modules/ or package-lock.json left in the main checkout) that pose no
// real risk to the merge.
func repoIsDirty(repo string) (bool, error) {
	cmd := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("git status failed: %w\noutput: %s", err, out.String())
	}
	return strings.TrimSpace(out.String()) != "", nil
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
