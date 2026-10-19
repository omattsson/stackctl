package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeServer answers each "METHOD path" with a status and a body, and
// records the requests. An unknown route fails the test.
func routeServer(t *testing.T, routes map[string]struct {
	status int
	body   string
}) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		seen = append(seen, key)
		rt, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if strings.HasPrefix(rt.body, "{") || strings.HasPrefix(rt.body, "[") {
			w.Header().Set("Content-Type", "application/json")
		}
		if rt.status != 0 {
			w.WriteHeader(rt.status)
		}
		_, _ = io.WriteString(w, rt.body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

type route = struct {
	status int
	body   string
}

// stderrOf sets a buffer as stderr of cmd for the test.
func stderrOf(t *testing.T, cmd *cobra.Command) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetErr(&buf)
	t.Cleanup(func() { cmd.SetErr(nil) })
	return &buf
}

// ---------- stack follow / unfollow ----------

func TestStackFollowCmd(t *testing.T) {
	tests := []struct {
		name   string
		cmd    *cobra.Command
		method string
		body   string
		format string
		want   string
	}{
		{"follow", stackFollowCmd, http.MethodPost, `{"following":true,"follower_count":1}`, "table", "You follow stack 42 (1 follower)\n"},
		{"unfollow", stackUnfollowCmd, http.MethodDelete, `{"following":false,"follower_count":3}`, "table", "You do not follow stack 42 (3 followers)\n"},
		{"follow json", stackFollowCmd, http.MethodPost, `{"following":true,"follower_count":2}`, "json", `"follower_count": 2`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := routeServer(t, map[string]route{
				tt.method + " /api/v1/stack-instances/42/follow": {body: tt.body},
			})
			buf := setupStackTestCmd(t, srv.URL)
			printer.Format = output.Format(tt.format)

			require.NoError(t, tt.cmd.RunE(tt.cmd, []string{"42"}))
			assert.Equal(t, []string{tt.method + " /api/v1/stack-instances/42/follow"}, *seen)
			assert.Contains(t, buf.String(), tt.want)
		})
	}
}

