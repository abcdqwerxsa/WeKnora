-- Migration: 000095_user_approval (down)

ALTER TABLE users DROP COLUMN IF EXISTS is_approved;
ALTER TABLE tenants DROP COLUMN IF EXISTS is_joinable;
