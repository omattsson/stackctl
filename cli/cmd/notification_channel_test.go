package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	teamsURL     = "https://prod-1.example.logic.azure.com:443/workflows/abc/triggers/manual/paths/invoke?api-version=1&sig=SECRETSIG"
	channelJSON  = `{"id":"11111111-2222-3333-4444-555555555555","name":"team-alerts","webhook_url":"` + teamsURL + `","format":"teams","enabled":true,"filters":{"instance_name_patterns":["pr-*"]}}`
	channelsJSON = `[{"id":"11111111-2222-3333-4444-555555555555","name":"team-alerts","webhook_url":"` + teamsURL + `","format":"teams","enabled":true,"subscription_count":2,"filters":{}}]`
	channelID    = "11111111-2222-3333-4444-555555555555"
	channelPath  = "/api/v1/admin/notification-channels"
)

func TestMaskWebhookURL(t *testing.T) {
	tests := map[string]string{
		teamsURL:                             "https://prod-1.example.logic.azure.com:443/...",
		"https://hooks.example.com/T1/B2/x":  "https://hooks.example.com/...",
		"https://hooks.example.com":          "https://hooks.example.com",
		"https://user:pw@hooks.example.com/": "https://hooks.example.com",
		"not a url":                          "<hidden>",
		"https://[2001:db8::1]:8443/x":       "https://[2001:db8::1]:8443/...",
		"https://h.example.com#sig=x":        "https://h.example.com/...",
		"":                                   "",
	}
	for in, want := range tests {
		assert.Equal(t, want, maskWebhookURL(in), in)
	}
}

func TestNotificationChannelListCmd(t *testing.T) {
	tests := []struct {
		name    string
		format  output.Format
		showURL bool
		want    string
		notWant string
	}{
		{"table masks the URL", output.FormatTable, false, "https://prod-1.example.logic.azure.com:443/...", "SECRETSIG"},
		{"json masks the URL", output.FormatJSON, false, `"webhook_url": "https://prod-1.example.logic.azure.com:443/..."`, "SECRETSIG"},
		{"show-url", output.FormatTable, true, "SECRETSIG", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &recordServer{routes: map[string]route{"GET " + channelPath: {body: channelsJSON}}}
			buf, _ := setupChartCmd(t, notificationChannelListCmd, rs)
			printer.Format = tt.format
			if tt.showURL {
				require.NoError(t, notificationChannelListCmd.Flags().Set("show-url", "true"))
			}
			require.NoError(t, notificationChannelListCmd.RunE(notificationChannelListCmd, nil))
			assert.Contains(t, buf.String(), tt.want)
			if tt.notWant != "" {
				assert.NotContains(t, buf.String(), tt.notWant)
			}
			if tt.format == output.FormatTable && !tt.showURL {
				assert.Regexp(t, `team-alerts\s+teams\s+yes\s+2\s+all instances`, buf.String())
			}
		})
	}
}

func TestNotificationChannelGetCmd_JSONMasksTheURL(t *testing.T) {
	_, out := setupChannelCmd(t, notificationChannelGetCmd, map[string]route{"GET " + channelPath + "/" + channelID: {body: channelJSON}})
	printer.Format = output.FormatJSON
	require.NoError(t, notificationChannelGetCmd.RunE(notificationChannelGetCmd, []string{channelID}))
	stdout, _ := out()
	assert.Contains(t, stdout, `"webhook_url": "https://prod-1.example.logic.azure.com:443/..."`)
	assert.NotContains(t, stdout, "SECRETSIG")
}

func TestNotificationChannelGetCmd_ByName(t *testing.T) {
	rs := &recordServer{routes: map[string]route{
		"GET " + channelPath:                                      {body: channelsJSON},
		"GET " + channelPath + "/" + channelID:                    {body: channelJSON},
		"GET " + channelPath + "/" + channelID + "/subscriptions": {body: `{"event_types":["deployment.failed","deployment.success"]}`},
	}}
	buf, _ := setupChartCmd(t, notificationChannelGetCmd, rs)

	require.NoError(t, notificationChannelGetCmd.RunE(notificationChannelGetCmd, []string{"team-alerts"}))
	out := buf.String()
	assert.Regexp(t, `Events:\s+deployment.failed, deployment.success`, out)
	assert.Regexp(t, `Filters:\s+name=pr-\*`, out)
	assert.NotContains(t, out, "SECRETSIG")
}

