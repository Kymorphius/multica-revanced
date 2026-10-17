package agent

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAgentdeck struct {
	mu       sync.Mutex
	calls    [][]string
	stdin    []string
	statuses []string
	output   string
	envSeen  string
	envMode  os.FileMode
}

func (f *fakeAgentdeck) run(_ context.Context, stdin string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	f.stdin = append(f.stdin, stdin)
	switch {
	case args[0] == "launch":
		for i, a := range args {
			if a == "--env-file" {
				data, _ := os.ReadFile(args[i+1])
				st, _ := os.Stat(args[i+1])
				f.envSeen, f.envMode = string(data), st.Mode().Perm()
			}
		}
		return []byte(`{"success":true,"id":"sess-1"}`), nil
	case args[0] == "session" && args[1] == "show":
		st := "waiting"
		if len(f.statuses) > 0 {
			st, f.statuses = f.statuses[0], f.statuses[1:]
		}
		return []byte(`{"id":"sess-1","status":"` + st + `"}`), nil
	case args[0] == "session" && args[1] == "output":
		return []byte(`{"content":` + agentdeckJSONString(f.output) + `}`), nil
	}
	return []byte(`{}`), nil
}

func agentdeckJSONString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func newFakeAgentdeckBackend(f *fakeAgentdeck) *agentdeckBackend {
	return &agentdeckBackend{
		cfg:  Config{Logger: slog.Default(), TaskID: "task-12345678", Env: map[string]string{"MULTICA_TOKEN": "mat_secret'x", "BAD-KEY": "skip", "PATH": "/App/bin:/usr/bin:/bin"}},
		run:  f.run,
		poll: 5 * time.Millisecond, settle: 10 * time.Millisecond,
	}
}

func waitResult(t *testing.T, s *Session) Result {
	t.Helper()
	go func() {
		for range s.Messages {
		}
	}()
	select {
	case r := <-s.Result:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
	return Result{}
}

func TestAgentdeckRunsTaskInSessionWithPrivateEnv(t *testing.T) {
	f := &fakeAgentdeck{statuses: []string{"running", "running", "waiting"}, output: "改好了，已提交。\n===AGENTDECK_DONE=== status=ok summary=done"}
	b := newFakeAgentdeckBackend(f)
	s, err := b.Execute(context.Background(), "修复登录", ExecOptions{Cwd: t.TempDir(), ThreadName: "PP-12 修复登录"})
	if err != nil {
		t.Fatal(err)
	}
	r := waitResult(t, s)
	if r.Status != "completed" || r.Output != "改好了，已提交。" || r.SessionID != "sess-1" {
		t.Fatalf("result = %+v", r)
	}
	launch := f.calls[0]
	joined := strings.Join(launch, " ")
	if strings.Contains(joined, "mat_secret") || !strings.Contains(joined, "-t PP-12 修复登录") || !strings.Contains(joined, "-g Multica") || f.stdin[0] != "修复登录" {
		t.Fatalf("launch = %v / stdin %q", launch, f.stdin[0])
	}
	if f.envMode != 0o600 || f.envSeen != "export MULTICA_TOKEN='mat_secret'\\''x'\nexport PATH='/App/bin':\"$PATH\"\n" {
		t.Fatalf("env file %o %q", f.envMode, f.envSeen)
	}
	for i, a := range launch {
		if a == "--env-file" {
			if _, err := os.Stat(launch[i+1]); !os.IsNotExist(err) {
				t.Fatal("env file must be removed when the task ends")
			}
		}
	}
}

func TestAgentdeckFailureSentinelAndCancel(t *testing.T) {
	if r := agentdeckResult("做不了\n===AGENTDECK_DONE=== status=fail summary=缺少权限"); r.Status != "failed" || r.Error != "缺少权限" || r.Output != "做不了" {
		t.Fatalf("fail = %+v", r)
	}
	if r := agentdeckResult("只回答了问题"); r.Status != "completed" || r.Output != "只回答了问题" {
		t.Fatalf("plain = %+v", r)
	}
	f := &fakeAgentdeck{statuses: []string{"running", "running", "running", "running", "running", "running", "running", "running"}}
	b := newFakeAgentdeckBackend(f)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := b.Execute(ctx, "x", ExecOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Supplement(context.Background(), "请顺便补测试"); err != nil {
		t.Fatal(err)
	}
	cancel()
	r := waitResult(t, s)
	if r.Status != "cancelled" {
		t.Fatalf("cancel = %+v", r)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var stopped, supplemented bool
	for i, c := range f.calls {
		stopped = stopped || (c[0] == "session" && c[1] == "stop" && c[2] == "sess-1")
		supplemented = supplemented || (c[0] == "session" && c[1] == "send" && f.stdin[i] == "请顺便补测试")
	}
	if !stopped || !supplemented {
		t.Fatalf("calls = %v", f.calls)
	}
	if agentdeckTool([]string{"--tool=codex"}) != "codex" || agentdeckTool([]string{"--tool=rm"}) != "claude" {
		t.Fatal("tool selection")
	}
}
