package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNewPrinter_Formats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  Format
	}{
		{"table", FormatTable},
		{"json", FormatJSON},
		{"yaml", FormatYAML},
		{"JSON", FormatJSON},
		{"YAML", FormatYAML},
		{"", FormatTable},
		{"unknown", FormatTable},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			p := NewPrinter(tt.input, false, false)
			assert.Equal(t, tt.want, p.Format)
		})
	}
}

func TestNewPrinter_Flags(t *testing.T) {
	t.Parallel()
	p := NewPrinter("json", true, true)
	assert.True(t, p.Quiet)
	assert.True(t, p.NoColor)
}

func TestPrintJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	data := map[string]string{"name": "test", "status": "running"}
	err := p.PrintJSON(data)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, "running", result["status"])

	// Verify it's indented
	assert.Contains(t, buf.String(), "\n")
	assert.Contains(t, buf.String(), "  ")
}

func TestPrintYAML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	data := map[string]string{"name": "test", "status": "running"}
	err := p.PrintYAML(data)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, "running", result["status"])
}

func TestPrintIDs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintIDs([]string{"1", "42", "100"})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, []string{"1", "42", "100"}, lines)
}

func TestPrintIDs_Empty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintIDs([]string{})
	assert.Empty(t, buf.String())
}

func TestStatusColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  string
		noColor bool
		want    string
	}{
		{name: "running green", status: "running", noColor: false, want: colorGreen + "running" + colorReset},
		{name: "deployed green", status: "deployed", noColor: false, want: colorGreen + "deployed" + colorReset},
		{name: "healthy green", status: "healthy", noColor: false, want: colorGreen + "healthy" + colorReset},
		{name: "online green", status: "online", noColor: false, want: colorGreen + "online" + colorReset},
		{name: "error red", status: "error", noColor: false, want: colorRed + "error" + colorReset},
		{name: "failed red", status: "failed", noColor: false, want: colorRed + "failed" + colorReset},
		{name: "unhealthy red", status: "unhealthy", noColor: false, want: colorRed + "unhealthy" + colorReset},
		{name: "offline red", status: "offline", noColor: false, want: colorRed + "offline" + colorReset},
		{name: "deploying yellow", status: "deploying", noColor: false, want: colorYellow + "deploying" + colorReset},
		{name: "stopping yellow", status: "stopping", noColor: false, want: colorYellow + "stopping" + colorReset},
		{name: "pending yellow", status: "pending", noColor: false, want: colorYellow + "pending" + colorReset},
		{name: "draft gray", status: "draft", noColor: false, want: colorGray + "draft" + colorReset},
		{name: "stopped gray", status: "stopped", noColor: false, want: colorGray + "stopped" + colorReset},
		{name: "unknown gray", status: "unknown", noColor: false, want: colorGray + "unknown" + colorReset},
		{name: "no color", status: "running", noColor: true, want: "running"},
		{name: "unrecognized status", status: "custom", noColor: false, want: "custom"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Printer{NoColor: tt.noColor}
			assert.Equal(t, tt.want, p.StatusColor(tt.status))
		})
	}
}

func TestPrintTable(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	headers := []string{"ID", "NAME", "STATUS"}
	rows := [][]string{
		{"1", "my-stack", "running"},
		{"2", "other-stack", "stopped"},
	}
	p.PrintTable(headers, rows)

	output := buf.String()
	assert.Contains(t, output, "ID")
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "my-stack")
	assert.Contains(t, output, "other-stack")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "stopped")
}

func TestPrintTable_Empty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintTable([]string{"ID", "NAME"}, nil)
	output := buf.String()
	assert.Contains(t, output, "ID")
	assert.Contains(t, output, "NAME")
	// Only header line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 1)
}

func TestPrint_QuietMode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Quiet: true, Format: FormatTable}

	err := p.Print(nil, nil, nil, []string{"5", "10", "15"})
	require.NoError(t, err)
	assert.Equal(t, "5\n10\n15\n", buf.String())
}

func TestPrint_JSONFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	data := []map[string]string{{"id": "1"}}
	err := p.Print(data, nil, nil, nil)
	require.NoError(t, err)

	var result []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestPrint_YAMLFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	data := []map[string]string{{"id": "1"}}
	err := p.Print(data, nil, nil, nil)
	require.NoError(t, err)

	var result []map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestPrint_TableFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable}

	headers := []string{"ID", "NAME"}
	rows := [][]string{{"1", "test"}}
	err := p.Print(nil, headers, rows, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test")
}

