package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
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
		{name: "401", statusCode: 401, message: "invalid", want: "Not authenticated. Run 'stackctl login' first. (server: invalid)"},
		{name: "403", statusCode: 403, message: "denied", want: "Permission denied. (server: denied)"},
		{name: "404", statusCode: 404, message: "not found", want: "Resource not found: not found"},
		{name: "409", statusCode: 409, message: "version mismatch", want: "Conflict: version mismatch"},
		{name: "429", statusCode: 429, message: "slow down", want: "Rate limited. Try again later. (server: slow down)"},
		{name: "500", statusCode: 500, message: "oops", want: "Server error. Check backend logs. (server: oops)"},
		{name: "502", statusCode: 502, message: "oops", want: "Server error. Check backend logs. (server: oops)"},
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
	resp, err := c.Login("admin", "secret")
	require.NoError(t, err)
	assert.Equal(t, "jwt-token-abc", resp.Token)
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

func TestLogin_VerifiesRequestBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req types.LoginRequest
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "user1", req.Username)
		assert.Equal(t, "pass1", req.Password)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "tok",
			ExpiresAt: "2099-01-01T00:00:00Z",
			User:      types.User{Username: "user1", Role: "viewer"},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.Login("user1", "pass1")
	require.NoError(t, err)
	assert.Equal(t, "tok", resp.Token)
	assert.Equal(t, "user1", resp.User.Username)
	assert.Equal(t, "viewer", resp.User.Role)
}

func TestLogin_SetsClientToken(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{Token: "new-token"})
	}))
	defer server.Close()

	c := New(server.URL)
	assert.Empty(t, c.Token)

	_, err := c.Login("u", "p")
	require.NoError(t, err)
	assert.Equal(t, "new-token", c.Token)
}

func TestLogin_InvalidCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid credentials"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.Login("bad", "creds")
	require.Error(t, err)
	assert.Nil(t, resp)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Equal(t, "invalid credentials", apiErr.Message)
}

func TestLogin_ServerError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "db down"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.Login("u", "p")
	require.Error(t, err)
	assert.Nil(t, resp)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
}

func TestWhoami_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/auth/me", r.URL.Path)
		assert.Equal(t, "Bearer my-jwt", r.Header.Get("Authorization"))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: 5},
			Username: "testuser",
			Role:     "operator",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	c.Token = "my-jwt"
	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, uint(5), user.ID)
	assert.Equal(t, "testuser", user.Username)
	assert.Equal(t, "operator", user.Role)
}

func TestWhoami_Unauthorized(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "token expired"})
	}))
	defer server.Close()

	c := New(server.URL)
	user, err := c.Whoami()
	require.Error(t, err)
	assert.Nil(t, user)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Equal(t, "Not authenticated. Run 'stackctl login' first. (server: token expired)", apiErr.UserFacingError())
}

func TestWhoami_WithAPIKey(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "sk_key_123", r.Header.Get("X-API-Key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{Username: "apiuser", Role: "admin"})
	}))
	defer server.Close()

	c := New(server.URL)
	c.APIKey = "sk_key_123"
	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, "apiuser", user.Username)
}

// ---------- Stack Instance client methods ----------

func TestListStacks_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:       []types.StackInstance{{Base: types.Base{ID: 1}, Name: "stack-1"}},
			Total:      1,
			Page:       1,
			PageSize:   20,
			TotalPages: 1,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListStacks(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "stack-1", resp.Data[0].Name)
}

func TestListStacks_WithFilters(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "running", r.URL.Query().Get("status"))
		assert.Equal(t, "me", r.URL.Query().Get("owner"))
		assert.Equal(t, "2", r.URL.Query().Get("cluster_id"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{})
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListStacks(map[string]string{
		"status":     "running",
		"owner":      "me",
		"cluster_id": "2",
	})
	require.NoError(t, err)
}

func TestGetStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackInstance{
			Base:   types.Base{ID: 42},
			Name:   "my-stack",
			Status: "running",
			Owner:  "admin",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	stack, err := c.GetStack(42)
	require.NoError(t, err)
	assert.Equal(t, uint(42), stack.ID)
	assert.Equal(t, "my-stack", stack.Name)
	assert.Equal(t, "running", stack.Status)
}

