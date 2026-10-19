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
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expiryNow is the fixed clock of the expiry tests.
var expiryNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// useExpiryClock sets nowFunc to expiryNow for the test.
func useExpiryClock(t *testing.T) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return expiryNow }
	t.Cleanup(func() { nowFunc = prev })
}

// expiryServer records the requests of a command. GET /api/v1/ui-config
// returns maxDays (404 when maxDays is 0). Every other request gets
// status (default 200) and resp. Unless ignoreStop is set, the response
// instance gets the expires_at of the request body, as a current server
// does.
type expiryServer struct {
	maxDays    int
	status     int
	resp       string
	ignoreStop bool                      // emulate a server before v0.12.0
	requests   []string                  // "METHOD path"
	bodies     map[string]map[string]any // body by "METHOD path"
}

func (es *expiryServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	es.bodies = map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		es.requests = append(es.requests, key)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/ui-config" {
			if es.maxDays == 0 {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(types.UIConfig{MaxInstanceTTLDays: es.maxDays})
			return
		}
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if len(data) > 0 {
			m := map[string]any{}
			require.NoError(t, json.Unmarshal(data, &m))
			es.bodies[key] = m
		}
		if es.status != 0 {
			w.WriteHeader(es.status)
		}
		resp := es.resp
		if resp == "" {
			resp = `{"id":"42","name":"my-stack"}`
		}
		if stop, ok := es.bodies[key]["expires_at"]; ok && !es.ignoreStop {
			m := map[string]any{}
			require.NoError(t, json.Unmarshal([]byte(resp), &m))
			target := m
			if inst, ok := m["instance"].(map[string]any); ok {
				target = inst
			}
			target["expires_at"] = stop
			data, err := json.Marshal(m)
			require.NoError(t, err)
			resp = string(data)
		}
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// resetCmdFlags sets every flag of cmd back to its default now (other
// tests can leave values behind) and after the test. The names are only a
// check that the flags exist.
func resetCmdFlags(t *testing.T, cmd *cobra.Command, names ...string) {
	t.Helper()
	for _, n := range names {
		require.NotNil(t, cmd.Flags().Lookup(n), n)
	}
	reset := func() {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			resetFlag(t, cmd.Flags(), f.Name, f.DefValue)
		})
	}
	reset()
	t.Cleanup(reset)
}

func TestParseStopAt(t *testing.T) {
	local, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-17 18:00", time.Local)
	require.NoError(t, err)
	tests := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"2026-10-17T18:00:00+02:00", time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC), false},
		{"2026-10-17T16:00:00Z", time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC), false},
		{"2026-10-17 18:00", local.UTC(), false},
		{"2026-10-17T18:00", local.UTC(), false},
		{" 2026-10-17 18:00:00 ", local.UTC(), false},
		{"17/10/2026", time.Time{}, true},
		{"", time.Time{}, true},
	}
	for _, tt := range tests {
		got, err := parseStopAt("stop-at", tt.in)
		if tt.wantErr {
			require.Error(t, err, tt.in)
			assert.Contains(t, err.Error(), "is not a valid time")
			continue
		}
		require.NoError(t, err, tt.in)
		assert.True(t, tt.want.Equal(got), "%s: got %s, want %s", tt.in, got, tt.want)
		assert.Equal(t, time.UTC, got.Location())
	}
}

func TestExpiryText(t *testing.T) {
	at := time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC)
	assert.Equal(t, "stops at 2026-10-17T16:00:00Z", expiryText(0, &at))
	assert.Equal(t, "2026-10-17T16:00:00Z", expiryText(240, &at))
	assert.Equal(t, "-", expiryText(0, nil))
	assert.Equal(t, "- (fixed stop time)", ttlText(0, &at))
	assert.Equal(t, "- (no expiry)", ttlText(0, nil))
	assert.Equal(t, "240 minutes", ttlText(240, &at))
}

