package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const codexTaskConfigHomeEnv = "MULTICA_CODEX_TASK_CONFIG_HOME"

// Shared mode separates task-owned configuration from native persistent state.
// Validate before the MCP writer runs: a missing or aliased private file must
// never cause the canonical user's configuration to be created or rewritten.
func prepareCodexSharedConfig(env map[string]string) (map[string]any, string, error) {
	privateHome := strings.TrimSpace(env[codexTaskConfigHomeEnv])
	if privateHome == "" {
		return nil, "", nil
	}
	canonicalHome := strings.TrimSpace(env["CODEX_HOME"])
	if !filepath.IsAbs(canonicalHome) || !filepath.IsAbs(privateHome) {
		return nil, "", fmt.Errorf("codex shared mode requires absolute CODEX_HOME and task configuration home")
	}
	canonicalInfo, err := os.Stat(canonicalHome)
	if err != nil || !canonicalInfo.IsDir() {
		return nil, "", fmt.Errorf("codex shared mode requires an existing canonical home directory")
	}
	privateInfo, err := os.Stat(privateHome)
	if err != nil || !privateInfo.IsDir() || os.SameFile(canonicalInfo, privateInfo) {
		return nil, "", fmt.Errorf("codex shared mode requires a separate existing task configuration directory")
	}
	configPath := filepath.Join(privateHome, "config.toml")
	configInfo, err := os.Lstat(configPath)
	if err != nil || !configInfo.Mode().IsRegular() {
		return nil, "", fmt.Errorf("codex shared mode requires a regular private config.toml")
	}
	if canonicalConfig, err := os.Stat(filepath.Join(canonicalHome, "config.toml")); err == nil && os.SameFile(canonicalConfig, configInfo) {
		return nil, "", fmt.Errorf("codex shared mode task config.toml aliases canonical configuration")
	}
	config, err := readCodexSharedConfig(configPath)
	return config, privateHome, err
}

func readCodexSharedConfig(configPath string) (map[string]any, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("codex shared mode could not read private config.toml")
	}
	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		// TOML parser diagnostics may echo a secret-bearing source line.
		return nil, fmt.Errorf("codex shared mode private config.toml is invalid TOML")
	}
	return codexJSONSafeConfig(config)
}

func codexJSONSafeConfig(config map[string]any) (map[string]any, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("codex shared mode configuration cannot be represented as JSON")
	}
	var safe map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&safe); err != nil {
		return nil, fmt.Errorf("codex shared mode configuration cannot be represented as JSON")
	}
	if safe == nil {
		safe = map[string]any{}
	}
	return safe, nil
}

