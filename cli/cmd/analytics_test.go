package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

func sampleAnalyticsOverview() types.AnalyticsOverview {
	return types.AnalyticsOverview{
		RunningInstances: 3,
		TotalDefinitions: 5,
		TotalDeploys:     42,
		TotalInstances:   10,
		TotalTemplates:   7,
		TotalUsers:       4,
	}
}

func sampleAnalyticsTemplates() []types.TemplateStats {
	return []types.TemplateStats{
		{
			Category: "web", DefinitionCount: 2, DeployCount: 8, ErrorCount: 1,
			InstanceCount: 5, IsPublished: true, SuccessCount: 7, SuccessRate: 87.5,
			TemplateID: "t1", TemplateName: "nginx",
		},
		{
			Category: "data", DefinitionCount: 1, DeployCount: 0,
			InstanceCount: 0, IsPublished: false, SuccessRate: 0,
			TemplateID: "t2", TemplateName: "kafka",
		},
	}
}

func sampleAnalyticsUsers() []types.UserStats {
	last := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return []types.UserStats{
		{DeployCount: 12, InstanceCount: 3, LastActive: &last, UserID: "u1", Username: "alice"},
		{DeployCount: 0, InstanceCount: 0, UserID: "u2", Username: "bob"},
	}
}

// ---------- analytics overview ----------

func TestAnalyticsOverviewCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/analytics/overview", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsOverview())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, analyticsOverviewCmd.RunE(analyticsOverviewCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "Total templates")
	assert.Contains(t, out, "7")
	assert.Contains(t, out, "Running instances")
	assert.Contains(t, out, "42")
}

// TestAnalyticsOverviewCmd_JSONGolden asserts the JSON output is byte-identical
// to the checked-in golden file. This locks in the alphabetical-key contract
// documented on AnalyticsOverview: any field reorder or formatter change will
// fail this test and force an explicit golden update.
func TestAnalyticsOverviewCmd_JSONGolden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Deliberately encode in a DIFFERENT order than the alphabetical
		// CLI-side order, to prove the CLI is the source of truth for the
		// output ordering rather than passing-through whatever the backend
		// sent.
		_ = json.NewEncoder(w).Encode(map[string]int{
			"total_templates":   7,
			"total_definitions": 5,
			"total_instances":   10,
			"running_instances": 3,
			"total_deploys":     42,
			"total_users":       4,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, analyticsOverviewCmd.RunE(analyticsOverviewCmd, []string{}))

	want, err := os.ReadFile(filepath.Join("testdata", "analytics_overview.golden.json"))
	require.NoError(t, err)
	assert.Equal(t, string(want), buf.String(),
		"JSON output must match testdata/analytics_overview.golden.json byte-for-byte")
}

// TestAnalyticsOverviewCmd_QuietOutput locks in the documented "quiet
// falls through to KV display" call (see analytics.go comment block in the
// overview RunE). A future refactor that wires the overview through a
// quiet-honoring printer path would regress this — that's the regression
// this test guards against.
func TestAnalyticsOverviewCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsOverview())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, analyticsOverviewCmd.RunE(analyticsOverviewCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "Total templates",
		"overview --quiet must still surface KV labels (no useful IDs to print for a scalar resource)")
	assert.Contains(t, out, "42",
		"overview --quiet must still surface the values")
}

func TestAnalyticsOverviewCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsOverview())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	require.NoError(t, analyticsOverviewCmd.RunE(analyticsOverviewCmd, []string{}))

	var got types.AnalyticsOverview
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, sampleAnalyticsOverview(), got)
}

func TestAnalyticsOverviewCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "devops role required"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := analyticsOverviewCmd.RunE(analyticsOverviewCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "devops role required")
}

// ---------- analytics templates ----------

func TestAnalyticsTemplatesCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/analytics/templates", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsTemplates())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, analyticsTemplatesCmd.RunE(analyticsTemplatesCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "nginx")
	assert.Contains(t, out, "kafka")
	assert.Contains(t, out, "87.5%")
	assert.Contains(t, out, "0.0%", "templates with no deploys must render 0.0%% explicitly")
}

func TestAnalyticsTemplatesCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsTemplates())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, analyticsTemplatesCmd.RunE(analyticsTemplatesCmd, []string{}))

	var got []types.TemplateStats
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "nginx", got[0].TemplateName)
	assert.InDelta(t, 87.5, got[0].SuccessRate, 0.001, "success rate must round-trip without float drift")
}

