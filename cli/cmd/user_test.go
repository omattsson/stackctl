package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

func sampleUsers() []types.User {
	return []types.User{
		{
			Base:     types.Base{ID: "u1"},
			Username: "alice",
			Role:     "admin",
			Email:    "alice@example.com",
		},
		{
			Base:           types.Base{ID: "u2"},
			Username:       "svc-ci",
			Role:           "user",
			ServiceAccount: true,
			AuthProvider:   "local",
		},
	}
}

// ---------- user list ----------

func TestUserListCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/users", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(sampleUsers()))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	require.NoError(t, userListCmd.RunE(userListCmd, []string{}))
	out := buf.String()
	assert.Contains(t, out, "USERNAME")
	assert.Contains(t, out, "alice")
	assert.Contains(t, out, "svc-ci")
	assert.Contains(t, out, "admin")
}

func TestUserListCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(sampleUsers()))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, userListCmd.RunE(userListCmd, []string{}))

	var got []types.User
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "alice", got[0].Username)
}

func TestUserListCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(sampleUsers()))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, userListCmd.RunE(userListCmd, []string{}))

	var got []types.User
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
}

func TestUserListCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(sampleUsers()))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, userListCmd.RunE(userListCmd, []string{}))
	assert.Equal(t, "u1\nu2\n", buf.String(), "quiet mode must emit one user ID per line")
}

func TestUserListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]types.User{}))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, userListCmd.RunE(userListCmd, []string{}))
	assert.Contains(t, buf.String(), "No users found")
}

func TestUserListCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin role required"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := userListCmd.RunE(userListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "admin role required")
}

// ---------- user delete ----------

func TestUserDeleteCmd_WithYesFlag(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/users/u1", r.URL.Path)
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, userDeleteCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, userDeleteCmd.Flags(), "yes", "false") })

	require.NoError(t, userDeleteCmd.RunE(userDeleteCmd, []string{"u1"}))
	assert.True(t, deleted)
	assert.Contains(t, buf.String(), "Deleted user u1")
}

func TestUserDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, userDeleteCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, userDeleteCmd.Flags(), "yes", "false") })

	require.NoError(t, userDeleteCmd.RunE(userDeleteCmd, []string{"u1"}))
	assert.Equal(t, "u1\n", buf.String())
}

func TestUserDeleteCmd_SelfDeleteRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Cannot delete your own account"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, userDeleteCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, userDeleteCmd.Flags(), "yes", "false") })

	err := userDeleteCmd.RunE(userDeleteCmd, []string{"self-id"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot delete your own account")
}

// ---------- user disable / enable ----------

func TestUserDisableCmd_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/api/v1/users/u1/disable", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "User disabled successfully"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, userDisableCmd.RunE(userDisableCmd, []string{"u1"}))
	assert.Contains(t, buf.String(), "Disabled user u1")
}

func TestUserEnableCmd_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/api/v1/users/u1/enable", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "User enabled successfully"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, userEnableCmd.RunE(userEnableCmd, []string{"u1"}))
	assert.Contains(t, buf.String(), "Enabled user u1")
}

func TestUserDisableCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, userDisableCmd.RunE(userDisableCmd, []string{"u1"}))
	assert.Equal(t, "u1\n", buf.String())
}

func TestUserDisableCmd_SelfDisableRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Cannot change your own account status"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := userDisableCmd.RunE(userDisableCmd, []string{"self-id"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot change your own account status")
}

// ---------- user reset-password ----------

func TestUserResetPasswordCmd_FromStdin(t *testing.T) {
	var capturedBody types.ResetPasswordRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/api/v1/users/u1/password", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, userResetPasswordCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() { resetFlag(t, userResetPasswordCmd.Flags(), "password-stdin", "false") })

	userResetPasswordCmd.SetIn(strings.NewReader("new-password-strong-enough\n"))
	t.Cleanup(func() { userResetPasswordCmd.SetIn(nil) })

	require.NoError(t, userResetPasswordCmd.RunE(userResetPasswordCmd, []string{"u1"}))
	assert.Equal(t, "new-password-strong-enough", capturedBody.Password)
	assert.Contains(t, buf.String(), "Reset password for user u1")
}

