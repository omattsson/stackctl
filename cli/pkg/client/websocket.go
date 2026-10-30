package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
)

// Close reasons that k8s-stack-manager sends with close code 1008 (policy
// violation) when it ends a WebSocket connection.
const (
	// CloseReasonSessionRevoked: the session of the user ended (delete,
	// disable, password reset, role change, logout or logout-all).
	CloseReasonSessionRevoked = "session revoked"
	// CloseReasonTokenExpired: the access token of the connection expired.
	CloseReasonTokenExpired = "token expired"
)

// WSClosedError reports that the server closed the WebSocket connection
// with close code 1008 (policy violation). Reason is the close reason of the
// server, for example CloseReasonSessionRevoked or CloseReasonTokenExpired.
type WSClosedError struct {
	Code   int
	Reason string
	// APIKey is true when the connection used an API key (no login
	// session), so "Log in again" is not the right advice.
	APIKey bool
}

// CloseReasonAPIKeyExpired: the API key of the connection expired.
const CloseReasonAPIKeyExpired = "api key expired"

// Error returns a message for the user, for example "Connection closed by
// the server: session revoked. Log in again."
func (e *WSClosedError) Error() string {
	if e.TokenExpired() {
		// The session can still be valid (only the access token expired),
		// so a new command can renew it. An SSO token cannot be renewed.
		return "Connection closed by the server: token expired. Run the command again. If it fails, run 'stackctl login'."
	}
	if e.Reason == CloseReasonAPIKeyExpired {
		return "Connection closed by the server: the API key expired. Create a new key with 'stackctl apikey create'."
	}
	reason := sanitizeServerMessage(e.Reason)
	if reason == "" {
		reason = "policy violation"
	}
	if e.APIKey {
		// A revoke of an API-key socket: the key was deleted, or its user was
		// disabled, deleted or had a password reset or role change.
		return fmt.Sprintf("Connection closed by the server: %s. The API key was deleted or its user changed; check it with 'stackctl apikey list'.", reason)
	}
	return fmt.Sprintf("Connection closed by the server: %s. Log in again.", reason)
}

// TokenExpired reports whether the server closed the connection because the
// access token expired. A renewed token can open a new connection.
func (e *WSClosedError) TokenExpired() bool {
	return e.Code == websocket.ClosePolicyViolation && e.Reason == CloseReasonTokenExpired
}

// SessionRevoked reports whether the server closed the connection because
// the session ended. Only a new login helps.
func (e *WSClosedError) SessionRevoked() bool {
	return e.Code == websocket.ClosePolicyViolation && e.Reason == CloseReasonSessionRevoked
}

// policyCloseError returns a *WSClosedError when err is a close frame with
// code 1008 (policy violation), else nil.
func policyCloseError(err error) *WSClosedError {
	var ce *websocket.CloseError
	if errors.As(err, &ce) && ce.Code == websocket.ClosePolicyViolation {
		return &WSClosedError{Code: ce.Code, Reason: ce.Text}
	}
	return nil
}

// terminalStatuses are the stack instance statuses that end an operation
// (deploy, stop, clean, rollback). k8s-stack-manager sets "running" after a
// deploy, "stopped" after a stop, "draft" after a clean, "error" after a
// failure and "partial" when some charts deployed and others failed.
// "failed" is not a server status; stackctl accepts it for compatibility.
var terminalStatuses = map[string]bool{
	"running": true,
	"stopped": true,
	"draft":   true,
	"error":   true,
	"partial": true,
	"failed":  true,
}

// failedStatuses are the terminal statuses of a failed operation.
var failedStatuses = map[string]bool{
	"error":   true,
	"partial": true,
	"failed":  true,
}

// IsTerminalStatus reports whether status ends an operation on a stack
// instance (running, stopped, draft, error, partial, failed).
func IsTerminalStatus(status string) bool {
	return terminalStatuses[status]
}

// IsFailedStatus reports whether status is a terminal status of a failed
// operation (error, partial, failed).
func IsFailedStatus(status string) bool {
	return failedStatuses[status]
}

