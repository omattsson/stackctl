package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// name64 is a valid DNS-1123 label but for its length of 64 characters.
var name64 = strings.Repeat("a", 31) + "-" + strings.Repeat("b", 32)

// invalidContainerNames are names that a server must not list and that
// stackctl drops: shell syntax, a flag, a fake marker and separator, upper
// case, too long, control and bidi characters.
var invalidContainerNames = []string{
	"$(id)", "`id`", "-v", "web*, admin", "Web", name64,
	"a\x1b[2J", "a\u202eb", "\u202e", "a b", "a\u061cb", "a.b", "a_b", "web-", "", "$HOME", "a;rm",
}

func TestValidContainerName(t *testing.T) {
	t.Parallel()
	require.Len(t, name64, 64)
	for _, name := range []string{"a", "0", "web", "web-0", "a-b-c", "9lives", name64[:63]} {
		t.Run("valid "+name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, ValidContainerName(name))
		})
	}
	for _, name := range invalidContainerNames {
		t.Run(fmt.Sprintf("invalid %q", name), func(t *testing.T) {
			t.Parallel()
			assert.False(t, ValidContainerName(name))
		})
	}
}

func TestExecContainerNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: []string{}},
		{name: "all valid, order kept", in: []string{"worker", "web"}, want: []string{"worker", "web"}},
		{name: "duplicates dropped", in: []string{"web", "web", "worker", "web"}, want: []string{"web", "worker"}},
		{name: "invalid names dropped", in: append(append([]string{"web"}, invalidContainerNames...), "worker"), want: []string{"web", "worker"}},
		{name: "only invalid names", in: invalidContainerNames, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ExecContainerNames(tt.in))
		})
	}
}

func TestExecHintsForChart(t *testing.T) {
	t.Parallel()
	hinted := &types.InstanceStatus{ExecHints: true, Charts: []types.ChartStatus{
		{ReleaseName: "web", ChartName: "nginx", ExecContainers: []string{"web", "worker"}},
		{ReleaseName: "db", ChartName: "mysql"},
	}}
	tests := []struct {
		name   string
		status *types.InstanceStatus
		chart  string
		want   *ExecChartHints
	}{
		{name: "nil status", chart: "web"},
		{name: "no exec_hints", status: &types.InstanceStatus{Charts: hinted.Charts}, chart: "web"},
		{name: "by release name", status: hinted, chart: "web", want: &ExecChartHints{Chart: "web", Containers: []string{"web", "worker"}}},
		{name: "by chart name", status: hinted, chart: "nginx", want: &ExecChartHints{Chart: "nginx", Containers: []string{"web", "worker"}}},
		{name: "computed, no list for the chart", status: hinted, chart: "db", want: &ExecChartHints{Chart: "db", Containers: []string{}}},
		{name: "chart not in the response", status: hinted, chart: "cache"},
		{name: "no chart has a list", status: &types.InstanceStatus{ExecHints: true, Charts: []types.ChartStatus{
			{ReleaseName: "web"}, {ReleaseName: "db"},
		}}, chart: "web"},
		{name: "names that are not container names are dropped", status: &types.InstanceStatus{ExecHints: true, Charts: []types.ChartStatus{
			{ReleaseName: "web", ExecContainers: []string{"$(id)", "web", "-v", "Web", "web"}},
		}}, chart: "web", want: &ExecChartHints{Chart: "web", Containers: []string{"web"}}},
		{name: "only invalid names while another chart has a list: empty", status: &types.InstanceStatus{ExecHints: true, Charts: []types.ChartStatus{
			{ReleaseName: "web", ExecContainers: []string{"web"}},
			{ReleaseName: "tok", ExecContainers: []string{"web*, admin", "`id`"}},
		}}, chart: "tok", want: &ExecChartHints{Chart: "tok", Containers: []string{}}},
		{name: "only invalid names on every chart: unknown", status: &types.InstanceStatus{ExecHints: true, Charts: []types.ChartStatus{
			{ReleaseName: "web", ExecContainers: []string{"$(id)"}},
			{ReleaseName: "tok", ExecContainers: []string{"-v"}},
		}}, chart: "web"},
		{name: "release name wins over chart name", status: &types.InstanceStatus{ExecHints: true, Charts: []types.ChartStatus{
			{ReleaseName: "a", ChartName: "b", ExecContainers: []string{"x"}},
			{ReleaseName: "b", ExecContainers: []string{"y"}},
		}}, chart: "b", want: &ExecChartHints{Chart: "b", Containers: []string{"y"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ExecHintsForChart(tt.status, tt.chart))
		})
	}
}