func TestUserResetPasswordCmd_TooShort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("backend must NOT be called when password is too short: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, userResetPasswordCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() { resetFlag(t, userResetPasswordCmd.Flags(), "password-stdin", "false") })

	userResetPasswordCmd.SetIn(strings.NewReader("short\n"))
	t.Cleanup(func() { userResetPasswordCmd.SetIn(nil) })

	err := userResetPasswordCmd.RunE(userResetPasswordCmd, []string{"u1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8")
}

func TestUserResetPasswordCmd_NonLocalRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Cannot reset password for non-local user"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, userResetPasswordCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() { resetFlag(t, userResetPasswordCmd.Flags(), "password-stdin", "false") })

	userResetPasswordCmd.SetIn(strings.NewReader("new-password-strong-enough\n"))
	t.Cleanup(func() { userResetPasswordCmd.SetIn(nil) })

	err := userResetPasswordCmd.RunE(userResetPasswordCmd, []string{"u1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-local")
}

// ---------- auth register ----------

func TestAuthRegisterCmd_HappyPath(t *testing.T) {
	var captured types.RegisterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/auth/register", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.User{
			Base: types.Base{ID: "new-id"}, Username: captured.Username,
			DisplayName: captured.DisplayName, Role: "user", AuthProvider: "local",
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, authRegisterCmd.Flags().Set("username", "alice"))
	require.NoError(t, authRegisterCmd.Flags().Set("display-name", "Alice"))
	require.NoError(t, authRegisterCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() {
		resetFlag(t, authRegisterCmd.Flags(), "username", "")
		resetFlag(t, authRegisterCmd.Flags(), "display-name", "")
		resetFlag(t, authRegisterCmd.Flags(), "password-stdin", "false")
	})

	authRegisterCmd.SetIn(strings.NewReader("strongpass!\n"))
	t.Cleanup(func() { authRegisterCmd.SetIn(nil) })

	require.NoError(t, authRegisterCmd.RunE(authRegisterCmd, []string{}))
	assert.Equal(t, "alice", captured.Username)
	assert.Equal(t, "strongpass!", captured.Password)
	assert.Equal(t, "Alice", captured.DisplayName)
	assert.Contains(t, buf.String(), "alice")
}

func TestAuthRegisterCmd_ShortPasswordRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server must NOT be called when password is too short")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, authRegisterCmd.Flags().Set("username", "alice"))
	require.NoError(t, authRegisterCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() {
		resetFlag(t, authRegisterCmd.Flags(), "username", "")
		resetFlag(t, authRegisterCmd.Flags(), "password-stdin", "false")
	})

	authRegisterCmd.SetIn(strings.NewReader("short\n"))
	t.Cleanup(func() { authRegisterCmd.SetIn(nil) })

	err := authRegisterCmd.RunE(authRegisterCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8")
}

func TestAuthRegisterCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Registration is disabled"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, authRegisterCmd.Flags().Set("username", "alice"))
	require.NoError(t, authRegisterCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() {
		resetFlag(t, authRegisterCmd.Flags(), "username", "")
		resetFlag(t, authRegisterCmd.Flags(), "password-stdin", "false")
	})

	authRegisterCmd.SetIn(strings.NewReader("strongpass!\n"))
	t.Cleanup(func() { authRegisterCmd.SetIn(nil) })

	err := authRegisterCmd.RunE(authRegisterCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "registration is disabled")
}

func TestAuthRegisterCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.User{Base: types.Base{ID: "new-id"}, Username: "alice"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, authRegisterCmd.Flags().Set("username", "alice"))
	require.NoError(t, authRegisterCmd.Flags().Set("password-stdin", "true"))
	t.Cleanup(func() {
		resetFlag(t, authRegisterCmd.Flags(), "username", "")
		resetFlag(t, authRegisterCmd.Flags(), "password-stdin", "false")
	})

	authRegisterCmd.SetIn(strings.NewReader("strongpass!\n"))
	t.Cleanup(func() { authRegisterCmd.SetIn(nil) })

	require.NoError(t, authRegisterCmd.RunE(authRegisterCmd, []string{}))
	assert.Equal(t, "new-id\n", buf.String())
}