// StreamOptions changes the behaviour of StreamDeploymentLogsWithOptions.
type StreamOptions struct {
	// AfterSubscribe runs after the connection is open and the subscribe
	// message is sent, before the first read. A non-nil result ends the
	// stream with that result. Use it to read the instance status after a
	// reconnect, so a terminal status during the reconnect gap counts.
	AfterSubscribe func(ctx context.Context) (*types.StreamResult, error)
}

// StreamDeploymentLogs connects to the backend WebSocket and streams deployment
// log lines for the given instance to w. It blocks until a terminal status is
// received, the context is cancelled, or the connection drops.
func (c *Client) StreamDeploymentLogs(ctx context.Context, instanceID string, w io.Writer, warnWriter io.Writer) (*types.StreamResult, error) {
	return c.StreamDeploymentLogsWithOptions(ctx, instanceID, w, warnWriter, StreamOptions{})
}

// StreamDeploymentLogsWithOptions is StreamDeploymentLogs with options.
func (c *Client) StreamDeploymentLogsWithOptions(ctx context.Context, instanceID string, w io.Writer, warnWriter io.Writer, opts StreamOptions) (*types.StreamResult, error) {
	conn, err := c.dialWS(ctx, "/ws", warnWriter)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Subscribe to this instance so we receive deployment.log events
	// (the hub only sends log lines to subscribed clients).
	sub, _ := json.Marshal(map[string]interface{}{
		"type":    "subscribe",
		"payload": map[string]string{"instance_id": instanceID},
	})
	if err := conn.WriteMessage(websocket.TextMessage, sub); err != nil {
		return nil, fmt.Errorf("subscribing to instance: %w", err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Send a close frame AND tear down the TCP connection. Use
			// WriteControl with an explicit deadline rather than the
			// blocking WriteMessage — a stalled connection could pin the
			// goroutine on WriteMessage forever, preventing conn.Close
			// from ever running. With WriteControl + 1s deadline + Close,
			// ReadMessage in the read loop unwinds within ctx-cancel
			// latency regardless of network state. Mirrors WatchEvents.
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(time.Second))
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	if opts.AfterSubscribe != nil {
		result, err := opts.AfterSubscribe(ctx)
		if err != nil || result != nil {
			return result, err
		}
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return &types.StreamResult{Status: "unknown"}, nil
			}
			if closeErr := policyCloseError(err); closeErr != nil {
				closeErr.APIKey = c.APIKey != ""
				return nil, closeErr
			}
			return nil, fmt.Errorf("reading WebSocket message: %w", err)
		}

		var msg types.WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			if warnWriter != nil {
				fmt.Fprintf(warnWriter, "Warning: skipping malformed WebSocket message: %v\n", err)
			}
			continue
		}

		switch msg.Type {
		case "deployment.log":
			var logLine types.WSDeploymentLog
			if err := json.Unmarshal(msg.Data, &logLine); err != nil {
				if warnWriter != nil {
					fmt.Fprintf(warnWriter, "Warning: skipping malformed log payload: %v\n", err)
				}
				continue
			}
			if logLine.InstanceID != instanceID {
				continue
			}
			fmt.Fprintln(w, logLine.Line)

		case "deployment.status":
			var status types.WSDeploymentStatus
			if err := json.Unmarshal(msg.Data, &status); err != nil {
				if warnWriter != nil {
					fmt.Fprintf(warnWriter, "Warning: skipping malformed status payload: %v\n", err)
				}
				continue
			}
			if status.InstanceID != instanceID {
				continue
			}
			if terminalStatuses[status.Status] {
				return &types.StreamResult{
					Status:       status.Status,
					ErrorMessage: status.ErrorMessage,
				}, nil
			}
		}
	}
}

