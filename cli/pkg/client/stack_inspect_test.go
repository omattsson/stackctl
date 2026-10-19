package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneRoute answers one method and path with body; other requests fail.
func oneRoute(t *testing.T, method, path, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method || r.URL.Path != path {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		data, _ := io.ReadAll(r.Body)
		assert.Empty(t, data, "no request body")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestFollowAndUnfollowStack(t *testing.T) {
	t.Parallel()
	st, err := oneRoute(t, http.MethodPost, "/api/v1/stack-instances/42/follow", `{"following":true,"follower_count":2}`).FollowStack("42")
	require.NoError(t, err)
	assert.True(t, st.Following)
	assert.Equal(t, int64(2), st.FollowerCount)

	st, err = oneRoute(t, http.MethodDelete, "/api/v1/stack-instances/42/follow", `{"following":false,"follower_count":1}`).UnfollowStack("42")
	require.NoError(t, err)
	assert.False(t, st.Following)
	assert.Equal(t, int64(1), st.FollowerCount)
}

func TestGetDeployPreview(t *testing.T) {
	t.Parallel()
	p, err := oneRoute(t, http.MethodGet, "/api/v1/stack-instances/42/deploy-preview",
		`{"instance_id":"42","values_drift":true,"warning":"w","charts":[{"chart_name":"api","pending_values":"a: 1","has_changes":true}]}`).GetDeployPreview("42")
	require.NoError(t, err)
	assert.True(t, p.ValuesDrift)
	require.Len(t, p.Charts, 1)
	assert.Equal(t, "a: 1", p.Charts[0].PendingValues)
}

func TestGetStackPodsAndRecent(t *testing.T) {
	t.Parallel()
	st, err := oneRoute(t, http.MethodGet, "/api/v1/stack-instances/42/pods",
		`{"namespace":"ns","charts":[{"chart_name":"api","pods":[{"name":"p1"}]}],"events":[{"type":"Warning","reason":"BackOff"}]}`).GetStackPods("42")
	require.NoError(t, err)
	assert.Equal(t, "p1", st.Charts[0].Pods[0].Name)
	assert.Equal(t, "BackOff", st.Events[0].Reason)

	items, err := oneRoute(t, http.MethodGet, "/api/v1/stack-instances/recent", `[{"id":"1"},{"id":"2"}]`).ListRecentStacks()
	require.NoError(t, err)
	assert.Len(t, items, 2)
}
