package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/pmezard/go-difflib/difflib"
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
				params["pageSize"] = strconv.Itoa(pageSize)
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

For devops and admin users the fields and charts are the working copy
(draft). Users get the released version: the latest version that
'template publish' created. With k8s-stack-manager v0.6.0 or later the
output also shows:

  Working copy version  the version of the working copy
  Released version      the version users get ("none" before the first publish)
  Unpublished changes   "yes" when the working copy differs from the release

Each chart row is a chart of the working copy or the release. A note in
brackets shows how it differs, for example "[changed since release: chart
version 1.2.0 -> 1.3.0]", "[not in release]" or "[in release only]".
Use --released to list the released charts instead.

Examples:
  stackctl template get 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f
  stackctl template get 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --released
  stackctl template get 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f -o json`,
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

		released, _ := cmd.Flags().GetBool("released")
		return printTemplateDetail(tmpl, released)
	},
}

var templateInstantiateCmd = &cobra.Command{
	Use:   "instantiate <id>",
	Short: "Create a stack definition from a template",
	Long: `Create a new stack definition from a template.

The definition uses the released version of the template (the latest
'template publish'), not the working copy. A template without a released
version cannot be instantiated.

Examples:
  stackctl template instantiate 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name my-stack
  stackctl template instantiate 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name my-stack --branch feature/xyz --cluster 5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e`,
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
			return templateReleaseError(err, c, id, args[0])
		}

		return printDefinition(def)
	},
}

var templateQuickDeployCmd = &cobra.Command{
	Use:   "quick-deploy <id>",
	Short: "Create and deploy a stack instance from a template",
	Long: `Create and deploy a stack instance from a template in one step.

The stack uses the released version of the template (the latest
'template publish'), not the working copy. A template without a released
version cannot be deployed.

Examples:
  stackctl template quick-deploy 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name my-stack
  stackctl template quick-deploy 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name my-stack --branch feature/xyz --cluster 5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e`,
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
			return templateReleaseError(err, c, id, args[0])
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
  stackctl template delete 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f
  stackctl template delete 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --yes`,
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

The template is a working copy (draft). Run 'template publish' to release
it to users.

