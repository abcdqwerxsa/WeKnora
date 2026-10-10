package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory SQLite database with tenant table.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.TenantMember{}))
	return db
}

func TestDeleteTenant_SoftDeletesMemberships(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepository(db)

	tenant := &types.Tenant{Name: "gone", Status: "active"}
	require.NoError(t, db.Create(tenant).Error)

	member := &types.TenantMember{
		UserID:   "user-1",
		TenantID: tenant.ID,
		Role:     types.TenantRoleOwner,
		Status:   types.TenantMemberStatusActive,
	}
	require.NoError(t, db.Create(member).Error)

	require.NoError(t, repo.DeleteTenant(ctx, tenant.ID))

	var tenantCount int64
	require.NoError(t, db.Model(&types.Tenant{}).Count(&tenantCount).Error)
	assert.Equal(t, int64(0), tenantCount)

	var memberCount int64
	require.NoError(t, db.Model(&types.TenantMember{}).Count(&memberCount).Error)
	assert.Equal(t, int64(0), memberCount)

	// Unscoped: rows still exist but are soft-deleted.
	var rawTenantCount int64
	require.NoError(t, db.Unscoped().Model(&types.Tenant{}).Count(&rawTenantCount).Error)
	assert.Equal(t, int64(1), rawTenantCount)

	var rawMemberCount int64
	require.NoError(t, db.Unscoped().Model(&types.TenantMember{}).Count(&rawMemberCount).Error)
	assert.Equal(t, int64(1), rawMemberCount)
}

// TestCountTenants covers the bootstrap-admin trigger in UserService.Register:
// the registrant becomes the platform admin iff this count returns 0.
//
// Soft-delete semantics: rows with a non-null DeletedAt are excluded
// (this is GORM's default and matches ListTenants elsewhere in this
// package). Practical consequence: if an operator deletes every tenant
// via the management UI, the next registrant becomes the platform admin
// — which is the right behaviour for a "reset to factory" workflow.
// `docker compose down -v` would wipe the volume outright, which is a
// different scenario.
func TestCountTenants(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepository(db)

	// Empty DB → 0
	got, err := repo.CountTenants(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), got, "fresh DB should report 0 tenants")

	// One active tenant → 1
	require.NoError(t, db.Create(&types.Tenant{Name: "alpha", Status: "active"}).Error)
	got, err = repo.CountTenants(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got)

	// Add a joinable one + soft-delete the original.
	// Soft-deleted rows are excluded by GORM's default scope, so the
	// count drops to 1 (just the active beta tenant).
	require.NoError(t, db.Create(&types.Tenant{Name: "beta", Status: "active", IsJoinable: true}).Error)
	require.NoError(t, db.Delete(&types.Tenant{}, "name = ?", "alpha").Error)

	got, err = repo.CountTenants(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got, "soft-deleted rows must NOT be counted")

	// Unscoped count (for sanity) should still see the deleted row.
	var unscoped int64
	require.NoError(t, db.Unscoped().Model(&types.Tenant{}).Count(&unscoped).Error)
	assert.Equal(t, int64(2), unscoped, "unscoped should still see soft-deleted rows")

	// Hard-delete the soft-deleted one (cleanup).
	require.NoError(t, db.Unscoped().Where("name = ?", "alpha").Delete(&types.Tenant{}).Error)
}
