package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func looksLikeID(s string) bool {
	if uuidRegex.MatchString(s) {
		return true
	}
	if _, err := strconv.Atoi(s); err == nil {
		return true
	}
	return false
}

func resolveStackID(c *client.Client, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("stack name or ID must not be empty")
	}

	if looksLikeID(nameOrID) {
		return nameOrID, nil
	}

	resp, err := c.ListStacks(map[string]string{"name": nameOrID})
	if err != nil {
		return "", fmt.Errorf("resolving stack name %q: %w", nameOrID, err)
	}

	switch len(resp.Data) {
	case 0:
		return "", fmt.Errorf("no stack found with name %q", nameOrID)
	case 1:
		if !strings.EqualFold(resp.Data[0].Name, nameOrID) {
			return "", fmt.Errorf("no stack found with name %q", nameOrID)
		}
		return resp.Data[0].ID, nil
	default:
		msg := fmt.Sprintf("multiple stacks match name %q — use the ID instead:\n", nameOrID)
		for _, s := range resp.Data {
			msg += fmt.Sprintf("  %s  (owner: %s, status: %s)\n", s.ID, displayName(s.OwnerUsername, s.Owner), s.Status)
		}
		return "", fmt.Errorf("%s", msg)
	}
}

func resolveDefinitionID(c *client.Client, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("definition name or ID must not be empty")
	}

	if looksLikeID(nameOrID) {
		return nameOrID, nil
	}

	resp, err := c.ListDefinitions(map[string]string{"name": nameOrID})
	if err != nil {
		return "", fmt.Errorf("resolving definition name %q: %w", nameOrID, err)
	}

	switch len(resp.Data) {
	case 0:
		return "", fmt.Errorf("no definition found with name %q", nameOrID)
	case 1:
		if !strings.EqualFold(resp.Data[0].Name, nameOrID) {
			return "", fmt.Errorf("no definition found with name %q", nameOrID)
		}
		return resp.Data[0].ID, nil
	default:
		msg := fmt.Sprintf("multiple definitions match name %q — use the ID instead:\n", nameOrID)
		for _, d := range resp.Data {
			msg += fmt.Sprintf("  %s  (owner: %s)\n", d.ID, displayName(d.OwnerUsername, d.Owner))
		}
		return "", fmt.Errorf("%s", msg)
	}
}

// templateNamePageSize is the page size that resolveTemplateID requests.
const templateNamePageSize = 100

// templateNameMaxPages caps the pages that resolveTemplateID reads.
const templateNameMaxPages = 100

// resolveTemplateID returns the ID of the template with the exact name
// nameOrID, or nameOrID itself when it looks like an ID. It sends ?name= to
// GET /api/v1/templates, but servers before k8s-stack-manager v0.6.0 ignore
// that filter and other servers can match more than the exact name, so it
// reads every page of the (filtered or full) list and matches the exact
// name on the client.
func resolveTemplateID(c *client.Client, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("template name or ID must not be empty")
	}

	if looksLikeID(nameOrID) {
		return nameOrID, nil
	}

	var matches []types.StackTemplate
	seen := make(map[string]bool)
	for page := 1; page <= templateNameMaxPages; page++ {
		resp, err := c.ListTemplates(map[string]string{
			"name":     nameOrID,
			"page":     strconv.Itoa(page),
			"pageSize": strconv.Itoa(templateNamePageSize),
		})
		if err != nil {
			return "", fmt.Errorf("resolving template name %q: %w", nameOrID, err)
		}
		newItems := 0
		for _, tmpl := range resp.Data {
			if seen[tmpl.ID] {
				continue
			}
			seen[tmpl.ID] = true
			newItems++
			if tmpl.Name == nameOrID {
				matches = append(matches, tmpl)
			}
		}
		// Stop at the last page. A page without new items means the server
		// ignores the page parameter.
		if newItems == 0 || len(resp.Data) < templateNamePageSize ||
			(resp.TotalPages > 0 && page >= resp.TotalPages) ||
			(resp.Total > 0 && len(seen) >= resp.Total) {
			break
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no template found with name %q", nameOrID)
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, len(matches))
		for i, tmpl := range matches {
			ids[i] = tmpl.ID
		}
		return "", fmt.Errorf("multiple templates named %q: %s; use the ID instead", nameOrID, strings.Join(ids, ", "))
	}
}

