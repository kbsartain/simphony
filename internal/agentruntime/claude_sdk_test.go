package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindClaudeSDKPackageWalksAncestors(t *testing.T) {
	root := t.TempDir()
	sdkPath := filepath.Join(root, claudeSDKModulePath)
	if err := os.MkdirAll(filepath.Dir(sdkPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sdkPath, []byte("export {};\n"), 0644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "workspaces", "ISSUE-1")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	got := findClaudeSDKPackage(nested)
	if got != sdkPath {
		t.Fatalf("findClaudeSDKPackage(%q) = %q, want %q", nested, got, sdkPath)
	}
}

func TestFindClaudeSDKPackageReturnsEmptyWhenMissing(t *testing.T) {
	if got := findClaudeSDKPackage(t.TempDir()); got != "" {
		t.Fatalf("findClaudeSDKPackage returned %q for missing SDK", got)
	}
}
