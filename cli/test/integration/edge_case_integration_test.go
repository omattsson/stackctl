package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- Expired Token / 401 Handling ----------

func TestEdgeCase_ExpiredTokenHandling(t *testing.T) {
	t.Parallel()

	// Return empty error message so APIError.UserFacingError() is used,
	// which maps 401 → "Not authenticated. Run 'stackctl login' first."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	t.Cleanup(server.Close)

	c := client.New(server.URL)
	c.Token = "expired-jwt-token"

	tests := []struct {
		name string
		fn   func() error
	}{
		{
			name: "ListStacks",
			fn: func() error {
				_, err := c.ListStacks(nil)
				return err
			},
		},
		{
			name: "GetStack",
			fn: func() error {
				_, err := c.GetStack(1)
				return err
			},
		},
		{
			name: "CreateStack",
			fn: func() error {
				_, err := c.CreateStack(&types.CreateStackRequest{
					Name:              "test",
					StackDefinitionID: 1,
				})
				return err
			},
		},
		{
			name: "DeployStack",
			fn: func() error {
				_, err := c.DeployStack(1)
				return err
			},
		},
		{
			name: "ListTemplates",
			fn: func() error {
				_, err := c.ListTemplates(nil)
				return err
			},
		},
		{
			name: "ListDefinitions",
			fn: func() error {
				_, err := c.ListDefinitions(nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.fn()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Not authenticated")
		})
	}
}

// ---------- Network Errors ----------

func TestEdgeCase_NetworkErrors(t *testing.T) {
	t.Parallel()

	// Create a server and immediately close it to get a connection-refused endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := server.URL
	server.Close()

	c := client.New(closedURL)

	tests := []struct {
		name string
		fn   func() error
	}{
		{
			name: "ListStacks",
			fn: func() error {
				_, err := c.ListStacks(nil)
				return err
			},
		},
		{
			name: "GetStack",
			fn: func() error {
				_, err := c.GetStack(1)
				return err
			},
		},
		{
			name: "DeployStack",
			fn: func() error {
				_, err := c.DeployStack(1)
				return err
			},
		},
		{
			name: "Login",
			fn: func() error {
				_, err := c.Login("user", "pass")
				return err
			},
		},
		{
			name: "DeleteStack",
			fn: func() error {
				return c.DeleteStack(1)
			},
		},
		{
			name: "BulkDeploy",
			fn: func() error {
				_, err := c.BulkDeploy([]uint{1, 2, 3})
				return err
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.fn()
			require.Error(t, err, "should return an error, not panic")
			assert.ErrorContains(t, err, "making request:")
		})
	}
}

// ---------- Invalid Input Validation ----------

func TestEdgeCase_InvalidInputValidation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodPost:
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			if name == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "name is required"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(types.StackInstance{
				Base: types.Base{ID: 1},
				Name: name,
			})

		case r.URL.Path == "/api/v1/stack-instances/0" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid stack ID"})

		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/stack-instances/1/overrides/1":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid request body"})
				return
			}
			values, ok := body["values"]
			if !ok || values == nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "values are required"})
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.ValueOverride{
				Base:       types.Base{ID: 10},
				InstanceID: 1,
				ChartID:    1,
				Values:     "{}",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
	t.Cleanup(server.Close)

	c := client.New(server.URL)

	t.Run("CreateStackEmptyName", func(t *testing.T) {
		t.Parallel()
		_, err := c.CreateStack(&types.CreateStackRequest{
			Name:              "",
			StackDefinitionID: 1,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})

	t.Run("GetStackIDZero", func(t *testing.T) {
		t.Parallel()
		_, err := c.GetStack(0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid stack ID")
	})

	t.Run("SetValueOverrideNilValues", func(t *testing.T) {
		t.Parallel()
		_, err := c.SetValueOverride(1, 1, &types.SetValueOverrideRequest{
			Values: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "values are required")
	})

	t.Run("SetValueOverrideEmptyValues", func(t *testing.T) {
		t.Parallel()
		// Empty map (not nil) should succeed
		override, err := c.SetValueOverride(1, 1, &types.SetValueOverrideRequest{
			Values: map[string]interface{}{},
		})
		require.NoError(t, err)
		assert.Equal(t, uint(10), override.ID)
	})
}

// ---------- Concurrent Operations ----------

func TestEdgeCase_ConcurrentOperations(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Artificial delay to simulate real-world latency
		time.Sleep(50 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
				Data: []types.StackInstance{
					{Base: types.Base{ID: 1}, Name: "stack-1", Status: "running"},
					{Base: types.Base{ID: 2}, Name: "stack-2", Status: "running"},
				},
				Total:      2,
				Page:       1,
				PageSize:   20,
				TotalPages: 1,
			})

		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodPost:
			var req types.CreateStackRequest
			json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(types.StackInstance{
				Base:   types.Base{ID: 100},
				Name:   req.Name,
				Status: "draft",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
	t.Cleanup(server.Close)

	c := client.New(server.URL)

	t.Run("ConcurrentListStacks", func(t *testing.T) {
		t.Parallel()

		const concurrency = 10
		var wg sync.WaitGroup
		errs := make([]error, concurrency)
		results := make([]*types.ListResponse[types.StackInstance], concurrency)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				resp, err := c.ListStacks(nil)
				errs[idx] = err
				results[idx] = resp
			}(i)
		}

		wg.Wait()

		for i := 0; i < concurrency; i++ {
			require.NoError(t, errs[i], "goroutine %d should not error", i)
			require.NotNil(t, results[i], "goroutine %d should return a result", i)
			assert.Len(t, results[i].Data, 2, "goroutine %d should get 2 stacks", i)
		}
	})

	t.Run("ConcurrentReadWrite", func(t *testing.T) {
		t.Parallel()

		const concurrency = 5
		var wg sync.WaitGroup
		listErrs := make([]error, concurrency)
		createErrs := make([]error, concurrency)

		// Fire concurrent reads and writes
		for i := 0; i < concurrency; i++ {
			wg.Add(2)
			go func(idx int) {
				defer wg.Done()
				_, err := c.ListStacks(nil)
				listErrs[idx] = err
			}(i)
			go func(idx int) {
				defer wg.Done()
				_, err := c.CreateStack(&types.CreateStackRequest{
					Name:              fmt.Sprintf("concurrent-stack-%d", idx),
					StackDefinitionID: 1,
				})
				createErrs[idx] = err
			}(i)
		}

		wg.Wait()

		for i := 0; i < concurrency; i++ {
			assert.NoError(t, listErrs[i], "list goroutine %d should not error", i)
			assert.NoError(t, createErrs[i], "create goroutine %d should not error", i)
		}
	})
}

// ---------- Large Response Handling ----------

func TestEdgeCase_LargeResponseHandling(t *testing.T) {
	t.Parallel()

	const itemCount = 1000

	// Build a large list of stacks
	stacks := make([]types.StackInstance, itemCount)
	for i := 0; i < itemCount; i++ {
		stacks[i] = types.StackInstance{
			Base:      types.Base{ID: uint(i + 1)},
			Name:      fmt.Sprintf("stack-%d", i+1),
			Status:    "running",
			Owner:     "admin",
			Branch:    "main",
			Namespace: fmt.Sprintf("ns-%d", i+1),
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:       stacks,
			Total:      itemCount,
			Page:       1,
			PageSize:   itemCount,
			TotalPages: 1,
		})
	}))
	t.Cleanup(server.Close)

	c := client.New(server.URL)

	t.Run("ParseLargeList", func(t *testing.T) {
		t.Parallel()
		resp, err := c.ListStacks(nil)
		require.NoError(t, err)
		require.Len(t, resp.Data, itemCount)
		assert.Equal(t, uint(1), resp.Data[0].ID)
		assert.Equal(t, uint(itemCount), resp.Data[itemCount-1].ID)
		assert.Equal(t, "stack-1", resp.Data[0].Name)
		assert.Equal(t, fmt.Sprintf("stack-%d", itemCount), resp.Data[itemCount-1].Name)
	})

	t.Run("LargeListTotal", func(t *testing.T) {
		t.Parallel()
		resp, err := c.ListStacks(nil)
		require.NoError(t, err)
		assert.Equal(t, itemCount, resp.Total)
	})
}

