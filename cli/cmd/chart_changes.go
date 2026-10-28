package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// Commands that add or remove the charts of a definition or a template,
// and the template upgrade and link of a definition.

// addChartFlags registers the flags of "add-chart". withTemplate adds the
// flags of a template chart (--locked-file, --required).
func addChartFlags(cmd *cobra.Command, withTemplate bool) {
	cmd.Flags().String("name", "", "Chart name (required)")
	cmd.Flags().String("repository-url", "", "Helm chart repository URL (e.g. oci://acr.example.com/helm)")
	cmd.Flags().String("chart-path", "", "Chart path (e.g. /charts/app-core)")
	cmd.Flags().String("chart-version", "", "Chart version")
	cmd.Flags().String("source-repo-url", "", "Git repository URL for branch listing")
	cmd.Flags().String("build-pipeline-id", "", "CI pipeline ID that builds the chart's image (used by a pre-deploy CI gate)")
	cmd.Flags().Int("deploy-order", 0, "Deploy order (0+)")
	cmd.Flags().String("file", "", "File containing default values")
	if withTemplate {
		cmd.Flags().String("locked-file", "", "File containing locked values (always win over instance overrides)")
		cmd.Flags().Bool("required", false, "The chart is required: a definition from the template must keep it")
	}
	_ = cmd.MarkFlagRequired("name")
}

// chartFields are the values of the add-chart flags.
type chartFields struct {
	name, repositoryURL, chartPath, chartVersion, sourceRepoURL, buildPipelineID string
	defaultValues, lockedValues                                                  string
	deployOrder                                                                  int
	required                                                                     bool
}

// readChartFlags reads and checks the add-chart flags before any request.
func readChartFlags(cmd *cobra.Command) (chartFields, error) {
	var f chartFields
	f.name, _ = cmd.Flags().GetString("name")
	f.repositoryURL, _ = cmd.Flags().GetString("repository-url")
	f.chartPath, _ = cmd.Flags().GetString("chart-path")
	f.chartVersion, _ = cmd.Flags().GetString("chart-version")
	f.sourceRepoURL, _ = cmd.Flags().GetString("source-repo-url")
	f.buildPipelineID, _ = cmd.Flags().GetString("build-pipeline-id")
	f.deployOrder, _ = cmd.Flags().GetInt("deploy-order")
	if strings.TrimSpace(f.name) == "" {
		return f, fmt.Errorf("--name is required")
	}
	if f.deployOrder < 0 {
		return f, fmt.Errorf("--deploy-order must be 0 or more")
	}
	if err := validateChartRepositoryURL(f.repositoryURL); err != nil {
		return f, err
	}
	var err error
	if f.defaultValues, err = readValuesFlagFile(cmd, "file"); err != nil {
		return f, err
	}
	if cmd.Flags().Lookup("locked-file") != nil {
		if f.lockedValues, err = readValuesFlagFile(cmd, "locked-file"); err != nil {
			return f, err
		}
		f.required, _ = cmd.Flags().GetBool("required")
	}
	return f, nil
}

// readValuesFlagFile returns the content of the file of a flag, or "" when
// the flag is empty.
func readValuesFlagFile(cmd *cobra.Command, flag string) (string, error) {
	path, _ := cmd.Flags().GetString(flag)
	if path == "" {
		return "", nil
	}
	if err := checkNoPathTraversal(path); err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", readFileErr(path, err)
	}
	return string(data), nil
}

// ---------- definition add-chart / remove-chart ----------

var definitionAddChartCmd = &cobra.Command{
	Use:   "add-chart <definition>",
	Short: "Add a chart to a definition",
	Long: `Add a chart to a stack definition. Needs the devops or admin role.

<definition> is a definition name or ID. The server checks the chart source
against its allowed chart repositories (ALLOWED_CHART_REPOS).

Examples:
  stackctl definition add-chart example-dev --name worker --repository-url oci://registry.example.com/helm --chart-version 1.2.0
  stackctl definition add-chart example-dev --name api --chart-path /charts/api --deploy-order 2 --file api-values.yaml`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := readChartFlags(cmd)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		defID, err := resolveDefinitionID(c, args[0])
		if err != nil {
			return err
		}
		order := f.deployOrder
		chart, err := c.AddDefinitionChart(defID, &types.UpdateChartConfigRequest{
			ChartName:       f.name,
			RepositoryURL:   f.repositoryURL,
			ChartPath:       f.chartPath,
			ChartVersion:    f.chartVersion,
			SourceRepoURL:   f.sourceRepoURL,
			BuildPipelineID: f.buildPipelineID,
			DeployOrder:     &order,
			DefaultValues:   f.defaultValues,
		})
		if err != nil {
			return err
		}
		return printChartConfig(chart)
	},
}

