package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordServer answers "METHOD path" routes and records the requests and
// the JSON bodies. An unknown route fails the test.
type recordServer struct {
	routes   map[string]route
	requests []string
	bodies   map[string]map[string]any
}

func (rs *recordServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	rs.bodies = map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		rs.requests = append(rs.requests, key)
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			m := map[string]any{}
			assert.NoError(t, json.Unmarshal(data, &m))
			rs.bodies[key] = m
		}
		rt, ok := rs.routes[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if strings.HasPrefix(rt.body, "{") {
			w.Header().Set("Content-Type", "application/json")
		}
		if rt.status != 0 {
			w.WriteHeader(rt.status)
		}
		_, _ = io.WriteString(w, rt.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// setupChartCmd prepares cmd for a test: a server, stdout and stderr
// buffers and flags that are reset before and after the test.
func setupChartCmd(t *testing.T, cmd *cobra.Command, rs *recordServer) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := rs.start(t)
	buf := setupStackTestCmd(t, srv.URL)
	stderr := stderrOf(t, cmd)
	resetCmdFlags(t, cmd)
	t.Cleanup(func() { cmd.SetIn(nil) })
	return buf, stderr
}

const defWithCharts = `{"id":"7","name":"example-dev","charts":[{"id":"c1","chart_name":"api"},{"id":"c2","chart_name":"worker"}]}`

// ---------- definition add-chart ----------

func TestDefinitionAddChartCmd(t *testing.T) {
	values := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(values, []byte("replicas: 2\n"), 0o600))
	rs := &recordServer{routes: map[string]route{
		"POST /api/v1/stack-definitions/7/charts": {http.StatusCreated, `{"id":"c3","chart_name":"worker","repository_url":"oci://r.example.com/helm"}`},
	}}
	buf, _ := setupChartCmd(t, definitionAddChartCmd, rs)
	for k, v := range map[string]string{"name": "worker", "repository-url": "oci://r.example.com/helm", "chart-version": "1.2.0", "file": values} {
		require.NoError(t, definitionAddChartCmd.Flags().Set(k, v))
	}

	require.NoError(t, definitionAddChartCmd.RunE(definitionAddChartCmd, []string{"7"}))
	assert.Equal(t, map[string]any{
		"chart_name": "worker", "repository_url": "oci://r.example.com/helm", "chart_version": "1.2.0",
		"deploy_order": float64(0), "default_values": "replicas: 2\n",
	}, rs.bodies["POST /api/v1/stack-definitions/7/charts"])
	assert.Regexp(t, `ID:\s+c3`, buf.String())
}

func TestAddChartCmds_UsageErrors(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *cobra.Command
		flags   map[string]string
		wantErr string
	}{
		{"definition without name", definitionAddChartCmd, map[string]string{"name": " "}, "--name is required"},
		{"negative deploy order", definitionAddChartCmd, map[string]string{"name": "a", "deploy-order": "-1"}, "--deploy-order must be 0 or more"},
		{"bad repository URL", definitionAddChartCmd, map[string]string{"name": "a", "repository-url": "ftp://x/y"}, `scheme "ftp" is not supported`},
		{"path traversal", templateAddChartCmd, map[string]string{"name": "a", "locked-file": "../secret.yaml"}, ".."},
		{"missing file", templateAddChartCmd, map[string]string{"name": "a", "file": "/no/such/file.yaml"}, "file.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &recordServer{}
			_, _ = setupChartCmd(t, tt.cmd, rs)
			for k, v := range tt.flags {
				require.NoError(t, tt.cmd.Flags().Set(k, v))
			}
			err := tt.cmd.RunE(tt.cmd, []string{"7"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, rs.requests, "no request")
		})
	}
}

// ---------- definition remove-chart ----------

