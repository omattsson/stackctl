package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeJWT returns an unsigned JWT with the given exp claim and a unique
// subject, so that two tokens never compare equal.
func makeJWT(t *testing.T, sub string, exp time.Time) string {
	t.Helper()
	return makeJWTClaims(t, map[string]interface{}{"sub": sub, "exp": exp.Unix()})
}

// makeJWTIssued returns an unsigned JWT with iat and exp claims.
func makeJWTIssued(t *testing.T, sub string, iat, exp time.Time) string {
	t.Helper()
	return makeJWTClaims(t, map[string]interface{}{"sub": sub, "iat": iat.Unix(), "exp": exp.Unix()})
}

func makeJWTClaims(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return header + "." + enc.EncodeToString(payload) + ".sig"
}

// sessionServer fakes the auth endpoints of the backend. It rotates the
// refresh token like the backend: a refresh token is valid once.
type sessionServer struct {
	t *testing.T

	mu           sync.Mutex
	validAccess  map[string]bool
	validRefresh string
	nextID       int

	refreshCalls atomic.Int32
	meCalls      atomic.Int32
	logoutCalls  atomic.Int32

	// Behaviour switches.
	refreshStatus int           // when non-zero, refresh answers with this status
	graceNoCookie bool          // refresh answers without Set-Cookie
	refreshDelay  time.Duration // refresh waits before it answers
	meAlways401   bool          // /auth/me rejects every token
	clockOffset   time.Duration // server clock minus local clock

	lastLogoutCookie string
	lastLogoutBearer string
}

func newSessionServer(t *testing.T) (*sessionServer, *httptest.Server) {
	t.Helper()
	s := &sessionServer{t: t, validAccess: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *sessionServer) issueAccess() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	iat := time.Now().Add(s.clockOffset)
	tok := makeJWTIssued(s.t, fmt.Sprintf("access-%d", s.nextID), iat, iat.Add(15*time.Minute))
	s.validAccess[tok] = true
	return tok
}

func (s *sessionServer) setRefresh(v string) {
	s.mu.Lock()
	s.validRefresh = v
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *sessionServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/auth/login":
		tok := s.issueAccess()
		s.setRefresh("refresh-login")
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-login", Path: "/api/v1/auth", HttpOnly: true})
		writeJSON(w, http.StatusOK, map[string]interface{}{"token": tok, "user": map[string]string{"username": "alice"}})

	case "/api/v1/auth/refresh":
		s.refreshCalls.Add(1)
		assert.Empty(s.t, r.Header.Get("Authorization"), "refresh must not send the access token")
		if s.refreshDelay > 0 {
			time.Sleep(s.refreshDelay)
		}
		if s.refreshStatus != 0 {
			writeJSON(w, s.refreshStatus, map[string]string{"error": "Session idle timeout exceeded"})
			return
		}
		ck, err := r.Cookie("refresh_token")
		s.mu.Lock()
		ok := err == nil && ck.Value == s.validRefresh
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid refresh token"})
			return
		}
		tok := s.issueAccess()
		if !s.graceNoCookie {
			next := fmt.Sprintf("refresh-%d", s.refreshCalls.Load())
			s.setRefresh(next)
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: next, Path: "/api/v1/auth", HttpOnly: true})
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": tok})

	case "/api/v1/auth/logout":
		s.logoutCalls.Add(1)
		if ck, err := r.Cookie("refresh_token"); err == nil {
			s.lastLogoutCookie = ck.Value
		}
		s.lastLogoutBearer = r.Header.Get("Authorization")
		writeJSON(w, http.StatusOK, map[string]string{"message": "Logged out successfully"})

	case "/api/v1/auth/me":
		s.meCalls.Add(1)
		bearer := r.Header.Get("Authorization")
		s.mu.Lock()
		ok := len(bearer) > 7 && s.validAccess[bearer[7:]]
		s.mu.Unlock()
		if !ok || s.meAlways401 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": "u1", "username": "alice"})

	default:
		http.NotFound(w, r)
	}
}

