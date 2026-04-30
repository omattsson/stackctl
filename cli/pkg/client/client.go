package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
)

const defaultTimeout = 30 * time.Second
const maxServerMessageLen = 256

const (
	pathDefinition      = "/api/v1/stack-definitions/%s"
	pathDefinitionChart = "/api/v1/stack-definitions/%s/charts/%s"
	pathTemplate        = "/api/v1/templates/%s"
	pathOverride        = "/api/v1/stack-instances/%s/overrides/%s"
	pathBranchOverride  = "/api/v1/stack-instances/%s/branches/%s"
	pathQuotaOverride   = "/api/v1/stack-instances/%s/quota-overrides"
	pathSharedValues    = "/api/v1/clusters/%s/shared-values"
	pathSharedValuesID  = "/api/v1/clusters/%s/shared-values/%s"
)

// Client is the HTTP client for the k8s-stack-manager API.
// TLS configuration (insecure mode) is handled by the caller setting
// HTTPClient.Transport before making requests.
type Client struct {
	BaseURL      string
	Token        string
	APIKey       string
	HTTPClient   *http.Client
	Debug        bool
	DebugWriter  io.Writer
	RetryBackoff []time.Duration
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
	retryAfter time.Duration
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
		msg := sanitizeServerMessage(e.Message)
		if msg == "" {
			msg = "unknown resource"
		}
		return fmt.Sprintf("Resource not found: %s", msg)
	case http.StatusConflict:
		msg := sanitizeServerMessage(e.Message)
		if msg == "" {
			msg = "unknown conflict"
		}
		return fmt.Sprintf("Conflict: %s", msg)
	case http.StatusTooManyRequests:
		return e.withServerMsg("Rate limited. Try again later.")
	default:
		if e.StatusCode >= 500 {
			return e.withServerMsg("Server error. Check backend logs.")
		}
		return e.Message
	}
}

// sanitizeServerMessage cleans up a server error message for safe display.
// It trims whitespace, replaces control characters, collapses runs of
// whitespace, and truncates to maxServerMessageLen runes (with "..." appended
// if truncated).
func sanitizeServerMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(msg))
	for _, r := range msg {
		if r < 0x20 || r == 0x7f {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	clean := strings.Join(strings.Fields(b.String()), " ")

	runes := []rune(clean)
	if len(runes) > maxServerMessageLen {
		clean = string(runes[:maxServerMessageLen]) + "..."
	}
	return clean
}

// withServerMsg appends the server message (if non-empty) to a user-facing guidance string.
func (e *APIError) withServerMsg(guidance string) string {
	if msg := sanitizeServerMessage(e.Message); msg != "" {
		return guidance + " (server: " + msg + ")"
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

	if c.Debug && c.DebugWriter != nil {
		fmt.Fprintf(c.DebugWriter, "→ %s %s\n", method, u)
	}

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if c.Debug && c.DebugWriter != nil {
			fmt.Fprintf(c.DebugWriter, "✗ %s (%s)\n", err, time.Since(start).Truncate(time.Millisecond))
		}
		return nil, fmt.Errorf("making request: %w", err)
	}

	if c.Debug && c.DebugWriter != nil {
		fmt.Fprintf(c.DebugWriter, "← %d %s (%s)\n", resp.StatusCode, http.StatusText(resp.StatusCode), time.Since(start).Truncate(time.Millisecond))
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				apiErr.retryAfter = time.Duration(secs) * time.Second
			}
		}
		var errResp types.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			apiErr.Message = http.StatusText(resp.StatusCode)
			return nil, apiErr
		}
		apiErr.Message = errResp.Error
		return nil, apiErr
	}

	return resp, nil
}

var retryableStatuses = map[int]bool{
	http.StatusTooManyRequests:     true,
	http.StatusBadGateway:          true,
	http.StatusServiceUnavailable:  true,
	http.StatusGatewayTimeout:      true,
}

var idempotentMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPut:    true,
	http.MethodDelete: true,
	http.MethodHead:   true,
}

const maxRetryAfter = 30 * time.Second

var defaultRetryBackoff = []time.Duration{time.Second, 3 * time.Second}

