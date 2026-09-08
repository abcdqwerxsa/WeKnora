-- Migration: 000094_workflow_schedules
-- Description: Cron-scheduled runs of published workflows. Each row is one
-- schedule: a 5-field cron expression plus the run input (query + Start-form
-- inputs). The workflow scheduler fires enabled schedules whose workflow is
-- still published; runs land in the regular workflow_runs history.

CREATE TABLE IF NOT EXISTS workflow_schedules (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    workflow_id VARCHAR(36) NOT NULL,
    creator_id VARCHAR(36) NOT NULL DEFAULT '',
    cron VARCHAR(64) NOT NULL,
    query TEXT,
    inputs JSONB,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

COMMENT ON TABLE workflow_schedules IS 'Cron schedules for published workflows; ticks create regular workflow_runs rows.';
COMMENT ON COLUMN workflow_schedules.cron IS 'Standard 5-field cron expression (min hour dom month dow).';
COMMENT ON COLUMN workflow_schedules.enabled IS 'Disabled schedules stay listed but never fire.';

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_tenant_workflow ON workflow_schedules(tenant_id, workflow_id);
