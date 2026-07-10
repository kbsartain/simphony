// Package codexcmd resolves Codex CLI commands across supported installations.
package codexcmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// CommandContext builds a direct Windows Codex process invocation. Direct
// execution avoids cmd.exe's special handling of a quoted executable path.
// The bool is false for non-Codex commands and on non-Windows platforms.
func CommandContext(ctx context.Context, command string) (*exec.Cmd, bool, error) {
	if runtime.GOOS != "windows" {
		return nil, false, nil
	}
	originalExecutable, _ := splitExecutable(strings.TrimSpace(command))
	if !isCodexExecutable(originalExecutable) {
		return nil, false, nil
	}
	resolved, err := Resolve(command)
	if err != nil {
		return nil, true, err
	}
	executable, remainder := splitExecutable(resolved)
	args, err := splitCommandArgs(remainder)
	if err != nil {
		return nil, true, err
	}
	return exec.CommandContext(ctx, executable, args...), true, nil
}

// Resolve replaces a bare Codex executable with an accessible app-managed
// copy when the Microsoft Store ChatGPT/Codex package is first on PATH.
func Resolve(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" || runtime.GOOS != "windows" {
		return command, nil
	}
	executable, remainder := splitExecutable(command)
	if !isCodexExecutable(executable) {
		return command, nil
	}
	if (filepath.IsAbs(executable) || strings.ContainsAny(executable, `/\`)) && !isPackagedWindowsAppPath(executable) && !isLegacyWinGetCodexPath(executable) {
		return command, nil
	}

	resolved, err := resolveWindowsExecutable(os.Getenv("LOCALAPPDATA"), os.Getenv("USERPROFILE"), exec.LookPath)
	if err != nil {
		return "", err
	}
	return quoteWindowsArg(resolved) + remainder, nil
}

func resolveWindowsExecutable(localAppData string, userProfile string, lookPath func(string) (string, error)) (string, error) {
	type candidate struct {
		path     string
		modTime  int64
		priority int
	}
	candidates := make([]candidate, 0, 8)
	addCandidate := func(path string, priority int) {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return
		}
		candidates = append(candidates, candidate{path: path, modTime: info.ModTime().UnixNano(), priority: priority})
	}

	if localAppData != "" {
		binDir := filepath.Join(localAppData, "OpenAI", "Codex", "bin")
		entries, _ := os.ReadDir(binDir)
		for _, entry := range entries {
			if entry.IsDir() {
				// Versioned app binaries are the real executable. Prefer them
				// over launcher copies so cancellation cannot leak a child.
				addCandidate(filepath.Join(binDir, entry.Name(), "codex.exe"), 3)
			}
		}
		addCandidate(filepath.Join(binDir, "codex.exe"), 2)
	}
	if userProfile != "" {
		addCandidate(filepath.Join(userProfile, ".codex", ".sandbox-bin", "codex.exe"), 1)
		addCandidate(filepath.Join(userProfile, ".codex", "plugins", ".plugin-appserver", "codex.exe"), 1)
	}

	if path, err := lookPath("codex"); err == nil && !isPackagedWindowsAppPath(path) {
		addCandidate(path, 0)
	}
	if path, err := lookPath("codex.exe"); err == nil && !isPackagedWindowsAppPath(path) {
		addCandidate(path, 0)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].modTime > candidates[j].modTime
	})
	if len(candidates) > 0 {
		return candidates[0].path, nil
	}

	if path, err := lookPath("codex"); err == nil && isPackagedWindowsAppPath(path) {
		return "", fmt.Errorf("packaged Codex CLI at %q is not directly executable; open the ChatGPT/Codex app once or install the standalone Codex CLI so an app-managed user copy is available", path)
	}
	return "", fmt.Errorf("Codex CLI was not found; install Codex or configure agent_runtime.command with an absolute executable path")
}

func splitCommandArgs(command string) ([]string, error) {
	var args []string
	for i := 0; i < len(command); {
		for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
			i++
		}
		if i == len(command) {
			break
		}
		var arg strings.Builder
		var quote byte
		for i < len(command) {
			current := command[i]
			if quote == 0 && (current == ' ' || current == '\t') {
				break
			}
			if current == '"' || current == '\'' {
				if quote == 0 {
					quote = current
					i++
					continue
				}
				if quote == current {
					quote = 0
					i++
					continue
				}
			}
			if current == '\\' && i+1 < len(command) && (command[i+1] == '"' || command[i+1] == '\'') {
				i++
				current = command[i]
			}
			arg.WriteByte(current)
			i++
		}
		if quote != 0 {
			return nil, fmt.Errorf("unterminated quote in Codex command arguments")
		}
		args = append(args, arg.String())
	}
	return args, nil
}

func splitExecutable(command string) (string, string) {
	if command == "" {
		return "", ""
	}
	if command[0] == '"' || command[0] == '\'' {
		quote := command[0]
		for i := 1; i < len(command); i++ {
			if command[i] == quote {
				return command[1:i], command[i+1:]
			}
		}
		return strings.Trim(command, `"'`), ""
	}
	for i, r := range command {
		if r == ' ' || r == '\t' {
			return command[:i], command[i:]
		}
	}
	return command, ""
}

func isCodexExecutable(path string) bool {
	// filepath.Base follows the host OS, so normalize Windows separators before
	// classifying a configured Windows executable on Linux CI or tooling hosts.
	base := strings.ToLower(pathpkg.Base(strings.ReplaceAll(strings.TrimSpace(path), `\`, "/")))
	return base == "codex" || base == "codex.exe" || (strings.HasPrefix(base, "codex-") && strings.HasSuffix(base, ".exe"))
}

func isPackagedWindowsAppPath(path string) bool {
	normalized := strings.ToLower(filepath.Clean(path))
	return strings.Contains(normalized, `\program files\windowsapps\openai.codex_`)
}

func isLegacyWinGetCodexPath(path string) bool {
	normalized := strings.ToLower(filepath.Clean(path))
	return strings.Contains(normalized, `\microsoft\winget\packages\openai.codex_`)
}

func quoteWindowsArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
