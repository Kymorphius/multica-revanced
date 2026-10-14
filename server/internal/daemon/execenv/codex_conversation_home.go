package execenv

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CodexConversationHomeParams selects the data mode of one native conversation.
// PriorManagedHome is admitted only after the daemon validates and claims the
// prior managed env root. User-supplied local directories are never evidence.
type CodexConversationHomeParams struct {
	SharedHomeDefault bool
	Profile           string
	Task              TaskContextForEnv
	PriorSessionID    string
	PriorManagedHome  string
}

// ResolveCodexConversationSharedHome applies the profile default to new native
// conversations and pins existing ones to unambiguous rollout evidence. It
// reads filenames and private binding metadata only; it never moves data.
func ResolveCodexConversationSharedHome(params CodexConversationHomeParams) (bool, error) {
	if params.PriorSessionID == "" {
		return params.SharedHomeDefault, nil
	}
	if strings.ContainsAny(params.PriorSessionID, "/\\\x00") {
		return false, fmt.Errorf("invalid prior Codex native session identifier")
	}
	home, err := ResolveSharedCodexHome()
	if err != nil {
		return false, err
	}
	canonical, err := codexEvidenceDirectory(filepath.Join(home, "sessions"), nil, true)
	if err != nil {
		return false, err
	}
	scoped, err := codexScopedEvidenceDirectory(home, codexSessionStoreKey(params.Profile, params.Task), canonical)
	if err != nil {
		return false, err
	}
	roots := []string{canonical, scoped}
	var binding *bool
	var privateRoot string
	if params.PriorManagedHome != "" {
		privateRoot, binding, err = codexManagedEvidenceDirectory(params.PriorManagedHome, home, canonical, scoped)
		if err != nil {
			return false, err
		}
		roots = append(roots, privateRoot)
	}
	seen := make(map[string]struct{})
	found := make(map[string]bool)
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		present, err := codexEvidenceRolloutPresent(root, params.PriorSessionID)
		if err != nil {
			return false, err
		}
		if present {
			found[root] = root == canonical
		}
	}
	if len(found) != 1 {
		if len(found) == 0 {
			return false, fmt.Errorf("prior Codex session %s has no reachable native rollout; refusing to replace its conversation", params.PriorSessionID)
		}
		return false, fmt.Errorf("prior Codex session %s exists in distinct native stores; its shared-home mode is ambiguous", params.PriorSessionID)
	}
	for root, shared := range found {
		if binding != nil && (root != privateRoot || shared != *binding) {
			return false, fmt.Errorf("prior Codex session %s disagrees with its managed native-home binding", params.PriorSessionID)
		}
		return shared, nil
	}
	return false, fmt.Errorf("prior Codex conversation home could not be resolved")
}

func codexScopedEvidenceDirectory(home, key, canonical string) (string, error) {
	if key == "" {
		return "", nil
	}
	parts := append([]string{codexSessionStoreRoot}, strings.Split(key, string(filepath.Separator))...)
	path := home
	for i, part := range parts {
		path = filepath.Join(path, part)
		allowed := []string(nil)
		if i == len(parts)-1 && canonical != "" {
			allowed = []string{canonical}
		}
		physical, err := codexEvidenceDirectory(path, allowed, false)
		if err != nil || physical == "" {
			return "", err
		}
		if physical != path {
			return physical, nil // only a final alias to the canonical store
		}
	}
	return path, nil
}

func codexManagedEvidenceDirectory(private, home, canonical, scoped string) (string, *bool, error) {
	info, err := os.Lstat(private)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("prior managed Codex config home must be a real private directory")
	}
	var binding *bool
	marker := filepath.Join(private, codexSharedHomeMarker)
	if info, err := os.Lstat(marker); err == nil {
		if !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("prior managed Codex native-home marker must be a regular file")
		}
		data, err := os.ReadFile(marker)
		if err != nil {
			return "", nil, fmt.Errorf("read prior managed Codex native-home marker: %w", err)
		}
		dataHome := strings.TrimSpace(string(data))
		if dataHome == "" || !filepath.IsAbs(dataHome) || !sameResolvedCodexPath(dataHome, home) {
			return "", nil, fmt.Errorf("prior managed Codex native-home marker names a different data home")
		}
		shared := true
		binding = &shared
	} else if !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("inspect prior managed Codex native-home marker: %w", err)
	}
	allowed := []string{canonical, scoped}
	root, err := codexEvidenceDirectory(filepath.Join(private, "sessions"), allowed, false)
	if binding != nil && root == "" {
		root = canonical // native CODEX_HOME stays common even before alias repair
	}
	return root, binding, err
}

func codexEvidenceDirectory(path string, allowedLinks []string, allowRootAlias bool) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect prior Codex sessions path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		physical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("resolve prior Codex sessions alias %s: %w", path, err)
		}
		allowed := allowRootAlias
		for _, target := range allowedLinks {
			allowed = allowed || target != "" && sameResolvedCodexPath(physical, target)
		}
		if !allowed {
			return "", fmt.Errorf("prior Codex sessions path %s is a foreign store alias", path)
		}
		if info, err := os.Lstat(physical); err != nil || !info.IsDir() {
			return "", fmt.Errorf("prior Codex sessions alias %s does not resolve to a directory", path)
		}
		return physical, nil
	}
	if !info.IsDir() {
		return "", fmt.Errorf("prior Codex sessions path %s must be a directory", path)
	}
	return filepath.EvalSymlinks(path)
}

func codexEvidenceRolloutPresent(root, sessionID string) (bool, error) {
	const maxEntries = 20000
	entriesSeen := 0
	present := false
	var scan func(string, int) error
	scan = func(dir string, depth int) error {
		expected, err := os.Lstat(dir)
		if err != nil || !expected.IsDir() || expected.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("prior Codex rollout directory %s must be a real directory", dir)
		}
		file, err := os.Open(dir)
		if err != nil {
			return fmt.Errorf("read prior Codex rollout directory %s: %w", dir, err)
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(expected, opened) {
			return fmt.Errorf("prior Codex rollout directory %s changed during inspection", dir)
		}
		for {
			entries, readErr := file.ReadDir(256)
			for _, entry := range entries {
				entriesSeen++
				if entriesSeen > maxEntries {
					return fmt.Errorf("prior Codex rollout evidence exceeds the safe scan limit")
				}
				name := entry.Name()
				path := filepath.Join(dir, name)
				if strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, "-"+sessionID+".jsonl") || strings.HasSuffix(name, "-"+sessionID+".jsonl.zst")) {
					info, err := os.Lstat(path)
					if err != nil || !info.Mode().IsRegular() {
						return fmt.Errorf("prior Codex rollout %s must be a regular file", path)
					}
					present = true
				}
				if depth < 3 && codexEvidenceDateSegment(name, depth) {
					info, err := os.Lstat(path)
					if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
						return fmt.Errorf("prior Codex rollout date path %s must be a real directory", path)
					}
					if err := scan(path, depth+1); err != nil {
						return err
					}
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					return fmt.Errorf("read prior Codex rollout directory %s: %w", dir, readErr)
				}
				current, err := os.Lstat(dir)
				if err != nil || !os.SameFile(expected, current) || current.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("prior Codex rollout directory %s changed during inspection", dir)
				}
				return nil
			}
		}
	}
	err := scan(root, 0)
	return present, err
}

func codexEvidenceDateSegment(name string, depth int) bool {
	want := 2
	if depth == 0 {
		want = 4
	}
	if len(name) != want {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
