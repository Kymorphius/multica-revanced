//go:build agentintegration

package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// TestCodexSharedHomeRealMemoryAndNativeResume spends two provider turns only
// after explicit opt-in. All native state and synthetic memories are temporary;
// the explicitly selected source home supplies configuration and copied auth.
// No MCP process, task supplement, or external-message integration is enabled.
//
// Run from server with an authenticated Codex supporting thread config maps and
// skills/extraRoots/set (the enhanced 0.162.1 binary supports these APIs):
//
//	MULTICA_RUN_REAL_AGENT_SMOKE=1 \
//	MULTICA_SHARED_CODEX_SOURCE_HOME=/absolute/codex/home \
//	MULTICA_SHARED_CODEX_BIN=/absolute/codex/binary \
//	go test -tags=agentintegration ./pkg/agent \
//	  -run '^TestCodexSharedHomeRealMemoryAndNativeResume$' -count=1 -v -timeout=5m
//
// MULTICA_SHARED_CODEX_MODEL and MULTICA_SHARED_CODEX_MODEL_PROVIDER optionally
// select a model and a provider already defined in the source configuration.
// MULTICA_SHARED_CODEX_KEEP_FAILED=1 preserves private failure evidence, excluding
// the source configuration and copied auth. Only its directory path is logged.
func TestCodexSharedHomeRealMemoryAndNativeResume(t *testing.T) {
	requireRealAgentSmoke(t) // Must precede executable lookup or account access.
	if testing.Short() {
		t.Skip("real shared-home acceptance is excluded from -short mode")
	}
	source := strings.TrimSpace(os.Getenv("MULTICA_SHARED_CODEX_SOURCE_HOME"))
	binary := strings.TrimSpace(os.Getenv("MULTICA_SHARED_CODEX_BIN"))
	if !filepath.IsAbs(source) || !filepath.IsAbs(binary) {
		t.Fatal("set explicit absolute MULTICA_SHARED_CODEX_SOURCE_HOME and MULTICA_SHARED_CODEX_BIN")
	}
	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal("the explicitly selected Codex binary is not executable")
	}
	clearSharedCodexParentIdentity(t)
	shared, private, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	var cliStdout, cliStderr bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("MULTICA_SHARED_CODEX_KEEP_FAILED") == "1" {
			preserveSharedCodexFailure(t, shared, private, cliStdout.Bytes(), cliStderr.Bytes())
		}
	})
	for _, dir := range []string{shared, private, cwd} {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sourceConfig := filepath.Join(source, "config.toml")
	configBytes := sharedCodexReadPrivateFile(t, sourceConfig)
	sourceHash := sha256.Sum256(configBytes)
	t.Cleanup(func() { assertSharedCodexHash(t, sourceConfig, sourceHash) })
	var config map[string]any
	if err := toml.Unmarshal(configBytes, &config); err != nil {
		t.Fatal("the explicitly selected source configuration is invalid TOML")
	}
	if config == nil {
		config = map[string]any{}
	}
	// Keep provider routing, but do not inherit operator tools, instructions,
	// hooks, profile overrides, keychain auth, or any persistent state path.
	for _, key := range []string{"mcp_servers", "skills", "hooks", "profiles", "projects", "notify", "sqlite_home", "codex_home", "model_instructions_file", "experimental_instructions_file", "permissions", "default_permissions", "permission_profile"} {
		delete(config, key)
	}
	config["notify"] = []string{}
	config["cli_auth_credentials_store"] = "file"
	config["sandbox_mode"], config["approval_policy"] = "read-only", "never"
	config["features"] = map[string]any{"memories": true, "codex_hooks": false, "hooks": false}
	config["memories"] = map[string]any{"use_memories": true, "generate_memories": false}
	config["shell_environment_policy"] = map[string]any{"inherit": "all"}
	config["developer_instructions"] = "This is an isolated shared-home acceptance test. Read native memories when asked, using the memory index. Do not write files or access operator resources."
	if provider := strings.TrimSpace(os.Getenv("MULTICA_SHARED_CODEX_MODEL_PROVIDER")); provider != "" {
		config["model_provider"] = provider
	}
	model := strings.TrimSpace(os.Getenv("MULTICA_SHARED_CODEX_MODEL"))
	if model != "" {
		config["model"] = model
	}
	writeSharedCodexTOML(t, filepath.Join(shared, "config.toml"), config)
	rootHash := sha256.Sum256(sharedCodexReadPrivateFile(t, filepath.Join(shared, "config.toml")))
	// Copy instead of linking auth: a token refresh can only update this test's
	// private copy. The source credential bytes are never logged or written.
	authPath := filepath.Join(source, "auth.json")
	if auth, err := os.ReadFile(authPath); err == nil {
		authHash := sha256.Sum256(auth)
		t.Cleanup(func() { assertSharedCodexHash(t, authPath, authHash) })
		writeSharedCodexPrivateFile(t, filepath.Join(shared, "auth.json"), auth)
	} else if !os.IsNotExist(err) {
		t.Fatal("could not read explicitly selected source auth.json")
	}
	memoryMarker, skillMarker, configMarker, nonce := sharedCodexRandomToken(t), sharedCodexRandomToken(t), sharedCodexRandomToken(t), sharedCodexRandomToken(t)
	memoryPath := filepath.Join(shared, "memories", "MEMORY.md")
	writeSharedCodexPrivateFile(t, memoryPath, []byte("# Task Group: Multica isolated shared-home acceptance\n\nThe native memory marker is "+memoryMarker+".\n"))
	writeSharedCodexPrivateFile(t, filepath.Join(shared, "memories", "memory_summary.md"), []byte("v1\n\n## User Profile\n\nThis profile is a synthetic Multica acceptance fixture.\n\n## What's in Memory\n\n- Multica isolated shared-home acceptance: read MEMORY.md for the native memory marker.\n"))
	memoryHash := sha256.Sum256(sharedCodexReadPrivateFile(t, memoryPath))
	skillPath := filepath.Join(private, "skills", "multica-shared-probe", "SKILL.md")
	writeSharedCodexPrivateFile(t, skillPath, []byte("---\nname: multica-shared-probe\ndescription: Read this synthetic fixture when asked to use the private shared-home probe skill.\n---\n\nThe private skill marker is "+skillMarker+". Include it in the requested final response.\n"))
	config["developer_instructions"] = config["developer_instructions"].(string) + " The private task configuration marker is " + configMarker + "."
	writeSharedCodexTOML(t, filepath.Join(private, "config.toml"), config)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	backend := &codexBackend{cfg: Config{
		ExecutablePath: resolvedBinary,
		Env:            map[string]string{"CODEX_HOME": shared, "CODEX_SQLITE_HOME": shared, codexTaskConfigHomeEnv: private},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)), TaskID: "shared-home-real-acceptance",
	}}
	prompt := "Read the native memory index for the synthetic Multica shared-home acceptance marker. Use $multica-shared-probe. " +
		"Remember this conversation nonce: " + nonce + ". Return the memory marker, private skill marker, private task configuration marker, and nonce. Do not modify files or use network tools."
	session, err := backend.executeOnce(ctx, prompt, ExecOptions{
		Cwd: cwd, Model: model, Timeout: 3 * time.Minute, McpConfig: json.RawMessage(`{"mcpServers":{}}`),
	}, 1)
	if err != nil {
		t.Fatalf("start isolated shared-home backend: %v", err)
	}
	result, observedID, memoryRead := drainSharedCodexSession(t, ctx, session, memoryMarker)
	if result.Status != "completed" || result.SessionID == "" || result.SessionID != observedID {
		t.Fatalf("backend did not complete with its observed native session ID: status=%q observed=%t result=%t", result.Status, observedID != "", result.SessionID != "")
	}
	for _, marker := range []string{memoryMarker, skillMarker, configMarker, nonce} {
		if !strings.Contains(result.Output, marker) {
			t.Fatal("backend response missed a synthetic memory, private skill, private config, or conversation marker")
		}
	}
	if !memoryRead {
		t.Fatal("native memory marker was not observed in a tool result")
	}
	assertSharedCodexStorage(t, shared, private, result.SessionID, nonce)
	assertSharedCodexHash(t, filepath.Join(shared, "config.toml"), rootHash)
	assertSharedCodexHash(t, memoryPath, memoryHash)
	// The second client has no private configuration environment or skill root.
	// Its prompt carries neither remembered value and forbids tool reads, so
	// success must come from the exact persisted conversation it resumes.
	args := []string{"-c", `developer_instructions="This is a continuation acceptance. Use already-loaded conversation only, do not call tools or read any files."`,
		"-c", "memories.use_memories=false", "exec", "--json", "--skip-git-repo-check", "-C", cwd, "resume", result.SessionID,
		"Use only this conversation's prior context; do not use tools. Return the conversation nonce and native memory marker from the previous turn."}
	cmd := exec.CommandContext(ctx, resolvedBinary, args...)
	cmd.Dir, cmd.Env = cwd, buildEnv(map[string]string{"CODEX_HOME": shared, "CODEX_SQLITE_HOME": shared})
	cmd.WaitDelay = 10 * time.Second
	cmd.Stdout, cmd.Stderr = &cliStdout, &cliStderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("independent native CLI resume failed: %v (provider diagnostics withheld)", err)
	}
	assertSharedCodexResumeEvents(t, cliStdout.Bytes(), result.SessionID, nonce, memoryMarker)
	assertSharedCodexStorage(t, shared, private, result.SessionID, nonce)
	assertSharedCodexHash(t, filepath.Join(shared, "config.toml"), rootHash)
	assertSharedCodexHash(t, memoryPath, memoryHash)
	t.Log("verified native memory read, private config and skill, canonical-only persistence, and exact cross-client resume; source configuration and auth were preserved")
}

