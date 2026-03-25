package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
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
			ids := make([]uint, len(resp.Data))
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
			headers := []string{"ID", "NAME", "DESCRIPTION", "OWNER", "CHARTS"}
			rows := make([][]string, len(resp.Data))
			for i, d := range resp.Data {
				rows[i] = []string{
					strconv.FormatUint(uint64(d.ID), 10),
					d.Name,
					d.Description,
					d.Owner,
					strconv.Itoa(len(d.Charts)),
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
		fromFile, _ := cmd.Flags().GetString("from-file")

		var req types.CreateDefinitionRequest
		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return fmt.Errorf("file path must not contain '..' segments")
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", fromFile, err)
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
  stackctl definition update 1 --from-file definition.json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		fromFile, _ := cmd.Flags().GetString("from-file")
		name, _ := cmd.Flags().GetString("name")
		description, _ := cmd.Flags().GetString("description")

		if fromFile == "" && name == "" && description == "" {
			return fmt.Errorf("at least one of --name, --description, or --from-file must be specified")
		}

		var req types.UpdateDefinitionRequest
		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return fmt.Errorf("file path must not contain '..' segments")
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", fromFile, err)
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
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Fprintf(cmd.ErrOrStderr(), "This will permanently delete definition %d. Continue? (y/n): ", id)
			reader := bufio.NewReader(cmd.InOrStdin())
			answer, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				printer.PrintMessage("Aborted.")
				return nil
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		if err := c.DeleteDefinition(id); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}

		printer.PrintMessage("Deleted definition %d", id)
		return nil
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
				printer.PrintMessage("Exported definition %d to %s", id, outputFile)
			}
			return nil
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

		for _, segment := range strings.Split(filepath.ToSlash(file), "/") {
			if segment == ".." {
				return fmt.Errorf("file path must not contain '..' segments")
			}
		}
		file = filepath.Clean(file)

		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading file %s: %w", file, err)
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
			{Key: "ID", Value: strconv.FormatUint(uint64(def.ID), 10)},
			{Key: "Name", Value: def.Name},
			{Key: "Description", Value: def.Description},
			{Key: "Owner", Value: def.Owner},
			{Key: "Default Branch", Value: def.DefaultBranch},
		}
		for _, ch := range def.Charts {
			fields = append(fields, output.KeyValue{
				Key:   "Chart",
				Value: fmt.Sprintf("%s (%s@%s)", ch.Name, ch.RepoURL, ch.ChartVersion),
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
	definitionCreateCmd.Flags().String("from-file", "", "Create from JSON file")

	// definition update flags
	definitionUpdateCmd.Flags().String("name", "", "New definition name")
	definitionUpdateCmd.Flags().String("description", "", "New definition description")
	definitionUpdateCmd.Flags().String("from-file", "", "Update from JSON file")

	// definition delete flags
	definitionDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

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
	rootCmd.AddCommand(definitionCmd)
}
