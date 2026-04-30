package cmd

import (
	"fmt"
	"strconv"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage stack templates",
	Long:  "List, inspect, and instantiate reusable stack templates.",
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stack templates",
	Long: `List stack templates with optional filtering.

Examples:
  stackctl template list
  stackctl template list --published
  stackctl template list -o json
  stackctl template list -q | xargs -I{} stackctl template get {}`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		params := map[string]string{}

		if published, _ := cmd.Flags().GetBool("published"); published {
			params["published"] = "true"
		}
		if cmd.Flags().Changed("page") {
			page, _ := cmd.Flags().GetInt("page")
			if page > 0 {
				params["page"] = strconv.Itoa(page)
			}
		}
		if cmd.Flags().Changed(flagPageSize) {
			pageSize, _ := cmd.Flags().GetInt(flagPageSize)
			if pageSize > 0 {
				params["page_size"] = strconv.Itoa(pageSize)
			}
		}

		resp, err := c.ListTemplates(params)
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]string, len(resp.Data))
			for i, t := range resp.Data {
				ids[i] = t.ID
			}
			printer.PrintIDs(ids)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			headers := []string{"ID", "NAME", "DESCRIPTION", "PUBLISHED", "DEFINITIONS"}
			rows := make([][]string, len(resp.Data))
			for i, t := range resp.Data {
				published := "false"
				if t.Published {
					published = "true"
				}
				rows[i] = []string{
					t.ID,
					t.Name,
					t.Description,
					published,
					strconv.Itoa(t.DefinitionCount),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var templateGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show stack template details",
	Long: `Show detailed information about a stack template.

Examples:
  stackctl template get 1
  stackctl template get 1 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		tmpl, err := c.GetTemplate(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, tmpl.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(tmpl)
		case output.FormatYAML:
			return printer.PrintYAML(tmpl)
		default:
			published := "false"
			if tmpl.Published {
				published = "true"
			}
			fields := []output.KeyValue{
				{Key: "ID", Value: tmpl.ID},
				{Key: "Name", Value: tmpl.Name},
				{Key: "Description", Value: tmpl.Description},
				{Key: "Published", Value: published},
				{Key: "Owner", Value: tmpl.Owner},
			}
			for _, ch := range tmpl.Charts {
				fields = append(fields, output.KeyValue{
					Key:   "Chart",
					Value: fmt.Sprintf("%s (%s@%s)", ch.ChartName, ch.RepoURL, ch.ChartVersion),
				})
			}
			return printer.PrintSingle(tmpl, fields)
		}
	},
}

var templateInstantiateCmd = &cobra.Command{
	Use:   "instantiate <id>",
	Short: "Create a stack definition from a template",
	Long: `Create a new stack definition from a template.

Examples:
  stackctl template instantiate 1 --name my-stack
  stackctl template instantiate 1 --name my-stack --branch feature/xyz --cluster 2`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		branch, _ := cmd.Flags().GetString("branch")
		clusterID, _ := cmd.Flags().GetString("cluster")

		req := &types.InstantiateTemplateRequest{
			Name:      name,
			Branch:    branch,
			ClusterID: clusterID,
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		def, err := c.InstantiateTemplate(id, req)
		if err != nil {
			return err
		}

		return printDefinition(def)
	},
}

var templateQuickDeployCmd = &cobra.Command{
	Use:   "quick-deploy <id>",
	Short: "Create and deploy a stack instance from a template",
	Long: `Create and deploy a stack instance from a template in one step.

Examples:
  stackctl template quick-deploy 1 --name my-stack
  stackctl template quick-deploy 1 --name my-stack --branch feature/xyz --cluster 2`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		branch, _ := cmd.Flags().GetString("branch")
		clusterID, _ := cmd.Flags().GetString("cluster")

		req := &types.QuickDeployRequest{
			Name:      name,
			Branch:    branch,
			ClusterID: clusterID,
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		instance, err := c.QuickDeployTemplate(id, req)
		if err != nil {
			return err
		}

		return printInstance(instance)
	},
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a stack template",
	Long: `Permanently delete a stack template.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl template delete 1
  stackctl template delete 1 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will permanently delete template %s. Continue? (y/n): ",
			passthroughID,
			func(c *client.Client, id string) error { return c.DeleteTemplate(id) },
			"Deleted template %s",
		)
	},
}

func init() {
	// template list flags
	templateListCmd.Flags().Bool("published", false, "Show only published templates")
	templateListCmd.Flags().Int("page", 0, "Page number")
	templateListCmd.Flags().Int(flagPageSize, 0, "Page size")

	// template instantiate flags
	templateInstantiateCmd.Flags().String("name", "", "Stack definition name (required)")
	templateInstantiateCmd.Flags().String("branch", "", "Git branch")
	templateInstantiateCmd.Flags().String("cluster", "", "Target cluster ID")
	_ = templateInstantiateCmd.MarkFlagRequired("name")

	// template quick-deploy flags
	templateQuickDeployCmd.Flags().String("name", "", "Stack instance name (required)")
	templateQuickDeployCmd.Flags().String("branch", "", "Git branch")
	templateQuickDeployCmd.Flags().String("cluster", "", "Target cluster ID")
	_ = templateQuickDeployCmd.MarkFlagRequired("name")

	// template delete flags
	templateDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	templateDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// Wire up subcommands
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateGetCmd)
	templateCmd.AddCommand(templateInstantiateCmd)
	templateCmd.AddCommand(templateQuickDeployCmd)
	templateCmd.AddCommand(templateDeleteCmd)
	rootCmd.AddCommand(templateCmd)
}
