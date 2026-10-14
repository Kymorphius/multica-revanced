package daemon

import (
	"errors"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// configureCodexDataEnvironment runs after custom_env layering. The task home
// remains the private configuration and skills source; only native state uses
// the shared data home. Both data variables are owned by the daemon.
func configureCodexDataEnvironment(provider string, env *execenv.Environment, values map[string]string) error {
	if provider != "codex" {
		return nil
	}
	if env == nil || !filepath.IsAbs(env.CodexHome) {
		return errors.New("configure Codex data environment: private config home is missing")
	}
	dataHome := env.CodexHome
	delete(values, "MULTICA_CODEX_TASK_CONFIG_HOME")
	if env.CodexDataHome != "" {
		if !filepath.IsAbs(env.CodexDataHome) || filepath.Clean(env.CodexHome) == filepath.Clean(env.CodexDataHome) {
			return errors.New("configure Codex data environment: invalid shared data home")
		}
		dataHome = env.CodexDataHome
		values["MULTICA_CODEX_TASK_CONFIG_HOME"] = env.CodexHome
	}
	values["CODEX_HOME"] = dataHome
	values["CODEX_SQLITE_HOME"] = dataHome
	return nil
}
