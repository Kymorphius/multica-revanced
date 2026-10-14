# Shared Codex native state

Daemon-managed Codex tasks normally use isolated native state. A personal daemon
profile can opt into the user's shared Codex home, allowing terminal, desktop,
and Multica tasks to use the same native conversation history and memory store.

## Select the mode

Persist the choice in the daemon's profile:

```sh
multica --profile <profile> config set codex_shared_home true
```

The setting is resolved at daemon startup. Resolution order is an explicit
`--codex-shared-home` flag, `MULTICA_CODEX_SHARED_HOME`, the profile's
`codex_shared_home` boolean, then `false`. An explicit false overrides an
enabled lower-priority setting. An empty config-set value clears the saved
choice. Desktop preserves the profile setting across restarts.

Switch only while that profile is idle, including pending terminal reports.
Archive and import pre-existing isolated histories before enabling the mode.
Preparation refuses to repoint a private home containing native data; it does
not delete or silently abandon the previous conversation to make the switch.

## Configuration and native data

The task retains its private configuration home for generated sandbox, shell,
MCP, and skill policy. Both `CODEX_HOME` and `CODEX_SQLITE_HOME` select the shared
data home for the native process. `MULTICA_CODEX_TASK_CONFIG_HOME` identifies the
private configuration source to the Codex backend. The daemon owns these
environment variables; agent custom environment values cannot override them.

The backend materializes MCP configuration only into the private file, parses
the complete task configuration, and sends it over stdin JSON RPC on both
`thread/start` and `thread/resume`. MCP secrets never need command-line flags.
Omitted inherited MCP servers are disabled in a managed task. Incompatible
same-name inherited definitions must fail rather than retain unintended
transport or credential fields through recursive native configuration merging.

Private task skills are registered with `skills/extraRoots/set` before starting
the thread. This is process-scoped; Multica starts a separate app-server per
task. An unsupported native RPC fails explicitly instead of reverting silently
to isolated state. Codex app-server does not support the CLI's `-p` profile-file
selector, so it is not used for this integration.

## Persistence and concurrency

Shared sessions, archived sessions, index, native SQLite databases and their
WAL files, memory directory, generation lease, and native thread writer locks
belong to one data home. Preparation uses a dedicated shared branch and never
applies the isolated legacy-session reset routine to those paths.

Multica retains its per-conversation lifetime lock and real-rollout resume
presence check. The shared native thread lock also protects the same thread
from another Multica task, terminal client, or desktop client. Different
threads remain independent; the whole shared sessions directory is not a
single task-lifetime lock.

Session-store garbage collection continues to target only the legacy
`multica-sessions` namespace. It must never follow a shared alias or reclaim the
canonical sessions or memories. Task configuration cleanup must not follow its
native-state links.

## Verification

Default tests use temporary homes and fake executables. They cover opt-in
precedence, preparation and reuse, preservation/refusal of existing native data,
global configuration invariance, full RPC configuration on start/resume, private
skills, inherited MCP handling, and protected data environment variables.

Runtime acceptance separately checks the loaded daemon build, persisted
profile, native home and SQLite root, configuration invariance, cross-client
history/resume, and memory source access. A successful source build does not
prove the installed Desktop uses it: Desktop prefers its bundled CLI. Keep the
original signed application when deploying a locally signed derived build.
