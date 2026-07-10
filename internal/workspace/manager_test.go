package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestNewManager(t *testing.T) {
	// Absolute path
	tmp := t.TempDir()
	m, err := NewManager(tmp)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if m.root != tmp {
		t.Errorf("expected root %s, got %s", tmp, m.root)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("root directory does not exist: %v", err)
	}

	// Relative path resolved to absolute
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	rel, err := filepath.Rel(cwd, t.TempDir())
	if err != nil {
		t.Fatalf("filepath.Rel failed: %v", err)
	}
	m2, err := NewManager(rel)
	if err != nil {
		t.Fatalf("NewManager with relative path failed: %v", err)
	}
	if !filepath.IsAbs(m2.root) {
		t.Errorf("expected absolute root, got %s", m2.root)
	}
	expected, _ := filepath.Abs(rel)
	if m2.root != expected {
		t.Errorf("expected root %s, got %s", expected, m2.root)
	}
}

func TestPrepareWorkspace(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	issue := api.Issue{Identifier: "TEST-123"}
	ws, err := m.PrepareWorkspace(issue)
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}
	expectedPath := filepath.Join(root, "TEST-123")
	if ws.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, ws.Path)
	}
	if ws.WorkspaceKey != "TEST-123" {
		t.Errorf("expected key TEST-123, got %s", ws.WorkspaceKey)
	}
	if !ws.CreatedNow {
		t.Error("expected CreatedNow=true")
	}
	if fi, err := os.Stat(ws.Path); err != nil || !fi.IsDir() {
		t.Errorf("workspace directory not created: %v", err)
	}
}

func TestPrepareWorkspace_Existing(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	key := "EXIST-456"
	path := filepath.Join(root, key)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	issue := api.Issue{Identifier: key}
	ws, err := m.PrepareWorkspace(issue)
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}
	if ws.CreatedNow {
		t.Error("expected CreatedNow=false for existing directory")
	}
}

func TestPrepareWorkspace_Sanitization(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	issue := api.Issue{Identifier: "ISSUE#1/2"}
	ws, err := m.PrepareWorkspace(issue)
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}
	expectedKey := "ISSUE_1_2"
	if ws.WorkspaceKey != expectedKey {
		t.Errorf("expected key %s, got %s", expectedKey, ws.WorkspaceKey)
	}
	expectedPath := filepath.Join(root, expectedKey)
	if ws.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, ws.Path)
	}
}

func TestPrepareWorkspace_PathEscape(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	issue := api.Issue{Identifier: ".."}
	_, err = m.PrepareWorkspace(issue)
	if err == nil {
		t.Fatal("expected error for path escape, got nil")
	}
}

func TestPrepareWorkspace_GitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := initTestRepo(t)
	root := t.TempDir()
	m, err := NewManagerWithConfig(api.WorkspaceConfig{
		Root:         root,
		Mode:         "git_worktree",
		Repo:         repo,
		BaseBranch:   "main",
		BranchPrefix: "github.com/kbsartain/simphony/",
	})
	if err != nil {
		t.Fatalf("NewManagerWithConfig failed: %v", err)
	}

	ws, err := m.PrepareWorkspace(api.Issue{Identifier: "TEST-123"})
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}
	if !ws.CreatedNow {
		t.Fatal("expected CreatedNow=true for new worktree")
	}
	if _, err := os.Stat(filepath.Join(ws.Path, ".git")); err != nil {
		t.Fatalf("expected worktree .git file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "README.md")); err != nil {
		t.Fatalf("expected repo file in worktree: %v", err)
	}

	branch := gitOutput(t, ws.Path, "branch", "--show-current")
	if branch != "github.com/kbsartain/simphony/TEST-123" {
		t.Fatalf("branch = %q, want simphony/TEST-123", branch)
	}

	reused, err := m.PrepareWorkspace(api.Issue{Identifier: "TEST-123"})
	if err != nil {
		t.Fatalf("PrepareWorkspace existing failed: %v", err)
	}
	if reused.CreatedNow {
		t.Fatal("expected CreatedNow=false for existing worktree")
	}
}

func TestPrepareWorkspace_GitWorktree_UsesIssueBranchName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := initTestRepo(t)
	root := t.TempDir()
	m, err := NewManagerWithConfig(api.WorkspaceConfig{
		Root:       root,
		Mode:       "git_worktree",
		Repo:       repo,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("NewManagerWithConfig failed: %v", err)
	}

	branchName := "feature/custom-branch"
	ws, err := m.PrepareWorkspace(api.Issue{Identifier: "TEST-124", BranchName: &branchName})
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}
	branch := gitOutput(t, ws.Path, "branch", "--show-current")
	if branch != branchName {
		t.Fatalf("branch = %q, want %q", branch, branchName)
	}
}

func TestMergeWorkspaceToBaseBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := initTestRepo(t)
	root := t.TempDir()
	m, err := NewManagerWithConfig(api.WorkspaceConfig{
		Root:       root,
		Mode:       "git_worktree",
		Repo:       repo,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("NewManagerWithConfig failed: %v", err)
	}

	issue := api.Issue{Identifier: "TEST-126"}
	ws, err := m.PrepareWorkspace(issue)
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(ws.Path, "feature.txt"), []byte("feature"), 0644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGitForTest(t, ws.Path, "add", "feature.txt")
	runGitForTest(t, ws.Path, "commit", "-m", "feature change")

	if err := m.MergeWorkspaceToBaseBranch(issue, ws.Path); err != nil {
		t.Fatalf("MergeWorkspaceToBaseBranch failed: %v", err)
	}
	branch := gitOutput(t, ws.Path, "branch", "--show-current")
	expectedBranch := m.branchName(issue, sanitizeKey(issue.Identifier))
	if branch != expectedBranch {
		t.Fatalf("expected issue worktree to remain on %q after merge, got %q", expectedBranch, branch)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("expected merged file on base worktree, got %v", err)
	}

	if err := m.MergeWorkspaceToBaseBranch(issue, ws.Path); err != nil {
		t.Fatalf("MergeWorkspaceToBaseBranch second merge failed: %v", err)
	}
}

func TestRemoveWorkspace_GitWorktreeCleanup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := initTestRepo(t)
	root := t.TempDir()
	m, err := NewManagerWithConfig(api.WorkspaceConfig{
		Root:             root,
		Mode:             "git_worktree",
		Repo:             repo,
		BaseBranch:       "main",
		CleanupWorktrees: true,
	})
	if err != nil {
		t.Fatalf("NewManagerWithConfig failed: %v", err)
	}

	ws, err := m.PrepareWorkspace(api.Issue{Identifier: "TEST-125"})
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}
	if err := m.RemoveWorkspace("TEST-125"); err != nil {
		t.Fatalf("RemoveWorkspace failed: %v", err)
	}
	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree path removed, got err=%v", err)
	}
}

func TestRemoveWorkspace(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	key := "REM-789"
	path := filepath.Join(root, key)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := m.RemoveWorkspace(key); err != nil {
		t.Fatalf("RemoveWorkspace failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected workspace to be removed, but it still exists")
	}
}

func TestRunHook_Success(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	wsPath := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	script := "echo hello"
	if err := m.RunHook("before_run", script, wsPath, 5000); err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
}

func TestRunHook_Timeout(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	wsPath := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	var script string
	if runtime.GOOS == "windows" {
		script = "for /L %i in (0,0,1) do @echo >nul"
	} else {
		script = "sleep 5"
	}
	err = m.RunHook("before_run", script, wsPath, 100)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestRunHook_Failure(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	wsPath := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	var script string
	if runtime.GOOS == "windows" {
		script = "exit /B 1"
	} else {
		script = "exit 1"
	}
	err = m.RunHook("before_run", script, wsPath, 5000)
	if err == nil {
		t.Fatal("expected failure error, got nil")
	}
}

func TestBoundedOutputBufferPreservesHeadTailAndStripsANSI(t *testing.T) {
	buffer := newBoundedOutputBuffer(80, 32)
	_, _ = buffer.Write([]byte("\x1b[32mFIRST-SUMMARY\x1b[0m\n"))
	_, _ = buffer.Write([]byte(strings.Repeat("middle-output-", 20)))
	_, _ = buffer.Write([]byte("\n\x1b[31mFINAL-FAILURE\x1b[0m\n"))

	got := buffer.String()
	if !strings.Contains(got, "FIRST-SUMMARY") || !strings.Contains(got, "FINAL-FAILURE") {
		t.Fatalf("bounded output lost head or tail: %q", got)
	}
	if !strings.Contains(got, "output truncated: original_bytes=") {
		t.Fatalf("bounded output lacks truncation marker: %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("bounded output retained ANSI controls: %q", got)
	}
}

func TestRunHookFailureRetainsLongOutputFailureTail(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	wsPath := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	var script string
	if runtime.GOOS == "windows" {
		script = `(for /L %i in (0,1,5000) do @echo passing-suite-%i) & echo FINAL_FAILURE_SUMMARY 1>&2 & exit /B 1`
	} else {
		script = `i=0; while [ $i -lt 5000 ]; do echo "passing-suite-$i"; i=$((i+1)); done; echo FINAL_FAILURE_SUMMARY >&2; exit 1`
	}

	err = m.RunHook("before_run", script, wsPath, 15000)
	if err == nil {
		t.Fatal("expected long-output hook failure")
	}
	message := err.Error()
	if !strings.Contains(message, "passing-suite-0") || !strings.Contains(message, "FINAL_FAILURE_SUMMARY") {
		t.Fatalf("hook error lost useful head or failure tail: %s", message)
	}
	if !strings.Contains(message, "output truncated: original_bytes=") {
		t.Fatalf("hook error lacks truncation metadata: %s", message)
	}
	if len(message) > maxHookOutputBytes+2048 {
		t.Fatalf("hook error length = %d, want bounded output", len(message))
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitForTest(t, repo, "init", "-b", "main")
	runGitForTest(t, repo, "config", "user.email", "test@example.com")
	runGitForTest(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitForTest(t, repo, "add", "README.md")
	runGitForTest(t, repo, "commit", "-m", "initial")
	return repo
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}
