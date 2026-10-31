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
		`{"namespace":"ns","charts":[{"chart_name":"api","pods":[{"name":"p1"}]}],"events":[{"type":"Warning","reason":"BackOff"}]}`).GetStackPods("42", false)
	require.NoError(t, err)
	assert.Equal(t, "p1", st.Charts[0].Pods[0].Name)
	assert.Equal(t, "BackOff", st.Events[0].Reason)

	items, err := oneRoute(t, http.MethodGet, "/api/v1/stack-instances/recent", `[{"id":"1"},{"id":"2"}]`).ListRecentStacks()
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestSanitizeLogText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a\tb\nc [2J d\n", SanitizeLogText("a\tb\r\nc\x1b[2J d\u202e\n"))
	assert.Equal(t, "ab\n", SanitizeLogText("a\u061Cb\n"), "the arabic letter mark is a bidi control")
}

func TestRunStackActionAndJobLog(t *testing.T) {
	t.Parallel()
	var gotBody, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotBody, gotPath, gotQuery = string(data), r.URL.EscapedPath(), r.URL.RawQuery
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"action":"a b","status_code":202,"job_id":"j1","result":{"x":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"log":"hi","next_offset":2,"done":true,"status":"succeeded"}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)

	res, err := c.RunStackAction("42", "a b", nil)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/stack-instances/42/actions/a%20b", gotPath)
	assert.JSONEq(t, `{}`, gotBody, "no parameters: an empty object")
	assert.Equal(t, "j1", res.JobID)
	assert.Equal(t, map[string]any{"x": float64(1)}, res.Result)

	_, err = c.RunStackAction("42", "a", map[string]any{"k": true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"parameters":{"k":true}}`, gotBody)

	chunk, err := c.GetActionJobLog("42", "a", "j.1", 7)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/stack-instances/42/actions/a/jobs/j.1/log", gotPath)
	assert.Equal(t, "offset=7", gotQuery)
	assert.True(t, chunk.Done)
}