// TestExpiryFlags_UsageErrors: the usage errors come before any request.
func TestExpiryFlags_UsageErrors(t *testing.T) {
	useExpiryClock(t)
	tests := []struct {
		name    string
		cmd     *cobra.Command
		args    []string
		flags   map[string]string
		wantErr string
	}{
		{"create ttl and stop-at", stackCreateCmd, nil, map[string]string{"name": "s", "definition": "1", "ttl": "60", "stop-at": "2026-10-16T12:00:00Z"}, "--ttl and --stop-at cannot be used together"},
		{"create stop-at in the past", stackCreateCmd, nil, map[string]string{"name": "s", "definition": "1", "stop-at": "2026-10-15T11:00:00Z"}, "is in the past"},
		{"create negative ttl", stackCreateCmd, nil, map[string]string{"name": "s", "definition": "1", "ttl": "-1"}, "--ttl must be a non-negative integer"},
		{"clone bad stop-at", stackCloneCmd, []string{"42"}, map[string]string{"stop-at": "soon"}, `--stop-at "soon" is not a valid time`},
		{"update nothing", stackUpdateCmd, []string{"42"}, nil, "nothing to update"},
		{"update empty name", stackUpdateCmd, []string{"42"}, map[string]string{"name": " "}, "--name cannot be empty"},
		{"update ttl and stop-at", stackUpdateCmd, []string{"42"}, map[string]string{"ttl": "0", "stop-at": "2026-10-16T12:00:00Z"}, "cannot be used together"},
		{"quick-deploy ttl and stop-at", templateQuickDeployCmd, []string{"10"}, map[string]string{"name": "q", "ttl": "60", "stop-at": "2026-10-16T12:00:00Z"}, "cannot be used together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("API must not be called: %s %s", r.Method, r.URL.Path)
			}))
			t.Cleanup(srv.Close)
			_ = setupStackTestCmd(t, srv.URL)
			resetCmdFlags(t, tt.cmd, "ttl", "stop-at")
			for k, v := range tt.flags {
				require.NoError(t, tt.cmd.Flags().Set(k, v))
			}
			err := tt.cmd.RunE(tt.cmd, tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestStackCreateCmd_StopAt(t *testing.T) {
	useExpiryClock(t)
	es := &expiryServer{maxDays: 7, status: http.StatusCreated}
	srv := es.start(t)
	_ = setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackCreateCmd, "name", "definition", "stop-at")
	require.NoError(t, stackCreateCmd.Flags().Set("name", "s"))
	require.NoError(t, stackCreateCmd.Flags().Set("definition", "1"))
	require.NoError(t, stackCreateCmd.Flags().Set("stop-at", "2026-10-16T20:00:00+02:00"))

	require.NoError(t, stackCreateCmd.RunE(stackCreateCmd, nil))
	body := es.bodies["POST /api/v1/stack-instances"]
	require.NotNil(t, body)
	assert.Equal(t, "2026-10-16T18:00:00Z", body["expires_at"], "the stop time goes in UTC")
	assert.NotContains(t, body, "ttl_minutes")
}

func TestStackCreateCmd_LimitFromServer(t *testing.T) {
	useExpiryClock(t)
	tests := []struct {
		name    string
		maxDays int
		flag    string
		value   string
		wantErr string
	}{
		{"ttl above the server limit", 1, "ttl", "1441", "--ttl 1441 is above the limit of the server: 1 day (1440 minutes)"},
		{"ttl above the 30-day default of an older server", 0, "ttl", "43201", "30 days (43200 minutes)"},
		{"stop-at after the limit", 7, "stop-at", "2026-10-22T12:01:00Z", "--stop-at 2026-10-22T12:01:00Z is too far away: the limit of the server is 7 days from now (2026-10-22T12:00:00Z)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := &expiryServer{maxDays: tt.maxDays}
			srv := es.start(t)
			_ = setupStackTestCmd(t, srv.URL)
			resetCmdFlags(t, stackCreateCmd, "name", "definition", "ttl", "stop-at")
			require.NoError(t, stackCreateCmd.Flags().Set("name", "s"))
			require.NoError(t, stackCreateCmd.Flags().Set("definition", "1"))
			require.NoError(t, stackCreateCmd.Flags().Set(tt.flag, tt.value))

			err := stackCreateCmd.RunE(stackCreateCmd, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, []string{"GET /api/v1/ui-config"}, es.requests, "no create request after a refused value")
		})
	}
}

