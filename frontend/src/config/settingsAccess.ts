export type SettingsRoleKey = 'viewer' | 'contributor' | 'admin' | 'owner'

/**
 * Workspace-scoped settings access policy.
 *
 * Keep this as the single frontend source of truth for both the complete
 * Settings navigation and any shortcuts that lead into it. Backend route
 * guards remain authoritative.
 */
export const SETTINGS_SECTION_MIN_ROLE: Record<string, SettingsRoleKey> = {
  general: 'viewer',
  ollama: 'admin',
  models: 'admin',
  websearch: 'admin',
  chathistory: 'admin',
  vectorstore: 'admin',
  parser: 'admin',
  storage: 'admin',
  sandbox: 'admin',
  // Install writes a root shell into the sandbox image every session of
  // that config boots. Same Admin+ bar as the sandbox editor itself.
  skills: 'admin',
  mcp: 'admin',
  system: 'viewer',
  userprofile: 'viewer',
  tenant: 'viewer',
  members: 'viewer',
  mymemory: 'viewer',
  memory: 'admin',
  // Every member fills in their own environment variables; the workspace-wide
  // values stay on the Admin+ skills page.
  envvars: 'viewer',
  // Pending-approval queue: opened to Admin+ so workspace admins can
  // approve/reject self-registered users without escalating to a
  // SystemAdmin. Backend still gates /system/admin/users/:id/{approve,reject}
  // by g.Admin() (see internal/router/routes_auth_tenant.go). Promote /
  // revoke / list system admins / audit-log / runtime / settings all
  // remain SystemAdmin-only via SYSTEM_ADMIN_SETTINGS_SECTIONS below.
  'pending-users': 'admin',
}

/**
 * A management-labelled avatar shortcut has a stricter threshold than the
 * corresponding read-only Settings page.
 */
export const SETTINGS_MANAGEMENT_SHORTCUT_MIN_ROLE = {
  members: 'owner',
  models: 'admin',
  skills: 'admin',
} as const satisfies Record<string, SettingsRoleKey>

export const SYSTEM_ADMIN_SETTINGS_SECTIONS = new Set([
  'system-global',
  'runtime-queues',
  'platform-api-keys',
  'system-audit-log',
  // 'pending-users' intentionally NOT in this set: workspace admins can
  // approve/reject pending registrations (see SETTINGS_SECTION_MIN_ROLE
  // entry above). SystemAdmin-only routes are promoted/revoke/list/audit
  // and other platform surfaces.
])
