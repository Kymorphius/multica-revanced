package agent

// agentdeckBackend runs a task as an interactive agent-deck session instead of
// spawning the coding CLI directly (fork-local, see the World Store three-way
// board design). The daemon still prepares the workdir, the runtime brief
// (CLAUDE.md) and the task-scoped MULTICA_* environment; this backend hands
// that environment to the session through `agent-deck launch --env-file`, so
// the token never appears in argv or in agent-deck's persisted command, and
// then follows the session until its turn ends.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type agentdeckBackend struct {
	cfg Config
	// run executes agent-deck; tests replace it.
	run func(ctx context.Context, stdin string, args ...string) ([]byte, error)
	// poll is the status polling interval; settle is how long a finished
	// status must hold before the turn counts as ended.
	poll, settle time.Duration
}

const agentdeckGroup = "Multica"

var (
	agentdeckSentinel = regexp.MustCompile(`(?m)^===AGENTDECK_DONE=== status=(ok|fail)(?: summary=(.*))?\s*$`)
	agentdeckEnvKey   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func (b *agentdeckBackend) exe() string {
	if b.cfg.ExecutablePath != "" {
		return b.cfg.ExecutablePath
	}
	return "agent-deck"
}

func (b *agentdeckBackend) runCmd(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	if b.run != nil {
		return b.run(ctx, stdin, args...)
	}
	cmd := exec.CommandContext(ctx, b.exe(), args...)
	hideAgentWindow(cmd)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return stdout.Bytes(), fmt.Errorf("agent-deck %s: %w %s", args[0], err, msg)
	}
	return stdout.Bytes(), nil
}

// writeAgentdeckEnv writes the task environment as `export` lines into a new
// private directory. Values are single-quoted, so nothing in them is expanded.
func writeAgentdeckEnv(env map[string]string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "multica-agentdeck-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.Chmod(dir, 0o700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		if agentdeckEnvKey.MatchString(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString("export " + k + "='" + strings.ReplaceAll(env[k], "'", `'\''`) + "'\n")
	}
	path := filepath.Join(dir, "task.env")
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// agentdeckTool picks the tool inside agent-deck from the agent's custom
// args ("--tool=codex"); claude is the default.
func agentdeckTool(args []string) string {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--tool="); ok && (v == "claude" || v == "codex") {
			return v
		}
	}
	return "claude"
}

func (b *agentdeckBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if b.run == nil {
		if _, err := exec.LookPath(b.exe()); err != nil {
			return nil, fmt.Errorf("agent-deck executable not found at %q: %w", b.exe(), err)
		}
	}
	if opts.Cwd == "" {
		return nil, fmt.Errorf("agent-deck backend needs a task workdir")
	}
	poll, settle := b.poll, b.settle
	if poll <= 0 {
		poll = 3 * time.Second
	}
	if settle <= 0 {
		settle = 8 * time.Second
	}
	envFile, cleanup, err := writeAgentdeckEnv(b.cfg.Env)
	if err != nil {
		return nil, fmt.Errorf("agent-deck env file: %w", err)
	}
	title := strings.TrimSpace(opts.ThreadName)
	if title == "" {
		title = "Multica " + shortID(b.cfg.TaskID)
	}
	args := []string{"launch", "-c", agentdeckTool(opts.CustomArgs), "--json", "-t", title, "-g", agentdeckGroup,
		"-no-parent", "--env-file", envFile, "--message-file", "-", opts.Cwd}
	launchCtx, launchCancel := context.WithTimeout(ctx, 3*time.Minute)
	out, err := b.runCmd(launchCtx, prompt, args...)
	launchCancel()
	var launched struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(out), &launched)
	id := launched.ID
	if id == "" {
		id = launched.SessionID
	}
	if err != nil || id == "" {
		cleanup()
		if err == nil {
			err = fmt.Errorf("agent-deck launch returned no session id")
		}
		return nil, err
	}
	b.cfg.Logger.Info("agent-deck session launched", "session", id, "cwd", opts.Cwd)

	msgs := make(chan Message, 64)
	results := make(chan Result, 1)
	runCtx, cancel := runContext(ctx, opts.Timeout)
	started := time.Now()
	send := func(m Message) {
		select {
		case msgs <- m:
		default: // the daemon is not reading; status heartbeats are droppable
		}
	}
	send(Message{Type: MessageStatus, Status: "running", SessionID: id})

	go func() {
		defer cancel()
		defer cleanup()
		defer close(msgs)
		finish := func(r Result) {
			cleanup() // the token file is gone before the daemon sees the result
			r.SessionID, r.DurationMs = id, time.Since(started).Milliseconds()
			results <- r
			close(results)
		}
		var seenRunning bool
		var endedAt time.Time
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				stopCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
				_, _ = b.runCmd(stopCtx, "", "session", "stop", id)
				stop()
				status := "cancelled"
				if ctx.Err() == nil {
					status = "timeout"
				}
				finish(Result{Status: status, Error: "agent-deck session " + id + " stopped: " + status})
				return
			case <-ticker.C:
			}
			showCtx, showCancel := context.WithTimeout(runCtx, 30*time.Second)
			raw, err := b.runCmd(showCtx, "", "session", "show", id, "--json")
			showCancel()
			var show struct {
				Status string `json:"status"`
			}
			if err != nil || json.Unmarshal(bytes.TrimSpace(raw), &show) != nil {
				continue // transient; the run timeout still bounds the wait
			}
			switch show.Status {
			case "running", "starting":
				seenRunning, endedAt = true, time.Time{}
				send(Message{Type: MessageStatus, Status: "running", SessionID: id})
				continue
			case "error":
				finish(Result{Status: "failed", Error: "agent-deck session " + id + " is in error state"})
				return
			}
			// waiting / idle / stopped: the turn ended once it has held for settle
			// (and the session ran at all, or a minute passed without it starting).
			if !seenRunning && time.Since(started) < time.Minute {
				continue
			}
			if endedAt.IsZero() {
				endedAt = time.Now()
				continue
			}
			if time.Since(endedAt) < settle {
				continue
			}
			outCtx, outCancel := context.WithTimeout(context.Background(), 30*time.Second)
			rawOut, _ := b.runCmd(outCtx, "", "session", "output", id, "--json", "-max-tokens", "6000")
			outCancel()
			var reply struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(bytes.TrimSpace(rawOut), &reply)
			finish(agentdeckResult(reply.Content))
			return
		}
	}()

	return &Session{
		Messages: msgs,
		Result:   results,
		// Comments arriving mid-run go into the same session once it is idle.
		Supplement: func(sctx context.Context, text string) error {
			_, err := b.runCmd(sctx, text, "session", "send", "--queue", "--message-file", "-", id)
			return err
		},
		SupplementReady: func() bool { return true },
	}, nil
}

// agentdeckResult reads the final reply: agent-deck asks the agent to end with
// a completion sentinel; status=fail marks the task failed.
func agentdeckResult(content string) Result {
	output := strings.TrimSpace(content)
	if m := agentdeckSentinel.FindStringSubmatch(output); m != nil {
		output = strings.TrimSpace(agentdeckSentinel.ReplaceAllString(output, ""))
		if m[1] == "fail" {
			reason := strings.TrimSpace(m[2])
			if reason == "" {
				reason = "agent reported failure"
			}
			return Result{Status: "failed", Output: output, Error: reason}
		}
	}
	return Result{Status: "completed", Output: output}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
