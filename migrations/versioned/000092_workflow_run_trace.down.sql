-- Migration: 000092_workflow_run_trace (down)
ALTER TABLE workflow_runs DROP COLUMN IF EXISTS trace;
