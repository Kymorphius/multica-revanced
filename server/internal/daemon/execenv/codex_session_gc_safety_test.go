package execenv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneCodexSessionStoresRefusesSymlinkedPaths(t *testing.T) {
	for _, level := range []string{"parent", "namespace", "agent", "store"} {
		t.Run(level, func(t *testing.T) {
			sharedHome := t.TempDir()
			t.Setenv("CODEX_HOME", sharedHome)
			storeRoot := filepath.Join(sharedHome, codexSessionStoreRoot)
			namespace := filepath.Join(storeRoot, codexSessionStoreNamespace(""))
			commonSessions := filepath.Join(sharedHome, "sessions")
			var alias, sentinel string
			switch level {
			case "parent":
				alias = storeRoot
				sentinel = filepath.Join(commonSessions, codexSessionStoreNamespace(""), "agent", "store", "rollout.jsonl")
			case "namespace":
				alias = namespace
				sentinel = filepath.Join(commonSessions, "agent", "store", "rollout.jsonl")
			case "agent":
				alias = filepath.Join(namespace, "agent")
				sentinel = filepath.Join(commonSessions, "store", "rollout.jsonl")
			case "store":
				alias = filepath.Join(namespace, "agent", "store")
				sentinel = filepath.Join(commonSessions, "rollout.jsonl")
			}
			writeFile(t, sentinel, "canonical history must survive")
			if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := createDirLink(commonSessions, alias); err != nil {
				t.Fatal(err)
			}
			removed, freed := PruneCodexSessionStores("", time.Hour, time.Now().Add(48*time.Hour), nil, testLogger())
			if removed != 0 || freed != 0 {
				t.Fatalf("GC followed a %s alias: removed %d, freed %d", level, removed, freed)
			}
			assertFileContents(t, sentinel, "canonical history must survive", false)
		})
	}
}

func TestPruneCodexSessionStoresAcceptsConfiguredHomeAlias(t *testing.T) {
	sharedHome := t.TempDir()
	alias := filepath.Join(t.TempDir(), "codex-home")
	if err := createDirLink(sharedHome, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", alias)
	store := filepath.Join(sharedHome, codexSessionStoreRoot, codexSessionStoreNamespace(""), "agent", "store")
	writeFile(t, filepath.Join(store, "rollout.jsonl"), "expired isolated history")
	reserved := filepath.Join(alias, codexSessionStoreRoot, codexSessionStoreNamespace(""), "agent", "store")
	guard := func(path string) (func(), bool) {
		if path != reserved {
			t.Fatalf("reservation path = %q, want configured home spelling %q", path, reserved)
		}
		return nil, false
	}
	if removed, _ := PruneCodexSessionStores("", time.Hour, time.Now().Add(48*time.Hour), guard, testLogger()); removed != 0 {
		t.Fatal("GC ignored a reservation through the configured home alias")
	}
	removed, freed := PruneCodexSessionStores("", time.Hour, time.Now().Add(48*time.Hour), nil, testLogger())
	if removed != 1 || freed == 0 {
		t.Fatalf("GC did not resolve the configured home alias: removed %d, freed %d", removed, freed)
	}
}