Examples:
  stackctl template create --name my-template --description "My template"
  stackctl template create --name my-template --version 1.0.0
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
		if cmd.Flags().Changed("version") {
			version, _ := cmd.Flags().GetString("version")
			req.Version = strings.TrimSpace(version)
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
	Long: `Update the working copy (draft) of a stack template from flags or a JSON file.

The command reads the template first. With k8s-stack-manager v0.6.0 or later
it sends only the fields you change. Older servers replace every field, so
the command then sends all fields with your changes. Fields you do not
change keep their values. A JSON file needs only the fields to change.
--description "" clears the description.

Users keep the released version until you run 'template publish'.

Examples:
  stackctl template update 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name new-name
  stackctl template update 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --description "Updated description"
  stackctl template update 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --version 1.1.0
  stackctl template update 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --from-file template.json`,
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
		version, _ := cmd.Flags().GetString("version")
		nameChanged := cmd.Flags().Changed("name")
		descriptionChanged := cmd.Flags().Changed("description")
		versionChanged := cmd.Flags().Changed("version")
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)

		if fromFile == "" && !nameChanged && !descriptionChanged && !versionChanged {
			return fmt.Errorf("at least one of --name, --description, --version, or --from-file must be specified")
		}
		if nameChanged && name == "" {
			return fmt.Errorf("--name must not be empty")
		}
		if versionChanged && version == "" {
			return fmt.Errorf("--version must not be empty")
		}

		var fileData []byte
		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return errors.New(msgPathTraversal)
				}
			}
			fromFile = filepath.Clean(fromFile)
			fileData, err = os.ReadFile(fromFile)
			if err != nil {
				return readFileErr(fromFile, err)
			}
			if !json.Valid(fileData) {
				return fmt.Errorf("invalid JSON in file %s", fromFile)
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		current, err := c.GetTemplate(id)
		if err != nil {
			return err
		}

		var tmpl *types.StackTemplate
		if current.HasUnpublishedChanges != nil {
			// v0.6.0+: the server changes only the fields that are sent.
			var patch types.PatchTemplateRequest
			if fileData != nil {
				if err := json.Unmarshal(fileData, &patch); err != nil {
					return fmt.Errorf("invalid JSON in file %s: %w", fromFile, err)
				}
			}
			if nameChanged {
				patch.Name = &name
			}
			if descriptionChanged {
				patch.Description = &description
			}
			if versionChanged {
				patch.Version = &version
			}
			tmpl, err = c.PatchTemplate(id, &patch)
		} else {
			// Older server: it replaces every field, so send the full record.
			req := types.UpdateTemplateRequest{
				Name:          current.Name,
				Description:   current.Description,
				Category:      current.Category,
				Version:       current.Version,
				DefaultBranch: current.DefaultBranch,
			}
			if fileData != nil {
				if err := json.Unmarshal(fileData, &req); err != nil {
					return fmt.Errorf("invalid JSON in file %s: %w", fromFile, err)
				}
			}
			if nameChanged {
				req.Name = name
			}
			if descriptionChanged {
				// An empty description is omitted; the older server then clears it.
				req.Description = description
			}
			if versionChanged {
				req.Version = version
			}
			tmpl, err = c.UpdateTemplate(id, &req)
		}
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
  stackctl template clone 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name my-clone
  stackctl template clone 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f --name my-clone -o json`,
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
	Use:   "publish <name|id>",
	Short: "Release the working copy of a stack template",
	Long: `Release the working copy (draft) of a stack template to users.

Publish stores the template fields and charts as a new version. Quick
deploy, instantiate and definition upgrades use the latest released version.
Edits with 'template update' and 'template update-chart' change only the
working copy until the next publish.

--version sets the version of the new release (for example 1.2.0). Without
--version the server uses the version of the working copy. Each version
can be released only once. When the working copy equals the latest release,
no new version is created.

--version and --change-summary need k8s-stack-manager v0.6.0 or later. The
command reads the template first and stops before the publish when the
server is older.

Examples:
  stackctl template publish my-template --version 1.2.0
  stackctl template publish my-template --version 1.2.0 --change-summary "Chart app-core 0.3.7"
  stackctl template publish 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f
  stackctl template publish my-template -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		version, _ := cmd.Flags().GetString("version")
		summary, _ := cmd.Flags().GetString("change-summary")
		versionChanged := cmd.Flags().Changed("version")
		summaryChanged := cmd.Flags().Changed("change-summary")
		version = strings.TrimSpace(version)
		if versionChanged && version == "" {
			return fmt.Errorf("--version must not be empty")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveTemplateID(c, args[0])
		if err != nil {
			return err
		}

		var req *types.PublishTemplateRequest
		if versionChanged || summaryChanged {
			// An older server ignores the body and publishes without a
			// version: check the server before the release, which cannot
			// be undone.
			current, err := c.GetTemplate(id)
			if err != nil {
				return err
			}
			if current.HasUnpublishedChanges == nil {
				return fmt.Errorf("--version and --change-summary need k8s-stack-manager v0.6.0 or later; nothing was published. Upgrade the server, or publish without these flags")
			}
			req = &types.PublishTemplateRequest{Version: version, ChangeSummary: summary}
		}

		resp, err := c.PublishTemplateRelease(id, req)
		if err != nil {
			return publishError(err, args[0], version)
		}

		if err := printPublishResult(resp); err != nil {
			return err
		}
		// An older server ignores the body and publishes without a release.
		if req != nil && resp.SnapshotCreated == nil {
			return fmt.Errorf("the server published the template but ignored --version and --change-summary; upgrade k8s-stack-manager to v0.6.0 or later")
		}
		return nil
	},
}

// printPublishResult prints the response of 'template publish'.
func printPublishResult(resp *types.PublishTemplateResponse) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, resp.ID)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(resp)
	case output.FormatYAML:
		return printer.PrintYAML(resp)
	default:
		released := ""
		if resp.PublishedVersion != nil {
			released = *resp.PublishedVersion
		}
		if released == "" || resp.SnapshotCreated == nil {
			// Older server: the response is the template only.
			return printTemplate(&resp.StackTemplate)
		}
		if !*resp.SnapshotCreated {
			printer.PrintMessage("No changes since version %s; no new version created", released)
			return nil
		}
		printer.PrintMessage("Published version %s", released)
		return nil
	}
}

