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
	"time"

	"github.com/omattsson/stackctl/cli/cmd"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apikeyMockState backs the apikey integration tests.
type apikeyMockState struct {
	mu     sync.Mutex
	nextID int
	keys   map[string]*types.APIKey // keyed by APIKey.ID
	caller string                   // user ID returned by /auth/me
}

func newAPIKeyMockState() *apikeyMockState {
	return &apikeyMockState{nextID: 1, keys: map[string]*types.APIKey{}, caller: "caller-uuid"}
}

func startAPIKeyMockServer(t *testing.T, state *apikeyMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/auth/me" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: state.caller}, Username: "me", Role: "user"})
			return
		}

		trimmed := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
		if trimmed == r.URL.Path {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
			return
		}
		// trimmed = "<userID>/api-keys" OR "<userID>/api-keys/<keyID>"
		parts := strings.SplitN(trimmed, "/", 3)
		if len(parts) < 2 || parts[1] != "api-keys" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
			return
		}
		userID := parts[0]

		switch {
		case len(parts) == 2 && r.Method == http.MethodGet:
			state.mu.Lock()
			out := make([]types.APIKey, 0, len(state.keys))
			for _, k := range state.keys {
				if k.UserID == userID {
					out = append(out, *k)
				}
			}
			state.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)

		case len(parts) == 2 && r.Method == http.MethodPost:
			var req types.CreateAPIKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			if req.ExpiresAt == nil && req.ExpiresInDays == nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "expiry required"})
				return
			}
			state.mu.Lock()
			id := fmt.Sprintf("key-%d", state.nextID)
			state.nextID++
			now := time.Now().UTC()
			var exp *time.Time
			if req.ExpiresInDays != nil {
				t := now.AddDate(0, 0, *req.ExpiresInDays)
				exp = &t
			}
			key := &types.APIKey{
				ID: id, UserID: userID, Name: req.Name,
				Prefix: "abcdef0123456789", CreatedAt: now, ExpiresAt: exp,
			}
			state.keys[id] = key
			state.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{
				ID: key.ID, Name: key.Name, Prefix: key.Prefix,
				RawKey: "sk_" + id + "-raw", CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt,
			})

		case len(parts) == 3 && r.Method == http.MethodDelete:
			keyID := parts[2]
			state.mu.Lock()
			_, exists := state.keys[keyID]
			if exists {
				delete(state.keys, keyID)
			}
			state.mu.Unlock()
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "API key not found"})
				return
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestAPIKeyWorkflow_CreateListRevoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newAPIKeyMockState()
	server := startAPIKeyMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	days := 30
	resp, err := c.CreateAPIKey(state.caller, &types.CreateAPIKeyRequest{Name: "ci", ExpiresInDays: &days})
	require.NoError(t, err)
	require.NotEmpty(t, resp.RawKey)
	assert.True(t, strings.HasPrefix(resp.RawKey, "sk_"), "raw key must carry sk_ prefix")

	keys, err := c.ListAPIKeys(state.caller)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "ci", keys[0].Name)

	require.NoError(t, c.DeleteAPIKey(state.caller, resp.ID))
	keys, err = c.ListAPIKeys(state.caller)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestAPIKeyCobra_CreateListRevoke_QuietBootstrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newAPIKeyMockState()
	server := startAPIKeyMockServer(t, state)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	// Reset root persistent flags before the FIRST Execute() so this test
	// doesn't inherit state from any earlier in-process Cobra test that
	// ran in the same package. ResetFlagsForTest() only resets root
	// persistent flags (--output, --quiet, --api-url, etc.) — subcommand
	// flags like --name/--expires-in-days are scoped to the command and
	// not affected.
	cmd.ResetFlagsForTest()

	// apikey create --name ci --expires-in-days 30 --quiet
	// Verifies the CI-bootstrap contract: stdout is the raw key ONLY,
	// one line, pipeable.
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"apikey", "create", "--name", "ci", "--expires-in-days", "30", "--quiet"})
	require.NoError(t, cmd.Execute())
	rawKey := strings.TrimRight(buf.String(), "\n")
	require.NotEmpty(t, rawKey)
	assert.True(t, strings.HasPrefix(rawKey, "sk_"), "quiet output must start with sk_")
	assert.NotContains(t, rawKey, "\n", "quiet output must be a single line")

	// apikey list — created key visible, raw key never appears (the list
	// endpoint never returns raw_key; this assertion guards against a
	// future regression where the CLI starts including request bodies in
	// table output or accidentally surfaces a cached create-response).
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"apikey", "list"})
	require.NoError(t, cmd.Execute())
	listOut := buf.String()
	assert.Contains(t, listOut, "ci")
	assert.NotContains(t, listOut, "raw_key", "list output must NEVER contain raw_key field")
	assert.NotContains(t, listOut, "sk_", "list output must NEVER contain the sk_ prefix")

	// Locate the created key ID from server state.
	state.mu.Lock()
	var createdID string
	for id := range state.keys {
		createdID = id
	}
	state.mu.Unlock()
	require.NotEmpty(t, createdID)

	// apikey revoke <id> --yes
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"apikey", "revoke", createdID, "--yes"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Revoked")

	state.mu.Lock()
	_, exists := state.keys[createdID]
	state.mu.Unlock()
	assert.False(t, exists)
}