func TestGetStack_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	stack, err := c.GetStack(999)
	require.Error(t, err)
	assert.Nil(t, stack)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

func TestCreateStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances", r.URL.Path)

		var body types.CreateStackRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "new-stack", body.Name)
		assert.Equal(t, uint(3), body.StackDefinitionID)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{
			Base:              types.Base{ID: 50},
			Name:              "new-stack",
			StackDefinitionID: 3,
			Status:            "draft",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	stack, err := c.CreateStack(&types.CreateStackRequest{
		Name:              "new-stack",
		StackDefinitionID: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, uint(50), stack.ID)
	assert.Equal(t, "new-stack", stack.Name)
	assert.Equal(t, "draft", stack.Status)
}

func TestDeleteStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteStack(42)
	require.NoError(t, err)
}

func TestDeleteStack_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteStack(999)
	require.Error(t, err)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

func TestDeployStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/deploy", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{
			Base:       types.Base{ID: 100},
			InstanceID: 42,
			Action:     "deploy",
			Status:     "started",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	log, err := c.DeployStack(42)
	require.NoError(t, err)
	assert.Equal(t, uint(100), log.ID)
	assert.Equal(t, uint(42), log.InstanceID)
	assert.Equal(t, "deploy", log.Action)
}

func TestStopStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/stop", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{
			Base:       types.Base{ID: 101},
			InstanceID: 42,
			Action:     "stop",
			Status:     "started",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	log, err := c.StopStack(42)
	require.NoError(t, err)
	assert.Equal(t, uint(101), log.ID)
	assert.Equal(t, "stop", log.Action)
}

func TestCleanStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/clean", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{
			Base:       types.Base{ID: 102},
			InstanceID: 42,
			Action:     "clean",
			Status:     "started",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	log, err := c.CleanStack(42)
	require.NoError(t, err)
	assert.Equal(t, uint(102), log.ID)
	assert.Equal(t, "clean", log.Action)
}

func TestGetStackStatus_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/status", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.InstanceStatus{
			Status: "running",
			Pods: []types.PodStatus{
				{Name: "pod-1", Status: "Running", Ready: true, Restarts: 0, Age: "1h"},
				{Name: "pod-2", Status: "Running", Ready: true, Restarts: 2, Age: "30m"},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	status, err := c.GetStackStatus(42)
	require.NoError(t, err)
	assert.Equal(t, "running", status.Status)
	assert.Len(t, status.Pods, 2)
	assert.Equal(t, "pod-1", status.Pods[0].Name)
	assert.True(t, status.Pods[0].Ready)
	assert.Equal(t, 2, status.Pods[1].Restarts)
}

func TestGetStackLogs_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/deploy-log", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{
			Base:       types.Base{ID: 200},
			InstanceID: 42,
			Action:     "deploy",
			Status:     "completed",
			Output:     "All charts installed successfully.",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	log, err := c.GetStackLogs(42)
	require.NoError(t, err)
	assert.Equal(t, uint(200), log.ID)
	assert.Equal(t, "deploy", log.Action)
	assert.Equal(t, "completed", log.Status)
	assert.Contains(t, log.Output, "All charts installed")
}

func TestCloneStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/clone", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{
			Base:   types.Base{ID: 55},
			Name:   "my-stack-clone",
			Status: "draft",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	clone, err := c.CloneStack(42)
	require.NoError(t, err)
	assert.Equal(t, uint(55), clone.ID)
	assert.Equal(t, "my-stack-clone", clone.Name)
	assert.Equal(t, "draft", clone.Status)
}

func TestExtendStack_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/extend", r.URL.Path)

		var body map[string]int
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, 60, body["ttl_minutes"])

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackInstance{
			Base:       types.Base{ID: 42},
			Name:       "my-stack",
			TTLMinutes: 120,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	stack, err := c.ExtendStack(42, 60)
	require.NoError(t, err)
	assert.Equal(t, uint(42), stack.ID)
	assert.Equal(t, 120, stack.TTLMinutes)
}

// ---------- Template client methods ----------

func TestListTemplates_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/templates", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:       []types.StackTemplate{{Base: types.Base{ID: 1}, Name: "tmpl-1", Published: true}},
			Total:      1,
			Page:       1,
			PageSize:   20,
			TotalPages: 1,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListTemplates(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "tmpl-1", resp.Data[0].Name)
	assert.True(t, resp.Data[0].Published)
}

