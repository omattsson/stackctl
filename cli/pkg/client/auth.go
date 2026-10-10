package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/types"
)

// Session renewal
//
// A username/password login gets a short-lived access token (15 minutes by
// default) and a refresh token in the httpOnly cookie "refresh_token". The
// refresh token is stored in the token file. The client renews the access
// token:
//
//   - before a request, when the access token expires within RenewMargin;
//   - after a 401 from a non-auth endpoint, once, and then retries the
//     request once.
//
// The server rotates the refresh token on each renewal and ends the session
// when a used refresh token comes back. Renewal therefore holds an exclusive
// lock on the token file and re-reads the file after it gets the lock: when
// another process has already renewed, the client uses that token and does
// not call the API.
//
// SSO logins and old token files have no refresh token. API keys have no
// session. The client does not renew those.

const (
	// RefreshCookieName is the name of the cookie that carries the refresh
	// token.
	RefreshCookieName = "refresh_token"

	pathLogin     = "/api/v1/auth/login"
	pathRefresh   = "/api/v1/auth/refresh"
	pathLogout    = "/api/v1/auth/logout"
	pathLogoutAll = "/api/v1/auth/logout-all"
	pathOIDC      = "/api/v1/auth/oidc/"
)

// RenewMargin is the time before expiry at which the client renews the
// access token.
var RenewMargin = 60 * time.Second

// TokenStore persists a login session. *config.TokenStore implements it.
type TokenStore interface {
	// Load returns the stored token, or nil when there is none.
	Load() (*config.StoredToken, error)
	// Save replaces the stored token.
	Save(*config.StoredToken) error
	// Delete removes the stored token.
	Delete() error
	// Lock takes an exclusive lock that other processes also respect.
	Lock() (unlock func(), err error)
}

// errNoRefreshToken means that the stored session cannot be renewed.
var errNoRefreshToken = errors.New("no refresh token stored")

// do executes an HTTP request. For a login session (Tokens set, no API key)
// it renews the access token before it expires, and after a 401 it renews
// once and retries the request once.
func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	if !c.canRenew(path) {
		return c.send(method, path, body)
	}

	if err := c.renewIfExpiring(); err != nil {
		return nil, err
	}

	stale := c.currentToken()
	resp, err := c.send(method, path, body)
	var apiErr *APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	if rerr := c.renew(stale); rerr != nil {
		if errors.Is(rerr, errNoRefreshToken) {
			return nil, err
		}
		return nil, rerr
	}
	c.debugf("↻ session renewed after 401; retrying %s %s\n", method, path)
	return c.send(method, path, body)
}

// EnsureFreshToken renews the access token when it expires within
// RenewMargin and returns the access token to use. Use it before the token
// leaves the client, for example to hand it to a plugin.
func (c *Client) EnsureFreshToken() (string, error) {
	if c.Tokens != nil && c.APIKey == "" {
		if err := c.renewIfExpiring(); err != nil {
			return "", err
		}
	}
	return c.currentToken(), nil
}

