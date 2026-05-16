package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

		return printTemplate(tmpl)
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

var templateCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new stack template",
	Long: `Create a new stack template from flags or a JSON file.

Examples:
  stackctl template create --name my-template --description "My template"
  stackctl template create --from-file template.json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString(flagFromFile)

		var req types.CreateTemplateRequest
		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return errors.New(msgPathTraversal)
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return readFileErr(fromFile, err)
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return fmt.Errorf("invalid JSON in file %s: %w", fromFile, err)
			}
			if req.Name == "" {
				return fmt.Errorf("'name' field is required in the template file")
			}
		} else {
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				return fmt.Errorf("--name is required (or use --from-file)")
			}
			description, _ := cmd.Flags().GetString("description")
			req = types.CreateTemplateRequest{
				Name:        name,
				Description: description,
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		tmpl, err := c.CreateTemplate(&req)
		if err != nil {
			return err
		}

		return printTemplate(tmpl)
	},
}

var templateUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a stack template",
	Long: `Update an existing stack template from flags or a JSON file.

Examples:
  stackctl template update 1 --name new-name
  stackctl template update 1 --description "Updated description"
  stackctl template update 1 --from-file template.json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		fromFile, _ := cmd.Flags().GetString(flagFromFile)
		name, _ := cmd.Flags().GetString("name")
		description, _ := cmd.Flags().GetString("description")

		if fromFile == "" && name == "" && description == "" {
			return fmt.Errorf("at least one of --name, --description, or --from-file must be specified")
		}

		var req types.UpdateTemplateRequest
		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return errors.New(msgPathTraversal)
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return readFileErr(fromFile, err)
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return fmt.Errorf("invalid JSON in file %s: %w", fromFile, err)
			}
		} else {
			if name != "" {
				req.Name = name
			}
			if description != "" {
				req.Description = description
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		tmpl, err := c.UpdateTemplate(id, &req)
		if err != nil {
			return err
		}

		return printTemplate(tmpl)
	},
}