// newSessionClient returns a client with a token store in a temp dir that
// holds the given token and refresh token.
func newSessionClient(t *testing.T, url string, st *config.StoredToken) (*Client, *config.TokenStore) {
	t.Helper()
	store := &config.TokenStore{Context: "test", Dir: filepath.Join(t.TempDir(), "tokens")}
	if st != nil {
		require.NoError(t, store.Save(st))
	}
	c := New(url)
	c.RetryBackoff = []time.Duration{}
	c.Tokens = store
	if st != nil {
		c.Token = st.Token
	}
	return c, store
}

func TestLoginSession_ReturnsRefreshCookie(t *testing.T) {
	t.Parallel()
	_, srv := newSessionServer(t)
	c := New(srv.URL)

	resp, refresh, err := c.LoginSession("alice", "secret")
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "refresh-login", refresh)
	assert.Equal(t, resp.Token, c.Token)
}

func TestLoginSession_NoCookie(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"token": "jwt"})
	}))
	defer srv.Close()

	_, refresh, err := New(srv.URL).LoginSession("alice", "secret")
	require.NoError(t, err)
	assert.Empty(t, refresh)
}

func TestLoginSession_WrongPasswordDoesNotRenew(t *testing.T) {
	t.Parallel()
	var refreshCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/refresh" {
			refreshCalls.Add(1)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid credentials"})
	}))
	defer srv.Close()

	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: "jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)})
	_, _, err := c.LoginSession("alice", "wrong")
	require.Error(t, err)
	assert.Equal(t, int32(0), refreshCalls.Load())
}

func TestRenew_BeforeExpiry(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	old := s.issueAccess()
	s.setRefresh("r1")
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: old, RefreshToken: "r1", Username: "alice", ExpiresAt: time.Now().Add(30 * time.Second),
	})

	_, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, int32(1), s.refreshCalls.Load())
	assert.Equal(t, int32(1), s.meCalls.Load(), "no 401 round trip")
	assert.NotEqual(t, old, c.Token)

	st, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, c.Token, st.Token)
	assert.Equal(t, "refresh-1", st.RefreshToken, "rotated refresh token is stored")
	assert.Equal(t, "alice", st.Username, "username is kept")
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), st.ExpiresAt, 5*time.Second)
}

func TestRenew_NotNeededWhenFresh(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	tok := s.issueAccess()
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: tok, RefreshToken: "r1", ExpiresAt: time.Now().Add(10 * time.Minute)})

	_, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, int32(0), s.refreshCalls.Load())
}

func TestRenew_On401RetriesOnce(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	// The file says the token is fresh, but the server rejects it.
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: "revoked-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, "alice", user.Username)
	assert.Equal(t, int32(1), s.refreshCalls.Load())
	assert.Equal(t, int32(2), s.meCalls.Load())

	st, _ := store.Load()
	assert.Equal(t, c.Token, st.Token)
}

func TestRenew_On401NoLoop(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	s.meAlways401 = true
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: "jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	_, err := c.Whoami()
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Equal(t, int32(1), s.refreshCalls.Load(), "one renewal only")
	assert.Equal(t, int32(2), s.meCalls.Load(), "one retry only")

	st, _ := store.Load()
	require.NotNil(t, st, "a successful renewal keeps the session")
}

func TestRenew_SessionEndedDeletesTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
	}{
		{"401", http.StatusUnauthorized},
		{"403", http.StatusForbidden},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, srv := newSessionServer(t)
			s.refreshStatus = tt.status
			c, store := newSessionClient(t, srv.URL, &config.StoredToken{
				Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute),
			})

			_, err := c.Whoami()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Not authenticated. Run 'stackctl login' first.")
			assert.Equal(t, int32(0), s.meCalls.Load())

			st, err := store.Load()
			require.NoError(t, err)
			assert.Nil(t, st, "tokens are deleted")
			assert.Empty(t, c.Token)
		})
	}
}

func TestRenew_SessionEndedOn401Path(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.refreshStatus = http.StatusUnauthorized
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: "revoked-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	_, err := c.Whoami()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Run 'stackctl login'")
	assert.Equal(t, int32(1), s.meCalls.Load(), "no retry after a failed renewal")
	st, _ := store.Load()
	assert.Nil(t, st)
}

