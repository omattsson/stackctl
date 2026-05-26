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
	Sleeper      func(time.Duration) // injectable for tests; defaults to time.Sleep
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
		for _, h := range []string{"Authorization", "X-API-Key", "Content-Type"} {
			if v := req.Header.Get(h); v != "" {
				fmt.Fprintf(c.DebugWriter, "  %s: %s\n", h, maskCredential(h, v))
			}
		}
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
			apiErr.retryAfter = parseRetryAfter(ra)
		}
		// Most endpoints return {"error": "..."}, but a few (notably
		// POST /api/v1/clusters/:id/test on 502) return
		// {"status": "...", "message": "..."} instead. Decode both
		// shapes so the backend-provided message surfaces in the
		// user-facing APIError.
		var errResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			apiErr.Message = http.StatusText(resp.StatusCode)
			return nil, apiErr
		}
		switch {
		case errResp.Error != "":
			apiErr.Message = errResp.Error
		case errResp.Message != "":
			apiErr.Message = errResp.Message
		default:
			// Decoded successfully but both fields empty — fall back to the
			// status text so the user-facing rendering still has *some*
			// context rather than just "Server error.".
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return nil, apiErr
	}

	return resp, nil
}

var retryableStatuses = map[int]bool{
	http.StatusTooManyRequests:    true,
	http.StatusBadGateway:         true,
	http.StatusServiceUnavailable: true,
	http.StatusGatewayTimeout:     true,
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
			c.sleep(wait)
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

func (c *Client) sleep(d time.Duration) {
	if c.Sleeper != nil {
		c.Sleeper(d)
		return
	}
	time.Sleep(d)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func maskCredential(header, value string) string {
	switch strings.ToLower(header) {
	case "authorization":
		if i := strings.IndexByte(value, ' '); i > 0 {
			return value[:i] + " ***"
		}
		return "***"
	case "x-api-key":
		if len(value) > 4 {
			return "***" + value[len(value)-4:]
		}
		return "***"
	default:
		return value
	}
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

// GetOIDCConfig fetches the OIDC configuration from the server.
func (c *Client) GetOIDCConfig() (*types.OIDCConfig, error) {
	var cfg types.OIDCConfig
	if err := c.Get("/api/v1/auth/oidc/config", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// CLIAuth initiates a CLI SSO authentication session.
func (c *Client) CLIAuth() (*types.CLIAuthResponse, error) {
	var resp types.CLIAuthResponse
	if err := c.Post("/api/v1/auth/oidc/cli-auth", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CLIToken polls for CLI SSO authentication completion.
func (c *Client) CLIToken(sessionID string) (*types.CLITokenResponse, error) {
	var resp types.CLITokenResponse
	if err := c.Post("/api/v1/auth/oidc/cli-token", types.CLITokenRequest{SessionID: sessionID}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
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

// CreateTemplate creates a new stack template.
func (c *Client) CreateTemplate(req *types.CreateTemplateRequest) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Post("/api/v1/templates", req, &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// UpdateTemplate updates an existing stack template by ID.
func (c *Client) UpdateTemplate(id string, req *types.UpdateTemplateRequest) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Put(fmt.Sprintf(pathTemplate, id), req, &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// CloneTemplate clones a stack template by ID.
func (c *Client) CloneTemplate(id string, req *types.CloneTemplateRequest) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Post(fmt.Sprintf(pathTemplate+"/clone", id), req, &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// PublishTemplate publishes a stack template by ID.
func (c *Client) PublishTemplate(id string) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Post(fmt.Sprintf(pathTemplate+"/publish", id), nil, &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// UnpublishTemplate unpublishes a stack template by ID.
func (c *Client) UnpublishTemplate(id string) (*types.StackTemplate, error) {
	var tmpl types.StackTemplate
	err := c.Post(fmt.Sprintf(pathTemplate+"/unpublish", id), nil, &tmpl)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// ListTemplateVersions returns all version snapshots for a template.
func (c *Client) ListTemplateVersions(templateID string) ([]types.TemplateVersion, error) {
	var versions []types.TemplateVersion
	err := c.Get(fmt.Sprintf("/api/v1/templates/%s/versions", templateID), &versions)
	if err != nil {
		return nil, err
	}
	return versions, nil
}

// GetTemplateVersion returns a specific version snapshot for a template.
func (c *Client) GetTemplateVersion(templateID, versionID string) (*types.TemplateVersionDetail, error) {
	var v types.TemplateVersionDetail
	err := c.Get(fmt.Sprintf("/api/v1/templates/%s/versions/%s", templateID, versionID), &v)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// DiffTemplateVersions compares two template version snapshots.
func (c *Client) DiffTemplateVersions(templateID, leftID, rightID string) (*types.TemplateVersionDiff, error) {
	var diff types.TemplateVersionDiff
	err := c.GetWithQuery(
		fmt.Sprintf("/api/v1/templates/%s/versions/diff", templateID),
		map[string]string{"left": leftID, "right": rightID},
		&diff,
	)
	if err != nil {
		return nil, err
	}
	return &diff, nil
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

// BulkDeleteTemplates deletes multiple stack templates.
func (c *Client) BulkDeleteTemplates(ids []string) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/templates/bulk/delete", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkPublishTemplates publishes multiple stack templates.
func (c *Client) BulkPublishTemplates(ids []string) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/templates/bulk/publish", types.BulkRequest{IDs: ids}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// BulkUnpublishTemplates unpublishes multiple stack templates.
func (c *Client) BulkUnpublishTemplates(ids []string) (*types.BulkResponse, error) {
	var resp types.BulkResponse
	err := c.Post("/api/v1/templates/bulk/unpublish", types.BulkRequest{IDs: ids}, &resp)
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
//
// @see GET /api/v1/clusters/:id/health/summary
func (c *Client) GetClusterHealth(id string) (*types.ClusterHealthSummary, error) {
	var health types.ClusterHealthSummary
	err := c.Get(fmt.Sprintf("/api/v1/clusters/%s/health/summary", id), &health)
	if err != nil {
		return nil, err
	}
	return &health, nil
}

// TestClusterConnection asks the backend to verify connectivity to the
// cluster's API server. On a reachable cluster the backend responds 200 with
// status=="success"; on an unreachable cluster it returns a non-2xx response
// which surfaces here as a client.APIError.
//
// @see POST /api/v1/clusters/:id/test
func (c *Client) TestClusterConnection(id string) (*types.ClusterTestConnectionResult, error) {
	var result types.ClusterTestConnectionResult
	if err := c.Post(fmt.Sprintf("/api/v1/clusters/%s/test", id), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetClusterNodes returns per-node status for a cluster.
//
// @see GET /api/v1/clusters/:id/health/nodes
func (c *Client) GetClusterNodes(id string) ([]types.ClusterNodeStatus, error) {
	var nodes []types.ClusterNodeStatus
	if err := c.Get(fmt.Sprintf("/api/v1/clusters/%s/health/nodes", id), &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetClusterNamespaces returns the stack-* namespaces present in the cluster.
//
// @see GET /api/v1/clusters/:id/namespaces
func (c *Client) GetClusterNamespaces(id string) ([]types.ClusterNamespace, error) {
	var namespaces []types.ClusterNamespace
	if err := c.Get(fmt.Sprintf("/api/v1/clusters/%s/namespaces", id), &namespaces); err != nil {
		return nil, err
	}
	return namespaces, nil
}

// GetClusterQuota returns the resource-quota config for a cluster.
// The backend responds 404 when no quota is configured for the cluster;
// that surfaces here as a *client.APIError with StatusCode == 404.
//
// @see GET /api/v1/clusters/:id/quotas
func (c *Client) GetClusterQuota(id string) (*types.ClusterQuota, error) {
	var quota types.ClusterQuota
	if err := c.Get(fmt.Sprintf("/api/v1/clusters/%s/quotas", id), &quota); err != nil {
		return nil, err
	}
	return &quota, nil
}

// SetClusterQuota upserts the resource-quota config for a cluster (admin only).
// The backend re-reads the saved config so the returned struct has ID and
// timestamps populated.
//
// @see PUT /api/v1/clusters/:id/quotas
func (c *Client) SetClusterQuota(id string, req *types.SetClusterQuotaRequest) (*types.ClusterQuota, error) {
	var quota types.ClusterQuota
	if err := c.Put(fmt.Sprintf("/api/v1/clusters/%s/quotas", id), req, &quota); err != nil {
		return nil, err
	}
	return &quota, nil
}

// DeleteClusterQuota removes the resource-quota config for a cluster (admin
// only). Returns nil on the 204 path; APIError on 403/404/5xx.
//
// @see DELETE /api/v1/clusters/:id/quotas
func (c *Client) DeleteClusterQuota(id string) error {
	return c.Delete(fmt.Sprintf("/api/v1/clusters/%s/quotas", id))
}

// GetClusterUtilization returns aggregated per-namespace resource utilization.
//
// @see GET /api/v1/clusters/:id/utilization
func (c *Client) GetClusterUtilization(id string) (*types.ClusterUtilization, error) {
	var util types.ClusterUtilization
	if err := c.Get(fmt.Sprintf("/api/v1/clusters/%s/utilization", id), &util); err != nil {
		return nil, err
	}
	return &util, nil
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

// CreateCluster registers a new Kubernetes cluster.
func (c *Client) CreateCluster(req *types.CreateClusterRequest) (*types.Cluster, error) {
	var cluster types.Cluster
	err := c.Post("/api/v1/clusters", req, &cluster)
	if err != nil {
		return nil, err
	}
	return &cluster, nil
}

// UpdateCluster updates cluster metadata and/or kubeconfig.
func (c *Client) UpdateCluster(id string, req *types.UpdateClusterRequest) (*types.Cluster, error) {
	var cluster types.Cluster
	err := c.Put(fmt.Sprintf("/api/v1/clusters/%s", id), req, &cluster)
	if err != nil {
		return nil, err
	}
	return &cluster, nil
}

// DeleteCluster permanently removes a registered cluster.
func (c *Client) DeleteCluster(id string) error {
	return c.Delete(fmt.Sprintf("/api/v1/clusters/%s", id))
}

// SetDefaultCluster marks a cluster as the default for new deployments.
func (c *Client) SetDefaultCluster(id string) error {
	return c.Post(fmt.Sprintf("/api/v1/clusters/%s/default", id), nil, nil)
}

// ListCleanupPolicies returns every cleanup policy defined on the server.
// Admin-only — non-admin callers receive APIError with status 403.
//
// @see GET /api/v1/admin/cleanup-policies
func (c *Client) ListCleanupPolicies() ([]types.CleanupPolicy, error) {
	var policies []types.CleanupPolicy
	if err := c.Get("/api/v1/admin/cleanup-policies", &policies); err != nil {
		return nil, err
	}
	return policies, nil
}

// CreateCleanupPolicy creates a new cleanup policy. Admin-only.
//
// @see POST /api/v1/admin/cleanup-policies
func (c *Client) CreateCleanupPolicy(req *types.CreateCleanupPolicyRequest) (*types.CleanupPolicy, error) {
	var policy types.CleanupPolicy
	if err := c.Post("/api/v1/admin/cleanup-policies", req, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// UpdateCleanupPolicy replaces an existing cleanup policy by ID. Admin-only.
// PUT is a full upsert; callers must provide every field.
//
// @see PUT /api/v1/admin/cleanup-policies/:id
func (c *Client) UpdateCleanupPolicy(id string, req *types.UpdateCleanupPolicyRequest) (*types.CleanupPolicy, error) {
	var policy types.CleanupPolicy
	if err := c.Put(fmt.Sprintf("/api/v1/admin/cleanup-policies/%s", id), req, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// DeleteCleanupPolicy removes a cleanup policy by ID. Admin-only.
//
// @see DELETE /api/v1/admin/cleanup-policies/:id
func (c *Client) DeleteCleanupPolicy(id string) error {
	return c.Delete(fmt.Sprintf("/api/v1/admin/cleanup-policies/%s", id))
}

// RunCleanupPolicy executes a cleanup policy immediately. When dryRun is true
// the scheduler only logs the matched instances; otherwise it applies the
// policy's action. Admin-only. The dry-run flag is sent as a query parameter,
// matching the backend handler which reads c.Query("dry_run").
//
// Returns one CleanupResult per matched instance — never nil, may be empty if
// nothing matched. A successful HTTP response can still contain results with
// Status == "error" (partial failure on a per-instance basis); callers should
// inspect each result rather than relying on the absence of an error return.
//
// @see POST /api/v1/admin/cleanup-policies/:id/run
func (c *Client) RunCleanupPolicy(id string, dryRun bool) ([]types.CleanupResult, error) {
	path := fmt.Sprintf("/api/v1/admin/cleanup-policies/%s/run", id)
	if dryRun {
		path += "?dry_run=true"
	}
	var results []types.CleanupResult
	if err := c.Post(path, nil, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// Register creates a new user account. Requires an authenticated caller —
// the backend rejects with 403 when self-registration is disabled and the
// caller is not an admin. The Role and ServiceAccount fields on the request
// are silently overridden by the server unless the caller is admin.
//
// @see POST /api/v1/auth/register
func (c *Client) Register(req *types.RegisterRequest) (*types.User, error) {
	var user types.User
	if err := c.Post("/api/v1/auth/register", req, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ListUsers returns every user account on the server. Admin-only.
//
// @see GET /api/v1/users
func (c *Client) ListUsers() ([]types.User, error) {
	var users []types.User
	if err := c.Get("/api/v1/users", &users); err != nil {
		return nil, err
	}
	return users, nil
}

// DeleteUser permanently removes a user account by ID. Admin-only. The
// backend rejects with 400 when the caller tries to delete its own account.
//
// @see DELETE /api/v1/users/:id
func (c *Client) DeleteUser(id string) error {
	return c.Delete(fmt.Sprintf("/api/v1/users/%s", id))
}

// DisableUser marks a user account as disabled. All active sessions and API
// keys are revoked server-side. Admin-only. The backend rejects with 400
// when the caller tries to disable its own account.
//
// @see PUT /api/v1/users/:id/disable
func (c *Client) DisableUser(id string) error {
	return c.Put(fmt.Sprintf("/api/v1/users/%s/disable", id), nil, nil)
}

// EnableUser re-enables a previously disabled user account. Admin-only.
//
// @see PUT /api/v1/users/:id/enable
func (c *Client) EnableUser(id string) error {
	return c.Put(fmt.Sprintf("/api/v1/users/%s/enable", id), nil, nil)
}

// ResetUserPassword sets a new password for a local user account. Admin-only.
// The backend rejects with 400 for passwords shorter than 8 characters and
// for users with a non-local AuthProvider.
//
// @see PUT /api/v1/users/:id/password
func (c *Client) ResetUserPassword(id, password string) error {
	return c.Put(fmt.Sprintf("/api/v1/users/%s/password", id), &types.ResetPasswordRequest{Password: password}, nil)
}

// ListAPIKeys returns every API key configured for the given user. Callers
// must be the target user OR have the admin role; the backend returns 403
// otherwise. The raw key value is NEVER returned by this endpoint — only
// the prefix is available for visual identification.
//
// @see GET /api/v1/users/:id/api-keys
func (c *Client) ListAPIKeys(userID string) ([]types.APIKey, error) {
	var keys []types.APIKey
	if err := c.Get(fmt.Sprintf("/api/v1/users/%s/api-keys", userID), &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// CreateAPIKey creates a new API key for the given user. Callers must be the
// target user OR have the admin role.
//
// The returned response carries the plaintext key in RawKey (prefixed with
// "sk_") — this is the ONLY time the raw key is available and it cannot be
// retrieved again. Callers must surface it immediately and must not persist
// it to config files. The client's debug logging is configured to log only
// the request line and a small set of HEADERS (with masking), never the
// request or response body, so debug-mode runs do not leak the key.
//
// @see POST /api/v1/users/:id/api-keys
func (c *Client) CreateAPIKey(userID string, req *types.CreateAPIKeyRequest) (*types.CreateAPIKeyResponse, error) {
	var resp types.CreateAPIKeyResponse
	if err := c.Post(fmt.Sprintf("/api/v1/users/%s/api-keys", userID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteAPIKey revokes the given API key. Callers must be the target user OR
// have the admin role; the backend returns 403 otherwise.
//
// @see DELETE /api/v1/users/:id/api-keys/:keyId
func (c *Client) DeleteAPIKey(userID, keyID string) error {
	return c.Delete(fmt.Sprintf("/api/v1/users/%s/api-keys/%s", userID, keyID))
}

// GetAnalyticsOverview returns platform-wide aggregate counts. Devops-gated;
// non-devops callers receive APIError with status 403. Response is cached
// server-side for ~30s.
//
// @see GET /api/v1/analytics/overview
func (c *Client) GetAnalyticsOverview() (*types.AnalyticsOverview, error) {
	var overview types.AnalyticsOverview
	if err := c.Get("/api/v1/analytics/overview", &overview); err != nil {
		return nil, err
	}
	return &overview, nil
}

// GetAnalyticsTemplates returns per-template usage statistics. Devops-gated.
// Response is cached server-side for ~30s.
//
// @see GET /api/v1/analytics/templates
func (c *Client) GetAnalyticsTemplates() ([]types.TemplateStats, error) {
	var stats []types.TemplateStats
	if err := c.Get("/api/v1/analytics/templates", &stats); err != nil {
		return nil, err
	}
	return stats, nil
}

// GetAnalyticsUsers returns per-user usage statistics. Admin-only — non-admin
// callers (including devops) receive APIError with status 403.
//
// @see GET /api/v1/analytics/users
func (c *Client) GetAnalyticsUsers() ([]types.UserStats, error) {
	var stats []types.UserStats
	if err := c.Get("/api/v1/analytics/users", &stats); err != nil {
		return nil, err
	}
	return stats, nil
}

// auditLogParamsToQuery converts the typed filter struct into the
// backend's expected query-param shape. Empty / zero fields are omitted.
// Time fields are formatted as RFC3339 — the only format the backend accepts.
func auditLogParamsToQuery(p types.AuditLogListParams) map[string]string {
	q := map[string]string{}
	if p.UserID != "" {
		q["user_id"] = p.UserID
	}
	if p.EntityType != "" {
		q["entity_type"] = p.EntityType
	}
	if p.EntityID != "" {
		q["entity_id"] = p.EntityID
	}
	if p.Action != "" {
		q["action"] = p.Action
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	if p.StartDate != nil {
		q["start_date"] = p.StartDate.UTC().Format(time.RFC3339)
	}
	if p.EndDate != nil {
		q["end_date"] = p.EndDate.UTC().Format(time.RFC3339)
	}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Offset > 0 {
		q["offset"] = strconv.Itoa(p.Offset)
	}
	return q
}

// ListAuditLogs returns a page of audit log entries matching the filters.
// All filter fields are optional; an empty struct returns the default page
// (limit=25, offset=0). Backend supports cursor pagination via NextCursor.
//
// @see GET /api/v1/audit-logs
func (c *Client) ListAuditLogs(p types.AuditLogListParams) (*types.PaginatedAuditLogs, error) {
	var resp types.PaginatedAuditLogs
	if err := c.GetWithQuery("/api/v1/audit-logs", auditLogParamsToQuery(p), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ExportAuditLogs returns the raw body of the audit log export endpoint.
// Format must be "json" or "csv"; the backend rejects anything else with 400.
// Admin-gated — non-admin callers (including devops) receive APIError 403.
//
// The body is returned untouched so the CLI can stream the server's CSV
// (with its own quoting rules) directly to disk without re-encoding.
//
// @see GET /api/v1/audit-logs/export
func (c *Client) ExportAuditLogs(format string, p types.AuditLogListParams) ([]byte, error) {
	q := auditLogParamsToQuery(p)
	q["format"] = format
	path := "/api/v1/audit-logs/export"
	if encoded := encodeQueryParams(q); encoded != "" {
		path = path + "?" + encoded
	}
	resp, err := c.doWithRetry(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Server caps the export at 10,000 rows; with realistic row sizes that
	// fits comfortably inside 25MB. Use a LimitReader as a defense against
	// a misconfigured/compromised backend streaming an unbounded body.
	const maxAuditExportSize = 25 * 1024 * 1024 // 25MB
	limited := io.LimitReader(resp.Body, maxAuditExportSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if int64(len(data)) > maxAuditExportSize {
		return nil, fmt.Errorf("audit export response exceeds maximum size of %d bytes", maxAuditExportSize)
	}
	return data, nil
}

// encodeQueryParams builds a stable-ordered URL-encoded query string from a
// map of non-empty values. Identical to the body of GetWithQuery but factored
// out because ExportAuditLogs needs the raw URL (not doJSON).
func encodeQueryParams(params map[string]string) string {
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	return q.Encode()
}

// NotificationListParams holds the optional query parameters for
// ListNotifications. Empty / zero fields are omitted from the wire request.
type NotificationListParams struct {
	UnreadOnly bool
	Limit      int
	Offset     int
}

// ListNotifications returns a page of the authenticated user's notifications.
// Backend defaults: limit=20 (max 100), offset=0, unread_only=false.
//
// @see GET /api/v1/notifications
func (c *Client) ListNotifications(p NotificationListParams) (*types.PaginatedNotifications, error) {
	q := map[string]string{}
	if p.UnreadOnly {
		q["unread_only"] = "true"
	}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Offset > 0 {
		q["offset"] = strconv.Itoa(p.Offset)
	}
	var resp types.PaginatedNotifications
	if err := c.GetWithQuery("/api/v1/notifications", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CountUnreadNotifications returns the unread count for badge display.
//
// @see GET /api/v1/notifications/count
func (c *Client) CountUnreadNotifications() (int64, error) {
	var resp types.UnreadCountResponse
	if err := c.Get("/api/v1/notifications/count", &resp); err != nil {
		return 0, err
	}
	return resp.UnreadCount, nil
}

// MarkNotificationAsRead marks a single notification as read. The backend
// verifies ownership and returns 404 if the notification belongs to another
// user (or does not exist).
//
// @see POST /api/v1/notifications/{id}/read
func (c *Client) MarkNotificationAsRead(id string) error {
	return c.Post(fmt.Sprintf("/api/v1/notifications/%s/read", id), nil, nil)
}

// MarkAllNotificationsAsRead marks every notification for the authenticated
// user as read in a single request.
//
// @see POST /api/v1/notifications/read-all
func (c *Client) MarkAllNotificationsAsRead() error {
	return c.Post("/api/v1/notifications/read-all", nil, nil)
}

// GetNotificationPreferences returns the user's notification preferences.
//
// @see GET /api/v1/notifications/preferences
func (c *Client) GetNotificationPreferences() ([]types.NotificationPreference, error) {
	var prefs []types.NotificationPreference
	if err := c.Get("/api/v1/notifications/preferences", &prefs); err != nil {
		return nil, err
	}
	return prefs, nil
}

// UpdateNotificationPreferences sends an array of preference updates and
// returns the full updated preference list on success. The backend rejects
// the request if any element has an empty event_type.
//
// @see PUT /api/v1/notifications/preferences
func (c *Client) UpdateNotificationPreferences(prefs []types.NotificationPreference) ([]types.NotificationPreference, error) {
	var resp []types.NotificationPreference
	if err := c.Put("/api/v1/notifications/preferences", prefs, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ListFavorites returns the authenticated user's favorited entities.
//
// @see GET /api/v1/favorites
func (c *Client) ListFavorites() ([]types.Favorite, error) {
	var favs []types.Favorite
	if err := c.Get("/api/v1/favorites", &favs); err != nil {
		return nil, err
	}
	return favs, nil
}

// AddFavorite adds an entity to the user's favorites. Idempotent on the
// backend — re-adding the same (user_id, entity_type, entity_id) tuple
// returns 201 with the existing row (no duplicate-key error).
//
// @see POST /api/v1/favorites
func (c *Client) AddFavorite(req types.AddFavoriteRequest) (*types.Favorite, error) {
	var fav types.Favorite
	if err := c.Post("/api/v1/favorites", req, &fav); err != nil {
		return nil, err
	}
	return &fav, nil
}

// RemoveFavorite removes an entity from the user's favorites. The backend
// returns 204 No Content; idempotent — removing a non-existent favorite
// also succeeds rather than returning 404.
//
// entityID is user-supplied free text; url.PathEscape guards against odd
// characters (slashes, control chars) silently splitting the path. The
// allowlisted entityType doesn't need escaping but goes through the same
// helper for consistency.
//
// @see DELETE /api/v1/favorites/{entityType}/{entityId}
func (c *Client) RemoveFavorite(entityType, entityID string) error {
	return c.Delete(fmt.Sprintf("/api/v1/favorites/%s/%s",
		url.PathEscape(entityType), url.PathEscape(entityID)))
}

// ListGitProviders returns the status of all configured Git providers
// (azure_devops, gitlab). Status reflects whether the provider was
// configured at server start; it does NOT perform a live connectivity
// check.
//
// @see GET /api/v1/git/providers
func (c *Client) ListGitProviders() ([]types.GitProvider, error) {
	var providers []types.GitProvider
	if err := c.Get("/api/v1/git/providers", &providers); err != nil {
		return nil, err
	}
	return providers, nil
}
