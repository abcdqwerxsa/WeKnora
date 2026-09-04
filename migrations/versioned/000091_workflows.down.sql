DO $$ BEGIN RAISE NOTICE '[Migration 000091 down] Dropping workflow tables'; END $$;

DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflows;