func TestRenew_ServerErrorKeepsTokens(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.refreshStatus = http.StatusInternalServerError
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute),
	})

	_, err := c.Whoami()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "renewing session")
	assert.NotContains(t, err.Error(), "stackctl login")

	st, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, "expired-jwt", st.Token)
	assert.Equal(t, "r1", st.RefreshToken)
}

func TestRenew_NetworkErrorKeepsTokens(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused from now on

	c, store := newSessionClient(t, url, &config.StoredToken{
		Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute),
	})
	_, err := c.Whoami()
	require.Error(t, err)

	st, _ := store.Load()
	require.NotNil(t, st)
	assert.Equal(t, "r1", st.RefreshToken)
}

func TestRenew_FailureWithValidTokenContinues(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.refreshStatus = http.StatusBadGateway
	tok := s.issueAccess()
	// Expires within the margin, but has not expired yet.
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: tok, RefreshToken: "r1", ExpiresAt: time.Now().Add(30 * time.Second),
	})

	_, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, tok, c.Token)
	st, _ := store.Load()
	assert.Equal(t, "r1", st.RefreshToken)
}

func TestRenew_GraceResponseKeepsRefreshToken(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	s.graceNoCookie = true
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute),
	})

	_, err := c.Whoami()
	require.NoError(t, err)
	st, _ := store.Load()
	assert.Equal(t, "r1", st.RefreshToken)
	assert.Equal(t, c.Token, st.Token)
	assert.NotEqual(t, "expired-jwt", st.Token)
}

func TestRenew_OldTokenFileWithoutRefreshToken(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	tok := s.issueAccess()

	// Fresh token, no refresh token: works as before.
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: tok, ExpiresAt: time.Now().Add(30 * time.Second)})
	_, err := c.Whoami()
	require.NoError(t, err)

	// A 401 is returned unchanged and the file is kept.
	c2, store := newSessionClient(t, srv.URL, &config.StoredToken{Token: "revoked", ExpiresAt: time.Now().Add(time.Hour)})
	_, err = c2.Whoami()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not authenticated")
	assert.Equal(t, int32(0), s.refreshCalls.Load())
	st, _ := store.Load()
	assert.NotNil(t, st)
}

func TestRenew_APIKeyNeverRenews(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: "jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)})
	c.APIKey = "sk_test"

	_, err := c.Whoami()
	require.Error(t, err)
	assert.Equal(t, int32(0), s.refreshCalls.Load())
}

// TestRenew_ConcurrentClientsOneAPICall simulates two stackctl processes:
// two clients share one token file. The server rotates the refresh token,
// so a second refresh with the old token would fail.
func TestRenew_ConcurrentClientsOneAPICall(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	s.refreshDelay = 200 * time.Millisecond
	dir := filepath.Join(t.TempDir(), "tokens")
	seed := &config.TokenStore{Context: "test", Dir: dir}
	require.NoError(t, seed.Save(&config.StoredToken{Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)}))

	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		c := New(srv.URL)
		c.Tokens = &config.TokenStore{Context: "test", Dir: dir}
		c.Token = "expired-jwt"
		wg.Add(1)
		go func(i int, c *Client) {
			defer wg.Done()
			_, errs[i] = c.Whoami()
		}(i, c)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "client %d", i)
	}
	assert.Equal(t, int32(1), s.refreshCalls.Load(), "one refresh call for all clients")
	st, _ := seed.Load()
	assert.Equal(t, "refresh-1", st.RefreshToken)
}

// TestRenew_ConcurrentGoroutinesOneClient runs requests on one client from
// several goroutines (run with -race).
func TestRenew_ConcurrentGoroutinesOneClient(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	s.refreshDelay = 100 * time.Millisecond
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Whoami()
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), s.refreshCalls.Load())
}

