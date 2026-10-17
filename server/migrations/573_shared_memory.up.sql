CREATE TABLE memory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID,
    title TEXT NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 160),
    content TEXT NOT NULL CHECK (char_length(btrim(content)) BETWEEN 1 AND 4000),
    source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'automatic')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE workspace_memory_settings (
    workspace_id UUID PRIMARY KEY,
    automatic_extraction BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
