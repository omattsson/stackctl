package cmd

import (
	"fmt"
	"strconv"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// listPageSizeMax is the largest pageSize that the list endpoints accept
// (k8s-stack-manager handlers listPageSizeMax; larger values are capped).
const listPageSizeMax = 100

// maxListPages stops the page loop of quiet mode if a server never ends
// the list.
const maxListPages = 1000

// quietAllPages reports whether a list command prints the IDs of all pages:
// --quiet without an explicit --page.
func quietAllPages(cmd *cobra.Command) bool {
	return printer.Quiet && !cmd.Flags().Changed("page")
}

// printAllIDs prints the IDs of every page of a list, one per line. See
// listAllIDs.
func printAllIDs[T any](params map[string]string, list func(map[string]string) (*types.ListResponse[T], error), idOf func(T) string) error {
	ids, err := listAllIDs(params, list, idOf)
	if err != nil {
		return err
	}
	printer.PrintIDs(ids)
	return nil
}

// listAllIDs requests page 1, 2, ... of a list and returns the IDs of all
// items in server order, without duplicates. It uses the pageSize of params,
// else listPageSizeMax. It stops when it has total items, after the last
// page (total_pages), after a short or empty page, or when a page adds no new
// ID (a server that ignores page). Older servers that return every match on
// one page need one request.
func listAllIDs[T any](params map[string]string, list func(map[string]string) (*types.ListResponse[T], error), idOf func(T) string) ([]string, error) {
	query := make(map[string]string, len(params)+2)
	for k, v := range params {
		query[k] = v
	}
	if query["pageSize"] == "" {
		query["pageSize"] = strconv.Itoa(listPageSizeMax)
	}

	seen := map[string]struct{}{}
	var ids []string
	for page := 1; page <= maxListPages; page++ {
		query["page"] = strconv.Itoa(page)
		resp, err := list(query)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, item := range resp.Data {
			id := idOf(item)
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
			added++
		}
		if added == 0 ||
			len(ids) >= resp.Total ||
			(resp.TotalPages > 0 && page >= resp.TotalPages) ||
			(resp.PageSize > 0 && len(resp.Data) < resp.PageSize) {
			break
		}
	}
	return ids, nil
}

// printListFooter writes "Showing X of TOTAL" to stderr when the server has
// more items than the page shows. stderr keeps -q and -o json/yaml output
// clean for scripts.
func printListFooter(cmd *cobra.Command, shown, total int) {
	if total > shown {
		fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d of %d. Use --page/--page-size to see more.\n", shown, total)
	}
}