func clearSharedCodexParentIdentity(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "AGENTDECK_") && key != "CODEX_THREAD_ID" && key != "CODEX_SESSION_ID" {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Setenv(key, value) })
	}
}

func sharedCodexRandomToken(t *testing.T) string {
	t.Helper()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(token[:])
}

func sharedCodexReadPrivateFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("could not read acceptance configuration or fixture")
	}
	return data
}

func writeSharedCodexPrivateFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSharedCodexTOML(t *testing.T, path string, config map[string]any) {
	t.Helper()
	data, err := toml.Marshal(config)
	if err != nil {
		t.Fatal("could not encode private acceptance configuration")
	}
	writeSharedCodexPrivateFile(t, path, data)
}

func assertSharedCodexHash(t *testing.T, path string, expected [32]byte) {
	t.Helper()
	if sha256.Sum256(sharedCodexReadPrivateFile(t, path)) != expected {
		t.Error("source or canonical configuration, credential, or memory fixture changed unexpectedly")
	}
}

func drainSharedCodexSession(t *testing.T, ctx context.Context, session *Session, memoryMarker string) (Result, string, bool) {
	t.Helper()
	messages, observedID, memoryRead := session.Messages, "", false
	observe := func(message Message) {
		if message.SessionID != "" {
			observedID = message.SessionID
		}
		if message.Type == MessageToolResult && strings.Contains(message.Output+message.Content, memoryMarker) {
			memoryRead = true
		}
	}
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				messages = nil
				continue
			}
			observe(message)
		case result := <-session.Result:
			// Messages closes before Result is delivered, but select can choose
			// Result while buffered messages remain. Drain their evidence too.
			if messages != nil {
				for message := range messages {
					observe(message)
				}
			}
			return result, observedID, memoryRead
		case <-ctx.Done():
			t.Fatal("isolated shared-home acceptance exceeded its three-minute deadline")
		}
	}
}