func TestStackUpdateCmd_SendsOnlyGivenFields(t *testing.T) {
	useExpiryClock(t)
	tests := []struct {
		name  string
		flags map[string]string
		want  map[string]any
	}{
		{"rename", map[string]string{"name": "new-name"}, map[string]any{"name": "new-name"}},
		{"branch", map[string]string{"branch": "feature/x"}, map[string]any{"branch": "feature/x"}},
		{"ttl 0 clears the expiry", map[string]string{"ttl": "0"}, map[string]any{"ttl_minutes": float64(0)}},
		{"stop-at", map[string]string{"stop-at": "2026-10-16T12:00:00Z"}, map[string]any{"expires_at": "2026-10-16T12:00:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := &expiryServer{maxDays: 30}
			srv := es.start(t)
			buf := setupStackTestCmd(t, srv.URL)
			resetCmdFlags(t, stackUpdateCmd)
			for k, v := range tt.flags {
				require.NoError(t, stackUpdateCmd.Flags().Set(k, v))
			}

			require.NoError(t, stackUpdateCmd.RunE(stackUpdateCmd, []string{"42"}))
			assert.Equal(t, tt.want, es.bodies["PUT /api/v1/stack-instances/42"])
			assert.Contains(t, buf.String(), "my-stack")
		})
	}
}

func TestStackUpdateCmd_NameConflict(t *testing.T) {
	es := &expiryServer{
		status: http.StatusConflict,
		resp:   `{"error":"instance name already exists","message":"A stack named \"taken\" already exists on this cluster (owner: bob)","suggestions":["taken-2","taken-3"]}`,
	}
	srv := es.start(t)
	_ = setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, stackUpdateCmd, "name")
	require.NoError(t, stackUpdateCmd.Flags().Set("name", "taken"))

	err := stackUpdateCmd.RunE(stackUpdateCmd, []string{"42"})
	require.Error(t, err)
	assert.Equal(t, `Conflict: A stack named "taken" already exists on this cluster (owner: bob). Free names: taken-2, taken-3`, err.Error())
}

func TestStackCloneCmd_ExpiryBody(t *testing.T) {
	useExpiryClock(t)
	tests := []struct {
		name  string
		flags map[string]string
		want  map[string]any
	}{
		{"no flags: no body", nil, nil},
		{"ttl", map[string]string{"ttl": "240"}, map[string]any{"ttl_minutes": float64(240)}},
		{"stop-at", map[string]string{"stop-at": "2026-10-16T12:00:00Z"}, map[string]any{"expires_at": "2026-10-16T12:00:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := &expiryServer{maxDays: 30, status: http.StatusCreated, resp: `{"id":"55"}`}
			srv := es.start(t)
			buf := setupStackTestCmd(t, srv.URL)
			var stderr bytes.Buffer
			stackCloneCmd.SetErr(&stderr)
			t.Cleanup(func() { stackCloneCmd.SetErr(nil) })
			resetCmdFlags(t, stackCloneCmd, "ttl", "stop-at")
			for k, v := range tt.flags {
				require.NoError(t, stackCloneCmd.Flags().Set(k, v))
			}

			require.NoError(t, stackCloneCmd.RunE(stackCloneCmd, []string{"42"}))
			assert.Equal(t, tt.want, es.bodies["POST /api/v1/stack-instances/42/clone"])
			assert.Contains(t, buf.String(), "new stack 55")
			if tt.flags == nil {
				assert.NotContains(t, es.requests, "GET /api/v1/ui-config", "no limit check without --ttl or --stop-at")
			}
		})
	}
}