// dialWS opens a WebSocket to the given path on the client's BaseURL with
// the standard stackctl auth chain:
//
//   - X-API-Key when c.APIKey is set;
//   - Authorization: Bearer when c.Token is set;
//   - On HTTP 401 from the header path AND c.Token != "", a single retry
//     with the JWT as a URL-escaped ?token= query param (the backend
//     handler reads from either location — see handlers/websocket.go).
//
// Subprotocol auth is NOT attempted; the backend doesn't support it yet
// (tracked as k8s-stack-manager K4).
//
// Custom HTTP transports have their TLS config copied to the dialer so
// --insecure works for the WS upgrade. A non-*http.Transport custom
// transport triggers a warning via warnWriter (when non-nil) because we
// can't extract TLS settings safely.
func (c *Client) dialWS(ctx context.Context, path string, warnWriter io.Writer) (*websocket.Conn, error) {
	return c.dialWSOpts(ctx, path, warnWriter, wsDialOptions{})
}

// wsDialOptions changes the behaviour of dialWSOpts.
type wsDialOptions struct {
	// stream marks a stream route (port-forward, exec). These routes accept
	// only header auth: no ?token= retry, which would only put the JWT in
	// access logs. A 401 gets one forced session renewal and one more dial.
	stream bool
	// op names the operation in handshake errors, for example
	// "port-forward".
	op string
}

// dialWSOpts is dialWS with options (see wsDialOptions).
func (c *Client) dialWSOpts(ctx context.Context, path string, warnWriter io.Writer, opts wsDialOptions) (*websocket.Conn, error) {
	wsURL, err := c.websocketURL(path)
	if err != nil {
		return nil, err
	}

	// Renew a login session first: the upgrade request has no 401 renewal.
	token, err := c.EnsureFreshToken()
	if err != nil {
		return nil, err
	}

	header := baseWSHeader()
	if c.APIKey != "" {
		header.Set("X-API-Key", c.APIKey)
	} else if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}

	dialer := websocket.DefaultDialer
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		if t, ok := c.HTTPClient.Transport.(*http.Transport); ok {
			d := *websocket.DefaultDialer
			d.TLSClientConfig = t.TLSClientConfig
			dialer = &d
		} else if warnWriter != nil {
			fmt.Fprintln(warnWriter, "Warning: custom HTTP transport detected; WebSocket TLS config may not be applied")
		}
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err == nil {
		return conn, nil
	}
	if opts.stream {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized && c.APIKey == "" && token != "" {
			renewed, rerr := c.RenewSession()
			if rerr != nil {
				return nil, rerr
			}
			if renewed {
				c.debugf("↻ session renewed after a WebSocket 401; dialing %s again\n", opts.op)
				header.Set("Authorization", "Bearer "+c.currentToken())
				conn, resp, err = dialer.DialContext(ctx, wsURL, header)
				if err == nil {
					return conn, nil
				}
			}
		}
		if hsErr := handshakeError(resp, err, c.APIKey != ""); hsErr != nil {
			hsErr.Op = opts.op
			return nil, hsErr
		}
		return nil, fmt.Errorf("connecting to the %s WebSocket: %w", opts.op, err)
	}
	// 401 → fall back to ?token= query param when a JWT is configured.
	// The retry is gated on c.Token because an API-key-only caller has no
	// token to fall back to. When BOTH APIKey and Token are set, this
	// transparently switches to the JWT path (documented precedence).
	if resp != nil && resp.StatusCode == http.StatusUnauthorized && token != "" {
		fallbackURL := appendQueryToken(wsURL, token)
		// The fallback carries the token in the URL only, never in a header.
		conn2, resp2, err2 := dialer.DialContext(ctx, fallbackURL, baseWSHeader())
		if err2 != nil {
			if hsErr := handshakeError(resp2, err2, false); hsErr != nil {
				return nil, hsErr
			}
			return nil, fmt.Errorf("connecting to WebSocket (header auth failed with 401, query-param fallback also failed): %w", err2)
		}
		return conn2, nil
	}
	if hsErr := handshakeError(resp, err, c.APIKey != ""); hsErr != nil {
		return nil, hsErr
	}
	return nil, fmt.Errorf("connecting to WebSocket: %w", err)
}

// baseWSHeader returns the headers of both WebSocket dials (the header-auth
// dial and its ?token= fallback), without any credentials. Add a header for
// every upgrade request here, so the fallback cannot miss it.
func baseWSHeader() http.Header {
	header := http.Header{}
	setUserAgent(header)
	return header
}

