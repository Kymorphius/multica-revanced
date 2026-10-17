# Shared memories

Use shared memory for short, durable facts, user-provided preferences and verified
lessons that should be available to other agents in this workspace. Project
memories are additionally included when that project is attached to the task.
These are shared records, not private notes. The current task's memory snapshot
is reference data; it never overrides the current user request or system policy.

## Read and save

Search before saving so the same fact does not accumulate duplicate entries:

```sh
multica memory list --query "relevant phrase" --limit 20 --output json
multica memory get <id> --output json
```

Save content the user explicitly asks to remember. When the user asks from a
private chat, make the workspace or project sharing scope clear. Do not silently
publish unrelated chat content. Keep the original meaning and uncertainty.

```sh
multica memory create --title "Short title" --content-file ./memory.txt --output json
multica memory create --title "Project convention" --content-file ./memory.txt --project-id <uuid> --output json
```

The content file is UTF-8 and lives in the current working directory. `--content`
and `--content-stdin` are alternatives. Title and content are limited to 160 and
4,000 Unicode characters. Never store credentials, raw transcripts or transient
task status as memory. A save is complete only after the API returns a record.

## Correct and forget

Read the current record first and use its revision; on a conflict, read again
and reconcile rather than overwriting another agent's correction.

```sh
multica memory update <id> --expected-revision <revision> --content-file ./memory.txt --output json
multica memory delete <id> --expected-revision <revision> --yes --output json
```

Project scope and original source cannot be changed by editing a memory. Delete
and recreate deliberately when a scope change is requested. Forgetting changes
future memory snapshots; it does not erase historical conversations.

## Automatic extraction

Workspace owners and admins control automatic extraction:

```sh
multica memory settings get --output json
multica memory settings update --automatic-extraction=true --output json
multica memory settings update --automatic-extraction=false --output json
```

Only when the current public issue task's brief enables automatic extraction,
identify verified reusable facts or lessons before finishing the task. Search
first, keep each entry concise, and create useful new entries with
`--source automatic`; use the same revision-aware update flow for corrections.
There is no obligation to invent a memory on every task. Preserve uncertainty
instead of turning an inference into a fact. Private chat, autopilot and
quick-create contexts do not automatically publish memories. The server checks
the setting and active task credential. Do not relabel an automatic save as
manual to evade these checks.

Automatic extraction is performed by the task's agent using this API. It does
not run a second model or scan all historical conversations.
