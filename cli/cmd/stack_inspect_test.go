package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

func TestExecContainersText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ch   types.ChartStatus
		want string
	}{
		{name: "no list", want: "-"},
		{name: "one container", ch: types.ChartStatus{ExecContainers: []string{"web"}}, want: "web"},
		{name: "default marked", ch: types.ChartStatus{ExecContainers: []string{"web", "worker"}, ExecDefaultContainer: "web"}, want: "web*, worker"},
		{name: "only names that are not container names", ch: types.ChartStatus{ExecContainers: unsafeContainerNames, ExecDefaultContainer: "$(id)"}, want: "-"},
		{name: "unsafe names dropped, valid kept", ch: types.ChartStatus{
			ExecContainers:       append(append([]string{"web"}, unsafeContainerNames...), validName63),
			ExecDefaultContainer: "web",
		}, want: "web*, " + validName63},
		{name: "fake marker and separator", ch: types.ChartStatus{ExecContainers: []string{"web*, admin", "worker"}}, want: "worker"},
		{name: "an invalid default gets no marker", ch: types.ChartStatus{ExecContainers: []string{"web", "Web"}, ExecDefaultContainer: "Web"}, want: "web"},
		{name: "a default not in the list", ch: types.ChartStatus{ExecContainers: []string{"web"}, ExecDefaultContainer: "worker"}, want: "web"},
		{name: "duplicates", ch: types.ChartStatus{ExecContainers: []string{"web", "web", "worker"}, ExecDefaultContainer: "web"}, want: "web*, worker"},
		{name: "empty list", ch: types.ChartStatus{ExecContainers: []string{}}, want: "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, execContainersText(tt.ch))
		})
	}
}

// podsHintsServer serves GET /api/v1/stack-instances/42/pods with body and
// records the query of each request.
func podsHintsServer(t *testing.T, body string) (*httptest.Server, func() []url.Values) {
	t.Helper()
	var mu sync.Mutex
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/stack-instances/42/pods" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return append([]url.Values(nil), queries...)
	}
}

// runPods runs "stack pods 42" in the output mode ("table", "json", "yaml"
// or "quiet") and returns stdout.
func runPods(t *testing.T, body, mode string) (string, func() []url.Values) {
	t.Helper()
	useExpiryClock(t) // 2026-10-15T12:00Z
	srv, queries := podsHintsServer(t, body)
	buf := setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackPodsCmd, "all-events")
	switch mode {
	case "json":
		printer.Format = output.FormatJSON
	case "yaml":
		printer.Format = output.FormatYAML
	case "quiet":
		printer.Quiet = true
	}
	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"42"}))
	return buf.String(), queries
}

// podsTableRows returns the header and the pod rows of the pods table
// (the lines before the events).
func podsTableRows(t *testing.T, out string) (header string, rows []string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.NotEmpty(t, lines)
	header = lines[0]
	for _, l := range lines[1:] {
		if l == "" || strings.HasPrefix(l, "Events:") {
			break
		}
		rows = append(rows, l)
	}
	return header, rows
}

// podsHintsPod is one pod of a chart in podsHintsBody, started an hour
// before the test clock.
func podsHintsPod(name string) string {
	return `{"name":"` + name + `","phase":"Running","ready":true,"restart_count":0,"start_time":"2026-10-15T11:00:00Z",` +
		`"container_states":[{"name":"app","ready":true}]}`
}

