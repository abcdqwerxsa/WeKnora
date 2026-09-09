-- Migration: 000096_workflow_schedule_files (down)

ALTER TABLE workflow_schedules DROP COLUMN IF EXISTS files;