func TestDefinitionRemoveChartCmd(t *testing.T) {
	tests := []struct {
		name       string
		yes        bool
		stdin      string
		wantDelete bool
		wantOut    string
	}{
		{"yes flag", true, "", true, "Removed chart c2 from definition 7\n"},
		{"confirmed", false, "y\n", true, "Removed chart c2 from definition 7\n"},
		{"declined", false, "n\n", false, "Aborted.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &recordServer{routes: map[string]route{
				"GET /api/v1/stack-definitions/7":              {body: defWithCharts},
				"DELETE /api/v1/stack-definitions/7/charts/c2": {status: http.StatusNoContent},
			}}
			buf, stderr := setupChartCmd(t, definitionRemoveChartCmd, rs)
			if tt.yes {
				require.NoError(t, definitionRemoveChartCmd.Flags().Set("yes", "true"))
			} else {
				definitionRemoveChartCmd.SetIn(strings.NewReader(tt.stdin))
			}

			require.NoError(t, definitionRemoveChartCmd.RunE(definitionRemoveChartCmd, []string{"7", "worker"}))
			assert.Equal(t, tt.wantDelete, containsString(rs.requests, "DELETE /api/v1/stack-definitions/7/charts/c2"))
			assert.Equal(t, tt.wantOut, buf.String())
			if !tt.yes {
				assert.Contains(t, stderr.String(), "Remove chart worker from definition 7? (y/n): ")
			}
		})
	}
}

// ---------- definition check-upgrade / upgrade ----------

const upgradeAvailable = `{"upgrade_available":true,"current_version":"1.0.0","latest_version":"1.1.0",
 "changes":{"charts_added":["cache"],"charts_removed":["legacy"],"charts_modified":["api"],"charts_unchanged":["web","db"]},
 "chart_diffs":[{"chart_name":"api","change_type":"modified","has_differences":true},
                {"chart_name":"web","change_type":"modified","has_differences":true},
                {"chart_name":"db","change_type":"unchanged","has_differences":false}]}`

// noUpgrade is the only body that the server sends without an upgrade.
const noUpgrade = `{"upgrade_available":false}`

const linkedDef = `{"id":"7","name":"example-dev","source_template_id":"9","source_template_version":"1.1.0"}`

func TestDefinitionCheckUpgradeCmd(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{"GET /api/v1/stack-definitions/7/check-upgrade": {body: upgradeAvailable}}}
		buf, _ := setupChartCmd(t, definitionCheckUpgradeCmd, rs)
		require.NoError(t, definitionCheckUpgradeCmd.RunE(definitionCheckUpgradeCmd, []string{"7"}))
		out := buf.String()
		assert.Contains(t, out, "Upgrade available: from version 1.0.0 to version 1.1.0")
		assert.Regexp(t, `cache\s+added`, out)
		assert.Regexp(t, `api\s+modified`, out)
		assert.Regexp(t, `legacy\s+only in the definition \(kept\)`, out)
		assert.Regexp(t, `web\s+modified`, out, "chart_diffs has differences (for example the chart version)")
		assert.Regexp(t, `db\s+unchanged`, out)
	})
	t.Run("latest", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{
			"GET /api/v1/stack-definitions/7/check-upgrade": {body: noUpgrade},
			"GET /api/v1/stack-definitions/7":               {body: linkedDef},
		}}
		buf, _ := setupChartCmd(t, definitionCheckUpgradeCmd, rs)
		require.NoError(t, definitionCheckUpgradeCmd.RunE(definitionCheckUpgradeCmd, []string{"7"}))
		assert.Equal(t, "No upgrade: the definition has template version 1.1.0, and the template has no newer published version.\n", buf.String())
	})
	t.Run("no template", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{
			"GET /api/v1/stack-definitions/7/check-upgrade": {body: noUpgrade},
			"GET /api/v1/stack-definitions/7":               {body: `{"id":"7","name":"example-dev"}`},
		}}
		buf, _ := setupChartCmd(t, definitionCheckUpgradeCmd, rs)
		require.NoError(t, definitionCheckUpgradeCmd.RunE(definitionCheckUpgradeCmd, []string{"7"}))
		assert.Equal(t, "No upgrade: the definition has no source template.\n", buf.String())
	})
	t.Run("quiet and json", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{"GET /api/v1/stack-definitions/7/check-upgrade": {body: upgradeAvailable}}}
		buf, _ := setupChartCmd(t, definitionCheckUpgradeCmd, rs)
		printer.Quiet = true
		require.NoError(t, definitionCheckUpgradeCmd.RunE(definitionCheckUpgradeCmd, []string{"7"}))
		assert.Equal(t, "yes\n", buf.String())

		buf.Reset()
		printer.Quiet = false
		printer.Format = output.FormatJSON
		require.NoError(t, definitionCheckUpgradeCmd.RunE(definitionCheckUpgradeCmd, []string{"7"}))
		var got types.UpgradeCheck
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		require.Len(t, got.ChartDiffs, 3)
		assert.Equal(t, []string{"cache"}, got.Changes.ChartsAdded)
	})
}