// wsAuthRequiredMessage is the 401 message of the k8s-stack-manager
// WebSocket handler when the request has no JWT. A server that accepts API
// keys on /ws (k8s-stack-manager v0.8.0 or later) answers a bad key with
// "Invalid API key" or "API key expired" instead.
const wsAuthRequiredMessage = "Authentication required"

// maxHandshakeBody is the number of response body bytes that
// handshakeError reads. gorilla/websocket keeps at most 1024 bytes.
const maxHandshakeBody = 4096

// WSHandshakeError reports that the server refused the WebSocket upgrade
// with an HTTP status. Message is the sanitized error message from the
// JSON response body, or the HTTP status text. The error never contains
// response headers or credentials.
type WSHandshakeError struct {
	StatusCode int
	Message    string
	// Code is the stable error code of the JSON body ({"error","code"}) of
	// the stream routes, for example StreamCodeFeatureDisabled. Empty when
	// the body has no code.
	Code string
	// Op names the operation of a stream route, for example "port-forward".
	// Empty for /ws.
	Op string
	// APIKey is true when the refused request used an API key (no JWT
	// fallback).
	APIKey bool
	err    error
}

// Error codes of the stream routes (port-forward, exec) of
// k8s-stack-manager, in the JSON body of a refused upgrade.
const (
	StreamCodeFeatureDisabled     = "feature_disabled"
	StreamCodeAPIKeyNotAllowed    = "api_key_not_allowed"
	StreamCodeForbidden           = "forbidden"
	StreamCodeClusterDisabled     = "cluster_port_forward_disabled"
	StreamCodeNamespaceNotAllowed = "namespace_not_allowed"
	StreamCodeOriginNotAllowed    = "origin_not_allowed"
	StreamCodeInstanceNotFound    = "instance_not_found"
	StreamCodeServiceNotFound     = "service_not_found"
	StreamCodePortNotFound        = "port_not_found"
	StreamCodeInvalidService      = "invalid_service"
	StreamCodeInvalidPort         = "invalid_port"
	StreamCodeUpgradeRequired     = "upgrade_required"
	StreamCodeInstanceNotDeployed = "instance_not_deployed"
	StreamCodeServiceExternalName = "service_external_name"
	StreamCodeServiceNoSelector   = "service_no_selector"
	StreamCodeTunnelLimit         = "tunnel_limit"
	StreamCodeNoReadyPod          = "no_ready_pod"
	StreamCodeKubeTimeout         = "kube_timeout"
	StreamCodeShuttingDown        = "shutting_down"
	StreamCodeDialFailed          = "dial_failed"
	StreamCodeInternal            = "internal"
)

// Error returns a message for the user, for example "WebSocket refused by
// the server (HTTP 401): Invalid or expired token. Run 'stackctl login'."
func (e *WSHandshakeError) Error() string {
	if e.Op != "" {
		return e.streamError()
	}
	msg := fmt.Sprintf("WebSocket refused by the server (HTTP %d): %s", e.StatusCode, e.Message)
	switch {
	case e.APIKeyNotAccepted():
		return msg + ". This server does not accept API keys for live updates; use a login session or upgrade the server (k8s-stack-manager v0.8.0 or later)."
	case e.StatusCode == http.StatusUnauthorized && e.APIKey:
		return msg + ". Check the API key."
	case e.StatusCode == http.StatusUnauthorized:
		return msg + ". Run 'stackctl login'."
	default:
		return msg
	}
}

