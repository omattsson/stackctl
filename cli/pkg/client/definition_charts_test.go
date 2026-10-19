package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefinitionAndTemplateChartRoutes(t *testing.T) {
	t.Parallel()
	type call struct{ method, path, body string }
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got = append(got, call{r.Method, r.URL.Path, string(data)})
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/check-upgrade"):
			_, _ = io.WriteString(w, `{"upgrade_available":true,"latest_version":"2","changes":{"charts_added":["x"]}}`)
		default:
			_, _ = io.WriteString(w, `{"id":"r1","source_template_id":"9","source_template_version":"2"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	order := 0

	_, err := c.AddDefinitionChart("7", &types.UpdateChartConfigRequest{ChartName: "a", DeployOrder: &order})
	require.NoError(t, err)
	require.NoError(t, c.DeleteDefinitionChart("7", "c1"))
	check, err := c.CheckDefinitionUpgrade("7")
	require.NoError(t, err)
	assert.Equal(t, []string{"x"}, check.Changes.ChartsAdded)
	def, err := c.UpgradeDefinition("7")
	require.NoError(t, err)
	assert.Equal(t, "2", def.SourceTemplateVersion)
	_, err = c.SetDefinitionTemplateLink("7", &types.TemplateLinkRequest{})
	require.NoError(t, err)
	_, err = c.AddTemplateChart("9", &types.UpdateTemplateChartRequest{ChartName: "b"})
	require.NoError(t, err)
	require.NoError(t, c.DeleteTemplateChart("9", "t1"))

	want := []call{
		{http.MethodPost, "/api/v1/stack-definitions/7/charts", `{"chart_name":"a","deploy_order":0}`},
		{http.MethodDelete, "/api/v1/stack-definitions/7/charts/c1", ""},
		{http.MethodGet, "/api/v1/stack-definitions/7/check-upgrade", ""},
		{http.MethodPost, "/api/v1/stack-definitions/7/upgrade", ""},
		{http.MethodPut, "/api/v1/stack-definitions/7/template-link", `{"template_id":"","version":""}`},
		{http.MethodPost, "/api/v1/templates/9/charts", ""},
		{http.MethodDelete, "/api/v1/templates/9/charts/t1", ""},
	}
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, w.method, got[i].method, i)
		assert.Equal(t, w.path, got[i].path, i)
		if w.body != "" {
			assert.JSONEq(t, w.body, got[i].body, i)
		}
	}
	assert.Contains(t, got[5].body, `"required":false`, "a template chart sends the full record")
}
