package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests use the package globals (cfg, flags) and t.Setenv, so they do
// not run in parallel, like the other command tests.

func testJWT(t *testing.T, sub string, exp time.Time) string {
	t.Helper()
	enc := base64.RawURLEncoding
	payload, err := json.Marshal(map[string]interface{}{"sub": sub, "exp": exp.Unix()})
	require.NoError(t, err)
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(payload) + ".sig"
}

func readStored(t *testing.T) *storedToken {
	t.Helper()
	st, err := tokenStore().Load()
	require.NoError(t, err)
	return st
}

func TestLoginCmd_StoresRefreshToken(t *testing.T) {
	exp := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	jwt := testJWT(t, "alice", exp)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/login", r.URL.Path)
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-abc", Path: "/api/v1/auth", HttpOnly: true})
		// No expires_at in the response: the expiry comes from the JWT.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"token": jwt, "user": types.User{Username: "alice"}})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	require.NoError(t, loginCmd.Flags().Set("username", "alice"))
	require.NoError(t, loginCmd.Flags().Set("password", "secret123"))

	require.NoError(t, loginCmd.RunE(loginCmd, nil))
	assert.Contains(t, buf.String(), "Logged in as alice")
	assert.NotContains(t, buf.String(), "refresh-abc")

	st := readStored(t)
	require.NotNil(t, st)
	assert.Equal(t, jwt, st.Token)
	assert.Equal(t, "refresh-abc", st.RefreshToken)
	assert.Equal(t, server.URL, st.APIURL, "API URL of the login is stored")
	assert.True(t, exp.Equal(st.ExpiresAt), "expiry from JWT exp claim")

	if runtime.GOOS != "windows" {
		path, _ := tokenStore().Path()
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}

func TestLogoutCmd_SendsRefreshCookie(t *testing.T) {
	var gotCookie, gotBearer string
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/logout", r.URL.Path)
		calls.Add(1)
		if ck, err := r.Cookie("refresh_token"); err == nil {
			gotCookie = ck.Value
		}
		gotBearer = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("jwt-1", "refresh-1", "alice", time.Now().Add(time.Minute)))
	var errBuf bytes.Buffer
	logoutCmd.SetErr(&errBuf)
	t.Cleanup(func() { logoutCmd.SetErr(nil) })

	require.NoError(t, logoutCmd.RunE(logoutCmd, nil))
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, "refresh-1", gotCookie)
	assert.Equal(t, "Bearer jwt-1", gotBearer)
	assert.Empty(t, errBuf.String())
	assert.Contains(t, buf.String(), "Logged out")
	assert.Nil(t, readStored(t), "token file removed")
}

func TestLogoutCmd_ServerErrorWarnsAndDeletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"Internal server error"}`))
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("jwt-1", "refresh-1", "alice", time.Now().Add(time.Minute)))
	var errBuf bytes.Buffer
	logoutCmd.SetErr(&errBuf)
	t.Cleanup(func() { logoutCmd.SetErr(nil) })

	require.NoError(t, logoutCmd.RunE(logoutCmd, nil))
	assert.Contains(t, errBuf.String(), "Warning: could not end the session on the server")
	assert.NotContains(t, errBuf.String(), "refresh-1")
	assert.Contains(t, buf.String(), "Logged out")
	assert.Nil(t, readStored(t), "token file removed even after a server error")
}

func TestLoadToken_ExpiredWithRefreshToken(t *testing.T) {
	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}
	require.NoError(t, saveSession("expired-jwt", "refresh-1", "alice", time.Now().Add(-time.Hour)))

	token, warning, err := loadToken()
	require.NoError(t, err, "an expired access token is renewed, not an error")
	assert.Equal(t, "expired-jwt", token)
	assert.Empty(t, warning)
}

func TestLoadToken_OldFileWithoutRefreshToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)
	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "tokens"), 0700))
	exp := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	raw := fmt.Sprintf(`{"token":"old-jwt","expires_at":%q,"username":"bob"}`, exp)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tokens", "test.json"), []byte(raw), 0600))

	token, warning, err := loadToken()
	require.NoError(t, err)
	assert.Equal(t, "old-jwt", token)
	assert.Contains(t, warning, "cannot be renewed")
	assert.Contains(t, warning, "12 hours")
}

func TestNewClient_SetsTokenStore(t *testing.T) {
	setupLoginTestCmd(t, "http://127.0.0.1:1")
	require.NoError(t, saveSession("jwt", "refresh-1", "alice", time.Now().Add(time.Hour)))

	c, err := newClient()
	require.NoError(t, err)
	assert.NotNil(t, c.Tokens, "session renewal enabled for a stored login")

	flagAPIKey = "sk_test"
	t.Cleanup(func() { flagAPIKey = "" })
	c, err = newClient()
	require.NoError(t, err)
	assert.Nil(t, c.Tokens, "no renewal with an API key")
}

func TestContextEnv_RenewsExpiringToken(t *testing.T) {
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	var refreshCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/refresh", r.URL.Path)
		refreshCalls.Add(1)
		ck, err := r.Cookie("refresh_token")
		require.NoError(t, err)
		assert.Equal(t, "refresh-1", ck.Value)
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-2", Path: "/api/v1/auth"})
		_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
	}))
	defer server.Close()

	withContextConfig(t, &config.Context{APIURL: server.URL})
	require.NoError(t, saveSession("old-jwt", "refresh-1", "alice", time.Now().Add(20*time.Second)))

	env := contextEnv(os.Environ())

	v, _ := envValue(env, "STACKCTL_TOKEN")
	assert.Equal(t, newJWT, v)
	assert.Equal(t, int32(1), refreshCalls.Load())
	st := readStored(t)
	assert.Equal(t, newJWT, st.Token)
	assert.Equal(t, "refresh-2", st.RefreshToken)
}

func TestContextEnv_EndedSessionLeavesTokenOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Refresh token expired"}`))
	}))
	defer server.Close()

	withContextConfig(t, &config.Context{APIURL: server.URL})
	oldQuiet := flagQuiet
	flagQuiet = true
	t.Cleanup(func() { flagQuiet = oldQuiet })
	require.NoError(t, saveSession("old-jwt", "refresh-1", "alice", time.Now().Add(-time.Minute)))

	env := contextEnv(os.Environ())

	_, hasToken := envValue(env, "STACKCTL_TOKEN")
	assert.False(t, hasToken)
	assert.Nil(t, readStored(t), "ended session is removed")
}

