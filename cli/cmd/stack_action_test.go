package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const actionListBody = `{"instance_id":"42","can_invoke":true,"actions":[
 {"name":"refresh-db","label":"Refresh DB","confirm":"Replace the database?","has_job_log":true,"can_invoke":true,
  "parameters":[{"name":"market","label":"Market","type":"enum","options":["se","dk"],"default":"se","required":true},
                {"name":"dry_run","label":"Dry run","type":"bool"},
                {"name":"note","label":"Note","type":"string","required":true}]},
 {"name":"ping","label":"Ping","has_job_log":false,"can_invoke":true,"parameters":[]}]}`

// logStep is one answer of the job log route: a chunk or an error status.
type logStep struct {
	status     int
	retryAfter string
	raw        string // a raw 200 body instead of chunk
	chunk      types.ActionJobLog
}

// actionServer emulates the action routes of one stack (ID 42).
type actionServer struct {
	list     string
	runCode  int    // status code of the subscriber in the envelope
	runJobID string // job_id of the envelope
	runMsg   string
	steps    []logStep // answers of the job log route, in order; the last one repeats
	runs     []map[string]any
	offsets  []string
	logCalls int
}

func (as *actionServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/stack-instances/42/actions":
			_, _ = io.WriteString(w, as.list)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/stack-instances/42/actions/"):
			body := map[string]any{}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			as.runs = append(as.runs, body)
			name := strings.TrimPrefix(r.URL.Path, "/api/v1/stack-instances/42/actions/")
			code := as.runCode
			if code == 0 {
				code = http.StatusAccepted
			}
			res := types.ActionResult{Action: name, InstanceID: "42", StatusCode: code, Result: map[string]any{"ok": code < 400}, JobID: as.runJobID, Message: as.runMsg}
			json.NewEncoder(w).Encode(res)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/log"):
			as.offsets = append(as.offsets, r.URL.Query().Get("offset"))
			i := as.logCalls
			if i >= len(as.steps) {
				i = len(as.steps) - 1
			}
			as.logCalls++
			st := as.steps[i]
			if st.raw != "" {
				_, _ = io.WriteString(w, st.raw)
				return
			}
			if st.status != 0 {
				if st.retryAfter != "" {
					w.Header().Set("Retry-After", st.retryAfter)
				}
				w.WriteHeader(st.status)
				_, _ = io.WriteString(w, `{"error":"upstream"}`)
				return
			}
			json.NewEncoder(w).Encode(st.chunk)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeJobClock makes jobSleep advance nowFunc and records the waits. Each
// read of the clock also advances it by 100 ms, as time passes during the
// requests, so a loop without a sleep still reaches its deadline.
func fakeJobClock(t *testing.T) *[]time.Duration {
	t.Helper()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	var waits []time.Duration
	prevNow, prevSleep := nowFunc, jobSleep
	nowFunc = func() time.Time {
		now = now.Add(100 * time.Millisecond)
		return now
	}
	jobSleep = func(d time.Duration) {
		waits = append(waits, d)
		now = now.Add(d)
	}
	t.Cleanup(func() { nowFunc, jobSleep = prevNow, prevSleep })
	return &waits
}

func setupActionRun(t *testing.T, as *actionServer) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := as.start(t)
	buf := setupStackTestCmd(t, srv.URL)
	stderr := stderrOf(t, stackActionRunCmd)
	resetCmdFlags(t, stackActionRunCmd, "param", "yes", "wait", "timeout")
	t.Cleanup(func() { stackActionRunCmd.SetIn(nil) })
	return buf, stderr
}

func setParams(t *testing.T, params ...string) {
	t.Helper()
	for _, p := range params {
		require.NoError(t, stackActionRunCmd.Flags().Set("param", p))
	}
}

// ---------- stack action list ----------