func TestListTemplates_WithQueryParams(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "true", r.URL.Query().Get("published"))
		assert.Equal(t, "2", r.URL.Query().Get("page"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{})
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListTemplates(map[string]string{
		"published": "true",
		"page":      "2",
	})
	require.NoError(t, err)
}

func TestGetTemplate_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/templates/10", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackTemplate{
			Base:        types.Base{ID: 10},
			Name:        "web-template",
			Description: "A web app template",
			Published:   true,
			Owner:       "admin",
			Charts: []types.ChartConfig{
				{Name: "frontend", RepoURL: "https://charts.example.com", ChartVersion: "1.0.0"},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	tmpl, err := c.GetTemplate(10)
	require.NoError(t, err)
	assert.Equal(t, uint(10), tmpl.ID)
	assert.Equal(t, "web-template", tmpl.Name)
	assert.True(t, tmpl.Published)
	assert.Len(t, tmpl.Charts, 1)
	assert.Equal(t, "frontend", tmpl.Charts[0].Name)
}

func TestGetTemplate_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	tmpl, err := c.GetTemplate(999)
	require.Error(t, err)
	assert.Nil(t, tmpl)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

func TestInstantiateTemplate_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/templates/10/instantiate", r.URL.Path)

		var body types.InstantiateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-instance", body.Name)
		assert.Equal(t, "feature/xyz", body.Branch)
		assert.Equal(t, uint(2), body.ClusterID)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{
			Base:   types.Base{ID: 50},
			Name:   "my-instance",
			Status: "draft",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	instance, err := c.InstantiateTemplate(10, &types.InstantiateTemplateRequest{
		Name:      "my-instance",
		Branch:    "feature/xyz",
		ClusterID: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, uint(50), instance.ID)
	assert.Equal(t, "my-instance", instance.Name)
	assert.Equal(t, "draft", instance.Status)
}

func TestQuickDeployTemplate_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/templates/10/quick-deploy", r.URL.Path)

		var body types.QuickDeployRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "quick-stack", body.Name)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{
			Base:   types.Base{ID: 60},
			Name:   "quick-stack",
			Status: "deploying",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	instance, err := c.QuickDeployTemplate(10, &types.QuickDeployRequest{
		Name: "quick-stack",
	})
	require.NoError(t, err)
	assert.Equal(t, uint(60), instance.ID)
	assert.Equal(t, "quick-stack", instance.Name)
	assert.Equal(t, "deploying", instance.Status)
}

// ---------- Definition client methods ----------

func TestListDefinitions_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data:       []types.StackDefinition{{Base: types.Base{ID: 1}, Name: "def-1", Owner: "admin"}},
			Total:      1,
			Page:       1,
			PageSize:   20,
			TotalPages: 1,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListDefinitions(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "def-1", resp.Data[0].Name)
}

func TestListDefinitions_WithQueryParams(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "me", r.URL.Query().Get("owner"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{})
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListDefinitions(map[string]string{"owner": "me"})
	require.NoError(t, err)
}

func TestGetDefinition_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackDefinition{
			Base:          types.Base{ID: 5},
			Name:          "api-service",
			Description:   "API stack",
			DefaultBranch: "main",
			Owner:         "admin",
			Charts: []types.ChartConfig{
				{Name: "api", RepoURL: "https://charts.example.com", ChartVersion: "2.0.0"},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.GetDefinition(5)
	require.NoError(t, err)
	assert.Equal(t, uint(5), def.ID)
	assert.Equal(t, "api-service", def.Name)
	assert.Equal(t, "main", def.DefaultBranch)
	assert.Len(t, def.Charts, 1)
}

func TestGetDefinition_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.GetDefinition(999)
	require.Error(t, err)
	assert.Nil(t, def)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

func TestCreateDefinition_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions", r.URL.Path)

		var body types.CreateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "new-def", body.Name)
		assert.Equal(t, "A new definition", body.Description)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{
			Base:        types.Base{ID: 20},
			Name:        "new-def",
			Description: "A new definition",
			Owner:       "admin",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.CreateDefinition(&types.CreateDefinitionRequest{
		Name:        "new-def",
		Description: "A new definition",
	})
	require.NoError(t, err)
	assert.Equal(t, uint(20), def.ID)
	assert.Equal(t, "new-def", def.Name)
}

