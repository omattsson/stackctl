//go:build live

package live

import (
	"errors"
	"net/http"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveTemplate_PublishRelease covers the draft-and-release model of
// k8s-stack-manager v0.6.0+: publish with a version, an idempotent
// re-publish, an edit of the working copy, a diff against "working", a
// second release, and the 409 for a version that already exists.
// It skips on older servers.
func TestLiveTemplate_PublishRelease(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()
	tmpl, err := c.CreateTemplate(&types.CreateTemplateRequest{
		Name:        prefix + "-release",
		Description: "live-test release fixture (v1)",
		Version:     "1.0.0",
		Charts: []types.ChartConfig{
			{ChartName: "noop-a", RepoURL: "", ChartVersion: "0.1.0"},
		},
	})
	require.NoError(t, err, "create template")
	deleteTemplateIfExists(t, c, tmpl.ID)

	current, err := c.GetTemplate(tmpl.ID)
	require.NoError(t, err, "get template")
	if current.HasUnpublishedChanges == nil {
		t.Skip("server has no draft-and-release model (needs k8s-stack-manager v0.6.0 or later)")
	}
	assert.Nil(t, current.PublishedVersion, "no release before the first publish")

	// 1. Publish 1.0.0.
	first, err := c.PublishTemplateRelease(tmpl.ID, &types.PublishTemplateRequest{Version: "1.0.0", ChangeSummary: "first release"})
	require.NoError(t, err, "publish 1.0.0")
	require.NotNil(t, first.SnapshotCreated)
	assert.True(t, *first.SnapshotCreated, "first publish creates a version")
	require.NotNil(t, first.PublishedVersion)
	assert.Equal(t, "1.0.0", *first.PublishedVersion)
	require.NotNil(t, first.PublishedVersionID)
	firstID := *first.PublishedVersionID

	// 2. Re-publish without changes: no new version.
	again, err := c.PublishTemplateRelease(tmpl.ID, nil)
	require.NoError(t, err, "re-publish unchanged")
	require.NotNil(t, again.SnapshotCreated)
	assert.False(t, *again.SnapshotCreated, "unchanged publish creates no version")
	require.NotNil(t, again.PublishedVersion)
	assert.Equal(t, "1.0.0", *again.PublishedVersion)

	// 3. Edit the working copy (partial PUT).
	desc := "live-test release fixture (v2)"
	_, err = c.PatchTemplate(tmpl.ID, &types.PatchTemplateRequest{Description: &desc})
	require.NoError(t, err, "edit working copy")
	edited, err := c.GetTemplate(tmpl.ID)
	require.NoError(t, err)
	require.NotNil(t, edited.HasUnpublishedChanges)
	assert.True(t, *edited.HasUnpublishedChanges, "edit makes unpublished changes")
	require.NotNil(t, edited.PublishedVersion)
	assert.Equal(t, "1.0.0", *edited.PublishedVersion, "users keep the release")
	assert.Len(t, edited.PublishedCharts, 1, "the release has one chart")

	// 4. Diff the release against the working copy.
	diff, err := c.DiffTemplateVersions(tmpl.ID, firstID, types.TemplateVersionWorkingCopy)
	require.NoError(t, err, "diff release vs working")
	assert.Equal(t, "1.0.0", diff.Left.Version)
	assert.False(t, diff.Left.IsWorkingCopy)
	assert.True(t, diff.Right.IsWorkingCopy, "right side is the working copy")
	assert.Equal(t, types.TemplateVersionWorkingCopy, diff.Right.ID)
	assert.Equal(t, desc, diff.Right.Snapshot.Template.Description)

	// 5. A version that exists gives 409.
	_, err = c.PublishTemplateRelease(tmpl.ID, &types.PublishTemplateRequest{Version: "1.0.0"})
	require.Error(t, err, "publish an existing version")
	var apiErr *client.APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusConflict, apiErr.StatusCode)

	// 6. Publish 1.1.0.
	second, err := c.PublishTemplateRelease(tmpl.ID, &types.PublishTemplateRequest{Version: "1.1.0", ChangeSummary: "description"})
	require.NoError(t, err, "publish 1.1.0")
	require.NotNil(t, second.SnapshotCreated)
	assert.True(t, *second.SnapshotCreated)
	require.NotNil(t, second.PublishedVersion)
	assert.Equal(t, "1.1.0", *second.PublishedVersion)
	assert.False(t, second.HasUnpublishedChanges != nil && *second.HasUnpublishedChanges, "no unpublished changes after publish")

	versions, err := c.ListTemplateVersions(tmpl.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(versions), 2)
	for i, v := range versions {
		assert.NotEmpty(t, v.CreatedByName(), "versions[%d] must name the creator", i)
	}
}