func TestStackActionListCmd(t *testing.T) {
	as := &actionServer{list: actionListBody}
	srv := as.start(t)

	t.Run("table", func(t *testing.T) {
		buf := setupStackTestCmd(t, srv.URL)
		require.NoError(t, stackActionListCmd.RunE(stackActionListCmd, []string{"42"}))
		out := buf.String()
		assert.Regexp(t, `refresh-db\s+Refresh DB\s+yes\s+yes\s+market:enum\(se\|dk\)\*=se, dry_run:bool, note:string\*`, out)
		assert.Regexp(t, `ping\s+Ping\s+yes\s+no\s+-`, out)
	})
	t.Run("quiet", func(t *testing.T) {
		buf := setupStackTestCmd(t, srv.URL)
		printer.Quiet = true
		require.NoError(t, stackActionListCmd.RunE(stackActionListCmd, []string{"42"}))
		assert.Equal(t, "refresh-db\nping\n", buf.String())
	})
	t.Run("json", func(t *testing.T) {
		buf := setupStackTestCmd(t, srv.URL)
		printer.Format = output.FormatJSON
		require.NoError(t, stackActionListCmd.RunE(stackActionListCmd, []string{"42"}))
		var got types.ActionList
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		require.Len(t, got.Actions, 2)
		assert.Equal(t, []string{"se", "dk"}, got.Actions[0].Parameters[0].Options)
	})
	t.Run("no actions", func(t *testing.T) {
		empty := &actionServer{list: `{"instance_id":"42","actions":[]}`}
		buf := setupStackTestCmd(t, empty.start(t).URL)
		require.NoError(t, stackActionListCmd.RunE(stackActionListCmd, []string{"42"}))
		assert.Equal(t, "This stack has no custom actions.\n", buf.String())
	})
	t.Run("scope incomplete", func(t *testing.T) {
		incomplete := &actionServer{list: strings.Replace(actionListBody, `"instance_id":"42",`, `"instance_id":"42","scope_incomplete":true,`, 1)}
		buf := setupStackTestCmd(t, incomplete.start(t).URL)
		require.NoError(t, stackActionListCmd.RunE(stackActionListCmd, []string{"42"}))
		assert.Regexp(t, `ping\s+Ping`, buf.String())
		assert.True(t, strings.HasSuffix(buf.String(), msgActionsIncomplete+"\n"), buf.String())
	})
	t.Run("no actions, scope incomplete", func(t *testing.T) {
		empty := &actionServer{list: `{"instance_id":"42","actions":[],"scope_incomplete":true}`}
		buf := setupStackTestCmd(t, empty.start(t).URL)
		require.NoError(t, stackActionListCmd.RunE(stackActionListCmd, []string{"42"}))
		assert.Equal(t, "This stack has no custom actions.\n"+msgActionsIncomplete+"\n", buf.String())
	})
}

// ---------- stack action run: parameters and confirmation ----------

func TestStackActionRunCmd_ParametersAreTyped(t *testing.T) {
	as := &actionServer{list: actionListBody}
	buf, stderr := setupActionRun(t, as)
	require.NoError(t, stackActionRunCmd.Flags().Set("yes", "true"))
	setParams(t, "dry_run=true", "note=hello", "extra=1")

	require.NoError(t, stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "refresh-db"}))
	require.Len(t, as.runs, 1)
	assert.Equal(t, map[string]any{"parameters": map[string]any{
		"market": "se", "dry_run": true, "note": "hello", "extra": "1",
	}}, as.runs[0], "default, bool conversion and an undeclared string")
	assert.Contains(t, buf.String(), "Action refresh-db: status 202")
	assert.Empty(t, stderr.String())
}