func TestStackPodsCmd_ExecHints(t *testing.T) {
	tests := []struct {
		name string
		// charts is the "charts" array of the response (exec_hints: true).
		charts string
		// wantRows are "CHART POD EXEC" per table row.
		wantRows [][3]string
		// wantJSON are exec_containers / exec_default_container per chart as
		// received (nil: absent).
		wantContainers [][]any
		wantDefault    []any
		wantPods       []string
	}{
		{
			name:           "one container with default",
			charts:         `[{"chart_name":"api","release_name":"api","pods":[` + podsHintsPod("api-0") + `],"exec_containers":["app"],"exec_default_container":"app"}]`,
			wantRows:       [][3]string{{"api", "api-0", "app*"}},
			wantContainers: [][]any{{"app"}},
			wantDefault:    []any{"app"},
			wantPods:       []string{"api-0"},
		},
		{
			name:           "two containers, default marked",
			charts:         `[{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") + `],"exec_containers":["web","worker"],"exec_default_container":"web"}]`,
			wantRows:       [][3]string{{"web", "web-0", "web*, worker"}},
			wantContainers: [][]any{{"web", "worker"}},
			wantDefault:    []any{"web"},
			wantPods:       []string{"web-0"},
		},
		{
			name:           "two containers, default is the second",
			charts:         `[{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") + `],"exec_containers":["web","worker"],"exec_default_container":"worker"}]`,
			wantRows:       [][3]string{{"web", "web-0", "web, worker*"}},
			wantContainers: [][]any{{"web", "worker"}},
			wantDefault:    []any{"worker"},
			wantPods:       []string{"web-0"},
		},
		{
			name:           "two containers without default",
			charts:         `[{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") + `],"exec_containers":["web","worker"]}]`,
			wantRows:       [][3]string{{"web", "web-0", "web, worker"}},
			wantContainers: [][]any{{"web", "worker"}},
			wantDefault:    []any{nil},
			wantPods:       []string{"web-0"},
		},
		{
			name:           "empty list",
			charts:         `[{"chart_name":"tok","release_name":"tok","pods":[` + podsHintsPod("tok-0") + `]}]`,
			wantRows:       [][3]string{{"tok", "tok-0", "-"}},
			wantContainers: [][]any{nil},
			wantDefault:    []any{nil},
			wantPods:       []string{"tok-0"},
		},
		{
			name:           "empty list sent as []",
			charts:         `[{"chart_name":"tok","release_name":"tok","pods":[` + podsHintsPod("tok-0") + `],"exec_containers":[]}]`,
			wantRows:       [][3]string{{"tok", "tok-0", "-"}},
			wantContainers: [][]any{nil}, // omitempty, as the server marshals it
			wantDefault:    []any{nil},
			wantPods:       []string{"tok-0"},
		},
		{
			name: "several pods of one chart",
			charts: `[{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") + `,` + podsHintsPod("web-1") + `,` + podsHintsPod("web-2") +
				`],"exec_containers":["web","worker"],"exec_default_container":"web"}]`,
			wantRows:       [][3]string{{"web", "web-0", "web*, worker"}, {"web", "web-1", "web*, worker"}, {"web", "web-2", "web*, worker"}},
			wantContainers: [][]any{{"web", "worker"}},
			wantDefault:    []any{"web"},
			wantPods:       []string{"web-0", "web-1", "web-2"},
		},
		{
			name: "multi-chart mix",
			charts: `[` +
				`{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") + `,` + podsHintsPod("web-1") + `],"exec_containers":["web","worker"],"exec_default_container":"web"},` +
				`{"chart_name":"mysql","release_name":"db","pods":[` + podsHintsPod("db-0") + `]},` +
				`{"chart_name":"cache","release_name":"cache","pods":[` + podsHintsPod("cache-0") + `],"exec_containers":["redis"]},` +
				`{"chart_name":"jobs","release_name":"jobs","pods":[]}` +
				`]`,
			wantRows: [][3]string{
				{"web", "web-0", "web*, worker"}, {"web", "web-1", "web*, worker"},
				{"mysql", "db-0", "-"}, {"cache", "cache-0", "redis"},
			},
			wantContainers: [][]any{{"web", "worker"}, nil, {"redis"}, nil},
			wantDefault:    []any{"web", nil, nil, nil},
			wantPods:       []string{"web-0", "web-1", "db-0", "cache-0"},
		},
	}
	for _, tt := range tests {
		body := `{"namespace":"stack-demo","exec_hints":true,"charts":` + tt.charts + `}`
		t.Run(tt.name+"/table", func(t *testing.T) {
			out, queries := runPods(t, body, "table")
			header, rows := podsTableRows(t, out)
			assert.Equal(t, []string{"CHART", "POD", "PHASE", "READY", "RESTARTS", "AGE", "REASON", "EXEC"}, strings.Fields(header), "EXEC is the last column")
			require.Len(t, rows, len(tt.wantRows), "one row per pod:\n%s", out)
			execCol := strings.Index(header, "EXEC")
			require.Positive(t, execCol)
			for i, want := range tt.wantRows {
				f := strings.Fields(rows[i])
				require.GreaterOrEqual(t, len(f), 8, rows[i])
				assert.Equal(t, want[0], f[0], "CHART of row %d", i)
				assert.Equal(t, want[1], f[1], "POD of row %d", i)
				assert.Equal(t, []string{"Running", "1/1", "0", "1h", "-"}, f[2:7], "the other columns keep their values")
				require.Greater(t, len(rows[i]), execCol, rows[i])
				assert.Equal(t, want[2], rows[i][execCol:], "EXEC of row %d is aligned under the header and last", i)
			}
			q := queries()
			require.Len(t, q, 1)
			assert.Equal(t, url.Values{"exec_hints": {"true"}}, q[0], "the request asks for exec hints")
		})
		t.Run(tt.name+"/json", func(t *testing.T) {
			out, queries := runPods(t, body, "json")
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &got), out)
			assertExecFields(t, got, tt.wantContainers, tt.wantDefault)
			assert.Equal(t, url.Values{"exec_hints": {"true"}}, queries()[0])
		})
		t.Run(tt.name+"/yaml", func(t *testing.T) {
			out, queries := runPods(t, body, "yaml")
			var got map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(out), &got), out)
			assertExecFields(t, got, tt.wantContainers, tt.wantDefault)
			assert.Equal(t, url.Values{"exec_hints": {"true"}}, queries()[0])
		})
		t.Run(tt.name+"/quiet", func(t *testing.T) {
			out, queries := runPods(t, body, "quiet")
			assert.Equal(t, strings.Join(tt.wantPods, "\n")+"\n", out, "-q prints the pod names only")
			q := queries()
			require.Len(t, q, 1)
			assert.Equal(t, url.Values{}, q[0], "-q does not ask for exec hints")
		})
	}
}

// assertExecFields checks the exec fields of a decoded -o json or -o yaml
// output of stack pods.
func assertExecFields(t *testing.T, got map[string]any, wantContainers [][]any, wantDefault []any) {
	t.Helper()
	assert.Equal(t, true, got["exec_hints"])
	charts, ok := got["charts"].([]any)
	require.True(t, ok, "charts: %v", got["charts"])
	require.Len(t, charts, len(wantContainers))
	for i, raw := range charts {
		ch, ok := raw.(map[string]any)
		require.True(t, ok)
		gotList, has := ch["exec_containers"]
		if wantContainers[i] == nil {
			assert.False(t, has, "chart %d has no exec_containers: %v", i, gotList)
		} else {
			assert.Equal(t, wantContainers[i], gotList, "exec_containers of chart %d", i)
		}
		gotDefault, has := ch["exec_default_container"]
		if wantDefault[i] == nil {
			assert.False(t, has, "chart %d has no exec_default_container: %v", i, gotDefault)
		} else {
			assert.Equal(t, wantDefault[i], gotDefault, "exec_default_container of chart %d", i)
		}
	}
}

func TestStackPodsCmd_NoExecHintsOlderServer(t *testing.T) {
	t.Run("table identical to before", func(t *testing.T) {
		out, queries := runPods(t, podsBody(t), "table")
		want, err := os.ReadFile(filepath.Join("testdata", "stack_pods_table.golden.txt"))
		require.NoError(t, err)
		assert.Equal(t, string(want), out, "the table must match testdata/stack_pods_table.golden.txt (the output without exec hints)")
		header, _ := podsTableRows(t, out)
		assert.Equal(t, []string{"CHART", "POD", "PHASE", "READY", "RESTARTS", "AGE", "REASON"}, strings.Fields(header))
		assert.NotContains(t, out, "EXEC")
		assert.Equal(t, url.Values{"exec_hints": {"true"}}, queries()[0], "the request asks for hints also when the server sends none")
	})
	t.Run("exec fields without exec_hints are not shown", func(t *testing.T) {
		// exec_hints is what turns the column on, not the per-chart lists.
		body := `{"namespace":"stack-demo","charts":[{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") +
			`],"exec_containers":["web"],"exec_default_container":"web"}]}`
		out, _ := runPods(t, body, "table")
		header, rows := podsTableRows(t, out)
		assert.Equal(t, []string{"CHART", "POD", "PHASE", "READY", "RESTARTS", "AGE", "REASON"}, strings.Fields(header))
		require.Len(t, rows, 1)
		assert.Equal(t, []string{"web", "web-0", "Running", "1/1", "0", "1h", "-"}, strings.Fields(rows[0]))
	})
	t.Run("exec_hints false", func(t *testing.T) {
		body := `{"namespace":"stack-demo","exec_hints":false,"charts":[{"chart_name":"web","release_name":"web","pods":[` + podsHintsPod("web-0") + `]}]}`
		out, _ := runPods(t, body, "table")
		assert.NotContains(t, out, "EXEC")
	})
	for _, mode := range []string{"json", "yaml"} {
		t.Run(mode+" has no exec fields", func(t *testing.T) {
			out, _ := runPods(t, podsBody(t), mode)
			assert.NotContains(t, out, "exec_hints")
			assert.NotContains(t, out, "exec_containers")
			assert.NotContains(t, out, "exec_default_container")
		})
	}
	t.Run("quiet unchanged", func(t *testing.T) {
		out, queries := runPods(t, podsBody(t), "quiet")
		assert.Equal(t, "backend-1\nbackend-2\n", out)
		q := queries()
		require.Len(t, q, 1)
		assert.Equal(t, url.Values{}, q[0], "-q does not ask for exec hints")
	})
}

