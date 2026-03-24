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

	"github.com/omattsson/stackctl/pkg/types"
)

const defaultTimeout = 30 * time.Second

// Client is the HTTP client for the k8s-stack-manager API.
type Client struct {
	BaseURL    string
	Token      string
	APIKey     string
	HTTPClient *http.Client
	Insecure   bool
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
	return e.Message
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

// Login authenticates and returns the JWT token.
func (c *Client) Login(username, password string) (string, error) {
	var resp types.LoginResponse
	err := c.Post("/api/v1/auth/login", types.LoginRequest{
		Username: username,
		Password: password,
	}, &resp)
	if err != nil {
		return "", err
	}
	c.Token = resp.Token
	return resp.Token, nil
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
