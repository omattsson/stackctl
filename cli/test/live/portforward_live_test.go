//go:build live

package live

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Environment (besides the suite variables):
//   - STACKCTL_LIVE_PF_CHART — chart path that the backend can read
//     (default: testdata/portforward-echo next to this file; works when the
//     backend runs on the same host).
//
// The test needs a login (JWT): the route refuses API keys by default. The
// backend needs STACK_PORT_FORWARD_ENABLED=true. The test sets
// allow_port_forward on the default cluster (admin) and restores it.

const (
	pfService = "echo"
	pfPort    = 8080
)

func pfChartPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("STACKCTL_LIVE_PF_CHART"); p != "" {
		return p
	}
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "testdata", "portforward-echo")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("echo chart not found: %v", err)
	}
	return p
}

func waitStackStatus(t *testing.T, c *client.Client, id string, want string, timeout time.Duration) *types.StackInstance {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		inst, err := c.GetStack(id)
		require.NoError(t, err, "get stack")
		if inst.Status == want {
			return inst
		}
		if inst.Status != want && client.IsFailedStatus(inst.Status) {
			t.Fatalf("stack entered status %s: %s", inst.Status, inst.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("stack status %q after %s, want %q", inst.Status, timeout, want)
		}
		time.Sleep(2 * time.Second)
	}
}

// startPFListener accepts local connections and tunnels each one to the
// echo service through the backend, as `stackctl stack port-forward` does.
func startPFListener(t *testing.T, c *client.Client, instanceID string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				ws, err := dialPFRetry(ctx, c, instanceID)
				if err != nil {
					_ = conn.Close()
					return
				}
				_, _ = c.Tunnel(ctx, ws, conn, client.TunnelOptions{})
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		wg.Wait()
	})
	return ln.Addr().String()
}

// dialPFRetry retries while the pod is not ready yet (503) or the dial fails (502).
func dialPFRetry(ctx context.Context, c *client.Client, id string) (*websocket.Conn, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ws, err := c.DialPortForward(ctx, id, pfService, pfPort)
		if err == nil {
			return ws, nil
		}
		var hs *client.WSHandshakeError
		retry := errors.As(err, &hs) && (hs.StatusCode == 502 || hs.StatusCode == 503)
		if !retry || time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		time.Sleep(time.Second)
	}
}

func TestLivePortForward_Tunnel(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	// Probe: a feature-disabled backend answers 404 before it looks at the instance.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer probeCancel()
	if ws, err := c.DialPortForward(probeCtx, "00000000-0000-0000-0000-0000000000ff", pfService, pfPort); err == nil {
		_ = ws.Close()
		t.Fatal("dial of an unknown instance must fail")
	} else {
		var hs *client.WSHandshakeError
		if errors.As(err, &hs) {
			switch hs.Code {
			case client.StreamCodeFeatureDisabled:
				t.Skip("port-forward is disabled on this backend (STACK_PORT_FORWARD_ENABLED)")
			case client.StreamCodeAPIKeyNotAllowed:
				t.Skip("backend refuses API keys on the stream routes; use STACKCTL_LIVE_USER and STACKCTL_LIVE_PASS")
			}
		}
	}

	chart := pfChartPath(t)
	cluster := requireCluster(t, c)

	// allow_port_forward is not in the typed cluster request; send the raw field.
	var raw struct {
		AllowPortForward bool `json:"allow_port_forward"`
	}
	require.NoError(t, c.Get("/api/v1/clusters/"+cluster.ID, &raw), "get cluster")
	if !raw.AllowPortForward {
		if err := c.Put("/api/v1/clusters/"+cluster.ID, map[string]any{"allow_port_forward": true}, nil); err != nil {
			t.Skipf("cannot enable allow_port_forward on the cluster (admin needed): %v", err)
		}
		t.Cleanup(func() {
			_ = c.Put("/api/v1/clusters/"+cluster.ID, map[string]any{"allow_port_forward": false}, nil)
		})
	}

	prefix := liveResourcePrefix()
	def, err := c.CreateDefinition(&types.CreateDefinitionRequest{
		Name:        prefix + "-pf-def",
		Description: "live port-forward echo",
	})
	require.NoError(t, err, "create definition")
	deleteDefinitionIfExists(t, c, def.ID)
	_, err = c.AddDefinitionChart(def.ID, &types.UpdateChartConfigRequest{
		ChartName:    "portforward-echo",
		ChartPath:    chart,
		ChartVersion: "0.1.0",
	})
	require.NoError(t, err, "add chart")

	inst, err := c.CreateStack(&types.CreateStackRequest{
		Name:              prefix + "-pf",
		StackDefinitionID: def.ID,
		Branch:            "main",
		ClusterID:         cluster.ID,
	})
	require.NoError(t, err, "create stack")
	t.Cleanup(func() {
		_, _ = c.CleanStack(inst.ID)
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			cur, err := c.GetStack(inst.ID)
			if err != nil || cur.Status != "cleaning" {
				break
			}
			time.Sleep(2 * time.Second)
		}
		_ = c.DeleteStack(inst.ID)
	})

	_, err = c.DeployStack(inst.ID)
	require.NoError(t, err, "deploy stack")
	waitStackStatus(t, c, inst.ID, "running", 4*time.Minute)

	addr := startPFListener(t, c, inst.ID)

	roundTrip := func(t *testing.T, payload []byte, halfClose bool) []byte {
		t.Helper()
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		go func() {
			_, _ = conn.Write(payload)
			if halfClose {
				_ = conn.(*net.TCPConn).CloseWrite()
			}
		}()
		got := make([]byte, len(payload))
		_, err = io.ReadFull(conn, got)
		require.NoError(t, err, "read echo")
		return got
	}

	t.Run("round trip", func(t *testing.T) {
		payload := []byte("hello through the backend\n")
		assert.Equal(t, payload, roundTrip(t, payload, false))
	})

	t.Run("parallel connections carry independent data", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make([][]byte, 2)
		payloads := [][]byte{
			bytes.Repeat([]byte("A1"), 100_000),
			bytes.Repeat([]byte("B2"), 70_000),
		}
		for i := range payloads {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = roundTrip(t, payloads[i], false)
			}()
		}
		wg.Wait()
		for i := range payloads {
			assert.True(t, bytes.Equal(payloads[i], results[i]), "connection %d got foreign or damaged data", i)
		}
	})

	t.Run("half-close still delivers the response", func(t *testing.T) {
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		payload := []byte(fmt.Sprintf("half-close-%d", time.Now().UnixNano()))
		_, err = conn.Write(payload)
		require.NoError(t, err)
		require.NoError(t, conn.(*net.TCPConn).CloseWrite())
		got, err := io.ReadAll(conn)
		require.NoError(t, err, "read after CloseWrite")
		assert.Equal(t, payload, got)
	})
}
