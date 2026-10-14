// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import { mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DaemonProfileConfigStore, parseProfileConfig } from "./daemon-profile-config";

const directories: string[] = [];

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), "multica-profile-config-"));
  directories.push(directory);
  const file = join(directory, "config.json");
  return { directory, file, store: new DaemonProfileConfigStore(() => file) };
}

afterEach(async () => {
  await Promise.all(directories.splice(0).map((path) => rm(path, { recursive: true, force: true })));
});

describe("Desktop profile config", () => {
  it("merges concurrent preference, auth, and server updates without stale snapshots", async () => {
    const { store, file, directory } = await fixture();
    await writeFile(file, JSON.stringify({ token: "previous", disable_auto_update: true }));

    await Promise.all([
      store.update("desktop-test", (config) => { config.codex_shared_home = true; }),
      store.update("desktop-test", (config) => { config.token = "new"; }),
      store.update("desktop-test", (config) => { config.server_url = "https://example.test"; }),
    ]);

    await expect(store.read("desktop-test")).resolves.toEqual({
      token: "new",
      disable_auto_update: true,
      codex_shared_home: true,
      server_url: "https://example.test",
    });
    expect((await stat(file)).mode & 0o777).toBe(0o600);
    expect(await readdir(directory)).toEqual(["config.json"]);
  });

  it("preserves the existing file and resumes the queue after a rejected update", async () => {
    const { store, file } = await fixture();
    const original = JSON.stringify({ token: "keep", codex_shared_home: false });
    await writeFile(file, original);
    await expect(store.update("desktop-test", (config) => {
      config.token = "discard";
      config.codex_shared_home = "true";
    })).rejects.toThrow("must be a boolean");
    expect(await readFile(file, "utf-8")).toBe(original);

    await store.update("desktop-test", (config) => { config.codex_shared_home = true; });
    await expect(store.read("desktop-test")).resolves.toEqual({ token: "keep", codex_shared_home: true });
  });

  it.each(["{bad", "null", "[]", '{"codex_shared_home":"false"}', '{"codex_shared_home":null}'])(
    "rejects malformed config instead of replacing it (%s)",
    async (raw) => {
      const { store, file } = await fixture();
      await writeFile(file, raw);
      await expect(store.read("desktop-test")).rejects.toThrow();
      await expect(store.update("desktop-test", (config) => { config.codex_shared_home = true; })).rejects.toThrow();
      expect(await readFile(file, "utf-8")).toBe(raw);
    },
  );

  it("treats only a missing file as an empty profile and rejects default-profile access", async () => {
    const { store, file } = await fixture();
    await expect(store.read("desktop-test")).resolves.toEqual({});
    await expect(store.read("")).rejects.toThrow("unresolved");
    expect(() => store.update("", () => {})).toThrow("unresolved");
    await expect(readFile(file)).rejects.toMatchObject({ code: "ENOENT" });
    expect(parseProfileConfig('{"codex_shared_home":false}')).toEqual({ codex_shared_home: false });
  });
});
