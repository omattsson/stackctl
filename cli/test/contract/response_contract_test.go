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
		{name: "UIConfig", goType: types.UIConfig{}, swaggerDef: "handlers.UIConfigResponse"},
		{name: "Dashboard", goType: types.Dashboard{}, swaggerDef: "handlers.DashboardResponse"},
		{name: "DashboardCluster", goType: types.DashboardCluster{}, swaggerDef: "handlers.DashboardCluster"},
		{name: "DashboardDeployment", goType: types.DashboardDeployment{}, swaggerDef: "handlers.DashboardDeployment"},
		{name: "DashboardExpiring", goType: types.DashboardExpiring{}, swaggerDef: "handlers.DashboardExpiring"},
		{name: "DashboardFailing", goType: types.DashboardFailing{}, swaggerDef: "handlers.DashboardFailing"},
		{name: "SharedValues", goType: types.SharedValues{}, swaggerDef: "models.SharedValues", excludeGoTags: baseOnly},
		{name: "UpdateSharedValuesRequest", goType: types.UpdateSharedValuesRequest{}, swaggerDef: "models.SharedValues"},
		{name: "FollowState", goType: types.FollowState{}, swaggerDef: "handlers.followStateResponse"},
		{name: "DeployPreview", goType: types.DeployPreview{}, swaggerDef: "handlers.DeployPreviewResponse"},
		{name: "ChartDeployPreview", goType: types.ChartDeployPreview{}, swaggerDef: "handlers.ChartDeployPreview"},
		{name: "ActionList", goType: types.ActionList{}, swaggerDef: "handlers.actionListResponse"},
		{name: "ActionJobLog", goType: types.ActionJobLog{}, swaggerDef: "handlers.actionJobLogResponse"},
		{name: "UpgradeCheck", goType: types.UpgradeCheck{}, swaggerDef: "handlers.upgradeCheckResponse"},
		{name: "UpgradeChanges", goType: types.UpgradeChanges{}, swaggerDef: "handlers.upgradeChanges"},
		{name: "TemplateLinkRequest", goType: types.TemplateLinkRequest{}, swaggerDef: "handlers.templateLinkRequest"},
		// filters is a $ref in the schema (no type); the
		// NotificationChannelFilters case checks its fields.
		{name: "CreateNotificationChannelRequest", goType: types.CreateNotificationChannelRequest{}, swaggerDef: "handlers.createChannelRequest", excludeGoTags: []string{"filters"}},
		{name: "UpdateNotificationChannelRequest", goType: types.UpdateNotificationChannelRequest{}, swaggerDef: "handlers.updateChannelRequest", excludeGoTags: []string{"filters"}},
		{name: "NotificationChannelFilters", goType: types.NotificationChannelFilters{}, swaggerDef: "models.NotificationChannelFilters"},
		{
			name:       "NotificationChannel",
			goType:     types.NotificationChannel{},
			swaggerDef: "models.NotificationChannel",
			// subscription_count comes only with the list
			// (handlers.notificationChannelWithCount); warnings only with
			// create and update (handlers.notificationChannelResponse).
			// filters is a $ref (see above).
			excludeGoTags: []string{"subscription_count", "warnings", "filters"},
		},
		// The delivery log route documents a generic map, so the schema has
		// no definition for ChannelDeliveryLog.
		{
			name:       "StackInstance",
			goType:     types.StackInstance{},
			swaggerDef: "models.StackInstance",
			// definition_name, cluster_name and owner_username are display
			// fields the backend does not send on instance responses yet
			// (k8s-stack-manager#470). warning comes only with the clone
			// response (handlers.CloneInstanceResponse).
			excludeGoTags: append([]string{"definition_name", "cluster_name", "owner_username", "warning"}, baseOnly...),
		},
		{
			name:          "CloneInstanceResponse",
			goType:        types.StackInstance{},
			swaggerDef:    "handlers.CloneInstanceResponse",
			excludeGoTags: append([]string{"definition_name", "cluster_name", "owner_username"}, baseOnly...),
		},
		{name: "RollbackResponse", goType: types.RollbackResponse{}, swaggerDef: "handlers.RollbackResponse"},
		{name: "OrphanedNamespace", goType: types.OrphanedNamespace{}, swaggerDef: "handlers.OrphanedNamespaceResponse"},
		{name: "ResourceCounts", goType: types.ResourceCounts{}, swaggerDef: "k8s.ResourceCounts"},
		{name: "BulkResponse", goType: types.BulkResponse{}, swaggerDef: "handlers.BulkOperationResponse"},
		{name: "BulkTemplateResponse", goType: types.BulkResponse{}, swaggerDef: "handlers.BulkTemplateResponse"},
		{
			// One Go type decodes both result shapes: the template tags
			// are not part of the instance result.
			name:          "BulkOperationResult",
			goType:        types.BulkOperationResult{},
			swaggerDef:    "handlers.BulkOperationResultItem",
			excludeGoTags: []string{"template_id", "template_name"},
		},
		{
			name:          "BulkTemplateResult",
			goType:        types.BulkOperationResult{},
			swaggerDef:    "handlers.BulkTemplateResultItem",
			excludeGoTags: []string{"instance_id", "instance_name", "log_id"},
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
