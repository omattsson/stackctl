package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- cluster shared-values update ----------

const (
	svCluster = "5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e"
	svPath    = "/api/v1/clusters/" + svCluster + "/shared-values"
	svList    = `[{"id":"sv1","cluster_id":"` + svCluster + `","name":"defaults","description":"base values","values":"persistence:\n  storageClass: slow\nreplicas: 1\n","priority":5}]`
)

func TestClusterSharedValuesUpdateCmd(t *testing.T) {
	file := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(file, []byte("image: new\n"), 0o600))
	tests := []struct {
		name     string
		arg      string
		flags    []string
		want     map[string]any
		wantNote bool
	}{
		{"set changes one key and keeps the rest", "defaults", []string{"set", "persistence.storageClass=fast"},
			map[string]any{"name": "defaults", "description": "base values", "priority": float64(5),
				"values": "persistence:\n    storageClass: fast\nreplicas: 1\n"}, true},
		{"file replaces the values", "defaults", []string{"file", file},
			map[string]any{"name": "defaults", "description": "base values", "priority": float64(5), "values": "image: new\n"}, false},
		{"priority and description only, ID in upper case", "SV1", []string{"priority", "0", "description", ""},
			map[string]any{"name": "defaults", "description": "", "priority": float64(0),
				"values": "persistence:\n  storageClass: slow\nreplicas: 1\n"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, out := setupChannelCmd(t, clusterSharedValuesUpdateCmd, map[string]route{
				"GET " + svPath:          {body: svList},
				"PUT " + svPath + "/sv1": {body: `{"id":"sv1","name":"defaults"}`},
			})
			setFlags(t, clusterSharedValuesUpdateCmd, tt.flags...)

			require.NoError(t, clusterSharedValuesUpdateCmd.RunE(clusterSharedValuesUpdateCmd, []string{svCluster, tt.arg}))
			assert.Equal(t, tt.want, rs.bodies["PUT "+svPath+"/sv1"])
			stdout, stderr := out()
			assert.Equal(t, `Updated shared values "defaults" (sv1) on cluster `+svCluster+"\n", stdout)
			assert.Equal(t, tt.wantNote, strings.Contains(stderr, "Note: --set writes the stored values again as YAML"))
		})
	}
}

func TestClusterSharedValuesUpdateCmd_Errors(t *testing.T) {
	dup := `[{"id":"a","name":"x"},{"id":"b","name":"x"}]`
	tests := []struct {
		name    string
		list    string
		arg     string
		flags   []string
		wantErr string
	}{
		{"nothing to update", svList, "defaults", nil, "nothing to update"},
		{"unknown entry", svList, "nope", []string{"priority", "1"}, `shared values "nope" not found on cluster`},
		{"duplicate name", dup, "x", []string{"priority", "1"}, `more than one shared values entry is named "x": use the ID`},
		{"bad set", svList, "defaults", []string{"set", "novalue"}, "novalue"},
		{"stored values are a list", `[{"id":"l","name":"list","values":"- a\n- b\n"}]`, "list", []string{"set", "x=1"},
			`the stored values of "list" are not a YAML mapping`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, _ := setupChannelCmd(t, clusterSharedValuesUpdateCmd, map[string]route{"GET " + svPath: {body: tt.list}})
			setFlags(t, clusterSharedValuesUpdateCmd, tt.flags...)
			err := clusterSharedValuesUpdateCmd.RunE(clusterSharedValuesUpdateCmd, []string{svCluster, tt.arg})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NotContains(t, strings.Join(rs.requests, ","), "PUT")
		})
	}
}

// ---------- override branch get ----------

func TestOverrideBranchGetCmd(t *testing.T) {
	const stackPath = "/api/v1/stack-instances/42"
	instance := `{"id":"42","name":"my-stack","stack_definition_id":"7"}`
	def := `{"id":"7","charts":[{"id":"c1","chart_name":"api"}]}`

	t.Run("override", func(t *testing.T) {
		_, out := setupChannelCmd(t, overrideBranchGetCmd, map[string]route{
			"GET " + stackPath:                  {body: instance},
			"GET /api/v1/stack-definitions/7":   {body: def},
			"GET " + stackPath + "/branches/c1": {body: `{"stack_instance_id":"42","chart_config_id":"c1","branch":"feature/x","updated_at":"2026-10-19T08:00:00Z"}`},
		})
		require.NoError(t, overrideBranchGetCmd.RunE(overrideBranchGetCmd, []string{"42", "api"}))
		stdout, _ := out()
		assert.Regexp(t, `Branch:\s+feature/x`, stdout)
	})
	t.Run("no override", func(t *testing.T) {
		_, _ = setupChannelCmd(t, overrideBranchGetCmd, map[string]route{
			"GET " + stackPath:                  {body: instance},
			"GET /api/v1/stack-definitions/7":   {body: def},
			"GET " + stackPath + "/branches/c1": {http.StatusNotFound, `{"error":"Branch override not found"}`},
		})
		err := overrideBranchGetCmd.RunE(overrideBranchGetCmd, []string{"42", "api"})
		require.Error(t, err)
		assert.Equal(t, "chart api on stack 42 has no branch override", err.Error())
	})
}

