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
	if e.Message != "" {
		return e.Message
	}
	return e.UserFacingError()
}

// UserFacingError returns a user-friendly error message based on the status code.
func (e *APIError) UserFacingError() string {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "Not authenticated. Run 'stackctl login' first."
	case http.StatusForbidden:
		return "Permission denied."
	case http.StatusNotFound:
		return fmt.Sprintf("Resource not found: %s", e.Message)
	case http.StatusConflict:
		return fmt.Sprintf("Conflict: %s", e.Message)
	case http.StatusTooManyRequests:
		return "Rate limited. Try again later."
	default:
		if e.StatusCode >= 500 {
			return "Server error. Check backend logs."
		}
		return e.Message
	}
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