// setupChannelCmd prepares a channel command; StringArray flags are reset
// by resetCmdFlags.
func setupChannelCmd(t *testing.T, cmd *cobra.Command, routes map[string]route) (*recordServer, func() (string, string)) {
	t.Helper()
	rs := &recordServer{routes: routes}
	buf, stderr := setupChartCmd(t, cmd, rs)
	return rs, func() (string, string) { return buf.String(), stderr.String() }
}

func setFlags(t *testing.T, cmd *cobra.Command, kv ...string) {
	t.Helper()
	require.Zero(t, len(kv)%2)
	for i := 0; i < len(kv); i += 2 {
		require.NoError(t, cmd.Flags().Set(kv[i], kv[i+1]))
	}
}

func TestNotificationChannelCreateCmd(t *testing.T) {
	t.Setenv("CHANNEL_SECRET", "s3cr3t-value")
	created := strings.Replace(channelJSON, `"enabled":true`, `"enabled":true,"warnings":["cluster_ids: cluster c9 does not exist"]`, 1)
	rs, out := setupChannelCmd(t, notificationChannelCreateCmd, map[string]route{
		"GET " + channelPath + "/event-types":                     {body: `{"event_types":["deployment.failed","deployment.success"]}`},
		"POST " + channelPath:                                     {http.StatusCreated, created},
		"PUT " + channelPath + "/" + channelID + "/subscriptions": {body: `{"event_types":["deployment.failed"]}`},
	})
	setFlags(t, notificationChannelCreateCmd,
		"name", "team-alerts", "url", teamsURL, "format", "teams", "secret-env", "CHANNEL_SECRET",
		"filter-name", "pr-*", "filter-definition", "7", "filter-cluster", "c9", "event", "deployment.failed")

	require.NoError(t, notificationChannelCreateCmd.RunE(notificationChannelCreateCmd, nil))
	assert.Equal(t, map[string]any{
		"name": "team-alerts", "webhook_url": teamsURL, "secret": "s3cr3t-value", "enabled": true, "format": "teams",
		"filters": map[string]any{"instance_name_patterns": []any{"pr-*"}, "definition_ids": []any{"7"}, "cluster_ids": []any{"c9"}},
	}, rs.bodies["POST "+channelPath])
	assert.Equal(t, map[string]any{"event_types": []any{"deployment.failed"}}, rs.bodies["PUT "+channelPath+"/"+channelID+"/subscriptions"])
	stdout, stderr := out()
	assert.Regexp(t, `Events:\s+deployment.failed`, stdout)
	assert.Equal(t, "Warning: cluster_ids: cluster c9 does not exist\n", stderr)
	assert.NotContains(t, stdout+stderr, "s3cr3t-value", "the secret is never printed")
	assert.NotContains(t, stdout+stderr, "SECRETSIG")
}

func TestNotificationChannelCreateCmd_SecretFromStdin(t *testing.T) {
	rs, _ := setupChannelCmd(t, notificationChannelCreateCmd, map[string]route{"POST " + channelPath: {http.StatusCreated, channelJSON}})
	setFlags(t, notificationChannelCreateCmd, "name", "n", "url", "https://h.example.com/x", "secret-stdin", "true", "disabled", "true")
	notificationChannelCreateCmd.SetIn(strings.NewReader("from-stdin\n"))

	require.NoError(t, notificationChannelCreateCmd.RunE(notificationChannelCreateCmd, nil))
	body := rs.bodies["POST "+channelPath]
	assert.Equal(t, "from-stdin", body["secret"])
	assert.Equal(t, false, body["enabled"])
}