// Capture launch overrides into the RPC map so the complete private snapshot
// does not shadow explicit model/config selections. MCP overrides travel only
// over stdin; data-home overrides are discarded rather than escaping shared
// storage. Retaining other arguments preserves app-server startup behavior.
func applyCodexSharedLaunchOverrides(config map[string]any, args []string, managedMCP, managedFastMode bool, explicitKeys map[string]bool) ([]string, error) {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := unshellQuoteArg(args[i])
		flag, inline, hasInline := strings.Cut(arg, "=")
		if strings.HasPrefix(arg, "-c") && !strings.HasPrefix(arg, "--") && flag != "-c" {
			flag, inline, hasInline = "-c", arg[2:], true
			arg = "-c=" + inline
		}
		if flag == "--codex-home" || flag == "--sqlite-home" {
			if !hasInline && i+1 < len(args) {
				i++
			}
			continue
		}
		if flag == "-p" || flag == "--profile" || (strings.HasPrefix(arg, "-p") && !strings.HasPrefix(arg, "--")) {
			return nil, fmt.Errorf("codex shared mode does not support app-server profile arguments")
		}
		if flag != "-c" && flag != "--config" {
			filtered = append(filtered, arg)
			// A copied features table would otherwise shadow CLI feature flags.
			if (flag == "--enable" || flag == "--disable") && (hasInline || i+1 < len(args)) {
				feature := inline
				if !hasInline {
					i++
					feature = unshellQuoteArg(args[i])
					filtered = append(filtered, feature)
				}
				features, _ := config["features"].(map[string]any)
				if features == nil {
					features = map[string]any{}
					config["features"] = features
				}
				features[feature] = flag == "--enable"
			}
			continue
		}
		value := inline
		if !hasInline {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("codex shared mode config argument has no value")
			}
			i++
			value = args[i]
			var literalAssignment map[string]any
			if toml.Unmarshal([]byte(value), &literalAssignment) != nil {
				value = unshellQuoteArg(value)
			}
		}
		key, rawValue, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("codex shared mode config argument requires key=value")
		}
		var override map[string]any
		if err := toml.Unmarshal([]byte(value), &override); err != nil {
			// Native Codex accepts a bare value as a string when TOML parsing fails.
			quoted, _ := json.Marshal(strings.TrimSpace(rawValue))
			if err := toml.Unmarshal([]byte(key+"="+string(quoted)), &override); err != nil {
				return nil, fmt.Errorf("codex shared mode config argument has an invalid key")
			}
		}
		if codexConfigChangesHomeOrShellPolicy(override) {
			continue
		}
		if _, mcp := override["mcp_servers"]; managedMCP && mcp {
			continue
		}
		if features, ok := override["features"].(map[string]any); managedFastMode && ok {
			if _, explicit := features[codexFastModeFeature]; explicit {
				continue
			}
		}
		safe, err := codexJSONSafeConfig(override)
		if err != nil {
			return nil, err
		}
		mergeCodexSharedConfig(config, safe)
		for key := range safe {
			if explicitKeys != nil {
				explicitKeys[key] = true
			}
		}
		if _, mcp := safe["mcp_servers"]; !mcp {
			filtered = append(filtered, arg)
			if !hasInline {
				filtered = append(filtered, value)
			}
		}
	}
	return filtered, nil
}

func codexConfigChangesHomeOrShellPolicy(config map[string]any) bool {
	for key, value := range config {
		if key == "sqlite_home" || key == "codex_home" || key == shellEnvironmentPolicyConfigNamespace {
			return true
		}
		if key == "profiles" {
			profiles, _ := value.(map[string]any)
			for _, profile := range profiles {
				if table, ok := profile.(map[string]any); ok && codexConfigChangesHomeOrShellPolicy(table) {
					return true
				}
			}
		}
	}
	return false
}

func mergeCodexSharedConfig(target, source map[string]any) {
	for key, value := range source {
		sourceTable, sourceOK := value.(map[string]any)
		targetTable, targetOK := target[key].(map[string]any)
		if sourceOK && targetOK {
			mergeCodexSharedConfig(targetTable, sourceTable)
		} else {
			target[key] = value
		}
	}
}

// Native --disable wins over --enable even when the latter occurs later.
// Reapply the complete prefix/launch sequence after collecting config values.
func applyCodexSharedFeatureFlags(config map[string]any, args []string) {
	for _, wanted := range []string{"--enable", "--disable"} {
		for i := 0; i < len(args); i++ {
			flag, feature, inline := strings.Cut(args[i], "=")
			if flag != wanted || (!inline && i+1 >= len(args)) {
				continue
			}
			if !inline {
				i++
				feature = args[i]
			}
			features, _ := config["features"].(map[string]any)
			if features == nil {
				features = map[string]any{}
				config["features"] = features
			}
			features[feature] = wanted == "--enable"
		}
	}
}