func TestAnalyticsTemplatesCmd_JSONFieldOrderAlphabetical(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.TemplateStats{sampleAnalyticsTemplates()[0]})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, analyticsTemplatesCmd.RunE(analyticsTemplatesCmd, []string{}))

	out := buf.String()
	// The struct fields are declared alphabetically, so encoding/json emits
	// them in that order. Verify by checking that 'category' precedes
	// 'definition_count' which precedes 'template_id' which precedes
	// 'template_name'. A regression that reorders fields breaks this.
	keyOrder := []string{"category", "definition_count", "deploy_count", "error_count",
		"instance_count", "is_published", "success_count", "success_rate",
		"template_id", "template_name"}
	prev := -1
	for _, k := range keyOrder {
		idx := strings.Index(out, `"`+k+`"`)
		require.NotEqual(t, -1, idx, "key %q must appear in JSON output", k)
		assert.Greater(t, idx, prev, "key %q must appear AFTER previous key (alphabetical order)", k)
		prev = idx
	}
}

func TestAnalyticsTemplatesCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsTemplates())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, analyticsTemplatesCmd.RunE(analyticsTemplatesCmd, []string{}))
	assert.Equal(t, "t1\nt2\n", buf.String(), "quiet mode must emit one template ID per line")
}

func TestAnalyticsTemplatesCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.TemplateStats{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, analyticsTemplatesCmd.RunE(analyticsTemplatesCmd, []string{}))
	assert.Contains(t, buf.String(), "No templates found")
}

// ---------- analytics users ----------

func TestAnalyticsUsersCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/analytics/users", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsUsers())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, analyticsUsersCmd.RunE(analyticsUsersCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "alice")
	assert.Contains(t, out, "bob")
	assert.Contains(t, out, "12") // alice's deploy count
}

func TestAnalyticsUsersCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsUsers())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, analyticsUsersCmd.RunE(analyticsUsersCmd, []string{}))

	var got []types.UserStats
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "alice", got[0].Username)
	assert.Nil(t, got[1].LastActive, "users with no activity must serialize last_active as nil/absent")
}

func TestAnalyticsUsersCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleAnalyticsUsers())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, analyticsUsersCmd.RunE(analyticsUsersCmd, []string{}))
	assert.Equal(t, "u1\nu2\n", buf.String(), "quiet mode must emit one user ID per line")
}

// TestAnalyticsUsersCmd_JSONFieldOrderAlphabetical mirrors the templates
// version: locks in the alphabetical key order documented on UserStats so
// a future field reorder is caught by the test rather than by a downstream
// JSON-diff consumer.
func TestAnalyticsUsersCmd_JSONFieldOrderAlphabetical(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.UserStats{sampleAnalyticsUsers()[0]})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, analyticsUsersCmd.RunE(analyticsUsersCmd, []string{}))

	out := buf.String()
	keyOrder := []string{"deploy_count", "instance_count", "last_active", "user_id", "username"}
	prev := -1
	for _, k := range keyOrder {
		idx := strings.Index(out, `"`+k+`"`)
		require.NotEqual(t, -1, idx, "key %q must appear in JSON output", k)
		assert.Greater(t, idx, prev, "key %q must appear AFTER previous key (alphabetical order)", k)
		prev = idx
	}
}

func TestAnalyticsUsersCmd_AdminForbidden(t *testing.T) {
	// Devops can read overview + templates but NOT users; 403 surfaces with
	// the backend message intact.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin role required"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := analyticsUsersCmd.RunE(analyticsUsersCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "admin role required")
}

// ---------- API error matrix (401/404/500) ----------
// Reuses helpers from user_test.go (apiErrorMatrixCases, startAPIErrorServer,
// assertAPIError). Each command keeps its own linear test per
// tests.instructions.md.

func TestAnalyticsOverviewCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			assertAPIError(t, tc, func() error {
				return analyticsOverviewCmd.RunE(analyticsOverviewCmd, []string{})
			})
		})
	}
}

func TestAnalyticsTemplatesCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			assertAPIError(t, tc, func() error {
				return analyticsTemplatesCmd.RunE(analyticsTemplatesCmd, []string{})
			})
		})
	}
}

func TestAnalyticsUsersCmd_APIErrorMatrix(t *testing.T) {
	for _, tc := range apiErrorMatrixCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := startAPIErrorServer(t, tc)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			assertAPIError(t, tc, func() error {
				return analyticsUsersCmd.RunE(analyticsUsersCmd, []string{})
			})
		})
	}
}
