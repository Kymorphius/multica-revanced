package execenv

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ensureSharedWorkspaceSkillOverrides hides the canonical provider skills that
// yield to workspace-assigned top-level names. Shared CODEX_HOME discovery sees
// both user roots and private extra roots; equal names alone do not shadow the
// canonical skill because Codex deduplicates by canonical document path.
func ensureSharedWorkspaceSkillOverrides(configPath, sharedHome string, workspaceSkills []SkillContextForEnv) error {
	if sharedHome == "" || len(workspaceSkills) == 0 {
		return nil
	}
	root := filepath.Join(sharedHome, "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read shared Codex skills for workspace overrides: %w", err)
	}
	paths := make([]string, 0, len(workspaceSkills))
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !workspaceClaimsRuntimeSkill(entry.Name(), workspaceSkills) {
			continue
		}
		// Installers may link the top-level provider root to ~/.agents/skills.
		// Resolve that explicit root once; nested traversal never follows links.
		skillRoot, err := filepath.EvalSymlinks(filepath.Join(root, entry.Name()))
		if err != nil {
			return fmt.Errorf("resolve claimed shared Codex skill %s: %w", entry.Name(), err)
		}
		info, err := os.Lstat(skillRoot)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("claimed shared Codex skill %s must be a directory", entry.Name())
		}
		documents, err := sharedCodexSkillDocuments(skillRoot)
		if err != nil {
			return err
		}
		for _, path := range documents {
			if _, exists := seen[path]; exists {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return appendCodexDisabledSkillPaths(configPath, paths)
}

func sharedCodexSkillDocuments(root string) ([]string, error) {
	const maxDepth, maxDirectories, maxEntries = 6, 2000, 20000
	paths := make([]string, 0, 1)
	entries, directories := 0, 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("inspect claimed shared Codex skill path %s: %w", path, err)
		}
		entries++
		if entries > maxEntries {
			return fmt.Errorf("claimed shared Codex skill %s exceeds the safe traversal limit", root)
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			// Native user discovery follows directory links. Refuse ambiguous
			// trees instead of silently leaving a competing skill enabled.
			return fmt.Errorf("claimed shared Codex skill contains a nested link at %s; use a real skill tree for the workspace override", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		depth := len(strings.Split(filepath.ToSlash(rel), "/"))
		if entry.IsDir() {
			directories++
			if directories > maxDirectories {
				return fmt.Errorf("claimed shared Codex skill %s exceeds the safe traversal limit", root)
			}
			if path != root && depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "SKILL.md" {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("claimed shared Codex skill document %s must be a regular file", path)
			}
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}
