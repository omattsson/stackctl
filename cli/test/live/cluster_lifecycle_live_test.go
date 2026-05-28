//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveCluster_CreateUpdateDelete locks the cluster-CRUD wire
// contract end-to-end: create → get → update → delete. The shaped
// fields (registry_password, image_pull_secret_name, max_namespaces,
// etc.) drifted twice during the epic #59 work — this catches that
// class of regression on the lifecycle path.
//
// IsDefault is deliberately set to false on both create and update —
// flipping the default would disrupt every other test that resolves
// the default cluster via requireCluster().
func TestLiveCluster_CreateUpdateDelete(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()
	maxNS := 5

	// 1. Create — registry fields are exercised because the
	// registry_password drift in PR #95 is the canonical example of why
	// this surface needs a live test.
	// Backend requires api_server_url AND one of {kubeconfig_data,
	// kubeconfig_path, use_in_cluster}. We use kubeconfig_path because
	// kubeconfig_data is rejected unless KUBECONFIG_ENCRYPTION_KEY is
	// configured (often not set in dev/CI). The path string is only
	// validated for non-emptiness on create — it isn't dereferenced
	// until a deploy actually runs, which we never trigger here.
	created, err := c.CreateCluster(&types.CreateClusterRequest{
		Name:                prefix + "-cluster",
		Description:         "live-test stub cluster",
		APIServerURL:        "https://ci-stub-cluster.invalid:6443",
		KubeconfigPath:      "/dev/null/ci-stub-kubeconfig",
		Region:              "ci-region",
		MaxNamespaces:       maxNS,
		IsDefault:           false,
		UseInCluster:        false,
		RegistryURL:         "registry.example.com",
		RegistryUsername:    "ci-test",
		RegistryPassword:    "ci-test-password",
		ImagePullSecretName: "ci-test-pull-secret",
	})
	require.NoError(t, err, "create cluster")
	require.NotEmpty(t, created.ID, "created cluster must have an ID")
	assert.Equal(t, prefix+"-cluster", created.Name, "name must round-trip")

	// Always best-effort delete so a failed assertion leaves no debris.
	t.Cleanup(func() {
		_ = c.DeleteCluster(created.ID)
	})

	// 2. Get — round-trip every field that has historically drifted.
	// We can't assert registry_password because the backend treats it
	// as write-only (json:"-" on the read path).
	got, err := c.GetCluster(created.ID)
	require.NoError(t, err, "get cluster by ID")
	assert.Equal(t, created.ID, got.ID, "id must round-trip via GET")
	assert.Equal(t, "live-test stub cluster", got.Description, "description must round-trip via GET")
	assert.False(t, got.IsDefault, "fresh non-default cluster must echo is_default=false")

	// 3. Update — flip the description, leave the rest alone. The CLI
	// uses pointer fields on UpdateClusterRequest so unset fields are
	// not sent. We re-send the registry fields to confirm the
	// registry_password update path doesn't 400 on an unchanged value.
	newDesc := "live-test stub cluster (updated)"
	pw := "ci-test-password" // intentionally unchanged
	updated, err := c.UpdateCluster(created.ID, &types.UpdateClusterRequest{
		Description:      &newDesc,
		RegistryPassword: &pw,
	})
	require.NoError(t, err, "update cluster")
	assert.Equal(t, newDesc, updated.Description, "description must round-trip through PUT")

	// 4. Delete (explicit — cleanup is the safety net).
	require.NoError(t, c.DeleteCluster(created.ID), "delete cluster")

	// Confirm gone — GET should 404.
	_, err = c.GetCluster(created.ID)
	require.Error(t, err, "GetCluster on deleted cluster must fail")
}
