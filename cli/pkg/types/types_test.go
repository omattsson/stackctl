package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStackInstance_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().Truncate(time.Second)
	clusterID := "cluster-1"
	orig := StackInstance{
		Base:              Base{ID: "abc-123", CreatedAt: now, UpdatedAt: now, Version: "1"},
		Name:              "my-stack",
		StackDefinitionID: "def-1",
		Owner:             "admin",
		Branch:            "main",
		Namespace:         "ns-my-stack",
		Status:            "running",
		ClusterID:         &clusterID,
		TTLMinutes:        60,
		DeployedAt:        &now,
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded StackInstance
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, orig.ID, decoded.ID)
	assert.Equal(t, orig.Name, decoded.Name)
	assert.Equal(t, orig.Status, decoded.Status)
	assert.Equal(t, orig.ClusterID, decoded.ClusterID)
	assert.Equal(t, orig.TTLMinutes, decoded.TTLMinutes)
	assert.WithinDuration(t, *orig.DeployedAt, *decoded.DeployedAt, time.Second)
}

func TestStackInstance_OmitsNilOptionalFields(t *testing.T) {
	t.Parallel()
	inst := StackInstance{
		Base:   Base{ID: "1"},
		Name:   "test",
		Status: "draft",
	}

	data, err := json.Marshal(inst)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	_, hasClusterID := raw["cluster_id"]
	_, hasDeployedAt := raw["last_deployed_at"]
	_, hasExpiresAt := raw["expires_at"]
	assert.False(t, hasClusterID, "nil ClusterID should be omitted")
	assert.False(t, hasDeployedAt, "nil DeployedAt should be omitted")
	assert.False(t, hasExpiresAt, "nil ExpiresAt should be omitted")
}

func TestListResponse_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	orig := ListResponse[StackInstance]{
		Data: []StackInstance{
			{Base: Base{ID: "1"}, Name: "stack-1"},
			{Base: Base{ID: "2"}, Name: "stack-2"},
		},
		Total:      2,
		Page:       1,
		PageSize:   20,
		TotalPages: 1,
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded ListResponse[StackInstance]
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, 2, decoded.Total)
	assert.Len(t, decoded.Data, 2)
	assert.Equal(t, "stack-1", decoded.Data[0].Name)
}

func TestWSMessage_RawPayload(t *testing.T) {
	t.Parallel()
	logPayload := WSDeploymentLog{
		InstanceID: "42",
		LogID:      "log-1",
		Line:       "Installing chart...",
	}
	payloadBytes, err := json.Marshal(logPayload)
	require.NoError(t, err)

	msg := WSMessage{
		Type: "deployment.log",
		Data: json.RawMessage(payloadBytes),
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded WSMessage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "deployment.log", decoded.Type)

	var decodedLog WSDeploymentLog
	require.NoError(t, json.Unmarshal(decoded.Data, &decodedLog))
	assert.Equal(t, "42", decodedLog.InstanceID)
	assert.Equal(t, "Installing chart...", decodedLog.Line)
}

func TestQuotaOverride_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	podLimit := 20
	orig := QuotaOverride{
		StackInstanceID: "42",
		CPURequest:      "100m",
		CPULimit:        "500m",
		MemRequest:      "128Mi",
		MemLimit:        "512Mi",
		StorageLimit:    "10Gi",
		PodLimit:        &podLimit,
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded QuotaOverride
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, orig.StackInstanceID, decoded.StackInstanceID)
	assert.Equal(t, orig.CPURequest, decoded.CPURequest)
	assert.Equal(t, orig.MemLimit, decoded.MemLimit)
	assert.Equal(t, orig.StorageLimit, decoded.StorageLimit)
	require.NotNil(t, decoded.PodLimit)
	assert.Equal(t, 20, *decoded.PodLimit)
	assert.Contains(t, string(data), `"stack_instance_id":"42"`)
}

