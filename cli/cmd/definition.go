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

const (
	flagFromFile     = "from-file"
	msgPathTraversal = "file path must not contain '..' segments"
)

var definitionCmd = &cobra.Command{
	Use:   "definition",
	Short: "Manage stack definitions",
	Long:  "Create, update, delete, export, and import stack definitions.",
}

var definitionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stack definitions",
	Long: `List stack definitions with optional filtering.

Examples:
  stackctl definition list
  stackctl definition list --mine
  stackctl definition list -o json
  stackctl definition list -q | xargs -I{} stackctl definition get {}`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		params := map[string]string{}

		if mine, _ := cmd.Flags().GetBool("mine"); mine {
			params["owner"] = "me"
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

		resp, err := c.ListDefinitions(params)
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]string, len(resp.Data))
			for i, d := range resp.Data {
				ids[i] = d.ID
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
			headers := []string{"ID", "NAME", "DESCRIPTION", "OWNER"}
			rows := make([][]string, len(resp.Data))
			for i, d := range resp.Data {
				rows[i] = []string{
					d.ID,
					d.Name,
					d.Description,
					d.Owner,
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var definitionGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show stack definition details",
	Long: `Show detailed information about a stack definition.

Examples:
  stackctl definition get 1
  stackctl definition get 1 -o json`,
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

		def, err := c.GetDefinition(id)
		if err != nil {
			return err
		}

		return printDefinition(def)
	},
}

var definitionCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new stack definition",
	Long: `Create a new stack definition from flags or a JSON file.

Examples:
  stackctl definition create --name my-def --description "My definition"
  stackctl definition create --from-file definition.json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString(flagFromFile)

		var req types.CreateDefinitionRequest
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
				return fmt.Errorf("'name' field is required in the definition file")
			}
		} else {
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				return fmt.Errorf("--name is required (or use --from-file)")
			}
			description, _ := cmd.Flags().GetString("description")
			req = types.CreateDefinitionRequest{
				Name:        name,
				Description: description,
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		def, err := c.CreateDefinition(&req)
		if err != nil {
			return err
		}

		return printDefinition(def)
	},
}

var definitionUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a stack definition",
	Long: `Update an existing stack definition from flags or a JSON file.

Examples:
  stackctl definition update 1 --name new-name
  stackctl definition update 1 --branch develop
  stackctl definition update 1 --from-file definition.json`,
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
		branch, _ := cmd.Flags().GetString("branch")

		if fromFile == "" && name == "" && description == "" && branch == "" {
			return fmt.Errorf("at least one of --name, --description, --branch, or --from-file must be specified")
		}

		var req types.UpdateDefinitionRequest
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
			if branch != "" {
				req.DefaultBranch = branch
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		def, err := c.UpdateDefinition(id, &req)
		if err != nil {
			return err
		}

		return printDefinition(def)
	},
}

var definitionDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a stack definition",
	Long: `Permanently delete a stack definition.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl definition delete 1
  stackctl definition delete 1 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will permanently delete definition %s. Continue? (y/n): ",
			passthroughID,
			func(c *client.Client, id string) error { return c.DeleteDefinition(id) },
			"Deleted definition %s",
		)
	},
}

var definitionExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export a stack definition as JSON",
	Long: `Export a stack definition as a JSON bundle.

By default, the JSON is written to stdout. Use --output-file to write to a file.

Examples:
  stackctl definition export 1
  stackctl definition export 1 --output-file definition.json`,
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

		data, err := c.ExportDefinition(id)
		if err != nil {
			return err
		}

		outputFile, _ := cmd.Flags().GetString("output-file")
		if outputFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(outputFile), "/") {
				if segment == ".." {
					return fmt.Errorf("output file path must not contain '..' segments")
				}
			}
			outputFile = filepath.Clean(outputFile)
			if err := os.WriteFile(outputFile, data, 0600); err != nil {
				return fmt.Errorf("writing file %s: %w", outputFile, err)
			}
			if printer.Quiet {
				fmt.Fprintln(printer.Writer, id)
			} else {
				printer.PrintMessage("Exported definition %s to %s", id, outputFile)
			}
			return nil
		}

		if printer.Quiet {
			_, err = fmt.Fprintln(printer.Writer, id)
			return err
		}

		_, err = fmt.Fprint(printer.Writer, string(data))
		return err
	},
}

var definitionImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a stack definition from a JSON file",
	Long: `Import a stack definition from a JSON file.

Examples:
  stackctl definition import --file definition.json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		if file == "" {
			return fmt.Errorf("--file is required")
		}

		for _, segment := range strings.Split(filepath.ToSlash(file), "/") {
			if segment == ".." {
				return errors.New(msgPathTraversal)
			}
		}
		file = filepath.Clean(file)

		data, err := os.ReadFile(file)
		if err != nil {
			return readFileErr(file, err)
		}

		if !json.Valid(data) {
			return fmt.Errorf("file %s contains invalid JSON", file)
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		def, err := c.ImportDefinition(data)
		if err != nil {
			return err
		}

		return printDefinition(def)
	},
}

var definitionUpdateChartCmd = &cobra.Command{
	Use:   "update-chart <definition-id> <chart-id>",
	Short: "Update a chart config within a definition",
	Long: `Update a chart configuration's settings within a stack definition.

The command fetches the current chart config and merges your changes,
so unspecified fields are preserved.

