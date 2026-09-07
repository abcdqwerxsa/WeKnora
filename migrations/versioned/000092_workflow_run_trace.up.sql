-- Migration: 000092_workflow_run_trace
-- Description: Per-node execution trace on workflow runs (Phase 1 of the
-- workflow roadmap: run debugging / detail panel). Terminal node records
-- (finished|failed) in execution order; the list endpoint omits the column,
-- the run-detail endpoint returns it.

ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS trace JSONB;

COMMENT ON COLUMN workflow_runs.trace IS 'Per-node execution trace (JSON array of WorkflowRunTraceEntry, execution order) behind the run-detail/debug panel.';