func TestStackPodsCmd_ExecHintsNoPods(t *testing.T) {
	// With hints and without pods the table is not printed at all.
	out, _ := runPods(t, `{"namespace":"stack-demo","exec_hints":true,"charts":[{"chart_name":"web","pods":[],"exec_containers":["web"]}]}`, "table")
	assert.Equal(t, "No pods found in namespace stack-demo.\n", out)
}

func TestStackPodsCmd_ExecHintsWithEvents(t *testing.T) {
	// The events table keeps its columns; only the pods table gets EXEC.
	var status map[string]any
	require.NoError(t, json.Unmarshal([]byte(podsBody(t)), &status))
	status["exec_hints"] = true
	charts := status["charts"].([]any)
	charts[0].(map[string]any)["exec_containers"] = []string{"app", "sidecar"}
	charts[0].(map[string]any)["exec_default_container"] = "app"
	data, err := json.Marshal(status)
	require.NoError(t, err)

	out, _ := runPods(t, string(data), "table")
	header, rows := podsTableRows(t, out)
	assert.Equal(t, "EXEC", strings.Fields(header)[7])
	require.Len(t, rows, 2)
	assert.True(t, strings.HasSuffix(rows[0], "app*, sidecar"), rows[0])
	assert.True(t, strings.HasSuffix(rows[1], "app*, sidecar"), rows[1])
	assert.Regexp(t, `backend\s+backend-2\s+Running\s+1/2\s+7\s+1h\s+CrashLoopBackOff\s+app\*, sidecar`, out)
	assert.Contains(t, out, "LAST SEEN")
	assert.Regexp(t, `\n\s*LAST SEEN\s+TYPE\s+REASON\s+OBJECT\s+COUNT\s+MESSAGE\n`, out, "the events header has no EXEC")
}

