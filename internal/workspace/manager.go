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

const (
	maxHookOutputBytes  = 32 * 1024
	hookOutputHeadBytes = 8 * 1024
)

var ansiControlRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

type boundedOutputBuffer struct {
	mu        sync.Mutex
	maxBytes  int
	headLimit int
	tailLimit int
	total     int64
	head      []byte
	tail      []byte
}

func newBoundedOutputBuffer(maxBytes int, headBytes int) *boundedOutputBuffer {
	if maxBytes <= 0 {
		maxBytes = maxHookOutputBytes
	}
	if headBytes < 0 {
		headBytes = 0
	}
	if headBytes > maxBytes {
		headBytes = maxBytes
	}
	return &boundedOutputBuffer{maxBytes: maxBytes, headLimit: headBytes, tailLimit: maxBytes - headBytes}
}

func (b *boundedOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(p)
	b.total += int64(written)
	if remaining := b.headLimit - len(b.head); remaining > 0 {
		count := remaining
		if count > len(p) {
			count = len(p)
		}
		b.head = append(b.head, p[:count]...)
		p = p[count:]
	}
	if len(p) == 0 || b.tailLimit == 0 {
		return written, nil
	}
	if len(p) >= b.tailLimit {
		b.tail = append(b.tail[:0], p[len(p)-b.tailLimit:]...)
		return written, nil
	}
	b.tail = append(b.tail, p...)
	if overflow := len(b.tail) - b.tailLimit; overflow > 0 {
		copy(b.tail, b.tail[overflow:])
		b.tail = b.tail[:b.tailLimit]
	}
	return written, nil
}

func (b *boundedOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	clean := func(value []byte) string {
		return ansiControlRe.ReplaceAllString(strings.ToValidUTF8(string(value), "�"), "")
	}
	head := clean(b.head)
	tail := clean(b.tail)
	if b.total <= int64(b.maxBytes) {
		return head + tail
	}
	marker := fmt.Sprintf("\n...[output truncated: original_bytes=%d retained_head_bytes=%d retained_tail_bytes=%d]...\n", b.total, len(b.head), len(b.tail))
	return head + marker + tail
}

