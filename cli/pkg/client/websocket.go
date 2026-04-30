package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
			dialer = &websocket.Dialer{
				TLSClientConfig: t.TLSClientConfig,
			}
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
