package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func isolateCodexPathDiscovery(t *testing.T) {
	t.Helper()
	redirectTestHome(t, t.TempDir())
	missingDir := t.TempDir()
	for _, prefix := range []string{
		"CLAUDE", "OPENCODE", "CODEARTS", "DEVECO", "OPENCLAW", "HERMES", "PI", "OMP", "CURSOR", "COPILOT",
		"KIMI", "REASONIX", "DSH", "KIRO", "CODEBUDDY", "ANTIGRAVITY", "QODER", "QODERCLICN", "TRAECLI", "GROK",
		"QWEN", "QWENPAW", "DIM", "MCODE", "ZEROCLAW",
	} {
		t.Setenv("MULTICA_"+prefix+"_PATH", filepath.Join(missingDir, strings.ToLower(prefix)))
	}
	t.Setenv("PATH", missingDir)
	t.Setenv("MULTICA_CODEX_PATH", "")
	t.Setenv("MULTICA_DAEMON_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("MULTICA_CODEX_SHARED_HOME", "false")
}

func writeCodexPathCandidate(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("fake Codex; must not execute"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveAgentExecutablePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func codexPathCandidate(t *testing.T) string {
	t.Helper()
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(t.TempDir(), name)
}

func TestLoadConfigCodexPathProfileAndEnvironmentPrecedence(t *testing.T) {
	isolateCodexPathDiscovery(t)
	profilePath := codexPathCandidate(t)
	profileResolved := writeCodexPathCandidate(t, profilePath)
	envPath := codexPathCandidate(t)
	envResolved := writeCodexPathCandidate(t, envPath)
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{CodexPath: profilePath}, "native-test"); err != nil {
		t.Fatal(err)
	}
	load := func() Config {
		t.Helper()
		cfg, err := LoadConfig(Overrides{Profile: "native-test", WorkspacesRoot: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	cfg := load()
	if cfg.CodexPath != profilePath || cfg.Agents["codex"].Path != profileResolved || cfg.Agents["codex"].Command != profilePath {
		t.Fatalf("profile pin not selected: config pin %q, agent %+v", cfg.CodexPath, cfg.Agents["codex"])
	}
	if os.Getenv("MULTICA_CODEX_PATH") != "" {
		t.Fatal("profile resolution mutated global environment")
	}
	t.Setenv("MULTICA_CODEX_PATH", envPath)
	if got := load().Agents["codex"]; got.Path != envResolved || got.Command != envPath {
		t.Fatalf("env override not selected: %+v", got)
	}
	// An environment pin must also outrank a stale saved executable.
	if err := os.Remove(profilePath); err != nil {
		t.Fatal(err)
	}
	if got := load().Agents["codex"].Path; got != envResolved {
		t.Fatalf("stale profile beat environment: %q", got)
	}
	t.Setenv("MULTICA_CODEX_PATH", "")
	if _, err := LoadConfig(Overrides{Profile: "native-test", AllowNoAgents: true}); err == nil || !strings.Contains(err.Error(), "codex_path") {
		t.Fatalf("stale configured executable error = %v", err)
	}
}

func TestRefreshAgentAvailabilityUsesCodexProfilePin(t *testing.T) {
	isolateCodexPathDiscovery(t)
	profilePath := codexPathCandidate(t)
	d := New(Config{CodexPath: profilePath, WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if gained := d.refreshAgentAvailability(); len(gained) != 0 {
		t.Fatalf("missing pin resolved another agent: %v", gained)
	}
	resolved := writeCodexPathCandidate(t, profilePath)
	if gained := d.refreshAgentAvailability(); len(gained) != 1 || gained[0] != "codex" || d.agents()["codex"].Path != resolved {
		t.Fatalf("periodic discovery did not select pin: %v, %+v", gained, d.agents())
	}
	if os.Getenv("MULTICA_CODEX_PATH") != "" {
		t.Fatal("periodic discovery mutated global environment")
	}
	envPath := codexPathCandidate(t)
	envResolved := writeCodexPathCandidate(t, envPath)
	t.Setenv("MULTICA_CODEX_PATH", envPath)
	d = New(Config{CodexPath: profilePath, WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.refreshAgentAvailability()
	if got := d.agents()["codex"].Path; got != envResolved {
		t.Fatalf("periodic discovery ignored env precedence: %q", got)
	}
}

func TestLoadConfigSharedCodexHomeCanonicalReadbackAndInvalidHome(t *testing.T) {
	isolateCodexPathDiscovery(t)
	t.Setenv("MULTICA_CODEX_PATH", filepath.Join(t.TempDir(), "missing-codex"))
	sharedHome := t.TempDir()
	t.Setenv("CODEX_HOME", sharedHome)
	value := true
	cfg, err := LoadConfig(Overrides{CodexSharedHome: &value, AllowNoAgents: true, WorkspacesRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(sharedHome)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CodexHome != want || !cfg.CodexSharedHome {
		t.Fatalf("shared home readback = %q, %v; want %q", cfg.CodexHome, cfg.CodexSharedHome, want)
	}
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "absent"))
	if _, err := LoadConfig(Overrides{CodexSharedHome: &value, AllowNoAgents: true}); err == nil || !strings.Contains(err.Error(), "shared Codex home") {
		t.Fatalf("invalid shared home startup error = %v", err)
	}
}
