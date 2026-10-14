package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestConfigCodexPathSetShowClearAndReject(t *testing.T) {
	redirectTestHome(t, t.TempDir())
	cmd := newConfigTestCmd()
	_ = cmd.Flags().Set("profile", "native-test")
	name := "codex-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("fake executable; never invoked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runConfigSet(cmd, []string{"codex_path", path}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return runConfigShow(cmd, nil) })
	if err != nil || !strings.Contains(out, "codex_path:") || !strings.Contains(out, path) {
		t.Fatalf("show path: %s, %v", out, err)
	}
	if err := runConfigSet(cmd, []string{"codex_path", "codex"}); err == nil {
		t.Fatal("relative command accepted as a profile pin")
	}
	cfg, err := cli.LoadCLIConfigForProfile("native-test")
	if err != nil || cfg.CodexPath != path {
		t.Fatalf("bad setting changed stored pin: %q, %v", cfg.CodexPath, err)
	}
	if err := runConfigSet(cmd, []string{"codex_path", ""}); err != nil {
		t.Fatal(err)
	}
	cfg, err = cli.LoadCLIConfigForProfile("native-test")
	if err != nil || cfg.CodexPath != "" {
		t.Fatalf("clear pin: %q, %v", cfg.CodexPath, err)
	}
}
