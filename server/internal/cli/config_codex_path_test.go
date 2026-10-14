package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIConfigCodexPathValidationAndRoundTrip(t *testing.T) {
	redirectTestHome(t, t.TempDir())
	name := "native-codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("test-created executable; never invoked"), 0o755); err != nil {
		t.Fatal(err)
	}
	valid, err := ValidateCodexPath(path)
	if err != nil || valid != path {
		t.Fatalf("validate executable = %q, %v", valid, err)
	}
	if err := SaveCLIConfigForProfile(CLIConfig{CodexPath: valid}, "native-test"); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCLIConfigForProfile("native-test")
	if err != nil || loaded.CodexPath != path {
		t.Fatalf("loaded path = %q, %v", loaded.CodexPath, err)
	}
	for _, invalid := range []string{"codex", "./codex", filepath.Join(t.TempDir(), name), t.TempDir()} {
		if _, err := ValidateCodexPath(invalid); err == nil || !strings.Contains(err.Error(), "codex_path") {
			t.Fatalf("invalid path %q error = %v", invalid, err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateCodexPath(path); err == nil {
			t.Fatal("non-executable file accepted")
		}
	}
	if value, err := ValidateCodexPath(""); err != nil || value != "" {
		t.Fatalf("clear = %q, %v", value, err)
	}
}
