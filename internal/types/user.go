package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// UserPreferences holds per-user preferences persisted server-side
// so they sync across devices/browsers. Fields are pointers so we can
// distinguish "client didn't send this key" (leave existing value alone)
// from "client explicitly set false" — the partial-update merge in
// UpdateUserPreferences relies on this.
//
// Adding a new preference key:
//  1. Add a *T field below + JSON tag (snake_case, must match the front-end key).
//  2. Extend the merge logic in service.UserService.UpdateUserPreferences.
//  3. Surface the new knob in the frontend settings store.
//
// No DB DDL is required — preferences is a single jsonb column.
type UserPreferences struct {
	// LastActiveTenantID remembers the last workspace the user actively
	// switched into, so a fresh login (new device, cleared browser, new
	// refresh token) lands them back in that workspace instead of always
	// bouncing to their home workspace. Written by the SPA's preferences
	// PUT and by service-level SwitchTenant (including when switching
	// home, which stores the home ID). Login / RefreshToken validate that
	// the workspace still exists and the user still has an active membership
	// (or CanAccessAllTenants) before honouring this preference; an
	// invalid pointer is best-effort cleared and the user falls back to
	// home. Refresh JWT claims have no tenant_id, so RefreshToken
	// re-resolves from this field.
	//
	// nil  = no preference (use user.TenantID, i.e. home)
	// *0   = "clear preference" sentinel for the partial-update endpoint
	//        (UpdateUserPreferences turns this into nil). Otherwise treat
	//        a stored *0 the same as nil.
	// *N   = preferred workspace id.
	LastActiveTenantID *uint64 `json:"last_active_tenant_id,omitempty"`

	// OidcOnlyLogin is set server-side when an account is auto-provisioned
	// via OIDC with a random password the user never received. The profile
	// UI hides self-service password rotation until the user sets a known
	// password via ChangePassword (which clears this flag).
	OidcOnlyLogin *bool `json:"oidc_only_login,omitempty"`
}

// Value implements driver.Valuer so GORM persists UserPreferences as
// JSON text (Postgres jsonb column / SQLite TEXT). Empty struct serialises
// to "{}", matching the NOT NULL DEFAULT '{}' column constraint.
func (p UserPreferences) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Scan implements sql.Scanner so GORM can hydrate UserPreferences back
// from the underlying column. Accept []byte (Postgres jsonb / SQLite blob)
// and string (some drivers hand TEXT as string) for portability.
func (p *UserPreferences) Scan(value interface{}) error {
	if value == nil {
		*p = UserPreferences{}
		return nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return errors.New("UserPreferences.Scan: unsupported type")
	}
	if len(data) == 0 {
		*p = UserPreferences{}
		return nil
	}
	return json.Unmarshal(data, p)
}

// User represents a user in the system
type User struct {
	// Unique identifier of the user
	ID string `json:"id"         gorm:"type:varchar(36);primaryKey"`
	// Username of the user
	Username string `json:"username"   gorm:"type:varchar(100);uniqueIndex;not null"`
	// Email address of the user
	Email string `json:"email"      gorm:"type:varchar(255);uniqueIndex;not null"`
	// Hashed password of the user
	PasswordHash string `json:"-"          gorm:"type:varchar(255);not null"`
	// Avatar URL of the user
	Avatar string `json:"avatar"     gorm:"type:varchar(500)"`
	// Workspace ID that the user belongs to
	TenantID uint64 `json:"tenant_id"  gorm:"index"`
	// Whether the user is active
	IsActive bool `json:"is_active"  gorm:"default:true"`
	// Whether the user can access all workspaces (cross-workspace access)
	CanAccessAllTenants bool `json:"can_access_all_tenants" gorm:"default:false"`
	// IsApproved gates the public sign-up flow: new self-registered users
	// start at IsApproved=false and a SystemAdmin flips it to true after
	// reviewing the row. Login is rejected for IsApproved=false users
	// (see AuthService.Login). Bypassed entirely for OIDC/provisioned
	// accounts, which set IsApproved=true at creation time.
	IsApproved bool `json:"is_approved" gorm:"default:false;index"`
	// Whether the user is a system administrator (independent of workspace roles)
	IsSystemAdmin bool `json:"is_system_admin" gorm:"default:false;index"`
	// Per-user UI/feature preferences.
	// Stored as JSON (jsonb on Postgres, TEXT on SQLite) via the
	// driver.Valuer / sql.Scanner methods on UserPreferences.
	Preferences UserPreferences `json:"preferences" gorm:"type:jsonb;not null;default:'{}'"`
	// Creation time of the user
	CreatedAt time.Time `json:"created_at"`
	// Last updated time of the user
	UpdatedAt time.Time `json:"updated_at"`
	// Deletion time of the user
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// Association relationship, not stored in the database
	Tenant *Tenant `json:"tenant,omitempty" gorm:"foreignKey:TenantID"`
}

