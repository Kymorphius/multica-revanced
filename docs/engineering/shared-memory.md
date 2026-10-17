# Shared Multica memory

Multica stores workspace and optional project memories in PostgreSQL. Claude,
Codex, Hermes and other providers receive the same authorized task snapshot;
each provider's native memory remains separate. New memory management is
available from the workspace's Memories page in web and desktop, and from
`multica memory` in the CLI.

## Saving and recall

Manual saves use `POST /api/memories`, either through the page or an agent
responding to an explicit request to remember something. Each record has a
title, content, optional project, original author and source, timestamps and
monotonic revision. Workspace memories apply to all tasks in that workspace;
project memories are also included in that project's tasks. The list endpoint
supports literal case-insensitive search and bounded pagination.

At claim time the server loads a bounded snapshot after validating the task's
workspace. The daemon renders the snapshot in per-turn context for every
provider, including resumed conversations. The snapshot includes IDs and
revisions and marks omitted results so the agent can search for more. Memory
content is reference data, not higher-priority instructions. A correction or
deletion changes later snapshots; previous transcripts retain their history.

## Automatic task experience

Workspace owners and admins can enable automatic extraction from the page or
`multica memory settings update --automatic-extraction=true`. The setting is
off by default. For an enabled public issue task, the per-turn brief asks the
executing agent to identify verified, reusable facts and lessons, search for
duplicates, then save useful entries through `multica memory`. It does not
require an entry from every task. No second model, queue, or historical
transcript scan is introduced.

The server permits automatic-source saves only for authenticated public issue
tasks when the setting is enabled. Private chats, autopilots and quick-create
contexts cannot publish automatic memories. A private chat can save a memory
when the user explicitly requests sharing it with the workspace or project.
An agent may decide that nothing in a task is worth remembering; extraction
quality depends on the agent following the brief and making suitable tool
calls, as with other agent-driven actions.

## Authorization and concurrent edits

All operations are workspace-scoped and require workspace membership. Members,
admins and owners may edit shared records; viewer policy permits reads only.
Settings changes require a human owner or admin. Task credentials supply the
authoritative agent, task and workspace identity, and project writes must match
the task's attached project. Cloud machine credentials cannot write memories.

`PUT /api/memories/{id}` requires `expected_revision`; delete requires that value
as a query parameter. Both use a database compare-and-swap and return 409 when
the record has changed. The UI preserves unsaved edits on an error. Original
author, scope and source are immutable. Project and workspace deletion remove
their memories in the existing cleanup transactions.

## Verification and rollout

Migrations 573 and 574 create the tables and a concurrent context index without
foreign keys. Use the managed checkout environment for migrations and DB tests.
Focused tests cover tenant/project isolation, task-token authority, automatic
publication gates, revision conflicts, cleanup, prompt bounds, CLI contracts,
API response parsing, and management interactions.

Source verification does not change an installed desktop or a remote server.
For a rollout, apply the migrations, deploy the server and matching daemon/CLI,
and load the updated web or desktop client. Enable automatic extraction only in
the intended workspace. Older daemons ignore the optional task fields and do
not gain recall or extraction behavior until updated.