var templateUnpublishCmd = &cobra.Command{
	Use:   "unpublish <id>",
	Short: "Unpublish a stack template",
	Long: `Unpublish a stack template to prevent new instantiations.

Examples:
  stackctl template unpublish 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f
  stackctl template unpublish 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f -o json`,
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
  stackctl template versions list 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f
  stackctl template versions list 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f -o json`,
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
					v.CreatedByName(),
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
  stackctl template versions get 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f $(stackctl template versions list 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f -q | head -1)
  stackctl template versions get 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f <version-id> -o json`,
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
				{"Created By", v.CreatedByName()},
				{"Created At", v.CreatedAt.Format("2006-01-02 15:04")},
			}
			for _, ch := range v.Snapshot.Charts {
				label := ch.ChartName
				if ch.ChartVersion != "" {
					label = fmt.Sprintf("%s (%s)", ch.ChartName, ch.ChartVersion)
				}
				rows = append(rows, []string{"Chart", label})
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var templateVersionsDiffCmd = &cobra.Command{
	Use:   "diff <id> <left-version-id|working> <right-version-id|working>",
	Short: "Compare two template versions",
	Long: `Compare two template version snapshots side by side.

The version IDs are the UUIDs shown in the ID column of 'template versions list'.
Use "working" for the working copy (draft) to see the unpublished changes
(needs k8s-stack-manager v0.6.0 or later).
In table mode, shows a chart-level diff summary.
In JSON or YAML mode, returns the full structured diff.

Examples:
  stackctl template versions diff 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f <left-version-id> <right-version-id>
  stackctl template versions diff 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f $(stackctl template versions list 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f -q | head -1) working
  stackctl template versions diff 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f <left-version-id> <right-version-id> -o json`,
	Args:         cobra.ExactArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		templateID, err := parseID(args[0])
		if err != nil {
			return err
		}
		leftID, err := parseVersionRef(args[1])
		if err != nil {
			return err
		}
		rightID, err := parseVersionRef(args[2])
		if err != nil {
			return err
		}

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
			leftLabel := diffSideLabel(diff.Left)
			rightLabel := diffSideLabel(diff.Right)
			fmt.Fprintf(printer.Writer, "Comparing %s -> %s\n\n", leftLabel, rightLabel)
			headers := []string{"CHART", "CHANGE", "REPO URL CHANGED", "VALUES CHANGED", "CHART VERSION"}
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
					chartVersionChange(ch.LeftChartVersion, ch.RightChartVersion),
				}
			}
			if err := printer.PrintTable(headers, rows); err != nil {
				return err
			}
			// Render per-chart unified text diff for charts with values changes.
			for _, ch := range diff.ChartDiffs {
				if ch.LeftValues == ch.RightValues {
					continue
				}
				ud := difflib.UnifiedDiff{
					A:        difflib.SplitLines(ch.LeftValues),
					B:        difflib.SplitLines(ch.RightValues),
					FromFile: fmt.Sprintf("%s (%s)", ch.ChartName, leftLabel),
					ToFile:   fmt.Sprintf("%s (%s)", ch.ChartName, rightLabel),
					Context:  3,
				}
				text, err := difflib.GetUnifiedDiffString(ud)
				if err != nil || text == "" {
					continue
				}
				fmt.Fprintf(printer.Writer, "\n%s", text)
			}
			return nil
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
		fields := templateFields(tmpl)
		for _, ch := range tmpl.Charts {
			fields = append(fields, output.KeyValue{Key: "Chart", Value: chartLabel(ch)})
		}
		return printer.PrintSingle(tmpl, fields)
	}
}