func TestStackFollowCmd_ServerErrors(t *testing.T) {
	tests := []struct {
		name string
		rt   route
		want string
	}{
		{"old server without the route", route{http.StatusNotFound, "404 page not found"}, "upgrade k8s-stack-manager to v0.8.0 or later"},
		{"not configured", route{http.StatusNotImplemented, `{"error":"Following is not available"}`}, "following is not configured on the server"},
		{"forbidden", route{http.StatusForbidden, `{"error":"no access"}`}, "Permission denied."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := routeServer(t, map[string]route{"POST /api/v1/stack-instances/42/follow": tt.rt})
			_ = setupStackTestCmd(t, srv.URL)

			err := stackFollowCmd.RunE(stackFollowCmd, []string{"42"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestStackGetCmd_FollowRows(t *testing.T) {
	following := true
	count := int64(4)
	inst := sampleStack()
	inst.Status = "draft" // no access URL lookup
	inst.Following = &following
	inst.FollowerCount = &count
	data, err := json.Marshal(inst)
	require.NoError(t, err)
	srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/42": {body: string(data)}})
	buf := setupStackTestCmd(t, srv.URL)

	require.NoError(t, stackGetCmd.RunE(stackGetCmd, []string{"42"}))
	out := buf.String()
	assert.Regexp(t, `Following:\s+yes`, out)
	assert.Regexp(t, `Followers:\s+4`, out)
}

func TestStackGetCmd_NoFollowRowsOnOldServer(t *testing.T) {
	inst := sampleStack()
	inst.Status = "draft"
	data, err := json.Marshal(inst)
	require.NoError(t, err)
	srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/42": {body: string(data)}})
	buf := setupStackTestCmd(t, srv.URL)

	require.NoError(t, stackGetCmd.RunE(stackGetCmd, []string{"42"}))
	assert.NotContains(t, buf.String(), "Following")
	assert.NotContains(t, buf.String(), "Followers")
}

// ---------- stack preview ----------

const previewBody = `{"instance_id":"42","instance_name":"my-stack","charts":[` +
	`{"chart_name":"backend","previous_values":"replicas: 1\n","pending_values":"replicas: 2\n","has_changes":true},` +
	`{"chart_name":"frontend","previous_values":"a: b","pending_values":"a: b","has_changes":false}]}`

func setupPreview(t *testing.T, body string) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/42/deploy-preview": {body: body}})
	buf := setupStackTestCmd(t, srv.URL)
	stderr := stderrOf(t, stackPreviewCmd)
	resetCmdFlags(t, stackPreviewCmd, "chart")
	return buf, stderr
}

func TestStackPreviewCmd_AllCharts(t *testing.T) {
	buf, stderr := setupPreview(t, previewBody)

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	want := "---\n# chart: backend (changed since the last deploy)\nreplicas: 2\n" +
		"---\n# chart: frontend (no change since the last deploy)\na: b\n"
	assert.Equal(t, want, buf.String())
	assert.Empty(t, stderr.String())
}

func TestStackPreviewCmd_OneChartIsPlainYAML(t *testing.T) {
	buf, _ := setupPreview(t, previewBody)
	require.NoError(t, stackPreviewCmd.Flags().Set("chart", "backend"))

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	assert.Equal(t, "replicas: 2\n", buf.String())
}

func TestStackPreviewCmd_UnknownChart(t *testing.T) {
	_, _ = setupPreview(t, previewBody)
	require.NoError(t, stackPreviewCmd.Flags().Set("chart", "db"))

	err := stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"})
	require.Error(t, err)
	assert.Equal(t, `chart "db" is not in the stack (charts: backend, frontend)`, err.Error())
}

func TestStackPreviewCmd_QuietListsChangedCharts(t *testing.T) {
	buf, _ := setupPreview(t, previewBody)
	printer.Quiet = true

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	assert.Equal(t, "backend\n", buf.String())
}

func TestStackPreviewCmd_DriftWarningOnStderr(t *testing.T) {
	body := strings.Replace(previewBody, `"charts":[`, `"values_drift":true,"charts":[`, 1)
	buf, stderr := setupPreview(t, body)
	require.NoError(t, stackPreviewCmd.Flags().Set("chart", "backend"))

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	assert.Equal(t, "replicas: 2\n", buf.String(), "stdout stays plain YAML")
	assert.Equal(t, "Warning: "+types.ValuesDriftWarning+"\n", stderr.String())
}

func TestStackPreviewCmd_NeverDeployed(t *testing.T) {
	body := `{"charts":[{"chart_name":"api","previous_values":"","pending_values":"x: 1\n","has_changes":true}]}`
	buf, _ := setupPreview(t, body)

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	assert.Equal(t, "---\n# chart: api (no previous deploy)\nx: 1\n", buf.String())
}

func TestStackPreviewCmd_NoCharts(t *testing.T) {
	buf, stderr := setupPreview(t, `{"charts":[]}`)

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	assert.Empty(t, buf.String())
	assert.Equal(t, "The stack has no charts.\n", stderr.String())
}

func TestStackPreviewCmd_JSONHasBothValues(t *testing.T) {
	buf, _ := setupPreview(t, previewBody)
	printer.Format = output.FormatJSON

	require.NoError(t, stackPreviewCmd.RunE(stackPreviewCmd, []string{"42"}))
	var got types.DeployPreview
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got.Charts, 2)
	assert.Equal(t, "replicas: 1\n", got.Charts[0].PreviousValues)
	assert.Equal(t, "replicas: 2\n", got.Charts[0].PendingValues)
}

// ---------- stack pods ----------

func podsBody(t *testing.T) string {
	t.Helper()
	start := time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)
	status := types.InstanceStatus{
		Namespace: "stack-demo",
		Charts: []types.ChartStatus{{
			ChartName: "backend",
			Pods: []types.PodStatus{
				{Name: "backend-1", Phase: "Running", Ready: true, StartTime: &start,
					ContainerStates: []types.ContainerStateInfo{{Name: "app", Ready: true}, {Name: "sidecar", Ready: true}}},
				{Name: "backend-2", Phase: "Running", RestartCount: 7, StartTime: &start,
					ContainerStates: []types.ContainerStateInfo{{Name: "app", Reason: "CrashLoopBackOff"}, {Name: "sidecar", Ready: true}}},
			},
		}},
		Events: []types.PodEvent{
			{Type: "Normal", Reason: "Pulled", Object: "Pod/backend-1", Count: 1, LastSeen: start},
			{Type: "Warning", Reason: "BackOff", Object: "Pod/backend-2", Count: 18, LastSeen: start.Add(50 * time.Minute), Message: "Back-off\x1b[2J restarting"},
			{Type: "Warning", Reason: "Evil\x1b]0;x\x07", Object: "Pod/\tbad", Count: 1, LastSeen: start.Add(40 * time.Minute)},
		},
	}
	data, err := json.Marshal(status)
	require.NoError(t, err)
	return string(data)
}

func setupPods(t *testing.T) *bytes.Buffer {
	t.Helper()
	useExpiryClock(t) // 2026-10-15T12:00Z
	srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/42/pods": {body: podsBody(t)}})
	buf := setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackPodsCmd, "all-events")
	return buf
}

func TestStackPodsCmd_Table(t *testing.T) {
	buf := setupPods(t)

	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"42"}))
	out := buf.String()
	assert.Regexp(t, `backend\s+backend-1\s+Running\s+2/2\s+0\s+1h\s+-`, out)
	assert.Regexp(t, `backend\s+backend-2\s+Running\s+1/2\s+7\s+1h\s+CrashLoopBackOff`, out)
	assert.Contains(t, out, "Events:")
	assert.Regexp(t, `10m\s+Warning\s+BackOff\s+Pod/backend-2\s+18\s+Back-off \[2J restarting`, out, "the message is sanitized")
	assert.NotContains(t, out, "Pulled", "Normal events need --all-events")
	assert.NotContains(t, out, "\x1b", "no escape character reaches the terminal")
	assert.NotContains(t, out, "\x07")
	assert.Contains(t, out, "Evil ]0;x")
}