func TestUpdateDefinition_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)

		var body types.UpdateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "updated-name", body.Name)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackDefinition{
			Base: types.Base{ID: 5},
			Name: "updated-name",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.UpdateDefinition(5, &types.UpdateDefinitionRequest{
		Name: "updated-name",
	})
	require.NoError(t, err)
	assert.Equal(t, uint(5), def.ID)
	assert.Equal(t, "updated-name", def.Name)
}

func TestDeleteDefinition_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteDefinition(5)
	require.NoError(t, err)
}

func TestDeleteDefinition_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteDefinition(999)
	require.Error(t, err)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

func TestExportDefinition_Success(t *testing.T) {
	t.Parallel()
	exportJSON := `{"name":"exported-def","description":"test export","charts":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions/5/export", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(exportJSON))
	}))
	defer server.Close()

	c := New(server.URL)
	data, err := c.ExportDefinition(5)
	require.NoError(t, err)
	assert.Equal(t, exportJSON, string(data))
}

func TestExportDefinition_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	data, err := c.ExportDefinition(999)
	require.Error(t, err)
	assert.Nil(t, data)
}

func TestImportDefinition_Success(t *testing.T) {
	t.Parallel()
	importJSON := []byte(`{"name":"imported-def","description":"test import","charts":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-definitions/import", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "imported-def")

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{
			Base:        types.Base{ID: 50},
			Name:        "imported-def",
			Description: "test import",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.ImportDefinition(importJSON)
	require.NoError(t, err)
	assert.Equal(t, uint(50), def.ID)
	assert.Equal(t, "imported-def", def.Name)
}

func TestImportDefinition_ServerError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition already exists"})
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.ImportDefinition([]byte(`{"name":"dup"}`))
	require.Error(t, err)
	assert.Nil(t, def)
}

// ---------- Value Override client methods ----------

func TestListValueOverrides_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/overrides", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{
			{Base: types.Base{ID: 1}, InstanceID: 42, ChartID: 1, Values: `{"replicas":3}`},
			{Base: types.Base{ID: 2}, InstanceID: 42, ChartID: 2, Values: `{"debug":true}`},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	overrides, err := c.ListValueOverrides(42)
	require.NoError(t, err)
	assert.Len(t, overrides, 2)
	assert.Equal(t, uint(1), overrides[0].ChartID)
	assert.Equal(t, uint(2), overrides[1].ChartID)
}

func TestListValueOverrides_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	overrides, err := c.ListValueOverrides(999)
	require.Error(t, err)
	assert.Nil(t, overrides)
}

func TestGetValueOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/overrides/1", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ValueOverride{
			Base: types.Base{ID: 1}, InstanceID: 42, ChartID: 1, Values: `{"replicas":3}`,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.GetValueOverride(42, 1)
	require.NoError(t, err)
	assert.Equal(t, uint(1), override.ChartID)
	assert.Equal(t, uint(42), override.InstanceID)
	assert.Contains(t, override.Values, "replicas")
}

func TestGetValueOverride_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "override not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.GetValueOverride(42, 99)
	require.Error(t, err)
	assert.Nil(t, override)
}

func TestSetValueOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/overrides/1", r.URL.Path)

		var body types.SetValueOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, float64(5), body.Values["replicas"])

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ValueOverride{
			Base: types.Base{ID: 1}, InstanceID: 42, ChartID: 1, Values: `{"replicas":5}`,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.SetValueOverride(42, 1, &types.SetValueOverrideRequest{
		Values: map[string]interface{}{"replicas": float64(5)},
	})
	require.NoError(t, err)
	assert.Equal(t, uint(1), override.ChartID)
}

func TestSetValueOverride_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "set failed"})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.SetValueOverride(42, 1, &types.SetValueOverrideRequest{
		Values: map[string]interface{}{"key": "val"},
	})
	require.Error(t, err)
	assert.Nil(t, override)
}

func TestDeleteValueOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/overrides/1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteValueOverride(42, 1)
	require.NoError(t, err)
}