func TestWSHandshakeError_ExecHintAdvice(t *testing.T) {
	t.Parallel()
	many := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	tests := []struct {
		name         string
		code         string
		message      string
		noServerText bool
		container    string
		hints        *ExecChartHints
		want         string
	}{
		{name: "not allowed, two others", code: StreamCodeContainerNotAllowed, message: "The container is not allowed for exec", container: "php",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"web", "worker"}},
			want:  `exec refused by the server (HTTP 403): The container is not allowed for exec: "php". Use -c web or -c worker.`},
		{name: "three containers", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"a", "b", "c"}},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c a, -c b or -c c."},
		{name: "asked container left out", code: StreamCodeContainerNotFound, message: "No ready pod runs the container", container: "web",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"web", "worker"}},
			want:  `exec refused by the server (HTTP 404): No ready pod runs the container: "web". Use -c worker.`},
		{name: "only the asked container keeps the advice", code: StreamCodeContainerNotFound, message: "No ready pod runs the container", container: "web",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"web"}},
			want:  `exec refused by the server (HTTP 404): No ready pod runs the container: "web". Check the container name and 'stackctl stack pods'.`},
		{name: "no container of the chart", code: StreamCodeContainerNotAllowed, message: "The container is not allowed for exec",
			hints: &ExecChartHints{Chart: "ksm-tok"},
			want:  "exec refused by the server (HTTP 403): The container is not allowed for exec. The server lists no container of chart ksm-tok that accepts exec; ask an administrator."},
		{name: "capped list", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: many},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c a, -c b, -c c, -c d, -c e, -c f, -c g, -c h or one of 2 more (see 'stackctl stack pods')."},
		{name: "without server text the meaning comes first", code: StreamCodeServiceAccountToken, message: "Forbidden", noServerText: true,
			hints: &ExecChartHints{Chart: "web", Containers: []string{"worker"}},
			want:  "exec refused by the server (HTTP 403): Forbidden. The container holds a service-account token. Use -c worker."},
		{name: "unknown hints keep the advice", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			want: "exec refused by the server (HTTP 403): The container holds a service-account token. The server refuses exec into such a container; ask an administrator."},
		{name: "other codes ignore hints", code: StreamCodeClusterExecDisabled, message: "This cluster does not allow exec",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"web"}},
			want:  "exec refused by the server (HTTP 403): This cluster does not allow exec. Ask an administrator."},
		{name: "duplicates and empty names are dropped", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"web", "", "web", "worker", "\u200e", "worker"}},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c web or -c worker."},
		{name: "eight names are not capped", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: many[:8]},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c a, -c b, -c c, -c d, -c e, -c f, -c g or -c h."},
		{name: "nine names are capped", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: many[:9]},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c a, -c b, -c c, -c d, -c e, -c f, -c g, -c h or one of 1 more (see 'stackctl stack pods')."},
		{name: "not found without -c", code: StreamCodeContainerNotFound, message: "No ready pod runs the container",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"web"}},
			want:  "exec refused by the server (HTTP 404): No ready pod runs the container. Use -c web."},
		{name: "a -c container with escapes is sanitized; the invalid hint is dropped", code: StreamCodeContainerNotAllowed, message: "The container is not allowed for exec", container: "a\x1bb",
			hints: &ExecChartHints{Chart: "web", Containers: []string{"a\x1bb", "ok"}},
			want:  `exec refused by the server (HTTP 403): The container is not allowed for exec: "a b". Use -c ok.`},
		{name: "a chart name with escapes is sanitized", code: StreamCodeContainerNotAllowed, message: "The container is not allowed for exec",
			hints: &ExecChartHints{Chart: "k\x1b[2Jsm"},
			want:  "exec refused by the server (HTTP 403): The container is not allowed for exec. The server lists no container of chart k [2Jsm that accepts exec; ask an administrator."},
		{name: "names that are not container names are dropped", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: append([]string{"ok"}, invalidContainerNames...)},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c ok."},
		{name: "a valid name of 63 characters is kept", code: StreamCodeServiceAccountToken, message: "The container holds a service-account token",
			hints: &ExecChartHints{Chart: "web", Containers: []string{name64[:63], name64}},
			want:  "exec refused by the server (HTTP 403): The container holds a service-account token. Use -c " + name64[:63] + "."},
		{name: "only names that are not container names: as an empty list", code: StreamCodeContainerNotAllowed, message: "The container is not allowed for exec", container: "php",
			hints: &ExecChartHints{Chart: "web", Containers: invalidContainerNames},
			want:  `exec refused by the server (HTTP 403): The container is not allowed for exec: "php". The server lists no container of chart web that accepts exec; ask an administrator.`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status := 403
			if tt.code == StreamCodeContainerNotFound {
				status = 404
			}
			e := &WSHandshakeError{StatusCode: status, Message: tt.message, Code: tt.code, Op: execOp,
				Container: tt.container, ExecHints: tt.hints, noServerText: tt.noServerText}
			assert.Equal(t, tt.want, e.Error())
		})
	}
}

func TestWSHandshakeError_APIKeyAdvice(t *testing.T) {
	t.Parallel()
	e := &WSHandshakeError{StatusCode: 403, Message: "API keys cannot open this stream", Code: StreamCodeAPIKeyNotAllowed, Op: "port-forward"}
	assert.Equal(t, "port-forward refused by the server (HTTP 403): API keys cannot open this stream. API keys are not allowed for this command: "+StreamAPIKeyAdvice+".", e.Error())
}