// streamError is the message of a refused stream route upgrade.
func (e *WSHandshakeError) streamError() string {
	if e.UpgradeDropped() {
		return fmt.Sprintf("%s failed: the server answered HTTP %d without a WebSocket upgrade; a proxy between stackctl and the server probably dropped the upgrade", e.Op, e.StatusCode)
	}
	if e.NoRoute() {
		return fmt.Sprintf("%s failed: the server has no %s route (HTTP 404); upgrade k8s-stack-manager", e.Op, e.Op)
	}
	msg := fmt.Sprintf("%s refused by the server (HTTP %d): %s", e.Op, e.StatusCode, e.Message)
	switch {
	case e.StatusCode == http.StatusUnauthorized && e.APIKey:
		return msg + ". Check the API key."
	case e.StatusCode == http.StatusUnauthorized:
		return msg + ". Run 'stackctl login'."
	}
	switch e.Code {
	case StreamCodeFeatureDisabled:
		return msg + ". The server has " + e.Op + " turned off; ask an administrator."
	case StreamCodeAPIKeyNotAllowed:
		return msg + ". API keys are not allowed for this command: run 'stackctl login' and remove the API key (--api-key, STACKCTL_API_KEY or the api-key of the context)."
	case StreamCodeServiceNotFound, StreamCodePortNotFound:
		return msg + ". Check the service name and the service port (not the container port)."
	case StreamCodeInstanceNotDeployed:
		return msg + ". Deploy the stack first."
	case StreamCodeTunnelLimit:
		return msg + ". Close other connections, then try again."
	}
	return msg
}

// Unwrap returns the dialer error (websocket.ErrBadHandshake).
func (e *WSHandshakeError) Unwrap() error {
	return e.err
}

// FeatureDisabled reports whether the server has the stream feature turned
// off (404 with code feature_disabled).
func (e *WSHandshakeError) FeatureDisabled() bool {
	return e.StatusCode == http.StatusNotFound && e.Code == StreamCodeFeatureDisabled
}

// NoRoute reports a 404 without an error code: a server without the route
// (an older k8s-stack-manager). Only meaningful for a stream route.
func (e *WSHandshakeError) NoRoute() bool {
	return e.StatusCode == http.StatusNotFound && e.Code == ""
}

// UpgradeDropped reports a success status, or a 400 without a code or with
// upgrade_required: the request reached the server without the upgrade
// headers, so a proxy dropped them.
func (e *WSHandshakeError) UpgradeDropped() bool {
	if e.StatusCode >= 200 && e.StatusCode < 300 {
		return true
	}
	return e.StatusCode == http.StatusBadRequest && (e.Code == "" || e.Code == StreamCodeUpgradeRequired)
}

// Fatal reports whether every later dial of the same stream would fail the
// same way: 400, 401, 403, 404 and 409. 429, 5xx and network errors can pass
// on the next try.
func (e *WSHandshakeError) Fatal() bool {
	switch e.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusConflict:
		return true
	}
	return e.UpgradeDropped()
}

// APIKeyNotAccepted reports whether the server refused an API key because
// its WebSocket endpoint accepts only a JWT (k8s-stack-manager before
// v0.8.0).
func (e *WSHandshakeError) APIKeyNotAccepted() bool {
	return e.APIKey && e.StatusCode == http.StatusUnauthorized &&
		strings.EqualFold(e.Message, wsAuthRequiredMessage)
}

// handshakeError returns a *WSHandshakeError when the dial failed because
// the server answered the upgrade with an HTTP status, else nil (for
// example a network error). It reads the error message from the JSON body
// ({"error": "..."} or {"message": "..."}); a body in another format (for
// example an HTML page of a proxy) gives the HTTP status text.
func handshakeError(resp *http.Response, err error, apiKey bool) *WSHandshakeError {
	if resp == nil || !errors.Is(err, websocket.ErrBadHandshake) {
		return nil
	}
	hsErr := &WSHandshakeError{StatusCode: resp.StatusCode, APIKey: apiKey, err: err}
	if resp.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHandshakeBody))
		_ = resp.Body.Close()
		var errResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(body, &errResp) == nil {
			hsErr.Code = sanitizeServerMessage(errResp.Code)
			hsErr.Message = sanitizeServerMessage(errResp.Error)
			if hsErr.Message == "" {
				hsErr.Message = sanitizeServerMessage(errResp.Message)
			}
		}
	}
	if hsErr.Message == "" {
		hsErr.Message = http.StatusText(resp.StatusCode)
	}
	if hsErr.Message == "" {
		hsErr.Message = "unexpected response"
	}
	return hsErr
}

