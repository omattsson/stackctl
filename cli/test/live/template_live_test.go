//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveTemplate_ListAndGet verifies the read-side template wire
// contract: list returns the paginated envelope and at least one item
// has the documented fields populated.
func TestLiveTemplate_ListAndGet(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	tmpl := requireTemplate(t, c)

	// GetTemplate returns the new TemplateDetailResponse shape (template
	// fields at top level + a charts array). Sanity-check we got back
	// something the type can decode without losing the ID.
	got, err := c.GetTemplate(tmpl.ID)
	require.NoError(t, err, "get template by ID")
	assert.Equal(t, tmpl.ID, got.ID)
	assert.NotEmpty(t, got.Name)
}

// TestLiveTemplate_CreateWithInlineCharts exercises the path fixed in
// k8s-stack-manager#264 — `POST /api/v1/templates` now accepts a `charts`
// array inline and persists every entry transactionally. This test was
// the missing safety net while that bug shipped.
func TestLiveTemplate_CreateWithInlineCharts(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()
	name := prefix + "-template"

	created, err := c.CreateTemplate(&types.CreateTemplateRequest{
		Name:        name,
		Description: "live-test fixture — safe to delete",
		Charts: []types.ChartConfig{
			{ChartName: "noop-a", RepoURL: "", ChartVersion: "0.1.0"},
			{ChartName: "noop-b", RepoURL: "", ChartVersion: "0.1.0"},
		},
	})
	require.NoError(t, err, "create template with inline charts")
	require.NotEmpty(t, created.ID, "created template must have an ID")
	deleteTemplateIfExists(t, c, created.ID)

	// Read the template back via GET — backend's TemplateDetailResponse
	// includes the persisted charts. Before k8s-stack-manager#264 the
	// inline charts array was silently dropped by gin's bind, so the
	// re-read returned 0 charts.
	//
	// Note: stackctl's ChartConfig type intentionally omits backend-only
	// fields like chart_path / locked_values / deploy_order / required —
	// we only assert presence + the fields the client maps.
	detail, err := c.GetTemplate(created.ID)
	require.NoError(t, err, "get template by ID")
	require.Len(t, detail.Charts, 2, "GET must return both inline-created charts")

	// Publish to materialise a version snapshot — versions are only
	// created at publish time, not at create time. Then read it back.
	_, err = c.PublishTemplate(created.ID)
	require.NoError(t, err, "publish template (to trigger version snapshot)")

	versions, err := c.ListTemplateVersions(created.ID)
	require.NoError(t, err, "list template versions")
	require.NotEmpty(t, versions, "publish must produce a version snapshot")

	snap, err := c.GetTemplateVersion(created.ID, versions[0].ID)
	require.NoError(t, err, "get template version detail")
	assert.Len(t, snap.Snapshot.Charts, 2, "snapshot must echo both inline charts after publish")
}

// TestLiveTemplate_PublishLifecycle round-trips publish/unpublish — both
// are devops-gated; we assume the API key has at least devops role.
func TestLiveTemplate_PublishLifecycle(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()
	tmpl, err := c.CreateTemplate(&types.CreateTemplateRequest{
		Name:        prefix + "-publish",
		Description: "live-test publish fixture",
	})
	require.NoError(t, err)
	deleteTemplateIfExists(t, c, tmpl.ID)
	require.False(t, tmpl.Published, "fresh template must start unpublished")

	_, err = c.PublishTemplate(tmpl.ID)
	require.NoError(t, err, "publish template")
	after, err := c.GetTemplate(tmpl.ID)
	require.NoError(t, err)
	assert.True(t, after.Published, "template must be published after PublishTemplate")

	_, err = c.UnpublishTemplate(tmpl.ID)
	require.NoError(t, err, "unpublish template")
	again, err := c.GetTemplate(tmpl.ID)
	require.NoError(t, err)
	assert.False(t, again.Published, "template must be unpublished after UnpublishTemplate")
}
