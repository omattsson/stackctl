//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveTemplate_VersionsListGetDiff exercises the template-version
// wire contract: list → get → diff. Each publish creates a new version
// snapshot server-side, so we publish twice (with a description change
// in between) to guarantee at least two diffable versions.
//
// Coverage rationale: the versions endpoints carry a custom envelope
// (TemplateVersionDetail wraps TemplateVersion + Snapshot; the diff
// response is left/right/chart_diffs) — wire-shape regressions on any
// of these would silently break `template versions diff` in the CLI.
func TestLiveTemplate_VersionsListGetDiff(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()

	// Create a throwaway template with one chart so the snapshot is
	// non-empty. Note: stackctl's CreateTemplateRequest has no `version`
	// field, so the template-level Version stays empty and the diff
	// response's left.version / right.version come back empty too. We
	// assert on the snapshot's template name (always populated) rather
	// than .version, and call out the gap below.
	tmpl, err := c.CreateTemplate(&types.CreateTemplateRequest{
		Name:        prefix + "-versions",
		Description: "live-test versions fixture (v1)",
		Charts: []types.ChartConfig{
			{ChartName: "noop-a", RepoURL: "", ChartVersion: "0.1.0"},
		},
	})
	require.NoError(t, err, "create template")
	require.NotEmpty(t, tmpl.ID, "created template must have an ID")
	deleteTemplateIfExists(t, c, tmpl.ID)

	// First publish → version 1.
	_, err = c.PublishTemplate(tmpl.ID)
	require.NoError(t, err, "publish template (v1)")

	// Update description so the second snapshot differs from the first.
	// Backend rejects PUT /api/v1/templates/:id with `name is required`
	// when Name is empty (the stackctl type has json:"name,omitempty" but
	// the backend treats it as required). Echo the existing name to keep
	// it a description-only change. Worth a follow-up: tighten stackctl's
	// UpdateTemplateRequest to drop the omitempty on Name, or relax the
	// backend.
	_, err = c.UpdateTemplate(tmpl.ID, &types.UpdateTemplateRequest{
		Name:        tmpl.Name,
		Description: "live-test versions fixture (v2)",
	})
	require.NoError(t, err, "update template")

	// Second publish → version 2.
	_, err = c.PublishTemplate(tmpl.ID)
	require.NoError(t, err, "publish template (v2)")

	// 1. List — must return both snapshots.
	versions, err := c.ListTemplateVersions(tmpl.ID)
	require.NoError(t, err, "list template versions")
	require.GreaterOrEqual(t, len(versions), 2,
		"two publishes must produce at least two version rows (got %d)", len(versions))
	for i, v := range versions {
		assert.Equal(t, tmpl.ID, v.TemplateID, "versions[%d].template_id must echo the template", i)
		assert.NotEmpty(t, v.ID, "versions[%d].id must be populated", i)
		assert.False(t, v.CreatedAt.IsZero(), "versions[%d].created_at must decode", i)
	}

	// 2. Get version detail — TemplateVersionDetail = TemplateVersion + Snapshot.
	left := versions[len(versions)-1] // oldest (list is typically newest-first)
	right := versions[0]              // newest
	detail, err := c.GetTemplateVersion(tmpl.ID, right.ID)
	require.NoError(t, err, "get template version detail")
	assert.Equal(t, right.ID, detail.ID, "detail must echo the requested version id")
	assert.NotEmpty(t, detail.Snapshot.Template.Name, "snapshot.template.name must decode")
	assert.Len(t, detail.Snapshot.Charts, 1, "snapshot must echo the one inline chart")
	assert.Equal(t, "noop-a", detail.Snapshot.Charts[0].ChartName,
		"snapshot chart name must round-trip")

	// 3. Diff between the two snapshots — the description-only change is
	// captured at the template level, not in chart_diffs, but the chart_diffs
	// slice must decode (empty or not). What matters is the left/right
	// envelope wire shape.
	diff, err := c.DiffTemplateVersions(tmpl.ID, left.ID, right.ID)
	require.NoError(t, err, "diff template versions")
	require.NotNil(t, diff, "diff response must not be nil")
	// diff.{left,right}.version mirrors the template-level Version which
	// stackctl can't set today (CreateTemplateRequest has no version
	// field — a follow-up gap). Assert on the snapshot.template.name
	// instead, which always round-trips.
	assert.NotEmpty(t, diff.Left.Snapshot.Template.Name, "diff.left.snapshot.template.name must decode")
	assert.NotEmpty(t, diff.Right.Snapshot.Template.Name, "diff.right.snapshot.template.name must decode")
	assert.NotNil(t, diff.ChartDiffs, "chart_diffs must be a populated slice, even if empty")
}