// RenewSession renews the login session now, also when the stored expiry is
// not near. Use it when the server reports that the access token expired,
// for example a WebSocket close with the reason "token expired". It returns
// false (and no error) when the client cannot renew: an API key, no stored
// session, or a session without a refresh token (SSO login, old token
// file). A 401 *APIError means that the server ended the session.
func (c *Client) RenewSession() (bool, error) {
	if c.Tokens == nil || c.APIKey != "" {
		return false, nil
	}
	if err := c.renew(c.currentToken()); err != nil {
		if errors.Is(err, errNoRefreshToken) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// canRenew reports whether a request to path can use session renewal.
func (c *Client) canRenew(path string) bool {
	if c.Tokens == nil || c.APIKey != "" {
		return false
	}
	return !isSessionPath(path)
}

// isSessionPath reports whether path is an endpoint that creates or ends a
// session. A 401 from these endpoints must not start a renewal.
func isSessionPath(path string) bool {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	switch path {
	case pathLogin, pathRefresh, pathLogout, pathLogoutAll:
		return true
	}
	return strings.HasPrefix(path, pathOIDC)
}

// renewIfExpiring renews the session when the stored access token expires
// within RenewMargin. When the renewal fails but the current token has not
// expired yet, the request continues with the current token.
//
// The read here is not under the lock. A failed read (for example while
// another process replaces the file on Windows) is not an error: the request
// continues, and a 401 starts a renewal that reads the file under the lock.
func (c *Client) renewIfExpiring() error {
	st, err := c.loadSession()
	if err != nil {
		c.debugf("✗ reading token file: %v\n", err)
		return nil
	}
	if st == nil || st.RefreshToken == "" || !c.sessionMatches(st) {
		return nil // nothing to renew
	}

	current := c.currentToken()
	if !expiresWithin(st.ExpiresAt, RenewMargin) {
		// Another process can have renewed the session since this client
		// loaded its token.
		if st.Token != "" && st.Token != current {
			c.setToken(st.Token)
		}
		return nil
	}

	rerr := c.renew(current)
	if rerr == nil || errors.Is(rerr, errNoRefreshToken) {
		return nil
	}
	if !st.ExpiresAt.IsZero() && time.Now().Before(st.ExpiresAt) && c.currentToken() != "" {
		c.debugf("✗ session renewal failed, continuing with current token: %v\n", rerr)
		return nil
	}
	return rerr
}

// loadSession returns the session that this process could not save (see
// renew), or else the stored session.
func (c *Client) loadSession() (*config.StoredToken, error) {
	c.tokenMu.Lock()
	if c.unsaved != nil {
		st := *c.unsaved
		c.tokenMu.Unlock()
		return &st, nil
	}
	c.tokenMu.Unlock()
	return c.Tokens.Load()
}

// renew renews the session under the token file lock. stale is the access
// token that the caller found expiring or that got a 401.
//
// It returns errNoRefreshToken when there is no refresh token, a 401
// *APIError when the server ended the session (the stored token is then
// deleted), and any other error (network, 5xx) unchanged with the stored
// token kept.
func (c *Client) renew(stale string) error {
	c.renewMu.Lock()
	defer c.renewMu.Unlock()

	unlock, err := c.Tokens.Lock()
	if err != nil {
		return fmt.Errorf("renewing session: %w", err)
	}
	defer unlock()

	st, err := c.loadSession()
	if err != nil {
		return fmt.Errorf("renewing session: %w", err)
	}
	if st == nil || st.RefreshToken == "" || !c.sessionMatches(st) {
		return errNoRefreshToken
	}

	// Another process (or goroutine) renewed while this one waited for the
	// lock: use its token.
	if st.Token != "" && st.Token != stale && !expiresWithin(st.ExpiresAt, RenewMargin) {
		c.setToken(st.Token)
		return nil
	}

	sent := st.RefreshToken
	access, rotated, err := c.RefreshSession(sent)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			// Delete the file only when it holds the session that ended. The
			// session can come from memory (see below), and a new login can
			// have written a new file meanwhile.
			if fileSt, lerr := c.Tokens.Load(); lerr == nil && fileSt != nil && fileSt.RefreshToken == sent {
				if derr := c.Tokens.Delete(); derr != nil {
					c.debugf("✗ removing token file: %v\n", derr)
				}
			}
			c.tokenMu.Lock()
			c.Token = ""
			c.unsaved = nil
			c.tokenMu.Unlock()
			return &APIError{StatusCode: http.StatusUnauthorized, Message: apiErr.Message}
		}
		return fmt.Errorf("renewing session: %w", err)
	}

	st.Token = access
	if rotated != "" {
		// The grace response (a rotated token used again within seconds)
		// has no Set-Cookie: keep the stored refresh token.
		st.RefreshToken = rotated
	}
	st.ExpiresAt = time.Time{}
	if exp, err := LocalExpiry(access, time.Now()); err == nil {
		st.ExpiresAt = exp
	}

	// The server has rotated the refresh token: losing the new one ends the
	// session at the next renewal. Retry a failed save once. When it still
	// fails, keep the session in memory for the rest of this process and
	// delete the file (still under the lock): the file holds the used
	// refresh token, and another process that sent it would make the server
	// revoke the whole session.
	saveErr := c.Tokens.Save(st)
	if saveErr != nil {
		saveErr = c.Tokens.Save(st)
	}
	if saveErr != nil {
		if derr := c.Tokens.Delete(); derr != nil {
			c.debugf("✗ removing token file: %v\n", derr)
		}
	}
	c.tokenMu.Lock()
	c.Token = access
	if saveErr != nil {
		kept := *st
		c.unsaved = &kept
	} else {
		c.unsaved = nil
	}
	c.tokenMu.Unlock()
	if saveErr != nil {
		c.debugf("✗ saving renewed token: %v\n", saveErr)
		c.warnf("Warning: could not save the renewed session; other stackctl commands for this context are logged out until you run 'stackctl login'.\n")
	}
	return nil
}

// sessionMatches reports whether the stored session belongs to the API URL
// of this client. The refresh token is never sent to another server.
func (c *Client) sessionMatches(st *config.StoredToken) bool {
	return st.APIURL == "" || SameAPIURL(st.APIURL, c.BaseURL)
}