// doWithRetry wraps do with retry logic for transient failures on idempotent methods.
func (c *Client) doWithRetry(method, path string, body interface{}) (*http.Response, error) {
	if !idempotentMethods[method] {
		return c.do(method, path, body)
	}

	backoff := c.RetryBackoff
	if backoff == nil {
		backoff = defaultRetryBackoff
	}
	var lastErr error

	for attempt := 0; attempt <= len(backoff); attempt++ {
		resp, err := c.do(method, path, body)
		if err == nil {
			return resp, nil
		}

		apiErr, ok := err.(*APIError)
		if !ok || !retryableStatuses[apiErr.StatusCode] {
			return nil, err
		}
		lastErr = err

		if attempt < len(backoff) {
			wait := backoff[attempt]
			if apiErr.StatusCode == http.StatusTooManyRequests {
				if ra := apiErr.retryAfter; ra > 0 {
					if ra > maxRetryAfter {
						ra = maxRetryAfter
					}
					wait = ra
				}
			}
			if c.Debug && c.DebugWriter != nil {
				fmt.Fprintf(c.DebugWriter, "↻ retrying in %s (attempt %d/%d)\n", wait, attempt+1, len(backoff))
			}
			time.Sleep(wait)
		}
	}
	return nil, lastErr
}

// doJSON executes a request and decodes the JSON response into v.
func (c *Client) doJSON(method, path string, body interface{}, v interface{}) error {
	resp, err := c.doWithRetry(method, path, body)
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
	resp, err := c.doWithRetry(http.MethodDelete, path, nil)
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
func (c *Client) GetStack(id string) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%s", id), &instance)
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
func (c *Client) DeleteStack(id string) error {
	return c.Delete(fmt.Sprintf("/api/v1/stack-instances/%s", id))
}

// DeployStack triggers a deployment for a stack instance.
func (c *Client) DeployStack(id string) (*types.DeployResponse, error) {
	var resp types.DeployResponse
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%s/deploy", id), nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// StopStack triggers a stop for a stack instance.
func (c *Client) StopStack(id string) (*types.DeployResponse, error) {
	var resp types.DeployResponse
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%s/stop", id), nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// CleanStack triggers an undeploy and namespace removal for a stack instance.
func (c *Client) CleanStack(id string) (*types.DeployResponse, error) {
	var resp types.DeployResponse
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%s/clean", id), nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetStackStatus returns the current status and pod states for a stack instance.
func (c *Client) GetStackStatus(id string) (*types.InstanceStatus, error) {
	var status types.InstanceStatus
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%s/status", id), &status)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// GetStackLogs returns the latest deployment log for a stack instance.
func (c *Client) GetStackLogs(id string) (*types.DeploymentLog, error) {
	var result types.DeploymentLogResult
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%s/deploy-log", id), &result)
	if err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no deployment logs found for instance %s", id)
	}
	return &result.Data[0], nil
}

// GetDeploymentHistory returns paginated deployment history for a stack instance.
func (c *Client) GetDeploymentHistory(id string, params map[string]string) (*types.DeploymentLogResult, error) {
	var result types.DeploymentLogResult
	err := c.GetWithQuery(fmt.Sprintf("/api/v1/stack-instances/%s/deploy-log", id), params, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// RollbackStack triggers a rollback for a stack instance.
func (c *Client) RollbackStack(id string, req *types.RollbackRequest) (*types.RollbackResponse, error) {
	var resp types.RollbackResponse
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%s/rollback", id), req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDeployLogValues returns the values snapshot for a specific deployment log entry.
func (c *Client) GetDeployLogValues(instanceID, logID string) (*types.DeployLogValuesResponse, error) {
	var resp types.DeployLogValuesResponse
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%s/deploy-log/%s/values", instanceID, logID), &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// CloneStack clones a stack instance and returns the new instance.
func (c *Client) CloneStack(id string) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%s/clone", id), nil, &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// ExtendStack extends the TTL of a stack instance by the given number of minutes.
func (c *Client) ExtendStack(id string, minutes int) (*types.StackInstance, error) {
	var instance types.StackInstance
	body := map[string]int{"ttl_minutes": minutes}
	err := c.Post(fmt.Sprintf("/api/v1/stack-instances/%s/extend", id), body, &instance)
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
func (c *Client) GetTemplate(id string) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Get(fmt.Sprintf("/api/v1/templates/%s", id), &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// InstantiateTemplate creates a new stack definition from a template.
func (c *Client) InstantiateTemplate(id string, req *types.InstantiateTemplateRequest) (*types.StackDefinition, error) {
	var def types.StackDefinition
	err := c.Post(fmt.Sprintf("/api/v1/templates/%s/instantiate", id), req, &def)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// QuickDeployTemplate creates and deploys a stack instance from a template in one step.
func (c *Client) QuickDeployTemplate(id string, req *types.QuickDeployRequest) (*types.StackInstance, error) {
	var instance types.StackInstance
	err := c.Post(fmt.Sprintf("/api/v1/templates/%s/quick-deploy", id), req, &instance)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// DeleteTemplate deletes a stack template by ID.
func (c *Client) DeleteTemplate(id string) error {
	return c.Delete(fmt.Sprintf(pathTemplate, id))
}

// ListOrphanedNamespaces returns namespaces that have the stack-manager label but no matching DB record.
func (c *Client) ListOrphanedNamespaces() ([]types.OrphanedNamespace, error) {
	var ns []types.OrphanedNamespace
	err := c.Get("/api/v1/orphaned-namespaces", &ns)
	if err != nil {
		return nil, err
	}
	return ns, nil
}

// DeleteOrphanedNamespace removes an orphaned namespace.
func (c *Client) DeleteOrphanedNamespace(namespace string) error {
	return c.Delete(fmt.Sprintf("/api/v1/orphaned-namespaces/%s", namespace))
}

// GetDefinitionChart returns a single chart config within a definition.
func (c *Client) GetDefinitionChart(defID, chartID string) (*types.ChartConfig, error) {
	var chart types.ChartConfig
	err := c.Get(fmt.Sprintf(pathDefinitionChart, defID, chartID), &chart)
	if err != nil {
		return nil, err
	}
	return &chart, nil
}

// UpdateDefinitionChart updates a chart config within a definition.
func (c *Client) UpdateDefinitionChart(defID, chartID string, req *types.UpdateChartConfigRequest) (*types.ChartConfig, error) {
	var chart types.ChartConfig
	err := c.Put(fmt.Sprintf(pathDefinitionChart, defID, chartID), req, &chart)
	if err != nil {
		return nil, err
	}
	return &chart, nil
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
func (c *Client) GetDefinition(id string) (*types.StackDefinition, error) {
	var def types.StackDefinition
	err := c.Get(fmt.Sprintf(pathDefinition, id), &def)
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
func (c *Client) UpdateDefinition(id string, req *types.UpdateDefinitionRequest) (*types.StackDefinition, error) {
	var def types.StackDefinition
	err := c.Put(fmt.Sprintf(pathDefinition, id), req, &def)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// DeleteDefinition deletes a stack definition by ID.
func (c *Client) DeleteDefinition(id string) error {
	return c.Delete(fmt.Sprintf(pathDefinition, id))
}

// ExportDefinition exports a stack definition as raw JSON bytes.
func (c *Client) ExportDefinition(id string) ([]byte, error) {
	resp, err := c.doWithRetry(http.MethodGet, fmt.Sprintf("/api/v1/stack-definitions/%s/export", id), nil)
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
func (c *Client) ListValueOverrides(instanceID string) ([]types.ValueOverride, error) {
	var overrides []types.ValueOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%s/overrides", instanceID), &overrides)
	if err != nil {
		return nil, err
	}
	return overrides, nil
}

// GetValueOverride returns a single value override for a chart.
func (c *Client) GetValueOverride(instanceID, chartID string) (*types.ValueOverride, error) {
	var override types.ValueOverride
	err := c.Get(fmt.Sprintf(pathOverride, instanceID, chartID), &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// SetValueOverride sets value overrides for a chart.
func (c *Client) SetValueOverride(instanceID, chartID string, req *types.SetValueOverrideRequest) (*types.ValueOverride, error) {
	var override types.ValueOverride
	err := c.Put(fmt.Sprintf(pathOverride, instanceID, chartID), req, &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// DeleteValueOverride deletes a value override for a chart.
func (c *Client) DeleteValueOverride(instanceID, chartID string) error {
	return c.Delete(fmt.Sprintf(pathOverride, instanceID, chartID))
}

// ListBranchOverrides returns all branch overrides for a stack instance.
func (c *Client) ListBranchOverrides(instanceID string) ([]types.BranchOverride, error) {
	var overrides []types.BranchOverride
	err := c.Get(fmt.Sprintf("/api/v1/stack-instances/%s/branches", instanceID), &overrides)
	if err != nil {
		return nil, err
	}
	return overrides, nil
}

// GetBranchOverride returns a single branch override for a chart.
func (c *Client) GetBranchOverride(instanceID, chartID string) (*types.BranchOverride, error) {
	var override types.BranchOverride
	err := c.Get(fmt.Sprintf(pathBranchOverride, instanceID, chartID), &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// SetBranchOverride sets a branch override for a chart.
func (c *Client) SetBranchOverride(instanceID, chartID string, req *types.SetBranchOverrideRequest) (*types.BranchOverride, error) {
	var override types.BranchOverride
	err := c.Put(fmt.Sprintf(pathBranchOverride, instanceID, chartID), req, &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// DeleteBranchOverride deletes a branch override for a chart.
func (c *Client) DeleteBranchOverride(instanceID, chartID string) error {
	return c.Delete(fmt.Sprintf(pathBranchOverride, instanceID, chartID))
}

// GetQuotaOverride returns the quota override for a stack instance.
func (c *Client) GetQuotaOverride(instanceID string) (*types.QuotaOverride, error) {
	var override types.QuotaOverride
	err := c.Get(fmt.Sprintf(pathQuotaOverride, instanceID), &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// SetQuotaOverride sets the quota override for a stack instance.
func (c *Client) SetQuotaOverride(instanceID string, req *types.SetQuotaOverrideRequest) (*types.QuotaOverride, error) {
	var override types.QuotaOverride
	err := c.Put(fmt.Sprintf(pathQuotaOverride, instanceID), req, &override)
	if err != nil {
		return nil, err
	}
	return &override, nil
}

// DeleteQuotaOverride deletes the quota override for a stack instance.
func (c *Client) DeleteQuotaOverride(instanceID string) error {
	return c.Delete(fmt.Sprintf(pathQuotaOverride, instanceID))
}

// GetMergedValues returns the merged Helm values for a stack instance.
func (c *Client) GetMergedValues(instanceID string, chartName string) (*types.MergedValues, error) {
	var values types.MergedValues
	params := map[string]string{}
	if chartName != "" {
		params["chart"] = chartName
	}
	err := c.GetWithQuery(fmt.Sprintf("/api/v1/stack-instances/%s/values", instanceID), params, &values)
	if err != nil {
		return nil, err
	}
	return &values, nil
}

// CompareInstances compares two stack instances.
func (c *Client) CompareInstances(leftID, rightID string) (*types.CompareResult, error) {
	var result types.CompareResult
	params := map[string]string{
		"left":  leftID,
		"right": rightID,
	}
	err := c.GetWithQuery("/api/v1/stack-instances/compare", params, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// BulkDeploy triggers deployment for multiple stack instances.
func (c *Client) BulkDeploy(ids []string) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/deploy", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkStop stops multiple stack instances.
func (c *Client) BulkStop(ids []string) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/stop", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkClean undeploys and removes namespaces for multiple stack instances.
func (c *Client) BulkClean(ids []string) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/stack-instances/bulk/clean", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkDelete deletes multiple stack instances.
func (c *Client) BulkDelete(ids []string) (*types.BulkResponse, error) {
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

// ListClusters returns all registered clusters.
// The backend returns a plain array, not a paginated ListResponse.
func (c *Client) ListClusters() ([]types.Cluster, error) {
	var resp []types.Cluster
	err := c.Get("/api/v1/clusters", &resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// GetCluster returns a single cluster by ID.
func (c *Client) GetCluster(id string) (*types.Cluster, error) {
	var cluster types.Cluster
	err := c.Get(fmt.Sprintf("/api/v1/clusters/%s", id), &cluster)
	if err != nil {
		return nil, err
	}
	return &cluster, nil
}

// GetClusterHealth returns the health summary for a cluster.
func (c *Client) GetClusterHealth(id string) (*types.ClusterHealthSummary, error) {
	var health types.ClusterHealthSummary
	err := c.Get(fmt.Sprintf("/api/v1/clusters/%s/health/summary", id), &health)
	if err != nil {
		return nil, err
	}
	return &health, nil
}

// ListSharedValues returns all shared values for a cluster.
func (c *Client) ListSharedValues(clusterID string) ([]types.SharedValues, error) {
	var sv []types.SharedValues
	err := c.Get(fmt.Sprintf(pathSharedValues, clusterID), &sv)
	if err != nil {
		return nil, err
	}
	return sv, nil
}

// SetSharedValues creates or updates shared values for a cluster.
func (c *Client) SetSharedValues(clusterID string, req *types.SetSharedValuesRequest) (*types.SharedValues, error) {
	var sv types.SharedValues
	err := c.Post(fmt.Sprintf(pathSharedValues, clusterID), req, &sv)
	if err != nil {
		return nil, err
	}
	return &sv, nil
}

// DeleteSharedValues deletes shared values from a cluster.
func (c *Client) DeleteSharedValues(clusterID, sharedValuesID string) error {
	return c.Delete(fmt.Sprintf(pathSharedValuesID, clusterID, sharedValuesID))
}