var definitionRemoveChartCmd = &cobra.Command{
	Use:   "remove-chart <definition> <chart>",
	Short: "Remove a chart from a definition",
	Long: `Remove a chart from a stack definition. Needs the devops or admin role.

<definition> is a definition name or ID. <chart> is a chart name or ID of
that definition. A definition that is linked to a template cannot lose a
chart that the template requires (the server refuses). The command asks for
confirmation unless --yes is set. Running stacks keep the chart until their
next deploy.

Examples:
  stackctl definition remove-chart example-dev worker
  stackctl definition remove-chart example-dev worker --yes`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defID, err := resolveDefinitionID(c, args[0])
		if err != nil {
			return err
		}
		chartID, err := resolveDefinitionChartID(c, defID, args[1])
		if err != nil {
			return err
		}
		if err := confirmAction(cmd, fmt.Sprintf("Remove chart %s from definition %s? (y/n): ", args[1], args[0])); err != nil {
			return err
		}
		if err := c.DeleteDefinitionChart(defID, chartID); err != nil {
			return err
		}
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, chartID)
			return nil
		}
		printer.PrintMessage("Removed chart %s from definition %s", chartID, defID)
		return nil
	},
}

// ---------- definition check-upgrade / upgrade ----------

var definitionCheckUpgradeCmd = &cobra.Command{
	Use:   "check-upgrade <definition>",
	Short: "Check whether the source template has a newer version",
	Long: `Check whether the source template of a definition has a newer published
version, and which charts an upgrade adds or changes.

A chart that is only in the definition shows as "only in the definition";
the upgrade keeps it. A chart shows as "modified" when its values, its
repository URL or another field differs. A definition without a source
template, or with an unpublished template, has no upgrade.

-q prints "yes" or "no" (an upgrade is available or not). -o json and
-o yaml include chart_diffs with the values of each chart: the definition
now (left) and the template (right).

Examples:
  stackctl definition check-upgrade example-dev
  stackctl definition check-upgrade example-dev -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defID, err := resolveDefinitionID(c, args[0])
		if err != nil {
			return err
		}
		check, err := c.CheckDefinitionUpgrade(defID)
		if err != nil {
			return err
		}
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, yesNo(check.UpgradeAvailable))
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(check)
		case output.FormatYAML:
			return printer.PrintYAML(check)
		default:
			return printUpgradeCheck(c, defID, check)
		}
	},
}

// printUpgradeCheck prints the result of check-upgrade as text and a table
// of the chart changes. Without an upgrade the server sends no reason, so
// the command reads the template link of the definition.
func printUpgradeCheck(c *client.Client, defID string, check *types.UpgradeCheck) error {
	if !check.UpgradeAvailable {
		printer.PrintMessage("%s", noUpgradeReason(c, defID))
		return nil
	}
	printer.PrintMessage("Upgrade available: from version %s to version %s",
		valueOrDash(client.SanitizeServerText(check.CurrentVersion)), client.SanitizeServerText(check.LatestVersion))
	rows := upgradeRows(check)
	if len(rows) == 0 {
		return nil
	}
	return printer.PrintTable([]string{"CHART", "CHANGE"}, rows)
}

// noUpgradeReason explains why a definition has no upgrade.
func noUpgradeReason(c *client.Client, defID string) string {
	def, err := c.GetDefinition(defID)
	switch {
	case err != nil:
		return "No upgrade is available."
	case def.SourceTemplateID == "":
		return "No upgrade: the definition has no source template."
	default:
		return fmt.Sprintf("No upgrade: the definition has template version %s, and the template has no newer published version.",
			valueOrDash(client.SanitizeServerText(def.SourceTemplateVersion)))
	}
}

// upgradeRows returns one row for each chart of the changes. The server
// marks a chart "modified" only for other values or another repository
// URL; a chart with differences in chart_diffs (for example another chart
// version) is also shown as modified.
func upgradeRows(check *types.UpgradeCheck) [][]string {
	ch := check.Changes
	if ch == nil {
		return nil
	}
	differs := map[string]bool{}
	for _, d := range check.ChartDiffs {
		if d.HasDifferences {
			differs[d.ChartName] = true
		}
	}
	var rows [][]string
	add := func(names []string, change string) {
		for _, n := range names {
			rows = append(rows, []string{client.SanitizeServerText(n), change})
		}
	}
	add(ch.ChartsAdded, "added")
	add(ch.ChartsModified, "modified")
	var unchanged []string
	for _, n := range ch.ChartsUnchanged {
		if differs[n] {
			add([]string{n}, "modified")
		} else {
			unchanged = append(unchanged, n)
		}
	}
	add(ch.ChartsRemoved, "only in the definition (kept)")
	add(unchanged, "unchanged")
	return rows
}

// valueOrDash returns s, or "-" when s is empty.
func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var definitionUpgradeCmd = &cobra.Command{
	Use:   "upgrade <definition>",
	Short: "Upgrade a definition to the latest version of its template",
	Long: `Upgrade a definition to the latest published version of its source
template. The upgrade adds the new charts. For the charts that are also in
the template, it replaces the default values with the values of the
template: changes that you made to those values in the definition are
lost. It also sets the repository URL, the deploy order and (when the
template has them) the chart version, chart path, source repository and
build pipeline. Charts that are only in the definition stay. Running stacks
get the change at their next deploy. "definition check-upgrade -o yaml"
shows the values of both sides.

The command shows the changes first and asks for confirmation unless --yes
is set. Needs the owner of the definition, a devops user or an admin.

Examples:
  stackctl definition upgrade example-dev
  stackctl definition upgrade example-dev --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defID, err := resolveDefinitionID(c, args[0])
		if err != nil {
			return err
		}
		check, err := c.CheckDefinitionUpgrade(defID)
		if err != nil {
			return err
		}
		if !check.UpgradeAvailable {
			// Nothing to do: the command shows why and succeeds.
			switch {
			case printer.Quiet:
				return nil
			case printer.Format == output.FormatJSON:
				return printer.PrintJSON(check)
			case printer.Format == output.FormatYAML:
				return printer.PrintYAML(check)
			default:
				return printUpgradeCheck(c, defID, check)
			}
		}

		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			if err := printUpgradeCheckTo(cmd, c, defID, check); err != nil {
				return err
			}
			if err := confirmAction(cmd, fmt.Sprintf("Upgrade definition %s to version %s? The default values of the template charts replace the values of the definition. (y/n): ",
				args[0], client.SanitizeServerText(check.LatestVersion))); err != nil {
				return err
			}
		}

		def, err := c.UpgradeDefinition(defID)
		if err != nil {
			return err
		}
		if printer.Format == output.FormatTable && !printer.Quiet {
			printer.PrintMessage("Upgraded definition %s to version %s", defID, valueOrDash(client.SanitizeServerText(def.SourceTemplateVersion)))
			return nil
		}
		return printDefinition(def)
	},
}