func TestPrintSingle_Table(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable}

	fields := []KeyValue{
		{Key: "ID", Value: "42"},
		{Key: "Name", Value: "my-stack"},
		{Key: "Status", Value: "running"},
	}
	err := p.PrintSingle(nil, fields)
	require.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "ID:")
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "Name:")
	assert.Contains(t, output, "my-stack")
}

func TestPrintSingle_JSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	data := map[string]string{"id": "42", "name": "test"}
	err := p.PrintSingle(data, nil)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "42", result["id"])
}

func TestPrintSingle_YAML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	data := map[string]string{"id": "42", "name": "test"}
	err := p.PrintSingle(data, nil)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "42", result["id"])
}

func TestPrintMessage(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintMessage("Hello %s, count: %d", "world", 42)
	assert.Equal(t, "Hello world, count: 42\n", buf.String())
}

// --- Resource-specific formatter tests ---

// Helper to create a realistic StackInstance for tests.
func newTestStackInstance() types.StackInstance {
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	deployedAt := time.Date(2025, 6, 15, 10, 35, 0, 0, time.UTC)
	clusterID := "3"
	return types.StackInstance{
		Base: types.Base{
			ID:        "42",
			CreatedAt: now,
			UpdatedAt: now,
			Version:   "1",
		},
		Name:              "my-app-feature",
		StackDefinitionID: "7",
		DefinitionName:    "my-app",
		Owner:             "alice",
		Branch:            "feature/login",
		Namespace:         "my-app-feature-ns",
		Status:            "running",
		ClusterID:         &clusterID,
		ClusterName:       "dev-cluster",
		TTLMinutes:        120,
		DeployedAt:        &deployedAt,
	}
}

func TestStackInstance_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	instance := newTestStackInstance()
	err := p.PrintJSON(instance)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, "42", result["id"])
	assert.Equal(t, "my-app-feature", result["name"])
	assert.Equal(t, "7", result["stack_definition_id"])
	assert.Equal(t, "my-app", result["definition_name"])
	assert.Equal(t, "alice", result["owner"])
	assert.Equal(t, "feature/login", result["branch"])
	assert.Equal(t, "my-app-feature-ns", result["namespace"])
	assert.Equal(t, "running", result["status"])
	assert.Equal(t, "3", result["cluster_id"])
	assert.Equal(t, "dev-cluster", result["cluster_name"])
	assert.Equal(t, float64(120), result["ttl_minutes"])
	assert.NotEmpty(t, result["deployed_at"])
	assert.NotEmpty(t, result["created_at"])
	assert.NotEmpty(t, result["updated_at"])
	// deleted_at should be omitted when nil
	_, hasDeletedAt := result["deleted_at"]
	assert.False(t, hasDeletedAt, "deleted_at should be omitted when nil")
}

func TestStackInstance_YAML(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	instance := newTestStackInstance()
	err := p.PrintYAML(instance)
	require.NoError(t, err)

	// yaml.v3 inlines fields from embedded structs, so we can unmarshal directly into StackInstance
	var result types.StackInstance
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, "42", result.ID)
	assert.Equal(t, "my-app-feature", result.Name)
	assert.Equal(t, "7", result.StackDefinitionID)
	assert.Equal(t, "alice", result.Owner)
	assert.Equal(t, "feature/login", result.Branch)
	assert.Equal(t, "running", result.Status)
	require.NotNil(t, result.ClusterID)
	assert.Equal(t, "3", *result.ClusterID)
	assert.Equal(t, "dev-cluster", result.ClusterName)
	assert.Nil(t, result.DeletedAt, "deleted_at should be nil")
}

func TestStackInstance_TablePrint(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable, NoColor: true}

	instance := newTestStackInstance()
	headers := []string{"ID", "NAME", "BRANCH", "STATUS", "OWNER", "NAMESPACE"}
	rows := [][]string{
		{
			instance.ID,
			instance.Name,
			instance.Branch,
			p.StatusColor(instance.Status),
			instance.Owner,
			instance.Namespace,
		},
	}
	err := p.Print(instance, headers, rows, nil)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "ID")
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "BRANCH")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "my-app-feature")
	assert.Contains(t, output, "feature/login")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "alice")
	assert.Contains(t, output, "my-app-feature-ns")
}

