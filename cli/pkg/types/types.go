package types

import (
	"encoding/json"
	"time"
)

// Base fields shared by all API resources.
type Base struct {
	ID        string     `json:"id" yaml:"id"`
	CreatedAt time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
	Version   string     `json:"version" yaml:"version"`
}

// StackInstance represents a deployed stack instance.
//
// OwnerUsername, DefinitionName and ClusterName are display names. Servers
// that do not send them leave them empty; the table output then shows the
// IDs.
//
// ValuesDrift is true when the running values come from a rollback and the
// stored overrides produce other values: the next deploy applies the stored
// overrides again. Only GET /api/v1/stack-instances/:id computes it
// (k8s-stack-manager v0.6.0+). Warning is set by the clone response (a
// quota override that is not copied). "stack get" sets Warning to
// ValuesDriftWarning when ValuesDrift is true and the server sends no text.
type StackInstance struct {
	Base
	Name              string     `json:"name" yaml:"name"`
	StackDefinitionID string     `json:"stack_definition_id" yaml:"stack_definition_id"`
	DefinitionName    string     `json:"definition_name,omitempty" yaml:"definition_name,omitempty"`
	Owner             string     `json:"owner_id" yaml:"owner_id"`
	OwnerUsername     string     `json:"owner_username,omitempty" yaml:"owner_username,omitempty"`
	Branch            string     `json:"branch" yaml:"branch"`
	Namespace         string     `json:"namespace" yaml:"namespace"`
	Status            string     `json:"status" yaml:"status"`
	ClusterID         *string    `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
	ClusterName       string     `json:"cluster_name,omitempty" yaml:"cluster_name,omitempty"`
	TTLMinutes        int        `json:"ttl_minutes,omitempty" yaml:"ttl_minutes,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	DeployedAt        *time.Time `json:"last_deployed_at,omitempty" yaml:"last_deployed_at,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty" yaml:"error_message,omitempty"`
	ValuesDrift       bool       `json:"values_drift,omitempty" yaml:"values_drift,omitempty"`
	Warning           string     `json:"warning,omitempty" yaml:"warning,omitempty"`
}

// ValuesDriftWarning is a copy of the server constant msgValuesDrift
// (k8s-stack-manager backend/internal/api/handlers/instance_rollback.go).
// The swagger file does not contain the text, so no test compares them;
// keep both in sync. "stack get" uses it when the server sends values_drift
// without a warning text.
const ValuesDriftWarning = "The stored overrides differ from the running values. The next deploy applies the stored overrides again."