func TestDeleteValueOverride_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "override not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteValueOverride(42, 99)
	require.Error(t, err)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

// ---------- Branch Override client methods ----------

func TestListBranchOverrides_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/branches", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.BranchOverride{
			{Base: types.Base{ID: 1}, InstanceID: 42, ChartID: 1, Branch: "feature/xyz"},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	overrides, err := c.ListBranchOverrides(42)
	require.NoError(t, err)
	assert.Len(t, overrides, 1)
	assert.Equal(t, "feature/xyz", overrides[0].Branch)
}

func TestListBranchOverrides_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	overrides, err := c.ListBranchOverrides(999)
	require.Error(t, err)
	assert.Nil(t, overrides)
}

func TestGetBranchOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/branches/1", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.BranchOverride{
			Base: types.Base{ID: 1}, InstanceID: 42, ChartID: 1, Branch: "main",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.GetBranchOverride(42, 1)
	require.NoError(t, err)
	assert.Equal(t, "main", override.Branch)
	assert.Equal(t, uint(42), override.InstanceID)
}

func TestGetBranchOverride_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "branch override not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.GetBranchOverride(42, 99)
	require.Error(t, err)
	assert.Nil(t, override)
}

func TestSetBranchOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/branches/1", r.URL.Path)

		var body types.SetBranchOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "feature/new", body.Branch)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.BranchOverride{
			Base: types.Base{ID: 1}, InstanceID: 42, ChartID: 1, Branch: "feature/new",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.SetBranchOverride(42, 1, &types.SetBranchOverrideRequest{Branch: "feature/new"})
	require.NoError(t, err)
	assert.Equal(t, "feature/new", override.Branch)
}

func TestSetBranchOverride_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "set branch failed"})
	}))
	defer server.Close()

	c := New(server.URL)
	override, err := c.SetBranchOverride(42, 1, &types.SetBranchOverrideRequest{Branch: "main"})
	require.Error(t, err)
	assert.Nil(t, override)
}

func TestDeleteBranchOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/branches/1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteBranchOverride(42, 1)
	require.NoError(t, err)
}

func TestDeleteBranchOverride_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "branch override not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteBranchOverride(42, 99)
	require.Error(t, err)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

// ---------- Quota Override client methods ----------

func TestGetQuotaOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/quota-overrides", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.QuotaOverride{
			InstanceID: 42, CPURequest: "100m", CPULimit: "500m",
			MemRequest: "128Mi", MemLimit: "512Mi",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	quota, err := c.GetQuotaOverride(42)
	require.NoError(t, err)
	assert.Equal(t, uint(42), quota.InstanceID)
	assert.Equal(t, "100m", quota.CPURequest)
	assert.Equal(t, "500m", quota.CPULimit)
	assert.Equal(t, "128Mi", quota.MemRequest)
	assert.Equal(t, "512Mi", quota.MemLimit)
}

func TestGetQuotaOverride_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	quota, err := c.GetQuotaOverride(999)
	require.Error(t, err)
	assert.Nil(t, quota)
}

func TestSetQuotaOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/quota-overrides", r.URL.Path)

		var body types.SetQuotaOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "200m", body.CPURequest)
		assert.Equal(t, "1Gi", body.MemLimit)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.QuotaOverride{
			InstanceID: 42, CPURequest: "200m", MemLimit: "1Gi",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	quota, err := c.SetQuotaOverride(42, &types.SetQuotaOverrideRequest{
		CPURequest: "200m", MemLimit: "1Gi",
	})
	require.NoError(t, err)
	assert.Equal(t, "200m", quota.CPURequest)
	assert.Equal(t, "1Gi", quota.MemLimit)
}

func TestSetQuotaOverride_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "set quota failed"})
	}))
	defer server.Close()

	c := New(server.URL)
	quota, err := c.SetQuotaOverride(42, &types.SetQuotaOverrideRequest{CPURequest: "100m"})
	require.Error(t, err)
	assert.Nil(t, quota)
}

func TestDeleteQuotaOverride_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/quota-overrides", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteQuotaOverride(42)
	require.NoError(t, err)
}

func TestDeleteQuotaOverride_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteQuotaOverride(999)
	require.Error(t, err)

	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