func TestStackInstance_PrintSingle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable, NoColor: true}

	instance := newTestStackInstance()
	fields := []KeyValue{
		{Key: "ID", Value: instance.ID},
		{Key: "Name", Value: instance.Name},
		{Key: "Branch", Value: instance.Branch},
		{Key: "Status", Value: p.StatusColor(instance.Status)},
		{Key: "Definition", Value: instance.DefinitionName},
		{Key: "Owner", Value: instance.Owner},
		{Key: "Namespace", Value: instance.Namespace},
		{Key: "Cluster", Value: instance.ClusterName},
	}
	err := p.PrintSingle(instance, fields)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "ID:")
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "Name:")
	assert.Contains(t, output, "my-app-feature")
	assert.Contains(t, output, "Branch:")
	assert.Contains(t, output, "feature/login")
	assert.Contains(t, output, "Definition:")
	assert.Contains(t, output, "my-app")
	assert.Contains(t, output, "Cluster:")
	assert.Contains(t, output, "dev-cluster")
}

func TestStackInstance_QuietMode(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Quiet: true, Format: FormatTable}

	err := p.Print(nil, nil, nil, []string{"42", "99"})
	require.NoError(t, err)
	assert.Equal(t, "42\n99\n", buf.String())
}

func TestListResponse_StackInstance_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	instance := newTestStackInstance()
	listResp := types.ListResponse[types.StackInstance]{
		Data:       []types.StackInstance{instance},
		Total:      25,
		Page:       2,
		PageSize:   10,
		TotalPages: 3,
	}

	err := p.PrintJSON(listResp)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, float64(25), result["total"])
	assert.Equal(t, float64(2), result["page"])
	assert.Equal(t, float64(10), result["page_size"])
	assert.Equal(t, float64(3), result["total_pages"])

	dataArr, ok := result["data"].([]interface{})
	require.True(t, ok)
	require.Len(t, dataArr, 1)

	item, ok := dataArr[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "my-app-feature", item["name"])
	assert.Equal(t, "42", item["id"])
}

func TestListResponse_StackInstance_YAML(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	instance := newTestStackInstance()
	listResp := types.ListResponse[types.StackInstance]{
		Data:       []types.StackInstance{instance},
		Total:      25,
		Page:       2,
		PageSize:   10,
		TotalPages: 3,
	}

	err := p.PrintYAML(listResp)
	require.NoError(t, err)

	var result types.ListResponse[types.StackInstance]
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, 25, result.Total)
	assert.Equal(t, 2, result.Page)
	assert.Equal(t, 10, result.PageSize)
	assert.Equal(t, 3, result.TotalPages)
	require.Len(t, result.Data, 1)
	assert.Equal(t, "my-app-feature", result.Data[0].Name)
	assert.Equal(t, "42", result.Data[0].ID)
}

func TestListResponse_EmptyData_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	listResp := types.ListResponse[types.StackInstance]{
		Data:       []types.StackInstance{},
		Total:      0,
		Page:       1,
		PageSize:   10,
		TotalPages: 0,
	}

	err := p.PrintJSON(listResp)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, float64(0), result["total"])
	dataArr, ok := result["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, dataArr, 0)
}