func (c *Client) websocketURL(path string) (string, error) {
	base := c.BaseURL
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return "", fmt.Errorf("unsupported URL scheme in %q", c.BaseURL)
	}
	return strings.TrimRight(base, "/") + path, nil
}

// WatchFilter narrows the event stream returned by WatchEvents. All fields
// are optional; an empty filter passes every "deployment.status" event the
// backend broadcasts on /ws.
//
// Filtering is performed client-side because the backend hub broadcasts
// status events to every connected client (per
// backend/internal/deployer/broadcast.go broadcastStatus → hub.Broadcast).
// The hub's subscribe protocol only targets per-instance log streams.
type WatchFilter struct {
	// InstanceIDs limits events to the listed instance UUIDs. Nil/empty
	// matches every instance.
	InstanceIDs []string
	// Status limits events to a single status string (e.g. "running",
	// "failed"). Empty matches every status.
	Status string
}

// matches returns true when the supplied status payload satisfies the filter.
func (f WatchFilter) matches(s types.WSDeploymentStatus) bool {
	if f.Status != "" && s.Status != f.Status {
		return false
	}
	if len(f.InstanceIDs) > 0 {
		for _, id := range f.InstanceIDs {
			if id == s.InstanceID {
				return true
			}
		}
		return false
	}
	return true
}

// WatchEvents subscribes to the backend /ws stream and pushes each
// "deployment.status" event that matches filter onto the returned channel.
//
// Lifecycle:
//   - The returned channel is closed when ctx is cancelled, when the
//     connection drops, or when the read loop exits. Receivers should
//     range over the channel until it closes. Use Watch to learn why the
//     stream ended (for example a server close with code 1008).
//   - Spawns ONE background goroutine that reads from the WS, decodes
//     payloads, applies the filter, and forwards matches. Goroutine exits
//     when ctx is Done or the read loop returns; goleak verified.
//   - Caller is responsible for cancelling ctx when done; the function
//     does not buffer beyond the returned channel's capacity (8).
//
// Auth:
//   - X-API-Key is sent when c.APIKey is set. k8s-stack-manager accepts
//     it on /ws from v0.8.0; an older server answers 401 "Authentication
//     required" (WSHandshakeError.APIKeyNotAccepted).
//   - Authorization: Bearer <token> is sent when c.Token is set.
//   - If the upgrade is rejected with HTTP 401 AND c.Token is set, the
//     dialer retries with the JWT as a ?token= query param (the backend
//     reads from either location). The retry is gated on c.Token because
//     an API-key-only caller has no token to fall back to.
//   - When BOTH c.APIKey and c.Token are set, a 401 on the API-key path
//     transparently falls back to the JWT query-param path. This is
//     intentional — operators that configure both expect the JWT to be
//     the operational credential and the API key the headless one.
//   - The Sec-WebSocket-Protocol subprotocol fallback documented in
//     stackctl#75 is NOT yet supported by the backend (tracked as
//     k8s-stack-manager K4); the dialer does not attempt it.
//
// Non-status messages (deployment.log) are silently dropped so this can
// be safely connected to the same /ws endpoint as StreamDeploymentLogs
// without cross-talk.
func (c *Client) WatchEvents(ctx context.Context, filter WatchFilter) (<-chan types.WatchEvent, error) {
	stream, err := c.Watch(ctx, filter)
	if err != nil {
		return nil, err
	}
	return stream.Events, nil
}

// WatchStream is an open event stream of Watch.
type WatchStream struct {
	// Events receives the matching events. It is closed when the stream
	// ends.
	Events <-chan types.WatchEvent

	// err is written before Events is closed, so a read after the close
	// is safe.
	err error
}

// Err returns why the stream ended: a *WSClosedError when the server closed
// the connection with close code 1008 (session revoked, token expired),
// else nil (context cancelled, normal close, connection lost). Call it only
// after Events is closed.
func (s *WatchStream) Err() error {
	return s.err
}

