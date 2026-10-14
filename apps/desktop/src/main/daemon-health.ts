import { z } from "zod";

const daemonHealthSchema = z.object({
  status: z.string().optional(),
  pid: z.number().int().positive().optional(),
  profile: z.string().optional(),
  os: z.string().optional(),
  uptime: z.string().optional(),
  daemon_id: z.string().optional(),
  device_name: z.string().optional(),
  server_url: z.string().optional(),
  cli_version: z.string().optional(),
  active_task_count: z.number().int().nonnegative().optional(),
  agents: z.array(z.string()).optional(),
  workspaces: z.array(z.unknown()).optional(),
  codex_shared_home: z.boolean().optional(),
  codex_home: z.string().optional(),
  codex_path: z.string().optional(),
});

export type DaemonHealth = z.infer<typeof daemonHealthSchema>;

// Electron main externalizes workspace dependencies; importing the core
// package's raw TypeScript schema helper would fail in the installed app.
function parseWithFallback<T>(
  value: unknown,
  schema: z.ZodType<T>,
  fallback: T | null,
): T | null {
  const result = schema.safeParse(value);
  return result.success ? result.data : fallback;
}

/**
 * Old daemons may omit optional fields. Malformed known fields invalidate the
 * observation instead of manufacturing a running/profile/setting identity.
 * Unknown response fields are stripped and the raw payload is never logged.
 */
export function parseDaemonHealth(value: unknown): DaemonHealth | null {
  return parseWithFallback(value, daemonHealthSchema, null);
}
