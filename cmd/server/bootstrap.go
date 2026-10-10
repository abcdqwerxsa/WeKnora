// Bootstrap-time hooks that run after the DI container is built but
// before the HTTP server starts listening. These are deliberately
// best-effort: any failure here only warns and does NOT abort startup.
// The reasoning is that an operator running with a misconfigured env
// var should still be able to bring the server up (and fix the issue
// from the running instance) rather than have a typo brick the deploy.
package main

import (
	"context"
	"os"
	"strings"

	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// bootstrapEnvVar is the env var that names the email of the user who
// may be promoted to system administrator when the deployment has no
// existing system administrators.
//
// Why an env var (vs a CLI subcommand)?
//   - Zero-friction in docker-compose / k8s deploys: set it once in the
//     manifest and the very first user account that signs up with that
//     email is auto-promoted, with no extra ops step.
//   - Idempotent: if the user is already a system admin, bootstrapping is
//     a no-op.
//   - Safe to leave set: once at least one system admin exists, the env
//     var stops granting privileges. That prevents a UI revoke from being
//     silently undone on the next restart.
const bootstrapEnvVar = "LUOSA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL"

// runStartupBootstrap consults the env and applies any one-shot
// bootstrap actions. Currently it only handles system-admin promotion;
// future bootstrap steps (default model seeding, etc.) can be added
// here as additional dig.Invoke calls.
func runStartupBootstrap(c *dig.Container) {
	ctx := context.Background()

	// Legacy hash repair for migration 000065 placeholder rows. Invoked each
	// startup but short-circuits with a cheap EXISTS once every row is
	// backfilled (no api_key decryption on the steady-state path).
	if err := c.Invoke(func(apiKeySvc interfaces.TenantAPIKeyService) {
		if n, err := apiKeySvc.BackfillMissingKeyHashes(ctx); err != nil {
			logger.Warnf(ctx, "[bootstrap] tenant api key hash backfill failed: %v", err)
		} else if n > 0 {
			logger.Infof(ctx, "[bootstrap] backfilled %d legacy tenant api key hash(es)", n)
		}
	}); err != nil {
		logger.Warnf(ctx, "[bootstrap] failed to resolve TenantAPIKeyService: %v", err)
	}

	// Default tenant seed: on a fresh deployment, /auth/available-
	// departments returns an empty list, which leaves the registration
	// form's department dropdown with no selectable workspace. Seed a
	// "Default Workspace" (is_joinable=true) so the first user can pick
	// it and land in a real workspace instead of the create_personal
	// fallback path. Idempotent: skipped on subsequent restarts.
	//
	// DISABLED 2026-09-30: UserService.Register now decides the very
	// first user's home workspace itself — see the CountTenants +
	// CreatePersonal branch there. Seeding a tenant here BEFORE the
	// first registration makes CountTenants() return 1, which breaks
	// the bootstrap-admin rule (the registrant is no longer "first").
	// Keep the seedDefaultTenant function below in case a future
	// deployment needs to recover a deleted default workspace, but do
	// NOT call it from startup.
	if false {
		if err := c.Invoke(func(tenantSvc interfaces.TenantService) {
			seedDefaultTenant(ctx, tenantSvc)
		}); err != nil {
			logger.Warnf(ctx, "[bootstrap] failed to resolve TenantService: %v", err)
		}
	}

	email := strings.TrimSpace(os.Getenv(bootstrapEnvVar))
	if email == "" {
		return
	}
	// dig.Invoke resolves UserService from the container; if user
	// service registration is broken we want to know loudly, but still
	// not abort startup — bootstrap is best-effort.
	if err := c.Invoke(func(userSvc interfaces.UserService) {
		bootstrapSystemAdmin(ctx, userSvc, email)
	}); err != nil {
		logger.Warnf(ctx, "[bootstrap] failed to resolve UserService: %v", err)
	}
}

// bootstrapSystemAdmin promotes the user identified by `email` to system
// administrator only when the deployment currently has no system admins.
// The function is idempotent and non-fatal — it warns and returns on
// every error path.
//
// The bootstrap intentionally does NOT create a user when the email is
// not yet registered: account creation is a workflow with side effects
// (password hashing, tenant assignment, audit) that we don't want to
// short-circuit. Operators should sign up normally first, then set the
// env var on the next restart.
func bootstrapSystemAdmin(ctx context.Context, userSvc interfaces.UserService, email string) {
	user, err := userSvc.GetUserByEmail(ctx, email)
	if err != nil {
		// "not found" surfaces as an error in this codebase; treat it
		// gently — operators commonly set the var before the user has
		// signed up. The next restart after registration will succeed.
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: user lookup failed (have they signed up yet?): %v",
			bootstrapEnvVar, email, err)
		return
	}
	if user == nil {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: no matching user (will retry on next restart)",
			bootstrapEnvVar, email)
		return
	}
	if user.IsSystemAdmin {
		logger.Infof(ctx,
			"[bootstrap] %s=%s: user %s is already a system admin (no-op)",
			bootstrapEnvVar, email, user.ID)
		return
	}
	_, total, err := userSvc.ListSystemAdmins(ctx, 0, 1)
	if err != nil {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: cannot verify existing system admins, skipping promotion: %v",
			bootstrapEnvVar, email, err)
		return
	}
	if total > 0 {
		logger.Infof(ctx,
			"[bootstrap] %s=%s: %d system admin(s) already exist; not promoting user %s",
			bootstrapEnvVar, email, total, user.ID)
		return
	}
	user.IsSystemAdmin = true
	if err := userSvc.UpdateUser(ctx, user); err != nil {
		logger.Warnf(ctx,
			"[bootstrap] %s=%s: failed to promote user %s: %v",
			bootstrapEnvVar, email, user.ID, err)
		return
	}
	logger.Infof(ctx,
		"[bootstrap] promoted user %s (%s) to system admin via %s",
		user.ID, email, bootstrapEnvVar)
}

// seedDefaultTenant creates a "Default Workspace" tenant on a fresh
// deployment so /auth/available-departments (which the registration
// form's department dropdown calls) returns at least one option. With
// at least one joinable tenant in place, the registration flow stays
// on the join_existing branch and the user lands in a real workspace
// instead of the create_personal fallback path.
//
// Idempotent: if any tenant already exists, this is a no-op so it can
// run on every startup without polluting a populated system.
func seedDefaultTenant(ctx context.Context, tenantSvc interfaces.TenantService) {
	tenants, err := tenantSvc.ListAllTenants(ctx)
	if err != nil {
		logger.Warnf(ctx, "[bootstrap] failed to list tenants: %v", err)
		return
	}
	if len(tenants) > 0 {
		logger.Infof(ctx,
			"[bootstrap] %d tenant(s) already exist, skipping default tenant seed",
			len(tenants))
		return
	}
	defaultTenant := &types.Tenant{
		Name:        "Default Workspace",
		Description: "系统首次启动自动创建的默认空间。新注册用户可加入此处。",
		IsJoinable:  true,
	}
	if _, err := tenantSvc.CreateTenant(ctx, defaultTenant); err != nil {
		logger.Warnf(ctx, "[bootstrap] failed to create default tenant: %v", err)
		return
	}
	logger.Infof(ctx,
		"[bootstrap] created default tenant 'Default Workspace' (is_joinable=true)")
}
