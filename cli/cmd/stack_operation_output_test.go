package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tests in this file mutate package globals (printer, cfg) and command
// flags, so they do not run in parallel.

// setOutputMode sets the printer of setupStackTestCmd to format and quiet
// and returns the buffer that gets the messages (stderr).
func setOutputMode(t *testing.T, stdout *bytes.Buffer, format string, quiet bool) *bytes.Buffer {
	t.Helper()
	var stderr bytes.Buffer
	printer = output.NewPrinter(format, quiet, true)
	printer.Writer = stdout
	printer.ErrWriter = &stderr
	return &stderr
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	prev := cmd.Flags().Lookup(name).Value.String()
	require.NoError(t, cmd.Flags().Set(name, value))
	t.Cleanup(func() { _ = cmd.Flags().Set(name, prev) })
}

// TestStackOperationCmds_OutputModes: deploy, stop, clean and rollback
// print a message in the table format, the 202 response with -o json|yaml
// and the log ID with -q. stdout never mixes text into JSON or IDs.
func TestStackOperationCmds_OutputModes(t *testing.T) {
	cmds := []struct {
		cmd  *cobra.Command
		op   string
		want string
	}{
		{stackDeployCmd, "deploy", "Deploying stack 42... (log ID: log-1)\n"},
		{stackStopCmd, "stop", "Stopping stack 42... (log ID: log-1)\n"},
		{stackCleanCmd, "clean", "Cleaning stack 42... (log ID: log-1)\n"},
		{stackRollbackCmd, "rollback", "Rollback started for stack 42 (log ID: log-1)\n"},
	}
	for _, c := range cmds {
		for _, mode := range []string{"table", "json", "yaml", "quiet"} {
			t.Run(c.op+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// assert, not require: this runs outside the test goroutine.
					if !assert.Equal(t, "/api/v1/stack-instances/42/"+c.op, r.URL.Path) {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(types.DeployResponse{LogID: "log-1", Message: "started"})
				}))
				defer server.Close()
				buf := setupStackTestCmd(t, server.URL)
				format := mode
				if mode == "quiet" {
					format = "table"
				}
				stderr := setOutputMode(t, buf, format, mode == "quiet")
				if c.cmd.Flags().Lookup("yes") != nil {
					setFlag(t, c.cmd, "yes", "true")
				}

				require.NoError(t, c.cmd.RunE(c.cmd, []string{"42"}))

				var got types.DeployResponse
				switch mode {
				case "table":
					assert.Equal(t, c.want, buf.String())
				case "json":
					require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
					assert.Equal(t, types.DeployResponse{LogID: "log-1", Message: "started"}, got)
				case "yaml":
					require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
					assert.Equal(t, types.DeployResponse{LogID: "log-1", Message: "started"}, got)
				case "quiet":
					assert.Equal(t, "log-1\n", buf.String())
				}
				assert.Empty(t, stderr.String())
			})
		}
	}
}

// TestStackDryRun_MachineOutputOnStderr: the --dry-run message is for
// people, so with -o json and -q it goes to stderr and stdout stays empty.
func TestStackDryRun_MachineOutputOnStderr(t *testing.T) {
	for _, mode := range []string{"json", "quiet"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				calls.Add(1)
			}))
			defer server.Close()
			buf := setupStackTestCmd(t, server.URL)
			format := map[string]string{"json": "json", "quiet": "table"}[mode]
			stderr := setOutputMode(t, buf, format, mode == "quiet")
			setFlag(t, stackDeleteCmd, "dry-run", "true")

			require.NoError(t, stackDeleteCmd.RunE(stackDeleteCmd, []string{"my-app"}))
			assert.Empty(t, buf.String())
			assert.Equal(t, "Would delete my-app\n", stderr.String())
			assert.Zero(t, calls.Load(), "dry-run sends no request")
		})
	}
}