func TestStackPodsCmd_ExecHintsOnlyValidNames(t *testing.T) {
	// Container names come from the server: only Kubernetes container names
	// are shown, so the column cannot carry shell syntax, a fake "*" or ", ",
	// escapes or bidi controls.
	charts := []map[string]any{
		{"chart_name": "web", "release_name": "web", "pods": []any{json.RawMessage(podsHintsPod("web-0"))},
			"exec_containers":        append(append([]string{"ok"}, unsafeContainerNames...), validName63),
			"exec_default_container": "a\x1b[2J"},
		{"chart_name": "tok", "release_name": "tok", "pods": []any{json.RawMessage(podsHintsPod("tok-0"))},
			"exec_containers": unsafeContainerNames, "exec_default_container": "$(id)"},
	}
	data, err := json.Marshal(map[string]any{"namespace": "stack-demo", "exec_hints": true, "charts": charts})
	require.NoError(t, err)
	out, _ := runPods(t, string(data), "table")
	header, rows := podsTableRows(t, out)
	execCol := strings.Index(header, "EXEC")
	require.Positive(t, execCol)
	require.Len(t, rows, 2)
	assert.Equal(t, "ok, "+validName63, rows[0][execCol:], "unsafe names dropped; an invalid default gets no marker")
	assert.Equal(t, "-", rows[1][execCol:], "no valid name left: as an empty list")
	for _, bad := range unsafeContainerNames {
		assert.NotContains(t, out, bad)
	}
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\u202e")
	assert.NotContains(t, out, "\u061c")
	assert.NotContains(t, out, "*")
}

