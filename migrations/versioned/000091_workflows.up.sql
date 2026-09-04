-- Migration: 000091_workflows
-- Description: Workflow orchestration definitions and run history.
-- Slice 2 of the workflow-orchestration feature: storage only. The DSL is
-- stored as an opaque JSON document (structurally validated by the service
-- layer); execution wiring lands in a follow-up slice, so workflow_runs
-- rows are not created by any endpoint yet.
DO $$ BEGIN RAISE NOTICE '[Migration 000091] Creating workflows'; END $$;

CREATE TABLE IF NOT EXISTS workflows (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    creator_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    description TEXT,
    dsl JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(50) NOT NULL DEFAULT 'draft',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

COMMENT ON COLUMN workflows.dsl IS 'Workflow DSL document (dual view: graph for canvas rendering, components for execution topology). Opaque JSON here; validated in the service layer.';
COMMENT ON COLUMN workflows.status IS 'draft | published | archived';
COMMENT ON COLUMN workflows.version IS 'Bumped by 1 on every successful update; reserved for future publish/rollback semantics.';

CREATE INDEX IF NOT EXISTS idx_workflows_tenant ON workflows(tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000091] Creating workflow_runs'; END $$;

CREATE TABLE IF NOT EXISTS workflow_runs (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    workflow_id VARCHAR(36) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    input JSONB,
    output JSONB,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

COMMENT ON TABLE workflow_runs IS 'Run history. Populated by the execution wiring slice; listed read-only until then.';

CREATE INDEX IF NOT EXISTS idx_workflow_runs_tenant_workflow ON workflow_runs(tenant_id, workflow_id);