// ---------- logout --all ----------

func TestLogoutCmd_All(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("jwt-1", "refresh-1", "alice", time.Now().Add(time.Hour)))
	require.NoError(t, logoutCmd.Flags().Set("all", "true"))
	t.Cleanup(func() { resetFlag(t, logoutCmd.Flags(), "all", "false") })

	require.NoError(t, logoutCmd.RunE(logoutCmd, nil))
	assert.Equal(t, []string{"/api/v1/auth/logout-all Bearer jwt-1", "/api/v1/auth/logout Bearer jwt-1"}, paths)
	assert.Contains(t, buf.String(), "Ended all sessions of your user on the server")
	assert.Contains(t, buf.String(), "Logged out")
	assert.Nil(t, readStored(t), "token file removed")
}

func TestLogoutCmd_AllRenewsAnExpiredToken(t *testing.T) {
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/logout-all" && r.Header.Get("Authorization") == "Bearer jwt-1":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"token expired"}`))
		case r.URL.Path == "/api/v1/auth/refresh":
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-2", Path: "/api/v1/auth"})
			_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
		default:
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		}
	}))
	defer server.Close()

	_ = setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("jwt-1", "refresh-1", "alice", time.Now().Add(time.Hour)))
	require.NoError(t, logoutCmd.Flags().Set("all", "true"))
	t.Cleanup(func() { resetFlag(t, logoutCmd.Flags(), "all", "false") })

	require.NoError(t, logoutCmd.RunE(logoutCmd, nil))
	assert.Equal(t, []string{
		"/api/v1/auth/logout-all Bearer jwt-1",
		"/api/v1/auth/refresh ",
		"/api/v1/auth/logout-all Bearer " + newJWT,
		"/api/v1/auth/logout Bearer " + newJWT,
	}, calls)
	assert.Nil(t, readStored(t))
}

func TestLogoutCmd_AllCredentials(t *testing.T) {
	tests := []struct {
		name     string
		session  bool
		wantAuth string
	}{
		{"session wins over the API key", true, "Bearer jwt-1"},
		{"API key without a session", false, "key:sk_test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/logout-all" {
					gotAuth = r.Header.Get("Authorization")
					if k := r.Header.Get("X-API-Key"); k != "" {
						gotAuth = "key:" + k
					}
				}
				_, _ = w.Write([]byte(`{"message":"ok"}`))
			}))
			defer server.Close()

			_ = setupLoginTestCmd(t, server.URL)
			if tt.session {
				require.NoError(t, saveSession("jwt-1", "refresh-1", "alice", time.Now().Add(time.Hour)))
			}
			flagAPIKey = "sk_test"
			require.NoError(t, logoutCmd.Flags().Set("all", "true"))
			t.Cleanup(func() { resetFlag(t, logoutCmd.Flags(), "all", "false") })

			require.NoError(t, logoutCmd.RunE(logoutCmd, nil))
			assert.Equal(t, tt.wantAuth, gotAuth)
		})
	}
}

func TestLogoutCmd_AllFailsKeepsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/logout-all", r.URL.Path, "logout must not run after a failed logout-all")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"Internal server error"}`))
	}))
	defer server.Close()

	_ = setupLoginTestCmd(t, server.URL)
	require.NoError(t, saveSession("jwt-1", "refresh-1", "alice", time.Now().Add(time.Hour)))
	require.NoError(t, logoutCmd.Flags().Set("all", "true"))
	t.Cleanup(func() { resetFlag(t, logoutCmd.Flags(), "all", "false") })

	err := logoutCmd.RunE(logoutCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ending all sessions on the server: Server error")
	assert.NotNil(t, readStored(t), "the local token stays so the user can try again")
}

func TestLogoutCmd_AllNotLoggedIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s", r.URL.Path)
	}))
	defer server.Close()
	_ = setupLoginTestCmd(t, server.URL)
	require.NoError(t, logoutCmd.Flags().Set("all", "true"))
	t.Cleanup(func() { resetFlag(t, logoutCmd.Flags(), "all", "false") })

	err := logoutCmd.RunE(logoutCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not logged in")
}

// ---------- favorite check ----------