// printTemplateDetail prints a template for 'template get'. In table mode it
// adds the release state (k8s-stack-manager v0.6.0+) and marks how each
// working-copy chart differs from the release. With released set it lists
// the released charts instead.
func printTemplateDetail(tmpl *types.StackTemplate, released bool) error {
	if printer.Quiet || printer.Format == output.FormatJSON || printer.Format == output.FormatYAML {
		return printTemplate(tmpl)
	}

	hasReleaseInfo := tmpl.HasUnpublishedChanges != nil
	if released && !hasReleaseInfo {
		return fmt.Errorf("the server does not report released charts; --released needs k8s-stack-manager v0.6.0 or later")
	}

	fields := templateFields(tmpl)
	if hasReleaseInfo {
		releasedVersion := "none"
		if tmpl.PublishedVersion != nil && *tmpl.PublishedVersion != "" {
			releasedVersion = *tmpl.PublishedVersion
			if !tmpl.Published {
				releasedVersion += " (unpublished; quick deploy and use are blocked until published)"
			}
		}
		unpublished := "no"
		if *tmpl.HasUnpublishedChanges {
			unpublished = "yes"
		}
		fields = append(fields,
			output.KeyValue{Key: "Released version", Value: releasedVersion},
			output.KeyValue{Key: "Unpublished changes", Value: unpublished},
		)
	}

	switch {
	case released:
		if len(tmpl.PublishedCharts) == 0 {
			fields = append(fields, output.KeyValue{Key: "Released charts", Value: "none"})
		}
		for _, ch := range tmpl.PublishedCharts {
			fields = append(fields, output.KeyValue{Key: "Released chart", Value: chartLabel(ch)})
		}
	case hasReleaseInfo && tmpl.PublishedVersion != nil:
		fields = append(fields, compareTemplateCharts(tmpl.Charts, tmpl.PublishedCharts)...)
	default:
		for _, ch := range tmpl.Charts {
			fields = append(fields, output.KeyValue{Key: "Chart", Value: chartLabel(ch)})
		}
	}
	return printer.PrintSingle(tmpl, fields)
}

// templateFields returns the table rows of the template fields.
func templateFields(tmpl *types.StackTemplate) []output.KeyValue {
	published := "false"
	if tmpl.Published {
		published = "true"
	}
	fields := []output.KeyValue{
		{Key: "ID", Value: tmpl.ID},
		{Key: "Name", Value: tmpl.Name},
		{Key: "Description", Value: tmpl.Description},
	}
	if tmpl.Version != "" {
		// Managers (v0.6.0+ sends has_unpublished_changes) see the working
		// copy; other users get the released version from the server.
		label := "Version"
		if tmpl.HasUnpublishedChanges != nil {
			label = "Working copy version"
		}
		fields = append(fields, output.KeyValue{Key: label, Value: tmpl.Version})
	}
	fields = append(fields,
		output.KeyValue{Key: "Published", Value: published},
		output.KeyValue{Key: "Owner", Value: displayNameWithID(tmpl.OwnerUsername, tmpl.Owner)},
	)
	return fields
}

// chartLabel is the compact one-line form of a chart: name (repo@version).
func chartLabel(ch types.ChartConfig) string {
	return fmt.Sprintf("%s (%s@%s)", ch.ChartName, ch.RepoURL, ch.ChartVersion)
}

// compareTemplateCharts returns one "Chart" row per chart of the working copy
// and the release, matched by chart name. A note in brackets shows how the
// working copy differs from the release.
func compareTemplateCharts(working, released []types.ChartConfig) []output.KeyValue {
	releasedByName := make(map[string]types.ChartConfig, len(released))
	for _, ch := range released {
		releasedByName[ch.ChartName] = ch
	}
	seen := make(map[string]bool, len(working))
	rows := make([]output.KeyValue, 0, len(working)+len(released))
	for _, ch := range working {
		seen[ch.ChartName] = true
		label := chartLabel(ch)
		rel, ok := releasedByName[ch.ChartName]
		switch {
		case !ok:
			label += " [not in release]"
		default:
			if changes := chartChanges(rel, ch); len(changes) > 0 {
				label += " [changed since release: " + strings.Join(changes, ", ") + "]"
			}
		}
		rows = append(rows, output.KeyValue{Key: "Chart", Value: label})
	}
	for _, ch := range released {
		if seen[ch.ChartName] {
			continue
		}
		rows = append(rows, output.KeyValue{Key: "Chart", Value: chartLabel(ch) + " [in release only]"})
	}
	return rows
}

