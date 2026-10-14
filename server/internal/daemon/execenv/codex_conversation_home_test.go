package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexConversationHomePinsModeAcrossDefaultFlips(t *testing.T) {
	for _, shape := range []string{"managed", "in_place", "worktree"} {
		for _, originalShared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared_%v", shape, originalShared), func(t *testing.T) {
				home := t.TempDir()
				writeFile(t, filepath.Join(home, "config.toml"), sharedMemoryTestConfig)
				writeFile(t, filepath.Join(home, "memories_1.sqlite"), "preserve native database")
				t.Setenv("CODEX_HOME", home)
				t.Setenv(MulticaCodexMemoryEnv, "0")
				task := TaskContextForEnv{IssueID: "mode-issue", AgentID: "mode-agent"}
				params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "mode-ws", Provider: "codex", CodexSharedHome: originalShared, Task: task}
				if shape == "in_place" {
					params.LocalWorkDir = t.TempDir()
				} else if shape == "worktree" {
					params.LocalWorktree = &LocalWorktreeParams{LocalPath: newTestRepo(t)}
				}
				params.TaskID = "mode-first"
				first, err := Prepare(params, testLogger())
				if err != nil {
					t.Fatal(err)
				}
				defer first.Cleanup(true)
				rollout := filepath.Join(first.CodexHome, "sessions", "2026", "10", "14", "rollout-fixture-native-mode.jsonl")
				writeFile(t, rollout, "preserve native conversation")
				originalStore, err := filepath.EvalSymlinks(filepath.Join(first.CodexHome, "sessions"))
				if err != nil {
					t.Fatal(err)
				}
				for i, configured := range []bool{!originalShared, originalShared, !originalShared} {
					resolution := CodexConversationHomeParams{SharedHomeDefault: configured, Task: task, PriorSessionID: "native-mode"}
					if shape == "managed" {
						// The first environment still holds its proven root claim.
						resolution.PriorManagedHome = first.CodexHome
					}
					mode, err := ResolveCodexConversationSharedHome(resolution)
					if err != nil || mode != originalShared {
						t.Fatalf("flip %d configured %v resolved %v: %v", i, configured, mode, err)
					}
					var next *Environment
					if shape == "managed" {
						next = Reuse(ReuseParams{WorkDir: first.WorkDir, Provider: "codex", CodexSharedHome: mode, ResumeSessionID: "native-mode", Task: task}, testLogger())
					} else {
						params.TaskID = fmt.Sprintf("mode-next-%d", i)
						params.CodexSharedHome = mode
						next, err = Prepare(params, testLogger())
						if next != nil {
							defer next.Cleanup(true)
						}
					}
					if err != nil || next == nil {
						t.Fatalf("continue pinned native mode: %v", err)
					}
					store, err := filepath.EvalSymlinks(filepath.Join(next.CodexHome, "sessions"))
					if err != nil || store != originalStore || !CodexResumeRolloutPresent(next.CodexHome, "native-mode") {
						t.Fatalf("flip changed native store or rollout: store %q, original %q, err %v", store, originalStore, err)
					}
					if (next.CodexDataHome != "") != originalShared {
						t.Fatal("pinned preparation routed native state to another home")
					}
				}
				assertFileContents(t, rollout, "preserve native conversation", false)
				assertFileContents(t, filepath.Join(home, "config.toml"), sharedMemoryTestConfig, false)
				assertFileContents(t, filepath.Join(home, "memories_1.sqlite"), "preserve native database", false)
			})
		}
	}
}

func TestCodexConversationHomeNewSessionUsesDefaultWithoutReadingStores(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-home"))
	for _, configured := range []bool{false, true} {
		mode, err := ResolveCodexConversationSharedHome(CodexConversationHomeParams{SharedHomeDefault: configured})
		if err != nil || mode != configured {
			t.Fatalf("new native session did not use profile default: %v, %v", mode, err)
		}
	}
}

func TestCodexConversationHomeImportedEmptyOverlayUsesCanonicalEvidence(t *testing.T) {
	home, private := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeFile(t, filepath.Join(private, "config.toml"), "retained private task config")
	writeFile(t, filepath.Join(home, "sessions", "rollout-imported-native-id.jsonl"), "imported native conversation")
	mode, err := ResolveCodexConversationSharedHome(CodexConversationHomeParams{PriorSessionID: "native-id", PriorManagedHome: private})
	if err != nil || !mode {
		t.Fatalf("imported empty overlay did not select its confirmed canonical store: %v", err)
	}
	assertFileContents(t, filepath.Join(private, "config.toml"), "retained private task config", false)
}

