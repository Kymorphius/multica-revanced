package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const codexSharedHomeMarker = ".multica-codex-shared-home"

// ResolveSharedCodexHome returns the physical home selected by CODEX_HOME (or
// ~/.codex). Shared mode requires an existing directory so a stale alias cannot
// silently create an unrelated native data store.
func ResolveSharedCodexHome() (string, error) {
	path, err := filepath.EvalSymlinks(resolveSharedCodexHome())
	if err != nil {
		return "", fmt.Errorf("resolve shared Codex home: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve shared Codex home: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("shared Codex home %s must be an existing directory", path)
	}
	return path, nil
}

var sharedCodexDataPaths = []struct {
	name string
	dir  bool
}{
	{"sessions", true},
	{"archived_sessions", true},
	{"thread-writer-locks", true},
	{"session_index.jsonl", false},
}

// prepareSharedCodexDataLinks exposes native data to the private task config
// home, primarily for existing rollout-presence checks. The backend itself uses
// sharedHome as CODEX_HOME. Never run the isolated-store migration/reset here:
// native SQLite, memories and writer locks belong to the common home.
func prepareSharedCodexDataLinks(codexHome, sharedHome string) error {
	markerPresent, err := validateSharedCodexTaskHome(codexHome, sharedHome)
	if err != nil {
		return err
	}
	marker := filepath.Join(codexHome, codexSharedHomeMarker)
	// Mark the mode before publishing any links, so a partially prepared home
	// cannot later enter the isolated legacy migration branch.
	if !markerPresent {
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create shared Codex task config marker: %w", err)
		}
		_, writeErr := f.WriteString(sharedHome + "\n")
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, entry := range sharedCodexDataPaths {
		src := filepath.Join(sharedHome, entry.name)
		dst := filepath.Join(codexHome, entry.name)
		if err := validateSharedCodexLink(dst, src, entry.dir); err != nil {
			return err
		}
		if info, err := os.Lstat(dst); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			// os.Remove refuses a directory that gained contents after validation.
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("remove empty private Codex data path %s: %w", dst, err)
			}
		}
		if entry.dir {
			if err := os.MkdirAll(src, 0o700); err != nil {
				return fmt.Errorf("create shared Codex data directory %s: %w", src, err)
			}
			if err := createDirLink(src, dst); err != nil {
				return fmt.Errorf("link shared Codex data directory %s: %w", dst, err)
			}
		} else if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("link shared Codex data file %s: %w", dst, err)
		}
	}
	return nil
}

// ValidateSharedCodexTaskHome checks an existing private overlay before its
// owning env root may be reset. Callers must hold the env-root claim while
// validating and resetting, so local native history cannot disappear before
// shared preparation has a chance to refuse the mode switch.
func ValidateSharedCodexTaskHome(codexHome string) error {
	if _, err := os.Lstat(codexHome); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect shared Codex task config home: %w", err)
	}
	sharedHome, err := ResolveSharedCodexHome()
	if err != nil {
		return err
	}
	_, err = validateSharedCodexTaskHome(codexHome, sharedHome)
	return err
}

func validateSharedCodexTaskHome(codexHome, sharedHome string) (bool, error) {
	info, err := os.Lstat(codexHome)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("shared Codex task config home %s must be a private directory", codexHome)
	}
	if sameResolvedCodexPath(codexHome, sharedHome) {
		return false, fmt.Errorf("shared Codex task config home must differ from the native data home")
	}
	marker := filepath.Join(codexHome, codexSharedHomeMarker)
	markerPresent := false
	if info, err := os.Lstat(marker); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("shared Codex task config marker must be a regular file")
		}
		data, err := os.ReadFile(marker)
		if err != nil || strings.TrimSpace(string(data)) != sharedHome {
			return false, fmt.Errorf("shared Codex task config marker belongs to another native data home")
		}
		markerPresent = true
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect shared Codex task config marker: %w", err)
	}
	// Validate every local native path before replacing any empty path or
	// copying config. Existing authoritative data requires an explicit import.
	for _, entry := range sharedCodexDataPaths {
		src := filepath.Join(sharedHome, entry.name)
		if info, err := os.Stat(src); err == nil {
			if info.IsDir() != entry.dir {
				return false, fmt.Errorf("shared Codex data path %s has an unexpected type", src)
			}
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect shared Codex data path %s: %w", src, err)
		}
		if err := validateSharedCodexLink(filepath.Join(codexHome, entry.name), filepath.Join(sharedHome, entry.name), entry.dir); err != nil {
			return false, err
		}
	}
	entries, err := os.ReadDir(codexHome)
	if err != nil {
		return false, fmt.Errorf("read shared Codex task config home: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "memories" || strings.Contains(name, ".sqlite") {
			if err := refusePrivateCodexData(filepath.Join(codexHome, name)); err != nil {
				return false, err
			}
		}
	}
	return markerPresent, nil
}

func validateSharedCodexLink(dst, src string, dir bool) error {
	info, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect private Codex data path %s: %w", dst, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if sameResolvedCodexPath(dst, src) {
			return nil
		}
		return privateCodexDataError(dst)
	}
	if dir && info.IsDir() {
		return refusePrivateCodexData(dst)
	}
	// An existing index is authoritative, including an empty index. Keep it
	// until the operator has explicitly archived/imported the private store.
	return privateCodexDataError(dst)
}

func refusePrivateCodexData(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private Codex data path %s: %w", path, err)
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("read private Codex data path %s: %w", path, err)
		}
		if len(entries) == 0 {
			return nil
		}
	}
	return privateCodexDataError(path)
}

func privateCodexDataError(path string) error {
	return fmt.Errorf("private Codex data exists at %s; archive/import this store before enabling shared Codex home", path)
}

func sameResolvedCodexPath(a, b string) bool {
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		} else if target, linkErr := os.Readlink(path); linkErr == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			path = target
		}
		// session_index.jsonl may not exist yet. Resolve its existing parent
		// so aliases of the common home still compare equal for a dangling link.
		if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			path = filepath.Join(parent, filepath.Base(path))
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		return filepath.Clean(path)
	}
	return resolve(a) == resolve(b)
}
