package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
)

const defaultTimeout = 30 * time.Second

// Client is the HTTP client for the k8s-stack-manager API.
// TLS configuration (insecure mode) is handled by the caller setting
// HTTPClient.Transport before making requests.
type Client struct {
	BaseURL    string
	Token      string
	APIKey     string
	HTTPClient *http.Client
}

// New creates a new API client.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// APIError represents an error response from the API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.UserFacingError()
}

// UserFacingError returns a user-friendly error message based on the status code.
// When the server provides a message, it is appended for context.
func (e *APIError) UserFacingError() string {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return e.withServerMsg("Not authenticated. Run 'stackctl login' first.")
	case http.StatusForbidden:
		return e.withServerMsg("Permission denied.")
	case http.StatusNotFound:
		return fmt.Sprintf("Resource not found: %s", e.Message)
	case http.StatusConflict:
		return fmt.Sprintf("Conflict: %s", e.Message)
	case http.StatusTooManyRequests:
		return e.withServerMsg("Rate limited. Try again later.")
	default:
		if e.StatusCode >= 500 {
			return e.withServerMsg("Server error. Check backend logs.")
		}
		return e.Message
	}
}

// withServerMsg appends the server message (if non-empty) to a user-facing guidance string.
func (e *APIError) withServerMsg(guidance string) string {
	if e.Message != "" {
		return guidance + " (server: " + e.Message + ")"
	}
	return guidance
}

// do executes an HTTP request with auth headers and error handling.
func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	// Build URL by combining base and path. We avoid url.JoinPath because it
	// escapes query strings. Instead, parse and resolve properly.
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	ref, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("parsing path: %w", err)
	}
	u := base.ResolveReference(ref).String()

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, u, reqBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	// API key takes precedence over JWT token
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	} else if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var errResp types.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: errResp.Error}
	}

	return resp, nil
}

