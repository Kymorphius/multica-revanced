package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const sharedMemoryTestConfig = `model = "fixture"
developer_instructions = "Read the common memory library."
[features]
memories = true
[memories]
generate_memories = true
use_memories = true
[mcp_servers.fixture]
command = "fixture-only"
`

func TestCodexSharedHomePrepareReuseAndCleanup(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	writeFile(t, filepath.Join(sharedHome, "skills", "global", "SKILL.md"), "Global skill stays unchanged.")
	writeFile(t, filepath.Join(sharedHome, "memories", "MEMORY.md"), "Common native memory.")
	writeFile(t, filepath.Join(sharedHome, "memories_1.sqlite"), "fixture native memory database")
	writeFile(t, filepath.Join(sharedHome, "sessions", "rollout-fixture-shared-id.jsonl"), "fixture rollout")
	t.Setenv("CODEX_HOME", sharedHome)
	t.Setenv(MulticaCodexMemoryEnv, "0")
	env, err := Prepare(PrepareParams{
		WorkspacesRoot: t.TempDir(), WorkspaceID: "ws-shared", TaskID: "task-shared",
		Provider: "codex", CodexSharedHome: true,
		Task: TaskContextForEnv{IssueID: "shared", AgentSkills: []SkillContextForEnv{{Name: "Task Writing", Content: "Task-specific writing."}}},
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(true)
	physical, err := filepath.EvalSymlinks(sharedHome)
	if err != nil || env.CodexDataHome != physical || sameResolvedCodexPath(env.CodexHome, sharedHome) {
		t.Fatalf("homes = config %q data %q, want private config and %q", env.CodexHome, env.CodexDataHome, physical)
	}
	assertSharedCodexTaskConfig(t, env.CodexHome)
	if !CodexResumeRolloutPresent(env.CodexHome, "shared-id") {
		t.Fatal("resume gate did not see the canonical rollout")
	}
	assertFileContents(t, filepath.Join(env.CodexHome, "skills", "task-writing", "SKILL.md"), "Task-specific writing.", true)
	if _, err := os.Lstat(filepath.Join(env.CodexHome, "memories")); !os.IsNotExist(err) {
		t.Fatalf("private memories should not be linked or created: %v", err)
	}
	// Reuse restores canonical memory settings even if a former task config
	// contains the daemon's old disabling blocks.
	config := filepath.Join(env.CodexHome, "config.toml")
	if err := ensureCodexMemoryConfig(config, testLogger()); err != nil {
		t.Fatal(err)
	}
	reused := Reuse(ReuseParams{WorkDir: env.WorkDir, Provider: "codex", CodexSharedHome: true, ResumeSessionID: "shared-id",
		Task: TaskContextForEnv{IssueID: "shared", AgentSkills: []SkillContextForEnv{{Name: "Task Writing", Content: "Updated task writing."}}},
	}, testLogger())
	if reused == nil || reused.CodexDataHome != physical {
		t.Fatal("Reuse did not retain the canonical data home")
	}
	assertSharedCodexTaskConfig(t, reused.CodexHome)
	assertFileContents(t, filepath.Join(reused.CodexHome, "skills", "task-writing", "SKILL.md"), "Updated task writing.", true)
	if err := reused.Cleanup(true); err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig, false)
	assertFileContents(t, filepath.Join(sharedHome, "sessions", "rollout-fixture-shared-id.jsonl"), "fixture rollout", false)
	assertFileContents(t, filepath.Join(sharedHome, "memories", "MEMORY.md"), "Common native memory.", false)
	assertFileContents(t, filepath.Join(sharedHome, "memories_1.sqlite"), "fixture native memory database", false)
	assertFileContents(t, filepath.Join(sharedHome, "skills", "global", "SKILL.md"), "Global skill stays unchanged.", false)
}

func TestCodexSharedHomeDefaultRemainsIsolated(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	t.Setenv("CODEX_HOME", sharedHome)
	// Preparation consumes an explicit setting, never the ambient opt-in env.
	t.Setenv("MULTICA_CODEX_SHARED_HOME", "1")
	t.Setenv(MulticaCodexMemoryEnv, "0")
	env, err := Prepare(PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws-isolated", TaskID: "task-isolated", Provider: "codex"}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(true)
	if env.CodexDataHome != "" {
		t.Fatal("default task unexpectedly selected a common data home")
	}
	info, err := os.Lstat(filepath.Join(env.CodexHome, "sessions"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("isolated sessions is not a real local directory: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(env.CodexHome, "config.toml"))
	if err != nil || !strings.Contains(string(data), multicaMemoryFeatureBeginMarker) {
		t.Fatalf("default memory disabling block missing: %v", err)
	}
	assertFileContents(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig, false)
}

func TestCodexSharedHomeResolvesCanonicalAliases(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	alias := filepath.Join(t.TempDir(), "codex-alias")
	if err := createDirLink(sharedHome, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", alias)
	env, err := Prepare(PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws-alias", TaskID: "task-alias", Provider: "codex", CodexSharedHome: true}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(true)
	physical, err := filepath.EvalSymlinks(sharedHome)
	if err != nil || env.CodexDataHome != physical {
		t.Fatalf("data home = %q, physical %q, err %v", env.CodexDataHome, physical, err)
	}
	// A legacy alias to a not-yet-created index is still the same shared path.
	index := filepath.Join(env.CodexHome, "session_index.jsonl")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(alias, "session_index.jsonl"), index); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexHomeWithOpts(env.CodexHome, CodexHomeOptions{SharedHome: true, GOOS: "linux"}, testLogger()); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSharedHomeRefusesPrivateNativeData(t *testing.T) {
	for _, name := range []string{"sessions/rollout.jsonl", "archived_sessions/rollout.jsonl", "thread-writer-locks/thread.lock", "session_index.jsonl", "memories/MEMORY.md", "memories_1.sqlite", "state_5.sqlite-wal"} {
		t.Run(name, func(t *testing.T) {
			sharedHome := t.TempDir()
			writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
			t.Setenv("CODEX_HOME", sharedHome)
			private := t.TempDir()
			writeFile(t, filepath.Join(private, "config.toml"), "original private config")
			writeFile(t, filepath.Join(private, name), "authoritative private data")
			err := prepareCodexHomeWithOpts(private, CodexHomeOptions{SharedHome: true, GOOS: "linux"}, testLogger())
			if err == nil || !strings.Contains(err.Error(), "archive/import") {
				t.Fatalf("expected import-required refusal, got %v", err)
			}
			assertFileContents(t, filepath.Join(private, name), "authoritative private data", false)
			assertFileContents(t, filepath.Join(private, "config.toml"), "original private config", false)
			assertFileContents(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig, false)
			if _, err := os.Lstat(filepath.Join(private, codexSharedHomeMarker)); !os.IsNotExist(err) {
				t.Fatalf("refused preflight published a marker: %v", err)
			}
		})
	}
}

func TestCodexSharedHomeRefusesForeignStoreAndGlobalConfigHome(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	t.Setenv("CODEX_HOME", sharedHome)
	private := t.TempDir()
	foreign := t.TempDir()
	writeFile(t, filepath.Join(foreign, "rollout.jsonl"), "foreign history")
	if err := createDirLink(foreign, filepath.Join(private, "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexHomeWithOpts(private, CodexHomeOptions{SharedHome: true}, testLogger()); err == nil {
		t.Fatal("foreign private store accepted")
	}
	assertFileContents(t, filepath.Join(foreign, "rollout.jsonl"), "foreign history", false)
	for _, path := range []string{sharedHome, filepath.Join(t.TempDir(), "global-alias")} {
		if path != sharedHome {
			if err := createDirLink(sharedHome, path); err != nil {
				t.Fatal(err)
			}
		}
		if err := prepareCodexHomeWithOpts(path, CodexHomeOptions{SharedHome: true}, testLogger()); err == nil {
			t.Fatalf("global config home %s accepted as private", path)
		}
	}
	assertFileContents(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig, false)
}

func TestCodexSharedHomeCannotReuseAsIsolated(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	t.Setenv("CODEX_HOME", sharedHome)
	private := t.TempDir()
	if err := prepareCodexHomeWithOpts(private, CodexHomeOptions{SharedHome: true, GOOS: "linux"}, testLogger()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sharedHome, "sessions", "rollout-fixture-id.jsonl"), "common history")
	if err := prepareCodexHome(private, testLogger()); err == nil || !strings.Contains(err.Error(), "isolated mode") {
		t.Fatalf("expected mode-change refusal, got %v", err)
	}
	if !CodexResumeRolloutPresent(private, "id") {
		t.Fatal("mode-change refusal changed the shared rollout link")
	}
	assertSharedCodexTaskConfig(t, private)
}

func TestCodexSharedHomeStripsOnlyOwnedMemoryBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := renderMulticaMemoryFeatureBlock(false) + renderMulticaMemoryConfigBlock(false) + sharedMemoryTestConfig
	writeFile(t, path, content)
	if err := stripManagedCodexMemoryConfig(path); err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, path, sharedMemoryTestConfig, false)
	assertSharedCodexTaskConfig(t, filepath.Dir(path))
}

func TestCodexSharedHomePreservesPrivateHistoryBeforeSameTaskReset(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	t.Setenv("CODEX_HOME", sharedHome)
	params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws-rerun", TaskID: "task-rerun", Provider: "codex"}
	env, err := Prepare(params, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(true)
	rollout := filepath.Join(env.CodexHome, "sessions", "rollout-private-id.jsonl")
	writeFile(t, rollout, "private history before opt-in")
	env.ReleaseLock()
	params.CodexSharedHome = true
	if _, err := Prepare(params, testLogger()); err == nil || !strings.Contains(err.Error(), "archive/import") {
		t.Fatalf("expected refusal before reset, got %v", err)
	}
	assertFileContents(t, rollout, "private history before opt-in", false)
	if err := ValidateSharedCodexTaskHome(env.CodexHome); err == nil {
		t.Fatal("preclaimed-reset validation did not detect private history")
	}
	claim, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: params.WorkspacesRoot, WorkspaceID: params.WorkspaceID, TaskID: params.TaskID, CodexSharedHome: true})
	if claim != nil {
		claim.Release()
	}
	if err == nil || !strings.Contains(err.Error(), "archive/import") {
		t.Fatalf("expected parent claim refusal before reset, got %v", err)
	}
	assertFileContents(t, rollout, "private history before opt-in", false)
}

func TestCodexSharedHomeLocalDirectoryUsesCommonHistory(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	t.Setenv("CODEX_HOME", sharedHome)
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "user-file"), "keep the local directory")
	env, err := Prepare(PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws-local", TaskID: "task-local", Provider: "codex", CodexSharedHome: true, LocalWorkDir: local,
		Task: TaskContextForEnv{AgentID: "agent-local", IssueID: "issue-local"}}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(true)
	if !sameResolvedCodexPath(filepath.Join(env.CodexHome, "sessions"), filepath.Join(sharedHome, "sessions")) {
		t.Fatal("shared local task mounted a separate issue store")
	}
	if _, err := os.Stat(filepath.Join(sharedHome, codexSessionStoreRoot)); !os.IsNotExist(err) {
		t.Fatalf("shared preparation created a separate issue store: %v", err)
	}
	if err := env.Cleanup(true); err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, filepath.Join(local, "user-file"), "keep the local directory", false)
	if _, err := os.Stat(filepath.Join(sharedHome, "sessions")); err != nil {
		t.Fatalf("cleanup removed common sessions: %v", err)
	}
}

func assertSharedCodexTaskConfig(t *testing.T, home string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), multicaMemoryFeatureBeginMarker) || strings.Contains(string(data), multicaMemoryConfigBeginMarker) {
		t.Fatal("shared config contains an owned memory disabling block")
	}
	features, _ := parsed["features"].(map[string]any)
	memories, _ := parsed["memories"].(map[string]any)
	if features["memories"] != true || memories["generate_memories"] != true || memories["use_memories"] != true {
		t.Fatalf("shared memory settings not retained: features %v memories %v", features, memories)
	}
}

func assertFileContents(t *testing.T, path, expected string, contains bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains && strings.Contains(string(data), expected) || !contains && string(data) == expected {
		return
	}
	t.Fatalf("file %s did not preserve expected contents", path)
}