func TestFavoriteCheckCmd(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name   string
		body   string
		format output.Format
		want   string
	}{
		{"yes", `{"is_favorite":true}`, output.FormatTable, "yes\n"},
		{"no", `{"is_favorite":false}`, output.FormatTable, "no\n"},
		{"json", `{"is_favorite":true}`, output.FormatJSON, `"is_favorite": true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var query string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/favorites/check", r.URL.Path)
				query = r.URL.RawQuery
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			buf := setupStackTestCmd(t, server.URL)
			printer.Format = tt.format
			favoriteCheckType, favoriteCheckID = "instance", id
			t.Cleanup(ResetFavoriteFlagsForTest)

			require.NoError(t, favoriteCheckCmd.RunE(favoriteCheckCmd, nil))
			assert.Equal(t, "entity_id="+id+"&entity_type=instance", query)
			assert.Contains(t, buf.String(), tt.want)
		})
	}
}

func TestFavoriteCheckCmd_BadType(t *testing.T) {
	favoriteCheckType, favoriteCheckID = "cluster", "1"
	t.Cleanup(ResetFavoriteFlagsForTest)
	err := favoriteCheckCmd.RunE(favoriteCheckCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--type "cluster" is invalid`)
}

// ---------- dashboard ----------

const dashboardBody = `{
 "failing_instances":[{"id":"f1","name":"broken","status":"error","error_message":"helm failed\u001b[2J","updated_at":"2026-10-19T10:00:00Z"}],
 "expiring_soon":[{"id":"e1","name":"soon","status":"running","expires_at":"2026-10-15T12:30:00Z","ttl_minutes":60},
                  {"id":"f1","name":"broken","status":"error","expires_at":"2026-10-15T11:00:00Z","ttl_minutes":60}],
 "recent_deployments":[{"id":"d1","stack_instance_id":"e1","instance_name":"soon","action":"deploy","status":"success","started_at":"2026-10-15T11:00:00Z","owner_username":"alice"}],
 "clusters":[{"id":"c1","name":"dev","health_status":"healthy","node_count":3,"ready_node_count":2,"namespace_count":12}]}`

func TestDashboardCmd(t *testing.T) {
	useExpiryClock(t) // 2026-10-15T12:00Z
	t.Run("table", func(t *testing.T) {
		_, out := setupChannelCmd(t, dashboardCmd, map[string]route{"GET /api/v1/dashboard": {body: dashboardBody}})
		require.NoError(t, dashboardCmd.RunE(dashboardCmd, nil))
		stdout, _ := out()
		assert.Regexp(t, `Failing stacks:\n`, stdout)
		assert.Regexp(t, `f1\s+broken\s+error\s+helm failed \[2J`, stdout)
		assert.Regexp(t, `e1\s+soon\s+running\s+2026-10-15T12:30:00Z\s+30m`, stdout)
		assert.Regexp(t, `f1\s+broken\s+error\s+2026-10-15T11:00:00Z\s+expired`, stdout)
		assert.Regexp(t, `1h\s+soon\s+deploy\s+success\s+alice`, stdout)
		assert.Regexp(t, `dev\s+healthy\s+2/3\s+12`, stdout)
		assert.NotContains(t, stdout, "\x1b[2J")
	})
	t.Run("quiet lists unique IDs", func(t *testing.T) {
		_, out := setupChannelCmd(t, dashboardCmd, map[string]route{"GET /api/v1/dashboard": {body: dashboardBody}})
		printer.Quiet = true
		require.NoError(t, dashboardCmd.RunE(dashboardCmd, nil))
		stdout, _ := out()
		assert.Equal(t, "f1\ne1\n", stdout)
	})
	t.Run("empty for the role user: clusters without health data", func(t *testing.T) {
		_, out := setupChannelCmd(t, dashboardCmd, map[string]route{"GET /api/v1/dashboard": {body: `{"clusters":[{"id":"c1","name":"dev","health_status":""}],"recent_deployments":[],"expiring_soon":[],"failing_instances":[]}`}})
		require.NoError(t, dashboardCmd.RunE(dashboardCmd, nil))
		stdout, _ := out()
		assert.Equal(t, "Nothing to show: no failing or expiring stacks and no recent deployments.\n", stdout)
	})
	t.Run("json", func(t *testing.T) {
		_, out := setupChannelCmd(t, dashboardCmd, map[string]route{"GET /api/v1/dashboard": {body: dashboardBody}})
		printer.Format = output.FormatJSON
		require.NoError(t, dashboardCmd.RunE(dashboardCmd, nil))
		stdout, _ := out()
		var d types.Dashboard
		require.NoError(t, json.Unmarshal([]byte(stdout), &d))
		assert.Len(t, d.ExpiringSoon, 2)
		require.NotNil(t, d.Clusters[0].ReadyNodeCount)
	})
}