func TestEnsureFreshToken(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: "old", RefreshToken: "r1", ExpiresAt: time.Now().Add(10 * time.Second)})

	tok, err := c.EnsureFreshToken()
	require.NoError(t, err)
	assert.NotEqual(t, "old", tok)
	assert.Equal(t, int32(1), s.refreshCalls.Load())

	// Fresh now: no second call.
	tok2, err := c.EnsureFreshToken()
	require.NoError(t, err)
	assert.Equal(t, tok, tok2)
	assert.Equal(t, int32(1), s.refreshCalls.Load())
}

func TestLogout_SendsCookieAndBearer(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	c := New(srv.URL)
	c.Token = "jwt-1"

	require.NoError(t, c.Logout("r1"))
	assert.Equal(t, int32(1), s.logoutCalls.Load())
	assert.Equal(t, "r1", s.lastLogoutCookie)
	assert.Equal(t, "Bearer jwt-1", s.lastLogoutBearer)
}

func TestLogout_ServerError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
	}))
	defer srv.Close()

	err := New(srv.URL).Logout("r1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Server error")
}

func TestRenew_DebugOutputHidesTokens(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1-secret-value")
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: "expired-jwt-secret", RefreshToken: "r1-secret-value", ExpiresAt: time.Now().Add(-time.Minute)})
	var buf bytes.Buffer
	c.Debug = true
	c.DebugWriter = &buf

	_, err := c.Whoami()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "/api/v1/auth/refresh")
	assert.NotContains(t, buf.String(), "r1-secret-value")
	assert.NotContains(t, buf.String(), "refresh-1")
	assert.NotContains(t, buf.String(), c.Token)
}

func TestIsSessionPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/auth/login", true},
		{"/api/v1/auth/refresh", true},
		{"/api/v1/auth/logout", true},
		{"/api/v1/auth/logout-all", true},
		{"/api/v1/auth/oidc/cli-token", true},
		{"/api/v1/auth/login?x=1", true},
		{"/api/v1/auth/me", false},
		{"/api/v1/auth/register", false},
		{"/api/v1/stack-instances", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSessionPath(tt.path))
		})
	}
}

func TestTokenExpiry(t *testing.T) {
	t.Parallel()
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	got, err := TokenExpiry(makeJWT(t, "x", exp))
	require.NoError(t, err)
	assert.True(t, exp.Equal(got))

	_, err = TokenExpiry("not-a-jwt")
	assert.Error(t, err)
}

// TestRenew_ClockSkewNoRefreshStorm: the local clock is 20 minutes ahead
// of the server. By the local clock every new token (15 minutes) has
// already expired, so a comparison with exp would renew on every request.
func TestRenew_ClockSkewNoRefreshStorm(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.clockOffset = -20 * time.Minute
	s.setRefresh("r1")
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{
		Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute),
	})

	for i := 0; i < 5; i++ {
		_, err := c.Whoami()
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), s.refreshCalls.Load(), "one renewal, no storm")

	st, _ := store.Load()
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), st.ExpiresAt, 5*time.Second,
		"expiry is receive time + token lifetime on the local clock")
}

func TestLocalExpiry(t *testing.T) {
	t.Parallel()
	received := time.Now()
	serverNow := received.Add(-20 * time.Minute)

	got, err := LocalExpiry(makeJWTIssued(t, "x", serverNow, serverNow.Add(15*time.Minute)), received)
	require.NoError(t, err)
	assert.WithinDuration(t, received.Add(15*time.Minute), got, time.Second)

	// Without iat: exp unchanged.
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	got, err = LocalExpiry(makeJWT(t, "x", exp), received)
	require.NoError(t, err)
	assert.True(t, exp.Equal(got))

	_, err = LocalExpiry("not-a-jwt", received)
	assert.Error(t, err)
}

// failingStore fails the first failSaves calls to Save.
type failingStore struct {
	*config.TokenStore
	failSaves atomic.Int32
	saves     atomic.Int32
}

func (f *failingStore) Save(st *config.StoredToken) error {
	f.saves.Add(1)
	if f.failSaves.Add(-1) >= 0 {
		return fmt.Errorf("disk full")
	}
	return f.TokenStore.Save(st)
}

