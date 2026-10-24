package agentruntime

import (
	"os"
	"path/filepath"
)

const claudeSDKModulePath = "node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs"

// FindClaudeSDKPackage locates the Claude Agent SDK entry point relative to
// the Simphony working directory or executable. The returned absolute path can
// be imported directly by the workspace-local Node.js shim.
func FindClaudeSDKPackage() string {
	starts := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(executable))
	}
	for _, start := range starts {
		if path := findClaudeSDKPackage(start); path != "" {
			return path
		}
	}
	return ""
}

func findClaudeSDKPackage(start string) string {
	dir := filepath.Clean(start)
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		candidate := filepath.Join(dir, claudeSDKModulePath)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			path, err := filepath.Abs(candidate)
			if err == nil {
				return path
			}
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