func TestDeploymentLog_AllFormats(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	log := types.DeploymentLog{
		Base: types.Base{
			ID:        "101",
			CreatedAt: now,
			UpdatedAt: now,
			Version:   "1",
		},
		InstanceID: "42",
		Action:     "deploy",
		Status:     "success",
		Output:     "Deployed 3 charts successfully",
	}

	tests := []struct {
		name   string
		format Format
		verify func(t *testing.T, output string)
	}{
		{
			name:   "json",
			format: FormatJSON,
			verify: func(t *testing.T, output string) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(output), &result))
				assert.Equal(t, "101", result["id"])
				assert.Equal(t, "42", result["instance_id"])
				assert.Equal(t, "deploy", result["action"])
				assert.Equal(t, "success", result["status"])
				assert.Equal(t, "Deployed 3 charts successfully", result["output"])
			},
		},
		{
			name:   "yaml",
			format: FormatYAML,
			verify: func(t *testing.T, output string) {
				var result types.DeploymentLog
				require.NoError(t, yaml.Unmarshal([]byte(output), &result))
				assert.Equal(t, "101", result.ID)
				assert.Equal(t, "42", result.InstanceID)
				assert.Equal(t, "deploy", result.Action)
				assert.Equal(t, "success", result.Status)
				assert.Equal(t, "Deployed 3 charts successfully", result.Output)
			},
		},
		{
			name:   "table",
			format: FormatTable,
			verify: func(t *testing.T, output string) {
				assert.Contains(t, output, "101")
				assert.Contains(t, output, "deploy")
				assert.Contains(t, output, "success")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			p := &Printer{Writer: &buf, Format: tt.format, NoColor: true}

			if tt.format == FormatTable {
				headers := []string{"ID", "ACTION", "STATUS", "OUTPUT"}
				rows := [][]string{
					{
						log.ID,
						log.Action,
						log.Status,
						log.Output,
					},
				}
				err := p.Print(log, headers, rows, nil)
				require.NoError(t, err)
			} else {
				err := p.PrintSingle(log, nil)
				require.NoError(t, err)
			}

			tt.verify(t, buf.String())
		})
	}
}

func TestDeploymentLog_EmptyOutput(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	log := types.DeploymentLog{
		Base: types.Base{
			ID:      "102",
			Version: "1",
		},
		InstanceID: "42",
		Action:     "stop",
		Status:     "pending",
		Output:     "",
	}

	err := p.PrintJSON(log)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	// Output is tagged with omitempty and should be omitted from the JSON when empty
	_, hasOutput := result["output"]
	assert.False(t, hasOutput, "output field should be omitted when empty and tagged with omitempty")
	assert.Equal(t, "stop", result["action"])
	assert.Equal(t, "pending", result["status"])
}

func TestInstanceStatus_WithPods_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	status := types.InstanceStatus{
		Status: "running",
		Pods: []types.PodStatus{
			{Name: "my-app-abc123", Status: "Running", Ready: true, Restarts: 0, Age: "2d"},
			{Name: "my-db-def456", Status: "Running", Ready: true, Restarts: 3, Age: "5d"},
			{Name: "my-worker-ghi789", Status: "CrashLoopBackOff", Ready: false, Restarts: 42, Age: "1h"},
		},
	}

	err := p.PrintJSON(status)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, "running", result["status"])

	pods, ok := result["pods"].([]interface{})
	require.True(t, ok)
	require.Len(t, pods, 3)

	pod0, ok := pods[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "my-app-abc123", pod0["name"])
	assert.Equal(t, "Running", pod0["status"])
	assert.Equal(t, true, pod0["ready"])
	assert.Equal(t, float64(0), pod0["restarts"])
	assert.Equal(t, "2d", pod0["age"])

	pod2, ok := pods[2].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "my-worker-ghi789", pod2["name"])
	assert.Equal(t, "CrashLoopBackOff", pod2["status"])
	assert.Equal(t, false, pod2["ready"])
	assert.Equal(t, float64(42), pod2["restarts"])
}

func TestInstanceStatus_WithPods_YAML(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	status := types.InstanceStatus{
		Status: "running",
		Pods: []types.PodStatus{
			{Name: "my-app-abc123", Status: "Running", Ready: true, Restarts: 0, Age: "2d"},
			{Name: "my-db-def456", Status: "Running", Ready: true, Restarts: 3, Age: "5d"},
		},
	}

	err := p.PrintYAML(status)
	require.NoError(t, err)

	var result types.InstanceStatus
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, "running", result.Status)
	require.Len(t, result.Pods, 2)
	assert.Equal(t, "my-app-abc123", result.Pods[0].Name)
	assert.Equal(t, true, result.Pods[0].Ready)
	assert.Equal(t, "Running", result.Pods[1].Status)
	assert.Equal(t, 3, result.Pods[1].Restarts)
}

