-- name: ListMemories :many
SELECT * FROM memory
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.arg(search)::text = '' OR strpos(lower(title), lower(sqlc.arg(search)::text)) > 0 OR strpos(lower(content), lower(sqlc.arg(search)::text)) > 0)
ORDER BY updated_at DESC, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListMemoriesForContext :many
SELECT * FROM memory
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (project_id IS NULL OR project_id = sqlc.narg(project_id)::uuid)
ORDER BY updated_at DESC, id LIMIT 51;

-- name: GetMemoryInWorkspace :one
SELECT * FROM memory WHERE id = $1 AND workspace_id = $2;

-- name: CreateMemory :one
INSERT INTO memory (workspace_id, project_id, title, content, source, created_by_type, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateMemory :one
UPDATE memory SET title = COALESCE(sqlc.narg(title), title), content = COALESCE(sqlc.narg(content), content), revision = revision + 1, updated_at = now()
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id) AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: DeleteMemory :execrows
DELETE FROM memory WHERE id = $1 AND workspace_id = $2 AND revision = $3;

-- name: DeleteMemoriesByProject :exec
DELETE FROM memory WHERE workspace_id = $1 AND project_id = $2;

-- name: GetMemoryAutomaticExtraction :one
SELECT COALESCE((SELECT automatic_extraction FROM workspace_memory_settings WHERE workspace_id = $1), false)::boolean AS automatic_extraction;

-- name: SetMemoryAutomaticExtraction :exec
INSERT INTO workspace_memory_settings (workspace_id, automatic_extraction)
VALUES ($1, $2) ON CONFLICT (workspace_id) DO UPDATE
SET automatic_extraction = EXCLUDED.automatic_extraction, updated_at = now();

-- name: LockWorkspaceForMemoryWrite :one
-- Memory rows and settings have no workspace FK. Serialize publishers with
-- DeleteWorkspace's FOR UPDATE before creating rows or locking a project.
SELECT id FROM workspace WHERE id = $1 FOR KEY SHARE;
