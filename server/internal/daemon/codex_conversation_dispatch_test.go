package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCodexConversationDispatchPinsModeAndSameTaskRoot(t *testing.T) {
	for _, initialShared := range []bool{false, true} {
		t.Run(map[bool]string{false: "isolated", true: "shared"}[initialShared], func(t *testing.T) {
			d, logPath, home := newCodexConversationDispatchFixture(t)
			d.cfg.CodexSharedHome = initialShared
			firstTask := leaderReuseTestTask("mode-first")
			first, err := d.runTask(context.Background(), firstTask, "codex", 0, d.logger)
			if err != nil || first.SessionID == "" {
				t.Fatalf("first native conversation: %+v, %v", first, err)
			}
			originalHome := readCodexDispatchRequests(t, logPath)[0].Home
			for i, configured := range []bool{!initialShared, initialShared, !initialShared} {
				d.cfg.CodexSharedHome = configured
				next := leaderReuseTestTask("mode-next-" + string(rune('a'+i)))
				if i == 1 {
					next.ID = firstTask.ID // redispatch onto the claimed original root
				}
				next.PriorSessionID, next.PriorWorkDir = first.SessionID, first.WorkDir
				result, err := d.runTask(context.Background(), next, "codex", 0, d.logger)
				if err != nil || result.SessionID != first.SessionID || !sameDir(t, result.WorkDir, first.WorkDir) {
					t.Fatalf("flip %d replaced native conversation or root: %+v, %v", i, result, err)
				}
			}
			requests := readCodexDispatchRequests(t, logPath)
			if len(requests) != 4 {
				t.Fatalf("native thread request count = %d", len(requests))
			}
			for i, request := range requests {
				if !sameDir(t, request.Home, originalHome) || !sameDir(t, request.SQLite, originalHome) || i > 0 && (request.Method != "thread/resume" || request.ThreadID != first.SessionID) {
					t.Fatalf("flip changed native routing: %+v", request)
				}
			}
			if shared := sameDir(t, originalHome, home); shared != initialShared {
				t.Fatalf("initial native routing shared = %v", shared)
			}
		})
	}
}

