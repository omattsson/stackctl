package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/omattsson/stackctl/cli/cmd"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userMockState backs the user/auth admin integration tests.
type userMockState struct {
	mu     sync.Mutex
	nextID int
	users  map[string]*types.User
}

func newUserMockState() *userMockState {
	return &userMockState{nextID: 1, users: map[string]*types.User{}}
}

func startUserMockServer(t *testing.T, state *userMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/auth/register" && r.Method == http.MethodPost:
			var req types.RegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			if req.Username == "" || req.Password == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "username/password required"})
				return
			}
			state.mu.Lock()
			id := fmt.Sprintf("user-%d", state.nextID)
			state.nextID++
			user := &types.User{
				Base:           types.Base{ID: id},
				Username:       req.Username,
				DisplayName:    req.DisplayName,
				Role:           "user",
				AuthProvider:   "local",
				ServiceAccount: req.ServiceAccount,
			}
			if req.Role != "" {
				user.Role = req.Role
			}
			state.users[id] = user
			state.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(user)

		case r.URL.Path == "/api/v1/users" && r.Method == http.MethodGet:
			state.mu.Lock()
			out := make([]types.User, 0, len(state.users))
			for _, u := range state.users {
				out = append(out, *u)
			}
			state.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)

		default:
			trimmed := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
			if trimmed == r.URL.Path || trimmed == "" {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
				return
			}
			id := trimmed
			action := ""
			if i := strings.Index(id, "/"); i >= 0 {
				id, action = id[:i], id[i+1:]
			}
			state.mu.Lock()
			user, exists := state.users[id]
			state.mu.Unlock()
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "user not found"})
				return
			}

			switch action {
			case "":
				if r.Method != http.MethodDelete {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				state.mu.Lock()
				delete(state.users, id)
				state.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)

			case "disable":
				if r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				state.mu.Lock()
				user.Disabled = true
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "User disabled successfully"})

			case "enable":
				if r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				state.mu.Lock()
				user.Disabled = false
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "User enabled successfully"})

			case "password":
				if r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var req types.ResetPasswordRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				if len(req.Password) < 8 {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Password must be at least 8 characters"})
					return
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "Password reset"})

			default:
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "unknown action: " + action})
			}
		}
	}))
}

func TestUserWorkflow_RegisterListDisableEnableDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newUserMockState()
	server := startUserMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// register
	user, err := c.Register(&types.RegisterRequest{Username: "alice", Password: "strongpass!"})
	require.NoError(t, err)
	require.NotEmpty(t, user.ID)

	// list — caller sees the new user
	users, err := c.ListUsers()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].Username)

	// disable → enable round-trip
	require.NoError(t, c.DisableUser(user.ID))
	state.mu.Lock()
	assert.True(t, state.users[user.ID].Disabled)
	state.mu.Unlock()

	require.NoError(t, c.EnableUser(user.ID))
	state.mu.Lock()
	assert.False(t, state.users[user.ID].Disabled)
	state.mu.Unlock()

	// reset-password
	require.NoError(t, c.ResetUserPassword(user.ID, "newstrongpass!"))

	// delete
	require.NoError(t, c.DeleteUser(user.ID))
	users, err = c.ListUsers()
	require.NoError(t, err)
	assert.Empty(t, users)
}

func TestUserCobra_AuthRegisterThenUserList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newUserMockState()
	server := startUserMockServer(t, state)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	// auth register --username alice --password-stdin
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader("strongpass!\n"))
	cmd.SetArgs([]string{"auth", "register", "--username", "alice", "--password-stdin"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "alice")

	// user list — should now include alice
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"user", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "alice")
}