// config maps recursively overlay canonical tables, so an omitted managed MCP
// server needs an explicit disabled entry. Read the effective native baseline
// (including cwd layers) rather than guessing from the canonical TOML alone.
func (c *codexClient) prepareSharedThreadConfig(ctx context.Context, opts ExecOptions) error {
	if c.sharedConfig == nil {
		return nil
	}
	if hasManagedCodexMcpConfig(opts.McpConfig) {
		result, err := c.request(ctx, "config/read", map[string]any{"includeLayers": false, "cwd": opts.Cwd})
		if err != nil {
			return codexSharedRPCFailure("config/read", err)
		}
		var baseline struct {
			Config map[string]json.RawMessage `json:"config"`
		}
		if json.Unmarshal(result, &baseline) != nil || baseline.Config == nil {
			return fmt.Errorf("codex shared mode config/read returned invalid configuration")
		}
		var inherited map[string]json.RawMessage
		if raw := baseline.Config["mcp_servers"]; len(raw) != 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &inherited) != nil {
				return fmt.Errorf("codex shared mode config/read returned invalid MCP configuration")
			}
		}
		servers, _ := c.sharedConfig["mcp_servers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			c.sharedConfig["mcp_servers"] = servers
		}
		for name := range inherited {
			taskServer, present := servers[name]
			if !present {
				servers[name] = map[string]any{"enabled": false}
				continue
			}
			var ownerServer map[string]any
			taskTable, ok := taskServer.(map[string]any)
			if !ok || json.Unmarshal(inherited[name], &ownerServer) != nil || ownerServer == nil {
				return fmt.Errorf("codex shared mode cannot validate inherited MCP configuration")
			}
			for _, key := range []string{"command", "url", "args", "env", "env_vars", "http_headers", "env_http_headers", "http_headers_helper", "bearer_token", "bearer_token_env_var", "cwd", "auth", "oauth", "oauth_resource", "scopes", "environment_id"} {
				if value, exists := ownerServer[key]; exists && codexSharedValueWouldInherit(value, taskTable[key]) {
					return fmt.Errorf("codex shared mode managed MCP conflicts with inherited transport or credentials; configure the complete task server or use a distinct server name")
				}
			}
		}
		for _, server := range servers {
			if table, ok := server.(map[string]any); ok {
				if _, explicit := table["enabled"]; !explicit {
					table["enabled"] = true
				}
			}
		}
	}
	if _, err := c.request(ctx, "skills/extraRoots/set", map[string]any{
		"extraRoots": []string{filepath.Join(c.sharedTaskConfigHome, "skills")},
	}); err != nil {
		return codexSharedRPCFailure("skills/extraRoots/set", err)
	}
	return nil
}

// The copied owner model defaults belong to fresh threads. Native resume keeps
// persisted model/provider/effort unless the caller explicitly selected one.
// Copy the root map so a missing-thread fallback still receives fresh defaults.
func (c *codexClient) sharedResumeConfig() map[string]any {
	config := make(map[string]any, len(c.sharedConfig))
	for key, value := range c.sharedConfig {
		config[key] = value
	}
	for _, key := range []string{"model", "model_provider", "model_reasoning_effort"} {
		if !c.sharedLaunchConfigKeys[key] {
			delete(config, key)
		}
	}
	return config
}

func codexSharedValueWouldInherit(owner, task any) bool {
	if task == nil {
		return owner != nil
	}
	if ownerTable, ok := owner.(map[string]any); ok {
		taskTable, _ := task.(map[string]any)
		for key, value := range ownerTable {
			if codexSharedValueWouldInherit(value, taskTable[key]) {
				return true
			}
		}
		return false
	}
	return false
}

func codexSharedRPCFailure(method string, err error) error {
	if isCodexTransportError(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("codex shared mode %s failed: %w", method, err)
	}
	// A native rejection can echo the secret-bearing RPC config. Keep only the
	// method and protocol code, which also makes unsupported APIs actionable.
	var rpcErr *codexRPCError
	if errors.As(err, &rpcErr) {
		if method == "thread/resume" && strings.Contains(rpcErr.Message, "already has an active writer") {
			return fmt.Errorf("codex shared mode thread/resume failed: this thread has another active writer; close its existing session before resuming")
		}
		return fmt.Errorf("codex shared mode %s failed (code=%d); verify installed Codex supports shared configuration APIs", method, rpcErr.Code)
	}
	return fmt.Errorf("codex shared mode %s failed; verify installed Codex supports shared configuration APIs", method)
}

// Only an explicit native missing-rollout response proves that a fresh thread
// is safe. Lock contention, schema/config errors and unknown resume outcomes
// must retain the caller's original continuation pointer.
func codexSharedResumeMayStartFresh(err error, threadID string) bool {
	var rpcErr *codexRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32600 {
		return false
	}
	return rpcErr.Message == "no rollout found for thread id "+threadID || rpcErr.Message == "thread not found: "+threadID
}
