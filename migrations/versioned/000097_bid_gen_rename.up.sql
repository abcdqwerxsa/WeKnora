-- Migration: 000097_bid_gen_rename
-- Description: Rename stale `office-doc-gen` rows left over from before the
-- preloaded skill was repurposed to `bid_gen`. The repo dir
-- `skills/preloaded/office-doc-gen/` no longer exists (replaced by
-- `skills/preloaded/bid_gen/` with a pandoc JSON AST round-trip pipeline),
-- but old DB rows in tenant_skill_catalog and tenant_skills still carry
-- the old `name`. The frontend's SkillSettings page reads `name` directly
-- from these tables, so without this migration operators would see a
-- broken `office-doc-gen` entry in the catalog until they manually
-- re-uploaded. This is a no-op once the rename has propagated.
--
-- Safe under the partial-unique indexes (uq_tenant_skill_catalog_name,
-- uq_tenant_skills_config_name) because the UPDATE is scoped to rows that
-- already match the old name and the new name must be free in the same
-- scope; we collapse the rare "both old and new exist" case by skipping the
-- update if a non-deleted `bid_gen` row is already present in the same
-- scope.

DO $$ BEGIN RAISE NOTICE '[Migration 000097] Renaming office-doc-gen to bid_gen in tenant_skill_catalog'; END $$;

UPDATE tenant_skill_catalog AS old
SET name = 'bid_gen',
    updated_at = NOW()
WHERE old.name = 'office-doc-gen'
  AND old.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM tenant_skill_catalog AS fresh
    WHERE fresh.tenant_id = old.tenant_id
      AND fresh.name = 'bid_gen'
      AND fresh.deleted_at IS NULL
  );

DO $$ BEGIN RAISE NOTICE '[Migration 000097] Renaming office-doc-gen to bid_gen in tenant_skills'; END $$;

UPDATE tenant_skills AS old
SET name = 'bid_gen',
    updated_at = NOW()
WHERE old.name = 'office-doc-gen'
  AND old.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM tenant_skills AS fresh
    WHERE fresh.sandbox_config_id = old.sandbox_config_id
      AND fresh.name = 'bid_gen'
      AND fresh.deleted_at IS NULL
  );

DO $$ BEGIN RAISE NOTICE '[Migration 000097] Done.'; END $$