// TestOverrides_DecodeAPIShape decodes override responses in the shape the
// backend sends (models.ValueOverride / models.ChartBranchOverride /
// models.InstanceQuotaOverride) and checks that no field is lost.
func TestOverrides_DecodeAPIShape(t *testing.T) {
	t.Parallel()
	const valueJSON = `{"id":"0b5c1e7a-2f4d-4c3b-9a8e-7d6f5e4c3b2a","stack_instance_id":"6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d","chart_config_id":"3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b","values":"replicas: 2\n","updated_at":"2026-10-01T12:00:00Z"}`
	var vo ValueOverride
	require.NoError(t, json.Unmarshal([]byte(valueJSON), &vo))
	assert.Equal(t, "0b5c1e7a-2f4d-4c3b-9a8e-7d6f5e4c3b2a", vo.ID)
	assert.Equal(t, "6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d", vo.StackInstanceID)
	assert.Equal(t, "3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b", vo.ChartConfigID)
	assert.Equal(t, "replicas: 2\n", vo.Values)
	assert.False(t, vo.UpdatedAt.IsZero())

	const branchJSON = `{"id":"1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f","stack_instance_id":"6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d","chart_config_id":"3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b","branch":"feature/x","updated_at":"2026-10-01T12:00:00Z"}`
	var bo BranchOverride
	require.NoError(t, json.Unmarshal([]byte(branchJSON), &bo))
	assert.Equal(t, "6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d", bo.StackInstanceID)
	assert.Equal(t, "3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b", bo.ChartConfigID)
	assert.Equal(t, "feature/x", bo.Branch)

	const quotaJSON = `{"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-01T12:00:00Z","id":"2d3e4f5a-6b7c-4d8e-9f0a-1b2c3d4e5f6a","stack_instance_id":"6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d","cpu_request":"100m","cpu_limit":"1","memory_request":"128Mi","memory_limit":"1Gi","storage_limit":"5Gi","pod_limit":10}`
	var qo QuotaOverride
	require.NoError(t, json.Unmarshal([]byte(quotaJSON), &qo))
	assert.Equal(t, "6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d", qo.StackInstanceID)
	assert.Equal(t, "5Gi", qo.StorageLimit)
	require.NotNil(t, qo.PodLimit)
	assert.Equal(t, 10, *qo.PodLimit)
}

func TestStackDefinition_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	orig := StackDefinition{
		Base:          Base{ID: "def-1", Version: "2"},
		Name:          "api-service",
		Description:   "API stack definition",
		DefaultBranch: "main",
		Owner:         "admin",
		Charts: []ChartConfig{
			{
				Base:            Base{ID: "chart-1"},
				Name:            "api",
				RepoURL:         "https://charts.example.com",
				ChartName:       "api-chart",
				ChartPath:       "charts/api",
				ChartVersion:    "1.0.0",
				DeployOrder:     2,
				BuildPipelineID: "pipeline-42",
				LockedValues:    "image:\n  tag: v1",
				Required:        true,
			},
		},
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded StackDefinition
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, orig.Name, decoded.Name)
	assert.Equal(t, orig.DefaultBranch, decoded.DefaultBranch)
	assert.Len(t, decoded.Charts, 1)
	ch := decoded.Charts[0]
	assert.Equal(t, "api", ch.Name)
	assert.Equal(t, "1.0.0", ch.ChartVersion)
	// Round-trip the union fields so we catch silent drops (regression for
	// the 5-field gap that the live template test surfaced).
	assert.Equal(t, "charts/api", ch.ChartPath)
	assert.Equal(t, 2, ch.DeployOrder)
	assert.Equal(t, "pipeline-42", ch.BuildPipelineID)
	assert.Equal(t, "image:\n  tag: v1", ch.LockedValues)
	assert.True(t, ch.Required)
}