func TestTemplateQuickDeployCmd_Expiry(t *testing.T) {
	useExpiryClock(t)
	es := &expiryServer{maxDays: 30, status: http.StatusAccepted, resp: `{"instance":{"id":"77","name":"qd"},"definition":{"id":"9"},"log_id":"l1"}`}
	srv := es.start(t)
	buf := setupStackTestCmd(t, srv.URL)
	resetCmdFlags(t, templateQuickDeployCmd, "name", "stop-at")
	require.NoError(t, templateQuickDeployCmd.Flags().Set("name", "qd"))
	require.NoError(t, templateQuickDeployCmd.Flags().Set("stop-at", "2026-10-16T12:00:00Z"))

	require.NoError(t, templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"}))
	assert.Equal(t, map[string]any{"instance_name": "qd", "expires_at": "2026-10-16T12:00:00Z"}, es.bodies["POST /api/v1/templates/10/quick-deploy"])
	assert.Contains(t, buf.String(), "77", "the instance of the wrapped response is printed")
}

func TestStackExtendCmd_Until(t *testing.T) {
	_ = setupExtend(t)
	useExpiryClock(t)
	current := expiryNow.Add(4 * time.Hour)
	es := &extendServer{current: &current, now: expiryNow, maxDays: 30}
	srv := es.start(t)
	buf := setupStackTestCmd(t, srv.URL)
	require.NoError(t, stackExtendCmd.Flags().Set("until", "2026-10-16T20:00:00+02:00"))

	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Equal(t, "2026-10-16T18:00:00Z", es.until)
	assert.Empty(t, es.body, "--until sends no minutes")
	assert.Equal(t, "Extended stack 42\nOld expiry: 2026-10-15T16:00:00Z\nNew expiry: 2026-10-16T18:00:00Z\n", buf.String())
}

func TestStackExtendCmd_UntilRefused(t *testing.T) {
	useExpiryClock(t)
	current := expiryNow.Add(4 * time.Hour)
	tests := []struct {
		name    string
		until   string
		maxDays int
		wantErr string
	}{
		{"not after the current expiry", "2026-10-15T15:00:00Z", 30, "is not after the current expiry 2026-10-15T16:00:00Z"},
		{"after the limit", "2026-10-17T12:00:01Z", 2, "is too far away: the limit of the server is 2 days from now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = setupExtend(t)
			es := &extendServer{current: &current, now: expiryNow, maxDays: tt.maxDays}
			srv := es.start(t)
			_ = setupStackTestCmd(t, srv.URL)
			require.NoError(t, stackExtendCmd.Flags().Set("until", tt.until))

			err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, 0, es.calls, "no extend request")
		})
	}
}

// TestStackExtendCmd_MinutesCappedByServerLimit: a server with a 7-day
// limit caps the expiry. The command reads the limit and does not report
// an old server.
func TestStackExtendCmd_MinutesCappedByServerLimit(t *testing.T) {
	_ = setupExtend(t)
	current := extendNow.Add(7*24*time.Hour - time.Hour)
	es := &extendServer{current: &current, now: extendNow, maxDays: 7}
	srv := es.start(t)
	buf := setupStackTestCmd(t, srv.URL)
	require.NoError(t, stackExtendCmd.Flags().Set("minutes", "600"))

	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Equal(t, 1, es.uiCalls)
	assert.Contains(t, buf.String(), "New expiry: 2026-10-08T12:00:00Z")
}

func TestStackExtendCmd_MinutesNoUIConfigCall(t *testing.T) {
	_ = setupExtend(t)
	current := extendNow.Add(time.Hour)
	es := &extendServer{current: &current, now: extendNow}
	srv := es.start(t)
	_ = setupStackTestCmd(t, srv.URL)
	require.NoError(t, stackExtendCmd.Flags().Set("minutes", "60"))

	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Equal(t, 0, es.uiCalls, "a normal extend needs no ui-config call")
}