func TestStackPodsCmd_JSON(t *testing.T) {
	buf := setupPods(t)
	printer.Format = output.FormatJSON

	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"42"}))
	var got types.InstanceStatus
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got.Events, 3, "-o json keeps all events")
	assert.Equal(t, "CrashLoopBackOff", got.Charts[0].Pods[1].ContainerStates[0].Reason)
}

func TestStackPodsCmd_AllEvents(t *testing.T) {
	buf := setupPods(t)
	require.NoError(t, stackPodsCmd.Flags().Set("all-events", "true"))

	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"42"}))
	out := buf.String()
	assert.Contains(t, out, "Pulled")
	assert.Less(t, strings.Index(out, "BackOff"), strings.Index(out, "Pulled"), "the newest event comes first")
}

func TestStackPodsCmd_Quiet(t *testing.T) {
	buf := setupPods(t)
	printer.Quiet = true

	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"42"}))
	assert.Equal(t, "backend-1\nbackend-2\n", buf.String())
}

func TestStackPodsCmd_NoPods(t *testing.T) {
	srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/42/pods": {body: `{"namespace":"stack-demo","charts":[]}`}})
	buf := setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackPodsCmd, "all-events")

	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"42"}))
	assert.Equal(t, "No pods found in namespace stack-demo.\n", buf.String())
}

// ---------- stack recent ----------

func TestStackRecentCmd(t *testing.T) {
	inst := sampleStack()
	data, err := json.Marshal([]types.StackInstance{inst})
	require.NoError(t, err)

	t.Run("table", func(t *testing.T) {
		srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/recent": {body: string(data)}})
		buf := setupStackTestCmd(t, srv.URL)
		require.NoError(t, stackRecentCmd.RunE(stackRecentCmd, nil))
		assert.Contains(t, buf.String(), "EXPIRES")
		assert.Contains(t, buf.String(), inst.Name)
	})
	t.Run("quiet", func(t *testing.T) {
		srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/recent": {body: string(data)}})
		buf := setupStackTestCmd(t, srv.URL)
		printer.Quiet = true
		require.NoError(t, stackRecentCmd.RunE(stackRecentCmd, nil))
		assert.Equal(t, inst.ID+"\n", buf.String())
	})
	t.Run("json", func(t *testing.T) {
		srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/recent": {body: string(data)}})
		buf := setupStackTestCmd(t, srv.URL)
		printer.Format = output.FormatJSON
		require.NoError(t, stackRecentCmd.RunE(stackRecentCmd, nil))
		var got []types.StackInstance
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		require.Len(t, got, 1)
		assert.Equal(t, inst.ID, got[0].ID)
	})
	t.Run("empty", func(t *testing.T) {
		srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/recent": {body: `[]`}})
		buf := setupStackTestCmd(t, srv.URL)
		require.NoError(t, stackRecentCmd.RunE(stackRecentCmd, nil))
		assert.Equal(t, "You have no stack instances.\n", buf.String())
	})
}
