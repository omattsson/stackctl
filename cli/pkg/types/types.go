package types

import "time"

// Base fields shared by all API resources.
type Base struct {
	ID        uint       `json:"id" yaml:"id"`
	CreatedAt time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
	Version   uint       `json:"version" yaml:"version"`
}

// StackInstance represents a deployed stack instance.
type StackInstance struct {
	Base
	Name              string     `json:"name" yaml:"name"`
	StackDefinitionID uint       `json:"stack_definition_id" yaml:"stack_definition_id"`
	DefinitionName    string     `json:"definition_name,omitempty" yaml:"definition_name,omitempty"`
	Owner             string     `json:"owner" yaml:"owner"`
	Branch            string     `json:"branch" yaml:"branch"`
	Namespace         string     `json:"namespace" yaml:"namespace"`
	Status            string     `json:"status" yaml:"status"`
	ClusterID         *uint      `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
	ClusterName       string     `json:"cluster_name,omitempty" yaml:"cluster_name,omitempty"`
	TTLMinutes        int        `json:"ttl_minutes,omitempty" yaml:"ttl_minutes,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	DeployedAt        *time.Time `json:"deployed_at,omitempty" yaml:"deployed_at,omitempty"`
}

// StackDefinition represents a stack definition with its chart configurations.
type StackDefinition struct {
	Base
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultBranch string        `json:"default_branch" yaml:"default_branch"`
	Owner         string        `json:"owner" yaml:"owner"`
	Charts        []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// StackTemplate represents a reusable stack template.
type StackTemplate struct {
	Base
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Published   bool          `json:"published" yaml:"published"`
	Owner       string        `json:"owner" yaml:"owner"`
	Charts      []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// ChartConfig represents a Helm chart configuration within a definition or template.
type ChartConfig struct {
	Base
	Name          string `json:"name" yaml:"name"`
	RepoURL       string `json:"repo_url" yaml:"repo_url"`
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
	StackDefinitionID uint   `json:"stack_definition_id" yaml:"stack_definition_id"`
	Branch            string `json:"branch,omitempty" yaml:"branch,omitempty"`
	ClusterID         uint   `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
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
	Base
	InstanceID uint   `json:"instance_id" yaml:"instance_id"`
	Action     string `json:"action" yaml:"action"`
	Status     string `json:"status" yaml:"status"`
	Output     string `json:"output,omitempty" yaml:"output,omitempty"`
}

// ListResponse wraps paginated API responses.
type ListResponse[T any] struct {
	Data       []T `json:"data"`
	Total      int `json:"total"`
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalPages int `json:"total_pages"`
}

// BulkOperationResult represents the result of a bulk operation.
type BulkOperationResult struct {
	ID      uint   `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BulkResponse wraps bulk operation results.
type BulkResponse struct {
	Results []BulkOperationResult `json:"results"`
}

// ValueOverride represents a per-chart value override.
type ValueOverride struct {
	Base
	InstanceID uint   `json:"instance_id" yaml:"instance_id"`
	ChartID    uint   `json:"chart_id" yaml:"chart_id"`
	Values     string `json:"values" yaml:"values"`
}

// BranchOverride represents a per-chart branch override.
type BranchOverride struct {
	Base
	InstanceID uint   `json:"instance_id" yaml:"instance_id"`
	ChartID    uint   `json:"chart_id" yaml:"chart_id"`
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
	ClusterID uint   `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
}

// QuickDeployRequest is the request body for POST /api/v1/templates/:id/quick-deploy.
type QuickDeployRequest struct {
	Name      string `json:"name" yaml:"name"`
	Branch    string `json:"branch,omitempty" yaml:"branch,omitempty"`
	ClusterID uint   `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
}

// CreateDefinitionRequest is the request body for POST /api/v1/stack-definitions.
type CreateDefinitionRequest struct {
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Charts      []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
}

// UpdateDefinitionRequest is the request body for PUT /api/v1/stack-definitions/:id.
type UpdateDefinitionRequest struct {
	Name        string        `json:"name,omitempty" yaml:"name,omitempty"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Charts      []ChartConfig `json:"charts,omitempty" yaml:"charts,omitempty"`
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
