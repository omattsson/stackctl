package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file mutate package-level globals (printer, flagAPIURL,
// favoriteFlag*). They do NOT use t.Parallel().

func sampleFavorites() []types.Favorite {
	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	return []types.Favorite{
		{ID: "f1", UserID: "u1", EntityType: "definition", EntityID: "42", CreatedAt: t1},
		{ID: "f2", UserID: "u1", EntityType: "template", EntityID: "9", CreatedAt: t1},
	}
}

// ---------- favorite list ----------

func TestFavoriteListCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/favorites", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleFavorites())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, favoriteListCmd.RunE(favoriteListCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "ENTITY TYPE")
	assert.Contains(t, out, "definition")
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "template")
}

func TestFavoriteListCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleFavorites())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, favoriteListCmd.RunE(favoriteListCmd, []string{}))

	var got []types.Favorite
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
}

func TestFavoriteListCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleFavorites())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	require.NoError(t, favoriteListCmd.RunE(favoriteListCmd, []string{}))
	assert.Contains(t, buf.String(), "entity_type: definition")
}

func TestFavoriteListCmd_QuietPrintsEntityIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleFavorites())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, favoriteListCmd.RunE(favoriteListCmd, []string{}))
	assert.Equal(t, "42\n9", strings.TrimSpace(buf.String()))
	assert.NotContains(t, buf.String(), "ENTITY TYPE")
}

func TestFavoriteListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, favoriteListCmd.RunE(favoriteListCmd, []string{}))
	assert.Contains(t, buf.String(), "No favorites.")
}

// ---------- favorite add ----------

func TestFavoriteAddCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/favorites", r.URL.Path)
		var req types.AddFavoriteRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "definition", req.EntityType)
		assert.Equal(t, "42", req.EntityID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.Favorite{ID: "f1", EntityType: "definition", EntityID: "42"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	favoriteAddType = "definition"
	favoriteAddID = "42"
	require.NoError(t, favoriteAddCmd.RunE(favoriteAddCmd, []string{}))
	assert.Contains(t, buf.String(), "Favorited definition 42")
}

// TestFavoriteAddCmd_IdempotentReAdd locks in the acceptance criterion:
// re-adding the same favorite must not error. The backend returns the
// existing row with 201; the CLI must surface that as success, not a 5xx.
func TestFavoriteAddCmd_IdempotentReAdd(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.Favorite{ID: "f1", EntityType: "definition", EntityID: "42"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	favoriteAddType = "definition"
	favoriteAddID = "42"
	require.NoError(t, favoriteAddCmd.RunE(favoriteAddCmd, []string{}))
	require.NoError(t, favoriteAddCmd.RunE(favoriteAddCmd, []string{}))
	assert.Equal(t, 2, callCount, "both calls must reach the backend; idempotency is server-side")
}

func TestFavoriteAddCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.Favorite{ID: "f1", EntityType: "definition", EntityID: "42"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	printer.Format = output.FormatJSON
	favoriteAddType = "definition"
	favoriteAddID = "42"
	require.NoError(t, favoriteAddCmd.RunE(favoriteAddCmd, []string{}))

	var got types.Favorite
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "f1", got.ID)
}

func TestFavoriteAddCmd_QuietPrintsEntityID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.Favorite{ID: "f1", EntityType: "definition", EntityID: "42"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	printer.Quiet = true
	favoriteAddType = "definition"
	favoriteAddID = "42"
	require.NoError(t, favoriteAddCmd.RunE(favoriteAddCmd, []string{}))
	assert.Equal(t, "42", strings.TrimSpace(buf.String()))
}

func TestFavoriteAddCmd_InvalidTypeRejectedClientSide(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	favoriteAddType = "garbage"
	favoriteAddID = "42"
	err := favoriteAddCmd.RunE(favoriteAddCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition, instance, template")
	assert.False(t, called, "API must not be called for invalid --type")
}

func TestFavoriteAddCmd_MissingFlagsRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	err := favoriteAddCmd.RunE(favoriteAddCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--type")
}

// ---------- favorite remove ----------

func TestFavoriteRemoveCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/favorites/definition/42", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	favoriteRemoveType = "definition"
	favoriteRemoveID = "42"
	require.NoError(t, favoriteRemoveCmd.RunE(favoriteRemoveCmd, []string{}))
	assert.Contains(t, buf.String(), "Removed favorite definition 42")
}

// TestFavoriteRemoveCmd_IdempotentMissing locks the second half of the
// acceptance criterion: removing a non-existent favorite must succeed
// (backend returns 204 — not 404 — for unknown rows).
func TestFavoriteRemoveCmd_IdempotentMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	favoriteRemoveType = "definition"
	favoriteRemoveID = "missing"
	require.NoError(t, favoriteRemoveCmd.RunE(favoriteRemoveCmd, []string{}))
}

func TestFavoriteRemoveCmd_QuietPrintsID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	printer.Quiet = true
	favoriteRemoveType = "definition"
	favoriteRemoveID = "42"
	require.NoError(t, favoriteRemoveCmd.RunE(favoriteRemoveCmd, []string{}))
	assert.Equal(t, "42", strings.TrimSpace(buf.String()))
}

func TestFavoriteRemoveCmd_InvalidTypeRejectedClientSide(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetFavoriteFlagsForTest()
	defer resetFavoriteFlagsForTest()
	favoriteRemoveType = "garbage"
	favoriteRemoveID = "42"
	err := favoriteRemoveCmd.RunE(favoriteRemoveCmd, []string{})
	require.Error(t, err)
	assert.False(t, called)
}

// ---------- API error matrix ----------

func TestFavoriteCmds_APIError401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"login required"}`))
	}))
	defer server.Close()

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"list", func() error { return favoriteListCmd.RunE(favoriteListCmd, []string{}) }},
		{"add", func() error {
			favoriteAddType = "definition"
			favoriteAddID = "42"
			return favoriteAddCmd.RunE(favoriteAddCmd, []string{})
		}},
		{"remove", func() error {
			favoriteRemoveType = "definition"
			favoriteRemoveID = "42"
			return favoriteRemoveCmd.RunE(favoriteRemoveCmd, []string{})
		}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_ = setupStackTestCmd(t, server.URL)
			resetFavoriteFlagsForTest()
			defer resetFavoriteFlagsForTest()
			err := tc.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Not authenticated")
		})
	}
}

// ---------- validateFavoriteType ----------

func TestValidateFavoriteType(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{in: "definition"},
		{in: "instance"},
		{in: "template"},
		{in: "", wantErr: true},
		{in: "stack", wantErr: true},
		{in: "Definition", wantErr: true}, // case-sensitive
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			err := validateFavoriteType(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