// printUpgradeCheckTo prints the upgrade check as text to stderr, so the
// output of the command (stdout) stays the upgraded definition.
func printUpgradeCheckTo(cmd *cobra.Command, c *client.Client, defID string, check *types.UpgradeCheck) error {
	prev := printer.Writer
	printer.Writer = cmd.ErrOrStderr()
	defer func() { printer.Writer = prev }()
	return printUpgradeCheck(c, defID, check)
}

// ---------- definition link-template ----------

var definitionLinkTemplateCmd = &cobra.Command{
	Use:   "link-template <definition>",
	Short: "Link a definition to a template version, or remove the link",
	Long: `Link a definition to a published template version, or remove the link
with --unlink. Needs the devops or admin role. The link changes only the
source template of the definition; its charts and values do not change.

A linked definition gets the template rules: the required charts stay, the
locked values of the latest published template version apply at deploy,
and "definition check-upgrade" compares the definition with the template.
--version is a published version of the template; without it the server
uses the latest published version. The template must be published.

Examples:
  stackctl definition link-template example-dev --template web-stack
  stackctl definition link-template example-dev --template web-stack --version 1.2.0
  stackctl definition link-template example-dev --unlink`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		tmpl, _ := cmd.Flags().GetString("template")
		version, _ := cmd.Flags().GetString("version")
		unlink, _ := cmd.Flags().GetBool("unlink")
		switch {
		case unlink && (tmpl != "" || version != ""):
			return fmt.Errorf("--unlink cannot be used with --template or --version")
		case !unlink && strings.TrimSpace(tmpl) == "":
			return fmt.Errorf("--template or --unlink is required")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		defID, err := resolveDefinitionID(c, args[0])
		if err != nil {
			return err
		}
		req := &types.TemplateLinkRequest{Version: version}
		if !unlink {
			if req.TemplateID, err = resolveTemplateID(c, tmpl); err != nil {
				return err
			}
		}
		def, err := c.SetDefinitionTemplateLink(defID, req)
		if err != nil {
			return linkTemplateError(err)
		}
		if printer.Format == output.FormatTable && !printer.Quiet {
			if def.SourceTemplateID == "" {
				printer.PrintMessage("Removed the template link of definition %s", defID)
			} else {
				printer.PrintMessage("Linked definition %s to template %s version %s", defID, def.SourceTemplateID, valueOrDash(client.SanitizeServerText(def.SourceTemplateVersion)))
			}
			return nil
		}
		return printDefinition(def)
	},
}