func TestDefinitionUpgradeCmd(t *testing.T) {
	upgraded := `{"id":"7","name":"example-dev","source_template_id":"9","source_template_version":"1.1.0"}`
	tests := []struct {
		name       string
		check      string
		yes        bool
		stdin      string
		wantPost   bool
		wantOut    string
		wantStderr string
	}{
		{"yes", upgradeAvailable, true, "", true, "Upgraded definition 7 to version 1.1.0\n", ""},
		{"confirmed", upgradeAvailable, false, "y\n", true, "Upgraded definition 7 to version 1.1.0\n",
			"Upgrade definition 7 to version 1.1.0? The default values of the template charts replace the values of the definition. (y/n): "},
		{"declined", upgradeAvailable, false, "n\n", false, "Aborted.\n", ""},
		{"nothing to do", noUpgrade, true, "", false, "No upgrade: the definition has template version 1.1.0, and the template has no newer published version.\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &recordServer{routes: map[string]route{
				"GET /api/v1/stack-definitions/7/check-upgrade": {body: tt.check},
				"GET /api/v1/stack-definitions/7":               {body: linkedDef},
				"POST /api/v1/stack-definitions/7/upgrade":      {body: upgraded},
			}}
			buf, stderr := setupChartCmd(t, definitionUpgradeCmd, rs)
			if tt.yes {
				require.NoError(t, definitionUpgradeCmd.Flags().Set("yes", "true"))
			} else {
				definitionUpgradeCmd.SetIn(strings.NewReader(tt.stdin))
			}

			require.NoError(t, definitionUpgradeCmd.RunE(definitionUpgradeCmd, []string{"7"}))
			assert.Equal(t, tt.wantPost, containsString(rs.requests, "POST /api/v1/stack-definitions/7/upgrade"))
			assert.Equal(t, tt.wantOut, buf.String())
			assert.Contains(t, stderr.String(), tt.wantStderr)
			if !tt.yes {
				assert.Contains(t, stderr.String(), "cache", "the changes are shown before the question")
			}
		})
	}
}

// ---------- definition link-template ----------

func TestDefinitionLinkTemplateCmd(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]string
		resp     string
		wantBody map[string]any
		wantOut  string
	}{
		{"link", map[string]string{"template": "9", "version": "1.2.0"}, `{"id":"7","source_template_id":"9","source_template_version":"1.2.0"}`,
			map[string]any{"template_id": "9", "version": "1.2.0"}, "Linked definition 7 to template 9 version 1.2.0\n"},
		{"unlink", map[string]string{"unlink": "true"}, `{"id":"7"}`,
			map[string]any{"template_id": "", "version": ""}, "Removed the template link of definition 7\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &recordServer{routes: map[string]route{"PUT /api/v1/stack-definitions/7/template-link": {body: tt.resp}}}
			buf, _ := setupChartCmd(t, definitionLinkTemplateCmd, rs)
			for k, v := range tt.flags {
				require.NoError(t, definitionLinkTemplateCmd.Flags().Set(k, v))
			}
			require.NoError(t, definitionLinkTemplateCmd.RunE(definitionLinkTemplateCmd, []string{"7"}))
			assert.Equal(t, tt.wantBody, rs.bodies["PUT /api/v1/stack-definitions/7/template-link"])
			assert.Equal(t, tt.wantOut, buf.String())
		})
	}
}