// Watch is WatchEvents with access to the end reason of the stream (see
// WatchStream.Err).
func (c *Client) Watch(ctx context.Context, filter WatchFilter) (*WatchStream, error) {
	conn, err := c.dialWS(ctx, "/ws", nil)
	if err != nil {
		return nil, err
	}

	// Buffer of 8 is roughly 2s of backpressure tolerance at the backend's
	// typical broadcast rate (a few status events per deploy across multiple
	// instances). Bumping this only matters if the receiver is slower than
	// the broadcaster — for an interactive CLI that's unlikely.
	out := make(chan types.WatchEvent, 8)
	stream := &WatchStream{Events: out}

	// `done` is closed by the outer goroutine's defer (line 253) — it tells
	// the watchdog goroutine to exit when the read loop returns on its own
	// (e.g. the server hung up). Without it, a server-side close while ctx
	// is still alive would leave the watchdog parked on `<-ctx.Done()`
	// forever.
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(out)
		defer conn.Close()

		// Watchdog: closes the connection when ctx is cancelled so the
		// blocking ReadMessage below returns and the read loop exits;
		// returns on its own when `done` closes so a server-initiated
		// hang-up doesn't leak this goroutine. WriteControl with a write
		// deadline is used instead of WriteMessage so a stalled TCP
		// connection can't pin the goroutine forever and block conn.Close.
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(time.Second))
				_ = conn.Close()
			case <-done:
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() == nil {
					if closeErr := policyCloseError(err); closeErr != nil {
						closeErr.APIKey = c.APIKey != ""
						stream.err = closeErr
					}
				}
				return
			}
			var msg types.WSMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}
			if msg.Type != "deployment.status" {
				continue
			}
			var status types.WSDeploymentStatus
			if err := json.Unmarshal(msg.Data, &status); err != nil {
				continue
			}
			if !filter.matches(status) {
				continue
			}
			event := types.WatchEvent{
				Type:         msg.Type,
				InstanceID:   status.InstanceID,
				Status:       status.Status,
				LogID:        status.LogID,
				ErrorMessage: status.ErrorMessage,
				Timestamp:    time.Now().UTC(),
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return stream, nil
}

// appendQueryToken returns a URL with ?token=<jwt> appended (or merged
// into an existing query string). The token is URL-escaped to survive
// future token formats containing characters that would otherwise be
// interpreted as query delimiters (`+`, `=`, `&`, `?`, whitespace).
// Extracted so tests can verify the fallback URL shape without spinning
// up a real connection.
func appendQueryToken(rawURL, token string) string {
	escaped := url.QueryEscape(token)
	if strings.Contains(rawURL, "?") {
		return rawURL + "&token=" + escaped
	}
	return rawURL + "?token=" + escaped
}

// Close codes of the stream routes (port-forward, exec) of
// k8s-stack-manager.
const (
	CloseNormal         = websocket.CloseNormalClosure     // 1000
	CloseGoingAway      = websocket.CloseGoingAway         // 1001: server shutdown
	CloseUnsupported    = websocket.CloseUnsupportedData   // 1003: text frame from the client
	ClosePolicy         = websocket.ClosePolicyViolation   // 1008: access revoked
	CloseMessageTooBig  = websocket.CloseMessageTooBig     // 1009
	CloseInternalError  = websocket.CloseInternalServerErr // 1011
	CloseMaxDuration    = 4001                             // server time limit
	CloseIdleTimeout    = 4002                             // no data in either direction
	ClosePodStreamError = 4502                             // the reason holds the error
	closeNoStatus       = websocket.CloseNoStatusReceived  // 1005: close frame without a code
)

// StreamClosedError reports that the server closed a stream WebSocket
// (port-forward, exec) with a code other than 1000.
type StreamClosedError struct {
	// Subject names the stream in the message, for example "tunnel".
	Subject string
	Code    int
	// Reason is the sanitized close reason of the server.
	Reason string
	// APIKey is true when the stream used an API key.
	APIKey bool
}

// Error returns a message for the user, for example "tunnel closed: server
// time limit reached".
func (e *StreamClosedError) Error() string {
	return e.Subject + " closed: " + CloseCodeText(e.Code, e.Reason, e.APIKey)
}

