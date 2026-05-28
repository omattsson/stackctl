//go:build live

package live

import (
	"encoding/json"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveDefinition_ListAndGet locks the read-side definition contract.
func TestLiveDefinition_ListAndGet(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	def := requireDefinition(t, c)

	got, err := c.GetDefinition(def.ID)
	require.NoError(t, err, "get definition by ID")
	assert.Equal(t, def.ID, got.ID)
	assert.NotEmpty(t, got.Name)
}

// TestLiveDefinition_ExportImportRoundTrip locks the import wire contract
// — the seed-definitions.sh script in kvk-k8s-dev posts *.definition.json
// files via this path and any field-name drift would silently drop charts.
func TestLiveDefinition_ExportImportRoundTrip(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	def := requireDefinition(t, c)

	exported, err := c.ExportDefinition(def.ID)
	require.NoError(t, err, "export definition")
	require.NotEmpty(t, exported, "export must return a payload")

	// Verify the export looks like the import contract before sending it
	// back — a typo on schema_version would 400 on re-import.
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(exported, &bundle))
	assert.Contains(t, bundle, "schema_version", "exported bundle must have schema_version")
	assert.Contains(t, bundle, "definition", "exported bundle must have definition")

	// Re-import with a renamed definition so we don't collide.
	prefix := liveResourcePrefix()
	innerDef, ok := bundle["definition"].(map[string]any)
	require.True(t, ok, "definition field must be an object")
	innerDef["name"] = prefix + "-reimport"

	rewritten, err := json.Marshal(bundle)
	require.NoError(t, err)

	imported, err := c.ImportDefinition(rewritten)
	require.NoError(t, err, "import renamed bundle")
	require.NotEmpty(t, imported.ID)
	deleteDefinitionIfExists(t, c, imported.ID)

	// Re-fetch to confirm charts came along with the import.
	roundtrip, err := c.GetDefinition(imported.ID)
	require.NoError(t, err)
	if origCharts, ok := bundle["charts"].([]any); ok && len(origCharts) > 0 {
		assert.NotEmpty(t, roundtrip.Charts, "imported definition must carry its charts")
	}
}

// TestLiveDefinition_CreateBare verifies the CreateDefinitionRequest wire
// shape end-to-end. Bare (no charts) — the inline-charts case is exercised
// via ImportDefinition above.
func TestLiveDefinition_CreateBare(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()
	created, err := c.CreateDefinition(&types.CreateDefinitionRequest{
		Name:        prefix + "-def",
		Description: "live-test bare definition",
	})
	require.NoError(t, err, "create definition")
	require.NotEmpty(t, created.ID)
	deleteDefinitionIfExists(t, c, created.ID)

	assert.Equal(t, prefix+"-def", created.Name)
}
