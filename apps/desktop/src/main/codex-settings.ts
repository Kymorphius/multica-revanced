import type { CodexSettings } from "../shared/daemon-types";

export interface CodexSettingsHealth {
  status?: unknown;
  profile?: unknown;
  codex_shared_home?: unknown;
  codex_home?: unknown;
  codex_path?: unknown;
}

export function assertCodexSharedHomeInput(enabled: unknown): asserts enabled is boolean {
  if (typeof enabled !== "boolean") {
    throw new Error("Codex shared home must be a boolean");
  }
}

export function codexSettingsSnapshot({
  profile,
  savedSharedHome,
  health,
  externallyManaged,
  environmentValue,
}: {
  profile?: string;
  savedSharedHome: boolean;
  health: CodexSettingsHealth | null;
  externallyManaged: boolean;
  environmentValue?: string;
}): CodexSettings {
  const environmentOverride = Boolean(environmentValue);
  const settings: CodexSettings = {
    profile,
    savedSharedHome,
    editable: Boolean(profile) && !externallyManaged && !environmentOverride,
    environmentOverride,
  };
  // Saved preferences never stand in for observations of the running daemon.
  if (
    profile &&
    health?.profile === profile &&
    (health.status === "running" || health.status === "starting")
  ) {
    if (typeof health.codex_shared_home === "boolean") {
      settings.effectiveSharedHome = health.codex_shared_home;
    }
    if (typeof health.codex_home === "string" && health.codex_home) {
      settings.home = health.codex_home;
    }
    if (typeof health.codex_path === "string" && health.codex_path) {
      settings.executable = health.codex_path;
    }
  }
  return settings;
}