var templateCloneCmd = &cobra.Command{
	Use:   "clone <id>",
	Short: "Clone a stack template",
	Long: `Clone an existing stack template with a new name.

Examples:
  stackctl template clone 1 --name my-clone
  stackctl template clone 1 --name my-clone -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			return fmt.Errorf("--name must not be empty")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		tmpl, err := c.CloneTemplate(id, &types.CloneTemplateRequest{Name: name})
		if err != nil {
			return err
		}

		return printTemplate(tmpl)
	},
}

var templatePublishCmd = &cobra.Command{
	Use:   "publish <id>",
	Short: "Publish a stack template",
	Long: `Publish a stack template to make it available for use.

Examples:
  stackctl template publish 1
  stackctl template publish 1 -o json`,
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

		tmpl, err := c.PublishTemplate(id)
		if err != nil {
			return err
		}

		return printTemplate(tmpl)
	},
}

var templateUnpublishCmd = &cobra.Command{
	Use:   "unpublish <id>",
	Short: "Unpublish a stack template",
	Long: `Unpublish a stack template to prevent new instantiations.

Examples:
  stackctl template unpublish 1
  stackctl template unpublish 1 -o json`,
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

		tmpl, err := c.UnpublishTemplate(id)
		if err != nil {
			return err
		}

		return printTemplate(tmpl)
	},
}

var templateVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Manage template version history",
	Long:  "List, inspect, and compare versioned snapshots of a stack template.",
}

var templateVersionsListCmd = &cobra.Command{
	Use:   "list <id>",
	Short: "List version history for a template",
	Long: `List all published versions of a stack template, newest first.

Examples:
  stackctl template versions list 1
  stackctl template versions list 1 -o json`,
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

		versions, err := c.ListTemplateVersions(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]string, len(versions))
			for i, v := range versions {
				ids[i] = v.ID
			}
			printer.PrintIDs(ids)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(versions)
		case output.FormatYAML:
			return printer.PrintYAML(versions)
		default:
			headers := []string{"ID", "VERSION", "CHANGE SUMMARY", "CREATED BY", "CREATED AT"}
			rows := make([][]string, len(versions))
			for i, v := range versions {
				rows[i] = []string{
					v.ID,
					v.Version,
					v.ChangeSummary,
					v.CreatedBy,
					v.CreatedAt.Format("2006-01-02 15:04"),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var templateVersionsGetCmd = &cobra.Command{
	Use:   "get <id> <version-id>",
	Short: "Show a specific template version",
	Long: `Show details of a specific template version snapshot.

The <version-id> is the UUID shown in the ID column of 'template versions list'.

Examples:
  stackctl template versions get 1 $(stackctl template versions list 1 -q | head -1)
  stackctl template versions get 1 <version-id> -o json`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		templateID, err := parseID(args[0])
		if err != nil {
			return err
		}
		versionID := args[1]

		c, err := newClient()
		if err != nil {
			return err
		}

		v, err := c.GetTemplateVersion(templateID, versionID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, v.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(v)
		case output.FormatYAML:
			return printer.PrintYAML(v)
		default:
			headers := []string{"FIELD", "VALUE"}
			rows := [][]string{
				{"ID", v.ID},
				{"Template ID", v.TemplateID},
				{"Version", v.Version},
				{"Change Summary", v.ChangeSummary},
				{"Created By", v.CreatedBy},
				{"Created At", v.CreatedAt.Format("2006-01-02 15:04")},
			}
			for _, ch := range v.Snapshot.Charts {
				rows = append(rows, []string{"Chart", ch.ChartName})
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var templateVersionsDiffCmd = &cobra.Command{
	Use:   "diff <id> <left-version-id> <right-version-id>",
	Short: "Compare two template versions",
	Long: `Compare two template version snapshots side by side.

The version IDs are the UUIDs shown in the ID column of 'template versions list'.
In table mode, shows a chart-level diff summary.
In JSON or YAML mode, returns the full structured diff.

Examples:
  stackctl template versions diff 1 <left-version-id> <right-version-id>
  stackctl template versions diff 1 <left-version-id> <right-version-id> -o json`,
	Args:         cobra.ExactArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		templateID, err := parseID(args[0])
		if err != nil {
			return err
		}
		leftID := args[1]
		rightID := args[2]

		c, err := newClient()
		if err != nil {
			return err
		}

		diff, err := c.DiffTemplateVersions(templateID, leftID, rightID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			// quiet mode prints chart names with differences, one per line.
			// For diff, chart names are the stable identifiers in this context.
			for _, ch := range diff.ChartDiffs {
				if ch.HasDifferences {
					fmt.Fprintln(printer.Writer, ch.ChartName)
				}
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(diff)
		case output.FormatYAML:
			return printer.PrintYAML(diff)
		default:
			fmt.Fprintf(printer.Writer, "Comparing %s -> %s\n\n", diff.Left.Version, diff.Right.Version)
			headers := []string{"CHART", "CHANGE", "REPO URL CHANGED", "VALUES CHANGED"}
			rows := make([][]string, len(diff.ChartDiffs))
			for i, ch := range diff.ChartDiffs {
				repoChanged := "no"
				if ch.LeftRepoURL != ch.RightRepoURL {
					repoChanged = "yes"
				}
				valuesChanged := "no"
				if ch.LeftValues != ch.RightValues {
					valuesChanged = "yes"
				}
				rows[i] = []string{
					ch.ChartName,
					printer.StatusColor(ch.ChangeType),
					repoChanged,
					valuesChanged,
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

// printTemplate outputs a StackTemplate in the active format.
func printTemplate(tmpl *types.StackTemplate) error {
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

	// template create flags
	templateCreateCmd.Flags().String("name", "", "Template name (required unless --from-file is used)")
	templateCreateCmd.Flags().String("description", "", "Template description")
	templateCreateCmd.Flags().String(flagFromFile, "", "Path to a JSON file containing the template definition")

	// template update flags
	templateUpdateCmd.Flags().String("name", "", "New template name")
	templateUpdateCmd.Flags().String("description", "", "New template description")
	templateUpdateCmd.Flags().String(flagFromFile, "", "Path to a JSON file with updated template fields")

	// template clone flags
	templateCloneCmd.Flags().String("name", "", "Name for the cloned template (required)")
	_ = templateCloneCmd.MarkFlagRequired("name")

	// Wire up subcommands
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateGetCmd)
	templateCmd.AddCommand(templateInstantiateCmd)
	templateCmd.AddCommand(templateQuickDeployCmd)
	templateCmd.AddCommand(templateDeleteCmd)
	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateUpdateCmd)
	templateCmd.AddCommand(templateCloneCmd)
	templateCmd.AddCommand(templatePublishCmd)
	templateCmd.AddCommand(templateUnpublishCmd)
	templateVersionsCmd.AddCommand(templateVersionsListCmd)
	templateVersionsCmd.AddCommand(templateVersionsGetCmd)
	templateVersionsCmd.AddCommand(templateVersionsDiffCmd)
	templateCmd.AddCommand(templateVersionsCmd)
	rootCmd.AddCommand(templateCmd)
}