// linkTemplateError adds an upgrade hint to the 404 of a server without
// the template-link route (before v0.11.0).
func linkTemplateError(err error) error {
	if isStatus(err, http.StatusNotFound) && isRouteNotFound(err) {
		return fmt.Errorf("%w (the server does not support template links; upgrade k8s-stack-manager to v0.11.0 or later)", err)
	}
	return err
}

// isRouteNotFound reports whether a 404 has no JSON message: the route
// does not exist on the server.
func isRouteNotFound(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Message == http.StatusText(http.StatusNotFound)
}

// ---------- template add-chart / remove-chart ----------

// templateDraftNote is written to stderr after a change of template charts.
const templateDraftNote = "Note: the change is in the working copy of the template. Run 'stackctl template publish %s --version <new-version>' to make it visible to users.\n"

var templateAddChartCmd = &cobra.Command{
	Use:   "add-chart <template>",
	Short: "Add a chart to the working copy of a template",
	Long: `Add a chart to the working copy (draft) of a template. Needs the devops
role and the template owner, or an admin.

<template> is a template name or ID. Users get the change after the next
"template publish".

Examples:
  stackctl template add-chart web-stack --name worker --repository-url oci://registry.example.com/helm --chart-version 1.2.0
  stackctl template add-chart web-stack --name api --chart-path /charts/api --required --locked-file locked.yaml`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := readChartFlags(cmd)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		templateID, err := resolveTemplateID(c, args[0])
		if err != nil {
			return err
		}
		chart, err := c.AddTemplateChart(templateID, &types.UpdateTemplateChartRequest{
			ChartName:       f.name,
			RepositoryURL:   f.repositoryURL,
			SourceRepoURL:   f.sourceRepoURL,
			BuildPipelineID: f.buildPipelineID,
			ChartPath:       f.chartPath,
			ChartVersion:    f.chartVersion,
			DefaultValues:   f.defaultValues,
			LockedValues:    f.lockedValues,
			DeployOrder:     f.deployOrder,
			Required:        f.required,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), templateDraftNote, templateID)
		return printChartConfig(chart)
	},
}

var templateRemoveChartCmd = &cobra.Command{
	Use:   "remove-chart <template> <chart>",
	Short: "Remove a chart from the working copy of a template",
	Long: `Remove a chart from the working copy (draft) of a template. Needs the
devops role and the template owner, or an admin.

<template> is a template name or ID. <chart> is a chart name or ID of the
working copy. Users get the change after the next "template publish". The
command asks for confirmation unless --yes is set.

Examples:
  stackctl template remove-chart web-stack worker
  stackctl template remove-chart web-stack worker --yes`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		templateID, err := resolveTemplateID(c, args[0])
		if err != nil {
			return err
		}
		tmpl, err := c.GetTemplate(templateID)
		if err != nil {
			return fmt.Errorf("resolving chart %q: %w", args[1], err)
		}
		chartID, err := pickChartID(tmpl.Charts, strings.TrimSpace(args[1]), "template "+templateID)
		if err != nil {
			return err
		}
		if err := confirmAction(cmd, fmt.Sprintf("Remove chart %s from template %s? (y/n): ", args[1], args[0])); err != nil {
			return err
		}
		if err := c.DeleteTemplateChart(templateID, chartID); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), templateDraftNote, templateID)
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, chartID)
			return nil
		}
		printer.PrintMessage("Removed chart %s from template %s", chartID, templateID)
		return nil
	},
}

func init() {
	addChartFlags(definitionAddChartCmd, false)
	definitionRemoveChartCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	definitionUpgradeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	definitionLinkTemplateCmd.Flags().String("template", "", "Template name or ID to link")
	definitionLinkTemplateCmd.Flags().String("version", "", "Published template version (default: the latest)")
	definitionLinkTemplateCmd.Flags().Bool("unlink", false, "Remove the template link")

	addChartFlags(templateAddChartCmd, true)
	templateRemoveChartCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	definitionCmd.AddCommand(definitionAddChartCmd, definitionRemoveChartCmd,
		definitionCheckUpgradeCmd, definitionUpgradeCmd, definitionLinkTemplateCmd)
	templateCmd.AddCommand(templateAddChartCmd, templateRemoveChartCmd)
}
