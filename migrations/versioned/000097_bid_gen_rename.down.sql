-- Migration: 000097_bid_gen_rename (down)
-- Description: Reverse the rename — restore `office-doc-gen` rows. This is
-- only meaningful if the operator downgrades the app before re-introducing
-- the old skill bundle into the sandbox image. We do not attempt to
-- distinguish rows that came from this migration vs. rows that were
-- originally `bid_gen`, so a downgrade leaves any pre-existing
-- `bid_gen` rows untouched (the migration is a one-way data fix in
-- practice; the down file is here for completeness).

DO $$ BEGIN RAISE NOTICE '[Migration 000097 down] Renaming bid_gen back to office-doc-gen in tenant_skill_catalog'; END $$;

UPDATE tenant_skill_catalog
SET name = 'office-doc-gen',
    updated_at = NOW()
WHERE name = 'bid_gen'
  AND deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000097 down] Renaming bid_gen back to office-doc-gen in tenant_skills'; END $$;

UPDATE tenant_skills
SET name = 'office-doc-gen',
    updated_at = NOW()
WHERE name = 'bid_gen'
  AND deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000097 down] Done.'; END $$