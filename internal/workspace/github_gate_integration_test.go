package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestGitHubMergeGateIntegration(t *testing.T) {
	repo := os.Getenv("SIMPHONY_GITHUB_GATE_REPO")
	if repo == "" {
		t.Skip("SIMPHONY_GITHUB_GATE_REPO is not set")
	}

	workspaceRoot := filepath.Join(filepath.Dir(repo), ".github-gate-workspaces")
	m, err := NewManagerWithConfig(api.WorkspaceConfig{
		Root:             workspaceRoot,
		Mode:             "git_worktree",
		Repo:             repo,
		BaseBranch:       "main",
		BranchPrefix:     "simphony-gate-test/",
		CleanupWorktrees: true,
	})
	if err != nil {
		t.Fatalf("NewManagerWithConfig: %v", err)
	}

	identifier := fmt.Sprintf("GATE-%d", time.Now().Unix())
	issue := api.Issue{Identifier: identifier, Title: "Exercise real GitHub Actions merge gate"}
	ws, err := m.PrepareWorkspace(issue)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = m.RemoveWorkspace(identifier) })

	markerPath := filepath.Join(ws.Path, "merge-gate-result.txt")
	if err := os.WriteFile(markerPath, []byte("Simphony GitHub merge gate passed.\n"), 0o644); err != nil {
		t.Fatalf("write integration marker: %v", err)
	}
	runGitForTest(t, ws.Path, "add", "merge-gate-result.txt")
	runGitForTest(t, ws.Path, "commit", "-m", "Exercise Simphony merge gate")

	verify := func(repoPath string) error {
		data, err := os.ReadFile(filepath.Join(repoPath, "merge-gate-result.txt"))
		if err != nil {
			return err
		}
		if string(data) != "Simphony GitHub merge gate passed.\n" {
			return fmt.Errorf("unexpected integration marker %q", data)
		}
		return nil
	}
	err = m.MergeIssueBranchViaGitHubPR(issue, ws.Path, verify, api.GitHubConfig{
		Enabled:                   true,
		MergeMethod:               "squash",
		ChecksTimeoutMs:           300_000,
		ChecksRegistrationGraceMs: 60_000,
	})
	if err != nil {
		t.Fatalf("MergeIssueBranchViaGitHubPR: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "merge-gate-result.txt")); err != nil {
		t.Fatalf("merged marker is not present on refreshed main: %v", err)
	}
}