func assertSharedCodexStorage(t *testing.T, shared, private, threadID, nonce string) {
	t.Helper()
	for _, filename := range []string{"state_5.sqlite", "thread_history_1.sqlite", "memories_1.sqlite"} {
		data := sharedCodexReadPrivateFile(t, filepath.Join(shared, filename))
		if !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
			t.Fatalf("shared %s is not a native SQLite database", filename)
		}
	}
	rollouts := findCodexSessionRollouts(filepath.Join(shared, "sessions"), threadID)
	if len(rollouts) != 1 || !bytes.Contains(sharedCodexReadPrivateFile(t, rollouts[0]), []byte(nonce)) {
		t.Fatal("exact native thread rollout and conversation nonce were not persisted in the shared home")
	}
	err := filepath.WalkDir(private, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(entry.Name(), ".sqlite") || (entry.IsDir() && (entry.Name() == "sessions" || entry.Name() == "memories")) {
			return fmt.Errorf("native state escaped into the private task configuration directory")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertSharedCodexResumeEvents(t *testing.T, data []byte, threadID, nonce, memoryMarker string) {
	t.Helper()
	var observedID, answer string
	completed := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(line, &event) != nil {
			t.Fatal("native resume emitted malformed JSONL")
		}
		if event.Type == "thread.started" {
			observedID = event.ThreadID
		}
		if event.Type == "turn.completed" {
			completed = true
		}
		if event.Type == "error" || event.Type == "turn.failed" {
			t.Fatalf("native resume emitted terminal event type %q", event.Type)
		}
		// exec_events.rs defines item.error as a non-fatal diagnostic. It is
		// distinct from the terminal top-level error and turn.failed events.
		switch event.Item.Type {
		case "", "agent_message", "reasoning":
		case "error":
			t.Log("native resume emitted a non-fatal error item; diagnostic text withheld")
		case "command_execution", "file_change", "mcp_tool_call", "collab_tool_call", "web_search", "todo_list":
			t.Fatalf("native resume used item type %q instead of recalling conversation context without tools", event.Item.Type)
		default:
			t.Fatal("native resume emitted an unknown item type; update the native event contract before accepting it")
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" {
			answer += event.Item.Text
		}
	}
	if observedID != threadID || !completed || !strings.Contains(answer, nonce) || !strings.Contains(answer, memoryMarker) {
		t.Fatal("independent native CLI failed exact-session conversation and memory recall")
	}
}

func preserveSharedCodexFailure(t *testing.T, shared, private string, stdout, stderr []byte) {
	t.Helper()
	root, err := os.MkdirTemp("", "multica-shared-codex-failed-")
	if err != nil {
		t.Log("could not create private shared-home failure evidence directory")
		return
	}
	copyTree := func(source, target string) error {
		return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			destination := filepath.Join(target, relative)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0o700)
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, data, 0o600)
		})
	}
	for _, name := range []string{"sessions", "memories", "state_5.sqlite", "thread_history_1.sqlite", "memories_1.sqlite", "queue_1.sqlite", "goals_1.sqlite"} {
		if strings.HasSuffix(name, ".sqlite") {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if err := copyTree(filepath.Join(shared, name+suffix), filepath.Join(root, name+suffix)); err != nil {
					t.Log("a native database failure-evidence copy was unavailable")
				}
			}
		} else if err := copyTree(filepath.Join(shared, name), filepath.Join(root, name)); err != nil {
			t.Log("a native rollout or memory failure-evidence copy was unavailable")
		}
	}
	if err := copyTree(filepath.Join(private, "skills"), filepath.Join(root, "private-skills")); err != nil {
		t.Log("private skill failure-evidence copy was unavailable")
	}
	for name, data := range map[string][]byte{"cli-resume.jsonl": stdout, "cli-resume.stderr": stderr} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Log("native CLI failure-evidence copy was unavailable")
		}
	}
	t.Logf("private shared-home failure evidence retained at %s; configuration and auth input files excluded", root)
}