func TestStackActionRunCmd_UsageErrors(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		params  []string
		wantErr string
	}{
		{"bad bool", "refresh-db", []string{"note=x", "dry_run=maybe"}, `parameter "dry_run" must be true or false, not "maybe"`},
		{"bad enum", "refresh-db", []string{"note=x", "market=no"}, `parameter "market" must be one of se, dk, not "no"`},
		{"required missing", "refresh-db", nil, `parameter "note" is required: use --param note=<value>`},
		{"required empty", "refresh-db", []string{"note= "}, `parameter "note" is required and cannot be empty`},
		{"no equals sign", "refresh-db", []string{"note"}, `--param "note": use key=value`},
		{"duplicate", "refresh-db", []string{"note=a", "note=b"}, "--param note is set more than once"},
		{"unknown action", "nope", nil, `unknown action "nope" (actions of this stack: refresh-db, ping)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as := &actionServer{list: actionListBody}
			_, _ = setupActionRun(t, as)
			require.NoError(t, stackActionRunCmd.Flags().Set("yes", "true"))
			setParams(t, tt.params...)

			err := stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", tt.action})
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
			assert.Empty(t, as.runs, "no run request")
		})
	}
}

func TestStackActionRunCmd_CannotInvoke(t *testing.T) {
	as := &actionServer{list: strings.ReplaceAll(actionListBody, `"can_invoke":true`, `"can_invoke":false`)}
	_, _ = setupActionRun(t, as)

	err := stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "ping"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only the owner, a devops user and an admin can")
	assert.Empty(t, as.runs)
}

func TestStackActionRunCmd_Confirmation(t *testing.T) {
	tests := []struct {
		name    string
		stdin   string
		wantRun bool
		wantErr string
	}{
		{"yes", "y\n", true, ""},
		{"no", "n\n", false, "aborted: not confirmed"},
		{"no answer", "", false, "use --yes to confirm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as := &actionServer{list: actionListBody}
			_, stderr := setupActionRun(t, as)
			setParams(t, "note=x")
			stackActionRunCmd.SetIn(strings.NewReader(tt.stdin))

			err := stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "refresh-db"})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Contains(t, stderr.String(), "Replace the database? (y/n): ")
			assert.Equal(t, tt.wantRun, len(as.runs) == 1)
		})
	}
}

func TestStackActionRunCmd_NoConfirmationText(t *testing.T) {
	as := &actionServer{list: actionListBody, runCode: http.StatusOK}
	buf, _ := setupActionRun(t, as)
	stackActionRunCmd.SetIn(strings.NewReader("")) // a prompt would fail

	require.NoError(t, stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "ping"}))
	assert.Equal(t, map[string]any{}, as.runs[0], "no parameters: an empty body")
	assert.Contains(t, buf.String(), "Action ping: status 200")
}

func TestStackActionRunCmd_SubscriberRefusal(t *testing.T) {
	as := &actionServer{list: actionListBody, runCode: http.StatusConflict, runMsg: "already in flight"}
	buf, _ := setupActionRun(t, as)

	err := stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "ping"})
	require.Error(t, err)
	assert.Equal(t, "action ping failed: status 409: already in flight", err.Error())
	assert.Contains(t, buf.String(), "Message: already in flight", "the result is shown before the error")
}

func TestStackActionRunCmd_JobIDHint(t *testing.T) {
	as := &actionServer{list: actionListBody, runJobID: "job-1"}
	buf, stderr := setupActionRun(t, as)
	require.NoError(t, stackActionRunCmd.Flags().Set("yes", "true"))
	setParams(t, "note=x")

	require.NoError(t, stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "refresh-db"}))
	assert.Contains(t, buf.String(), "Job ID: job-1")
	assert.Equal(t, "Follow the job log: stackctl stack action log 42 refresh-db job-1 --follow\n", stderr.String())
	assert.Zero(t, as.logCalls, "no polling without --wait")
}

// ---------- stack action run --wait ----------

func TestStackActionRunCmd_WaitFollowsTheLog(t *testing.T) {
	waits := fakeJobClock(t)
	as := &actionServer{list: actionListBody, runJobID: "job-1", steps: []logStep{
		{chunk: types.ActionJobLog{Status: "running", Log: "step 1\n", NextOffset: 7}},
		{status: http.StatusServiceUnavailable, retryAfter: "7"},
		{status: http.StatusBadGateway},
		{chunk: types.ActionJobLog{Status: "running", Log: "step\x1b[2J 2\r\n", NextOffset: 20, Truncated: true}},
		{chunk: types.ActionJobLog{Status: "succeeded", Log: "done\n", NextOffset: 25, Done: true}},
	}}
	buf, stderr := setupActionRun(t, as)
	require.NoError(t, stackActionRunCmd.Flags().Set("yes", "true"))
	require.NoError(t, stackActionRunCmd.Flags().Set("wait", "true"))
	setParams(t, "note=x")

	require.NoError(t, stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "refresh-db"}))
	assert.Equal(t, "step 1\nstep [2J 2\ndone\nJob job-1 ended with status succeeded\n", buf.String())
	assert.Equal(t, "Action refresh-db: status 202. Job ID: job-1. Following the job log; the job continues on the server if you stop the command.\n"+
		"The job log is not available (status 503). Next try in 7s.\n"+
		"The job log is not available (status 502). Next try in 14s.\n", stderr.String())
	assert.Equal(t, []string{"0", "7", "7", "7", "20"}, as.offsets)
	assert.Equal(t, []time.Duration{3 * time.Second, 7 * time.Second, 14 * time.Second}, *waits,
		"poll interval, Retry-After, then double the last wait; no wait after a truncated chunk")
}

func TestStackActionRunCmd_WaitFailures(t *testing.T) {
	tests := []struct {
		name    string
		steps   []logStep
		timeout string
		wantErr string
	}{
		{"failed status", []logStep{{chunk: types.ActionJobLog{Status: "failed", Done: true}}}, "60m", "job job-1 ended with status failed"},
		{"not found", []logStep{{status: http.StatusNotFound}}, "60m", "reading the job log: Resource not found"},
		{"forbidden", []logStep{{status: http.StatusForbidden}}, "60m", "Permission denied."},
		{"timeout", []logStep{{chunk: types.ActionJobLog{Status: "running"}}}, "10s", "timed out after 10s; the job continues on the server. Run: stackctl stack action log 42 refresh-db job-1 --follow"},
		{"timeout on errors", []logStep{{status: http.StatusBadGateway}}, "1m", "timed out after 1m0s"},
		{"timeout on truncated chunks", []logStep{{chunk: types.ActionJobLog{Status: "running", Log: "x", Truncated: true}}}, "5s", "timed out after 5s"},
		{"not JSON", []logStep{{raw: "<html>proxy</html>"}}, "60m", "reading the job log: decoding response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = fakeJobClock(t)
			as := &actionServer{list: actionListBody, runJobID: "job-1", steps: tt.steps}
			buf, _ := setupActionRun(t, as)
			require.NoError(t, stackActionRunCmd.Flags().Set("yes", "true"))
			require.NoError(t, stackActionRunCmd.Flags().Set("wait", "true"))
			require.NoError(t, stackActionRunCmd.Flags().Set("timeout", tt.timeout))
			setParams(t, "note=x")

			err := stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "refresh-db"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			if tt.name != "failed status" {
				assert.NotContains(t, buf.String(), "ended with status", "the job did not end")
			}
		})
	}
}

func TestStackActionRunCmd_WaitJSON(t *testing.T) {
	_ = fakeJobClock(t)
	as := &actionServer{list: actionListBody, runJobID: "job-1", steps: []logStep{
		{chunk: types.ActionJobLog{Status: "succeeded", Log: "all done\n", Done: true}},
	}}
	buf, stderr := setupActionRun(t, as)
	printer.Format = output.FormatJSON
	require.NoError(t, stackActionRunCmd.Flags().Set("yes", "true"))
	require.NoError(t, stackActionRunCmd.Flags().Set("wait", "true"))
	setParams(t, "note=x")

	require.NoError(t, stackActionRunCmd.RunE(stackActionRunCmd, []string{"42", "refresh-db"}))
	var got types.ActionResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got), "stdout is only the JSON result")
	assert.Equal(t, "succeeded", got.JobStatus)
	assert.Equal(t, "job-1", got.JobID)
	assert.Contains(t, stderr.String(), "Job ID: job-1")
	assert.True(t, strings.HasSuffix(stderr.String(), "all done\n"), "the log goes to stderr")
}

// ---------- stack action log ----------

func setupActionLog(t *testing.T, as *actionServer) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := as.start(t)
	buf := setupStackTestCmd(t, srv.URL)
	stderr := stderrOf(t, stackActionLogCmd)
	resetCmdFlags(t, stackActionLogCmd, "follow", "offset", "timeout")
	return buf, stderr
}

func TestStackActionLogCmd_OneChunk(t *testing.T) {
	as := &actionServer{steps: []logStep{{chunk: types.ActionJobLog{Status: "running", Log: "line\n", Offset: 5, NextOffset: 10}}}}
	buf, stderr := setupActionLog(t, as)
	require.NoError(t, stackActionLogCmd.Flags().Set("offset", "5"))

	require.NoError(t, stackActionLogCmd.RunE(stackActionLogCmd, []string{"42", "refresh-db", "job-1"}))
	assert.Equal(t, "line\n", buf.String())
	assert.Equal(t, "Status: running (running, next offset 10)\n", stderr.String())
	assert.Equal(t, []string{"5"}, as.offsets)
}

func TestStackActionLogCmd_Follow(t *testing.T) {
	_ = fakeJobClock(t)
	as := &actionServer{steps: []logStep{
		{chunk: types.ActionJobLog{Status: "running", Log: "a\n", NextOffset: 2}},
		{chunk: types.ActionJobLog{Status: "recovery_required", Log: "b\n", NextOffset: 4, Done: true}},
	}}
	buf, _ := setupActionLog(t, as)
	require.NoError(t, stackActionLogCmd.Flags().Set("follow", "true"))

	err := stackActionLogCmd.RunE(stackActionLogCmd, []string{"42", "refresh-db", "job-1"})
	require.Error(t, err)
	assert.Equal(t, "job job-1 ended with status recovery_required", err.Error())
	assert.Equal(t, "a\nb\n", buf.String())
}

func TestStackActionLogCmd_FollowWithJSONIsAUsageError(t *testing.T) {
	as := &actionServer{}
	_, _ = setupActionLog(t, as)
	printer.Format = output.FormatJSON
	require.NoError(t, stackActionLogCmd.Flags().Set("follow", "true"))

	err := stackActionLogCmd.RunE(stackActionLogCmd, []string{"42", "refresh-db", "job-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be used with -o json")
	assert.Zero(t, as.logCalls)
}

// ---------- helpers ----------

func TestNextBackoff(t *testing.T) {
	plain := errors.New("connection refused")
	assert.Equal(t, 3*time.Second, nextBackoff(0, plain))
	assert.Equal(t, 6*time.Second, nextBackoff(3*time.Second, plain))
	assert.Equal(t, 30*time.Second, nextBackoff(24*time.Second, plain))
	assert.Equal(t, 30*time.Second, nextBackoff(30*time.Second, plain))
}

func TestTransientJobLogError(t *testing.T) {
	assert.True(t, transientJobLogError(fmt.Errorf("making request: %w", &url.Error{Op: "Get", URL: "x", Err: errors.New("refused")})))
	assert.True(t, transientJobLogError(&client.APIError{StatusCode: http.StatusGatewayTimeout}))
	assert.False(t, transientJobLogError(&client.APIError{StatusCode: http.StatusNotFound}))
	assert.False(t, transientJobLogError(errors.New("decoding response: invalid character")))
}

func TestParameterSummary(t *testing.T) {
	assert.Equal(t, "-", parameterSummary(nil))
	assert.Equal(t, "a:string*, b:enum(x|y)=x, c:bool=false", parameterSummary([]types.ActionParameter{
		{Name: "a", Type: "string", Required: true},
		{Name: "b", Type: "enum", Options: []string{"x", "y"}, Default: "x"},
		{Name: "c", Type: "bool", Default: false},
	}))
}

func TestFindAction_ScopeMessages(t *testing.T) {
	t.Parallel()
	ping := types.ActionInfo{Name: "ping"}
	tests := []struct {
		name    string
		list    types.ActionList
		wantErr string
	}{
		{"no actions", types.ActionList{}, `unknown action "nope": this stack has no custom actions`},
		{"no actions, scope incomplete", types.ActionList{ScopeIncomplete: true},
			`unknown action "nope": this stack has no custom actions; some actions could not be checked for this stack, try again`},
		{"other actions, scope incomplete", types.ActionList{Actions: []types.ActionInfo{ping}, ScopeIncomplete: true},
			`unknown action "nope" (actions of this stack: ping); some actions could not be checked for this stack, try again`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := findAction(&tt.list, "nope")
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}