Examples:
  stackctl definition update-chart 1 5 --chart-version 0.3.0
  stackctl definition update-chart 1 5 --chart-path /charts/kvk-core
  stackctl definition update-chart 1 5 --deploy-order 6
  stackctl definition update-chart 1 5 --file values.yaml`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		defID, err := parseID(args[0])
		if err != nil {
			return fmt.Errorf("invalid definition ID: %w", err)
		}
		chartID, err := parseID(args[1])
		if err != nil {
			return fmt.Errorf("invalid chart ID: %w", err)
		}

		chartPath, _ := cmd.Flags().GetString("chart-path")
		chartVersion, _ := cmd.Flags().GetString("chart-version")
		deployOrder, _ := cmd.Flags().GetInt("deploy-order")
		valuesFile, _ := cmd.Flags().GetString("file")

		if chartPath == "" && chartVersion == "" && deployOrder < 0 && valuesFile == "" {
			return fmt.Errorf("at least one of --chart-path, --chart-version, --deploy-order, or --file must be specified")
		}

		if valuesFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(valuesFile), "/") {
				if segment == ".." {
					return errors.New(msgPathTraversal)
				}
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		current, err := c.GetDefinitionChart(defID, chartID)
		if err != nil {
			return fmt.Errorf("fetching current chart config: %w", err)
		}

		req := types.UpdateChartConfigRequest{
			ChartName:     current.ChartName,
			ChartPath:     current.RepoURL,
			ChartVersion:  current.ChartVersion,
			DefaultValues: current.DefaultValues,
		}

		if chartPath != "" {
			req.ChartPath = chartPath
		}
		if chartVersion != "" {
			req.ChartVersion = chartVersion
		}
		if deployOrder >= 0 {
			req.DeployOrder = &deployOrder
		}
		if valuesFile != "" {
			valuesFile = filepath.Clean(valuesFile)
			data, err := os.ReadFile(valuesFile)
			if err != nil {
				return readFileErr(valuesFile, err)
			}
			req.DefaultValues = string(data)
		}

		updated, err := c.UpdateDefinitionChart(defID, chartID, &req)
		if err != nil {
			return err
		}

		return printChartConfig(updated)
	},
}

func printChartConfig(ch *types.ChartConfig) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, ch.ID)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(ch)
	case output.FormatYAML:
		return printer.PrintYAML(ch)
	default:
		fields := []output.KeyValue{
			{Key: "ID", Value: ch.ID},
			{Key: "Name", Value: ch.Name},
			{Key: "Chart", Value: ch.ChartName},
			{Key: "Repository", Value: ch.RepoURL},
			{Key: "Version", Value: ch.ChartVersion},
			{Key: "Release Name", Value: ch.ReleaseName},
		}
		return printer.PrintSingle(ch, fields)
	}
}

// printDefinition prints a stack definition in the configured output format.
func printDefinition(def *types.StackDefinition) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, def.ID)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(def)
	case output.FormatYAML:
		return printer.PrintYAML(def)
	default:
		fields := []output.KeyValue{
			{Key: "ID", Value: def.ID},
			{Key: "Name", Value: def.Name},
			{Key: "Description", Value: def.Description},
			{Key: "Owner", Value: def.Owner},
			{Key: "Default Branch", Value: def.DefaultBranch},
		}
		for _, ch := range def.Charts {
			fields = append(fields, output.KeyValue{
				Key:   "Chart",
				Value: fmt.Sprintf("%s (%s@%s)", ch.ChartName, ch.RepoURL, ch.ChartVersion),
			})
		}
		return printer.PrintSingle(def, fields)
	}
}

func init() {
	// definition list flags
	definitionListCmd.Flags().Bool("mine", false, "Show only my definitions")
	definitionListCmd.Flags().Int("page", 0, "Page number")
	definitionListCmd.Flags().Int(flagPageSize, 0, "Page size")

	// definition create flags
	definitionCreateCmd.Flags().String("name", "", "Definition name")
	definitionCreateCmd.Flags().String("description", "", "Definition description")
	definitionCreateCmd.Flags().String(flagFromFile, "", "Create from JSON file")

	// definition update flags
	definitionUpdateCmd.Flags().String("name", "", "New definition name")
	definitionUpdateCmd.Flags().String("description", "", "New definition description")
	definitionUpdateCmd.Flags().String("branch", "", "New default branch")
	definitionUpdateCmd.Flags().String(flagFromFile, "", "Update from JSON file")

	// definition update-chart flags
	definitionUpdateChartCmd.Flags().String("chart-path", "", "Chart path (e.g. /charts/kvk-core)")
	definitionUpdateChartCmd.Flags().String("chart-version", "", "Chart version")
	definitionUpdateChartCmd.Flags().Int("deploy-order", -1, "Deploy order (0+)")
	definitionUpdateChartCmd.Flags().String("file", "", "File containing default values")

	// definition delete flags
	definitionDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	definitionDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// definition export flags
	definitionExportCmd.Flags().String("output-file", "", "Write export to file instead of stdout")

	// definition import flags
	definitionImportCmd.Flags().String("file", "", "JSON file to import (required)")
	_ = definitionImportCmd.MarkFlagRequired("file")

	// Wire up subcommands
	definitionCmd.AddCommand(definitionListCmd)
	definitionCmd.AddCommand(definitionGetCmd)
	definitionCmd.AddCommand(definitionCreateCmd)
	definitionCmd.AddCommand(definitionUpdateCmd)
	definitionCmd.AddCommand(definitionDeleteCmd)
	definitionCmd.AddCommand(definitionExportCmd)
	definitionCmd.AddCommand(definitionImportCmd)
	definitionCmd.AddCommand(definitionUpdateChartCmd)
	rootCmd.AddCommand(definitionCmd)
}

func readFileErr(path string, err error) error {
	return fmt.Errorf("reading file %s: %w", path, err)
}
