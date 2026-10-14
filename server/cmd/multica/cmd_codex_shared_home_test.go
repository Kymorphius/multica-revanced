package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestConfigCodexSharedHomeSetShowClear(t *testing.T) {
	redirectTestHome(t, t.TempDir())
	cmd := newConfigTestCmd()
	_ = cmd.Flags().Set("profile", "shared-test")
	for _, value := range []string{"true", "false", ""} {
		if err := runConfigSet(cmd, []string{"codex_shared_home", value}); err != nil {
			t.Fatal(err)
		}
		cfg, err := cli.LoadCLIConfigForProfile("shared-test")
		if err != nil {
			t.Fatal(err)
		}
		if (cfg.CodexSharedHome == nil) != (value == "") || (value != "" && *cfg.CodexSharedHome != (value == "true")) {
			t.Fatalf("persisted preference = %v, want %q", cfg.CodexSharedHome, value)
		}
		out, err := captureStdout(t, func() error { return runConfigShow(cmd, nil) })
		if err != nil {
			t.Fatal(err)
		}
		want := value
		if value == "" {
			want = "(not set)"
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "codex_shared_home:") && strings.TrimSpace(strings.TrimPrefix(line, "codex_shared_home:")) == want {
				want = ""
				break
			}
		}
		if want != "" {
			t.Fatalf("config show missing stored shared preference %q: %s", want, out)
		}
	}
}

func TestConfigCodexSharedHomeRejectsMalformedValueWithoutMutation(t *testing.T) {
	value := true
	cfg := cli.CLIConfig{CodexSharedHome: &value, DisableAutoUpdate: true, DisableAutoReload: true}
	for _, malformed := range []string{"yes", "off", " true ", "default"} {
		if err := applyConfigSet(&cfg, "codex_shared_home", malformed); err == nil || !strings.Contains(err.Error(), "codex_shared_home") {
			t.Fatalf("value %q validation error = %v", malformed, err)
		}
		if cfg.CodexSharedHome == nil || !*cfg.CodexSharedHome || !cfg.DisableAutoUpdate || !cfg.DisableAutoReload {
			t.Fatal("invalid setting mutated configuration")
		}
	}
}

func TestDaemonCodexSharedHomeFlagAndChildForwarding(t *testing.T) {
	for _, command := range []*cobra.Command{daemonStartCmd, daemonRestartCmd} {
		if flag := command.Flags().Lookup("codex-shared-home"); flag == nil || flag.DefValue != "false" {
			t.Fatalf("%s has no default-false shared home flag", command.Name())
		}
	}
	for _, value := range []string{"unset", "true", "false"} {
		cmd := &cobra.Command{}
		cmd.Flags().Bool("codex-shared-home", false, "")
		if value != "unset" {
			if err := cmd.Flags().Set("codex-shared-home", value); err != nil {
				t.Fatal(err)
			}
		}
		pointer, err := codexSharedHomeFlag(cmd)
		if err != nil || (pointer == nil) != (value == "unset") || (pointer != nil && *pointer != (value == "true")) {
			t.Fatalf("flag %q resolved %v, %v", value, pointer, err)
		}
		args := buildDaemonStartArgs(cmd)
		want := "daemon start --foreground"
		if value != "unset" {
			want += " --codex-shared-home=" + value
		}
		if got := strings.Join(args, " "); got != want {
			t.Fatalf("child args = %q, want %q", got, want)
		}
	}
}