// ---------- MergedValues and CompareInstances client methods ----------

func TestGetMergedValues_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/values", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.MergedValues{
			InstanceID: 42,
			Charts: map[string]map[string]interface{}{
				"api": {"replicas": float64(3)},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	values, err := c.GetMergedValues(42, "")
	require.NoError(t, err)
	assert.Equal(t, uint(42), values.InstanceID)
	assert.Contains(t, values.Charts, "api")
}

func TestGetMergedValues_WithChartFilter(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "frontend", r.URL.Query().Get("chart"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.MergedValues{
			InstanceID: 42,
			Charts:     map[string]map[string]interface{}{"frontend": {"port": float64(8080)}},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	values, err := c.GetMergedValues(42, "frontend")
	require.NoError(t, err)
	assert.Contains(t, values.Charts, "frontend")
}

func TestGetMergedValues_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	values, err := c.GetMergedValues(999, "")
	require.Error(t, err)
	assert.Nil(t, values)
}

func TestCompareInstances_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/compare", r.URL.Path)
		assert.Equal(t, "42", r.URL.Query().Get("left"))
		assert.Equal(t, "43", r.URL.Query().Get("right"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.CompareResult{
			Left:  &types.StackInstance{Base: types.Base{ID: 42}, Name: "stack-a"},
			Right: &types.StackInstance{Base: types.Base{ID: 43}, Name: "stack-b"},
			Diffs: map[string]interface{}{"name": true},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.CompareInstances(42, 43)
	require.NoError(t, err)
	assert.Equal(t, uint(42), result.Left.ID)
	assert.Equal(t, uint(43), result.Right.ID)
	assert.Contains(t, result.Diffs, "name")
}

func TestCompareInstances_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "compare failed"})
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.CompareInstances(42, 43)
	require.Error(t, err)
	assert.Nil(t, result)
}

// ---------- Bulk operations ----------

func TestBulkDeploy_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/bulk/deploy", r.URL.Path)
		var body types.BulkRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []uint{1, 2, 3}, body.IDs)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.BulkResponse{
			Results: []types.BulkOperationResult{
				{ID: 1, Success: true},
				{ID: 2, Success: true},
				{ID: 3, Success: false, Error: "not found"},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkDeploy([]uint{1, 2, 3})
	require.NoError(t, err)
	assert.Len(t, resp.Results, 3)
	assert.True(t, resp.Results[0].Success)
	assert.False(t, resp.Results[2].Success)
	assert.Equal(t, "not found", resp.Results[2].Error)
}

func TestBulkDeploy_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "unauthorized"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkDeploy([]uint{1, 2})
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestBulkStop_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/bulk/stop", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.BulkResponse{
			Results: []types.BulkOperationResult{
				{ID: 1, Success: true},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkStop([]uint{1})
	require.NoError(t, err)
	assert.Len(t, resp.Results, 1)
	assert.True(t, resp.Results[0].Success)
}

func TestBulkStop_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "server error"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkStop([]uint{1})
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestBulkClean_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/bulk/clean", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.BulkResponse{
			Results: []types.BulkOperationResult{
				{ID: 5, Success: true},
				{ID: 6, Success: true},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkClean([]uint{5, 6})
	require.NoError(t, err)
	assert.Len(t, resp.Results, 2)
}

func TestBulkClean_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "forbidden"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkClean([]uint{5, 6})
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestBulkDelete_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/bulk/delete", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.BulkResponse{
			Results: []types.BulkOperationResult{
				{ID: 10, Success: true},
			},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkDelete([]uint{10})
	require.NoError(t, err)
	assert.Len(t, resp.Results, 1)
	assert.True(t, resp.Results[0].Success)
}

func TestBulkDelete_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.BulkDelete([]uint{999})
	require.Error(t, err)
	assert.Nil(t, resp)
}

// ---------- Git operations ----------

func TestListGitBranches_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/git/branches", r.URL.Path)
		assert.Equal(t, "https://github.com/org/repo", r.URL.Query().Get("repo"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.GitBranch{
			{Name: "main", IsHead: true},
			{Name: "develop", IsHead: false},
		})
	}))
	defer server.Close()

	c := New(server.URL)
	branches, err := c.ListGitBranches("https://github.com/org/repo")
	require.NoError(t, err)
	assert.Len(t, branches, 2)
	assert.Equal(t, "main", branches[0].Name)
	assert.True(t, branches[0].IsHead)
}