func TestCodexConversationHomeRefusesMissingOrDistinctRollouts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	task := TaskContextForEnv{IssueID: "ambiguous-issue", AgentID: "ambiguous-agent"}
	params := CodexConversationHomeParams{Task: task, PriorSessionID: "native-id"}
	if _, err := ResolveCodexConversationSharedHome(params); err == nil || !strings.Contains(err.Error(), "no reachable native rollout") {
		t.Fatalf("missing prior native session could become a fresh replacement: %v", err)
	}
	canonical := filepath.Join(home, "sessions", "rollout-fixture-native-id.jsonl")
	scoped := filepath.Join(codexSessionStoreDir(home, codexSessionStoreKey("", task)), "rollout-fixture-native-id.jsonl")
	writeFile(t, canonical, "canonical original")
	writeFile(t, scoped, "scoped original")
	for _, configured := range []bool{false, true} {
		params.SharedHomeDefault = configured
		if _, err := ResolveCodexConversationSharedHome(params); err == nil || !strings.Contains(err.Error(), "distinct native stores") {
			t.Fatalf("ambiguous native stores selected the GUI default: %v", err)
		}
	}
	assertFileContents(t, canonical, "canonical original", false)
	assertFileContents(t, scoped, "scoped original", false)
}

func TestCodexConversationHomeDeduplicatesCanonicalStoreAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	task := TaskContextForEnv{IssueID: "alias-issue", AgentID: "alias-agent"}
	canonical := filepath.Join(home, "sessions")
	writeFile(t, filepath.Join(canonical, "rollout-fixture-native-id.jsonl"), "canonical native conversation")
	scoped := codexSessionStoreDir(home, codexSessionStoreKey("", task))
	if err := os.MkdirAll(filepath.Dir(scoped), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := createDirLink(canonical, scoped); err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	physical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(private, codexSharedHomeMarker), physical+"\n")
	if err := createDirLink(canonical, filepath.Join(private, "sessions")); err != nil {
		t.Fatal(err)
	}
	mode, err := ResolveCodexConversationSharedHome(CodexConversationHomeParams{Task: task, PriorSessionID: "native-id", PriorManagedHome: private})
	if err != nil || !mode {
		t.Fatalf("aliases of one native store were treated as ambiguous: %v", err)
	}
}

func TestCodexConversationHomeRejectsForeignAliasesAndBindingDrift(t *testing.T) {
	for _, target := range []string{"parent", "namespace", "scoped", "date", "marker"} {
		t.Run(target, func(t *testing.T) {
			home, foreign := t.TempDir(), t.TempDir()
			t.Setenv("CODEX_HOME", home)
			task := TaskContextForEnv{IssueID: "guard-issue", AgentID: "guard-agent"}
			canonical := filepath.Join(home, "sessions")
			writeFile(t, filepath.Join(canonical, "rollout-fixture-native-id.jsonl"), "keep canonical history")
			writeFile(t, filepath.Join(foreign, "rollout-fixture-native-id.jsonl"), "keep foreign history")
			params := CodexConversationHomeParams{Task: task, PriorSessionID: "native-id"}
			var alias string
			switch target {
			case "parent":
				alias = filepath.Join(home, codexSessionStoreRoot)
			case "namespace":
				alias = filepath.Join(home, codexSessionStoreRoot, codexSessionStoreNamespace(""))
			case "scoped":
				alias = codexSessionStoreDir(home, codexSessionStoreKey("", task))
			case "date":
				alias = filepath.Join(canonical, "2026")
			case "marker":
				params.PriorManagedHome = t.TempDir()
				writeFile(t, filepath.Join(params.PriorManagedHome, codexSharedHomeMarker), foreign)
			}
			if alias != "" {
				if err := os.MkdirAll(filepath.Dir(alias), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := createDirLink(foreign, alias); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ResolveCodexConversationSharedHome(params); err == nil {
				t.Fatal("unsafe native-home evidence selected a mode")
			}
			assertFileContents(t, filepath.Join(canonical, "rollout-fixture-native-id.jsonl"), "keep canonical history", false)
			assertFileContents(t, filepath.Join(foreign, "rollout-fixture-native-id.jsonl"), "keep foreign history", false)
		})
	}
}