func TestGetStackPods_ExecHintsQuery(t *testing.T) {
	t.Parallel()
	const body = `{"namespace":"ns","exec_hints":true,"charts":[` +
		`{"chart_name":"nginx","release_name":"web","pods":[{"name":"web-0"}],"exec_containers":["web","worker"],"exec_default_container":"web"},` +
		`{"chart_name":"mysql","release_name":"db","pods":[{"name":"db-0"}]}]}`
	tests := []struct {
		name      string
		execHints bool
		wantQuery url.Values
	}{
		{name: "with hints", execHints: true, wantQuery: url.Values{"exec_hints": {"true"}}},
		{name: "without hints", execHints: false, wantQuery: url.Values{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var queries []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/stack-instances/42/pods", r.URL.Path)
				assert.Equal(t, "sk_test", r.Header.Get("X-API-Key"), "the pods route keeps the credentials")
				mu.Lock()
				queries = append(queries, r.URL.RawQuery)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)
			c := New(srv.URL)
			c.APIKey = "sk_test"
			st, err := c.GetStackPods("42", tt.execHints)
			require.NoError(t, err)

			mu.Lock()
			require.Len(t, queries, 1)
			q, err := url.ParseQuery(queries[0])
			mu.Unlock()
			require.NoError(t, err)
			assert.Equal(t, tt.wantQuery, q)

			// The fields decode whatever was asked.
			assert.True(t, st.ExecHints)
			require.Len(t, st.Charts, 2)
			assert.Equal(t, []string{"web", "worker"}, st.Charts[0].ExecContainers)
			assert.Equal(t, "web", st.Charts[0].ExecDefaultContainer)
			assert.Nil(t, st.Charts[1].ExecContainers)
			assert.Empty(t, st.Charts[1].ExecDefaultContainer)
		})
	}
}

func TestGetStackPods_ErrorWithHints(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"Stack instance not found"}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	c.RetryBackoff = []time.Duration{}
	_, err := c.GetStackPods("42", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Stack instance not found")
}

func TestInstanceStatus_ExecFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	t.Run("with hints", func(t *testing.T) {
		t.Parallel()
		in := `{"namespace":"ns","status":"running","charts":[{"release_name":"web","chart_name":"nginx","status":"running","deployments":null,"pods":null,"services":null,` +
			`"exec_containers":["web","worker"],"exec_default_container":"web"}],"exec_hints":true}`
		var st types.InstanceStatus
		require.NoError(t, json.Unmarshal([]byte(in), &st))
		out, err := json.Marshal(st)
		require.NoError(t, err)
		var got, want map[string]any
		require.NoError(t, json.Unmarshal(out, &got))
		require.NoError(t, json.Unmarshal([]byte(in), &want))
		assert.Equal(t, want["exec_hints"], got["exec_hints"])
		gotChart := got["charts"].([]any)[0].(map[string]any)
		assert.Equal(t, []any{"web", "worker"}, gotChart["exec_containers"])
		assert.Equal(t, "web", gotChart["exec_default_container"])

		y, err := yaml.Marshal(st)
		require.NoError(t, err)
		var gy map[string]any
		require.NoError(t, yaml.Unmarshal(y, &gy))
		assert.Equal(t, true, gy["exec_hints"], "yaml keys match json")
		yChart := gy["charts"].([]any)[0].(map[string]any)
		assert.Equal(t, []any{"web", "worker"}, yChart["exec_containers"])
		assert.Equal(t, "web", yChart["exec_default_container"])
	})
	t.Run("without hints the fields are left out", func(t *testing.T) {
		t.Parallel()
		st := types.InstanceStatus{Namespace: "ns", Charts: []types.ChartStatus{{ReleaseName: "web"}}}
		out, err := json.Marshal(st)
		require.NoError(t, err)
		assert.NotContains(t, string(out), "exec_")
		y, err := yaml.Marshal(st)
		require.NoError(t, err)
		assert.NotContains(t, string(y), "exec_")
	})
}

func TestUIConfig_StreamAPIKeysAllowed(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	tests := []struct {
		name string
		body string
		want *bool
	}{
		{name: "true", body: `{"title":"x","stream_api_keys_allowed":true}`, want: &yes},
		{name: "false", body: `{"title":"x","stream_api_keys_allowed":false}`, want: &no},
		{name: "missing (older server)", body: `{"title":"x","max_instance_ttl_days":30}`, want: nil},
		{name: "null", body: `{"title":"x","stream_api_keys_allowed":null}`, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/ui-config", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			cfg, err := New(srv.URL).GetUIConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.StreamAPIKeysAllowed)
			assert.Equal(t, "x", cfg.Title)

			// -o json of server info keeps a false value and leaves out a
			// missing one.
			out, err := json.Marshal(cfg)
			require.NoError(t, err)
			if tt.want == nil {
				assert.NotContains(t, string(out), "stream_api_keys_allowed")
			} else {
				assert.Contains(t, string(out), `"stream_api_keys_allowed":`+map[bool]string{true: "true", false: "false"}[*tt.want])
			}
		})
	}
}

func TestUIConfig_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	cfg, err := New(srv.URL).GetUIConfig()
	require.Error(t, err)
	assert.Nil(t, cfg)
}