// AuthToken represents an authentication token
type AuthToken struct {
	// Unique identifier of the token
	ID string `json:"id"         gorm:"type:varchar(36);primaryKey"`
	// User ID that owns this token
	UserID string `json:"user_id"    gorm:"type:varchar(36);index;not null"`
	// Token value (JWT or other format)
	Token string `json:"token"      gorm:"type:text;not null"`
	// Token type (access_token, refresh_token)
	TokenType string `json:"token_type" gorm:"type:varchar(50);not null"`
	// Token expiration time
	ExpiresAt time.Time `json:"expires_at"`
	// Whether the token is revoked
	IsRevoked bool `json:"is_revoked" gorm:"default:false"`
	// Creation time of the token
	CreatedAt time.Time `json:"created_at"`
	// Last updated time of the token
	UpdatedAt time.Time `json:"updated_at"`

	// Association relationship
	User *User `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// LoginRequest represents a login request
type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type OIDCAuthURLResponse struct {
	Success             bool   `json:"success"`
	ProviderDisplayName string `json:"provider_display_name,omitempty"`
	AuthorizationURL    string `json:"authorization_url,omitempty"`
	State               string `json:"state,omitempty"`
	// Nonce is bound to an HttpOnly cookie on /auth/oidc/url and verified
	// on callback; omitted from JSON so clients cannot replay it alone.
	Nonce string `json:"-"`
}

type OIDCConfigResponse struct {
	Success             bool   `json:"success"`
	Enabled             bool   `json:"enabled"`
	ProviderDisplayName string `json:"provider_display_name,omitempty"`
}

type OIDCCallbackResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	User    *User  `json:"user,omitempty"`
	// Tenant carries the active tenant for the issued token. The field
	// name is preserved for backward compatibility with existing frontend
	// OIDC callback handling; LoginResponse uses ActiveTenant for the
	// same data.
	Tenant *Tenant `json:"tenant,omitempty"`
	// Memberships mirrors LoginResponse.Memberships so the OIDC flow
	// produces the same role information available to password logins.
	// Always populated (length >= 1 for an authenticated user).
	Memberships  []Membership `json:"memberships"`
	Token        string       `json:"token,omitempty"`
	RefreshToken string       `json:"refresh_token,omitempty"`
	IsNewUser    bool         `json:"is_new_user,omitempty"`
}

type OIDCUserInfo struct {
	Subject  string                 `json:"subject,omitempty"`
	Username string                 `json:"username,omitempty"`
	Email    string                 `json:"email,omitempty"`
	Claims   map[string]interface{} `json:"claims,omitempty"`
}

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=2,max=50"`
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`

	// TenantID, when set, makes registration route the user into the named
	// existing tenant instead of auto-creating a personal workspace. The
	// service validates that the tenant exists and is flagged is_joinable;
	// otherwise the field is silently ignored and the request falls back to
	// create_personal behaviour. TenantProvisioning below decides whether
	// to honour TenantID (join_existing) or auto-create (create_personal).
	TenantID uint64 `json:"tenant_id,omitempty"`

	// TenantProvisioning is server-controlled registration context. It is
	// deliberately excluded from JSON so a public caller cannot choose its
	// own tenancy semantics. Empty preserves the historical behaviour and is
	// treated as create_personal by UserService.Register.
	TenantProvisioning TenantProvisioningMode `json:"-"`
}

// registerRequestJSON is the wire-shape mirror of RegisterRequest. We use it
// to implement a tolerant UnmarshalJSON so the public register endpoint
// does not 400 on common frontend quirks:
//
//   - "tenant_id": "" or null  → 0 (caller signals "no department" — let
//                                 the server pick create_personal)
//   - "tenant_id": "5"          → 5 (some libs serialise IDs as strings
//                                 because bigint+precision fear)
//
// Without this, TDesign's <t-form :data> + <t-select v-model> combination
// can ship an empty string for an unfilled number field and the default
// Go decoder rejects it with "cannot unmarshal string into uint64" — a
// 400 that blocks first-user signups entirely (see issue: first-user
// registration card deadlock).
type registerRequestJSON struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// json.RawMessage so we can defer the number/string decision to the
	// body of UnmarshalJSON below.
	TenantID json.RawMessage `json:"tenant_id,omitempty"`
}

// UnmarshalJSON implements tolerant decoding for RegisterRequest.
func (r *RegisterRequest) UnmarshalJSON(data []byte) error {
	var raw registerRequestJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Username = raw.Username
	r.Email = raw.Email
	r.Password = raw.Password
	if len(raw.TenantID) == 0 || string(raw.TenantID) == "null" {
		// Missing or explicit null → leave TenantID at zero value.
		return nil
	}
	// Try the canonical numeric decode first; fall back to string parsing
	// if the client serialised the id as a JSON string.
	var n uint64
	if err := json.Unmarshal(raw.TenantID, &n); err == nil {
		r.TenantID = n
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.TenantID, &s); err != nil {
		return err
	}
	if s == "" {
		// Frontend sent "" (TDesign coercion of undefined → ''). Treat as
		// "no department" rather than failing the whole request.
		return nil
	}
	parsed, perr := strconv.ParseUint(s, 10, 64)
	if perr != nil {
		return perr
	}
	r.TenantID = parsed
	return nil
}

// AdminCreateUserRequest is the payload for a SystemAdmin provisioning a
// new local user via POST /api/v1/system/admin/users/create.
//
// Password is optional: when absent (or null), the service generates a
// random one and returns it exactly once. Any provided value, the
// empty string included, is subject to the password policy.
type AdminCreateUserRequest struct {
	Username string  `json:"username" binding:"required,min=2,max=50"`
	Email    string  `json:"email"    binding:"required,email"`
	Password *string `json:"password"`
}

// TenantProvisioningMode controls what UserService.Register does after it
// has validated the identity fields. Joining an existing tenant is
// orchestrated by the invitation handler because the invitation token is the
// authority for the target tenant and role.
type TenantProvisioningMode string

const (
	TenantProvisioningCreatePersonal TenantProvisioningMode = "create_personal"
	TenantProvisioningTenantless     TenantProvisioningMode = "tenantless"
	TenantProvisioningJoinExisting   TenantProvisioningMode = "join_existing"
)

func (m TenantProvisioningMode) IsValid() bool {
	return m == TenantProvisioningCreatePersonal ||
		m == TenantProvisioningTenantless ||
		m == TenantProvisioningJoinExisting
}

// LoginResponse represents a login response
type LoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	User    *User  `json:"user,omitempty"`
	// ActiveTenant is the workspace whose ID is encoded in the issued JWT;
	// future requests are scoped to it until the client calls /auth/switch-tenant.
	// Defaults to the user's home workspace on a fresh login.
	ActiveTenant *Tenant `json:"active_tenant,omitempty"`
	// Memberships lists every workspace the user can authenticate into,
	// along with their role in each. Always populated (length 1 for users
	// who only belong to their home workspace) so frontends can render a
	// workspace switcher without a follow-up request. Serialised without
	// omitempty so the field is always present as a JSON array (possibly
	// empty) — the "always populated" contract relies on the server side
	// guaranteeing a non-nil slice.
	Memberships  []Membership `json:"memberships"`
	Token        string       `json:"token,omitempty"`
	RefreshToken string       `json:"refresh_token,omitempty"`
}

// RegisterResponse represents a registration response
type RegisterResponse struct {
	Success bool    `json:"success"`
	Message string  `json:"message,omitempty"`
	User    *User   `json:"user,omitempty"`
	Tenant  *Tenant `json:"tenant,omitempty"`
}

// UserInfo represents user information for API responses
type UserInfo struct {
	ID                  string          `json:"id"`
	Username            string          `json:"username"`
	Email               string          `json:"email"`
	Avatar              string          `json:"avatar"`
	TenantID            uint64          `json:"tenant_id"`
	IsActive            bool            `json:"is_active"`
	CanAccessAllTenants bool            `json:"can_access_all_tenants"`
	IsSystemAdmin       bool            `json:"is_system_admin"`
	Preferences         UserPreferences `json:"preferences"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// ToUserInfo converts User to UserInfo (without sensitive data)
func (u *User) ToUserInfo() *UserInfo {
	return &UserInfo{
		ID:                  u.ID,
		Username:            u.Username,
		Email:               u.Email,
		Avatar:              u.Avatar,
		TenantID:            u.TenantID,
		IsActive:            u.IsActive,
		CanAccessAllTenants: u.CanAccessAllTenants,
		IsSystemAdmin:       u.IsSystemAdmin,
		Preferences:         u.Preferences,
		CreatedAt:           u.CreatedAt,
		UpdatedAt:           u.UpdatedAt,
	}
}
