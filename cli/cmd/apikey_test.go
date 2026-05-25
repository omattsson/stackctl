package cmd

import (
	"bytes"
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
	"gopkg.in/yaml.v3"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

const callerID = "me-uuid"

func sampleAPIKeys() []types.APIKey {
	created := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	exp := time.Date(2027, 5, 1, 23, 59, 59, 0, time.UTC)
	return []types.APIKey{
		{ID: "k1", UserID: callerID, Name: "ci", Prefix: "0123456789abcdef", CreatedAt: created, ExpiresAt: &exp},
		{ID: "k2", UserID: callerID, Name: "release", Prefix: "fedcba9876543210", CreatedAt: created, ExpiresAt: &exp},
	}
}

// startAPIKeyServer returns a mock that handles /auth/me + the api-keys
// surface for the test-supplied userID. The user can override individual
// handlers by passing a non-nil override.
func startAPIKeyServer(t *testing.T, list []types.APIKey, override func(w http.ResponseWriter, r *http.Request) bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}, Username: "me", Role: "user"})
		case r.URL.Path == "/api/v1/users/"+callerID+"/api-keys" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(list)
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
}

// ---------- apikey list ----------

func TestAPIKeyListCmd_TableOutput(t *testing.T) {
	server := startAPIKeyServer(t, sampleAPIKeys(), nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	require.NoError(t, apikeyListCmd.RunE(apikeyListCmd, []string{}))
	out := buf.String()
	assert.Contains(t, out, "PREFIX")
	assert.Contains(t, out, "ci")
	assert.Contains(t, out, "release")
	assert.Contains(t, out, "0123456789abcdef")
}

func TestAPIKeyListCmd_JSONOutput(t *testing.T) {
	server := startAPIKeyServer(t, sampleAPIKeys(), nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, apikeyListCmd.RunE(apikeyListCmd, []string{}))
	var got []types.APIKey
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
}

func TestAPIKeyListCmd_YAMLOutput(t *testing.T) {
	server := startAPIKeyServer(t, sampleAPIKeys(), nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, apikeyListCmd.RunE(apikeyListCmd, []string{}))
	var got []types.APIKey
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
}

func TestAPIKeyListCmd_QuietOutput(t *testing.T) {
	server := startAPIKeyServer(t, sampleAPIKeys(), nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, apikeyListCmd.RunE(apikeyListCmd, []string{}))
	assert.Equal(t, "k1\nk2\n", buf.String(), "quiet mode must emit one key ID per line")
}

func TestAPIKeyListCmd_Empty(t *testing.T) {
	server := startAPIKeyServer(t, []types.APIKey{}, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyListCmd.RunE(apikeyListCmd, []string{}))
	assert.Contains(t, buf.String(), "No API keys configured")
}

func TestAPIKeyListCmd_ExplicitUserFlagSkipsAuthMe(t *testing.T) {
	authMeCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/me" {
			authMeCalled = true
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/api/v1/users/other-uuid/api-keys" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(sampleAPIKeys())
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyListCmd.Flags().Set("user", "other-uuid"))
	t.Cleanup(func() { resetFlag(t, apikeyListCmd.Flags(), "user", "") })

	require.NoError(t, apikeyListCmd.RunE(apikeyListCmd, []string{}))
	assert.False(t, authMeCalled, "--user must skip the /auth/me lookup")
}

// ---------- apikey create ----------

func TestAPIKeyCreateCmd_ExpiresInDays(t *testing.T) {
	var captured types.CreateAPIKeyRequest
	created := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	exp := created.AddDate(0, 0, 90)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me":
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}})
		case r.URL.Path == "/api/v1/users/"+callerID+"/api-keys" && r.Method == http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{
				ID: "new-id", Name: captured.Name, Prefix: "abcdef0123456789",
				RawKey: "sk_deadbeef", CreatedAt: created, ExpiresAt: &exp,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-in-days", "90"))
	t.Cleanup(func() {
		resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-in-days", "0")
	})

	require.NoError(t, apikeyCreateCmd.RunE(apikeyCreateCmd, []string{}))
	require.NotNil(t, captured.ExpiresInDays)
	assert.Equal(t, 90, *captured.ExpiresInDays)
	assert.Nil(t, captured.ExpiresAt, "expires-at must not be sent when only expires-in-days was provided")
	assert.Contains(t, buf.String(), "sk_deadbeef")
	assert.Contains(t, buf.String(), "non-retrievable")
}

func TestAPIKeyCreateCmd_ExpiresAt(t *testing.T) {
	var captured types.CreateAPIKeyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me":
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}})
		case r.URL.Path == "/api/v1/users/"+callerID+"/api-keys" && r.Method == http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{ID: "id", RawKey: "sk_xyz"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "release"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-at", "2027-01-01"))
	t.Cleanup(func() {
		resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-at", "")
	})

	require.NoError(t, apikeyCreateCmd.RunE(apikeyCreateCmd, []string{}))
	require.NotNil(t, captured.ExpiresAt)
	assert.Equal(t, "2027-01-01", *captured.ExpiresAt)
	assert.Nil(t, captured.ExpiresInDays)
}

func TestAPIKeyCreateCmd_QuietPrintsOnlyRawKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me":
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}})
		case strings.Contains(r.URL.Path, "/api-keys") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{ID: "id", RawKey: "sk_pipeable-secret"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-in-days", "30"))
	t.Cleanup(func() {
		resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-in-days", "0")
	})

	require.NoError(t, apikeyCreateCmd.RunE(apikeyCreateCmd, []string{}))
	assert.Equal(t, "sk_pipeable-secret\n", buf.String(),
		"quiet mode must emit ONLY the raw key — pipeable into a token file")
}

