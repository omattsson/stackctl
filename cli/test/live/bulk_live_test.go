//go:build live

package live

import (
	"fmt"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveBulk_TemplateRoundTrip exercises the template bulk wire contract
// without creating real workloads — bulk publish/unpublish are pure
// metadata operations on the template object.
//
// Note: this is the regression test for the `template_ids` field-name
// drift fixed in fix/bulk-wire-contract. Stub-based unit tests didn't
// catch it because they decoded against stackctl's own wrong type.
func TestLiveBulk_TemplateRoundTrip(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	// Create 2 throwaway templates so we can publish + unpublish them in
	// bulk without affecting the seeded Klaravik templates.
	prefix := liveResourcePrefix()
	ids := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		tmpl, err := c.CreateTemplate(&types.CreateTemplateRequest{
			Name:        fmt.Sprintf("%s-bulk-%d", prefix, i),
			Description: "live-test bulk fixture",
		})
		require.NoError(t, err, "create template %d", i)
		ids = append(ids, tmpl.ID)
		deleteTemplateIfExists(t, c, tmpl.ID)
	}

	// Publish
	pubResp, err := c.BulkPublishTemplates(ids)
	require.NoError(t, err, "bulk publish templates")
	require.Len(t, pubResp.Results, 2)
	for _, r := range pubResp.Results {
		assert.True(t, r.Success(),
			"bulk publish must succeed for template_id=%s status=%q error=%q",
			r.ID(), r.Status, r.Error)
	}

	// Unpublish
	unpubResp, err := c.BulkUnpublishTemplates(ids)
	require.NoError(t, err, "bulk unpublish templates")
	require.Len(t, unpubResp.Results, 2)
	for _, r := range unpubResp.Results {
		assert.True(t, r.Success(),
			"bulk unpublish must succeed for template_id=%s status=%q error=%q",
			r.ID(), r.Status, r.Error)
	}

	// Bulk delete — cleanup-via-bulk also exercises the delete endpoint.
	// We still have deleteTemplateIfExists registered as a safety net.
	delResp, err := c.BulkDeleteTemplates(ids)
	require.NoError(t, err, "bulk delete templates")
	require.Len(t, delResp.Results, 2)
	for _, r := range delResp.Results {
		assert.True(t, r.Success(),
			"bulk delete must succeed for template_id=%s status=%q error=%q",
			r.ID(), r.Status, r.Error)
	}
}

// TestLiveBulk_StackInstanceWireShape exercises the stack-instance bulk
// endpoints without actually deploying anything. Creates draft instances
// (status=draft, no PV, no pods), runs bulk delete against them, and
// verifies the response shape.
//
// This is the regression test for the `instance_ids` field-name drift.
func TestLiveBulk_StackInstanceWireShape(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	def := requireDefinition(t, c)

	// Create 2 draft instances. CreateStack returns immediately without
	// triggering helm — instance sits in `draft` state until DeployStack
	// is called, which we deliberately skip.
	prefix := liveResourcePrefix()
	ids := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		inst, err := c.CreateStack(&types.CreateStackRequest{
			Name:              fmt.Sprintf("%s-bulk-inst-%d", prefix, i),
			StackDefinitionID: def.ID,
			Branch:            "master",
		})
		require.NoError(t, err, "create draft instance %d", i)
		ids = append(ids, inst.ID)
		// Safety-net cleanup in case bulk delete fails partway.
		t.Cleanup(func(id string) func() {
			return func() { _ = c.DeleteStack(id) }
		}(inst.ID))
	}

	// Bulk delete — pure metadata op on draft instances, no helm hook fires.
	resp, err := c.BulkDelete(ids)
	require.NoError(t, err, "bulk delete draft instances")
	require.Len(t, resp.Results, 2)
	for _, r := range resp.Results {
		assert.True(t, r.Success(),
			"bulk delete must succeed for instance_id=%s status=%q error=%q",
			r.ID(), r.Status, r.Error)
	}

	// Confirm gone.
	for _, id := range ids {
		_, err := c.GetStack(id)
		assert.Error(t, err, "GetStack on deleted draft %s should fail", id)
	}
}