// stackCharts returns the chart configs of the definition of a stack
// instance, in deploy order (then by name).
func stackCharts(c *client.Client, instanceID string) ([]types.ChartConfig, error) {
	inst, err := c.GetStack(instanceID)
	if err != nil {
		return nil, err
	}
	def, err := c.GetDefinition(inst.StackDefinitionID)
	if err != nil {
		return nil, fmt.Errorf("reading definition %s of stack %s: %w", inst.StackDefinitionID, instanceID, err)
	}
	charts := append([]types.ChartConfig(nil), def.Charts...)
	sort.SliceStable(charts, func(i, j int) bool {
		if charts[i].DeployOrder != charts[j].DeployOrder {
			return charts[i].DeployOrder < charts[j].DeployOrder
		}
		return charts[i].ChartName < charts[j].ChartName
	})
	return charts, nil
}

// matchChart finds a chart by ID or by chart name (case-insensitive) in
// charts. scope names the owner of the charts for the error message.
func matchChart(charts []types.ChartConfig, nameOrID, scope string) (*types.ChartConfig, error) {
	for i := range charts {
		if charts[i].ID == nameOrID {
			return &charts[i], nil
		}
	}
	var matches []*types.ChartConfig
	for i := range charts {
		if strings.EqualFold(charts[i].ChartName, nameOrID) {
			matches = append(matches, &charts[i])
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		names := make([]string, len(charts))
		for i, ch := range charts {
			names[i] = ch.ChartName
		}
		available := "none"
		if len(names) > 0 {
			available = strings.Join(names, ", ")
		}
		return nil, fmt.Errorf("chart %q is not part of %s (charts: %s)", nameOrID, scope, available)
	default:
		msg := fmt.Sprintf("multiple charts in %s match name %q — use the ID instead:\n", scope, nameOrID)
		for _, ch := range matches {
			msg += fmt.Sprintf("  %s  %s\n", ch.ID, ch.ChartName)
		}
		return nil, fmt.Errorf("%s", msg)
	}
}

// resolveChartID resolves a chart name or ID within the definition of a
// stack instance: it matches a chart ID first, then a chart name. An
// unknown name is refused. An unknown UUID is passed through, so the API
// can answer for it (an override of a chart that was removed from the
// definition can still be deleted).
func resolveChartID(c *client.Client, instanceID, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("chart name or ID must not be empty")
	}
	charts, err := stackCharts(c, instanceID)
	if err != nil {
		return "", fmt.Errorf("resolving chart %q: %w", nameOrID, err)
	}
	return pickChartID(charts, nameOrID, "the definition of stack "+instanceID)
}

// resolveDefinitionChartID resolves a chart name or ID within a stack
// definition, like resolveChartID.
func resolveDefinitionChartID(c *client.Client, defID, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("chart name or ID must not be empty")
	}
	def, err := c.GetDefinition(defID)
	if err != nil {
		return "", fmt.Errorf("resolving chart %q: %w", nameOrID, err)
	}
	return pickChartID(def.Charts, nameOrID, "definition "+defID)
}

// pickChartID returns the ID of the chart that matches nameOrID (ID first,
// then name). A UUID that matches no chart is returned unchanged.
func pickChartID(charts []types.ChartConfig, nameOrID, scope string) (string, error) {
	ch, err := matchChart(charts, nameOrID, scope)
	if err == nil {
		return ch.ID, nil
	}
	if uuidRegex.MatchString(nameOrID) {
		return nameOrID, nil
	}
	return "", err
}
