// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseDaemonHealth } from "./daemon-health";

describe("local daemon health schema", () => {
  it("accepts older daemons without inventing Codex runtime observations", () => {
    expect(parseDaemonHealth({ status: "running", profile: "desktop-test", pid: 42 })).toEqual({
      status: "running", profile: "desktop-test", pid: 42,
    });
    expect(parseDaemonHealth({})).toEqual({});
  });

  it("preserves the known fields and drops unknown response data", () => {
    const health = {
      status: "running", pid: 42, profile: "desktop-test", os: "darwin",
      uptime: "2m", daemon_id: "daemon", device_name: "machine",
      server_url: "https://example.test", cli_version: "0.6.2", active_task_count: 0,
      agents: ["codex"], workspaces: [{ id: "workspace" }], codex_shared_home: false,
      codex_home: "", codex_path: "/test/native/codex",
    };
    expect(parseDaemonHealth({ ...health, token: "must-not-return", future_field: true })).toEqual(health);
  });

  it.each([null, "running", [], 123, { status: 1 }, { profile: false }, { pid: 0 },
    { pid: 1.5 }, { server_url: [] }, { os: {} }, { agents: ["codex", 1] },
    { workspaces: {} }, { active_task_count: -1 }, { codex_shared_home: "false" },
    { codex_shared_home: null }, { codex_home: 123 }, { codex_path: false },
  ])("safely rejects malformed known fields (%s)", (value) => {
    expect(parseDaemonHealth(value)).toBeNull();
  });
});
