package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestResolveCodexSharedHomePrecedenceAndStrictEnvironment(t *testing.T) {
	valueTrue, valueFalse := true, false
	cases := []struct {
		name      string
		env       *string
		override  *bool
		persisted *bool
		want      bool
		wantError bool
	}{
		{name: "default isolated"},
		{name: "persisted enabled", persisted: &valueTrue, want: true},
		{name: "persisted disabled", persisted: &valueFalse},
		{name: "env true beats persisted false", env: sharedHomeEnv("true"), persisted: &valueFalse, want: true},
		{name: "env false beats persisted true", env: sharedHomeEnv("false"), persisted: &valueTrue},
		{name: "explicit false beats env", override: &valueFalse, env: sharedHomeEnv("true"), persisted: &valueTrue},
		{name: "explicit true beats env", override: &valueTrue, env: sharedHomeEnv("false"), want: true},
		{name: "invalid env rejected", env: sharedHomeEnv("yes"), persisted: &valueTrue, wantError: true},
		{name: "empty env rejected", env: sharedHomeEnv(""), wantError: true},
		{name: "explicit flag bypasses invalid env", override: &valueFalse, env: sharedHomeEnv("bad")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MULTICA_CODEX_SHARED_HOME", "")
			if tc.env == nil {
				if err := os.Unsetenv("MULTICA_CODEX_SHARED_HOME"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("MULTICA_CODEX_SHARED_HOME", *tc.env)
			}
			got, err := resolveCodexSharedHome(tc.override, tc.persisted)
			if (err != nil) != tc.wantError || got != tc.want {
				t.Fatalf("resolve = %v, %v; want %v, error %v", got, err, tc.want, tc.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), "MULTICA_CODEX_SHARED_HOME") {
				t.Fatalf("validation error lacks env name: %v", err)
			}
		})
	}
}

func TestLoadConfigCodexSharedHomeReadsSelectedProfile(t *testing.T) {
	redirectTestHome(t, t.TempDir())
	// Every agent path is explicit and missing so config resolution cannot
	// discover installed agents, a desktop bundle, or a user's login shell.
	missingDir := t.TempDir()
	for _, prefix := range []string{
		"CLAUDE", "CODEX", "OPENCODE", "CODEARTS", "DEVECO", "OPENCLAW", "HERMES", "PI", "OMP", "CURSOR", "COPILOT",
		"KIMI", "REASONIX", "DSH", "KIRO", "CODEBUDDY", "ANTIGRAVITY", "QODER", "QODERCLICN", "TRAECLI", "GROK",
		"QWEN", "QWENPAW", "DIM", "MCODE", "ZEROCLAW",
	} {
		t.Setenv("MULTICA_"+prefix+"_PATH", filepath.Join(missingDir, strings.ToLower(prefix)))
	}
	t.Setenv("PATH", missingDir)
	t.Setenv("MULTICA_DAEMON_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("MULTICA_CODEX_SHARED_HOME", "")
	if err := os.Unsetenv("MULTICA_CODEX_SHARED_HOME"); err != nil {
		t.Fatal(err)
	}
	valueTrue, valueFalse := true, false
	if err := cli.SaveCLIConfig(cli.CLIConfig{CodexSharedHome: &valueFalse}); err != nil {
		t.Fatal(err)
	}
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{CodexSharedHome: &valueTrue}, "shared-test"); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"", "shared-test", "absent"} {
		cfg, err := LoadConfig(Overrides{Profile: profile, ServerURL: "http://localhost:0", WorkspacesRoot: t.TempDir(), AllowNoAgents: true})
		if err != nil {
			t.Fatalf("profile %q: %v", profile, err)
		}
		if cfg.CodexSharedHome != (profile == "shared-test") {
			t.Fatalf("profile %q shared home = %v", profile, cfg.CodexSharedHome)
		}
		if len(cfg.Agents) != 0 {
			t.Fatalf("config test discovered agent CLIs: %+v", cfg.Agents)
		}
	}
	if _, set := os.LookupEnv("MULTICA_CODEX_SHARED_HOME"); set {
		t.Fatal("profile load mutated the process environment")
	}
	t.Setenv("MULTICA_CODEX_SHARED_HOME", "false")
	cfg, err := LoadConfig(Overrides{Profile: "shared-test", ServerURL: "http://localhost:0", WorkspacesRoot: t.TempDir(), AllowNoAgents: true})
	if err != nil || cfg.CodexSharedHome {
		t.Fatalf("explicit env false did not override selected profile: %v, %v", cfg.CodexSharedHome, err)
	}
	t.Setenv("MULTICA_CODEX_SHARED_HOME", "invalid")
	if _, err := LoadConfig(Overrides{AllowNoAgents: true}); err == nil || !strings.Contains(err.Error(), "MULTICA_CODEX_SHARED_HOME") {
		t.Fatalf("malformed env error = %v", err)
	}
}

func sharedHomeEnv(value string) *string { return &value }
