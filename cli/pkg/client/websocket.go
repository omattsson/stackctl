package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
)

var terminalStatuses = map[string]bool{
	"running": true,
	"stopped": true,
	"error":   true,
	"draft":   true,
}

// StreamDeploymentLogs connects to the backend WebSocket and streams deployment
// log lines for the given instance to w. It blocks until a terminal status is
// received, the context is cancelled, or the connection drops.
func (c *Client) StreamDeploymentLogs(ctx context.Context, instanceID string, w io.Writer, warnWriter io.Writer) (*types.StreamResult, error) {
	wsURL, err := c.websocketURL("/ws")
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	if c.APIKey != "" {
		header.Set("X-API-Key", c.APIKey)
	} else if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
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

	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("connecting to WebSocket: %w", err)
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
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		case <-done:
		}
	}()
	defer close(done)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return &types.StreamResult{Status: "unknown"}, nil
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
//     range over the channel until it closes.
//   - Spawns ONE background goroutine that reads from the WS, decodes
//     payloads, applies the filter, and forwards matches. Goroutine exits
//     when ctx is Done or the read loop returns; goleak verified.
//   - Caller is responsible for cancelling ctx when done; the function
//     does not buffer beyond the returned channel's capacity (8).
//
// Auth:
//   - X-API-Key is sent when c.APIKey is set (the backend WS handler
//     currently ignores it; documented limitation).
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
	wsURL, err := c.websocketURL("/ws")
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	if c.APIKey != "" {
		header.Set("X-API-Key", c.APIKey)
	} else if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
	}

	dialer := websocket.DefaultDialer
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		if t, ok := c.HTTPClient.Transport.(*http.Transport); ok {
			d := *websocket.DefaultDialer
			d.TLSClientConfig = t.TLSClientConfig
			dialer = &d
		}
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		// 401 → fall back to ?token= query param. The backend WS handler
		// (handlers/websocket.go) reads tokens from either location, so a
		// 401 on the header path is the signal that the proxy stripped
		// Authorization (a real-world failure mode for reverse proxies and
		// browsers that we can recover from without operator intervention).
		if resp != nil && resp.StatusCode == http.StatusUnauthorized && c.Token != "" {
			fallbackURL := appendQueryToken(wsURL, c.Token)
			conn2, _, err2 := dialer.DialContext(ctx, fallbackURL, nil)
			if err2 != nil {
				return nil, fmt.Errorf("connecting to WebSocket (header auth failed with 401, query-param fallback also failed): %w", err2)
			}
			conn = conn2
		} else {
			return nil, fmt.Errorf("connecting to WebSocket: %w", err)
		}
	}

	// Buffer of 8 is roughly 2s of backpressure tolerance at the backend's
	// typical broadcast rate (a few status events per deploy across multiple
	// instances). Bumping this only matters if the receiver is slower than
	// the broadcaster — for an interactive CLI that's unlikely.
	out := make(chan types.WatchEvent, 8)

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
		// hang-up doesn't leak this goroutine.
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				_ = conn.Close()
			case <-done:
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
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

	return out, nil
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
