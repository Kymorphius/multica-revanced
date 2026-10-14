package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestSharedCodexWorkspaceSkillOverridesCanonicalPrepareAndReuse(t *testing.T) {
	sharedHome := t.TempDir()
	writeFile(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig)
	canonicalSkill := filepath.Join(sharedHome, "skills", "writing", "SKILL.md")
	writeFile(t, canonicalSkill, "Canonical writing instructions.")
	writeFile(t, filepath.Join(sharedHome, "skills", "review", "SKILL.md"), "Unclaimed review instructions.")
	t.Setenv("CODEX_HOME", sharedHome)
	workspace := []SkillContextForEnv{{Name: "Writing", Content: "Workspace writing instructions."}}
	env, err := Prepare(PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws-priority", TaskID: "task-priority", Provider: "codex", CodexSharedHome: true,
		Task: TaskContextForEnv{IssueID: "priority", AgentSkills: workspace, DisabledRuntimeSkills: []RuntimeSkillRefForEnv{{Root: "provider", Key: "writing"}}}}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(true)
	physicalSkill, err := filepath.EvalSymlinks(canonicalSkill)
	if err != nil {
		t.Fatal(err)
	}
	assertSharedWorkspaceSkillPriority(t, env.CodexHome, physicalSkill, "Workspace writing instructions.")
	reused := Reuse(ReuseParams{WorkDir: env.WorkDir, Provider: "codex", CodexSharedHome: true,
		Task: TaskContextForEnv{IssueID: "priority", AgentSkills: []SkillContextForEnv{{Name: "Writing", Content: "Updated workspace writing instructions."}}}}, testLogger())
	if reused == nil {
		t.Fatal("shared skill priority prevented valid reuse")
	}
	assertSharedWorkspaceSkillPriority(t, reused.CodexHome, physicalSkill, "Updated workspace writing instructions.")
	assertFileContents(t, canonicalSkill, "Canonical writing instructions.", false)
	assertFileContents(t, filepath.Join(sharedHome, "config.toml"), sharedMemoryTestConfig, false)
	// Removing the workspace claim restores default canonical discovery and
	// removes the prior run's disabling entry with the refreshed task config.
	reused = Reuse(ReuseParams{WorkDir: env.WorkDir, Provider: "codex", CodexSharedHome: true, Task: TaskContextForEnv{IssueID: "priority"}}, testLogger())
	if reused == nil {
		t.Fatal("reuse after removing workspace skill failed")
	}
	if overrides := sharedSkillOverrides(t, reused.CodexHome); len(overrides) != 0 {
		t.Fatalf("removed workspace claim left stale overrides: %v", overrides)
	}
}

func TestSharedCodexWorkspaceSkillOverridesResolveProviderRootAlias(t *testing.T) {
	sharedHome := t.TempDir()
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "SKILL.md"), "Linked canonical skill.")
	writeFile(t, filepath.Join(external, "nested", "SKILL.md"), "Nested canonical skill.")
	writeFile(t, filepath.Join(external, ".hidden", "SKILL.md"), "Hidden skill is not discovered.")
	if err := createDirLink(external, filepath.Join(external, ".hidden-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sharedHome, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createDirLink(external, filepath.Join(sharedHome, "skills", "writing")); err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	config := filepath.Join(private, "config.toml")
	writeFile(t, config, sharedMemoryTestConfig)
	if err := ensureSharedWorkspaceSkillOverrides(config, sharedHome, []SkillContextForEnv{{Name: "Writing"}}); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	overrides := sharedSkillOverrides(t, private)
	if len(overrides) != 2 {
		t.Fatalf("bounded scan included hidden skill roots: %v", overrides)
	}
	for _, path := range []string{filepath.Join(physical, "SKILL.md"), filepath.Join(physical, "nested", "SKILL.md")} {
		if overrides[path] != false {
			t.Fatalf("canonical provider-root document %q was not disabled: %v", path, overrides)
		}
	}
	assertFileContents(t, filepath.Join(external, "SKILL.md"), "Linked canonical skill.", false)
}

func TestSharedCodexWorkspaceSkillOverrideRefusesNestedLinks(t *testing.T) {
	sharedHome := t.TempDir()
	root := filepath.Join(sharedHome, "skills", "writing")
	writeFile(t, filepath.Join(root, "SKILL.md"), "Canonical writing.")
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "SKILL.md"), "External nested skill.")
	if err := createDirLink(external, filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, config, sharedMemoryTestConfig)
	err := ensureSharedWorkspaceSkillOverrides(config, sharedHome, []SkillContextForEnv{{Name: "Writing"}})
	if err == nil || !strings.Contains(err.Error(), "nested link") {
		t.Fatalf("expected bounded no-follow refusal, got %v", err)
	}
	assertFileContents(t, config, sharedMemoryTestConfig, false)
	assertFileContents(t, filepath.Join(external, "SKILL.md"), "External nested skill.", false)
}

func assertSharedWorkspaceSkillPriority(t *testing.T, private, canonicalSkill, expectedContent string) {
	t.Helper()
	overrides := sharedSkillOverrides(t, private)
	if overrides[canonicalSkill] != false {
		t.Fatalf("canonical same-name skill is not disabled: %v", overrides)
	}
	privateSkill := filepath.Join(private, "skills", "writing", "SKILL.md")
	if enabled, overridden := overrides[privateSkill]; overridden && !enabled {
		t.Fatal("private workspace version was disabled")
	}
	if len(overrides) != 1 {
		t.Fatalf("workspace claim disabled unclaimed skills: %v", overrides)
	}
	assertFileContents(t, privateSkill, expectedContent, true)
}

func sharedSkillOverrides(t *testing.T, private string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(private, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Skills struct {
			Config []struct {
				Path    string `toml:"path"`
				Enabled bool   `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]bool)
	for _, override := range config.Skills.Config {
		if _, exists := result[override.Path]; exists {
			t.Fatalf("duplicate skill path override %q", override.Path)
		}
		result[override.Path] = override.Enabled
	}
	return result
}
