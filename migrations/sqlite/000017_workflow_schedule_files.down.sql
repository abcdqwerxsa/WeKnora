-- Migration: 000017_workflow_schedule_files (sqlite, down)
-- SQLite does not support DROP COLUMN before 3.35; recreate is overkill for a dev dialect.
SELECT 1;