func TestNotificationChannelCreateCmd_UsageErrors(t *testing.T) {
	t.Setenv("EMPTY_SECRET", "")
	tests := []struct {
		name    string
		flags   []string
		wantErr string
	}{
		{"no url", []string{"name", "n"}, "--name and --url (or --url-env) are required"},
		{"http url", []string{"name", "n", "url", "http://h.example.com"}, "the webhook URL must start with https://"},
		{"url does not parse", []string{"name", "n", "url", "https://h/%zz?sig=TOKEN"}, "the webhook URL is not a valid URL"},
		{"url and url-env", []string{"name", "n", "url", "https://h.example.com", "url-env", "X"}, "--url and --url-env cannot be used together"},
		{"bad format", []string{"name", "n", "url", "https://h.example.com", "format", "slack"}, `--format must be generic or teams, not "slack"`},
		{"bad glob", []string{"name", "n", "url", "https://h.example.com", "filter-name", "pr-["}, `--filter-name "pr-[" is not a valid glob`},
		{"empty secret env", []string{"name", "n", "url", "https://h.example.com", "secret-env", "EMPTY_SECRET"}, "EMPTY_SECRET is not set or empty"},
		{"two secret sources", []string{"name", "n", "url", "https://h.example.com", "secret-env", "X", "secret-stdin", "true"}, "cannot be used together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, _ := setupChannelCmd(t, notificationChannelCreateCmd, map[string]route{})
			setFlags(t, notificationChannelCreateCmd, tt.flags...)
			err := notificationChannelCreateCmd.RunE(notificationChannelCreateCmd, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NotContains(t, err.Error(), "TOKEN", "an error never shows the URL")
			assert.Empty(t, rs.requests)
		})
	}
}

func TestNotificationChannelCreateCmd_URLFromEnv(t *testing.T) {
	t.Setenv("HOOK_URL", teamsURL)
	rs, out := setupChannelCmd(t, notificationChannelCreateCmd, map[string]route{"POST " + channelPath: {http.StatusCreated, channelJSON}})
	setFlags(t, notificationChannelCreateCmd, "name", "n", "url-env", "HOOK_URL")
	printer.Format = output.FormatYAML

	require.NoError(t, notificationChannelCreateCmd.RunE(notificationChannelCreateCmd, nil))
	assert.Equal(t, teamsURL, rs.bodies["POST "+channelPath]["webhook_url"])
	stdout, _ := out()
	assert.Contains(t, stdout, "webhook_url: https://prod-1.example.logic.azure.com:443/...")
	assert.NotContains(t, stdout, "SECRETSIG", "-o yaml masks the URL")
}

func TestNotificationChannelCreateCmd_UnknownEventBeforeCreate(t *testing.T) {
	rs, _ := setupChannelCmd(t, notificationChannelCreateCmd, map[string]route{
		"GET " + channelPath + "/event-types": {body: `{"event_types":["deployment.failed"]}`},
	})
	setFlags(t, notificationChannelCreateCmd, "name", "n", "url", "https://h.example.com/x", "event", "deploy.failed")

	err := notificationChannelCreateCmd.RunE(notificationChannelCreateCmd, nil)
	require.Error(t, err)
	assert.Equal(t, `unknown event type "deploy.failed" (see 'stackctl notification channel events')`, err.Error())
	assert.Equal(t, []string{"GET " + channelPath + "/event-types"}, rs.requests, "no channel is created")
}