func TestStackTemplate_DecodeReleaseFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		body            string
		wantReleaseInfo bool
		wantReleased    string
		wantCharts      int
	}{
		{
			name: "older server without release fields",
			body: `{"id":"1","name":"web","version":"1.0.0","is_published":true,"charts":[{"id":"c1","chart_name":"app"}]}`,
		},
		{
			name:            "no release yet",
			body:            `{"id":"1","name":"web","version":"","is_published":false,"published_version":null,"published_version_id":null,"published_charts":null,"has_unpublished_changes":true}`,
			wantReleaseInfo: true,
		},
		{
			name:            "released",
			body:            `{"id":"1","name":"web","version":"1.1.0","published_version":"1.0.0","published_version_id":"v1","published_charts":[{"id":"c1","chart_name":"app","chart_version":"0.1.0"}],"has_unpublished_changes":true}`,
			wantReleaseInfo: true,
			wantReleased:    "1.0.0",
			wantCharts:      1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var tmpl StackTemplate
			require.NoError(t, json.Unmarshal([]byte(tt.body), &tmpl))
			assert.Equal(t, tt.wantReleaseInfo, tmpl.HasUnpublishedChanges != nil)
			if tt.wantReleased == "" {
				assert.Nil(t, tmpl.PublishedVersion)
			} else {
				require.NotNil(t, tmpl.PublishedVersion)
				assert.Equal(t, tt.wantReleased, *tmpl.PublishedVersion)
			}
			assert.Len(t, tmpl.PublishedCharts, tt.wantCharts)
		})
	}
}

func TestTemplateVersion_CreatedByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "username", body: `{"id":"v1","created_by":"u-1","created_by_username":"alice"}`, want: "alice"},
		{name: "older server", body: `{"id":"v1","created_by":"u-1"}`, want: "u-1"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v TemplateVersion
			require.NoError(t, json.Unmarshal([]byte(tt.body), &v))
			assert.Equal(t, tt.want, v.CreatedByName())
		})
	}
}

func TestTemplateVersionDiff_DecodeOldAndNewSides(t *testing.T) {
	t.Parallel()
	var oldDiff TemplateVersionDiff
	require.NoError(t, json.Unmarshal([]byte(`{"left":{"version":"1.0.0","snapshot":{"template":{"name":"w"},"charts":[]}},"right":{"version":"1.1.0","snapshot":{"template":{"name":"w"},"charts":[]}},"chart_diffs":[]}`), &oldDiff))
	assert.Equal(t, "1.0.0", oldDiff.Left.Version)
	assert.Nil(t, oldDiff.Left.CreatedAt)
	assert.False(t, oldDiff.Right.IsWorkingCopy)

	var newDiff TemplateVersionDiff
	require.NoError(t, json.Unmarshal([]byte(`{"left":{"id":"v1","version":"1.0.0","created_by":"u-1","created_by_username":"alice","created_at":"2026-01-02T03:04:05Z","is_working_copy":false,"snapshot":{"schema_version":1,"template":{"name":"w"},"charts":[{"chart_name":"app","repo_url":"r","chart_version":"0.1.0","id":"c1"}]}},"right":{"id":"working","version":"1.1.0","created_at":"2026-01-03T00:00:00Z","is_working_copy":true,"snapshot":{"schema_version":1,"template":{"name":"w"},"charts":[]}},"chart_diffs":[{"chart_name":"app","change_type":"modified","has_differences":true,"left_chart_version":"0.1.0","right_chart_version":"0.2.0"}]}`), &newDiff))
	assert.Equal(t, "alice", newDiff.Left.CreatedByUsername)
	require.NotNil(t, newDiff.Left.CreatedAt)
	assert.Equal(t, 1, newDiff.Left.Snapshot.SchemaVersion)
	assert.Equal(t, "0.1.0", newDiff.Left.Snapshot.Charts[0].ChartVersion)
	assert.True(t, newDiff.Right.IsWorkingCopy)
	assert.Equal(t, TemplateVersionWorkingCopy, newDiff.Right.ID)
	assert.Equal(t, "0.2.0", newDiff.ChartDiffs[0].RightChartVersion)
}

func TestUpdateTemplateRequest_JSON(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(UpdateTemplateRequest{Name: "web", Version: "1.1.0", DefaultBranch: "main"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"web","version":"1.1.0","default_branch":"main"}`, string(data))
}
