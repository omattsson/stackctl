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

func TestNotificationChannelRoutes(t *testing.T) {
	t.Parallel()
	type call struct{ method, uri, body string }
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got = append(got, call{r.Method, r.URL.RequestURI(), string(data)})
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/admin/notification-channels" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `[{"id":"c1","subscription_count":3}]`)
		case r.URL.Path == "/api/v1/admin/notification-channels/c1/delivery-logs":
			_, _ = io.WriteString(w, `{"logs":[{"id":"l1"}],"total":1,"limit":5,"offset":10}`)
		case r.URL.Path == "/api/v1/admin/notification-channels/c1/test":
			_, _ = io.WriteString(w, `{"success":true,"status_code":200,"channel_enabled":true}`)
		case r.Method == http.MethodGet && (r.URL.Path == "/api/v1/admin/notification-channels/event-types" || r.URL.Path == "/api/v1/admin/notification-channels/c1/subscriptions"),
			r.Method == http.MethodPut && r.URL.Path == "/api/v1/admin/notification-channels/c1/subscriptions":
			_, _ = io.WriteString(w, `{"event_types":["a"]}`)
		default:
			_, _ = io.WriteString(w, `{"id":"c1","warnings":["w"]}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)

	list, err := c.ListNotificationChannels()
	require.NoError(t, err)
	require.NotNil(t, list[0].SubscriptionCount)
	assert.Equal(t, 3, *list[0].SubscriptionCount)
	_, err = c.GetNotificationChannel("c1")
	require.NoError(t, err)
	ch, err := c.CreateNotificationChannel(&types.CreateNotificationChannelRequest{Name: "n", WebhookURL: "https://h"})
	require.NoError(t, err)
	assert.Equal(t, []string{"w"}, ch.Warnings)
	empty := ""
	_, err = c.UpdateNotificationChannel("c1", &types.UpdateNotificationChannelRequest{Secret: &empty})
	require.NoError(t, err)
	require.NoError(t, c.DeleteNotificationChannel("c1"))
	ev, err := c.ListChannelEventTypes()
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ev)
	_, err = c.GetChannelSubscriptions("c1")
	require.NoError(t, err)
	_, err = c.SetChannelSubscriptions("c1", nil)
	require.NoError(t, err)
	res, err := c.TestNotificationChannel("c1")
	require.NoError(t, err)
	assert.True(t, res.Success)
	page, err := c.ListChannelDeliveryLogs("c1", 5, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Total)

	want := []call{
		{http.MethodGet, "/api/v1/admin/notification-channels", ""},
		{http.MethodGet, "/api/v1/admin/notification-channels/c1", ""},
		{http.MethodPost, "/api/v1/admin/notification-channels", `{"name":"n","webhook_url":"https://h"}`},
		{http.MethodPut, "/api/v1/admin/notification-channels/c1", `{"secret":""}`},
		{http.MethodDelete, "/api/v1/admin/notification-channels/c1", ""},
		{http.MethodGet, "/api/v1/admin/notification-channels/event-types", ""},
		{http.MethodGet, "/api/v1/admin/notification-channels/c1/subscriptions", ""},
		{http.MethodPut, "/api/v1/admin/notification-channels/c1/subscriptions", `{"event_types":[]}`},
		{http.MethodPost, "/api/v1/admin/notification-channels/c1/test", ""},
		{http.MethodGet, "/api/v1/admin/notification-channels/c1/delivery-logs?limit=5&offset=10", ""},
	}
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, w.method, got[i].method, i)
		assert.Equal(t, w.uri, got[i].uri, i)
		if w.body != "" {
			assert.JSONEq(t, w.body, got[i].body, i)
		}
	}
}
