package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmallGapRoutes(t *testing.T) {
	t.Parallel()
	type call struct{ method, uri, body, auth string }
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got = append(got, call{r.Method, r.URL.RequestURI(), string(data), r.Header.Get("X-API-Key")})
		switch r.URL.Path {
		case "/api/v1/favorites/check":
			_, _ = io.WriteString(w, `{"is_favorite":true}`)
		case "/api/v1/dashboard":
			_, _ = io.WriteString(w, `{"failing_instances":[{"id":"f1"}]}`)
		case "/api/v1/auth/logout-all":
			_, _ = io.WriteString(w, `{"message":"ok"}`)
		default:
			_, _ = io.WriteString(w, `{"id":"x","branch":"b"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	c.APIKey = "sk_test"

	_, err := c.UpdateSharedValues("c1", "sv1", &types.UpdateSharedValuesRequest{Name: "n", Priority: 0})
	require.NoError(t, err)
	o, err := c.GetBranchOverride("42", "ch1")
	require.NoError(t, err)
	assert.Equal(t, "b", o.Branch)
	require.NoError(t, c.LogoutAll())
	d, err := c.GetDashboard()
	require.NoError(t, err)
	assert.Equal(t, "f1", d.FailingInstances[0].ID)
	fav, err := c.CheckFavorite("template", "t1")
	require.NoError(t, err)
	assert.True(t, fav)

	want := []call{
		{http.MethodPut, "/api/v1/clusters/c1/shared-values/sv1", `{"name":"n","description":"","values":"","priority":0}`, "sk_test"},
		{http.MethodGet, "/api/v1/stack-instances/42/branches/ch1", "", "sk_test"},
		{http.MethodPost, "/api/v1/auth/logout-all", "", "sk_test"},
		{http.MethodGet, "/api/v1/dashboard", "", "sk_test"},
		{http.MethodGet, "/api/v1/favorites/check?entity_id=t1&entity_type=template", "", "sk_test"},
	}
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, w.method, got[i].method, i)
		assert.Equal(t, w.uri, got[i].uri, i)
		assert.Equal(t, w.auth, got[i].auth, i)
		if w.body != "" {
			assert.JSONEq(t, w.body, got[i].body, i)
		}
	}
}