func TestAPIKeyCreateCmd_JSONIncludesRawKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me":
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}})
		case strings.Contains(r.URL.Path, "/api-keys") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{ID: "id", Name: "ci", RawKey: "sk_json"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-in-days", "30"))
	t.Cleanup(func() {
		resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-in-days", "0")
	})

	require.NoError(t, apikeyCreateCmd.RunE(apikeyCreateCmd, []string{}))
	var got types.CreateAPIKeyResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "sk_json", got.RawKey)
}

func TestAPIKeyCreateCmd_RequiresExpiry(t *testing.T) {
	// Backend must NOT be called when no expiry is provided.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api-keys") {
			t.Fatalf("backend must NOT be called without an expiry: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
	t.Cleanup(func() { resetFlag(t, apikeyCreateCmd.Flags(), "name", "") })

	err := apikeyCreateCmd.RunE(apikeyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expiry is required")
}

func TestAPIKeyCreateCmd_MutuallyExclusiveExpiryFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api-keys") {
			t.Fatalf("backend must NOT be called when both expiry flags are set: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-at", "2027-01-01"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-in-days", "30"))
	t.Cleanup(func() {
		resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-at", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-in-days", "0")
	})

	err := apikeyCreateCmd.RunE(apikeyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestAPIKeyCreateCmd_NegativeDaysRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://localhost:0")
	require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
	require.NoError(t, apikeyCreateCmd.Flags().Set("expires-in-days", "-1"))
	t.Cleanup(func() {
		resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
		resetFlag(t, apikeyCreateCmd.Flags(), "expires-in-days", "0")
	})

	err := apikeyCreateCmd.RunE(apikeyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive integer")
}

// ---------- apikey revoke ----------

func TestAPIKeyRevokeCmd_WithYesFlag(t *testing.T) {
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me":
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}})
		case r.URL.Path == "/api/v1/users/"+callerID+"/api-keys/k1" && r.Method == http.MethodDelete:
			revoked = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyRevokeCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, apikeyRevokeCmd.Flags(), "yes", "false") })

	require.NoError(t, apikeyRevokeCmd.RunE(apikeyRevokeCmd, []string{"k1"}))
	assert.True(t, revoked)
	assert.Contains(t, buf.String(), "Revoked API key k1")
}

func TestAPIKeyRevokeCmd_DryRun(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, apikeyRevokeCmd.Flags().Set("dry-run", "true"))
	t.Cleanup(func() { resetFlag(t, apikeyRevokeCmd.Flags(), "dry-run", "false") })

	require.NoError(t, apikeyRevokeCmd.RunE(apikeyRevokeCmd, []string{"k1"}))
	assert.False(t, called)
	assert.Contains(t, buf.String(), "Would revoke")
}

func TestAPIKeyRevokeCmd_InteractiveDecline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API must not be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	apikeyRevokeCmd.SetIn(strings.NewReader("n\n"))
	apikeyRevokeCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		apikeyRevokeCmd.SetIn(nil)
		apikeyRevokeCmd.SetErr(nil)
	})

	require.NoError(t, apikeyRevokeCmd.RunE(apikeyRevokeCmd, []string{"k1"}))
	assert.Contains(t, buf.String(), "Aborted")
}

func TestAPIKeyRevokeCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/me":
			_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: callerID}})
		case strings.HasSuffix(r.URL.Path, "/api-keys/k1") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, apikeyRevokeCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, apikeyRevokeCmd.Flags(), "yes", "false") })

	require.NoError(t, apikeyRevokeCmd.RunE(apikeyRevokeCmd, []string{"k1"}))
	assert.Equal(t, "k1\n", buf.String())
}

// ---------- API error matrix (401/404/500) ----------
// Reuses the helpers defined in user_test.go (apiErrorMatrixCases,
// startAPIErrorServer, assertAPIError). Per-command tests keep the
// flag/stdin plumbing linear, per tests.instructions.md.

func TestAPIKeyListCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			// --user set so we don't depend on /auth/me; the error server
			// returns the same status for every path.
			require.NoError(t, apikeyListCmd.Flags().Set("user", "x"))
			t.Cleanup(func() { resetFlag(t, apikeyListCmd.Flags(), "user", "") })
			assertAPIError(t, tc, func() error {
				return apikeyListCmd.RunE(apikeyListCmd, []string{})
			})
		})
	}
}

func TestAPIKeyCreateCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			require.NoError(t, apikeyCreateCmd.Flags().Set("name", "ci"))
			require.NoError(t, apikeyCreateCmd.Flags().Set("expires-in-days", "30"))
			require.NoError(t, apikeyCreateCmd.Flags().Set("user", "x"))
			t.Cleanup(func() {
				resetFlag(t, apikeyCreateCmd.Flags(), "name", "")
				resetFlag(t, apikeyCreateCmd.Flags(), "expires-in-days", "0")
				resetFlag(t, apikeyCreateCmd.Flags(), "user", "")
			})
			assertAPIError(t, tc, func() error {
				return apikeyCreateCmd.RunE(apikeyCreateCmd, []string{})
			})
		})
	}
}

func TestAPIKeyRevokeCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			require.NoError(t, apikeyRevokeCmd.Flags().Set("yes", "true"))
			require.NoError(t, apikeyRevokeCmd.Flags().Set("user", "x"))
			t.Cleanup(func() {
				resetFlag(t, apikeyRevokeCmd.Flags(), "yes", "false")
				resetFlag(t, apikeyRevokeCmd.Flags(), "user", "")
			})
			assertAPIError(t, tc, func() error {
				return apikeyRevokeCmd.RunE(apikeyRevokeCmd, []string{"k1"})
			})
		})
	}
}
