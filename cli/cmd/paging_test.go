package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file mutate package-level globals (printer, cfg) via
// setupStackTestCmd. Do not add t.Parallel() to them.

// pagedServer serves a list of ids under path with server-side paging:
// page and pageSize (default 25, max 100), and total. It records the query
// of every request.
type pagedServer struct {
	*httptest.Server
	mu      sync.Mutex
	queries []map[string]string
}

func newPagedServer(t *testing.T, path string, ids []string) *pagedServer {
	t.Helper()
	ps := &pagedServer{}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, path, r.URL.Path)
		q := map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		ps.mu.Lock()
		ps.queries = append(ps.queries, q)
		ps.mu.Unlock()

		page, pageSize := 1, 25
		if v, err := strconv.Atoi(q["page"]); err == nil && v > 0 {
			page = v
		}
		if v, err := strconv.Atoi(q["pageSize"]); err == nil && v > 0 {
			pageSize = min(v, 100)
		}
		start := min((page-1)*pageSize, len(ids))
		end := min(start+pageSize, len(ids))
		items := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			items = append(items, fmt.Sprintf(`{"id":%q,"name":"name-%s","owner_id":"u1","status":"running"}`, id, id))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s],"total":%d,"page":%d,"pageSize":%d}`, strings.Join(items, ","), len(ids), page, pageSize)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func makeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%03d", i+1)
	}
	return ids
}

// resetListFlags resets the shared list flags of cmd when the test ends.
func resetListFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	t.Cleanup(func() {
		if cmd.Flags().Lookup("mine") != nil {
			resetFlag(t, cmd.Flags(), "mine", "false")
		}
		resetFlag(t, cmd.Flags(), "page", "0")
		resetFlag(t, cmd.Flags(), flagPageSize, "0")
	})
}

func TestListCmds_FooterWhenMoreItems(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
		path string
	}{
		{name: "stack list", cmd: stackListCmd, path: "/api/v1/stack-instances"},
		{name: "definition list", cmd: definitionListCmd, path: "/api/v1/stack-definitions"},
		{name: "template list", cmd: templateListCmd, path: "/api/v1/templates"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := newPagedServer(t, tt.path, makeIDs(60))
			buf := setupStackTestCmd(t, server.URL)
			errBuf := captureStderr(t, tt.cmd)
			resetListFlags(t, tt.cmd)

			require.NoError(t, tt.cmd.RunE(tt.cmd, nil))
			assert.Equal(t, "Showing 25 of 60. Use --page/--page-size to see more.\n", errBuf.String())
			assert.NotContains(t, buf.String(), "Showing")
			assert.Contains(t, buf.String(), "id-025")
			assert.NotContains(t, buf.String(), "id-026")
		})
	}
}

func TestStackListCmd_NoFooterWhenAllShown(t *testing.T) {
	server := newPagedServer(t, "/api/v1/stack-instances", makeIDs(3))
	_ = setupStackTestCmd(t, server.URL)
	errBuf := captureStderr(t, stackListCmd)
	resetListFlags(t, stackListCmd)

	require.NoError(t, stackListCmd.RunE(stackListCmd, nil))
	assert.Empty(t, errBuf.String())
}

func TestStackListCmd_JSONUnchangedWithFooterOnStderr(t *testing.T) {
	server := newPagedServer(t, "/api/v1/stack-instances", makeIDs(30))
	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	errBuf := captureStderr(t, stackListCmd)
	resetListFlags(t, stackListCmd)

	require.NoError(t, stackListCmd.RunE(stackListCmd, nil))
	var got types.ListResponse[types.StackInstance]
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got), "stdout stays valid JSON")
	assert.Len(t, got.Data, 25)
	assert.Equal(t, 30, got.Total)
	assert.Len(t, server.queries, 1, "-o json requests one page")
	assert.Contains(t, errBuf.String(), "Showing 25 of 30.")
}

func TestStackListCmd_QuietFetchesAllPages(t *testing.T) {
	server := newPagedServer(t, "/api/v1/stack-instances", makeIDs(150))
	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	errBuf := captureStderr(t, stackListCmd)
	resetListFlags(t, stackListCmd)
	require.NoError(t, stackListCmd.Flags().Set("mine", "true"))

	require.NoError(t, stackListCmd.RunE(stackListCmd, nil))
	assert.Equal(t, strings.Join(makeIDs(150), "\n")+"\n", buf.String())
	assert.Empty(t, errBuf.String(), "no footer: all IDs are printed")
	require.Len(t, server.queries, 2)
	for i, q := range server.queries {
		assert.Equal(t, "me", q["owner"])
		assert.Equal(t, "100", q["pageSize"])
		assert.Equal(t, strconv.Itoa(i+1), q["page"])
	}
}

func TestDefinitionListCmd_QuietFetchesAllPagesWithPageSize(t *testing.T) {
	server := newPagedServer(t, "/api/v1/stack-definitions", makeIDs(5))
	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	resetListFlags(t, definitionListCmd)
	require.NoError(t, definitionListCmd.Flags().Set(flagPageSize, "2"))

	require.NoError(t, definitionListCmd.RunE(definitionListCmd, nil))
	assert.Equal(t, strings.Join(makeIDs(5), "\n")+"\n", buf.String())
	assert.Len(t, server.queries, 3, "pages of 2: 2 + 2 + 1")
}

func TestStackListCmd_QuietWithPageIsOnePage(t *testing.T) {
	server := newPagedServer(t, "/api/v1/stack-instances", makeIDs(60))
	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	errBuf := captureStderr(t, stackListCmd)
	resetListFlags(t, stackListCmd)
	require.NoError(t, stackListCmd.Flags().Set("page", "3"))

	require.NoError(t, stackListCmd.RunE(stackListCmd, nil))
	assert.Equal(t, strings.Join(makeIDs(60)[50:], "\n")+"\n", buf.String())
	assert.Len(t, server.queries, 1)
	assert.Equal(t, "Showing 10 of 60. Use --page/--page-size to see more.\n", errBuf.String())
}

func TestListAllIDs_StopConditions(t *testing.T) {
	t.Parallel()
	ids := func(n int, from int) []types.StackInstance {
		out := make([]types.StackInstance, n)
		for i := range out {
			out[i].ID = strconv.Itoa(from + i)
		}
		return out
	}
	tests := []struct {
		name      string
		pages     func(page int) *types.ListResponse[types.StackInstance]
		wantIDs   int
		wantCalls int
	}{
		{
			name: "older server returns every match on one page",
			pages: func(int) *types.ListResponse[types.StackInstance] {
				return &types.ListResponse[types.StackInstance]{Data: ids(130, 1), Total: 130, PageSize: 130}
			},
			wantIDs: 130, wantCalls: 1,
		},
		{
			name: "server ignores page",
			pages: func(int) *types.ListResponse[types.StackInstance] {
				return &types.ListResponse[types.StackInstance]{Data: ids(100, 1), Total: 300, PageSize: 100}
			},
			wantIDs: 100, wantCalls: 2,
		},
		{
			name: "short page ends the list",
			pages: func(page int) *types.ListResponse[types.StackInstance] {
				if page == 1 {
					return &types.ListResponse[types.StackInstance]{Data: ids(100, 1), Total: 500, PageSize: 100}
				}
				return &types.ListResponse[types.StackInstance]{Data: ids(7, 101), Total: 500, PageSize: 100}
			},
			wantIDs: 107, wantCalls: 2,
		},
		{
			name: "no total",
			pages: func(int) *types.ListResponse[types.StackInstance] {
				return &types.ListResponse[types.StackInstance]{Data: ids(3, 1)}
			},
			wantIDs: 3, wantCalls: 1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			list := func(q map[string]string) (*types.ListResponse[types.StackInstance], error) {
				calls++
				page, _ := strconv.Atoi(q["page"])
				return tt.pages(page), nil
			}
			got, err := listAllIDs(map[string]string{}, list, func(s types.StackInstance) string { return s.ID })
			require.NoError(t, err)
			assert.Len(t, got, tt.wantIDs)
			assert.Equal(t, tt.wantCalls, calls)
		})
	}
}

func TestResolveName_MatchCount(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		total   int
		resolve func(string) error
		want    string
		notWant string
	}{
		{
			name: "stack, more matches than shown", path: "/api/v1/stack-instances", total: 30,
			want: `multiple stacks match name "dup" (30 matches, first 2 shown)`,
		},
		{
			name: "stack, all shown", path: "/api/v1/stack-instances", total: 2,
			want: `multiple stacks match name "dup" — use the ID instead`, notWant: "matches,",
		},
		{
			name: "definition, more matches than shown", path: "/api/v1/stack-definitions", total: 40,
			want: `multiple definitions match name "dup" (40 matches, first 2 shown)`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tt.path, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"data":[{"id":"a1","name":"dup","owner_id":"u1"},{"id":"a2","name":"dup","owner_id":"u2"}],"total":%d,"page":1,"pageSize":2}`, tt.total)
			}))
			defer server.Close()

			_ = setupStackTestCmd(t, server.URL)
			c, err := newClient()
			require.NoError(t, err)
			if tt.path == "/api/v1/stack-instances" {
				_, err = resolveStackID(c, "dup")
			} else {
				_, err = resolveDefinitionID(c, "dup")
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			if tt.notWant != "" {
				assert.NotContains(t, err.Error(), tt.notWant)
			}
		})
	}
}