func TestNotificationChannelCreateCmd_SubscriptionFails(t *testing.T) {
	_, out := setupChannelCmd(t, notificationChannelCreateCmd, map[string]route{
		"GET " + channelPath + "/event-types":                     {status: http.StatusInternalServerError, body: `{"error":"x"}`},
		"POST " + channelPath:                                     {http.StatusCreated, channelJSON},
		"PUT " + channelPath + "/" + channelID + "/subscriptions": {http.StatusBadRequest, `{"error":"unknown event type: nope"}`},
	})
	printer.Quiet = true
	setFlags(t, notificationChannelCreateCmd, "name", "n", "url", "https://h.example.com/x", "event", "nope")

	err := notificationChannelCreateCmd.RunE(notificationChannelCreateCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the channel "+channelID+" is created, but the subscription failed")
	assert.Contains(t, err.Error(), "Run: stackctl notification channel subscribe "+channelID+" --event <type>")
	stdout, _ := out()
	assert.Equal(t, channelID+"\n", stdout, "-q prints the ID of the created channel")
}

func TestNotificationChannelUpdateCmd(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  map[string]any
	}{
		{"disable", []string{"disable", "true"}, map[string]any{"enabled": false}},
		{"enable", []string{"enable", "true"}, map[string]any{"enabled": true}},
		{"clear secret", []string{"clear-secret", "true"}, map[string]any{"secret": ""}},
		{"clear filters", []string{"clear-filters", "true"}, map[string]any{"filters": map[string]any{}}},
		{"replace filters", []string{"filter-owner", "u1"}, map[string]any{"filters": map[string]any{"owner_ids": []any{"u1"}}}},
		{"rename and format", []string{"name", "new", "format", "generic"}, map[string]any{"name": "new", "format": "generic"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, _ := setupChannelCmd(t, notificationChannelUpdateCmd, map[string]route{"PUT " + channelPath + "/" + channelID: {body: channelJSON}})
			setFlags(t, notificationChannelUpdateCmd, tt.flags...)
			require.NoError(t, notificationChannelUpdateCmd.RunE(notificationChannelUpdateCmd, []string{channelID}))
			assert.Equal(t, tt.want, rs.bodies["PUT "+channelPath+"/"+channelID])
		})
	}
}

