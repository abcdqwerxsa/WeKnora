-- Migration: 000095_workflow_schedule_files
-- Description: Run attachments for scheduled runs. `files` carries the
-- temporary-document ids a schedule's runs resolve into LLM context, the
-- same way RunWorkflowRequest.files does for manual runs.

ALTER TABLE workflow_schedules ADD COLUMN IF NOT EXISTS files JSONB;

COMMENT ON COLUMN workflow_schedules.files IS 'Run attachment ids (temporary documents) resolved into LLM context on each tick.';