// SameAPIURL reports whether two API URLs are equal, ignoring a trailing
// slash.
func SameAPIURL(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// LoginSession authenticates with a username and password. It returns the
// login response and the refresh token from the "refresh_token" cookie
// (empty when the server sets none). On success, c.Token is set to the
// returned JWT for subsequent requests.
func (c *Client) LoginSession(username, password string) (*types.LoginResponse, string, error) {
	resp, err := c.do(http.MethodPost, pathLogin, types.LoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	var out types.LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}
	c.setToken(out.Token)
	return &out, refreshCookie(resp), nil
}

// RefreshSession sends POST /api/v1/auth/refresh with the refresh token as
// a cookie. It returns the new access token and the rotated refresh token.
// The rotated refresh token is empty when the response sets no cookie (the
// grace response); keep the old refresh token then.
func (c *Client) RefreshSession(refreshToken string) (accessToken, rotated string, err error) {
	resp, err := c.sendSession(http.MethodPost, pathRefresh, "", refreshToken)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("decoding refresh response: %w", err)
	}
	if out.Token == "" {
		return "", "", fmt.Errorf("server returned an empty token")
	}
	return out.Token, refreshCookie(resp), nil
}

// Logout ends the session on the server: POST /api/v1/auth/logout with the
// access token (the server blocks it) and the refresh token cookie (the
// server revokes it). Either value can be empty.
func (c *Client) Logout(refreshToken string) error {
	resp, err := c.sendSession(http.MethodPost, pathLogout, c.currentToken(), refreshToken)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// sendSession sends a request without a body to a session endpoint with an
// optional bearer token and an optional refresh token cookie. It never
// renews and never retries.
func (c *Client) sendSession(method, path, bearer, refreshToken string) (*http.Response, error) {
	u, err := c.resolveURL(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, u, bytes.NewReader(nil))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if refreshToken != "" {
		req.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: refreshToken})
	}

	c.debugf("→ %s %s\n", method, u)
	if bearer != "" {
		c.debugf("  Authorization: %s\n", maskCredential("Authorization", req.Header.Get("Authorization")))
	}
	if refreshToken != "" {
		c.debugf("  Cookie: %s=***\n", RefreshCookieName)
	}

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.debugf("✗ %s (%s)\n", err, time.Since(start).Truncate(time.Millisecond))
		return nil, fmt.Errorf("making request: %w", err)
	}
	c.debugf("← %d %s (%s)\n", resp.StatusCode, http.StatusText(resp.StatusCode), time.Since(start).Truncate(time.Millisecond))

	if resp.StatusCode >= 400 {
		return nil, decodeAPIError(resp)
	}
	return resp, nil
}

// refreshCookie returns the non-empty value of the refresh token cookie in
// the response, or "" when the response sets none.
func refreshCookie(resp *http.Response) string {
	for _, ck := range resp.Cookies() {
		if ck.Name == RefreshCookieName && ck.Value != "" && ck.MaxAge >= 0 {
			return ck.Value
		}
	}
	return ""
}

// TokenExpiry returns the "exp" claim of a JWT. It does not verify the
// signature.
func TokenExpiry(token string) (time.Time, error) {
	exp, _, err := tokenTimes(token)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(exp, 0), nil
}

// tokenTimes returns the exp and iat claims (Unix seconds; iat is 0 when
// missing) of a JWT without verifying the signature.
func tokenTimes(token string) (exp, iat int64, err error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return 0, 0, fmt.Errorf("invalid JWT format")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("decoding JWT payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
		Iat int64 `json:"iat"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return 0, 0, fmt.Errorf("parsing JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		return 0, 0, fmt.Errorf("JWT missing exp claim")
	}
	return claims.Exp, claims.Iat, nil
}

// maxTokenLifetime caps the lifetime that LocalExpiry trusts.
const maxTokenLifetime = 365 * 24 * 60 * 60 // seconds

// LocalExpiry returns the expiry of a JWT on the local clock: receivedAt
// plus the token lifetime (exp - iat). This removes the clock skew between
// this machine and the server. Without a usable iat claim, or with a
// lifetime above one year, it returns exp.
func LocalExpiry(token string, receivedAt time.Time) (time.Time, error) {
	exp, iat, err := tokenTimes(token)
	if err != nil {
		return time.Time{}, err
	}
	// iat > 0 and exp > iat keep exp-iat positive and free of overflow.
	if iat > 0 && exp > iat && exp-iat <= maxTokenLifetime {
		return receivedAt.Add(time.Duration(exp-iat) * time.Second), nil
	}
	return time.Unix(exp, 0), nil
}

// expiresWithin reports whether exp is known and less than margin away.
func expiresWithin(exp time.Time, margin time.Duration) bool {
	return !exp.IsZero() && time.Until(exp) < margin
}

func (c *Client) currentToken() string {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	return c.Token
}

func (c *Client) setToken(token string) {
	c.tokenMu.Lock()
	c.Token = token
	c.tokenMu.Unlock()
}

// warnf writes a warning to WarnWriter (os.Stderr when nil).
func (c *Client) warnf(format string, args ...interface{}) {
	w := c.WarnWriter
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

func (c *Client) debugf(format string, args ...interface{}) {
	if c.Debug && c.DebugWriter != nil {
		fmt.Fprintf(c.DebugWriter, format, args...)
	}
}
