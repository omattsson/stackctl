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
	assert.Equal(t, "Not authenticated. Run 'stackctl login' first.", apiErr.UserFacingError())
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