// TestStackDeployCmd_FollowMachineOutput: with --follow and -o json the log
// goes to stderr and stdout gets the instance after the deploy, also after
// a failed one; with -q stdout gets the log ID.
func TestStackDeployCmd_FollowMachineOutput(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		quiet   bool
		final   string
		wantErr string
	}{
		{"json running", "json", false, "running", ""},
		{"json error", "json", false, "error", "deploy failed (status error)"},
		{"yaml running", "yaml", false, "running", ""},
		{"quiet running", "table", true, "running", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getCalls atomic.Int32
			server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
				readSubscribe(conn)
				payload, _ := json.Marshal(types.WSDeploymentLog{InstanceID: "42", Line: "helm upgrade api"})
				msg, _ := json.Marshal(types.WSMessage{Type: "deployment.log", Data: payload})
				_ = conn.WriteMessage(websocket.TextMessage, msg)
				writeWatchStatus(t, conn, "42", tt.final)
				_, _, _ = conn.ReadMessage()
			}, map[string]http.HandlerFunc{
				"/api/v1/stack-instances/42/deploy": func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(types.DeployResponse{LogID: "log-1"})
				},
				"/api/v1/stack-instances/42": instanceRoute(&getCalls, "deploying", tt.final),
			})
			defer server.Close()
			buf := setupStackTestCmd(t, server.URL)
			_ = setOutputMode(t, buf, tt.format, tt.quiet)
			var cmdErr bytes.Buffer
			stackDeployCmd.SetErr(&cmdErr)
			t.Cleanup(func() { stackDeployCmd.SetErr(nil) })
			setFlag(t, stackDeployCmd, "follow", "true")

			done := make(chan error, 1)
			go func() { done <- stackDeployCmd.RunE(stackDeployCmd, []string{"42"}) }()
			var err error
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stack deploy -f did not exit")
			}

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			assert.Contains(t, cmdErr.String(), "helm upgrade api", "the log goes to stderr")
			assert.NotContains(t, buf.String(), "helm upgrade api")

			var inst types.StackInstance
			switch {
			case tt.quiet:
				assert.Equal(t, "log-1\n", buf.String())
			case tt.format == "json":
				require.NoError(t, json.Unmarshal(buf.Bytes(), &inst))
				assert.Equal(t, tt.final, inst.Status)
			default:
				require.NoError(t, yaml.Unmarshal(buf.Bytes(), &inst))
				assert.Equal(t, tt.final, inst.Status)
			}
		})
	}
}

// TestStackLogsCmd_FollowMachineOutput: `stack logs -f -o json` streams the
// log to stderr and prints the log as JSON on stdout, as without --follow.
func TestStackLogsCmd_FollowMachineOutput(t *testing.T) {
	server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		readSubscribe(conn)
		payload, _ := json.Marshal(types.WSDeploymentLog{InstanceID: "42", Line: "helm upgrade api"})
		msg, _ := json.Marshal(types.WSMessage{Type: "deployment.log", Data: payload})
		_ = conn.WriteMessage(websocket.TextMessage, msg)
		writeWatchStatus(t, conn, "42", "running")
		_, _, _ = conn.ReadMessage()
	}, map[string]http.HandlerFunc{
		"/api/v1/stack-instances/42/deploy-log": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(types.DeploymentLogResult{Data: []types.DeploymentLog{{ID: "log-1", Status: "success"}}})
		},
	})
	defer server.Close()
	buf := setupStackTestCmd(t, server.URL)
	_ = setOutputMode(t, buf, "json", false)
	var cmdErr bytes.Buffer
	stackLogsCmd.SetErr(&cmdErr)
	t.Cleanup(func() { stackLogsCmd.SetErr(nil) })
	setFlag(t, stackLogsCmd, "follow", "true")

	require.NoError(t, stackLogsCmd.RunE(stackLogsCmd, []string{"42"}))

	assert.Contains(t, cmdErr.String(), "helm upgrade api")
	var got types.DeploymentLog
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got), buf.String())
	assert.Equal(t, "log-1", got.ID)
}

// TestEmptyLists_MachineOutputIsEmptyArray: an empty list prints [] with
// -o json (not a message, not null), so `jq length` gives 0.
func TestEmptyLists_MachineOutputIsEmptyArray(t *testing.T) {
	t.Run("cluster shared-values list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()
		buf := setupStackTestCmd(t, server.URL)
		stderr := setOutputMode(t, buf, "json", false)

		require.NoError(t, clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"c1"}))
		assert.JSONEq(t, "[]", buf.String())
		assert.Empty(t, stderr.String())
	})
	t.Run("config list", func(t *testing.T) {
		buf := setupStackTestCmd(t, "http://unused")
		cfg.Contexts = nil
		stderr := setOutputMode(t, buf, "json", false)

		require.NoError(t, configListCmd.RunE(configListCmd, nil))
		assert.JSONEq(t, "[]", buf.String())
		assert.Empty(t, stderr.String())
	})
}
