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

	"simphony/pkg/api"
)

var sanitizeRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Manager manages per-issue filesystem workspaces and lifecycle hooks.
type Manager struct {
	root string
}

// NewManager creates a new Manager with the given workspace root.
// The root is converted to an absolute path if necessary, and created if it doesn't exist.
func NewManager(root string) (*Manager, error) {
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
	return &Manager{root: absRoot}, nil
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

// GetWorkspacePath returns the absolute workspace path for an issue identifier.
func (m *Manager) GetWorkspacePath(issueIdentifier string) string {
	return filepath.Join(m.root, sanitizeKey(issueIdentifier))
}

func sanitizeKey(identifier string) string {
	return sanitizeRe.ReplaceAllString(identifier, "_")
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
