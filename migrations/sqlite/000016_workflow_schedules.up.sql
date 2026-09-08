-- Workflow cron schedules (mirrors 000094_versioned). Row ids are
-- generated in Go, so there is no server-side default here.

CREATE TABLE IF NOT EXISTS workflow_schedules (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    workflow_id VARCHAR(36) NOT NULL,
    creator_id VARCHAR(36) NOT NULL DEFAULT '',
    cron VARCHAR(64) NOT NULL,
    query TEXT,
    inputs TEXT,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_tenant_workflow
    ON workflow_schedules (tenant_id, workflow_id);
