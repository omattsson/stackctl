package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omattsson/stackctl/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()
	c := New("http://localhost:8081/")
	assert.Equal(t, "http://localhost:8081", c.BaseURL, "trailing slash should be trimmed")
	assert.NotNil(t, c.HTTPClient)
}

func TestAuthHeaders_JWT(t *testing.T) {
	t.Parallel()
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer server.Close()

	c := New(server.URL)
	c.Token = "jwt-token-123"

	var result map[string]string
	err := c.Get("/test", &result)
	require.NoError(t, err)
	assert.Equal(t, "Bearer jwt-token-123", gotAuth)
}

func TestAuthHeaders_APIKey(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer server.Close()

	c := New(server.URL)
	c.APIKey = "sk_test_key"

	var result map[string]string
	err := c.Get("/test", &result)
	require.NoError(t, err)
	assert.Equal(t, "sk_test_key", gotAPIKey)
	assert.Empty(t, gotAuth, "JWT auth should not be set when API key is present")
}

func TestAuthHeaders_APIKeyTakesPrecedence(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer server.Close()

	c := New(server.URL)
	c.Token = "jwt-token-123"
	c.APIKey = "sk_test_key"

	var result map[string]string
	err := c.Get("/test", &result)
	require.NoError(t, err)
	assert.Equal(t, "sk_test_key", gotAPIKey)
	assert.Empty(t, gotAuth, "JWT should not be set when API key takes precedence")
}

func TestAuthHeaders_NoAuth(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer server.Close()

	c := New(server.URL)

	var result map[string]string
	err := c.Get("/test", &result)
	require.NoError(t, err)
	assert.Empty(t, gotAPIKey)
	assert.Empty(t, gotAuth)
}

func TestGet(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/test", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"key": "value"})
	}))
	defer server.Close()

	c := New(server.URL)
	var result map[string]string
	err := c.Get("/api/v1/test", &result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestPost(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req map[string]string
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "test", req["name"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]uint{"id": 42})
	}))
	defer server.Close()

	c := New(server.URL)
	var result map[string]uint
	err := c.Post("/test", map[string]string{"name": "test"}, &result)
	require.NoError(t, err)
	assert.Equal(t, uint(42), result["id"])
}

func TestPut(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}))
	defer server.Close()

	c := New(server.URL)
	var result map[string]string
	err := c.Put("/test/1", map[string]string{"name": "updated"}, &result)
	require.NoError(t, err)
	assert.Equal(t, "updated", result["status"])
}

func TestDelete(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/items/5", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.Delete("/api/v1/items/5")
	require.NoError(t, err)
}

func TestErrorHandling(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantMsg    string
	}{
		{
			name:       "401 unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"error": "invalid token"}`,
			wantMsg:    "invalid token",
		},
		{
			name:       "403 forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"error": "access denied"}`,
			wantMsg:    "access denied",
		},
		{
			name:       "404 not found",
			statusCode: http.StatusNotFound,
			body:       `{"error": "instance not found"}`,
			wantMsg:    "instance not found",
		},
		{
			name:       "409 conflict",
			statusCode: http.StatusConflict,
			body:       `{"error": "version mismatch"}`,
			wantMsg:    "version mismatch",
		},
		{
			name:       "429 rate limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error": "rate limit exceeded"}`,
			wantMsg:    "rate limit exceeded",
		},
		{
			name:       "500 server error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error": "Internal server error"}`,
			wantMsg:    "Internal server error",
		},
		{
			name:       "error with invalid JSON body",
			statusCode: http.StatusBadGateway,
			body:       `not json`,
			wantMsg:    "Bad Gateway",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			c := New(server.URL)
			var result map[string]string
			err := c.Get("/test", &result)
			require.Error(t, err)

			apiErr, ok := err.(*APIError)
			require.True(t, ok, "expected *APIError, got %T", err)
			assert.Equal(t, tt.statusCode, apiErr.StatusCode)
			assert.Equal(t, tt.wantMsg, apiErr.Message)
		})
	}
}

func TestAPIError_UserFacingError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		message    string
		want       string
	}{
		{name: "401", statusCode: 401, message: "invalid", want: "Not authenticated. Run 'stackctl login' first."},
		{name: "403", statusCode: 403, message: "denied", want: "Permission denied."},
		{name: "404", statusCode: 404, message: "not found", want: "Resource not found: not found"},
		{name: "409", statusCode: 409, message: "version mismatch", want: "Conflict: version mismatch"},
		{name: "429", statusCode: 429, message: "slow down", want: "Rate limited. Try again later."},
		{name: "500", statusCode: 500, message: "oops", want: "Server error. Check backend logs."},
		{name: "502", statusCode: 502, message: "oops", want: "Server error. Check backend logs."},
		{name: "400", statusCode: 400, message: "bad input", want: "bad input"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := &APIError{StatusCode: tt.statusCode, Message: tt.message}
			assert.Equal(t, tt.want, err.UserFacingError())
		})
	}
}

func TestGetWithQuery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "running", r.URL.Query().Get("status"))
		assert.Equal(t, "admin", r.URL.Query().Get("owner"))
		assert.Empty(t, r.URL.Query().Get("empty"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]string{})
	}))
	defer server.Close()

	c := New(server.URL)
	var result []string
	err := c.GetWithQuery("/test", map[string]string{
		"status": "running",
		"owner":  "admin",
		"empty":  "",
	}, &result)
	require.NoError(t, err)
}

func TestGetWithQuery_NoParams(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.URL.RawQuery)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]string{})
	}))
	defer server.Close()

	c := New(server.URL)
	var result []string
	err := c.GetWithQuery("/test", nil, &result)
	require.NoError(t, err)
}

func TestLogin(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/auth/login", r.URL.Path)

		var req types.LoginRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "admin", req.Username)
		assert.Equal(t, "secret", req.Password)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{Token: "jwt-token-abc"})
	}))
	defer server.Close()

	c := New(server.URL)
	token, err := c.Login("admin", "secret")
	require.NoError(t, err)
	assert.Equal(t, "jwt-token-abc", token)
	assert.Equal(t, "jwt-token-abc", c.Token, "client token should be set after login")
}

func TestLogin_Failure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid credentials"})
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.Login("bad", "creds")
	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestWhoami(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/auth/me", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: 1},
			Username: "admin",
			Role:     "admin",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	c.Token = "valid-token"
	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, uint(1), user.ID)
	assert.Equal(t, "admin", user.Username)
	assert.Equal(t, "admin", user.Role)
}

func TestConnectionError(t *testing.T) {
	t.Parallel()
	c := New("http://127.0.0.1:1") // port 1 should refuse connections
	var result map[string]string
	err := c.Get("/test", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "making request")
}
