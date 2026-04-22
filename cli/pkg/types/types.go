package types

import "time"

// Base fields shared by all API resources.
type Base struct {
	ID        string       `json:"id" yaml:"id"`
	CreatedAt time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
	Version   string       `json:"version" yaml:"version"`
}

// StackInstance represents a deployed stack instance.
type StackInstance struct {
	Base
	Name              string     `json:"name" yaml:"name"`
	StackDefinitionID string       `json:"stack_definition_id" yaml:"stack_definition_id"`
	DefinitionName    string     `json:"definition_name,omitempty" yaml:"definition_name,omitempty"`
	Owner             string     `json:"owner_id" yaml:"owner_id"`
	Branch            string     `json:"branch" yaml:"branch"`
	Namespace         string     `json:"namespace" yaml:"namespace"`
	Status            string     `json:"status" yaml:"status"`
	ClusterID         *string      `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
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
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Published   bool          `json:"is_published" yaml:"is_published"`
	Owner       string        `json:"owner_id" yaml:"owner_id"`
	Charts          []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
	DefinitionCount int           `json:"definition_count,omitempty" yaml:"definition_count,omitempty"`
}

// ChartConfig represents a Helm chart configuration within a definition or template.
type ChartConfig struct {
	Base
	Name          string `json:"name" yaml:"name"`
	RepoURL       string `json:"repository_url" yaml:"repository_url"`
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
	StackDefinitionID string   `json:"stack_definition_id" yaml:"stack_definition_id"`
	Branch            string `json:"branch,omitempty" yaml:"branch,omitempty"`
	ClusterID         string   `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
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
	ID      string   `json:"id" yaml:"id"`
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
	InstanceID string   `json:"instance_id" yaml:"instance_id"`
	ChartID    string   `json:"chart_id" yaml:"chart_id"`
	Values     string `json:"values" yaml:"values"`
}

// BranchOverride represents a per-chart branch override.
type BranchOverride struct {
	Base
	InstanceID string   `json:"instance_id" yaml:"instance_id"`
	ChartID    string   `json:"chart_id" yaml:"chart_id"`
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
	ClusterID string   `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
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

// ClusterHealthSummary represents a cluster's health summary.
type ClusterHealthSummary struct {
	Status    string `json:"status" yaml:"status"`
	NodeCount int    `json:"node_count" yaml:"node_count"`
	CPUUsage  string `json:"cpu_usage,omitempty" yaml:"cpu_usage,omitempty"`
	MemUsage  string `json:"memory_usage,omitempty" yaml:"memory_usage,omitempty"`
	CPUTotal  string `json:"cpu_total,omitempty" yaml:"cpu_total,omitempty"`
	MemTotal  string `json:"memory_total,omitempty" yaml:"memory_total,omitempty"`
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
	InstanceID string      `json:"instance_id" yaml:"instance_id"`
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
	InstanceID string                              `json:"instance_id" yaml:"instance_id"`
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

// CompareResult represents the comparison between two stack instances.
type CompareResult struct {
	Left  *StackInstance         `json:"left" yaml:"left"`
	Right *StackInstance         `json:"right" yaml:"right"`
	Diffs map[string]interface{} `json:"diffs,omitempty" yaml:"diffs,omitempty"`
}