func TestRenew_SaveFailureRetriesOnce(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)})
	fs := &failingStore{TokenStore: store}
	fs.failSaves.Store(1)
	c.Tokens = fs
	var warn bytes.Buffer
	c.WarnWriter = &warn

	_, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, int32(2), fs.saves.Load())
	assert.Empty(t, warn.String())
	st, _ := store.Load()
	assert.Equal(t, "refresh-1", st.RefreshToken)
}

func TestRenew_SaveFailureKeepsSessionInMemory(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)})
	fs := &failingStore{TokenStore: store}
	fs.failSaves.Store(100)
	c.Tokens = fs
	var warn bytes.Buffer
	c.WarnWriter = &warn

	_, err := c.Whoami()
	require.NoError(t, err, "the request uses the renewed token from memory")
	assert.Contains(t, warn.String(), "Warning: could not save the renewed session; other stackctl commands for this context are logged out until you run 'stackctl login'.")
	assert.NotContains(t, warn.String(), "refresh-1")
	assert.Equal(t, int32(2), fs.saves.Load(), "one retry")
	st, err := store.Load()
	require.NoError(t, err)
	assert.Nil(t, st, "the file with the used refresh token is deleted")

	// The next request in this process does not renew again.
	_, err = c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, int32(1), s.refreshCalls.Load())

	// A later renewal sends the rotated refresh token from memory; the
	// server accepts only that one.
	require.NoError(t, c.renew(c.currentToken()))
	assert.Equal(t, int32(2), s.refreshCalls.Load())
}

// TestRenew_SessionEndedKeepsNewLoginFile: the session lives in memory
// (save failed), another login writes a new file, then the in-memory session
// ends. The new file must survive.
func TestRenew_SessionEndedKeepsNewLoginFile(t *testing.T) {
	t.Parallel()
	s, srv := newSessionServer(t)
	s.setRefresh("r1")
	c, store := newSessionClient(t, srv.URL, &config.StoredToken{Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)})
	fs := &failingStore{TokenStore: store}
	fs.failSaves.Store(2)
	c.Tokens = fs
	c.WarnWriter = &bytes.Buffer{}

	_, err := c.Whoami()
	require.NoError(t, err)

	// Another login writes a new file.
	require.NoError(t, store.Save(&config.StoredToken{Token: "other-jwt", RefreshToken: "other-login", ExpiresAt: time.Now().Add(time.Hour)}))

	s.refreshStatus = http.StatusUnauthorized
	err = c.renew(c.currentToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Run 'stackctl login'")

	st, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, st, "the file of the other login survives")
	assert.Equal(t, "other-login", st.RefreshToken)
}

func TestLocalExpiry_LifetimeCap(t *testing.T) {
	t.Parallel()
	now := time.Now()
	exp := now.Add(2 * 365 * 24 * time.Hour).Truncate(time.Second)
	got, err := LocalExpiry(makeJWTIssued(t, "x", now, exp), now.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, exp.Equal(got), "lifetime above one year: exp is used")

	// A negative iat is ignored.
	tok := makeJWTClaims(t, map[string]interface{}{"iat": int64(-1 << 62), "exp": exp.Unix()})
	got, err = LocalExpiry(tok, now)
	require.NoError(t, err)
	assert.True(t, exp.Equal(got))
}

func TestRenew_OnlyForTheLoginAPIURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		storedURL   func(srvURL string) string
		wantRefresh int32
	}{
		{"other URL", func(string) string { return "https://other.example.dev" }, 0},
		{"same URL with slash", func(u string) string { return u + "/" }, 1},
		{"no URL (old file)", func(string) string { return "" }, 1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, srv := newSessionServer(t)
			s.setRefresh("r1")
			c, store := newSessionClient(t, srv.URL, &config.StoredToken{
				Token: "expired-jwt", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute),
				APIURL: tt.storedURL(srv.URL),
			})

			_, err := c.Whoami()
			assert.Equal(t, tt.wantRefresh, s.refreshCalls.Load())
			if tt.wantRefresh == 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "Not authenticated. Run 'stackctl login' first.")
				st, _ := store.Load()
				assert.NotNil(t, st, "the session of the other URL is kept")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