func TestNotificationChannelUpdateCmd_UsageErrors(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		wantErr string
	}{
		{"nothing", nil, "nothing to update"},
		{"enable and disable", []string{"enable", "true", "disable", "true"}, "--enable and --disable cannot be used together"},
		{"clear filters and a filter", []string{"clear-filters", "true", "filter-owner", "u1"}, "--clear-filters cannot be used with a --filter-* flag"},
		{"empty format", []string{"format", ""}, `--format must be generic or teams, not ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, _ := setupChannelCmd(t, notificationChannelUpdateCmd, map[string]route{})
			setFlags(t, notificationChannelUpdateCmd, tt.flags...)
			err := notificationChannelUpdateCmd.RunE(notificationChannelUpdateCmd, []string{channelID})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, rs.requests)
		})
	}
}

func TestNotificationChannelDeleteCmd(t *testing.T) {
	rs, out := setupChannelCmd(t, notificationChannelDeleteCmd, map[string]route{"DELETE " + channelPath + "/" + channelID: {status: http.StatusNoContent}})
	notificationChannelDeleteCmd.SetIn(strings.NewReader("y\n"))

	require.NoError(t, notificationChannelDeleteCmd.RunE(notificationChannelDeleteCmd, []string{channelID}))
	stdout, stderr := out()
	assert.Contains(t, rs.requests, "DELETE "+channelPath+"/"+channelID)
	assert.Equal(t, "Deleted notification channel "+channelID+"\n", stdout)
	assert.Contains(t, stderr, "Delete notification channel "+channelID+"? (y/n): ")
}

func TestNotificationChannelSubscribeCmd(t *testing.T) {
	t.Run("events", func(t *testing.T) {
		rs, out := setupChannelCmd(t, notificationChannelSubscribeCmd, map[string]route{
			"PUT " + channelPath + "/" + channelID + "/subscriptions": {body: `{"event_types":["a","b"]}`},
		})
		setFlags(t, notificationChannelSubscribeCmd, "event", "a", "event", "b")
		require.NoError(t, notificationChannelSubscribeCmd.RunE(notificationChannelSubscribeCmd, []string{channelID}))
		assert.Equal(t, map[string]any{"event_types": []any{"a", "b"}}, rs.bodies["PUT "+channelPath+"/"+channelID+"/subscriptions"])
		stdout, _ := out()
		assert.Equal(t, "a\nb\n", stdout)
	})
	t.Run("none sends an empty list", func(t *testing.T) {
		rs, _ := setupChannelCmd(t, notificationChannelSubscribeCmd, map[string]route{
			"PUT " + channelPath + "/" + channelID + "/subscriptions": {body: `{"event_types":[]}`},
		})
		setFlags(t, notificationChannelSubscribeCmd, "none", "true")
		require.NoError(t, notificationChannelSubscribeCmd.RunE(notificationChannelSubscribeCmd, []string{channelID}))
		assert.Equal(t, map[string]any{"event_types": []any{}}, rs.bodies["PUT "+channelPath+"/"+channelID+"/subscriptions"])
	})
	t.Run("unknown event", func(t *testing.T) {
		_, _ = setupChannelCmd(t, notificationChannelSubscribeCmd, map[string]route{
			"PUT " + channelPath + "/" + channelID + "/subscriptions": {http.StatusBadRequest, `{"error":"unknown event type: nope"}`},
		})
		setFlags(t, notificationChannelSubscribeCmd, "event", "nope")
		err := notificationChannelSubscribeCmd.RunE(notificationChannelSubscribeCmd, []string{channelID})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown event type: nope")
	})
	t.Run("no flag", func(t *testing.T) {
		rs, _ := setupChannelCmd(t, notificationChannelSubscribeCmd, map[string]route{})
		err := notificationChannelSubscribeCmd.RunE(notificationChannelSubscribeCmd, []string{channelID})
		require.Error(t, err)
		assert.Equal(t, "--event or --none is required", err.Error())
		assert.Empty(t, rs.requests)
	})
}

func TestNotificationChannelEventsCmd_JSON(t *testing.T) {
	_, out := setupChannelCmd(t, notificationChannelEventsCmd, map[string]route{"GET " + channelPath + "/event-types": {body: `{"event_types":["x","y"]}`}})
	printer.Format = output.FormatJSON
	require.NoError(t, notificationChannelEventsCmd.RunE(notificationChannelEventsCmd, nil))
	stdout, _ := out()
	var got map[string][]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, []string{"x", "y"}, got["event_types"])
}

func TestNotificationChannelTestCmd(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErr    string
		wantOut    string
		wantStderr string
	}{
		{"success", `{"success":true,"message":"Delivered","status_code":202,"channel_enabled":true}`, "", "Test succeeded: status 202. Delivered\n", ""},
		{"failure on a disabled channel", `{"success":false,"message":"Webhook returned 404","status_code":404,"channel_enabled":false}`,
			"the test event to channel " + channelID + " failed (status 404): Webhook returned 404", "", "Note: the channel is disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, out := setupChannelCmd(t, notificationChannelTestCmd, map[string]route{"POST " + channelPath + "/" + channelID + "/test": {body: tt.body}})
			err := notificationChannelTestCmd.RunE(notificationChannelTestCmd, []string{channelID})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
			stdout, stderr := out()
			assert.Equal(t, tt.wantOut, stdout)
			assert.Contains(t, stderr, tt.wantStderr)
		})
	}
}

func TestNotificationChannelLogsCmd(t *testing.T) {
	body := `{"logs":[{"id":"l1","created_at":"2026-10-19T08:00:00Z","event_type":"deployment.failed","status":"failed","status_code":500,"error_message":"boom\u001b[2J"},
	                  {"id":"l2","created_at":"2026-10-19T07:00:00Z","event_type":"test","status":"success","status_code":0}],"total":30,"limit":2,"offset":0}`
	_, out := setupChannelCmd(t, notificationChannelLogsCmd, map[string]route{"GET " + channelPath + "/" + channelID + "/delivery-logs": {body: body}})
	setFlags(t, notificationChannelLogsCmd, "limit", "2")

	require.NoError(t, notificationChannelLogsCmd.RunE(notificationChannelLogsCmd, []string{channelID}))
	stdout, stderr := out()
	assert.Regexp(t, `2026-10-19T08:00:00Z\s+deployment.failed\s+failed\s+500\s+boom \[2J`, stdout)
	assert.Regexp(t, `test\s+success\s+-\s+-`, stdout)
	assert.NotContains(t, stdout, "\x1b[2J")
	assert.Equal(t, "Showing 1 to 2 of 30. Use --offset 2 to see more.\n", stderr)
}

func TestNotificationChannelLogsCmd_BadLimit(t *testing.T) {
	rs, _ := setupChannelCmd(t, notificationChannelLogsCmd, map[string]route{})
	setFlags(t, notificationChannelLogsCmd, "limit", "500")
	err := notificationChannelLogsCmd.RunE(notificationChannelLogsCmd, []string{channelID})
	require.Error(t, err)
	assert.Equal(t, "--limit must be 1 to 100", err.Error())
	assert.Empty(t, rs.requests)
}