func TestDefinitionLinkTemplateCmd_Errors(t *testing.T) {
	tests := []struct {
		name    string
		flags   map[string]string
		rt      *route
		wantErr string
	}{
		{"nothing given", nil, nil, "--template or --unlink is required"},
		{"unlink with template", map[string]string{"unlink": "true", "template": "9"}, nil, "--unlink cannot be used with --template or --version"},
		{"old server", map[string]string{"template": "9"}, &route{http.StatusNotFound, "404 page not found"}, "upgrade k8s-stack-manager to v0.11.0 or later"},
		{"not published", map[string]string{"template": "9"}, &route{http.StatusConflict, `{"error":"Template has no published version"}`}, "Conflict: Template has no published version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &recordServer{routes: map[string]route{}}
			if tt.rt != nil {
				rs.routes["PUT /api/v1/stack-definitions/7/template-link"] = *tt.rt
			}
			_, _ = setupChartCmd(t, definitionLinkTemplateCmd, rs)
			for k, v := range tt.flags {
				require.NoError(t, definitionLinkTemplateCmd.Flags().Set(k, v))
			}
			err := definitionLinkTemplateCmd.RunE(definitionLinkTemplateCmd, []string{"7"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			if tt.rt == nil {
				assert.Empty(t, rs.requests)
			}
		})
	}
}

// ---------- template add-chart / remove-chart ----------

func TestTemplateAddChartCmd(t *testing.T) {
	locked := filepath.Join(t.TempDir(), "locked.yaml")
	require.NoError(t, os.WriteFile(locked, []byte("image: fixed\n"), 0o600))
	rs := &recordServer{routes: map[string]route{
		"POST /api/v1/templates/9/charts": {http.StatusCreated, `{"id":"t3","chart_name":"worker"}`},
	}}
	buf, stderr := setupChartCmd(t, templateAddChartCmd, rs)
	for k, v := range map[string]string{"name": "worker", "chart-path": "/charts/worker", "deploy-order": "3", "locked-file": locked, "required": "true"} {
		require.NoError(t, templateAddChartCmd.Flags().Set(k, v))
	}

	require.NoError(t, templateAddChartCmd.RunE(templateAddChartCmd, []string{"9"}))
	body := rs.bodies["POST /api/v1/templates/9/charts"]
	assert.Equal(t, "worker", body["chart_name"])
	assert.Equal(t, "/charts/worker", body["chart_path"])
	assert.Equal(t, float64(3), body["deploy_order"])
	assert.Equal(t, "image: fixed\n", body["locked_values"])
	assert.Equal(t, true, body["required"])
	assert.Regexp(t, `ID:\s+t3`, buf.String())
	assert.Equal(t, "Note: the change is in the working copy of the template. Run 'stackctl template publish 9 --version <new-version>' to make it visible to users.\n", stderr.String())
}

func TestTemplateRemoveChartCmd(t *testing.T) {
	rs := &recordServer{routes: map[string]route{
		"GET /api/v1/templates/9":              {body: `{"id":"9","name":"web-stack","charts":[{"id":"t1","chart_name":"api"},{"id":"t2","chart_name":"worker"}]}`},
		"DELETE /api/v1/templates/9/charts/t2": {status: http.StatusNoContent},
	}}
	buf, stderr := setupChartCmd(t, templateRemoveChartCmd, rs)
	require.NoError(t, templateRemoveChartCmd.Flags().Set("yes", "true"))

	require.NoError(t, templateRemoveChartCmd.RunE(templateRemoveChartCmd, []string{"9", "worker"}))
	assert.Contains(t, rs.requests, "DELETE /api/v1/templates/9/charts/t2")
	assert.Equal(t, "Removed chart t2 from template 9\n", buf.String())
	assert.Contains(t, stderr.String(), "template publish 9")
}

func TestTemplateRemoveChartCmd_UnknownChart(t *testing.T) {
	rs := &recordServer{routes: map[string]route{
		"GET /api/v1/templates/9": {body: `{"id":"9","charts":[{"id":"t1","chart_name":"api"}]}`},
	}}
	_, _ = setupChartCmd(t, templateRemoveChartCmd, rs)
	require.NoError(t, templateRemoveChartCmd.Flags().Set("yes", "true"))

	err := templateRemoveChartCmd.RunE(templateRemoveChartCmd, []string{"9", "worker"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "worker")
	assert.NotContains(t, strings.Join(rs.requests, ","), "DELETE")
}

func TestTemplateChartCmd_Forbidden(t *testing.T) {
	rs := &recordServer{routes: map[string]route{
		"POST /api/v1/templates/9/charts": {http.StatusForbidden, `{"error":"Only the template owner or an admin can change this template"}`},
	}}
	_, _ = setupChartCmd(t, templateAddChartCmd, rs)
	require.NoError(t, templateAddChartCmd.Flags().Set("name", "worker"))

	err := templateAddChartCmd.RunE(templateAddChartCmd, []string{"9"})
	require.Error(t, err)
	assert.Equal(t, "Permission denied. (server: Only the template owner or an admin can change this template)", err.Error())
}

func TestChartCmds_ServerRefusals(t *testing.T) {
	t.Run("remove a required chart", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{
			"GET /api/v1/stack-definitions/7":              {body: defWithCharts},
			"DELETE /api/v1/stack-definitions/7/charts/c1": {http.StatusConflict, `{"error":"Cannot delete required chart: api"}`},
		}}
		_, _ = setupChartCmd(t, definitionRemoveChartCmd, rs)
		require.NoError(t, definitionRemoveChartCmd.Flags().Set("yes", "true"))
		err := definitionRemoveChartCmd.RunE(definitionRemoveChartCmd, []string{"7", "api"})
		require.Error(t, err)
		assert.Equal(t, "Conflict: Cannot delete required chart: api", err.Error())
	})
	t.Run("upgrade without access", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{
			"GET /api/v1/stack-definitions/7/check-upgrade": {body: upgradeAvailable},
			"POST /api/v1/stack-definitions/7/upgrade":      {http.StatusForbidden, `{"error":"Only the owner, devops or admin can change this definition"}`},
		}}
		_, _ = setupChartCmd(t, definitionUpgradeCmd, rs)
		require.NoError(t, definitionUpgradeCmd.Flags().Set("yes", "true"))
		err := definitionUpgradeCmd.RunE(definitionUpgradeCmd, []string{"7"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Permission denied.")
	})
}