func TestListGitBranches_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "repository not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	branches, err := c.ListGitBranches("https://github.com/org/nonexistent")
	require.Error(t, err)
	assert.Nil(t, branches)
}

func TestListGitBranches_Empty(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.GitBranch{})
	}))
	defer server.Close()

	c := New(server.URL)
	branches, err := c.ListGitBranches("https://github.com/org/empty")
	require.NoError(t, err)
	assert.Empty(t, branches)
}

func TestValidateGitBranch_Valid(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/git/validate", r.URL.Path)
		assert.Equal(t, "https://github.com/org/repo", r.URL.Query().Get("repo"))
		assert.Equal(t, "main", r.URL.Query().Get("branch"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.GitValidateResponse{
			Valid:  true,
			Branch: "main",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ValidateGitBranch("https://github.com/org/repo", "main")
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, "main", resp.Branch)
}

func TestValidateGitBranch_Invalid(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.GitValidateResponse{
			Valid:   false,
			Branch:  "nonexistent",
			Message: "branch does not exist",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ValidateGitBranch("https://github.com/org/repo", "nonexistent")
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	assert.Equal(t, "branch does not exist", resp.Message)
}

func TestValidateGitBranch_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal error"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ValidateGitBranch("https://github.com/org/repo", "main")
	require.Error(t, err)
	assert.Nil(t, resp)
}

// ---------- Cluster operations ----------

func TestListClusters_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/clusters", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.Cluster]{
			Data:       []types.Cluster{{Base: types.Base{ID: 1}, Name: "dev-cluster", Status: "online"}},
			Total:      1,
			Page:       1,
			PageSize:   20,
			TotalPages: 1,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListClusters()
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "dev-cluster", resp.Data[0].Name)
}

func TestListClusters_Empty(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.Cluster]{
			Data: []types.Cluster{}, Total: 0, Page: 1, PageSize: 20, TotalPages: 0,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListClusters()
	require.NoError(t, err)
	assert.Equal(t, 0, resp.Total)
	assert.Empty(t, resp.Data)
}

func TestListClusters_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "unauthorized"})
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListClusters()
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetCluster_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/clusters/1", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.Cluster{
			Base:      types.Base{ID: 1},
			Name:      "dev-cluster",
			Status:    "online",
			IsDefault: true,
			NodeCount: 3,
		})
	}))
	defer server.Close()

	c := New(server.URL)
	cluster, err := c.GetCluster(1)
	require.NoError(t, err)
	assert.Equal(t, uint(1), cluster.ID)
	assert.Equal(t, "dev-cluster", cluster.Name)
	assert.True(t, cluster.IsDefault)
}

func TestGetCluster_NotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
	}))
	defer server.Close()

	c := New(server.URL)
	cluster, err := c.GetCluster(999)
	require.Error(t, err)
	assert.Nil(t, cluster)
}

func TestGetClusterHealth_Success(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/clusters/1/health/summary", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ClusterHealthSummary{
			Status:    "healthy",
			NodeCount: 3,
			CPUUsage:  "2.5",
			MemUsage:  "4Gi",
			CPUTotal:  "8",
			MemTotal:  "16Gi",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	health, err := c.GetClusterHealth(1)
	require.NoError(t, err)
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, "2.5", health.CPUUsage)
	assert.Equal(t, "16Gi", health.MemTotal)
}

func TestGetClusterHealth_Error(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster unreachable"})
	}))
	defer server.Close()

	c := New(server.URL)
	health, err := c.GetClusterHealth(1)
	require.Error(t, err)
	assert.Nil(t, health)
}

// ---------- malformed / empty response body ----------

func TestClient_MalformedJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json at all`))
	}))
	defer server.Close()

	c := New(server.URL)
	resp, err := c.ListStacks(nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "decoding response")
}

func TestClient_EmptyResponseBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write nothing — empty body
	}))
	defer server.Close()

	c := New(server.URL)
	stack, err := c.GetStack(1)
	require.Error(t, err)
	assert.Nil(t, stack)
	assert.Contains(t, err.Error(), "unexpected empty response body")
}