// chartChanges lists the fields that differ between the released chart and
// the working-copy chart. Values are compared after normalizeValues, like
// the server does. For a legacy release the server fills the fields that
// the snapshot does not hold from the working copy, so they compare equal.
func chartChanges(rel, work types.ChartConfig) []string {
	var changes []string
	if rel.ChartVersion != work.ChartVersion {
		changes = append(changes, fmt.Sprintf("chart version %s -> %s", orDash(rel.ChartVersion), orDash(work.ChartVersion)))
	}
	if rel.RepoURL != work.RepoURL {
		changes = append(changes, "repository")
	}
	if rel.ChartPath != work.ChartPath {
		changes = append(changes, "chart path")
	}
	if normalizeValues(rel.DefaultValues) != normalizeValues(work.DefaultValues) {
		changes = append(changes, "values")
	}
	if normalizeValues(rel.LockedValues) != normalizeValues(work.LockedValues) {
		changes = append(changes, "locked values")
	}
	if rel.Required != work.Required {
		changes = append(changes, "required")
	}
	if rel.DeployOrder != work.DeployOrder {
		changes = append(changes, "deploy order")
	}
	if rel.SourceRepoURL != work.SourceRepoURL {
		changes = append(changes, "source repo")
	}
	if rel.BuildPipelineID != work.BuildPipelineID {
		changes = append(changes, "build pipeline")
	}
	return changes
}

// normalizeValues trims trailing whitespace and newlines from a values
// document, like the server's models.NormalizeValues, so that a trailing
// newline is not a change.
func normalizeValues(s string) string {
	return strings.TrimRight(s, " \t\r\n")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// isNoReleaseError reports whether err is the 409 that k8s-stack-manager
// v0.6.0+ returns when a template has no published version. Older servers
// answer quick deploy with 400 "Template is not published".
func isNoReleaseError(err error) bool {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	switch apiErr.StatusCode {
	case http.StatusConflict:
		return strings.Contains(msg, "no published version")
	case http.StatusBadRequest:
		return strings.Contains(msg, "not published")
	}
	return false
}

// templateReleaseError turns the "no published version" response of
// instantiate and quick deploy into a message with the next step. It reads
// the template (best effort): a template that is unpublished but has a
// release, or an older server, needs a plain publish; a template without a
// release needs a publish with a version.
func templateReleaseError(err error, c *client.Client, id, template string) error {
	if !isNoReleaseError(err) {
		return err
	}
	if tmpl, getErr := bestEffortClient(c).GetTemplate(id); getErr == nil {
		hasRelease := tmpl.PublishedVersion != nil && *tmpl.PublishedVersion != ""
		if hasRelease || tmpl.HasUnpublishedChanges == nil {
			return fmt.Errorf("template %s is not published; publish it first: stackctl template publish %s", template, template)
		}
	}
	return fmt.Errorf("template %s has no published version; publish it first: stackctl template publish %s --version <version>", template, template)
}

// versionExistsRe reads the version from the 409 message of publish
// ("Version 1.2.0 already exists").
var versionExistsRe = regexp.MustCompile(`(?i)^version ([0-9A-Za-z._+-]{1,50}) already exists`)

// publishError turns the 409 and 400 responses of publish into clear
// messages. Other errors are returned unchanged.
func publishError(err error, template, version string) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg := strings.ToLower(apiErr.Message)
	switch {
	case apiErr.StatusCode == http.StatusConflict && strings.Contains(msg, "already exists"):
		if version == "" {
			if m := versionExistsRe.FindStringSubmatch(apiErr.Message); m != nil {
				version = m[1]
			}
		}
		if version == "" {
			return fmt.Errorf("template %s: the version of the working copy already exists; publish with a new version: stackctl template publish %s --version <new-version>", template, template)
		}
		return fmt.Errorf("template %s: version %s already exists; publish with a new version: stackctl template publish %s --version <new-version>", template, version, template)
	case apiErr.StatusCode == http.StatusBadRequest && strings.Contains(msg, "version is required"):
		return fmt.Errorf("template %s has no version; set one: stackctl template publish %s --version <version>", template, template)
	case apiErr.StatusCode == http.StatusBadRequest:
		return fmt.Errorf("cannot publish template %s: %w", template, err)
	}
	return err
}

