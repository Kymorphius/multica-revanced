package cli

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCLIConfigCodexSharedHomeRoundTrip(t *testing.T) {
	redirectTestHome(t, t.TempDir())
	for _, value := range []*bool{nil, boolValue(true), boolValue(false)} {
		cfg := CLIConfig{CodexSharedHome: value, DisableAutoUpdate: true, DisableAutoReload: true}
		if err := SaveCLIConfigForProfile(cfg, "shared-test"); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadCLIConfigForProfile("shared-test")
		if err != nil {
			t.Fatal(err)
		}
		if (loaded.CodexSharedHome == nil) != (value == nil) || (value != nil && *loaded.CodexSharedHome != *value) {
			t.Fatalf("shared preference = %v, want %v", loaded.CodexSharedHome, value)
		}
		if !loaded.DisableAutoUpdate || !loaded.DisableAutoReload {
			t.Fatal("saving shared preference dropped independent disable flags")
		}
		path, err := CLIConfigPathForProfile("shared-test")
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		if _, present := raw["codex_shared_home"]; present != (value != nil) {
			t.Fatalf("JSON preference presence = %v, want %v", present, value != nil)
		}
	}
	defaultCfg, err := LoadCLIConfig()
	if err != nil || defaultCfg.CodexSharedHome != nil {
		t.Fatalf("named profile leaked into default profile: %v, %v", defaultCfg.CodexSharedHome, err)
	}
}

func boolValue(value bool) *bool { return &value }
