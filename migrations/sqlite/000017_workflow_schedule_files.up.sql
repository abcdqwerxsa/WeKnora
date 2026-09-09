-- Migration: 000017_workflow_schedule_files (sqlite)
ALTER TABLE workflow_schedules ADD COLUMN files TEXT;