func TestInstanceStatus_WithPods_Table(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable, NoColor: true}

	pods := []types.PodStatus{
		{Name: "my-app-abc123", Status: "Running", Ready: true, Restarts: 0, Age: "2d"},
		{Name: "my-worker-ghi789", Status: "CrashLoopBackOff", Ready: false, Restarts: 42, Age: "1h"},
	}

	headers := []string{"NAME", "STATUS", "READY", "RESTARTS", "AGE"}
	rows := make([][]string, len(pods))
	for i, pod := range pods {
		ready := "false"
		if pod.Ready {
			ready = "true"
		}
		rows[i] = []string{pod.Name, p.StatusColor(pod.Status), ready, fmt.Sprintf("%d", pod.Restarts), pod.Age}
	}

	err := p.PrintTable(headers, rows)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "RESTARTS")
	assert.Contains(t, output, "my-app-abc123")
	assert.Contains(t, output, "Running")
	assert.Contains(t, output, "my-worker-ghi789")
	assert.Contains(t, output, "CrashLoopBackOff")
	assert.Contains(t, output, "42")
}

func TestInstanceStatus_NoPods_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	status := types.InstanceStatus{
		Status: "stopped",
	}

	err := p.PrintJSON(status)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, "stopped", result["status"])
	// pods should be omitted when nil
	_, hasPods := result["pods"]
	assert.False(t, hasPods, "pods should be omitted when nil")
}

func TestNilAndZeroValueHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data interface{}
		json func(t *testing.T, output string)
		yaml func(t *testing.T, output string)
	}{
		{
			name: "stack_instance_nil_optional_pointers",
			data: types.StackInstance{
				Base: types.Base{
					ID:      "1",
					Version: "1",
				},
				Name:   "minimal",
				Status: "draft",
			},
			json: func(t *testing.T, output string) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(output), &result))
				assert.Equal(t, "1", result["id"])
				assert.Equal(t, "minimal", result["name"])
				assert.Equal(t, "draft", result["status"])
				// nil pointer fields should be omitted
				_, hasClusterID := result["cluster_id"]
				assert.False(t, hasClusterID, "cluster_id should be omitted when nil")
				_, hasDeployedAt := result["deployed_at"]
				assert.False(t, hasDeployedAt, "deployed_at should be omitted when nil")
				_, hasDeletedAt := result["deleted_at"]
				assert.False(t, hasDeletedAt, "deleted_at should be omitted when nil")
				_, hasExpiresAt := result["expires_at"]
				assert.False(t, hasExpiresAt, "expires_at should be omitted when nil")
				// Empty string fields with omitempty should be omitted
				_, hasDefName := result["definition_name"]
				assert.False(t, hasDefName, "definition_name should be omitted when empty")
				_, hasClusterName := result["cluster_name"]
				assert.False(t, hasClusterName, "cluster_name should be omitted when empty")
			},
			yaml: func(t *testing.T, output string) {
				var result types.StackInstance
				require.NoError(t, yaml.Unmarshal([]byte(output), &result))
				assert.Equal(t, "1", result.ID)
				assert.Equal(t, "minimal", result.Name)
				assert.Equal(t, "draft", result.Status)
				// nil pointer fields should remain nil after round-trip
				assert.Nil(t, result.ClusterID, "cluster_id should be nil")
				assert.Nil(t, result.DeletedAt, "deleted_at should be nil")
				assert.Nil(t, result.DeployedAt, "deployed_at should be nil")
				assert.Nil(t, result.ExpiresAt, "expires_at should be nil")
			},
		},
		{
			name: "stack_instance_zero_ttl_and_empty_strings",
			data: types.StackInstance{
				Base: types.Base{
					ID:      "2",
					Version: "1",
				},
				Name:              "zero-ttl",
				StackDefinitionID: "5",
				Owner:             "bob",
				Branch:            "",
				Namespace:         "default",
				Status:            "draft",
				TTLMinutes:        0,
			},
			json: func(t *testing.T, output string) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(output), &result))
				assert.Equal(t, "zero-ttl", result["name"])
				// TTLMinutes=0 with omitempty should be omitted
				_, hasTTL := result["ttl_minutes"]
				assert.False(t, hasTTL, "ttl_minutes should be omitted when zero")
				// Branch="" without omitempty should still be present
				assert.Contains(t, output, "\"branch\"")
			},
			yaml: func(t *testing.T, output string) {
				var result types.StackInstance
				require.NoError(t, yaml.Unmarshal([]byte(output), &result))
				assert.Equal(t, "zero-ttl", result.Name)
				assert.Equal(t, "5", result.StackDefinitionID)
				assert.Equal(t, 0, result.TTLMinutes)
			},
		},
		{
			name: "deployment_log_empty_output_field",
			data: types.DeploymentLog{
				Base: types.Base{
					ID:      "200",
					Version: "1",
				},
				InstanceID: "50",
				Action:     "clean",
				Status:     "success",
				Output:     "",
			},
			json: func(t *testing.T, output string) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(output), &result))
				assert.Equal(t, "200", result["id"])
				assert.Equal(t, "clean", result["action"])
				// Output="" with omitempty should be omitted
				_, hasOutput := result["output"]
				assert.False(t, hasOutput, "output should be omitted when empty")
			},
			yaml: func(t *testing.T, output string) {
				var result types.DeploymentLog
				require.NoError(t, yaml.Unmarshal([]byte(output), &result))
				assert.Equal(t, "200", result.ID)
				assert.Equal(t, "clean", result.Action)
				assert.Equal(t, "success", result.Status)
				assert.Empty(t, result.Output, "output should be empty after round-trip")
			},
		},
		{
			name: "pod_status_zero_restarts_and_no_age",
			data: types.PodStatus{
				Name:     "test-pod",
				Status:   "Running",
				Ready:    true,
				Restarts: 0,
				Age:      "",
			},
			json: func(t *testing.T, output string) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(output), &result))
				assert.Equal(t, "test-pod", result["name"])
				assert.Equal(t, true, result["ready"])
				assert.Equal(t, float64(0), result["restarts"])
				// Age="" with omitempty should be omitted
				_, hasAge := result["age"]
				assert.False(t, hasAge, "age should be omitted when empty")
			},
			yaml: func(t *testing.T, output string) {
				var result types.PodStatus
				require.NoError(t, yaml.Unmarshal([]byte(output), &result))
				assert.Equal(t, "test-pod", result.Name)
				assert.Equal(t, true, result.Ready)
				assert.Equal(t, 0, result.Restarts)
				assert.Empty(t, result.Age)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name+"_json", func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			p := &Printer{Writer: &buf, Format: FormatJSON}
			err := p.PrintJSON(tt.data)
			require.NoError(t, err)
			tt.json(t, buf.String())
		})
		t.Run(tt.name+"_yaml", func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			p := &Printer{Writer: &buf, Format: FormatYAML}
			err := p.PrintYAML(tt.data)
			require.NoError(t, err)
			tt.yaml(t, buf.String())
		})
	}
}