// ---------- Server Error User-Facing Messages ----------

func TestEdgeCase_ServerErrorUserFacingMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantMsg    string
	}{
		{
			name:       "500 Internal Server Error",
			statusCode: http.StatusInternalServerError,
			wantMsg:    "Server error. Check backend logs.",
		},
		{
			name:       "502 Bad Gateway",
			statusCode: http.StatusBadGateway,
			wantMsg:    "Server error. Check backend logs.",
		},
		{
			name:       "503 Service Unavailable",
			statusCode: http.StatusServiceUnavailable,
			wantMsg:    "Server error. Check backend logs.",
		},
		{
			name:       "429 Rate Limited",
			statusCode: http.StatusTooManyRequests,
			wantMsg:    "Rate limited. Try again later.",
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			wantMsg:    "Permission denied.",
		},
		{
			name:       "404 Not Found",
			statusCode: http.StatusNotFound,
			wantMsg:    "not found",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Return empty error message so APIError.Error() falls through
			// to UserFacingError(), which maps status codes to friendly messages.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(types.ErrorResponse{})
			}))
			defer server.Close()

			c := client.New(server.URL)

			// Test with ListStacks
			_, err := c.ListStacks(nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)

			// Test with GetStack
			_, err = c.GetStack(1)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)

			// Test with DeployStack
			_, err = c.DeployStack(1)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}
