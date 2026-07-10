package codexcmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveWindowsExecutablePrefersNewestAppManagedCopy(t *testing.T) {
	localAppData := t.TempDir()
	oldPath := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "old", "codex.exe")
	newPath := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "new", "codex.exe")
	for _, path := range []string{oldPath, newPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWindowsExecutable(localAppData, t.TempDir(), func(string) (string, error) {
		return `C:\Program Files\WindowsApps\OpenAI.Codex_1.0.0_x64__test\app\resources\codex.exe`, nil
	})
	if err != nil {
		t.Fatalf("resolveWindowsExecutable failed: %v", err)
	}
	if got != newPath {
		t.Fatalf("resolved path = %q, want %q", got, newPath)
	}
}

func TestResolveWindowsExecutablePrefersVersionedBinaryOverNewerSandboxCopy(t *testing.T) {
	localAppData := t.TempDir()
	userProfile := t.TempDir()
	versioned := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "version", "codex.exe")
	sandbox := filepath.Join(userProfile, ".codex", ".sandbox-bin", "codex.exe")
	for _, path := range []string{versioned, sandbox} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(versioned, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWindowsExecutable(localAppData, userProfile, func(string) (string, error) {
		return sandbox, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != versioned {
		t.Fatalf("resolved path = %q, want real versioned binary %q", got, versioned)
	}
}

func TestResolveWindowsExecutableRejectsPackagedPathWithoutUserCopy(t *testing.T) {
	_, err := resolveWindowsExecutable(t.TempDir(), t.TempDir(), func(string) (string, error) {
		return `C:\Program Files\WindowsApps\OpenAI.Codex_1.0.0_x64__test\app\resources\codex.exe`, nil
	})
	if err == nil {
		t.Fatal("expected packaged executable error")
	}
}

func TestSplitExecutablePreservesArguments(t *testing.T) {
	executable, remainder := splitExecutable(`"C:\Tools\codex.exe" app-server --listen stdio://`)
	if executable != `C:\Tools\codex.exe` || remainder != ` app-server --listen stdio://` {
		t.Fatalf("splitExecutable = %q, %q", executable, remainder)
	}
}

func TestLegacyWinGetCodexPath(t *testing.T) {
	path := `C:\Users\dev\AppData\Local\Microsoft\WinGet\Packages\OpenAI.Codex_Microsoft.Winget.Source_test\codex-x86_64-pc-windows-msvc.exe`
	if !isCodexExecutable(path) || !isLegacyWinGetCodexPath(path) {
		t.Fatalf("expected legacy WinGet Codex path to be recognized")
	}
}

func TestSplitCommandArgsPreservesQuotedValues(t *testing.T) {
	args, err := splitCommandArgs(` app-server -c "model=gpt 5.6" --listen stdio://`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app-server", "-c", "model=gpt 5.6", "--listen", "stdio://"}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %#v, want %#v", args, want)
		}
	}
}