func TestCodexConversationDispatchResumesImportedOverlayAndMissingManagedRoot(t *testing.T) {
	for _, removeRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "imported_empty_overlay", true: "managed_root_removed"}[removeRoot], func(t *testing.T) {
			d, logPath, home := newCodexConversationDispatchFixture(t)
			d.cfg.CodexSharedHome = true
			first, err := d.runTask(context.Background(), leaderReuseTestTask("import-first"), "codex", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			if removeRoot {
				if err := os.RemoveAll(first.EnvRoot); err != nil {
					t.Fatal(err)
				}
			} else {
				// The authorized importer retains task config/provenance but
				// archives native entries, leaving the rollout only canonical.
				for _, name := range []string{"sessions", "archived_sessions", "thread-writer-locks", "session_index.jsonl", ".multica-codex-shared-home"} {
					if err := os.Remove(filepath.Join(first.EnvRoot, "codex-home", name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			d.cfg.CodexSharedHome = false
			next := leaderReuseTestTask("import-next")
			next.PriorSessionID, next.PriorWorkDir = first.SessionID, first.WorkDir
			result, err := d.runTask(context.Background(), next, "codex", 0, d.logger)
			if err != nil || result.SessionID != first.SessionID {
				t.Fatalf("canonical native continuation was replaced: %+v, %v", result, err)
			}
			requests := readCodexDispatchRequests(t, logPath)
			last := requests[len(requests)-1]
			if last.Method != "thread/resume" || last.ThreadID != first.SessionID || !sameDir(t, last.Home, home) {
				t.Fatalf("canonical native resume not preserved: %+v", last)
			}
		})
	}
}

func TestCodexConversationDispatchMissingLateRolloutCannotStartFresh(t *testing.T) {
	d, logPath, home := newCodexConversationDispatchFixture(t)
	d.cfg.CodexSharedHome = true
	first, err := d.runTask(context.Background(), leaderReuseTestTask("late-first"), "codex", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	previousHook := reuseBeforeUseTestHook
	t.Cleanup(func() { reuseBeforeUseTestHook = previousHook })
	reuseBeforeUseTestHook = func() {
		// Another native client can archive/remove a rollout after mode proof.
		if err := os.Remove(filepath.Join(home, "sessions", "rollout-fixture-"+first.SessionID+".jsonl")); err != nil {
			t.Fatal(err)
		}
	}
	d.cfg.CodexSharedHome = false
	next := leaderReuseTestTask("late-next")
	next.PriorSessionID, next.PriorWorkDir = first.SessionID, first.WorkDir
	if _, err := d.runTask(context.Background(), next, "codex", 0, d.logger); err == nil {
		t.Fatal("late missing native rollout silently created a replacement conversation")
	}
	if requests := readCodexDispatchRequests(t, logPath); len(requests) != 1 || requests[0].Method != "thread/start" {
		t.Fatalf("late missing native rollout reached the backend: %+v", requests)
	}
}

type codexDispatchRequest struct {
	Method, Home, SQLite, ThreadID string
}

func readCodexDispatchRequests(t *testing.T, path string) []codexDispatchRequest {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var requests []codexDispatchRequest
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var request codexDispatchRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return requests
}

func newCodexConversationDispatchFixture(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Python shebang fixture is POSIX-only")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("offline protocol fixture requires Python 3")
	}
	home, fixture := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	logPath := filepath.Join(fixture, "native-requests.jsonl")
	logLiteral, _ := json.Marshal(logPath)
	fake := filepath.Join(fixture, "codex-fixture")
	script := "#!" + python + "\n" + `import json, os, sys
if "--version" in sys.argv:
    print("codex-cli 0.162.1"); sys.exit(0)
sid = "native-" + os.environ.get("MULTICA_TASK_ID", "fixture")
def emit(value):
    print(json.dumps(value), flush=True)
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request: continue
    method, params = request.get("method"), request.get("params", {})
    result = {}
    if method in ("thread/start", "thread/resume"):
        sid = params.get("threadId", sid)
        home = os.environ["CODEX_HOME"]
        os.makedirs(os.path.join(home, "sessions"), exist_ok=True)
        rollout = os.path.join(home, "sessions", "rollout-fixture-" + sid + ".jsonl")
        if not os.path.exists(rollout):
            with open(rollout, "w") as file: file.write("offline native fixture\n")
        with open(` + string(logLiteral) + `, "a") as file:
            file.write(json.dumps({"Method":method,"ThreadID":sid,"Home":home,"SQLite":os.environ["CODEX_SQLITE_HOME"]}) + "\n")
        result = {"thread":{"id":sid}}
    emit({"jsonrpc":"2.0","id":request["id"],"result":result})
    if method == "turn/start":
        emit({"jsonrpc":"2.0","method":"turn/started","params":{"threadId":sid,"turn":{"id":"turn-fixture"}}})
        emit({"jsonrpc":"2.0","method":"item/completed","params":{"threadId":sid,"item":{"id":"message-fixture","type":"agentMessage","text":"done"}}})
        emit({"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":sid,"turn":{"id":"turn-fixture","status":"completed"}}})
`
	writeTestExecutable(t, fake, []byte(script))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	t.Cleanup(srv.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &Daemon{client: NewClient(srv.URL), logger: logger, workspaces: make(map[string]*workspaceState), runtimeIndex: map[string]Runtime{"rt-leader": {ID: "rt-leader", Provider: "codex"}}, activeEnvRoots: make(map[string]int), activeStores: make(map[string]int), agentVersions: map[string]string{"codex": "0.162.1"},
		cfg: Config{WorkspacesRoot: t.TempDir(), AgentTimeout: 5 * time.Second, ServerBaseURL: srv.URL, Agents: map[string]AgentEntry{"codex": {Path: fake}}}}
	return d, logPath, home
}
