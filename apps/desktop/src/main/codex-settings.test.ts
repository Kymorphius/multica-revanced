// @vitest-environment node
import { describe, expect, it } from "vitest";
import { assertCodexSharedHomeInput, codexSettingsSnapshot } from "./codex-settings";

const context = {
  profile: "desktop-test",
  savedSharedHome: true,
  externallyManaged: false,
};

describe("Codex settings IPC projection", () => {
  it("keeps saved and running states separate and exposes only runtime identity", () => {
    const health = {
      status: "running",
      profile: "desktop-test",
      codex_shared_home: false,
      codex_home: "/test/home",
      codex_path: "/test/bin/codex",
      token: "must-not-return",
    };
    expect(codexSettingsSnapshot({
      ...context,
      health,
    })).toEqual({
      profile: "desktop-test",
      savedSharedHome: true,
      effectiveSharedHome: false,
      home: "/test/home",
      executable: "/test/bin/codex",
      editable: true,
      environmentOverride: false,
    });
  });

  it.each([null, { status: "stopped", profile: "desktop-test", codex_shared_home: true }, {
    status: "running", profile: "other", codex_shared_home: true,
  }, {
    status: "running", profile: "desktop-test", codex_shared_home: "true", codex_home: 123, codex_path: false,
  }])("does not invent effective state from missing or malformed health", (health) => {
    const settings = codexSettingsSnapshot({ ...context, health });
    expect(settings).not.toHaveProperty("effectiveSharedHome");
    expect(settings).not.toHaveProperty("home");
    expect(settings).not.toHaveProperty("executable");
    expect(settings.savedSharedHome).toBe(true);
  });

  it.each(["true", "false", "invalid", " "])("disables edits for any nonempty environment override (%s)", (environmentValue) => {
    expect(codexSettingsSnapshot({ ...context, health: null, environmentValue })).toMatchObject({
      editable: false,
      environmentOverride: true,
    });
  });

  it("disables edits for unresolved profiles and externally managed daemons", () => {
    expect(codexSettingsSnapshot({ ...context, profile: undefined, health: null }).editable).toBe(false);
    expect(codexSettingsSnapshot({ ...context, externallyManaged: true, health: null }).editable).toBe(false);
    expect(codexSettingsSnapshot({ ...context, environmentValue: "", health: null }).editable).toBe(true);
  });

  it.each([undefined, null, "true", "false", 0, 1, {}, []])("rejects nonboolean IPC input (%s)", (enabled) => {
    expect(() => assertCodexSharedHomeInput(enabled)).toThrow("must be a boolean");
  });
});
