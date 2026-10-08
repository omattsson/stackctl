package contract

import (
	"reflect"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

// timeType is time.Time; the schema walker maps it to a swagger string.
var timeType = reflect.TypeOf(time.Time{})

// TestResponseSchemas_MatchBackend checks that the stackctl types that
// decode API responses use the json tags of the backend schema. A wrong
// tag decodes to a zero value without an error (stackctl#128, #130, #131),
// so every Go tag must exist in the swagger definition. "Required" is not
// checked: response fields are not validated by the backend.
func TestResponseSchemas_MatchBackend(t *testing.T) {
	t.Parallel()
	schema := loadSwagger(t)

	// baseOnly are the types.Base tags the backend does not send for
	// resources without soft delete or a version string.
	baseOnly := []string{"deleted_at", "version"}

	cases := []contractCase{
		{name: "ValueOverride", goType: types.ValueOverride{}, swaggerDef: "models.ValueOverride"},
		{name: "BranchOverride", goType: types.BranchOverride{}, swaggerDef: "models.ChartBranchOverride"},
		{name: "QuotaOverride", goType: types.QuotaOverride{}, swaggerDef: "models.InstanceQuotaOverride"},
		{name: "SetQuotaOverrideRequest", goType: types.SetQuotaOverrideRequest{}, swaggerDef: "handlers.setQuotaOverrideRequest"},
		{name: "CompareResult", goType: types.CompareResult{}, swaggerDef: "handlers.CompareInstancesResponse"},
		{name: "CompareInstanceSummary", goType: types.CompareInstanceSummary{}, swaggerDef: "handlers.CompareInstanceSummary"},
		{name: "CompareChartDiff", goType: types.CompareChartDiff{}, swaggerDef: "handlers.CompareChartDiff"},
		{name: "InstanceStatus", goType: types.InstanceStatus{}, swaggerDef: "k8s.NamespaceStatus"},
		{name: "ChartStatus", goType: types.ChartStatus{}, swaggerDef: "k8s.ChartStatus"},
		{name: "PodStatus", goType: types.PodStatus{}, swaggerDef: "k8s.PodInfo"},
		{name: "ContainerStateInfo", goType: types.ContainerStateInfo{}, swaggerDef: "k8s.ContainerStateInfo"},
		{name: "PodConditionInfo", goType: types.PodConditionInfo{}, swaggerDef: "k8s.PodConditionInfo"},
		{name: "DeploymentInfo", goType: types.DeploymentInfo{}, swaggerDef: "k8s.DeploymentInfo"},
		{name: "ServiceInfo", goType: types.ServiceInfo{}, swaggerDef: "k8s.ServiceInfo"},
		{name: "IngressInfo", goType: types.IngressInfo{}, swaggerDef: "k8s.IngressInfo"},
		{name: "PodEvent", goType: types.PodEvent{}, swaggerDef: "k8s.PodEvent"},
		{name: "GitBranch", goType: types.GitBranch{}, swaggerDef: "gitprovider.Branch"},
		{name: "Cluster", goType: types.Cluster{}, swaggerDef: "models.Cluster"},
		{
			name:       "StackInstance",
			goType:     types.StackInstance{},
			swaggerDef: "models.StackInstance",
			// definition_name and cluster_name are display fields the
			// backend does not send on instance responses (yet).
			excludeGoTags: append([]string{"definition_name", "cluster_name"}, baseOnly...),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def, ok := schema.Definitions[tc.swaggerDef]
			require.Truef(t, ok, "swagger schema is missing definition %q (refresh swagger.json?)", tc.swaggerDef)
			// Responses: skip the "required" direction.
			def.Required = nil
			assertFieldsMatch(t, tc.goType, def, tc.excludeGoTags, tc.excludeRequire)
		})
	}
}