// parseVersionRef validates a version ID argument of 'template versions
// diff'. It accepts a version ID or "working" (the working copy).
func parseVersionRef(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, types.TemplateVersionWorkingCopy) {
		return types.TemplateVersionWorkingCopy, nil
	}
	return parseID(s)
}

// diffSideLabel names one side of a version diff: the version string, or
// "working copy" for the draft.
func diffSideLabel(side types.TemplateVersionSide) string {
	if side.IsWorkingCopy || side.ID == types.TemplateVersionWorkingCopy {
		if side.Version != "" {
			return fmt.Sprintf("working copy (%s)", side.Version)
		}
		return "working copy"
	}
	if side.Version == "" {
		return orDash(side.ID)
	}
	return side.Version
}

// chartVersionChange shows a chart version change of a diff entry, or "-"
// when the server does not send chart versions or they are equal.
func chartVersionChange(left, right string) string {
	if left == right {
		if left == "" {
			return "-"
		}
		return left
	}
	return fmt.Sprintf("%s -> %s", orDash(left), orDash(right))
}

var templateUpdateChartCmd = &cobra.Command{
	Use:   "update-chart <template-id> <chart-id>",
	Short: "Update a chart config within a template",
	Long: `Update a chart configuration's settings within a stack template.

The command reads the current chart config from the template and merges your
changes, so unspecified fields are preserved (the API replaces the full record).

The change goes to the working copy (draft). Users get it after
'stackctl template publish <id> --version <new-version>'.

Examples:
  stackctl template update-chart 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --chart-version 0.3.7
  stackctl template update-chart 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --file values.yaml
  stackctl template update-chart 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --locked-file locked.yaml
  stackctl template update-chart 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --build-pipeline-id 42 --source-repo-url https://dev.azure.com/org/project/_git/repo
  stackctl template update-chart 8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --required=false`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		templateID, err := parseID(args[0])
		if err != nil {
			return fmt.Errorf("invalid template ID: %w", err)
		}
		chartID, err := parseID(args[1])
		if err != nil {
			return fmt.Errorf("invalid chart ID: %w", err)
		}

		chartPath, _ := cmd.Flags().GetString("chart-path")
		chartVersion, _ := cmd.Flags().GetString("chart-version")
		sourceRepoURL, _ := cmd.Flags().GetString("source-repo-url")
		repositoryURL, _ := cmd.Flags().GetString("repository-url")
		buildPipelineID, _ := cmd.Flags().GetString("build-pipeline-id")
		deployOrder, _ := cmd.Flags().GetInt("deploy-order")
		valuesFile, _ := cmd.Flags().GetString("file")
		lockedFile, _ := cmd.Flags().GetString("locked-file")
		requiredChanged := cmd.Flags().Changed("required")
		required, _ := cmd.Flags().GetBool("required")

		if chartPath == "" && chartVersion == "" && sourceRepoURL == "" && repositoryURL == "" &&
			buildPipelineID == "" && deployOrder < 0 && valuesFile == "" && lockedFile == "" && !requiredChanged {
			return fmt.Errorf("at least one of --chart-path, --chart-version, --source-repo-url, --repository-url, --build-pipeline-id, --deploy-order, --file, --locked-file, or --required must be specified")
		}
		if err := validateChartRepositoryURL(repositoryURL); err != nil {
			return err
		}
		if err := checkNoPathTraversal(valuesFile); err != nil {
			return err
		}
		if err := checkNoPathTraversal(lockedFile); err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		tmpl, err := c.GetTemplate(templateID)
		if err != nil {
			return fmt.Errorf("fetching template: %w", err)
		}
		var current *types.ChartConfig
		for i := range tmpl.Charts {
			if tmpl.Charts[i].ID == chartID {
				current = &tmpl.Charts[i]
				break
			}
		}
		if current == nil {
			return fmt.Errorf("chart %s not found in template %s", chartID, templateID)
		}

		// The API replaces every field: seed the request from the current
		// record so fields the user did not change keep their values.
		req := types.UpdateTemplateChartRequest{
			ChartName:       current.ChartName,
			RepositoryURL:   current.RepoURL,
			SourceRepoURL:   current.SourceRepoURL,
			BuildPipelineID: current.BuildPipelineID,
			ChartPath:       current.ChartPath,
			ChartVersion:    current.ChartVersion,
			DefaultValues:   current.DefaultValues,
			LockedValues:    current.LockedValues,
			DeployOrder:     current.DeployOrder,
			Required:        current.Required,
		}

		if chartPath != "" {
			req.ChartPath = chartPath
		}
		if chartVersion != "" {
			req.ChartVersion = chartVersion
		}
		if sourceRepoURL != "" {
			req.SourceRepoURL = sourceRepoURL
		}
		if repositoryURL != "" {
			req.RepositoryURL = repositoryURL
		}
		if buildPipelineID != "" {
			req.BuildPipelineID = buildPipelineID
		}
		if deployOrder >= 0 {
			req.DeployOrder = deployOrder
		}
		if requiredChanged {
			req.Required = required
		}
		if valuesFile != "" {
			valuesFile = filepath.Clean(valuesFile)
			data, err := os.ReadFile(valuesFile)
			if err != nil {
				return readFileErr(valuesFile, err)
			}
			req.DefaultValues = string(data)
		}
		if lockedFile != "" {
			lockedFile = filepath.Clean(lockedFile)
			data, err := os.ReadFile(lockedFile)
			if err != nil {
				return readFileErr(lockedFile, err)
			}
			req.LockedValues = string(data)
		}

		updated, err := c.UpdateTemplateChart(templateID, chartID, &req)
		if err != nil {
			return err
		}
		return printChartConfig(updated)
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

	// template get flags
	templateGetCmd.Flags().Bool("released", false, "List the released charts instead of the working copy charts")

	// template create flags
	templateCreateCmd.Flags().String("name", "", "Template name (required unless --from-file is used)")
	templateCreateCmd.Flags().String("description", "", "Template description")
	templateCreateCmd.Flags().String("version", "", "Version of the working copy (e.g. 1.0.0); 'template publish' uses it by default")
	templateCreateCmd.Flags().String(flagFromFile, "", "Path to a JSON file containing the template definition")

	// template update flags
	templateUpdateCmd.Flags().String("name", "", "New template name")
	templateUpdateCmd.Flags().String("description", "", "New template description")
	templateUpdateCmd.Flags().String("version", "", "New version of the working copy (e.g. 1.1.0)")
	templateUpdateCmd.Flags().String(flagFromFile, "", "Path to a JSON file with updated template fields")

	// template publish flags
	templatePublishCmd.Flags().String("version", "", "Version of the new release (default: the version of the working copy)")
	templatePublishCmd.Flags().String("change-summary", "", "Short description of the changes in this release")

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
	templateCmd.AddCommand(templateUpdateChartCmd)

	templateUpdateChartCmd.Flags().String("chart-path", "", "Chart path (e.g. /charts/app-core)")
	templateUpdateChartCmd.Flags().String("chart-version", "", "Chart version")
	templateUpdateChartCmd.Flags().String("source-repo-url", "", "Git repository URL for branch listing")
	templateUpdateChartCmd.Flags().String("repository-url", "", "Helm chart repository URL (e.g. oci://acr.example.com/helm)")
	templateUpdateChartCmd.Flags().String("build-pipeline-id", "", "CI pipeline ID that builds the chart's image (used by a pre-deploy CI gate)")
	templateUpdateChartCmd.Flags().Int("deploy-order", -1, "Deploy order (0+)")
	templateUpdateChartCmd.Flags().String("file", "", "File containing default values")
	templateUpdateChartCmd.Flags().String("locked-file", "", "File containing locked values (always win over instance overrides)")
	templateUpdateChartCmd.Flags().Bool("required", false, "Whether the chart is required when the template is instantiated")
	templateVersionsCmd.AddCommand(templateVersionsListCmd)
	templateVersionsCmd.AddCommand(templateVersionsGetCmd)
	templateVersionsCmd.AddCommand(templateVersionsDiffCmd)
	templateCmd.AddCommand(templateVersionsCmd)
	rootCmd.AddCommand(templateCmd)
}