// ---------- API error matrix (401/404/500) ----------
//
// Every new user/auth command must surface the documented APIError statuses
// (401/404/500) as a non-nil error from RunE so the cobra harness exits
// non-zero. Each command owns its own test function so the per-command
// flag/stdin plumbing stays linear (per tests.instructions.md), and they all
// reuse the same homogeneous matrix of (status, expected-error-substring).

// apiErrorMatrixCase is a single (status, body) the backend may emit; want
// is a lowercased substring expected in the surfaced error.
type apiErrorMatrixCase struct {
	name   string
	status int
	body   map[string]string
	want   string
}

var apiErrorMatrixCases = []apiErrorMatrixCase{
	{"Unauthorized", http.StatusUnauthorized, map[string]string{"error": "unauthorized"}, "authenticated"},
	{"NotFound", http.StatusNotFound, map[string]string{"error": "user not found"}, "not found"},
	{"InternalServerError", http.StatusInternalServerError, map[string]string{"error": "boom"}, "boom"},
}

// startAPIErrorServer returns an httptest server that always responds with
// the given status and body. Caller is responsible for Close().
func startAPIErrorServer(t *testing.T, tc apiErrorMatrixCase) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tc.status)
		_ = json.NewEncoder(w).Encode(tc.body)
	}))
}

// assertAPIError invokes run() and asserts the returned error surfaces the
// expected error substring. Shared by every per-command matrix test below.
func assertAPIError(t *testing.T, tc apiErrorMatrixCase, run func() error) {
	t.Helper()
	err := run()
	require.Error(t, err, "expected non-nil error for status %d", tc.status)
	assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tc.want),
		"error %q should contain %q", err.Error(), tc.want)
}

func TestUserListCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			assertAPIError(t, tc, func() error {
				return userListCmd.RunE(userListCmd, []string{})
			})
		})
	}
}

func TestUserDeleteCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			require.NoError(t, userDeleteCmd.Flags().Set("yes", "true"))
			t.Cleanup(func() { resetFlag(t, userDeleteCmd.Flags(), "yes", "false") })
			assertAPIError(t, tc, func() error {
				return userDeleteCmd.RunE(userDeleteCmd, []string{"u1"})
			})
		})
	}
}

func TestUserDisableCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			assertAPIError(t, tc, func() error {
				return userDisableCmd.RunE(userDisableCmd, []string{"u1"})
			})
		})
	}
}

func TestUserEnableCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			assertAPIError(t, tc, func() error {
				return userEnableCmd.RunE(userEnableCmd, []string{"u1"})
			})
		})
	}
}

func TestUserResetPasswordCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			require.NoError(t, userResetPasswordCmd.Flags().Set("password-stdin", "true"))
			t.Cleanup(func() { resetFlag(t, userResetPasswordCmd.Flags(), "password-stdin", "false") })
			userResetPasswordCmd.SetIn(strings.NewReader("new-password-strong-enough\n"))
			t.Cleanup(func() { userResetPasswordCmd.SetIn(nil) })
			assertAPIError(t, tc, func() error {
				return userResetPasswordCmd.RunE(userResetPasswordCmd, []string{"u1"})
			})
		})
	}
}

func TestAuthRegisterCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			require.NoError(t, authRegisterCmd.Flags().Set("username", "alice"))
			require.NoError(t, authRegisterCmd.Flags().Set("password-stdin", "true"))
			t.Cleanup(func() {
				resetFlag(t, authRegisterCmd.Flags(), "username", "")
				resetFlag(t, authRegisterCmd.Flags(), "password-stdin", "false")
			})
			authRegisterCmd.SetIn(strings.NewReader("strongpass!\n"))
			t.Cleanup(func() { authRegisterCmd.SetIn(nil) })
			assertAPIError(t, tc, func() error {
				return authRegisterCmd.RunE(authRegisterCmd, []string{})
			})
		})
	}
}