// doJSON executes a request and decodes the JSON response into v.
func (c *Client) doJSON(method, path string, body interface{}, v interface{}) error {
	resp, err := c.do(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			// Treat empty body as success only for 204 No Content
			if err == io.EOF && resp.StatusCode == http.StatusNoContent {
				return nil
			}
			if err == io.EOF {
				return fmt.Errorf("unexpected empty response body (status %d)", resp.StatusCode)
			}
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}

// Get performs a GET request and decodes the response.
func (c *Client) Get(path string, v interface{}) error {
	return c.doJSON(http.MethodGet, path, nil, v)
}

// Post performs a POST request and decodes the response.
func (c *Client) Post(path string, body interface{}, v interface{}) error {
	return c.doJSON(http.MethodPost, path, body, v)
}

// Put performs a PUT request and decodes the response.
func (c *Client) Put(path string, body interface{}, v interface{}) error {
	return c.doJSON(http.MethodPut, path, body, v)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string) error {
	resp, err := c.do(http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// GetWithQuery performs a GET request with query parameters.
func (c *Client) GetWithQuery(path string, params map[string]string, v interface{}) error {
	if len(params) > 0 {
		q := url.Values{}
		for k, v := range params {
			if v != "" {
				q.Set(k, v)
			}
		}
		if encoded := q.Encode(); encoded != "" {
			path = path + "?" + encoded
		}
	}
	return c.Get(path, v)
}

// Login authenticates with the given credentials. On success, c.Token is set
// to the returned JWT for subsequent requests.
func (c *Client) Login(username, password string) (*types.LoginResponse, error) {
	var resp types.LoginResponse
	err := c.Post("/api/v1/auth/login", types.LoginRequest{
		Username: username,
		Password: password,
	}, &resp)
	if err != nil {
		return nil, err
	}
	c.Token = resp.Token
	return &resp, nil
}

// Whoami returns the current authenticated user.
func (c *Client) Whoami() (*types.User, error) {
	var user types.User
	err := c.Get("/api/v1/auth/me", &user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// ListStacks returns a paginated list of stack instances, filtered by query params.
func (c *Client) ListStacks(params map[string]string) (*types.ListResponse[types.StackInstance], error) {
	var resp types.ListResponse[types.StackInstance]
	err := c.GetWithQuery("/api/v1/stack-instances", params, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetStack returns a single stack instance by ID.
func (c *Client) GetStack(id uint) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d", id), &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// CreateStack creates a new stack instance.
func (c *Client) CreateStack(req *types.CreateStackRequest) (*types.StackInstance, error) {
	var created types.StackInstance
	err := c.Post("/api/v1/stack-instances", req, &created)
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// DeleteStack deletes a stack instance by ID.
func (c *Client) DeleteStack(id uint) error {
	return c.Delete(fmt.Sprintf("/api/v1/stack-instances/%d", id))
}

// DeployStack triggers a deployment for a stack instance.
func (c *Client) DeployStack(id uint) (*types.DeploymentLog, error) {
	var log types.DeploymentLog
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%d/deploy", id), nil, &log)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// StopStack triggers a stop for a stack instance.
func (c *Client) StopStack(id uint) (*types.DeploymentLog, error) {
	var log types.DeploymentLog
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%d/stop", id), nil, &log)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// CleanStack triggers an undeploy and namespace removal for a stack instance.
func (c *Client) CleanStack(id uint) (*types.DeploymentLog, error) {
	var log types.DeploymentLog
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%d/clean", id), nil, &log)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// GetStackStatus returns the current status and pod states for a stack instance.
func (c *Client) GetStackStatus(id uint) (*types.InstanceStatus, error) {
	var status types.InstanceStatus
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/status", id), &status)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// GetStackLogs returns the latest deployment log for a stack instance.
func (c *Client) GetStackLogs(id uint) (*types.DeploymentLog, error) {
	var log types.DeploymentLog
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/deploy-log", id), &log)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// CloneStack clones a stack instance and returns the new instance.
func (c *Client) CloneStack(id uint) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%d/clone", id), nil, &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// ExtendStack extends the TTL of a stack instance by the given number of minutes.
func (c *Client) ExtendStack(id uint, minutes int) (*types.StackInstance, error) {
	var instance types.StackInstance
	body := map[string]int{"ttl_minutes": minutes}
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%d/extend", id), body, &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// ListTemplates returns a paginated list of stack templates, filtered by query params.
func (c *Client) ListTemplates(params map[string]string) (*types.ListResponse[types.StackTemplate], error) {
	var resp types.ListResponse[types.StackTemplate]
	err := c.GetWithQuery("/api/v1/templates", params, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetTemplate returns a single stack template by ID.
func (c *Client) GetTemplate(id uint) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Get(fmt.Sprintf("/api/v1/templates/%d", id), &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// InstantiateTemplate creates a new stack instance from a template.
func (c *Client) InstantiateTemplate(id uint, req *types.InstantiateTemplateRequest) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Post(fmt.Sprintf("/api/v1/templates/%d/instantiate", id), req, &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// QuickDeployTemplate creates and deploys a stack instance from a template in one step.
func (c *Client) QuickDeployTemplate(id uint, req *types.QuickDeployRequest) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Post(fmt.Sprintf("/api/v1/templates/%d/quick-deploy", id), req, &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// ListDefinitions returns a paginated list of stack definitions, filtered by query params.
func (c *Client) ListDefinitions(params map[string]string) (*types.ListResponse[types.StackDefinition], error) {
	var resp types.ListResponse[types.StackDefinition]
	err := c.GetWithQuery("/api/v1/stack-definitions", params, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDefinition returns a single stack definition by ID.
func (c *Client) GetDefinition(id uint) (*types.StackDefinition, error) {
	var def types.StackDefinition
	err := c.Get(fmt.Sprintf("/api/v1/stack-definitions/%d", id), &def)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// CreateDefinition creates a new stack definition.
func (c *Client) CreateDefinition(req *types.CreateDefinitionRequest) (*types.StackDefinition, error) {
	var def types.StackDefinition
	err := c.Post("/api/v1/stack-definitions", req, &def)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// UpdateDefinition updates an existing stack definition.
func (c *Client) UpdateDefinition(id uint, req *types.UpdateDefinitionRequest) (*types.StackDefinition, error) {
	var def types.StackDefinition
	err := c.Put(fmt.Sprintf("/api/v1/stack-definitions/%d", id), req, &def)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// DeleteDefinition deletes a stack definition by ID.
func (c *Client) DeleteDefinition(id uint) error {
	return c.Delete(fmt.Sprintf("/api/v1/stack-definitions/%d", id))
}

// ExportDefinition exports a stack definition as raw JSON bytes.
func (c *Client) ExportDefinition(id uint) ([]byte, error) {
	resp, err := c.do(http.MethodGet, fmt.Sprintf("/api/v1/stack-definitions/%d/export", id), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const maxExportSize = 10 * 1024 * 1024 // 10MB
	limitedReader := io.LimitReader(resp.Body, maxExportSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("reading export response: %w", err)
	}
	if int64(len(data)) > maxExportSize {
		return nil, fmt.Errorf("export response exceeds maximum size of %d bytes", maxExportSize)
	}
	return data, nil
}

// ImportDefinition imports a stack definition from JSON data
func (c *Client) ImportDefinition(data []byte) (*types.StackDefinition, error) {
	var def types.StackDefinition
	// The data goes through json.Marshal via do()
	resp, err := c.do(http.MethodPost, "/api/v1/stack-definitions/import", json.RawMessage(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&def); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &def, nil
}

// ListValueOverrides returns all value overrides for a stack instance.
func (c *Client) ListValueOverrides(instanceID uint) ([]types.ValueOverride, error) {
	var overrides []types.ValueOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/overrides", instanceID), &overrides)
	if err != nil {
		return nil, err
	}
	return overrides, nil
}

// GetValueOverride returns a single value override for a chart.
func (c *Client) GetValueOverride(instanceID, chartID uint) (*types.ValueOverride, error) {
	var override types.ValueOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/overrides/%d", instanceID, chartID), &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// SetValueOverride sets value overrides for a chart.
func (c *Client) SetValueOverride(instanceID, chartID uint, req *types.SetValueOverrideRequest) (*types.ValueOverride, error) {
	var override types.ValueOverride
	err := c.Put(fmt.Sprintf("/api/v1/stack-instances/%d/overrides/%d", instanceID, chartID), req, &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// DeleteValueOverride deletes a value override for a chart.
func (c *Client) DeleteValueOverride(instanceID, chartID uint) error {
	return c.Delete(fmt.Sprintf("/api/v1/stack-instances/%d/overrides/%d", instanceID, chartID))
}

// ListBranchOverrides returns all branch overrides for a stack instance.
func (c *Client) ListBranchOverrides(instanceID uint) ([]types.BranchOverride, error) {
	var overrides []types.BranchOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/branches", instanceID), &overrides)
	if err != nil {
		return nil, err
	}
	return overrides, nil
}

// GetBranchOverride returns a single branch override for a chart.
func (c *Client) GetBranchOverride(instanceID, chartID uint) (*types.BranchOverride, error) {
	var override types.BranchOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/branches/%d", instanceID, chartID), &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// SetBranchOverride sets a branch override for a chart.
func (c *Client) SetBranchOverride(instanceID, chartID uint, req *types.SetBranchOverrideRequest) (*types.BranchOverride, error) {
	var override types.BranchOverride
	err := c.Put(fmt.Sprintf("/api/v1/stack-instances/%d/branches/%d", instanceID, chartID), req, &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// DeleteBranchOverride deletes a branch override for a chart.
func (c *Client) DeleteBranchOverride(instanceID, chartID uint) error {
	return c.Delete(fmt.Sprintf("/api/v1/stack-instances/%d/branches/%d", instanceID, chartID))
}

// GetQuotaOverride returns the quota override for a stack instance.
func (c *Client) GetQuotaOverride(instanceID uint) (*types.QuotaOverride, error) {
	var override types.QuotaOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%d/quota-overrides", instanceID), &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// SetQuotaOverride sets the quota override for a stack instance.
func (c *Client) SetQuotaOverride(instanceID uint, req *types.SetQuotaOverrideRequest) (*types.QuotaOverride, error) {
	var override types.QuotaOverride
	err := c.Put(fmt.Sprintf("/api/v1/stack-instances/%d/quota-overrides", instanceID), req, &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// DeleteQuotaOverride deletes the quota override for a stack instance.
func (c *Client) DeleteQuotaOverride(instanceID uint) error {
	return c.Delete(fmt.Sprintf("/api/v1/stack-instances/%d/quota-overrides", instanceID))
}

// GetMergedValues returns the merged Helm values for a stack instance.
func (c *Client) GetMergedValues(instanceID uint, chartName string) (*types.MergedValues, error) {
	var values types.MergedValues
	params := map[string]string{}
	if chartName != "" {
		params["chart"] = chartName
	}
	err := c.GetWithQuery(fmt.Sprintf("/api/v1/stack-instances/%d/values", instanceID), params, &values)
	if err != nil {
		return nil, err
	}
	return &values, nil
}

// CompareInstances compares two stack instances.
func (c *Client) CompareInstances(leftID, rightID uint) (*types.CompareResult, error) {
	var result types.CompareResult
	params := map[string]string{
		"left":  fmt.Sprintf("%d", leftID),
		"right": fmt.Sprintf("%d", rightID),
	}
	err := c.GetWithQuery("/api/v1/stack-instances/compare", params, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// BulkDeploy triggers deployment for multiple stack instances.
func (c *Client) BulkDeploy(ids []uint) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/deploy", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkStop stops multiple stack instances.
func (c *Client) BulkStop(ids []uint) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/stop", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkClean undeploys and removes namespaces for multiple stack instances.
func (c *Client) BulkClean(ids []uint) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/clean", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkDelete deletes multiple stack instances.
func (c *Client) BulkDelete(ids []uint) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/delete", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListGitBranches returns branches for a git repository.
func (c *Client) ListGitBranches(repo string) ([]types.GitBranch, error) {
	var branches []types.GitBranch
	err := c.GetWithQuery("/api/v1/git/branches", map[string]string{"repo": repo}, &branches)
	if err != nil {
		return nil, err
	}
	return branches, nil
}

// ValidateGitBranch validates whether a branch exists in a git repository.
func (c *Client) ValidateGitBranch(repo, branch string) (*types.GitValidateResponse, error) {
	var resp types.GitValidateResponse
	err := c.GetWithQuery("/api/v1/git/validate", map[string]string{"repo": repo, "branch": branch}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListClusters returns a paginated list of clusters.
func (c *Client) ListClusters() (*types.ListResponse[types.Cluster], error) {
	var resp types.ListResponse[types.Cluster]
	err := c.Get("/api/v1/clusters", &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetCluster returns a single cluster by ID.
func (c *Client) GetCluster(id uint) (*types.Cluster, error) {
	var cluster types.Cluster
	err := c.Get(fmt.Sprintf("/api/v1/clusters/%d", id), &cluster)
	if err != nil {
		return nil, err
	}
	return &cluster, nil
}

// GetClusterHealth returns the health summary for a cluster.
func (c *Client) GetClusterHealth(id uint) (*types.ClusterHealthSummary, error) {
	var health types.ClusterHealthSummary
	err := c.Get(fmt.Sprintf("/api/v1/clusters/%d/health/summary", id), &health)
	if err != nil {
		return nil, err
	}
	return &health, nil
}
