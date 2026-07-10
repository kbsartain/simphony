// Package codexcmd resolves Codex CLI commands across supported installations.
package codexcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

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
		path    string
		modTime int64
	}
	candidates := make([]candidate, 0, 8)
	addCandidate := func(path string) {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return
		}
		candidates = append(candidates, candidate{path: path, modTime: info.ModTime().UnixNano()})
	}

	if localAppData != "" {
		binDir := filepath.Join(localAppData, "OpenAI", "Codex", "bin")
		entries, _ := os.ReadDir(binDir)
		for _, entry := range entries {
			if entry.IsDir() {
				addCandidate(filepath.Join(binDir, entry.Name(), "codex.exe"))
			}
		}
		addCandidate(filepath.Join(binDir, "codex.exe"))
	}
	if userProfile != "" {
		addCandidate(filepath.Join(userProfile, ".codex", ".sandbox-bin", "codex.exe"))
		addCandidate(filepath.Join(userProfile, ".codex", "plugins", ".plugin-appserver", "codex.exe"))
	}

	if path, err := lookPath("codex"); err == nil && !isPackagedWindowsAppPath(path) {
		addCandidate(path)
	}
	if path, err := lookPath("codex.exe"); err == nil && !isPackagedWindowsAppPath(path) {
		addCandidate(path)
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].modTime > candidates[j].modTime })
	if len(candidates) > 0 {
		return candidates[0].path, nil
	}

	if path, err := lookPath("codex"); err == nil && isPackagedWindowsAppPath(path) {
		return "", fmt.Errorf("packaged Codex CLI at %q is not directly executable; open the ChatGPT/Codex app once or install the standalone Codex CLI so an app-managed user copy is available", path)
	}
	return "", fmt.Errorf("Codex CLI was not found; install Codex or configure agent_runtime.command with an absolute executable path")
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
	base := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
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
