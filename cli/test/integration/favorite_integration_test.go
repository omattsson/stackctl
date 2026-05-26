package integration

import (
	"bytes"
	"encoding/json"
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

// favoriteStore is a thread-safe in-memory mock of the favorites backend.
// It enforces the same uniqueness key (user_id, entity_type, entity_id) as
// the real DB so the "add → list → remove → list" workflow round-trips end
// to end. The backend's documented idempotency behaviour is replicated:
// duplicate add returns the existing row with 201; missing remove returns 204.
type favoriteStore struct {
	mu   sync.Mutex
	rows map[string]types.Favorite // key: entityType + ":" + entityID
}

func newFavoriteStore() *favoriteStore { return &favoriteStore{rows: map[string]types.Favorite{}} }

func (s *favoriteStore) key(t, id string) string { return t + ":" + id }

func (s *favoriteStore) list() []types.Favorite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Favorite, 0, len(s.rows))
	for _, f := range s.rows {
		out = append(out, f)
	}
	return out
}

func (s *favoriteStore) add(t, id string) types.Favorite {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.key(t, id)
	if existing, ok := s.rows[k]; ok {
		return existing
	}
	fav := types.Favorite{ID: "fav-" + k, EntityType: t, EntityID: id}
	s.rows[k] = fav
	return fav
}

func (s *favoriteStore) remove(t, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, s.key(t, id))
}

// startFavoriteMockServer wires the in-memory store to the standard
// /api/v1/favorites routes.
func startFavoriteMockServer(t *testing.T, store *favoriteStore) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/favorites":
			_ = json.NewEncoder(w).Encode(store.list())
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/favorites":
			var req types.AddFavoriteRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.EntityType == "" || req.EntityID == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid request"})
				return
			}
			fav := store.add(req.EntityType, req.EntityID)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(fav)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/favorites/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/favorites/"), "/")
			if len(parts) != 2 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store.remove(parts[0], parts[1])
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
}

// TestFavoriteWorkflow_AddListRemoveList exercises the issue's acceptance
// criterion: idempotent add/remove with a real round-trip. add → list (1) →
// add again → list (still 1) → remove → list (0) → remove again → list (0).
func TestFavoriteWorkflow_AddListRemoveList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	store := newFavoriteStore()
	server := startFavoriteMockServer(t, store)
	defer server.Close()

	c := client.New(server.URL)

	// add
	_, err := c.AddFavorite(types.AddFavoriteRequest{EntityType: "definition", EntityID: "42"})
	require.NoError(t, err)

	favs, err := c.ListFavorites()
	require.NoError(t, err)
	require.Len(t, favs, 1)

	// add same — must remain 1
	_, err = c.AddFavorite(types.AddFavoriteRequest{EntityType: "definition", EntityID: "42"})
	require.NoError(t, err)
	favs, err = c.ListFavorites()
	require.NoError(t, err)
	require.Len(t, favs, 1, "duplicate add must be idempotent")

	// remove
	require.NoError(t, c.RemoveFavorite("definition", "42"))
	favs, err = c.ListFavorites()
	require.NoError(t, err)
	require.Empty(t, favs)

	// remove same — must succeed
	require.NoError(t, c.RemoveFavorite("definition", "42"), "remove missing must be idempotent")
}

// TestFavoriteCobra_AddListRemove drives the same workflow through Cobra,
// proving that flag wiring + output formatting + exit codes all behave
// correctly end-to-end inside a single Go test binary.
func TestFavoriteCobra_AddListRemove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	store := newFavoriteStore()
	server := startFavoriteMockServer(t, store)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer
	resetAll := func() {
		cmd.ResetFlagsForTest()
		cmd.ResetFavoriteFlagsForTest()
		buf.Reset()
		cmd.SetOut(&buf)
	}

	// add
	resetAll()
	cmd.SetArgs([]string{"favorite", "add", "--type", "definition", "--id", "42"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Favorited definition 42")

	// list — 1 row
	resetAll()
	cmd.SetArgs([]string{"favorite", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "definition")
	assert.Contains(t, buf.String(), "42")

	// add same again — idempotent
	resetAll()
	cmd.SetArgs([]string{"favorite", "add", "--type", "definition", "--id", "42"})
	require.NoError(t, cmd.Execute())

	resetAll()
	cmd.SetArgs([]string{"favorite", "list", "-o", "json"})
	require.NoError(t, cmd.Execute())
	var got []types.Favorite
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 1, "favorite list must show exactly 1 row after duplicate add")

	// remove
	resetAll()
	cmd.SetArgs([]string{"favorite", "remove", "--type", "definition", "--id", "42"})
	require.NoError(t, cmd.Execute())

	resetAll()
	cmd.SetArgs([]string{"favorite", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "No favorites.")
}

// startGitProvidersMockServer serves a deterministic /git/providers
// response so the integration test for the existing git command group can
// be extended without touching its branches/validate mocks.
func startGitProvidersMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/git/providers":
			_ = json.NewEncoder(w).Encode([]types.GitProvider{
				{Type: "azure_devops", Available: true},
				{Type: "gitlab", Available: false},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestGitProvidersCobra exercises `stackctl git providers` end-to-end
// through Cobra, asserting table + JSON outputs round-trip and the quiet
// mode follows the documented "type names" exception.
func TestGitProvidersCobra(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startGitProvidersMockServer(t)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	// default table
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"git", "providers"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "azure_devops")
	assert.Contains(t, buf.String(), "gitlab")

	// JSON
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"git", "providers", "-o", "json"})
	require.NoError(t, cmd.Execute())
	var got []types.GitProvider
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)

	// Quiet → provider type names (documented exception)
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"git", "providers", "--quiet"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "azure_devops\ngitlab", strings.TrimSpace(buf.String()))
}