func TestMultipleStackInstances_TableRows(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable, NoColor: true}

	instances := []types.StackInstance{
		{Base: types.Base{ID: "1"}, Name: "app-one", Branch: "main", Status: "running", Owner: "alice"},
		{Base: types.Base{ID: "2"}, Name: "app-two", Branch: "develop", Status: "stopped", Owner: "bob"},
		{Base: types.Base{ID: "3"}, Name: "app-three", Branch: "feature/x", Status: "error", Owner: "charlie"},
	}

	headers := []string{"ID", "NAME", "BRANCH", "STATUS", "OWNER"}
	rows := make([][]string, len(instances))
	for i, inst := range instances {
		rows[i] = []string{
			inst.ID,
			inst.Name,
			inst.Branch,
			p.StatusColor(inst.Status),
			inst.Owner,
		}
	}

	err := p.Print(instances, headers, rows, nil)
	require.NoError(t, err)

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 4) // 1 header + 3 data rows
	assert.Contains(t, lines[0], "ID")
	assert.Contains(t, lines[1], "app-one")
	assert.Contains(t, lines[2], "app-two")
	assert.Contains(t, lines[3], "app-three")
}

func TestListResponse_MultiplePages_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	listResp := types.ListResponse[types.DeploymentLog]{
		Data: []types.DeploymentLog{
			{Base: types.Base{ID: "1"}, InstanceID: "10", Action: "deploy", Status: "success", Output: "ok"},
			{Base: types.Base{ID: "2"}, InstanceID: "10", Action: "stop", Status: "success", Output: "stopped"},
		},
		Total:      50,
		Page:       3,
		PageSize:   2,
		TotalPages: 25,
	}

	err := p.PrintJSON(listResp)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

	assert.Equal(t, float64(50), result["total"])
	assert.Equal(t, float64(3), result["page"])
	assert.Equal(t, float64(2), result["page_size"])
	assert.Equal(t, float64(25), result["total_pages"])

	dataArr, ok := result["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, dataArr, 2)
}