// StackDefinition represents a stack definition with its chart configurations.
type StackDefinition struct {
	Base
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultBranch string        `json:"default_branch" yaml:"default_branch"`
	Owner         string        `json:"owner_id" yaml:"owner_id"`
	OwnerUsername string        `json:"owner_username,omitempty" yaml:"owner_username,omitempty"`
	Charts        []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// StackTemplate represents a reusable stack template.
//
// The template fields and Charts are the working copy (draft). Base.Version
// is the version string of the working copy. From k8s-stack-manager v0.6.0
// GET /api/v1/templates/:id also returns the latest published release:
// PublishedVersion, PublishedVersionID and PublishedCharts (null when the
// template has no release) and HasUnpublishedChanges. Older servers do not
// send these fields; HasUnpublishedChanges is then nil.
type StackTemplate struct {
	Base
	Name                  string        `json:"name" yaml:"name"`
	Description           string        `json:"description,omitempty" yaml:"description,omitempty"`
	Category              string        `json:"category,omitempty" yaml:"category,omitempty"`
	DefaultBranch         string        `json:"default_branch,omitempty" yaml:"default_branch,omitempty"`
	Published             bool          `json:"is_published" yaml:"is_published"`
	Owner                 string        `json:"owner_id" yaml:"owner_id"`
	OwnerUsername         string        `json:"owner_username,omitempty" yaml:"owner_username,omitempty"`
	Charts                []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
	DefinitionCount       int           `json:"definition_count,omitempty" yaml:"definition_count,omitempty"`
	PublishedVersion      *string       `json:"published_version,omitempty" yaml:"published_version,omitempty"`
	PublishedVersionID    *string       `json:"published_version_id,omitempty" yaml:"published_version_id,omitempty"`
	PublishedCharts       []ChartConfig `json:"published_charts,omitempty" yaml:"published_charts,omitempty"`
	HasUnpublishedChanges *bool         `json:"has_unpublished_changes,omitempty" yaml:"has_unpublished_changes,omitempty"`
}

// PublishTemplateRequest is the optional body of POST /api/v1/templates/:id/publish
// (k8s-stack-manager v0.6.0+). An empty Version makes the server use the
// version of the working copy.
type PublishTemplateRequest struct {
	Version       string `json:"version,omitempty" yaml:"version,omitempty"`
	ChangeSummary string `json:"change_summary,omitempty" yaml:"change_summary,omitempty"`
}

// PublishTemplateResponse is the response of POST /api/v1/templates/:id/publish:
// the template plus the release that users now get. SnapshotCreated is false
// when the working copy equals the latest release (no new version). Older
// servers return only the template; SnapshotCreated is then nil.
type PublishTemplateResponse struct {
	StackTemplate
	SnapshotCreated *bool `json:"snapshot_created,omitempty" yaml:"snapshot_created,omitempty"`
}

// ChartConfig represents a Helm chart configuration within a definition or
// template. Field set is the union of backend models.ChartConfig (definition
// charts) and models.TemplateChartConfig (template charts) — LockedValues and
// Required are populated only on template-chart responses.
type ChartConfig struct {
	Base
	StackDefinitionID string `json:"stack_definition_id,omitempty" yaml:"stack_definition_id,omitempty"`
	Name              string `json:"name" yaml:"name"`
	RepoURL           string `json:"repository_url" yaml:"repository_url"`
	SourceRepoURL     string `json:"source_repo_url,omitempty" yaml:"source_repo_url,omitempty"`
	ChartName         string `json:"chart_name" yaml:"chart_name"`
	ChartPath         string `json:"chart_path,omitempty" yaml:"chart_path,omitempty"`
	ChartVersion      string `json:"chart_version,omitempty" yaml:"chart_version,omitempty"`
	ReleaseName       string `json:"release_name,omitempty" yaml:"release_name,omitempty"`
	DefaultValues     string `json:"default_values,omitempty" yaml:"default_values,omitempty"`
	LockedValues      string `json:"locked_values,omitempty" yaml:"locked_values,omitempty"`
	DeployOrder       int    `json:"deploy_order,omitempty" yaml:"deploy_order,omitempty"`
	Required          bool   `json:"required,omitempty" yaml:"required,omitempty"`
	BuildPipelineID   string `json:"build_pipeline_id,omitempty" yaml:"build_pipeline_id,omitempty"`
}

// Cluster represents a registered Kubernetes cluster. Admin and devops
// users get the full backend models.Cluster from GET /api/v1/clusters;
// other roles get only the handlers.ClusterSummary fields (id, name,
// is_default), so every other field is empty for them. CreatedAt is nil
// for that summary; callers use it to tell the two shapes apart.
type Cluster struct {
	CreatedAt           *time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	ID                  string     `json:"id" yaml:"id"`
	Name                string     `json:"name" yaml:"name"`
	Description         string     `json:"description,omitempty" yaml:"description,omitempty"`
	Status              string     `json:"health_status,omitempty" yaml:"health_status,omitempty"`
	IsDefault           bool       `json:"is_default" yaml:"is_default"`
	APIServerURL        string     `json:"api_server_url,omitempty" yaml:"api_server_url,omitempty"`
	Region              string     `json:"region,omitempty" yaml:"region,omitempty"`
	MaxNamespaces       int        `json:"max_namespaces,omitempty" yaml:"max_namespaces,omitempty"`
	MaxInstancesPerUser int        `json:"max_instances_per_user,omitempty" yaml:"max_instances_per_user,omitempty"`
	UseInCluster        bool       `json:"use_in_cluster,omitempty" yaml:"use_in_cluster,omitempty"`
	RegistryURL         string     `json:"registry_url,omitempty" yaml:"registry_url,omitempty"`
	RegistryUsername    string     `json:"registry_username,omitempty" yaml:"registry_username,omitempty"`
	ImagePullSecretName string     `json:"image_pull_secret_name,omitempty" yaml:"image_pull_secret_name,omitempty"`
}

// CreateClusterRequest is the request body for POST /api/v1/clusters.
type CreateClusterRequest struct {
	Name                string `json:"name" yaml:"name"`
	Description         string `json:"description,omitempty" yaml:"description,omitempty"`
	APIServerURL        string `json:"api_server_url,omitempty" yaml:"api_server_url,omitempty"`
	KubeconfigData      string `json:"kubeconfig_data,omitempty" yaml:"kubeconfig_data,omitempty"`
	KubeconfigPath      string `json:"kubeconfig_path,omitempty" yaml:"kubeconfig_path,omitempty"`
	Region              string `json:"region,omitempty" yaml:"region,omitempty"`
	MaxNamespaces       int    `json:"max_namespaces,omitempty" yaml:"max_namespaces,omitempty"`
	MaxInstancesPerUser int    `json:"max_instances_per_user,omitempty" yaml:"max_instances_per_user,omitempty"`
	IsDefault           bool   `json:"is_default,omitempty" yaml:"is_default,omitempty"`
	UseInCluster        bool   `json:"use_in_cluster,omitempty" yaml:"use_in_cluster,omitempty"`
	RegistryURL         string `json:"registry_url,omitempty" yaml:"registry_url,omitempty"`
	RegistryUsername    string `json:"registry_username,omitempty" yaml:"registry_username,omitempty"`
	RegistryPassword    string `json:"registry_password,omitempty" yaml:"registry_password,omitempty"`
	ImagePullSecretName string `json:"image_pull_secret_name,omitempty" yaml:"image_pull_secret_name,omitempty"`
}

// UpdateClusterRequest is the request body for PUT /api/v1/clusters/:id.
// All fields are optional; only non-nil fields are sent.
type UpdateClusterRequest struct {
	Name                *string `json:"name,omitempty" yaml:"name,omitempty"`
	Description         *string `json:"description,omitempty" yaml:"description,omitempty"`
	APIServerURL        *string `json:"api_server_url,omitempty" yaml:"api_server_url,omitempty"`
	KubeconfigData      *string `json:"kubeconfig_data,omitempty" yaml:"kubeconfig_data,omitempty"`
	KubeconfigPath      *string `json:"kubeconfig_path,omitempty" yaml:"kubeconfig_path,omitempty"`
	Region              *string `json:"region,omitempty" yaml:"region,omitempty"`
	MaxNamespaces       *int    `json:"max_namespaces,omitempty" yaml:"max_namespaces,omitempty"`
	MaxInstancesPerUser *int    `json:"max_instances_per_user,omitempty" yaml:"max_instances_per_user,omitempty"`
	IsDefault           *bool   `json:"is_default,omitempty" yaml:"is_default,omitempty"`
	UseInCluster        *bool   `json:"use_in_cluster,omitempty" yaml:"use_in_cluster,omitempty"`
	RegistryURL         *string `json:"registry_url,omitempty" yaml:"registry_url,omitempty"`
	RegistryUsername    *string `json:"registry_username,omitempty" yaml:"registry_username,omitempty"`
	RegistryPassword    *string `json:"registry_password,omitempty" yaml:"registry_password,omitempty"`
	ImagePullSecretName *string `json:"image_pull_secret_name,omitempty" yaml:"image_pull_secret_name,omitempty"`
}

// User represents a user account on the server. Returned by
// GET /api/v1/auth/me (200, single), GET /api/v1/users (200, list,
// admin-only), and POST /api/v1/auth/register (201, single).
//
// Population: only returned on a 2xx response. On non-2xx the client
// surfaces an APIError and the struct is left zero-valued by the caller.
// Backend never serialises the password hash (json:"-" on the server side).
//
// Field semantics:
//   - AuthProvider — "local" (password) or an external IdP name (e.g. "oidc").
//     Reset-password only applies when AuthProvider == "local".
//   - ExternalID — set only for federated users; nil for local accounts.
//   - Disabled — when true, all sessions and API keys for this user have
//     been revoked and any new authentication attempt is rejected.
//   - ServiceAccount — when true, this is a non-interactive identity used
//     by CI/automation; only admins can create these.
type User struct {
	Base
	Username       string  `json:"username" yaml:"username"`
	DisplayName    string  `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Email          string  `json:"email,omitempty" yaml:"email,omitempty"`
	Role           string  `json:"role" yaml:"role"`
	AuthProvider   string  `json:"auth_provider,omitempty" yaml:"auth_provider,omitempty"`
	ExternalID     *string `json:"external_id,omitempty" yaml:"external_id,omitempty"`
	Disabled       bool    `json:"disabled" yaml:"disabled"`
	ServiceAccount bool    `json:"service_account" yaml:"service_account"`
}

// RegisterRequest is the body for POST /api/v1/auth/register.
//
// The endpoint requires an authenticated caller. Non-admin callers can only
// register if self-registration is enabled server-side; admins can always
// register and are the only callers permitted to set Role or ServiceAccount.
//
// Field semantics:
//   - Username, Password — required.
//   - DisplayName — optional; defaults to Username server-side when empty.
//   - Role — admin-only; ignored from non-admin callers (server forces "user").
//   - ServiceAccount — admin-only; ignored from non-admin callers.
type RegisterRequest struct {
	Username       string `json:"username" yaml:"username"`
	Password       string `json:"password" yaml:"password"`
	DisplayName    string `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Role           string `json:"role,omitempty" yaml:"role,omitempty"`
	ServiceAccount bool   `json:"service_account,omitempty" yaml:"service_account,omitempty"`
}

// ResetPasswordRequest is the body for PUT /api/v1/users/:id/password.
// Backend rejects passwords shorter than 8 characters.
type ResetPasswordRequest struct {
	Password string `json:"password" yaml:"password"`
}

// ChangeRoleRequest is the body for PUT /api/v1/users/:id/role
// (k8s-stack-manager v0.8.0 or later). Role is "user", "devops" or "admin".
type ChangeRoleRequest struct {
	Role string `json:"role" yaml:"role"`
}

// ChangeRoleResponse is the response of PUT /api/v1/users/:id/role. Changed
// is false when the user already had the role; the sessions of the user then
// stay valid.
type ChangeRoleResponse struct {
	ID      string `json:"id" yaml:"id"`
	OldRole string `json:"old_role" yaml:"old_role"`
	NewRole string `json:"new_role" yaml:"new_role"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
	Changed bool   `json:"changed" yaml:"changed"`
}

// APIKey is one entry in the response of GET /api/v1/users/:id/api-keys.
// Mirrors backend models.APIKey.
//
// Population: only returned on a 2xx response. On non-2xx the client surfaces
// an APIError and the struct is left zero-valued by the caller. The KeyHash
// is tagged json:"-" on the server side and is never present in any
// response; the raw key is only ever returned by Create (see
// CreateAPIKeyResponse) and is non-retrievable thereafter.
//
// Field semantics:
//   - Prefix — first 16 chars of the raw key, safe to display and used for
//     quick visual identification.
//   - LastUsedAt — nil until the key has authenticated at least one request.
//   - ExpiresAt — always present in responses (the backend rejects creation
//     without an expiry), modelled as pointer to allow zero-value omission
//     in JSON tooling.
type APIKey struct {
	ID         string     `json:"id" yaml:"id"`
	UserID     string     `json:"user_id" yaml:"user_id"`
	Name       string     `json:"name" yaml:"name"`
	Prefix     string     `json:"prefix" yaml:"prefix"`
	CreatedAt  time.Time  `json:"created_at" yaml:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" yaml:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
}

// CreateAPIKeyRequest is the body for POST /api/v1/users/:id/api-keys.
// The backend requires an expiry — exactly one of ExpiresAt or ExpiresInDays
// must be set. Setting both returns 400. ExpiresAt accepts RFC3339 or
// YYYY-MM-DD; the latter is treated as 23:59:59 UTC of that day. The expiry
// must be strictly in the future and may be capped by the server's
// API_KEY_MAX_LIFETIME_DAYS setting.
type CreateAPIKeyRequest struct {
	Name          string  `json:"name" yaml:"name"`
	ExpiresAt     *string `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	ExpiresInDays *int    `json:"expires_in_days,omitempty" yaml:"expires_in_days,omitempty"`
}

// CreateAPIKeyResponse is returned ONCE by POST /api/v1/users/:id/api-keys
// (HTTP 201). The RawKey field carries the plaintext key prefixed with
// "sk_" — it is never persisted server-side and cannot be retrieved again.
// Callers must surface it immediately (typically to stdout for piping) and
// must not store it in config files.
type CreateAPIKeyResponse struct {
	ID        string     `json:"id" yaml:"id"`
	Name      string     `json:"name" yaml:"name"`
	Prefix    string     `json:"prefix" yaml:"prefix"`
	RawKey    string     `json:"raw_key" yaml:"raw_key"`
	CreatedAt time.Time  `json:"created_at" yaml:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
}

// AnalyticsOverview is the success-path response of GET /api/v1/analytics/overview.
// Mirrors backend handlers.OverviewStats.
//
// Population: only returned on HTTP 200. On non-2xx the client surfaces an
// APIError and the struct is left zero-valued by the caller. Devops-gated.
//
// Field declaration order is ALPHABETICAL by json tag so encoding/json
// emits keys in stable, alphabetical order — required for golden-file
// JSON comparisons in tests and for downstream tooling that diffs the
// output. Do not reorder.
type AnalyticsOverview struct {
	RunningInstances int `json:"running_instances" yaml:"running_instances"`
	TotalDefinitions int `json:"total_definitions" yaml:"total_definitions"`
	TotalDeploys     int `json:"total_deploys" yaml:"total_deploys"`
	TotalInstances   int `json:"total_instances" yaml:"total_instances"`
	TotalTemplates   int `json:"total_templates" yaml:"total_templates"`
	TotalUsers       int `json:"total_users" yaml:"total_users"`
}

// TemplateStats is one element in the success-path response of
// GET /api/v1/analytics/templates. Mirrors backend handlers.TemplateStats.
//
// Population: only returned on HTTP 200. Devops-gated.
//
// Field declaration order is alphabetical by json tag (see AnalyticsOverview).
//
// Field semantics:
//   - SuccessRate is a percentage (0.0–100.0) computed server-side as
//     success_count/deploy_count*100; backend returns 0.0 when deploy_count
//     is zero. Treat as informational; tests use a tolerance for float
//     comparisons.
type TemplateStats struct {
	Category        string  `json:"category" yaml:"category"`
	DefinitionCount int     `json:"definition_count" yaml:"definition_count"`
	DeployCount     int     `json:"deploy_count" yaml:"deploy_count"`
	ErrorCount      int     `json:"error_count" yaml:"error_count"`
	InstanceCount   int     `json:"instance_count" yaml:"instance_count"`
	IsPublished     bool    `json:"is_published" yaml:"is_published"`
	SuccessCount    int     `json:"success_count" yaml:"success_count"`
	SuccessRate     float64 `json:"success_rate" yaml:"success_rate"`
	TemplateID      string  `json:"template_id" yaml:"template_id"`
	TemplateName    string  `json:"template_name" yaml:"template_name"`
}

// UserStats is one element in the success-path response of
// GET /api/v1/analytics/users. Mirrors backend handlers.UserStats.
//
// Population: only returned on HTTP 200. Admin-gated.
//
// Field declaration order is alphabetical by json tag (see AnalyticsOverview).
//
// Field semantics:
//   - LastActive is nil for users who have never deployed anything.
type UserStats struct {
	DeployCount   int        `json:"deploy_count" yaml:"deploy_count"`
	InstanceCount int        `json:"instance_count" yaml:"instance_count"`
	LastActive    *time.Time `json:"last_active,omitempty" yaml:"last_active,omitempty"`
	UserID        string     `json:"user_id" yaml:"user_id"`
	Username      string     `json:"username" yaml:"username"`
}

// AuditLogEntry is one row in the audit log. Mirrors backend models.AuditLog.
//
// Population: only returned on HTTP 200 (list endpoint). The export endpoint
// returns the same shape inside a top-level JSON array, or as a CSV with the
// header order documented in the export godoc — not this struct.
//
// Field declaration order is ALPHABETICAL by json tag so encoding/json
// emits keys in stable alphabetical order — required for golden-file
// JSON comparisons in tests. Do not reorder.
type AuditLogEntry struct {
	Action     string    `json:"action" yaml:"action"`
	Details    string    `json:"details" yaml:"details"`
	EntityID   string    `json:"entity_id" yaml:"entity_id"`
	EntityType string    `json:"entity_type" yaml:"entity_type"`
	ID         string    `json:"id" yaml:"id"`
	Timestamp  time.Time `json:"timestamp" yaml:"timestamp"`
	UserID     string    `json:"user_id" yaml:"user_id"`
	Username   string    `json:"username" yaml:"username"`
}

// PaginatedAuditLogs is the success-path response of GET /api/v1/audit-logs.
// Mirrors backend models.PaginatedAuditLogs.
//
// Population: only returned on HTTP 200. NextCursor is empty when no more
// pages exist; offset-based pagination is also supported via Limit/Offset.
//
// Field declaration order is alphabetical by json tag (see AuditLogEntry).
type PaginatedAuditLogs struct {
	Data       []AuditLogEntry `json:"data" yaml:"data"`
	Limit      int             `json:"limit" yaml:"limit"`
	NextCursor string          `json:"next_cursor,omitempty" yaml:"next_cursor,omitempty"`
	Offset     int             `json:"offset" yaml:"offset"`
	Total      int64           `json:"total" yaml:"total"`
}

// AuditLogListParams holds the query parameters accepted by GET /api/v1/audit-logs
// and GET /api/v1/audit-logs/export. The client forwards only the fields the
// caller sets — empty strings and zero ints are omitted from the wire request.
//
// StartDate / EndDate are formatted as RFC3339 before being sent — the backend
// rejects any other format with HTTP 400. The CLI translates --since/--until
// shorthand (e.g. "24h") to absolute RFC3339 timestamps before populating this
// struct.
//
// The struct is never sent as a body — fields are flattened to URL query
// parameters by client.auditLogParamsToQuery. The json/yaml tags are present
// for consistency with the cli/pkg/types convention (every exported field has
// both tags) and so the struct can be safely printed by debugging code.
type AuditLogListParams struct {
	StartDate  *time.Time `json:"start_date,omitempty" yaml:"start_date,omitempty"`
	EndDate    *time.Time `json:"end_date,omitempty" yaml:"end_date,omitempty"`
	UserID     string     `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	EntityType string     `json:"entity_type,omitempty" yaml:"entity_type,omitempty"`
	EntityID   string     `json:"entity_id,omitempty" yaml:"entity_id,omitempty"`
	Action     string     `json:"action,omitempty" yaml:"action,omitempty"`
	Cursor     string     `json:"cursor,omitempty" yaml:"cursor,omitempty"`
	Limit      int        `json:"limit,omitempty" yaml:"limit,omitempty"`
	Offset     int        `json:"offset,omitempty" yaml:"offset,omitempty"`
}

// Notification is one in-app notification. Mirrors backend models.Notification.
//
// Population: only returned on HTTP 200 (list endpoint).
//
// Field declaration order is alphabetical by json tag so encoding/json
// emits keys in stable alphabetical order — required for golden-file
// JSON comparisons in tests. Do not reorder.
type Notification struct {
	CreatedAt  time.Time `json:"created_at" yaml:"created_at"`
	EntityID   string    `json:"entity_id,omitempty" yaml:"entity_id,omitempty"`
	EntityType string    `json:"entity_type,omitempty" yaml:"entity_type,omitempty"`
	ID         string    `json:"id" yaml:"id"`
	IsRead     bool      `json:"is_read" yaml:"is_read"`
	Message    string    `json:"message" yaml:"message"`
	Title      string    `json:"title" yaml:"title"`
	Type       string    `json:"type" yaml:"type"`
	UserID     string    `json:"user_id" yaml:"user_id"`
}

// PaginatedNotifications is the success-path response of GET /api/v1/notifications.
// Mirrors backend models.PaginatedNotifications.
//
// Field declaration order is alphabetical by json tag (see Notification).
type PaginatedNotifications struct {
	Notifications []Notification `json:"notifications" yaml:"notifications"`
	Total         int64          `json:"total" yaml:"total"`
	UnreadCount   int64          `json:"unread_count" yaml:"unread_count"`
}

// NotificationPreference is one row in the notification-preference payload
// used by GET /api/v1/notifications/preferences and PUT /api/v1/notifications/preferences.
// Mirrors backend models.NotificationPreference.
//
// Population: returned on HTTP 200 from GET and PUT. On non-2xx the client
// surfaces an APIError and callers should treat the value as zero-valued.
//
// Field semantics:
//   - EventType is required and always populated on GET/PUT responses;
//     forms the primary key with UserID server-side.
//   - Enabled is required and always populated (default true on first PUT).
//   - Channel is OPTIONAL on PUT — backend defaults to "in_app" when the
//     field is empty. Always populated on GET responses.
//   - ID and UserID are populated on GET/PUT responses; they may be omitted
//     in PUT request bodies (the backend keys on EventType + the
//     authenticated user, not on the wire ID).
//
// Field declaration order is alphabetical by json tag (see Notification).
type NotificationPreference struct {
	Channel   string `json:"channel,omitempty" yaml:"channel,omitempty"`
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	EventType string `json:"event_type" yaml:"event_type"`
	ID        string `json:"id,omitempty" yaml:"id,omitempty"`
	UserID    string `json:"user_id,omitempty" yaml:"user_id,omitempty"`
}

// UnreadCountResponse is the success-path response of GET /api/v1/notifications/count.
// The endpoint returns a single key {"unread_count": N} rather than a wrapped
// object — modelled as a struct so JSON/YAML decode cleanly.
type UnreadCountResponse struct {
	UnreadCount int64 `json:"unread_count" yaml:"unread_count"`
}

// Favorite is one row in the authenticated user's favorites collection.
// Mirrors backend models.UserFavorite.
//
// Population: returned on HTTP 200/201 from GET /api/v1/favorites and POST
// /api/v1/favorites. On non-2xx the client surfaces an APIError.
//
// Field semantics:
//   - EntityType is one of "definition", "instance", "template" (validated
//     server-side).
//   - EntityID is the target entity's ID — there is no name-or-ID
//     resolution at this endpoint.
//   - ID and UserID are server-assigned and only populated in responses.
//
// Field declaration order is alphabetical by json tag (golden-file
// contract — required for stable JSON output).
type Favorite struct {
	CreatedAt  time.Time `json:"created_at" yaml:"created_at"`
	EntityID   string    `json:"entity_id" yaml:"entity_id"`
	EntityType string    `json:"entity_type" yaml:"entity_type"`
	ID         string    `json:"id,omitempty" yaml:"id,omitempty"`
	UserID     string    `json:"user_id,omitempty" yaml:"user_id,omitempty"`
}

// AddFavoriteRequest is the request body for POST /api/v1/favorites.
//
// Both fields are required; the backend rejects an empty EntityType or
// EntityID with HTTP 400. EntityType must be one of "definition",
// "instance", or "template".
type AddFavoriteRequest struct {
	EntityID   string `json:"entity_id" yaml:"entity_id"`
	EntityType string `json:"entity_type" yaml:"entity_type"`
}

// GitProvider is the status of one configured Git provider returned by
// GET /api/v1/git/providers. Mirrors backend gitprovider.ProviderStatus.
//
// Population: only returned on HTTP 200. The backend reports availability
// based on whether the provider was configured at server start; it does
// NOT perform a live connectivity check at request time (see the registry
// HealthCheck for that).
//
// Field declaration order is alphabetical by json tag.
type GitProvider struct {
	Available bool   `json:"available" yaml:"available"`
	Type      string `json:"type" yaml:"type"`
}

// CreateStackRequest is the request body for POST /api/v1/stack-instances.
// It contains only the writable fields — server-owned fields like ID, owner,
// namespace, status are excluded to avoid backend validation errors.
type CreateStackRequest struct {
	Name              string `json:"name" yaml:"name"`
	StackDefinitionID string `json:"stack_definition_id" yaml:"stack_definition_id"`
	Branch            string `json:"branch,omitempty" yaml:"branch,omitempty"`
	ClusterID         string `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
	TTLMinutes        int    `json:"ttl_minutes,omitempty" yaml:"ttl_minutes,omitempty"`
}

// LoginRequest is the request body for POST /api/v1/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is the response from POST /api/v1/auth/login.
type LoginResponse struct {
	Token     string `json:"token" yaml:"token"`
	ExpiresAt string `json:"expires_at" yaml:"expires_at"`
	User      User   `json:"user" yaml:"user"`
}

// DeploymentLog represents a deployment log entry.
type DeploymentLog struct {
	StartedAt      *time.Time `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
	ID             string     `json:"id" yaml:"id"`
	InstanceID     string     `json:"stack_instance_id" yaml:"stack_instance_id"`
	Action         string     `json:"action" yaml:"action"`
	Status         string     `json:"status" yaml:"status"`
	Output         string     `json:"output,omitempty" yaml:"output,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty" yaml:"error_message,omitempty"`
	ValuesSnapshot string     `json:"values_snapshot,omitempty" yaml:"values_snapshot,omitempty"`
	TargetLogID    string     `json:"target_log_id,omitempty" yaml:"target_log_id,omitempty"`
}

// DeployResponse is the response from deploy/stop/clean operations (HTTP 202).
type DeployResponse struct {
	LogID   string `json:"log_id" yaml:"log_id"`
	Message string `json:"message" yaml:"message"`
}

// RollbackRequest is the request body for POST /api/v1/stack-instances/:id/rollback.
type RollbackRequest struct {
	TargetLogID string `json:"target_log_id,omitempty" yaml:"target_log_id,omitempty"`
}

// RollbackResponse is the response from POST /api/v1/stack-instances/:id/rollback.
//
// For a rollback to a target deploy (k8s-stack-manager v0.6.0+), ValuesDrift
// is true when the stored overrides produce other values than the target:
// the next deploy changes the running values again. Warning explains it.
// Both are absent for a rollback by one revision and on older servers.
type RollbackResponse struct {
	LogID       string `json:"log_id" yaml:"log_id"`
	Message     string `json:"message" yaml:"message"`
	TargetLogID string `json:"target_log_id,omitempty" yaml:"target_log_id,omitempty"`
	ValuesDrift *bool  `json:"values_drift,omitempty" yaml:"values_drift,omitempty"`
	Warning     string `json:"warning,omitempty" yaml:"warning,omitempty"`
}

// DeploymentLogResult holds paginated deployment log results from the backend.
type DeploymentLogResult struct {
	Data       []DeploymentLog `json:"data"`
	Total      int64           `json:"total"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// DeployLogValuesResponse holds values snapshot for a deployment log entry.
type DeployLogValuesResponse struct {
	LogID  string                 `json:"log_id" yaml:"log_id"`
	Values map[string]interface{} `json:"values" yaml:"values"`
}

// ListResponse wraps paginated API responses.
type ListResponse[T any] struct {
	Data       []T `json:"data"`
	Total      int `json:"total"`
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
	TotalPages int `json:"total_pages"`
}

// BulkOperationResult represents the result of a single instance/template in
// a bulk operation. Field names match the backend's BulkOperationResultItem
// (stack-instance bulk) and the equivalent template bulk handler — both ship
// `instance_id` (or template_id) plus a string `status` of "success"/"error",
// not a bool. Earlier versions of this struct used `id` + `success` which
// caused the response to silently decode as zero values against the live
// backend.
type BulkOperationResult struct {
	// InstanceID is set on stack-instance bulk responses (deploy/stop/clean/delete).
	InstanceID string `json:"instance_id,omitempty" yaml:"instance_id,omitempty"`
	// TemplateID is set on template bulk responses (delete/publish/unpublish).
	TemplateID   string `json:"template_id,omitempty" yaml:"template_id,omitempty"`
	InstanceName string `json:"instance_name,omitempty" yaml:"instance_name,omitempty"`
	TemplateName string `json:"template_name,omitempty" yaml:"template_name,omitempty"`
	// Status is the per-item outcome; backend returns "success" or "error".
	Status string `json:"status" yaml:"status"`
	Error  string `json:"error,omitempty" yaml:"error,omitempty"`
	LogID  string `json:"log_id,omitempty" yaml:"log_id,omitempty"`
}

// ID returns the identifier carried by this result regardless of whether the
// underlying endpoint was instance-scoped or template-scoped. Empty if neither
// field is populated.
func (r BulkOperationResult) ID() string {
	if r.InstanceID != "" {
		return r.InstanceID
	}
	return r.TemplateID
}

// Success returns true when the per-item status is "success". A miscoded
// or omitted status string therefore reads as failure, which matches the
// caller's expectation (no positive confirmation == treat as failed).
func (r BulkOperationResult) Success() bool {
	return r.Status == "success"
}

// BulkResponse wraps bulk operation results. Total/Successful/Failed mirror
// the backend's BulkOperationResponse counters; Results retains per-item detail.
type BulkResponse struct {
	Total      int                   `json:"total" yaml:"total"`
	Successful int                   `json:"successful" yaml:"successful"`
	Failed     int                   `json:"failed" yaml:"failed"`
	Results    []BulkOperationResult `json:"results" yaml:"results"`
}

// ValueOverride is a per-chart value override. Mirrors backend
// models.ValueOverride (GET/PUT /api/v1/stack-instances/:id/overrides[/:chartId]).
// Values is a YAML document.
type ValueOverride struct {
	ID              string    `json:"id" yaml:"id"`
	StackInstanceID string    `json:"stack_instance_id" yaml:"stack_instance_id"`
	ChartConfigID   string    `json:"chart_config_id" yaml:"chart_config_id"`
	Values          string    `json:"values" yaml:"values"`
	UpdatedAt       time.Time `json:"updated_at" yaml:"updated_at"`
}

// BranchOverride is a per-chart branch override. Mirrors backend
// models.ChartBranchOverride (/api/v1/stack-instances/:id/branches[/:chartId]).
type BranchOverride struct {
	ID              string    `json:"id" yaml:"id"`
	StackInstanceID string    `json:"stack_instance_id" yaml:"stack_instance_id"`
	ChartConfigID   string    `json:"chart_config_id" yaml:"chart_config_id"`
	Branch          string    `json:"branch" yaml:"branch"`
	UpdatedAt       time.Time `json:"updated_at" yaml:"updated_at"`
}

// GitBranch is a branch from the git provider. Mirrors backend
// gitprovider.Branch (GET /api/v1/git/branches).
type GitBranch struct {
	Name      string `json:"name" yaml:"name"`
	IsDefault bool   `json:"is_default" yaml:"is_default"`
}

// InstantiateTemplateRequest is the request body for POST /api/v1/templates/:id/instantiate.
type InstantiateTemplateRequest struct {
	Name      string `json:"name" yaml:"name"`
	Branch    string `json:"branch,omitempty" yaml:"branch,omitempty"`
	ClusterID string `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
}

// QuickDeployRequest is the request body for POST /api/v1/templates/:id/quick-deploy.
type QuickDeployRequest struct {
	Name      string `json:"instance_name" yaml:"instance_name"`
	Branch    string `json:"branch,omitempty" yaml:"branch,omitempty"`
	ClusterID string `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
}

// CreateDefinitionRequest is the request body for POST /api/v1/stack-definitions.
type CreateDefinitionRequest struct {
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Charts      []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// UpdateDefinitionRequest is the request body for PUT /api/v1/stack-definitions/:id.
type UpdateDefinitionRequest struct {
	Name          string        `json:"name,omitempty" yaml:"name,omitempty"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultBranch string        `json:"default_branch,omitempty" yaml:"default_branch,omitempty"`
	Charts        []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// UpdateChartConfigRequest is the request body for PUT /api/v1/stack-definitions/:id/charts/:chartID.
//
// Includes every field the backend's ChartConfig knows about, so a
// GET-then-PUT round-trip in update-chart doesn't silently wipe fields
// stackctl flags don't expose. All string fields use omitempty, so an
// empty value is dropped from the JSON body and the backend preserves
// its existing value; there is no way to clear a field via this request.
type UpdateChartConfigRequest struct {
	ChartName       string `json:"chart_name,omitempty" yaml:"chart_name,omitempty"`
	RepositoryURL   string `json:"repository_url,omitempty" yaml:"repository_url,omitempty"`
	ChartPath       string `json:"chart_path,omitempty" yaml:"chart_path,omitempty"`
	ChartVersion    string `json:"chart_version,omitempty" yaml:"chart_version,omitempty"`
	SourceRepoURL   string `json:"source_repo_url,omitempty" yaml:"source_repo_url,omitempty"`
	BuildPipelineID string `json:"build_pipeline_id,omitempty" yaml:"build_pipeline_id,omitempty"`
	DeployOrder     *int   `json:"deploy_order,omitempty" yaml:"deploy_order,omitempty"`
	DefaultValues   string `json:"default_values,omitempty" yaml:"default_values,omitempty"`
}

// UpdateTemplateChartRequest is the body of PUT /api/v1/templates/:id/charts/:chartId.
// The backend replaces every field, so callers must send the full record.
type UpdateTemplateChartRequest struct {
	ChartName       string `json:"chart_name" yaml:"chart_name"`
	RepositoryURL   string `json:"repository_url" yaml:"repository_url"`
	SourceRepoURL   string `json:"source_repo_url" yaml:"source_repo_url"`
	BuildPipelineID string `json:"build_pipeline_id" yaml:"build_pipeline_id"`
	ChartPath       string `json:"chart_path" yaml:"chart_path"`
	ChartVersion    string `json:"chart_version" yaml:"chart_version"`
	DefaultValues   string `json:"default_values" yaml:"default_values"`
	LockedValues    string `json:"locked_values" yaml:"locked_values"`
	DeployOrder     int    `json:"deploy_order" yaml:"deploy_order"`
	Required        bool   `json:"required" yaml:"required"`
}

// OrphanedNamespace is one entry of GET /api/v1/admin/orphaned-namespaces:
// a stack-* namespace with no matching stack instance. Managed is true when
// the namespace has the label managed-by=k8s-stack-manager; the delete of an
// unmanaged namespace needs ?confirm=<name>. HelmReleases and ResourceCounts
// are filled only with ?details=true.
type OrphanedNamespace struct {
	ResourceCounts *ResourceCounts `json:"resource_counts,omitempty" yaml:"resource_counts,omitempty"`
	Name           string          `json:"name" yaml:"name"`
	Phase          string          `json:"phase,omitempty" yaml:"phase,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	HelmReleases   []string        `json:"helm_releases,omitempty" yaml:"helm_releases,omitempty"`
	Managed        bool            `json:"managed" yaml:"managed"`
}

// ResourceCounts holds the number of workload resources in a namespace.
type ResourceCounts struct {
	Deployments int `json:"deployments" yaml:"deployments"`
	Pods        int `json:"pods" yaml:"pods"`
	Services    int `json:"services" yaml:"services"`
}

// BulkInstancesRequest is the request body for bulk operations against
// stack instances (deploy / stop / clean / delete). The backend handler
// binds into `{"instance_ids": [...]}` with `binding:"required"` — using
// any other key returns 400 "instance_ids is required".
type BulkInstancesRequest struct {
	InstanceIDs []string `json:"instance_ids" yaml:"instance_ids"`
}

// BulkTemplatesRequest is the request body for bulk operations against
// stack templates (delete / publish / unpublish). The backend handler
// binds into `{"template_ids": [...]}` with `binding:"required"`.
type BulkTemplatesRequest struct {
	TemplateIDs []string `json:"template_ids" yaml:"template_ids"`
}

// GitValidateResponse is the result of GET /api/v1/git/validate-branch.
type GitValidateResponse struct {
	Valid  bool   `json:"valid" yaml:"valid"`
	Branch string `json:"branch" yaml:"branch"`
}

// ClusterHealthSummary is the success-path response of
// GET /api/v1/clusters/:id/health/summary. Field names mirror the backend
// k8s.ClusterSummary struct so JSON round-trips without renaming.
//
// Population: only returned on HTTP 200. On non-2xx the client surfaces an
// APIError and this struct is left zero-valued by the caller.
// Field semantics:
//   - NodeCount, ReadyNodeCount, NamespaceCount — always present (may be 0
//     for empty/just-registered clusters; 0 is a valid value, not "missing").
//   - TotalCPU, TotalMemory, AllocatableCPU, AllocatableMemory — populated
//     when the backend was able to read node capacity; omitted (empty string)
//     when no nodes were reachable, hence the `omitempty` tags.
type ClusterHealthSummary struct {
	NodeCount         int    `json:"node_count" yaml:"node_count"`
	ReadyNodeCount    int    `json:"ready_node_count" yaml:"ready_node_count"`
	TotalCPU          string `json:"total_cpu,omitempty" yaml:"total_cpu,omitempty"`
	TotalMemory       string `json:"total_memory,omitempty" yaml:"total_memory,omitempty"`
	AllocatableCPU    string `json:"allocatable_cpu,omitempty" yaml:"allocatable_cpu,omitempty"`
	AllocatableMemory string `json:"allocatable_memory,omitempty" yaml:"allocatable_memory,omitempty"`
	NamespaceCount    int    `json:"namespace_count" yaml:"namespace_count"`
}

// ClusterTestConnectionResult is the success-path response of
// POST /api/v1/clusters/:id/test (HTTP 200 only). Populated by
// Client.TestClusterConnection.
//
// Population: only returned on HTTP 200 with Status == "success". On an
// unreachable cluster the backend returns HTTP 502 with a JSON body of
// shape {"status":"error","message":"..."}; the client decoder in
// Client.do() maps that into an APIError (Message set to the backend
// "message"), so callers never see this struct populated with Status="error".
// Field semantics:
//   - Status — always present on the 200 path ("success").
//   - Message — present on the 200 path; backend uses "Connection successful".
//   - ServerVersion — populated when the backend's discovery client reported
//     a Kubernetes git version (e.g. "v1.29.4"); empty when the discovery
//     response was malformed or the test fake doesn't implement RESTClient.
type ClusterTestConnectionResult struct {
	Status        string `json:"status" yaml:"status"`
	Message       string `json:"message,omitempty" yaml:"message,omitempty"`
	ServerVersion string `json:"server_version,omitempty" yaml:"server_version,omitempty"`
}

// ClusterResourceQuantity holds CPU/memory/pod capacity values as strings,
// mirroring backend k8s.ResourceQuantity. Embedded inside ClusterNodeStatus
// (Capacity, Allocatable) returned from
// GET /api/v1/clusters/:id/health/nodes.
//
// Field semantics:
//   - CPU, Memory — always populated for a node returned by the API; format
//     is Kubernetes resource notation ("3800m", "16Gi").
//   - Pods — currently always populated by the backend (k8s
//     ResourceList.Pods().String() returns "0" worst case). The `omitempty`
//     JSON tag is defensive in case a future backend version stops emitting
//     the field.
type ClusterResourceQuantity struct {
	CPU    string `json:"cpu" yaml:"cpu"`
	Memory string `json:"memory" yaml:"memory"`
	Pods   string `json:"pods,omitempty" yaml:"pods,omitempty"`
}

// ClusterNodeCondition represents one node condition (Ready, MemoryPressure,
// DiskPressure, PIDPressure, NetworkUnavailable, etc.), mirroring backend
// k8s.NodeCondition. Embedded inside ClusterNodeStatus.Conditions.
//
// Field semantics:
//   - Type, Status — always present. Status is one of "True"/"False"/"Unknown".
//   - Message — populated when the controller has additional context (which
//     in practice is almost always — the kubelet sets a message even on
//     healthy Ready conditions). The `omitempty` JSON tag only hides
//     genuinely empty strings.
type ClusterNodeCondition struct {
	Type    string `json:"type" yaml:"type"`
	Status  string `json:"status" yaml:"status"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

// ClusterNodeStatus is one node's health snapshot, returned as an array
// element of GET /api/v1/clusters/:id/health/nodes (HTTP 200 only). Mirrors
// backend k8s.NodeStatus. Populated by Client.GetClusterNodes.
//
// Field semantics:
//   - Name, Status, Capacity, Allocatable, PodCount — always populated; Status
//     is "Ready" or "NotReady".
//   - Conditions — omitted when the backend has no conditions to report
//     (rare); typically a non-empty slice on Linux nodes.
type ClusterNodeStatus struct {
	Name        string                  `json:"name" yaml:"name"`
	Status      string                  `json:"status" yaml:"status"`
	Conditions  []ClusterNodeCondition  `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Capacity    ClusterResourceQuantity `json:"capacity" yaml:"capacity"`
	Allocatable ClusterResourceQuantity `json:"allocatable" yaml:"allocatable"`
	PodCount    int                     `json:"pod_count" yaml:"pod_count"`
}

// ClusterNamespace is one namespace entry returned as an array element of
// GET /api/v1/clusters/:id/namespaces (HTTP 200 only). Mirrors backend
// k8s.NamespaceInfo (filtered to "stack-*" namespaces). Populated by
// Client.GetClusterNamespaces.
//
// Field semantics:
//   - Name, Phase — always populated. Phase is the string form of
//     corev1.NamespacePhase ("Active" or "Terminating").
//   - CreatedAt — populated from the namespace's metadata.creationTimestamp;
//     a nil pointer when the backend omitted the field (rare; treated as
//     "unknown age" by the CLI).
type ClusterNamespace struct {
	Name      string     `json:"name" yaml:"name"`
	Phase     string     `json:"phase" yaml:"phase"`
	CreatedAt *time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`
}

// NamespaceResourceUsage is the per-namespace utilization entry embedded
// inside ClusterUtilization.Namespaces. Mirrors backend NamespaceResourceUsage.
//
// Field semantics:
//   - Namespace, PodCount, PodLimit — always present (PodLimit is 0 when the
//     namespace has no quota; CLI renders it as just PodCount in that case).
//   - CPUUsed/CPULimit/MemoryUsed/MemoryLimit — populated when the backend's
//     metrics client successfully fetched usage/limits; empty when metrics
//     were unavailable for that namespace (still returned by the API for
//     completeness — hence `omitempty`).
type NamespaceResourceUsage struct {
	Namespace   string `json:"namespace" yaml:"namespace"`
	CPUUsed     string `json:"cpu_used,omitempty" yaml:"cpu_used,omitempty"`
	CPULimit    string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemoryUsed  string `json:"memory_used,omitempty" yaml:"memory_used,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
	PodCount    int    `json:"pod_count" yaml:"pod_count"`
	PodLimit    int    `json:"pod_limit" yaml:"pod_limit"`
}

// ClusterQuota is the success-path response of
// GET /api/v1/clusters/:id/quotas and the response of
// PUT /api/v1/clusters/:id/quotas after upsert. Mirrors the backend
// models.ResourceQuotaConfig struct. Populated by Client.GetClusterQuota /
// Client.SetClusterQuota.
//
// Embeds Base per the repo convention for resource entities. The backend's
// ResourceQuotaConfig does not include a Version field, so the embedded
// Base.Version is always the zero string on the read path.
//
// The backend returns 404 when no quota is set for the cluster — the client
// surfaces that as an APIError with StatusCode 404, and the CLI distinguishes
// "no quota configured" from a missing cluster by checking that error.
//
// Field semantics:
//   - Base.ID, ClusterID, Base.CreatedAt, Base.UpdatedAt — always populated on
//     the 200 path (server-owned; ignored if sent in a PUT body).
//   - Base.Version, Base.DeletedAt — not used by this resource; left as zero
//     values for convention compliance.
//   - CPURequest, CPULimit, MemoryRequest, MemoryLimit, StorageLimit — empty
//     string when that dimension has no limit set. Kubernetes resource notation
//     when populated ("2", "500m", "4Gi").
//   - PodLimit — 0 means "no pod-count limit"; positive integers cap namespace
//     pod count.
type ClusterQuota struct {
	Base
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	CPURequest    string `json:"cpu_request,omitempty" yaml:"cpu_request,omitempty"`
	CPULimit      string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemoryRequest string `json:"memory_request,omitempty" yaml:"memory_request,omitempty"`
	MemoryLimit   string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
	StorageLimit  string `json:"storage_limit,omitempty" yaml:"storage_limit,omitempty"`
	PodLimit      int    `json:"pod_limit" yaml:"pod_limit"`
}

// SetClusterQuotaRequest is the body for PUT /api/v1/clusters/:id/quotas
// (admin-only). Mirrors the backend handlers.UpdateQuotaRequest. Used by
// Client.SetClusterQuota.
//
// Field semantics:
//   - Any string field left empty removes that dimension's limit on upsert.
//   - PodLimit == 0 means "no pod-count limit" (not "leave unchanged").
type SetClusterQuotaRequest struct {
	CPURequest    string `json:"cpu_request" yaml:"cpu_request"`
	CPULimit      string `json:"cpu_limit" yaml:"cpu_limit"`
	MemoryRequest string `json:"memory_request" yaml:"memory_request"`
	MemoryLimit   string `json:"memory_limit" yaml:"memory_limit"`
	StorageLimit  string `json:"storage_limit" yaml:"storage_limit"`
	PodLimit      int    `json:"pod_limit" yaml:"pod_limit"`
}

// ClusterUtilization is the success-path response of
// GET /api/v1/clusters/:id/utilization (HTTP 200 only). Mirrors backend
// ClusterUtilization. Populated by Client.GetClusterUtilization.
//
// Field semantics:
//   - ClusterID — always present, echoes the path parameter.
//   - Namespaces — always present (empty slice when no stack-* namespaces
//     exist on the cluster).
type ClusterUtilization struct {
	ClusterID  string                   `json:"cluster_id" yaml:"cluster_id"`
	Namespaces []NamespaceResourceUsage `json:"namespaces" yaml:"namespaces"`
}

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse represents a health check response.
type HealthResponse struct {
	Status string `json:"status"`
}

// InstanceStatus is the Kubernetes status of a stack instance, returned by
// GET /api/v1/stack-instances/:id/status. Mirrors backend k8s.NamespaceStatus:
// the pods are nested per chart under Charts. Every backend field is
// modelled so -o json / -o yaml pass the API shape through unchanged.
type InstanceStatus struct {
	LastChecked time.Time     `json:"last_checked" yaml:"last_checked"`
	Namespace   string        `json:"namespace" yaml:"namespace"`
	Status      string        `json:"status" yaml:"status"`
	Charts      []ChartStatus `json:"charts" yaml:"charts"`
	Ingresses   []IngressInfo `json:"ingresses,omitempty" yaml:"ingresses,omitempty"`
	Events      []PodEvent    `json:"events,omitempty" yaml:"events,omitempty"`
}

// ChartStatus is the status of one Helm release (k8s.ChartStatus).
type ChartStatus struct {
	ReleaseName string           `json:"release_name" yaml:"release_name"`
	ChartName   string           `json:"chart_name" yaml:"chart_name"`
	Status      string           `json:"status" yaml:"status"`
	Deployments []DeploymentInfo `json:"deployments" yaml:"deployments"`
	Pods        []PodStatus      `json:"pods" yaml:"pods"`
	Services    []ServiceInfo    `json:"services" yaml:"services"`
}

// PodStatus is the status of one Kubernetes pod (k8s.PodInfo).
type PodStatus struct {
	StartTime       *time.Time           `json:"start_time,omitempty" yaml:"start_time,omitempty"`
	ContainerStates []ContainerStateInfo `json:"container_states" yaml:"container_states"`
	Conditions      []PodConditionInfo   `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Name            string               `json:"name" yaml:"name"`
	Phase           string               `json:"phase" yaml:"phase"`
	Image           string               `json:"image" yaml:"image"`
	NodeName        string               `json:"node_name,omitempty" yaml:"node_name,omitempty"`
	RestartCount    int32                `json:"restart_count" yaml:"restart_count"`
	Ready           bool                 `json:"ready" yaml:"ready"`
}

// ContainerStateInfo is the state of one container (k8s.ContainerStateInfo).
type ContainerStateInfo struct {
	ExitCode     *int32 `json:"exit_code,omitempty" yaml:"exit_code,omitempty"`
	Name         string `json:"name" yaml:"name"`
	State        string `json:"state" yaml:"state"`
	Reason       string `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message      string `json:"message,omitempty" yaml:"message,omitempty"`
	Image        string `json:"image" yaml:"image"`
	RestartCount int32  `json:"restart_count" yaml:"restart_count"`
	Ready        bool   `json:"ready" yaml:"ready"`
}

// PodConditionInfo is one pod condition (k8s.PodConditionInfo).
type PodConditionInfo struct {
	Type    string `json:"type" yaml:"type"`
	Status  string `json:"status" yaml:"status"`
	Reason  string `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

// DeploymentInfo is the status of one Deployment (k8s.DeploymentInfo).
type DeploymentInfo struct {
	Name            string `json:"name" yaml:"name"`
	ReadyReplicas   int32  `json:"ready_replicas" yaml:"ready_replicas"`
	DesiredReplicas int32  `json:"desired_replicas" yaml:"desired_replicas"`
	UpdatedReplicas int32  `json:"updated_replicas" yaml:"updated_replicas"`
	Available       bool   `json:"available" yaml:"available"`
}

// ServiceInfo is one Kubernetes Service (k8s.ServiceInfo).
type ServiceInfo struct {
	Name         string   `json:"name" yaml:"name"`
	Type         string   `json:"type" yaml:"type"`
	ClusterIP    string   `json:"cluster_ip" yaml:"cluster_ip"`
	ExternalIP   string   `json:"external_ip,omitempty" yaml:"external_ip,omitempty"`
	Ports        []string `json:"ports,omitempty" yaml:"ports,omitempty"`
	NodePorts    []int32  `json:"node_ports,omitempty" yaml:"node_ports,omitempty"`
	IngressHosts []string `json:"ingress_hosts,omitempty" yaml:"ingress_hosts,omitempty"`
}

// IngressInfo is one access URL of the namespace (k8s.IngressInfo).
type IngressInfo struct {
	Name string `json:"name" yaml:"name"`
	Host string `json:"host" yaml:"host"`
	Path string `json:"path" yaml:"path"`
	URL  string `json:"url" yaml:"url"`
	TLS  bool   `json:"tls" yaml:"tls"`
}

// PodEvent is one Kubernetes event (k8s.PodEvent).
type PodEvent struct {
	FirstSeen time.Time `json:"first_seen" yaml:"first_seen"`
	LastSeen  time.Time `json:"last_seen" yaml:"last_seen"`
	Type      string    `json:"type" yaml:"type"`
	Reason    string    `json:"reason" yaml:"reason"`
	Message   string    `json:"message" yaml:"message"`
	Object    string    `json:"object" yaml:"object"`
	Count     int32     `json:"count" yaml:"count"`
}

// QuotaOverride is the per-instance resource quota override. Mirrors
// backend models.InstanceQuotaOverride (/api/v1/stack-instances/:id/quota-overrides).
type QuotaOverride struct {
	CreatedAt       time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" yaml:"updated_at"`
	ID              string    `json:"id" yaml:"id"`
	StackInstanceID string    `json:"stack_instance_id" yaml:"stack_instance_id"`
	CPURequest      string    `json:"cpu_request,omitempty" yaml:"cpu_request,omitempty"`
	CPULimit        string    `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemRequest      string    `json:"memory_request,omitempty" yaml:"memory_request,omitempty"`
	MemLimit        string    `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
	StorageLimit    string    `json:"storage_limit,omitempty" yaml:"storage_limit,omitempty"`
	PodLimit        *int      `json:"pod_limit,omitempty" yaml:"pod_limit,omitempty"`
}

// SetValueOverrideRequest is the request body for setting value overrides.
// The backend expects Values as a YAML string, not a structured map.
type SetValueOverrideRequest struct {
	Values string `json:"values"`
}

// SetBranchOverrideRequest is the request body for setting a branch override.
type SetBranchOverrideRequest struct {
	Branch string `json:"branch"`
}

// SetQuotaOverrideRequest is the request body of
// PUT /api/v1/stack-instances/:id/quota-overrides (backend
// handlers.setQuotaOverrideRequest). The backend replaces the whole
// override: a field that is not sent is cleared.
type SetQuotaOverrideRequest struct {
	CPURequest   string `json:"cpu_request,omitempty" yaml:"cpu_request,omitempty"`
	CPULimit     string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemRequest   string `json:"memory_request,omitempty" yaml:"memory_request,omitempty"`
	MemLimit     string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
	StorageLimit string `json:"storage_limit,omitempty" yaml:"storage_limit,omitempty"`
	PodLimit     *int   `json:"pod_limit,omitempty" yaml:"pod_limit,omitempty"`
}

// SharedValues represents cluster-level shared Helm values.
type SharedValues struct {
	Base
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
	Name      string `json:"name" yaml:"name"`
	Values    string `json:"values" yaml:"values"`
	Priority  int    `json:"priority" yaml:"priority"`
}

// SetSharedValuesRequest is the request body for creating/updating shared values.
type SetSharedValuesRequest struct {
	Name     string `json:"name"`
	Values   string `json:"values"`
	Priority int    `json:"priority,omitempty"`
}

// OIDCConfig represents the OIDC configuration from the server.
type OIDCConfig struct {
	Enabled          bool   `json:"enabled"`
	ProviderName     string `json:"provider_name"`
	LocalAuthEnabled bool   `json:"local_auth_enabled"`
}

// CLIAuthResponse is returned by POST /api/v1/auth/oidc/cli-auth.
type CLIAuthResponse struct {
	SessionID string `json:"session_id"`
	LoginURL  string `json:"login_url"`
	ExpiresIn int    `json:"expires_in"`
}

// CLITokenRequest is the request body for POST /api/v1/auth/oidc/cli-token.
type CLITokenRequest struct {
	SessionID string `json:"session_id"`
}

// CLITokenResponse is returned by POST /api/v1/auth/oidc/cli-token.
type CLITokenResponse struct {
	Status   string `json:"status"` // "pending" or "completed"
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	UserID   string `json:"user_id,omitempty"`
}

// WSMessage is the envelope for all WebSocket messages.
type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"payload"`
}

// WSDeploymentLog is the payload for "deployment.log" WebSocket messages.
type WSDeploymentLog struct {
	InstanceID string `json:"instance_id"`
	LogID      string `json:"log_id"`
	Line       string `json:"line"`
}

// WSDeploymentStatus is the payload for "deployment.status" WebSocket messages.
type WSDeploymentStatus struct {
	InstanceID   string `json:"instance_id"`
	Status       string `json:"status"`
	LogID        string `json:"log_id"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// StreamResult is the outcome of a WebSocket log-streaming session.
type StreamResult struct {
	Status       string
	ErrorMessage string
}

// WatchEvent is one render-ready event emitted by Client.WatchEvents.
// Wraps the backend's "deployment.status" payload with a client-side
// receipt timestamp so the CLI can show "when did this arrive" without
// the backend needing to emit one.
//
// Population: every event sent on the channel is fully populated.
// Timestamp is the client-side time.Now() at receipt — the backend
// payload (gitprovider.ProviderStatus / deploymentStatusPayload) does
// not carry one. Type is the WS envelope type ("deployment.status").
//
// Field declaration order is alphabetical by json tag (golden-file
// contract — required for stable JSON output in tests).
type WatchEvent struct {
	ErrorMessage string    `json:"error_message,omitempty" yaml:"error_message,omitempty"`
	InstanceID   string    `json:"instance_id" yaml:"instance_id"`
	LogID        string    `json:"log_id,omitempty" yaml:"log_id,omitempty"`
	Status       string    `json:"status" yaml:"status"`
	Timestamp    time.Time `json:"timestamp" yaml:"timestamp"`
	Type         string    `json:"type" yaml:"type"`
}

// CompareResult is the response of GET /api/v1/stack-instances/compare.
// Mirrors backend handlers.CompareInstancesResponse.
type CompareResult struct {
	Left   CompareInstanceSummary `json:"left" yaml:"left"`
	Right  CompareInstanceSummary `json:"right" yaml:"right"`
	Charts []CompareChartDiff     `json:"charts" yaml:"charts"`
}

// CompareInstanceSummary is one side of a comparison. Owner is the
// owner's username.
type CompareInstanceSummary struct {
	ID             string `json:"id" yaml:"id"`
	Name           string `json:"name" yaml:"name"`
	DefinitionName string `json:"definition_name" yaml:"definition_name"`
	Branch         string `json:"branch" yaml:"branch"`
	Owner          string `json:"owner" yaml:"owner"`
}

// CompareChartDiff holds the merged values of one chart on both sides.
// LeftValues or RightValues is nil when the chart exists on one side only.
type CompareChartDiff struct {
	ChartName      string  `json:"chart_name" yaml:"chart_name"`
	LeftValues     *string `json:"left_values" yaml:"left_values"`
	RightValues    *string `json:"right_values" yaml:"right_values"`
	HasDifferences bool    `json:"has_differences" yaml:"has_differences"`
}

// CreateTemplateRequest is the request body for POST /api/v1/templates.
type CreateTemplateRequest struct {
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	Category      string        `json:"category,omitempty" yaml:"category,omitempty"`
	Version       string        `json:"version,omitempty" yaml:"version,omitempty"`
	DefaultBranch string        `json:"default_branch,omitempty" yaml:"default_branch,omitempty"`
	Charts        []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// UpdateTemplateRequest is the request body for PUT /api/v1/templates/:id.
//
// Servers before k8s-stack-manager v0.6.0 replace name, description,
// category, version and default_branch with the request values, so an
// absent field becomes empty. v0.6.0+ keeps the fields that are not sent.
// To change one field on every server version, send the full record (see
// `template update`, which reads the template first). Empty fields are
// omitted, so a field cannot be cleared; the server ignores Charts.
type UpdateTemplateRequest struct {
	Name          string        `json:"name,omitempty" yaml:"name,omitempty"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	Category      string        `json:"category,omitempty" yaml:"category,omitempty"`
	Version       string        `json:"version,omitempty" yaml:"version,omitempty"`
	DefaultBranch string        `json:"default_branch,omitempty" yaml:"default_branch,omitempty"`
	Charts        []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// PatchTemplateRequest is a partial PUT /api/v1/templates/:id body for
// k8s-stack-manager v0.6.0+: the server changes only the fields that are
// sent (non-nil), and an empty string clears a field. Older servers replace
// every field; use UpdateTemplateRequest with the full record for them.
type PatchTemplateRequest struct {
	Name          *string `json:"name,omitempty" yaml:"name,omitempty"`
	Description   *string `json:"description,omitempty" yaml:"description,omitempty"`
	Category      *string `json:"category,omitempty" yaml:"category,omitempty"`
	Version       *string `json:"version,omitempty" yaml:"version,omitempty"`
	DefaultBranch *string `json:"default_branch,omitempty" yaml:"default_branch,omitempty"`
}

// CloneTemplateRequest is the request body for POST /api/v1/templates/:id/clone.
type CloneTemplateRequest struct {
	Name string `json:"name" yaml:"name"`
}

// TemplateVersion represents a version snapshot entry in the template history.
type TemplateVersion struct {
	ID            string `json:"id" yaml:"id"`
	TemplateID    string `json:"template_id" yaml:"template_id"`
	Version       string `json:"version" yaml:"version"`
	ChangeSummary string `json:"change_summary" yaml:"change_summary"`
	CreatedBy     string `json:"created_by" yaml:"created_by"`
	// CreatedByUsername is set by k8s-stack-manager v0.6.0+ (empty on older servers).
	CreatedByUsername string    `json:"created_by_username,omitempty" yaml:"created_by_username,omitempty"`
	CreatedAt         time.Time `json:"created_at" yaml:"created_at"`
}

// CreatedByName returns the username of the creator, or the user ID when
// the server does not send the username.
func (v TemplateVersion) CreatedByName() string {
	if v.CreatedByUsername != "" {
		return v.CreatedByUsername
	}
	return v.CreatedBy
}

// TemplateVersionDetail is the full version response including the parsed snapshot.
type TemplateVersionDetail struct {
	TemplateVersion
	Snapshot TemplateSnapshot `json:"snapshot" yaml:"snapshot"`
}

// TemplateSnapshot is the state of a template captured at publish time.
type TemplateSnapshot struct {
	SchemaVersion int                         `json:"schema_version,omitempty" yaml:"schema_version,omitempty"`
	Template      TemplateSnapshotData        `json:"template" yaml:"template"`
	Charts        []TemplateChartSnapshotData `json:"charts" yaml:"charts"`
}

// TemplateSnapshotData holds the template fields in a snapshot.
type TemplateSnapshotData struct {
	Name          string `json:"name" yaml:"name"`
	Description   string `json:"description,omitempty" yaml:"description,omitempty"`
	Category      string `json:"category,omitempty" yaml:"category,omitempty"`
	DefaultBranch string `json:"default_branch" yaml:"default_branch"`
	IsPublished   bool   `json:"is_published" yaml:"is_published"`
	Version       string `json:"version" yaml:"version"`
}

// TemplateChartSnapshotData holds chart config fields in a snapshot.
type TemplateChartSnapshotData struct {
	ChartName     string `json:"chart_name" yaml:"chart_name"`
	RepoURL       string `json:"repo_url" yaml:"repo_url"`
	DefaultValues string `json:"default_values,omitempty" yaml:"default_values,omitempty"`
	LockedValues  string `json:"locked_values,omitempty" yaml:"locked_values,omitempty"`
	IsRequired    bool   `json:"is_required" yaml:"is_required"`
	SortOrder     int    `json:"sort_order" yaml:"sort_order"`
	// Fields below exist from snapshot schema version 1.
	ID              string `json:"id,omitempty" yaml:"id,omitempty"`
	SourceRepoURL   string `json:"source_repo_url,omitempty" yaml:"source_repo_url,omitempty"`
	BuildPipelineID string `json:"build_pipeline_id,omitempty" yaml:"build_pipeline_id,omitempty"`
	ChartPath       string `json:"chart_path,omitempty" yaml:"chart_path,omitempty"`
	ChartVersion    string `json:"chart_version,omitempty" yaml:"chart_version,omitempty"`
}

// TemplateVersionDiff is the response from the version diff endpoint.
type TemplateVersionDiff struct {
	Left       TemplateVersionSide `json:"left" yaml:"left"`
	Right      TemplateVersionSide `json:"right" yaml:"right"`
	ChartDiffs []ChartDiffEntry    `json:"chart_diffs" yaml:"chart_diffs"`
}

// TemplateVersionSide is one side of a version diff. Version is the version
// string. The other metadata fields are set by k8s-stack-manager v0.6.0+.
// For the working copy (query value "working") ID is "working" and
// IsWorkingCopy is true.
type TemplateVersionSide struct {
	ID                string           `json:"id,omitempty" yaml:"id,omitempty"`
	Version           string           `json:"version" yaml:"version"`
	ChangeSummary     string           `json:"change_summary,omitempty" yaml:"change_summary,omitempty"`
	CreatedBy         string           `json:"created_by,omitempty" yaml:"created_by,omitempty"`
	CreatedByUsername string           `json:"created_by_username,omitempty" yaml:"created_by_username,omitempty"`
	CreatedAt         *time.Time       `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	IsWorkingCopy     bool             `json:"is_working_copy,omitempty" yaml:"is_working_copy,omitempty"`
	Snapshot          TemplateSnapshot `json:"snapshot" yaml:"snapshot"`
}

// TemplateVersionWorkingCopy is the version ID that selects the working copy
// (draft) of a template in the version diff endpoint (v0.6.0+).
const TemplateVersionWorkingCopy = "working"

// CleanupPolicy is the success-path response of
// GET/POST/PUT /api/v1/admin/cleanup-policies. Mirrors the backend
// models.CleanupPolicy wire format.
//
// Note: the backend model has no Version/DeletedAt columns, so those embedded
// Base fields stay zero on the read path. ID/CreatedAt/UpdatedAt are populated.
//
// Field semantics:
//   - ClusterID — UUID of a target cluster, or the literal string "all" to apply
//     the policy across every cluster.
//   - Action — one of "stop", "clean", "delete" (what to do with matching
//     instances).
//   - Condition — DSL string evaluated by the scheduler, e.g. "idle_days:7",
//     "status:stopped,age_days:14", or "ttl_expired".
//   - Schedule — standard 5-field cron expression, e.g. "0 2 * * *".
//   - Enabled — false pauses the policy without deleting it.
//   - DryRun — when true the scheduler logs matches but takes no action.
//   - LastRunAt — nil until the policy has been executed (manually or by the
//     scheduler) at least once.
type CleanupPolicy struct {
	Base
	Name      string     `json:"name" yaml:"name"`
	ClusterID string     `json:"cluster_id" yaml:"cluster_id"`
	Action    string     `json:"action" yaml:"action"`
	Condition string     `json:"condition" yaml:"condition"`
	Schedule  string     `json:"schedule" yaml:"schedule"`
	Enabled   bool       `json:"enabled" yaml:"enabled"`
	DryRun    bool       `json:"dry_run" yaml:"dry_run"`
	LastRunAt *time.Time `json:"last_run_at,omitempty" yaml:"last_run_at,omitempty"`
}

// CreateCleanupPolicyRequest is the body for POST /api/v1/admin/cleanup-policies
// (admin-only). The backend uses models.CleanupPolicy as the request DTO too;
// the CLI uses a dedicated type so that the read-path Base fields aren't
// echoed back as zero values on create.
//
// Field semantics match CleanupPolicy. ID/CreatedAt/UpdatedAt are populated by
// the server; do not set them on the request.
type CreateCleanupPolicyRequest struct {
	Name      string `json:"name" yaml:"name"`
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
	Action    string `json:"action" yaml:"action"`
	Condition string `json:"condition" yaml:"condition"`
	Schedule  string `json:"schedule" yaml:"schedule"`
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	DryRun    bool   `json:"dry_run" yaml:"dry_run"`
}

// CleanupResult is one entry in the response of
// POST /api/v1/admin/cleanup-policies/:id/run. Mirrors backend
// scheduler.CleanupResult.
//
// Population: only returned on HTTP 200. On non-2xx the client surfaces an
// APIError and the result slice is left nil by the caller.
//
// Field semantics:
//   - Status — "success" (action applied), "error" (action attempted and
//     failed), or "dry_run" (matched, no action taken). Callers should treat
//     "dry_run" as informational and "error" as the partial-failure signal.
//   - Error — populated only when Status == "error"; carries the per-instance
//     failure reason.
type CleanupResult struct {
	InstanceID   string `json:"instance_id" yaml:"instance_id"`
	InstanceName string `json:"instance_name" yaml:"instance_name"`
	Namespace    string `json:"namespace" yaml:"namespace"`
	OwnerID      string `json:"owner_id" yaml:"owner_id"`
	Action       string `json:"action" yaml:"action"`
	Status       string `json:"status" yaml:"status"`
	Error        string `json:"error,omitempty" yaml:"error,omitempty"`
}

// UpdateCleanupPolicyRequest is the body for PUT /api/v1/admin/cleanup-policies/:id
// (admin-only). Same shape as CreateCleanupPolicyRequest: PUT is a full
// upsert, so all fields must be provided.
type UpdateCleanupPolicyRequest struct {
	Name      string `json:"name" yaml:"name"`
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
	Action    string `json:"action" yaml:"action"`
	Condition string `json:"condition" yaml:"condition"`
	Schedule  string `json:"schedule" yaml:"schedule"`
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	DryRun    bool   `json:"dry_run" yaml:"dry_run"`
}

// ChartDiffEntry describes chart-level differences between two template versions.
type ChartDiffEntry struct {
	ChartName      string `json:"chart_name" yaml:"chart_name"`
	ChangeType     string `json:"change_type" yaml:"change_type"`
	HasDifferences bool   `json:"has_differences" yaml:"has_differences"`
	LeftValues     string `json:"left_values,omitempty" yaml:"left_values,omitempty"`
	RightValues    string `json:"right_values,omitempty" yaml:"right_values,omitempty"`
	LeftRepoURL    string `json:"left_repo_url,omitempty" yaml:"left_repo_url,omitempty"`
	RightRepoURL   string `json:"right_repo_url,omitempty" yaml:"right_repo_url,omitempty"`
	LeftLocked     string `json:"left_locked,omitempty" yaml:"left_locked,omitempty"`
	RightLocked    string `json:"right_locked,omitempty" yaml:"right_locked,omitempty"`
	LeftRequired   bool   `json:"left_required" yaml:"left_required"`
	RightRequired  bool   `json:"right_required" yaml:"right_required"`
	LeftSortOrder  int    `json:"left_sort_order" yaml:"left_sort_order"`
	RightSortOrder int    `json:"right_sort_order" yaml:"right_sort_order"`
	// Set by k8s-stack-manager v0.6.0+ when the side has the field
	// (snapshot schema version 1 or the working copy).
	LeftChartVersion  string `json:"left_chart_version,omitempty" yaml:"left_chart_version,omitempty"`
	RightChartVersion string `json:"right_chart_version,omitempty" yaml:"right_chart_version,omitempty"`
	LeftChartPath     string `json:"left_chart_path,omitempty" yaml:"left_chart_path,omitempty"`
	RightChartPath    string `json:"right_chart_path,omitempty" yaml:"right_chart_path,omitempty"`
}
