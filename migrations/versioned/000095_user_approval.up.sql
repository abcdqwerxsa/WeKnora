-- Migration: 000095_user_approval
-- Description: Self-registration now requires admin approval before the new
-- account can log in, and a tenant can opt into being selectable as the
-- "department" on the public registration form. Workflow:
--
--   1. Self-registration creates the user with is_approved = FALSE.
--   2. If the form also names a tenant_id, we verify tenants.is_joinable,
--      then write a tenant_members row (status='invited', role='contributor')
--      so the user's home tenant_id points at the chosen department and
--      the row flips to 'active' when an admin approves.
--   3. The login handler rejects users with is_approved = FALSE.
--   4. SystemAdmin endpoints approve / reject these rows.
--
-- Existing rows are migrated to is_approved = TRUE and is_joinable = FALSE
-- so the upgrade is non-breaking for accounts / tenants that pre-date the
-- feature.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_approved BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS is_joinable BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill: every user that exists at upgrade time is treated as already
-- approved (they got in via the legacy create_personal path). New
-- registrations after this migration default to FALSE so the gate fires.
UPDATE users SET is_approved = TRUE WHERE is_approved = FALSE;

-- Index the new approval flag. The admin queue filters on this column
-- exclusively, and partial indexes on boolean columns give Postgres a
-- tight scan even when the users table grows large.
CREATE INDEX IF NOT EXISTS idx_users_is_approved ON users(is_approved) WHERE is_approved = FALSE;
CREATE INDEX IF NOT EXISTS idx_tenants_is_joinable ON tenants(is_joinable) WHERE is_joinable = TRUE;
