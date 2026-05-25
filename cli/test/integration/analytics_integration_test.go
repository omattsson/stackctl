package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/cmd"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// analyticsRolePolicy returns a mock server that gates /analytics/users
// behind the admin role. The current role is injected via the role param;
// non-admin callers see 403 on users while overview/templates return 200.
func startAnalyticsMockServer(t *testing.T, role string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/analytics/overview":
			_ = json.NewEncoder(w).Encode(types.AnalyticsOverview{
				RunningInstances: 3, TotalDefinitions: 5, TotalDeploys: 42,
				TotalInstances: 10, TotalTemplates: 7, TotalUsers: 4,
			})
		case "/api/v1/analytics/templates":
			_ = json.NewEncoder(w).Encode([]types.TemplateStats{
				{TemplateID: "t1", TemplateName: "nginx", DeployCount: 8, SuccessCount: 7, SuccessRate: 87.5},
			})
		case "/api/v1/analytics/users":
			if role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "admin role required"})
				return
			}
			_ = json.NewEncoder(w).Encode([]types.UserStats{
				{UserID: "u1", Username: "alice", InstanceCount: 3, DeployCount: 12},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
}

func TestAnalyticsWorkflow_OverviewAndTemplatesViaClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	server := startAnalyticsMockServer(t, "devops")
	defer server.Close()

	c := client.New(server.URL)

	overview, err := c.GetAnalyticsOverview()
	require.NoError(t, err)
	assert.Equal(t, 42, overview.TotalDeploys)

	templates, err := c.GetAnalyticsTemplates()
	require.NoError(t, err)
	require.Len(t, templates, 1)
	assert.Equal(t, "nginx", templates[0].TemplateName)

	// Devops cannot read /users.
	_, err = c.GetAnalyticsUsers()
	require.Error(t, err)
	var apiErr *client.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
}

func TestAnalyticsCobra_DevopsOverviewAndTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAnalyticsMockServer(t, "devops")
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	cmd.ResetFlagsForTest()

	// analytics overview
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"analytics", "overview"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Total deploys")

	// analytics templates
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"analytics", "templates"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "nginx")

	// analytics users — devops gets 403 surfaced as a non-nil error
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"analytics", "users"})
	err := cmd.Execute()
	require.Error(t, err, "devops caller must receive a non-zero exit for analytics users")
	assert.Contains(t, strings.ToLower(err.Error()), "admin role required")
}

func TestAnalyticsCobra_AdminUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAnalyticsMockServer(t, "admin")
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"analytics", "users"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "alice")
}
