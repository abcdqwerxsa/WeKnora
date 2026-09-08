-- Per-node execution trace on workflow runs (mirrors 000092_versioned).
ALTER TABLE workflow_runs ADD COLUMN trace TEXT;