func TestStackPodsCmd_SanitizedCells(t *testing.T) {
	// CHART, POD, PHASE and the namespace come from the cluster: control
	// characters, ANSI escapes and bidi controls never reach the terminal.
	t.Run("pods table", func(t *testing.T) {
		body := `{"namespace":"stack-demo","charts":[{"chart_name":"we\u001b[2Jb","release_name":"web","pods":[` +
			`{"name":"web\u202e-0\u0007","phase":"Run\u001b]0;pwned\u0007ning","ready":true,"restart_count":0,"start_time":"2026-10-15T11:00:00Z",` +
			`"container_states":[{"name":"app","ready":true}]}]}]}`
		out, _ := runPods(t, body, "table")
		_, rows := podsTableRows(t, out)
		require.Len(t, rows, 1)
		assert.Equal(t, []string{"we", "[2Jb", "web-0", "Run", "]0;pwned", "ning", "1/1", "0", "1h", "-"}, strings.Fields(rows[0]))
		assert.NotContains(t, out, "\x1b")
		assert.NotContains(t, out, "\x07")
		assert.NotContains(t, out, "\u202e")
	})
	t.Run("namespace of the empty message", func(t *testing.T) {
		out, _ := runPods(t, `{"namespace":"stack\u001b[2J-demo\u061c","charts":[]}`, "table")
		assert.Equal(t, "No pods found in namespace stack [2J-demo.\n", out)
	})
}

func TestStackPodsCmd_ExecHintsByName(t *testing.T) {
	// A name is resolved first; the pods request of the ID asks for hints.
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/stack-instances":
			_, _ = io.WriteString(w, `{"data":[{"id":"42","name":"my-stack"}],"total":1,"page":1,"pageSize":25}`)
		case "/api/v1/stack-instances/42/pods":
			_, _ = io.WriteString(w, `{"namespace":"ns","exec_hints":true,"charts":[{"chart_name":"web","pods":[`+podsHintsPod("web-0")+`],"exec_containers":["web"]}]}`)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	useExpiryClock(t)
	buf := setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackPodsCmd, "all-events")
	require.NoError(t, stackPodsCmd.RunE(stackPodsCmd, []string{"my-stack"}))
	assert.Regexp(t, `web\s+web-0\s+.*\s+web\n`, buf.String())
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Equal(t, "/api/v1/stack-instances/42/pods?exec_hints=true", seen[1])
}

func TestStackPodsCmd_ServerErrorWithHints(t *testing.T) {
	// The extra query never changes the error mapping of the route.
	srv, _ := routeServer(t, map[string]route{"GET /api/v1/stack-instances/42/pods": {status: http.StatusForbidden, body: `{"error":"no access"}`}})
	setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackPodsCmd, "all-events")
	err := stackPodsCmd.RunE(stackPodsCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
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
