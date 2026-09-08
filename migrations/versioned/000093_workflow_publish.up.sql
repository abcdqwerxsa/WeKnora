-- Migration: 000093_workflow_publish
-- Description: Published-workflow snapshot (Phase 4 of the workflow
-- roadmap, Dify draft/published model). Publishing freezes the current DSL
-- into published_dsl; runs of a published workflow execute the snapshot,
-- while the draft DSL keeps evolving.

ALTER TABLE workflows ADD COLUMN IF NOT EXISTS published_dsl JSONB;

COMMENT ON COLUMN workflows.published_dsl IS 'Frozen DSL snapshot of the last publish; runs of status=published workflows execute this copy (Dify draft/published model). NULL = never published.';