func TestServerInfoCmd(t *testing.T) {
	tests := []struct {
		name     string
		uiConfig string // empty: 404
		want     []string
	}{
		{"current server", `{"title":"K8s Stack Manager","default_instance_ttl_minutes":480,"max_instance_ttl_days":14}`,
			[]string{"Ping", "ok (", "K8s Stack Manager", "480 minutes", "14 days"}},
		{"older server without ui-config", "",
			[]string{"30 days (default; the server does not send a limit)"}},
		{"ui-config fails", "fail",
			[]string{"TTL limit", "unknown", "UI config", "failed: Server error"}},
		{"title is sanitized", `{"title":"Evil\u001b[2Jname","max_instance_ttl_days":30}`,
			[]string{"Evil [2Jname"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("X-API-Key"))
				switch {
				case r.URL.Path == "/api/v1/ping":
					_, _ = io.WriteString(w, `{"message":"pong"}`)
				case r.URL.Path == "/api/v1/ui-config" && tt.uiConfig == "fail":
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"error":"boom"}`)
				case r.URL.Path == "/api/v1/ui-config" && tt.uiConfig != "":
					_, _ = io.WriteString(w, tt.uiConfig)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			buf := setupStackTestCmd(t, srv.URL)
			flagAPIKey = "secret-key"

			require.NoError(t, serverInfoCmd.RunE(serverInfoCmd, nil))
			out := buf.String()
			for _, w := range tt.want {
				assert.Contains(t, out, w)
			}
		})
	}
}

func TestServerInfoCmd_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	buf := setupStackTestCmd(t, url)
	printer.Format = output.FormatJSON

	err := serverInfoCmd.RunE(serverInfoCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not reachable")
	var info serverInfo
	require.NoError(t, json.Unmarshal(buf.Bytes(), &info))
	assert.False(t, info.Reachable)
	assert.NotEmpty(t, info.PingError)
	assert.True(t, strings.HasPrefix(info.URL, "http://127.0.0.1"))
}

// TestStopAt_OldServer: a server before v0.12.0 ignores expires_at. The
// command shows the result, then fails with an upgrade hint.
func TestStopAt_OldServer(t *testing.T) {
	useExpiryClock(t)
	tests := []struct {
		name string
		cmd  *cobra.Command
		args []string
		set  map[string]string
		resp string
		code int
	}{
		{"create", stackCreateCmd, nil, map[string]string{"name": "s", "definition": "1"}, "", http.StatusCreated},
		{"clone", stackCloneCmd, []string{"42"}, nil, `{"id":"55","ttl_minutes":240,"expires_at":"2026-10-15T16:00:00Z"}`, http.StatusCreated},
		{"update", stackUpdateCmd, []string{"42"}, nil, "", http.StatusOK},
		{"quick-deploy", templateQuickDeployCmd, []string{"10"}, map[string]string{"name": "q"}, `{"instance":{"id":"77"}}`, http.StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := &expiryServer{status: tt.code, resp: tt.resp, ignoreStop: true}
			srv := es.start(t)
			buf := setupStackTestCmd(t, srv.URL)
			resetCmdFlags(t, tt.cmd, "stop-at")
			for k, v := range tt.set {
				require.NoError(t, tt.cmd.Flags().Set(k, v))
			}
			require.NoError(t, tt.cmd.Flags().Set("stop-at", "2026-10-16T12:00:00Z"))

			err := tt.cmd.RunE(tt.cmd, tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the server did not apply --stop-at 2026-10-16T12:00:00Z")
			assert.Contains(t, err.Error(), "upgrade k8s-stack-manager to v0.12.0 or later")
			assert.NotEmpty(t, buf.String(), "the result is shown before the error")
		})
	}
}

func TestStackExtendCmd_UntilOldServer(t *testing.T) {
	_ = setupExtend(t)
	useExpiryClock(t)
	current := expiryNow.Add(time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/ui-config":
			http.NotFound(w, r)
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}, TTLMinutes: 240, ExpiresAt: &current})
		default:
			// v0.11.0: "until" is unknown; an empty body adds one TTL.
			exp := current.Add(240 * time.Minute)
			json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}, TTLMinutes: 240, ExpiresAt: &exp})
		}
	}))
	t.Cleanup(srv.Close)
	buf := setupStackTestCmd(t, srv.URL)
	require.NoError(t, stackExtendCmd.Flags().Set("until", "2026-10-17T12:00:00Z"))

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the server did not apply --until 2026-10-17T12:00:00Z (expiry: 2026-10-15T17:00:00Z)")
	assert.Contains(t, buf.String(), "New expiry: 2026-10-15T17:00:00Z")
}