func TestLoginCmd_RevokesPreviousSession(t *testing.T) {
	newJWT := testJWT(t, "new", time.Now().Add(15*time.Minute))
	var logoutCookie, logoutBearer string
	var logoutCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-new", Path: "/api/v1/auth"})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"token": newJWT, "user": types.User{Username: "alice"}})
		case "/api/v1/auth/logout":
			logoutCalls.Add(1)
			if ck, err := r.Cookie("refresh_token"); err == nil {
				logoutCookie = ck.Value
			}
			logoutBearer = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusInternalServerError) // errors are ignored
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("old-jwt", "refresh-old", "alice", time.Now().Add(time.Minute)))
	require.NoError(t, loginCmd.Flags().Set("username", "alice"))
	require.NoError(t, loginCmd.Flags().Set("password", "secret123"))

	require.NoError(t, loginCmd.RunE(loginCmd, nil))
	assert.Equal(t, int32(1), logoutCalls.Load())
	assert.Equal(t, "refresh-old", logoutCookie)
	assert.Equal(t, "Bearer old-jwt", logoutBearer)

	st := readStored(t)
	assert.Equal(t, newJWT, st.Token)
	assert.Equal(t, "refresh-new", st.RefreshToken)
}

func TestLoginCmd_SaveFailureKeepsOldSession(t *testing.T) {
	newJWT := testJWT(t, "new", time.Now().Add(15*time.Minute))
	var logoutCookies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-new", Path: "/api/v1/auth"})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"token": newJWT, "user": types.User{Username: "alice"}})
		case "/api/v1/auth/logout":
			if ck, err := r.Cookie("refresh_token"); err == nil {
				logoutCookies = append(logoutCookies, ck.Value)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("old-jwt", "refresh-old", "alice", time.Now().Add(time.Minute)))
	oldSave := storeSave
	storeSave = func(*config.TokenStore, *storedToken) error { return fmt.Errorf("disk full") }
	t.Cleanup(func() { storeSave = oldSave })
	require.NoError(t, loginCmd.Flags().Set("username", "alice"))
	require.NoError(t, loginCmd.Flags().Set("password", "secret123"))

	err := loginCmd.RunE(loginCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "saving token")
	assert.Equal(t, []string{"refresh-new"}, logoutCookies, "only the new, unsaved session is revoked")

	st := readStored(t)
	require.NotNil(t, st)
	assert.Equal(t, "refresh-old", st.RefreshToken, "old session kept")
}

func TestContextEnv_OldFileWithoutAPIURLRenewsForContextURL(t *testing.T) {
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	var refreshCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
	}))
	defer server.Close()

	withContextConfig(t, &config.Context{APIURL: server.URL})
	raw := fmt.Sprintf(`{"token":"old-jwt","refresh_token":"refresh-1","expires_at":%q}`, time.Now().Add(10*time.Second).UTC().Format(time.RFC3339))
	dir := filepath.Join(os.Getenv("STACKCTL_CONFIG_DIR"), "tokens")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dev-cluster.json"), []byte(raw), 0600))

	env := contextEnv(os.Environ())
	v, _ := envValue(env, "STACKCTL_TOKEN")
	assert.Equal(t, newJWT, v)
	assert.Equal(t, int32(1), refreshCalls.Load())
}

func TestContextEnv_NoRenewalForOtherAPIURL(t *testing.T) {
	var refreshCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	withContextConfig(t, &config.Context{APIURL: "https://login.example.dev"})
	require.NoError(t, saveSession("old-jwt", "refresh-1", "alice", time.Now().Add(10*time.Second)))
	flagAPIURL = server.URL // a different API URL for this command

	env := contextEnv(os.Environ())
	v, _ := envValue(env, "STACKCTL_TOKEN")
	assert.Equal(t, "old-jwt", v, "token unchanged; refresh token not sent")
	assert.Equal(t, int32(0), refreshCalls.Load())
}