// Fatal reports whether a new stream would fail the same way (1008: access
// revoked).
func (e *StreamClosedError) Fatal() bool {
	return e.Code == ClosePolicy
}

// Unwrap returns a *WSClosedError for close code 1008, so callers that
// handle a revoked /ws connection handle a revoked stream too.
func (e *StreamClosedError) Unwrap() error {
	if e.Code == ClosePolicy {
		return &WSClosedError{Code: e.Code, Reason: e.Reason, APIKey: e.APIKey}
	}
	return nil
}

// CloseCodeText returns a short text for the user for a close code of a
// stream route, for example "server time limit reached" for 4001.
func CloseCodeText(code int, reason string, apiKey bool) string {
	reason = sanitizeServerMessage(reason)
	switch code {
	case CloseNormal, closeNoStatus:
		return "normal close"
	case CloseGoingAway:
		return "the server is shutting down"
	case CloseUnsupported:
		return "the server refused a text frame"
	case ClosePolicy:
		if apiKey {
			return "access revoked (API key or permissions); check the key with 'stackctl apikey list'"
		}
		return "access revoked (token, session or permissions); run 'stackctl login'"
	case CloseMessageTooBig:
		return "message too big"
	case CloseInternalError:
		return "internal server error"
	case CloseMaxDuration:
		return "server time limit reached"
	case CloseIdleTimeout:
		return "idle timeout (no data in either direction)"
	case ClosePodStreamError:
		if reason == "" {
			return "pod stream error"
		}
		return "pod stream error: " + reason
	}
	if reason == "" {
		return fmt.Sprintf("closed by the server (code %d)", code)
	}
	return fmt.Sprintf("closed by the server (code %d): %s", code, reason)
}

// KeepaliveTimings are the keepalive deadlines of a stream WebSocket. A zero
// field takes the default. Tests inject short values.
type KeepaliveTimings struct {
	// PingInterval is the interval of the client pings (default 30 s).
	PingInterval time.Duration
	// ReadTimeout is the read deadline, moved by data, a ping or a pong
	// (default 75 s; the server pings every 30 s).
	ReadTimeout time.Duration
	// WriteTimeout is the deadline of a control frame (default 10 s).
	WriteTimeout time.Duration
}

// Default keepalive timings of a stream WebSocket.
const (
	DefaultPingInterval = 30 * time.Second
	DefaultReadTimeout  = 75 * time.Second
	DefaultWriteTimeout = 10 * time.Second
)

func (k KeepaliveTimings) withDefaults() KeepaliveTimings {
	if k.PingInterval <= 0 {
		k.PingInterval = DefaultPingInterval
	}
	if k.ReadTimeout <= 0 {
		k.ReadTimeout = DefaultReadTimeout
	}
	if k.WriteTimeout <= 0 {
		k.WriteTimeout = DefaultWriteTimeout
	}
	return k
}

// setKeepalive sets the read deadline and the ping and pong handlers of ws.
// It returns the function that moves the read deadline. Call it, and read
// ws, only from the reader goroutine: gorilla runs the handlers there.
func setKeepalive(ws *websocket.Conn, k KeepaliveTimings) (extend func()) {
	extend = func() { _ = ws.SetReadDeadline(time.Now().Add(k.ReadTimeout)) }
	extend()
	ws.SetPongHandler(func(string) error {
		extend()
		return nil
	})
	ws.SetPingHandler(func(data string) error {
		extend()
		err := ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(k.WriteTimeout))
		var ne net.Error
		if err == nil || errors.Is(err, websocket.ErrCloseSent) || (errors.As(err, &ne) && ne.Timeout()) {
			return nil
		}
		return err
	})
	return extend
}

// sendPing writes a ping control frame. It is safe to call concurrently with
// the data writer.
func sendPing(ws *websocket.Conn, k KeepaliveTimings) error {
	return ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(k.WriteTimeout))
}

// sendClose writes a close control frame. It is safe to call concurrently
// with the data writer.
func sendClose(ws *websocket.Conn, code int, timeout time.Duration) error {
	return ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""), time.Now().Add(timeout))
}
