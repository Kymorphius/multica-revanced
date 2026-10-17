CREATE INDEX CONCURRENTLY IF NOT EXISTS memory_workspace_context_idx ON memory (workspace_id, updated_at DESC, id);
