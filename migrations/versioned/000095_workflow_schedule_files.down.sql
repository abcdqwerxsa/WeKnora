-- Migration: 000095_workflow_schedule_files (down)

ALTER TABLE workflow_schedules DROP COLUMN IF EXISTS files;
