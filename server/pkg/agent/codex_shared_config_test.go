package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const sharedConfigSecret = "SYNTHETIC-MCP-SECRET-39f4"

func sharedCodexHomes(t *testing.T) (string, string, map[string]string) {
	t.Helper()
	canonical, private := t.TempDir(), t.TempDir()
	for _, home := range []string{canonical, private} {
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"owner-model\"\ndeveloper_instructions = \"owner guidance\"\n"+
			"[features]\nmemories = true\n[memories]\nuse_memories = true\ngenerate_memories = true\n"+
			"[shell_environment_policy]\ninherit = \"all\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return canonical, private, map[string]string{"CODEX_HOME": canonical, "CODEX_SQLITE_HOME": "/wrong-home", codexTaskConfigHomeEnv: private}
}

func TestCodexSharedConfigRejectsInvalidPrivateFiles(t *testing.T) {
	for _, invalid := range []string{"missing", "malformed", "symlink", "same-home", "hardlink"} {
		t.Run(invalid, func(t *testing.T) {
			canonical, private, env := sharedCodexHomes(t)
			privatePath := filepath.Join(private, "config.toml")
			switch invalid {
			case "missing":
				_ = os.Remove(privatePath)
			case "malformed":
				_ = os.WriteFile(privatePath, []byte("secret = \""+sharedConfigSecret), 0o600)
			case "same-home":
				env[codexTaskConfigHomeEnv] = canonical
			case "symlink", "hardlink":
				_ = os.Remove(privatePath)
				link := os.Symlink
				if invalid == "hardlink" {
					link = os.Link
				}
				if err := link(filepath.Join(canonical, "config.toml"), privatePath); err != nil {
					t.Skipf("link unsupported: %v", err)
				}
			}
			backend, err := New("codex", Config{ExecutablePath: writeFakeCodexAppServer(t, "exit 99\n"), Logger: slog.Default(), Env: env})
			if err != nil {
				t.Fatal(err)
			}
			_, err = backend.Execute(context.Background(), "test", ExecOptions{McpConfig: json.RawMessage(`{"mcpServers":{}}`)})
			if err == nil || strings.Contains(err.Error(), sharedConfigSecret) {
				t.Fatalf("expected safe private-config rejection, got %v", err)
			}
			canonicalBytes, _ := os.ReadFile(filepath.Join(canonical, "config.toml"))
			if !strings.Contains(string(canonicalBytes), "owner guidance") || strings.Contains(string(canonicalBytes), "multica-managed") {
				t.Fatal("invalid private config changed canonical file")
			}
		})
	}
}

func TestCodexSharedConfigLaunchOverrides(t *testing.T) {
	config := map[string]any{"model": "owner", "features": map[string]any{"fast_mode": false}}
	args := []string{"--sqlite-home=/escape", "-c", `"sqlite_home"="/escape"`, "--config", `profiles.task.sqlite_home="/escape"`,
		"-c", "model=bare-model", "--config=model_reasoning_effort=\"high\"", "--config", `mcp_servers.custom={command="server",env={TOKEN="` + sharedConfigSecret + `"}}`, "--enable", "fast_mode"}
	filtered, err := applyCodexSharedLaunchOverrides(config, args, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(filtered, " "), sharedConfigSecret) || strings.Contains(strings.Join(filtered, " "), "/escape") {
		t.Fatalf("home or MCP override escaped into argv: %v", filtered)
	}
	if config["model"] != "bare-model" || config["model_reasoning_effort"] != "high" || config["features"].(map[string]any)["fast_mode"] != true {
		t.Fatalf("explicit CLI choices were lost: %v", config)
	}
	server := config["mcp_servers"].(map[string]any)["custom"].(map[string]any)
	if server["env"].(map[string]any)["TOKEN"] != sharedConfigSecret {
		t.Fatal("MCP override missing from RPC config")
	}
	for _, args := range [][]string{{"-p", "profile"}, {"--profile=profile"}, {"-c"}, {"--config", "invalid"}} {
		if _, err := applyCodexSharedLaunchOverrides(config, args, false, false, nil); err == nil {
			t.Fatalf("invalid shared override accepted: %v", args)
		}
	}
}

func TestCodexSharedConfigParsedManagedMcpAndAttachedArgs(t *testing.T) {
	for _, managed := range []bool{true, false} {
		config := map[string]any{"mcp_servers": map[string]any{"saved": map[string]any{"command": "saved-server"}}}
		args := []string{"-c", `"mcp_servers".rogue.command="quoted-server"`,
			`-cmcp_servers.attached.env.TOKEN="` + sharedConfigSecret + `"`, `-csqlite_home="/escape"`, `-cmodel="attached-model"`}
		filtered, err := applyCodexSharedLaunchOverrides(config, args, managed, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		servers := config["mcp_servers"].(map[string]any)
		if managed && (len(servers) != 1 || servers["saved"].(map[string]any)["command"] != "saved-server") {
			t.Fatalf("parsed quoted or attached override expanded managed set: %v", servers)
		}
		if !managed && (servers["rogue"] == nil || servers["attached"] == nil) {
			t.Fatal("unmanaged CLI MCP was not captured in stdin config")
		}
		if strings.Contains(strings.Join(filtered, " "), sharedConfigSecret) || strings.Contains(strings.Join(filtered, " "), "/escape") ||
			config["model"] != "attached-model" {
			t.Fatalf("attached home/MCP escaped argv or model selection lost: %v", filtered)
		}
	}
	config := map[string]any{}
	applyCodexSharedFeatureFlags(config, []string{"--disable", "memories", "--enable", "memories"})
	if config["features"].(map[string]any)["memories"] != false {
		t.Fatal("native disable-over-enable feature precedence was lost")
	}
}

func TestCodexSharedConfigParsedOwnedPolicyAndFastMode(t *testing.T) {
	config := map[string]any{"shell_environment_policy": map[string]any{"inherit": "all"}, "features": map[string]any{codexFastModeFeature: true}}
	args := []string{`-cshell_environment_policy.inherit="none"`, "-c", `"shell_environment_policy"={inherit="none"}`,
		`-cprofiles.task.shell_environment_policy.inherit="none"`, "-c", `profiles."task name"={shell_environment_policy={inherit="none"}}`, `-cfeatures.fast_mode=false`}
	filtered, err := applyCodexSharedLaunchOverrides(config, args, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 || config["shell_environment_policy"].(map[string]any)["inherit"] != "all" || config["features"].(map[string]any)[codexFastModeFeature] != true {
		t.Fatalf("parsed quoted/attached config bypassed owned policy: argv=%v config=%v", filtered, config)
	}
}

func TestCodexSharedConfigStartResumeAndFallback(t *testing.T) {
	for _, mode := range []string{"start", "resume", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			c, fs, _ := newTestCodexClient(t)
			c.sharedTaskConfigHome = t.TempDir()
			c.sharedConfig = map[string]any{"sqlite_home": "/canonical", "developer_instructions": "owner guidance", "memories": map[string]any{"generate_memories": true},
				"mcp_servers": map[string]any{"task": map[string]any{"command": "task-server", "env": map[string]any{"TOKEN": sharedConfigSecret}}}}
			assertConfig := func(t *testing.T, params map[string]any) {
				config, _ := params["config"].(map[string]any)
				if config["sqlite_home"] != "/canonical" || config["developer_instructions"] != "owner guidance" || config["model_reasoning_effort"] != "high" {
					t.Errorf("full private config or explicit effort missing: %v", config)
				}
				mcp := config["mcp_servers"].(map[string]any)
				if mcp["owner-only"].(map[string]any)["enabled"] != false || mcp["task"].(map[string]any)["env"].(map[string]any)["TOKEN"] != sharedConfigSecret {
					t.Error("managed MCP set inherited an omitted server or lost task secret")
				}
				assertNoDeveloperInstructions(t, params)
			}
			script := []rpcResponse{{method: "config/read", result: json.RawMessage(`{"config":{"mcp_servers":{"owner-only":{"command":"owner-server"}}}}`), assertFn: func(t *testing.T, params map[string]any) {
				if params["cwd"] != "/work" {
					t.Error("effective baseline must include task cwd")
				}
			}}, {method: "skills/extraRoots/set", result: json.RawMessage(`{}`), assertFn: func(t *testing.T, params map[string]any) {
				if !reflect.DeepEqual(params["extraRoots"], []any{filepath.Join(c.sharedTaskConfigHome, "skills")}) {
					t.Errorf("private skill roots = %v", params["extraRoots"])
				}
			}}}
			opts := ExecOptions{Cwd: "/work", ThinkingLevel: "high", McpConfig: json.RawMessage(`{"mcpServers":{"task":{"command":"task-server"}}}`)}
			if mode != "start" {
				opts.ResumeSessionID = "old-thread"
				response := rpcResponse{method: "thread/resume", result: json.RawMessage(`{"thread":{"id":"old-thread"}}`), assertFn: assertConfig}
				if mode == "fallback" {
					response.result, response.errMsg, response.errCode = nil, "no rollout found for thread id old-thread", -32600
				}
				script = append(script, response)
			}
			if mode != "resume" {
				script = append(script, rpcResponse{method: "thread/start", result: json.RawMessage(`{"thread":{"id":"fresh-thread"}}`), assertFn: assertConfig})
			}
			wait := drainRPCScript(t, c, fs, script)
			defer wait()
			_, resumed, err := c.startOrResumeThread(context.Background(), opts, slog.Default())
			if err != nil || resumed != (mode == "resume") {
				t.Fatalf("shared %s: resumed=%v err=%v", mode, resumed, err)
			}
		})
	}
}

func TestCodexSharedConfigUnsupportedAPIsFailClosed(t *testing.T) {
	for _, method := range []string{"config/read", "skills/extraRoots/set", "thread/resume", "thread/start"} {
		t.Run(method, func(t *testing.T) {
			c, fs, _ := newTestCodexClient(t)
			c.sharedConfig, c.sharedTaskConfigHome = map[string]any{}, "/private"
			opts := ExecOptions{McpConfig: json.RawMessage(`{}`)}
			var script []rpcResponse
			for _, m := range []string{"config/read", "skills/extraRoots/set", "thread/start"} {
				if m == "thread/start" && method == "thread/resume" {
					m, opts.ResumeSessionID = "thread/resume", "old-thread"
				}
				response := rpcResponse{method: m, result: json.RawMessage(`{}`)}
				if m == "config/read" {
					response.result = json.RawMessage(`{"config":{}}`)
				}
				if m == method {
					response.result, response.errMsg, response.errCode = nil, "unsupported "+sharedConfigSecret, -32601
				}
				script = append(script, response)
				if m == method {
					break
				}
			}
			wait := drainRPCScript(t, c, fs, script)
			defer wait()
			_, _, err := c.startOrResumeThread(context.Background(), opts, slog.Default())
			if err == nil || !strings.Contains(err.Error(), method) || strings.Contains(err.Error(), sharedConfigSecret) {
				t.Fatalf("expected safe unsupported %s failure, got %v", method, err)
			}
			if len(fs.Lines()) != len(script) {
				t.Fatal("unsupported API silently fell back to a different execution mode")
			}
		})
	}
}

func TestCodexSharedConfigResumeRejectsBusyAndUncertainOutcomes(t *testing.T) {
	for _, message := range []string{"thread old-thread already has an active writer", "invalid config " + sharedConfigSecret,
		"thread not found: other-thread", "no rollout found for thread id other-thread", ""} {
		c, fs, _ := newTestCodexClient(t)
		c.sharedConfig, c.sharedTaskConfigHome = map[string]any{}, "/private"
		response := rpcResponse{method: "thread/resume", errMsg: message, errCode: -32600}
		if message == "" {
			response.result = json.RawMessage(`{"thread":{}}`)
		}
		wait := drainRPCScript(t, c, fs, []rpcResponse{{method: "skills/extraRoots/set", result: json.RawMessage(`{}`)}, response})
		_, _, err := c.startOrResumeThread(context.Background(), ExecOptions{ResumeSessionID: "old-thread"}, slog.Default())
		wait()
		if err == nil || strings.Contains(err.Error(), sharedConfigSecret) || len(fs.Lines()) != 2 {
			t.Fatalf("busy/uncertain resume did not retain original thread safely: %v", err)
		}
	}
}

func TestCodexSharedConfigRejectsInheritedMcpCredentials(t *testing.T) {
	for _, inherited := range []string{
		`{"command":"server","env":{"OWNER_TOKEN":"` + sharedConfigSecret + `"}}`,
		`{"url":"https://owner.invalid/mcp","http_headers":{"Authorization":"` + sharedConfigSecret + `"}}`,
	} {
		c, fs, _ := newTestCodexClient(t)
		c.sharedTaskConfigHome = "/private"
		c.sharedConfig = map[string]any{"mcp_servers": map[string]any{"task": map[string]any{"command": "server"}}}
		wait := drainRPCScript(t, c, fs, []rpcResponse{{method: "config/read", result: json.RawMessage(`{"config":{"mcp_servers":{"task":` + inherited + `}}}`)}})
		_, _, err := c.startOrResumeThread(context.Background(), ExecOptions{McpConfig: json.RawMessage(`{}`)}, slog.Default())
		wait()
		if err == nil || !strings.Contains(err.Error(), "inherited transport or credentials") || strings.Contains(err.Error(), sharedConfigSecret) || len(fs.Lines()) != 1 {
			t.Fatalf("inherited conflicting server did not fail safely before thread start: %v", err)
		}
	}
}

func TestCodexSharedConfigRejectsInheritedMcpAuthFields(t *testing.T) {
	for key, value := range map[string]any{
		"http_headers_helper": sharedConfigSecret,
		"auth":                map[string]any{"mode": sharedConfigSecret},
		"oauth":               map[string]any{"client_id": sharedConfigSecret},
		"oauth_resource":      sharedConfigSecret,
		"scopes":              []string{sharedConfigSecret},
		"environment_id":      sharedConfigSecret,
	} {
		t.Run(key, func(t *testing.T) {
			c, fs, _ := newTestCodexClient(t)
			c.sharedTaskConfigHome = "/private"
			c.sharedConfig = map[string]any{"mcp_servers": map[string]any{"task": map[string]any{"url": "https://server.invalid/mcp"}}}
			baseline, err := json.Marshal(map[string]any{"config": map[string]any{"mcp_servers": map[string]any{"task": map[string]any{"url": "https://server.invalid/mcp", key: value}}}})
			if err != nil {
				t.Fatal(err)
			}
			wait := drainRPCScript(t, c, fs, []rpcResponse{{method: "config/read", result: baseline}})
			_, _, err = c.startOrResumeThread(context.Background(), ExecOptions{McpConfig: json.RawMessage(`{}`)}, slog.Default())
			wait()
			if err == nil || strings.Contains(err.Error(), sharedConfigSecret) || len(fs.Lines()) != 1 {
				t.Fatalf("omitted inherited %s did not fail safely before skills/thread RPC: %v", key, err)
			}
		})
	}
}

func TestCodexSharedConfigResumeKeepsPersistedModelDefaults(t *testing.T) {
	for _, mode := range []string{"persisted", "explicit-args", "provider-only", "explicit-options", "missing-thread"} {
		t.Run(mode, func(t *testing.T) {
			c, fs, _ := newTestCodexClient(t)
			c.sharedTaskConfigHome = "/private"
			c.sharedConfig = map[string]any{"model": "owner-model", "model_provider": "owner-provider", "model_reasoning_effort": "owner-effort", "developer_instructions": "owner guidance"}
			opts := ExecOptions{ResumeSessionID: "old-thread"}
			if mode == "explicit-args" || mode == "provider-only" {
				c.sharedLaunchConfigKeys = map[string]bool{}
				args := []string{"-cmodel=explicit-model", "-cmodel_provider=explicit-provider", "-cmodel_reasoning_effort=high"}
				if mode == "provider-only" {
					args = []string{"-cmodel_provider=explicit-provider"}
				}
				if _, err := applyCodexSharedLaunchOverrides(c.sharedConfig, args, false, false, c.sharedLaunchConfigKeys); err != nil {
					t.Fatal(err)
				}
			} else if mode == "explicit-options" {
				opts.Model, opts.ThinkingLevel = "selected-model", "high"
			}
			response := rpcResponse{method: "thread/resume", result: json.RawMessage(`{"thread":{"id":"old-thread"}}`), assertFn: func(t *testing.T, params map[string]any) {
				config := params["config"].(map[string]any)
				if config["developer_instructions"] != "owner guidance" {
					t.Error("resume lost complete non-model task configuration")
				}
				if mode == "explicit-args" {
					if config["model"] != "explicit-model" || config["model_provider"] != "explicit-provider" || config["model_reasoning_effort"] != "high" || params["modelProvider"] != "explicit-provider" {
						t.Error("resume lost explicit CLI selections")
					}
				} else {
					for _, key := range []string{"model", "model_provider", "model_reasoning_effort"} {
						if key == "model_provider" && mode == "provider-only" {
							if config[key] != "explicit-provider" || params["modelProvider"] != "explicit-provider" {
								t.Error("provider-only selection must reach native typed resume field")
							}
							continue
						}
						if key == "model_reasoning_effort" && mode == "explicit-options" {
							if config[key] != "high" || params["model"] != "selected-model" {
								t.Error("resume lost explicit execution options")
							}
							continue
						}
						if _, exists := config[key]; exists {
							t.Errorf("resume overrides persisted %s with copied owner default", key)
						}
					}
				}
			}}
			if mode == "missing-thread" {
				response.result, response.errMsg, response.errCode = nil, "no rollout found for thread id old-thread", -32600
			}
			script := []rpcResponse{{method: "skills/extraRoots/set", result: json.RawMessage(`{}`)}, response}
			if mode == "missing-thread" {
				script = append(script, rpcResponse{method: "thread/start", result: json.RawMessage(`{"thread":{"id":"fresh"}}`), assertFn: func(t *testing.T, params map[string]any) {
					config := params["config"].(map[string]any)
					if config["model"] != "owner-model" || config["model_provider"] != "owner-provider" || config["model_reasoning_effort"] != "owner-effort" {
						t.Error("missing-thread fallback lost fresh owner defaults")
					}
				}})
			}
			wait := drainRPCScript(t, c, fs, script)
			_, _, err := c.startOrResumeThread(context.Background(), opts, slog.Default())
			wait()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexSharedConfigManagedServerReenabledAndUnmanagedUsesPrivateMap(t *testing.T) {
	for _, managed := range []bool{true, false} {
		c, fs, _ := newTestCodexClient(t)
		c.sharedTaskConfigHome = "/private"
		c.sharedConfig = map[string]any{"mcp_servers": map[string]any{"task": map[string]any{"command": "task-server"}}, "sqlite_home": "/canonical"}
		opts := ExecOptions{}
		var script []rpcResponse
		if managed {
			opts.McpConfig = json.RawMessage(`{}`)
			script = append(script, rpcResponse{method: "config/read", result: json.RawMessage(`{"config":{"mcp_servers":{"task":{"command":"owner-server","enabled":false}}}}`)})
		}
		script = append(script, rpcResponse{method: "skills/extraRoots/set", result: json.RawMessage(`{}`)}, rpcResponse{
			method: "thread/start", result: json.RawMessage(`{"thread":{"id":"fresh"}}`), assertFn: func(t *testing.T, params map[string]any) {
				config := params["config"].(map[string]any)
				server := config["mcp_servers"].(map[string]any)["task"].(map[string]any)
				if config["sqlite_home"] != "/canonical" || server["command"] != "task-server" || (managed && server["enabled"] != true) {
					t.Errorf("task config or managed enabled default missing: %v", config)
				}
			},
		})
		wait := drainRPCScript(t, c, fs, script)
		_, _, err := c.startOrResumeThread(context.Background(), opts, slog.Default())
		wait()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodexSharedConfigExecuteKeepsCanonicalBytesAndSecretsOffArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}
	canonical, private, env := sharedCodexHomes(t)
	before, _ := os.ReadFile(filepath.Join(canonical, "config.toml"))
	recordDir := t.TempDir()
	env["TEST_SHARED_RECORD"] = recordDir
	fake := writeFakeCodexAppServer(t, `printf '%s\n' "$@" > "$TEST_SHARED_RECORD/argv"
printf '%s\n' "$CODEX_HOME" "$CODEX_SQLITE_HOME" > "$TEST_SHARED_RECORD/env"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$TEST_SHARED_RECORD/rpc"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*) printf '{"id":%s,"result":{}}\n' "$id" ;;
    *'"method":"config/read"'*) printf '{"id":%s,"result":{"config":{"mcp_servers":{"owner-only":{"command":"owner-server"}}}}}\n' "$id" ;;
    *'"method":"skills/extraRoots/set"'*) printf '{"id":%s,"result":{}}\n' "$id" ;;
    *'"method":"thread/start"'*) printf '{"id":%s,"result":{"thread":{"id":"shared-thread"}}}\n' "$id" ;;
    *'"method":"turn/start"'*)
      printf '{"id":%s,"result":{"turn":{"id":"turn-1"}}}\n' "$id"
      printf '%s\n' '{"method":"turn/completed","params":{"threadId":"shared-thread","turn":{"id":"turn-1","status":"completed"}}}'
      ;;
  esac
done
`)
	var logs bytes.Buffer
	result, _ := executeFakeCodexCollectingMessagesWithConfig(t, fake, Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Env: env, LaunchPrefix: []string{"-c", `model="prefix-model"`}},
		ExecOptions{Cwd: recordDir, Timeout: 5 * time.Second, McpConfig: json.RawMessage(`{"mcpServers":{"task":{"command":"task-server","env":{"TOKEN":"` + sharedConfigSecret + `"}}}}`),
			CustomArgs: []string{"-c", `model="custom-model"`, "-c", `sqlite_home="/escape"`, "-c", `shell_environment_policy.inherit="none"`}}, 8*time.Second)
	if result.Status != "completed" {
		t.Fatalf("shared fake execution = %+v", result)
	}
	after, _ := os.ReadFile(filepath.Join(canonical, "config.toml"))
	if !bytes.Equal(before, after) {
		t.Fatal("shared task rewrote canonical config.toml")
	}
	argv, _ := os.ReadFile(filepath.Join(recordDir, "argv"))
	if strings.Contains(string(argv), sharedConfigSecret) || strings.Contains(logs.String(), sharedConfigSecret) || strings.Contains(string(argv), "/escape") {
		t.Fatal("MCP secret or conflicting home escaped RPC boundary")
	}
	processEnv, _ := os.ReadFile(filepath.Join(recordDir, "env"))
	if string(processEnv) != canonical+"\n"+canonical+"\n" {
		t.Fatalf("native persistent state homes = %q", processEnv)
	}
	requests, _ := os.ReadFile(filepath.Join(recordDir, "rpc"))
	var startConfig map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(requests)), "\n") {
		var request struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		if request.Method == "thread/start" {
			startConfig, _ = request.Params["config"].(map[string]any)
		}
	}
	if startConfig["model"] != "custom-model" || startConfig["sqlite_home"] != canonical || startConfig["developer_instructions"] != "owner guidance" ||
		startConfig["shell_environment_policy"].(map[string]any)["inherit"] != "all" || !strings.Contains(fmt.Sprint(startConfig), sharedConfigSecret) {
		t.Fatalf("complete task configuration missing/precedence incorrect: %v", startConfig)
	}
	privateBytes, _ := os.ReadFile(filepath.Join(private, "config.toml"))
	if !strings.Contains(string(privateBytes), sharedConfigSecret) {
		t.Fatal("managed MCP was not materialized in private configuration")
	}
}
