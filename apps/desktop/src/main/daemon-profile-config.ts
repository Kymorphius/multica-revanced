import { randomUUID } from "node:crypto";
import { mkdir, open, readFile, rename, rm } from "node:fs/promises";
import { dirname } from "node:path";
import { assertResolvedProfile } from "./daemon-profile";

type ProfileConfig = Record<string, unknown>;

export function parseProfileConfig(raw: string): ProfileConfig {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new Error("daemon profile config is not valid JSON");
  }
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("daemon profile config must be an object");
  }
  const config = value as ProfileConfig;
  if (
    "codex_shared_home" in config &&
    typeof config.codex_shared_home !== "boolean"
  ) {
    throw new Error("daemon profile codex_shared_home must be a boolean");
  }
  return config;
}

/** All Desktop profile writers merge their owned fields inside one queue. */
export class DaemonProfileConfigStore {
  private writeChain: Promise<void> = Promise.resolve();

  constructor(private readonly resolvePath: (profile: string) => string) {}

  private async readCurrent(profile: string): Promise<ProfileConfig> {
    assertResolvedProfile(profile);
    try {
      return parseProfileConfig(await readFile(this.resolvePath(profile), "utf-8"));
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") return {};
      throw error;
    }
  }

  async read(profile: string): Promise<ProfileConfig> {
    assertResolvedProfile(profile);
    await this.writeChain;
    return this.readCurrent(profile);
  }

  update(
    profile: string,
    mutate: (current: ProfileConfig) => void,
  ): Promise<void> {
    assertResolvedProfile(profile);
    const operation = async () => {
      const config = await this.readCurrent(profile);
      const before = JSON.stringify(config);
      mutate(config);
      const content = JSON.stringify(config, null, 2);
      // Validate updates too, before opening any output file.
      parseProfileConfig(content);
      if (JSON.stringify(config) === before) return;
      const path = this.resolvePath(profile);
      await mkdir(dirname(path), { recursive: true });
      const temporaryPath = `${path}.${randomUUID()}.tmp`;
      try {
        const file = await open(temporaryPath, "wx", 0o600);
        try {
          await file.writeFile(content, "utf-8");
          await file.sync();
        } finally {
          await file.close();
        }
        await rename(temporaryPath, path);
      } finally {
        await rm(temporaryPath, { force: true });
      }
    };
    const next = this.writeChain.then(operation);
    this.writeChain = next.catch(() => {});
    return next;
  }
}
