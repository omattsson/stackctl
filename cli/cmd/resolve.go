package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
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
			msg += fmt.Sprintf("  %s  (owner: %s, status: %s)\n", s.ID, s.Owner, s.Status)
		}
		return "", fmt.Errorf("%s", msg)
	}
}
