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
	orig := QuotaOverride{
		InstanceID: "42",
		CPURequest: "100m",
		CPULimit:   "500m",
		MemRequest: "128Mi",
		MemLimit:   "512Mi",
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded QuotaOverride
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, orig.InstanceID, decoded.InstanceID)
	assert.Equal(t, orig.CPURequest, decoded.CPURequest)
	assert.Equal(t, orig.MemLimit, decoded.MemLimit)
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
				Base:         Base{ID: "chart-1"},
				Name:         "api",
				RepoURL:      "https://charts.example.com",
				ChartName:    "api-chart",
				ChartVersion: "1.0.0",
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
	assert.Equal(t, "api", decoded.Charts[0].Name)
	assert.Equal(t, "1.0.0", decoded.Charts[0].ChartVersion)
}
