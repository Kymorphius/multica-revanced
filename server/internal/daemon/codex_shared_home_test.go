package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestCodexDataEnvironmentKeepsPrivateConfigAndSharedNativeState(t *testing.T) {
	private, shared := t.TempDir(), t.TempDir()
	values := map[string]string{"CODEX_HOME": "wrong", "CODEX_SQLITE_HOME": "wrong"}
	env := &execenv.Environment{CodexHome: private, CodexDataHome: shared}
	if err := configureCodexDataEnvironment("codex", env, values); err != nil {
		t.Fatal(err)
	}
	if values["CODEX_HOME"] != shared || values["CODEX_SQLITE_HOME"] != shared || values["MULTICA_CODEX_TASK_CONFIG_HOME"] != private {
		t.Fatalf("unexpected data/config routing: %v", values)
	}
	for _, name := range []string{"CODEX_HOME", "CODEX_SQLITE_HOME", "MULTICA_CODEX_TASK_CONFIG_HOME"} {
		if !isBlockedEnvKey(name) {
			t.Fatalf("task custom_env can override %s", name)
		}
	}
}

func TestSharedCodexReuseRefusalStopsBeforeFreshPreparation(t *testing.T) {
	shared, root := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", shared)
	workdir := filepath.Join(root, "workdir")
	rollout := filepath.Join(root, "codex-home", "sessions", "old.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte("preserve previous conversation"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	env, err := d.reuseExecutionEnvironment(context.Background(), execenv.ReuseParams{WorkDir: workdir, Provider: "codex", CodexSharedHome: true})
	if err == nil || env != nil {
		t.Fatal("private native data refusal could fall through to fresh preparation")
	}
	data, readErr := os.ReadFile(rollout)
	if readErr != nil || string(data) != "preserve previous conversation" {
		t.Fatalf("previous conversation was changed: %v", readErr)
	}
	if _, err := os.Stat(filepath.Join(root, "codex-home", ".multica-codex-shared-home")); !os.IsNotExist(err) {
		t.Fatal("shared preparation ran after refusal")
	}
}

func TestCodexDataEnvironmentIsolatedAndInvalidHomes(t *testing.T) {
	private := t.TempDir()
	values := map[string]string{"MULTICA_CODEX_TASK_CONFIG_HOME": "stale"}
	if err := configureCodexDataEnvironment("codex", &execenv.Environment{CodexHome: private}, values); err != nil {
		t.Fatal(err)
	}
	if values["CODEX_HOME"] != private || values["CODEX_SQLITE_HOME"] != private || values["MULTICA_CODEX_TASK_CONFIG_HOME"] != "" {
		t.Fatalf("isolated routing changed: %v", values)
	}
	for _, env := range []*execenv.Environment{nil, {}, {CodexHome: "relative"}, {CodexHome: private, CodexDataHome: private}, {CodexHome: private, CodexDataHome: filepath.Join("relative", "shared")}} {
		if err := configureCodexDataEnvironment("codex", env, map[string]string{}); err == nil {
			t.Fatal("invalid data home accepted")
		}
	}
	if err := configureCodexDataEnvironment("claude", nil, nil); err != nil {
		t.Fatal(err)
	}
}
