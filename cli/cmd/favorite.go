package cmd

import (
	"fmt"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var favoriteCmd = &cobra.Command{
	Use:     "favorite",
	Aliases: []string{"favorites", "fav"},
	Short:   "Manage the authenticated user's favorited entities",
	Long: `Manage stack definitions, instances, and templates favorited by the
authenticated user.

  list   — show all favorites (default table; --quiet prints entity IDs)
  add    — favorite an entity by --type/--id (idempotent server-side)
  remove — un-favorite an entity by --type/--id (idempotent server-side)

The "entity type" must be one of: definition, instance, template.
Favorites are scoped to the calling user; no admin gate.`,
}

// Distinct flag vars per subcommand. Sharing one pair of vars between `add`
// and `remove` would mean the second StringVar() call in init() silently
// overrides the first command's default and ties their state together for
// the lifetime of the process — exactly the fragility that motivated
// audit's PersistentFlags refactor. Here `list` takes no filters, so
// per-command locals are the cleanest split.
var (
	favoriteAddType    string
	favoriteAddID      string
	favoriteRemoveType string
	favoriteRemoveID   string
)

// favoriteEntityTypes mirrors the backend allowlist
// (backend/internal/models/user_favorite.go validFavoriteEntityTypes).
// Duplicated client-side so an invalid --type fails before any API call.
var favoriteEntityTypes = map[string]bool{
	"definition": true,
	"instance":   true,
	"template":   true,
}

func validateFavoriteType(t string) error {
	if t == "" {
		return fmt.Errorf("--type is required (one of: definition, instance, template)")
	}
	if !favoriteEntityTypes[t] {
		return fmt.Errorf("--type %q is invalid: must be one of definition, instance, template", t)
	}
	return nil
}

var favoriteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List favorited entities",
	Long: `List the authenticated user's favorited entities (definitions,
instances, templates).

Examples:
  stackctl favorite list
  stackctl favorite list -o json
  stackctl favorite list --quiet`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		favs, err := c.ListFavorites()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, f := range favs {
				fmt.Fprintln(printer.Writer, f.EntityID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(favs)
		case output.FormatYAML:
			return printer.PrintYAML(favs)
		default:
			if len(favs) == 0 {
				printer.PrintMessage("No favorites.")
				return nil
			}
			headers := []string{"ENTITY TYPE", "ENTITY ID", "FAVORITED AT"}
			rows := make([][]string, len(favs))
			for i, f := range favs {
				rows[i] = []string{f.EntityType, f.EntityID, f.CreatedAt.UTC().Format("2006-01-02 15:04")}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var favoriteAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an entity to favorites",
	Long: `Favorite an entity by --type and --id. The backend is idempotent —
re-adding the same (entity_type, entity_id) returns the existing row
without a duplicate-key error, so this command is safe to script.

Examples:
  stackctl favorite add --type definition --id 42
  stackctl favorite add --type template --id 9 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateFavoriteType(favoriteAddType); err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		fav, err := c.AddFavorite(types.AddFavoriteRequest{
			EntityType: favoriteAddType,
			EntityID:   favoriteAddID,
		})
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, fav.EntityID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(fav)
		case output.FormatYAML:
			return printer.PrintYAML(fav)
		default:
			printer.PrintMessage("Favorited %s %s.", fav.EntityType, fav.EntityID)
			return nil
		}
	},
}

var favoriteRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove an entity from favorites",
	Long: `Un-favorite an entity by --type and --id. The backend is
idempotent — removing a non-existent favorite returns 204 No Content
rather than 404, so this command is safe to script.

Examples:
  stackctl favorite remove --type definition --id 42`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateFavoriteType(favoriteRemoveType); err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		if err := c.RemoveFavorite(favoriteRemoveType, favoriteRemoveID); err != nil {
			return err
		}
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, favoriteRemoveID)
			return nil
		}
		printer.PrintMessage("Removed favorite %s %s.", favoriteRemoveType, favoriteRemoveID)
		return nil
	},
}

func init() {
	favoriteAddCmd.Flags().StringVar(&favoriteAddType, "type", "", "Entity type (definition, instance, template) — required")
	favoriteAddCmd.Flags().StringVar(&favoriteAddID, "id", "", "Entity ID — required")
	// `--id` validation goes through cobra's MarkFlagRequired (consistent
	// usage error + exit code + --help hint). `--type` stays hand-rolled
	// so validateFavoriteType can emit the allowlist hint
	// ("definition, instance, template") that the bare cobra error doesn't.
	_ = favoriteAddCmd.MarkFlagRequired("id")

	favoriteRemoveCmd.Flags().StringVar(&favoriteRemoveType, "type", "", "Entity type (definition, instance, template) — required")
	favoriteRemoveCmd.Flags().StringVar(&favoriteRemoveID, "id", "", "Entity ID — required")
	_ = favoriteRemoveCmd.MarkFlagRequired("id")

	favoriteCmd.AddCommand(favoriteListCmd)
	favoriteCmd.AddCommand(favoriteAddCmd)
	favoriteCmd.AddCommand(favoriteRemoveCmd)
	rootCmd.AddCommand(favoriteCmd)
}

// ResetFavoriteFlagsForTest clears the per-subcommand favorite flag vars
// between in-process Cobra invocations. Subcommand flags are NOT covered
// by ResetFlagsForTest.
func ResetFavoriteFlagsForTest() {
	favoriteAddType = ""
	favoriteAddID = ""
	favoriteRemoveType = ""
	favoriteRemoveID = ""
}

// Internal alias for cmd-package test files.
func resetFavoriteFlagsForTest() { ResetFavoriteFlagsForTest() }
