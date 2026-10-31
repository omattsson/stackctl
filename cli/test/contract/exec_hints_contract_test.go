package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecHintsContract pins the exec hints and the API-key flag of the
// stream routes (stackctl#183, k8s-stack-manager#728, v0.15.0) in the
// vendored swagger. TestResponseSchemas_MatchBackend checks every Go tag
// against the schema; this test also fails when the swagger loses a field
// (an old refresh) or a Go type loses its tag, and it checks the query
// parameter that stackctl sends, which the response check does not see.
func TestExecHintsContract(t *testing.T) {
	t.Parallel()
	schema := loadSwagger(t)

	fields := []struct {
		def, prop, swaggerType, itemsType string
		goType                            any
		goField                           string
	}{
		{"k8s.NamespaceStatus", "exec_hints", "boolean", "", types.InstanceStatus{}, "ExecHints"},
		{"k8s.ChartStatus", "exec_containers", "array", "string", types.ChartStatus{}, "ExecContainers"},
		{"k8s.ChartStatus", "exec_default_container", "string", "", types.ChartStatus{}, "ExecDefaultContainer"},
		{"handlers.UIConfigResponse", "stream_api_keys_allowed", "boolean", "", types.UIConfig{}, "StreamAPIKeysAllowed"},
	}
	for _, f := range fields {
		t.Run(f.def+"."+f.prop, func(t *testing.T) {
			t.Parallel()
			def, ok := schema.Definitions[f.def]
			require.Truef(t, ok, "swagger has no definition %q", f.def)
			prop, ok := def.Properties[f.prop]
			require.Truef(t, ok, "swagger definition %q has no %q (refresh swagger.json from k8s-stack-manager v0.15.0 or later)", f.def, f.prop)
			assert.Equal(t, f.swaggerType, prop.Type)
			if f.itemsType != "" {
				require.NotNil(t, prop.Items)
				assert.Equal(t, f.itemsType, prop.Items.Type)
			}
			sf, ok := reflect.TypeOf(f.goType).FieldByName(f.goField)
			require.Truef(t, ok, "%T has no field %s", f.goType, f.goField)
			jsonName, jsonOpts, _ := strings.Cut(sf.Tag.Get("json"), ",")
			assert.Equal(t, f.prop, jsonName, "json tag of %T.%s", f.goType, f.goField)
			assert.Contains(t, jsonOpts, "omitempty", "an older server sends no %s: the output must not invent it", f.prop)
			yamlName, _, _ := strings.Cut(sf.Tag.Get("yaml"), ",")
			assert.Equal(t, f.prop, yamlName, "-o yaml has the same key as -o json")
		})
	}
	t.Run("StreamAPIKeysAllowed tells missing from false", func(t *testing.T) {
		t.Parallel()
		sf, _ := reflect.TypeOf(types.UIConfig{}).FieldByName("StreamAPIKeysAllowed")
		assert.Equal(t, reflect.Ptr, sf.Type.Kind(), "nil means an older server; false stops the command")
	})

	var raw struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
				Type string `json:"type"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(swaggerJSON, &raw))
	for _, path := range []string{"/api/v1/stack-instances/{id}/pods", "/api/v1/stack-instances/{id}/status"} {
		t.Run(path+" exec_hints query", func(t *testing.T) {
			t.Parallel()
			get, ok := raw.Paths[path]["get"]
			require.Truef(t, ok, "swagger has no GET %s", path)
			found := false
			for _, p := range get.Parameters {
				if p.Name == "exec_hints" {
					found = true
					assert.Equal(t, "query", p.In)
					assert.Equal(t, "boolean", p.Type)
				}
			}
			assert.Truef(t, found, "GET %s has no exec_hints query parameter (refresh swagger.json)", path)
		})
	}
}