func TestChartCmds_OutputModes(t *testing.T) {
	t.Run("add-chart quiet prints the chart ID", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{"POST /api/v1/stack-definitions/7/charts": {http.StatusCreated, `{"id":"c3"}`}}}
		buf, _ := setupChartCmd(t, definitionAddChartCmd, rs)
		printer.Quiet = true
		require.NoError(t, definitionAddChartCmd.Flags().Set("name", "worker"))
		require.NoError(t, definitionAddChartCmd.RunE(definitionAddChartCmd, []string{"7"}))
		assert.Equal(t, "c3\n", buf.String())
	})
	t.Run("link-template yaml prints the definition", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{"PUT /api/v1/stack-definitions/7/template-link": {body: linkedDef}}}
		buf, _ := setupChartCmd(t, definitionLinkTemplateCmd, rs)
		printer.Format = output.FormatYAML
		require.NoError(t, definitionLinkTemplateCmd.Flags().Set("template", "9"))
		require.NoError(t, definitionLinkTemplateCmd.RunE(definitionLinkTemplateCmd, []string{"7"}))
		assert.Contains(t, buf.String(), "source_template_version: 1.1.0")
	})
	t.Run("upgrade json prints the definition", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{
			"GET /api/v1/stack-definitions/7/check-upgrade": {body: upgradeAvailable},
			"POST /api/v1/stack-definitions/7/upgrade":      {body: linkedDef},
		}}
		buf, _ := setupChartCmd(t, definitionUpgradeCmd, rs)
		printer.Format = output.FormatJSON
		require.NoError(t, definitionUpgradeCmd.Flags().Set("yes", "true"))
		require.NoError(t, definitionUpgradeCmd.RunE(definitionUpgradeCmd, []string{"7"}))
		var got types.StackDefinition
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		assert.Equal(t, "9", got.SourceTemplateID)
	})
	t.Run("template remove-chart declined", func(t *testing.T) {
		rs := &recordServer{routes: map[string]route{"GET /api/v1/templates/9": {body: `{"id":"9","charts":[{"id":"t2","chart_name":"worker"}]}`}}}
		buf, _ := setupChartCmd(t, templateRemoveChartCmd, rs)
		templateRemoveChartCmd.SetIn(strings.NewReader("n\n"))
		require.NoError(t, templateRemoveChartCmd.RunE(templateRemoveChartCmd, []string{"9", "worker"}))
		assert.Equal(t, "Aborted.\n", buf.String())
		assert.NotContains(t, strings.Join(rs.requests, ","), "DELETE")
	})
}
