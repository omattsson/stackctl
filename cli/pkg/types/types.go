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
type StackInstance struct {
	Base
	Name              string     `json:"name" yaml:"name"`
	StackDefinitionID string     `json:"stack_definition_id" yaml:"stack_definition_id"`
	DefinitionName    string     `json:"definition_name,omitempty" yaml:"definition_name,omitempty"`
	Owner             string     `json:"owner_id" yaml:"owner_id"`
	Branch            string     `json:"branch" yaml:"branch"`
	Namespace         string     `json:"namespace" yaml:"namespace"`
	Status            string     `json:"status" yaml:"status"`
	ClusterID         *string    `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
	ClusterName       string     `json:"cluster_name,omitempty" yaml:"cluster_name,omitempty"`
	TTLMinutes        int        `json:"ttl_minutes,omitempty" yaml:"ttl_minutes,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	DeployedAt        *time.Time `json:"last_deployed_at,omitempty" yaml:"last_deployed_at,omitempty"`
}

// StackDefinition represents a stack definition with its chart configurations.
type StackDefinition struct {
	Base
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultBranch string        `json:"default_branch" yaml:"default_branch"`
	Owner         string        `json:"owner_id" yaml:"owner_id"`
	Charts        []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// StackTemplate represents a reusable stack template.
type StackTemplate struct {
	Base
	Name            string        `json:"name" yaml:"name"`
	Description     string        `json:"description,omitempty" yaml:"description,omitempty"`
	Published       bool          `json:"is_published" yaml:"is_published"`
	Owner           string        `json:"owner_id" yaml:"owner_id"`
	Charts          []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
	DefinitionCount int           `json:"definition_count,omitempty" yaml:"definition_count,omitempty"`
}

// ChartConfig represents a Helm chart configuration within a definition or template.
type ChartConfig struct {
	Base
	Name          string `json:"name" yaml:"name"`
	RepoURL       string `json:"repository_url" yaml:"repository_url"`
	SourceRepoURL string `json:"source_repo_url,omitempty" yaml:"source_repo_url,omitempty"`
	ChartName     string `json:"chart_name" yaml:"chart_name"`
	ChartVersion  string `json:"chart_version,omitempty" yaml:"chart_version,omitempty"`
	ReleaseName   string `json:"release_name,omitempty" yaml:"release_name,omitempty"`
	DefaultValues string `json:"default_values,omitempty" yaml:"default_values,omitempty"`
}

// Cluster represents a registered Kubernetes cluster.
type Cluster struct {
	Base
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Status      string `json:"status" yaml:"status"`
	IsDefault   bool   `json:"is_default" yaml:"is_default"`
	NodeCount   int    `json:"node_count,omitempty" yaml:"node_count,omitempty"`
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
	ImagePullSecretName *string `json:"image_pull_secret_name,omitempty" yaml:"image_pull_secret_name,omitempty"`
}

// User represents a user account.
type User struct {
	Base
	Username string `json:"username" yaml:"username"`
	Role     string `json:"role" yaml:"role"`
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
type RollbackResponse struct {
	LogID   string `json:"log_id" yaml:"log_id"`
	Message string `json:"message" yaml:"message"`
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

// BulkOperationResult represents the result of a bulk operation.
type BulkOperationResult struct {
	ID      string `json:"id" yaml:"id"`
	Success bool   `json:"success" yaml:"success"`
	Error   string `json:"error,omitempty" yaml:"error,omitempty"`
}

// BulkResponse wraps bulk operation results.
type BulkResponse struct {
	Results []BulkOperationResult `json:"results" yaml:"results"`
}

// ValueOverride represents a per-chart value override.
type ValueOverride struct {
	Base
	InstanceID string `json:"instance_id" yaml:"instance_id"`
	ChartID    string `json:"chart_id" yaml:"chart_id"`
	Values     string `json:"values" yaml:"values"`
}

// BranchOverride represents a per-chart branch override.
type BranchOverride struct {
	Base
	InstanceID string `json:"instance_id" yaml:"instance_id"`
	ChartID    string `json:"chart_id" yaml:"chart_id"`
	Branch     string `json:"branch" yaml:"branch"`
}

// GitBranch represents a branch from the git provider.
type GitBranch struct {
	Name   string `json:"name" yaml:"name"`
	IsHead bool   `json:"is_head,omitempty" yaml:"is_head,omitempty"`
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
type UpdateChartConfigRequest struct {
	ChartName     string `json:"chart_name,omitempty" yaml:"chart_name,omitempty"`
	ChartPath     string `json:"chart_path,omitempty" yaml:"chart_path,omitempty"`
	ChartVersion  string `json:"chart_version,omitempty" yaml:"chart_version,omitempty"`
	SourceRepoURL string `json:"source_repo_url,omitempty" yaml:"source_repo_url,omitempty"`
	DeployOrder   *int   `json:"deploy_order,omitempty" yaml:"deploy_order,omitempty"`
	DefaultValues string `json:"default_values,omitempty" yaml:"default_values,omitempty"`
}

// OrphanedNamespace represents a Kubernetes namespace with no matching stack record.
type OrphanedNamespace struct {
	Namespace string `json:"namespace" yaml:"namespace"`
	Cluster   string `json:"cluster,omitempty" yaml:"cluster,omitempty"`
	CreatedAt string `json:"created_at,omitempty" yaml:"created_at,omitempty"`
}

// BulkRequest is the request body for bulk operations.
type BulkRequest struct {
	IDs []string `json:"ids" yaml:"ids"`
}

// GitValidateResponse represents the result of branch validation.
type GitValidateResponse struct {
	Valid   bool   `json:"valid" yaml:"valid"`
	Branch  string `json:"branch" yaml:"branch"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

// ClusterHealthSummary represents a cluster's health summary from GET /api/v1/clusters/:id/health/summary.
type ClusterHealthSummary struct {
	TotalCPU          string `json:"total_cpu" yaml:"total_cpu"`
	TotalMemory       string `json:"total_memory" yaml:"total_memory"`
	AllocatableCPU    string `json:"allocatable_cpu" yaml:"allocatable_cpu"`
	AllocatableMemory string `json:"allocatable_memory" yaml:"allocatable_memory"`
	NodeCount         int    `json:"node_count" yaml:"node_count"`
	ReadyNodeCount    int    `json:"ready_node_count" yaml:"ready_node_count"`
	NamespaceCount    int    `json:"namespace_count" yaml:"namespace_count"`
}

// NodeCondition represents a single node condition.
type NodeCondition struct {
	Type    string `json:"type" yaml:"type"`
	Status  string `json:"status" yaml:"status"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

// ResourceQuantity holds CPU, memory, and optional pod capacity values as strings.
type ResourceQuantity struct {
	CPU    string `json:"cpu" yaml:"cpu"`
	Memory string `json:"memory" yaml:"memory"`
	Pods   string `json:"pods,omitempty" yaml:"pods,omitempty"`
}

// ClusterNode represents the health and capacity of a single cluster node.
type ClusterNode struct {
	Conditions  []NodeCondition  `json:"conditions" yaml:"conditions"`
	Capacity    ResourceQuantity `json:"capacity" yaml:"capacity"`
	Allocatable ResourceQuantity `json:"allocatable" yaml:"allocatable"`
	Name        string           `json:"name" yaml:"name"`
	Status      string           `json:"status" yaml:"status"`
	PodCount    int              `json:"pod_count" yaml:"pod_count"`
}

// ClusterNamespace represents a Kubernetes namespace in a cluster.
type ClusterNamespace struct {
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
	Name      string    `json:"name" yaml:"name"`
	Phase     string    `json:"phase" yaml:"phase"`
}

// NamespaceResourceUsage holds resource usage for a single namespace.
type NamespaceResourceUsage struct {
	Namespace   string `json:"namespace" yaml:"namespace"`
	CPUUsed     string `json:"cpu_used" yaml:"cpu_used"`
	CPULimit    string `json:"cpu_limit" yaml:"cpu_limit"`
	MemoryUsed  string `json:"memory_used" yaml:"memory_used"`
	MemoryLimit string `json:"memory_limit" yaml:"memory_limit"`
	PodCount    int    `json:"pod_count" yaml:"pod_count"`
	PodLimit    int    `json:"pod_limit" yaml:"pod_limit"`
}

// ClusterUtilization represents per-namespace resource usage for a cluster.
type ClusterUtilization struct {
	ClusterID  string                   `json:"cluster_id" yaml:"cluster_id"`
	Namespaces []NamespaceResourceUsage `json:"namespaces" yaml:"namespaces"`
}

// TestConnectionResponse is the response from POST /api/v1/clusters/:id/test.
type TestConnectionResponse struct {
	Status        string `json:"status" yaml:"status"`
	Message       string `json:"message" yaml:"message"`
	ServerVersion string `json:"server_version,omitempty" yaml:"server_version,omitempty"`
}

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse represents a health check response.
type HealthResponse struct {
	Status string `json:"status"`
}

// PodStatus represents the status of a Kubernetes pod.
type PodStatus struct {
	Name     string `json:"name" yaml:"name"`
	Status   string `json:"status" yaml:"status"`
	Ready    bool   `json:"ready" yaml:"ready"`
	Restarts int    `json:"restarts" yaml:"restarts"`
	Age      string `json:"age,omitempty" yaml:"age,omitempty"`
}

// InstanceStatus represents the full status of a stack instance.
type InstanceStatus struct {
	Status string      `json:"status"`
	Pods   []PodStatus `json:"pods,omitempty"`
}

// QuotaOverride represents a per-instance resource quota override.
// Unlike other override types, the API returns quota overrides without standard Base fields (ID, Version).
type QuotaOverride struct {
	InstanceID string    `json:"instance_id" yaml:"instance_id"`
	CPURequest string    `json:"cpu_request,omitempty" yaml:"cpu_request,omitempty"`
	CPULimit   string    `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemRequest string    `json:"memory_request,omitempty" yaml:"memory_request,omitempty"`
	MemLimit   string    `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
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

// SetQuotaOverrideRequest is the request body for setting quota overrides.
type SetQuotaOverrideRequest struct {
	CPURequest string `json:"cpu_request,omitempty" yaml:"cpu_request,omitempty"`
	CPULimit   string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemRequest string `json:"memory_request,omitempty" yaml:"memory_request,omitempty"`
	MemLimit   string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
}

// MergedValues represents the merged Helm values for an instance.
type MergedValues struct {
	InstanceID string                            `json:"instance_id" yaml:"instance_id"`
	Charts     map[string]map[string]interface{} `json:"charts" yaml:"charts"`
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

// CompareResult represents the comparison between two stack instances.
type CompareResult struct {
	Left  *StackInstance         `json:"left" yaml:"left"`
	Right *StackInstance         `json:"right" yaml:"right"`
	Diffs map[string]interface{} `json:"diffs,omitempty" yaml:"diffs,omitempty"`
}

// CreateTemplateRequest is the request body for POST /api/v1/templates.
type CreateTemplateRequest struct {
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Charts      []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// UpdateTemplateRequest is the request body for PUT /api/v1/templates/:id.
type UpdateTemplateRequest struct {
	Name        string        `json:"name,omitempty" yaml:"name,omitempty"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Charts      []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// CloneTemplateRequest is the request body for POST /api/v1/templates/:id/clone.
type CloneTemplateRequest struct {
	Name string `json:"name" yaml:"name"`
}

// TemplateVersion represents a version snapshot entry in the template history.
type TemplateVersion struct {
	ID            string    `json:"id" yaml:"id"`
	TemplateID    string    `json:"template_id" yaml:"template_id"`
	Version       string    `json:"version" yaml:"version"`
	ChangeSummary string    `json:"change_summary" yaml:"change_summary"`
	CreatedBy     string    `json:"created_by" yaml:"created_by"`
	CreatedAt     time.Time `json:"created_at" yaml:"created_at"`
}

// TemplateVersionDetail is the full version response including the parsed snapshot.
type TemplateVersionDetail struct {
	TemplateVersion
	Snapshot TemplateSnapshot `json:"snapshot" yaml:"snapshot"`
}

// TemplateSnapshot is the state of a template captured at publish time.
type TemplateSnapshot struct {
	Template TemplateSnapshotData        `json:"template" yaml:"template"`
	Charts   []TemplateChartSnapshotData `json:"charts" yaml:"charts"`
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
}

// TemplateVersionDiff is the response from the version diff endpoint.
type TemplateVersionDiff struct {
	Left       TemplateVersionSide `json:"left" yaml:"left"`
	Right      TemplateVersionSide `json:"right" yaml:"right"`
	ChartDiffs []ChartDiffEntry    `json:"chart_diffs" yaml:"chart_diffs"`
}

// TemplateVersionSide is one side of a version diff.
type TemplateVersionSide struct {
	Version  string           `json:"version" yaml:"version"`
	Snapshot TemplateSnapshot `json:"snapshot" yaml:"snapshot"`
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
}
