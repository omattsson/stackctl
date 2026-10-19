package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawBody returns the decoded JSON body of r as a map, or nil for an empty body.
func rawBody(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	if len(data) == 0 {
		return nil
	}
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestUpdateStack_SendsOnlyGivenFields(t *testing.T) {
	t.Parallel()
	var got map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42", r.URL.Path)
		got = rawBody(t, r)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}, Name: "new-name"})
	}))
	defer server.Close()

	name := "new-name"
	ttl := 0
	inst, err := New(server.URL).UpdateStack("42", &types.UpdateStackRequest{Name: &name, TTLMinutes: &ttl})
	require.NoError(t, err)
	assert.Equal(t, "new-name", inst.Name)
	assert.Equal(t, map[string]interface{}{"name": "new-name", "ttl_minutes": float64(0)}, got,
		"ttl_minutes 0 must be sent (it clears the expiry); unset fields must not be sent")
}

func TestCloneStackWith_Body(t *testing.T) {
	t.Parallel()
	stop := time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		req  *types.CloneStackRequest
		want map[string]interface{}
	}{
		{"nil request sends no body", nil, nil},
		{"stop time", &types.CloneStackRequest{ExpiresAt: &stop}, map[string]interface{}{"expires_at": "2026-10-17T16:00:00Z"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/stack-instances/42/clone", r.URL.Path)
				got = rawBody(t, r)
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "55"}})
			}))
			defer server.Close()

			inst, err := New(server.URL).CloneStackWith("42", tt.req)
			require.NoError(t, err)
			assert.Equal(t, "55", inst.ID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtendStackUntil_SendsUTC(t *testing.T) {
	t.Parallel()
	var got map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/stack-instances/42/extend", r.URL.Path)
		got = rawBody(t, r)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}})
	}))
	defer server.Close()

	until := time.Date(2026, 10, 17, 18, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	_, err := New(server.URL).ExtendStackUntil("42", until)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"until": "2026-10-17T16:00:00Z"}, got)
}

func TestQuickDeployTemplate_ResponseShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"wrapped (current server)", `{"instance":{"id":"77","name":"qd"},"definition":{"id":"9"},"log_id":"l1"}`},
		{"flat instance", `{"id":"77","name":"qd"}`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			inst, err := New(server.URL).QuickDeployTemplate("10", &types.QuickDeployRequest{Name: "qd"})
			require.NoError(t, err)
			assert.Equal(t, "77", inst.ID)
			assert.Equal(t, "qd", inst.Name)
		})
	}
}

func TestQuickDeployTemplate_SendsExpiry(t *testing.T) {
	t.Parallel()
	var got map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = rawBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"instance":{"id":"77"}}`)
	}))
	defer server.Close()

	ttl := 0
	_, err := New(server.URL).QuickDeployTemplate("10", &types.QuickDeployRequest{Name: "qd", TTLMinutes: &ttl})
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"instance_name": "qd", "ttl_minutes": float64(0)}, got)
}

func TestGetUIConfigAndPing(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"), "public routes need no credentials")
		switch r.URL.Path {
		case "/api/v1/ui-config":
			_, _ = io.WriteString(w, `{"title":"K8s Stack Manager","default_instance_ttl_minutes":480,"max_instance_ttl_days":14}`)
		case "/api/v1/ping":
			_, _ = io.WriteString(w, `{"message":"pong"}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	require.NoError(t, c.Ping())
	cfg, err := c.GetUIConfig()
	require.NoError(t, err)
	assert.Equal(t, types.UIConfig{Title: "K8s Stack Manager", DefaultInstanceTTLMinutes: 480, MaxInstanceTTLDays: 14}, *cfg)
}

func TestConflictWithSuggestions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "name conflict with message and suggestions",
			body: `{"error":"instance name already exists","message":"A stack named \"demo\" already exists on this cluster (owner: alice)","suggestions":["demo-2","demo-3"]}`,
			want: `Conflict: A stack named "demo" already exists on this cluster (owner: alice). Free names: demo-2, demo-3`,
		},
		{
			name: "plain conflict",
			body: `{"error":"Template has no published version"}`,
			want: "Conflict: Template has no published version",
		},
		{
			name: "at most five sanitized suggestions",
			body: `{"error":"x","message":"taken","suggestions":["a\u001b[31m","", "b","c","d","e","f"]}`,
			want: "Conflict: taken. Free names: a [31m, b, c, d, e",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			name := "demo"
			_, err := New(server.URL).UpdateStack("42", &types.UpdateStackRequest{Name: &name})
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
		})
	}
}

func TestQuickDeployTemplate_NullInstance(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"instance":null,"log_id":"l1"}`)
	}))
	defer server.Close()

	_, err := New(server.URL).QuickDeployTemplate("10", &types.QuickDeployRequest{Name: "qd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the instance is null")
}
