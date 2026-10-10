-- Migration: 000018_workflow_schedule_files (sqlite)
ALTER TABLE workflow_schedules ADD COLUMN files TEXT;