// Manager manages per-issue filesystem workspaces and lifecycle hooks.
type Manager struct {
	mergeMu          sync.Mutex
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

// MergeWorkspaceToBaseBranch merges a workspace issue branch into the configured base branch.
// It is no-op unless the workspace mode is git_worktree.
func (m *Manager) MergeWorkspaceToBaseBranch(issue api.Issue, workspacePath string) error {
	return m.MergeWorkspaceToBaseBranchVerified(issue, workspacePath, nil)
}

// MergeWorkspaceToBaseBranchVerified merges an issue branch locally, runs an
// optional verification callback against the tentative merged result, and
// rolls the merge back if verification fails.
func (m *Manager) MergeWorkspaceToBaseBranchVerified(issue api.Issue, workspacePath string, verify func(repoPath string) error) error {
	if m.mode != "git_worktree" {
		return nil
	}
	m.mergeMu.Lock()
	defer m.mergeMu.Unlock()

	key := sanitizeKey(issue.Identifier)
	if key == "" {
		return fmt.Errorf("issue identifier is required for merge")
	}
	if strings.TrimSpace(workspacePath) == "" {
		workspacePath = m.GetWorkspacePath(issue.Identifier)
	}
	if !isInsideRoot(m.root, workspacePath) {
		return fmt.Errorf("workspace path %s escapes root %s", workspacePath, m.root)
	}
	if !isGitWorktree(workspacePath) {
		return fmt.Errorf("workspace path %s is not a git worktree", workspacePath)
	}

	baseBranch := strings.TrimSpace(m.baseBranch)
	if baseBranch == "" {
		baseBranch = "main"
	}
	branch := m.branchName(issue, key)

	if !branchExists(m.repo, branch) {
		return fmt.Errorf("workspace branch %q does not exist", branch)
	}
	if !branchExists(m.repo, baseBranch) {
		return fmt.Errorf("base branch %q does not exist", baseBranch)
	}

	if err := runGit(m.repo, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout base branch %q: %w", baseBranch, err)
	}
	preMergeSHA, err := currentCommitSHA(m.repo)
	if err != nil {
		return fmt.Errorf("resolve pre-merge commit: %w", err)
	}
	mergedNow := runGit(m.repo, "merge-base", "--is-ancestor", branch, baseBranch) != nil
	if mergedNow {
		if err := runGit(m.repo, "merge", "--no-ff", "--no-edit", branch); err != nil {
			_ = runGit(m.repo, "merge", "--abort")
			return fmt.Errorf("merge branch %q into %q: %w", branch, baseBranch, err)
		}
	}
	if verify != nil {
		if err := verify(m.repo); err != nil {
			if mergedNow {
				_ = runGit(m.repo, "reset", "--hard", preMergeSHA)
			}
			return fmt.Errorf("verify failed on merged result (rolled back): %w", err)
		}
	}
	if hasRemote, err := gitRemoteConfigured(m.repo); err != nil {
		return fmt.Errorf("check for remote origin: %w", err)
	} else if hasRemote {
		if err := runGit(m.repo, "push", "origin", baseBranch); err != nil {
			return fmt.Errorf("push branch %q: %w", baseBranch, err)
		}
	}

	return nil
}

// MergeIssueBranchViaGitHubPR verifies and pushes the issue branch, opens or
// reuses a PR, waits for checks, merges it, and refreshes the local base branch.
func (m *Manager) MergeIssueBranchViaGitHubPR(issue api.Issue, workspacePath string, verify func(repoPath string) error, cfg api.GitHubConfig) error {
	if m.mode != "git_worktree" {
		return nil
	}
	key := sanitizeKey(issue.Identifier)
	branch := m.branchName(issue, key)
	if branch == "" || branch == m.baseBranch || !branchExists(m.repo, branch) {
		return fmt.Errorf("invalid or missing workspace branch %q for issue %s", branch, issue.Identifier)
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
	timeout := time.Duration(cfg.ChecksTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	grace := time.Duration(cfg.ChecksRegistrationGraceMs) * time.Millisecond
	if grace < 0 {
		grace = 0
	}
	if err := waitForGitHubChecks(m.repo, prNumber, timeout, grace); err != nil {
		return fmt.Errorf("PR #%d checks did not pass: %w", prNumber, err)
	}
	if err := mergeGitHubPR(m.repo, prNumber, cfg.MergeMethod); err != nil {
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

// RunVerifyCommands runs configured verification commands in order.
func (m *Manager) RunVerifyCommands(repoPath string, commands []string, timeoutMs int) error {
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

func ensureGitHubPR(repo string, branch string, base string, issue api.Issue) (int, error) {
	if number, err := existingGitHubPR(repo, branch); err == nil && number > 0 {
		return number, nil
	}
	title := fmt.Sprintf("%s: %s", issue.Identifier, issue.Title)
	body := fmt.Sprintf("Automated PR for %s, opened by Simphony.", issue.Identifier)
	if issue.URL != nil && strings.TrimSpace(*issue.URL) != "" {
		body += "\n\n" + *issue.URL
	}
	out := newBoundedOutputBuffer(maxHookOutputBytes, hookOutputHeadBytes)
	cmd := exec.Command("gh", "pr", "create", "--base", base, "--head", branch, "--title", title, "--body", body)
	cmd.Dir, cmd.Stdout, cmd.Stderr = repo, out, out
	if err := cmd.Run(); err != nil {
		if number, retryErr := existingGitHubPR(repo, branch); retryErr == nil && number > 0 {
			return number, nil
		}
		return 0, fmt.Errorf("gh pr create failed: %w\noutput: %s", err, out.String())
	}
	return existingGitHubPR(repo, branch)
}

func existingGitHubPR(repo string, branch string) (int, error) {
	out := newBoundedOutputBuffer(maxHookOutputBytes, hookOutputHeadBytes)
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "number")
	cmd.Dir, cmd.Stdout, cmd.Stderr = repo, out, out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("gh pr view failed: %w\noutput: %s", err, out.String())
	}
	var parsed struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		return 0, fmt.Errorf("parse gh pr view output: %w\noutput: %s", err, out.String())
	}
	if parsed.Number == 0 {
		return 0, fmt.Errorf("no PR number in gh pr view output: %s", out.String())
	}
	return parsed.Number, nil
}

type githubChecksRunFunc func(ctx context.Context, watch bool) (string, error)

func waitForGitHubChecks(repo string, prNumber int, timeout time.Duration, registrationGrace time.Duration) error {
	runner := func(ctx context.Context, watch bool) (string, error) {
		args := []string{"pr", "checks", strconv.Itoa(prNumber)}
		if watch {
			args = append(args, "--watch", "--fail-fast")
		}
		out := newBoundedOutputBuffer(maxHookOutputBytes, hookOutputHeadBytes)
		cmd := exec.CommandContext(ctx, "gh", args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = repo, out, out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	}
	return waitForGitHubChecksWithRunner(timeout, registrationGrace, 2*time.Second, runner)
}

func waitForGitHubChecksWithRunner(timeout time.Duration, registrationGrace time.Duration, pollInterval time.Duration, run githubChecksRunFunc) error {
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	if registrationGrace < 0 {
		registrationGrace = 0
	}
	if registrationGrace > timeout {
		registrationGrace = timeout
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	registrationDeadline := time.Now().Add(registrationGrace)
	var lastOutput string
	for {
		output, err := run(ctx, false)
		lastOutput = output
		if !isNoChecksReported(output, err) {
			watchOutput, watchErr := run(ctx, true)
			if watchErr == nil {
				return nil
			}
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("checks_timeout after %s waiting for registered checks: %w\noutput: %s", timeout, watchErr, watchOutput)
			}
			return fmt.Errorf("checks_failed: %w\noutput: %s", watchErr, watchOutput)
		}
		if registrationGrace == 0 || !time.Now().Before(registrationDeadline) {
			return fmt.Errorf("checks_not_registered after %s grace period\noutput: %s", registrationGrace, lastOutput)
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("checks_timeout after %s while waiting for checks to register\noutput: %s", timeout, lastOutput)
		case <-timer.C:
		}
	}
}

func isNoChecksReported(output string, err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(output + "\n" + err.Error())
	return strings.Contains(message, "no checks reported") || strings.Contains(message, "no checks were reported")
}

func mergeGitHubPR(repo string, prNumber int, method string) error {
	flag := "--squash"
	switch method {
	case "merge":
		flag = "--merge"
	case "rebase":
		flag = "--rebase"
	}
	out := newBoundedOutputBuffer(maxHookOutputBytes, hookOutputHeadBytes)
	cmd := exec.Command("gh", "pr", "merge", strconv.Itoa(prNumber), flag, "--delete-branch=false")
	cmd.Dir, cmd.Stdout, cmd.Stderr = repo, out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr merge failed: %w\noutput: %s", err, out.String())
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

	out := newBoundedOutputBuffer(maxHookOutputBytes, hookOutputHeadBytes)
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Run(); err != nil {
		output := strings.TrimSpace(out.String())
		if ctx.Err() == context.DeadlineExceeded {
			if output != "" {
				return fmt.Errorf("hook timed out after %dms: %w\noutput: %s", timeoutMs, err, output)
			}
			return fmt.Errorf("hook timed out after %dms: %w", timeoutMs, err)
		}
		if output == "" {
			return fmt.Errorf("hook exited with error: %w", err)
		}
		return fmt.Errorf("hook exited with error: %w\noutput: %s", err, output)
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

func currentCommitSHA(repo string) (string, error) {
	out, err := runGitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitRemoteConfigured(repo string) (bool, error) {
	out, err := runGitOutput(repo, "remote")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
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
