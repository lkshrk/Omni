package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
)

func newListCmd(state *rootState) *cobra.Command {
	var providerFilter string
	var groupFilter string
	var profileFilter string
	var stateFilter string
	var format string

	cmd := &cobra.Command{
		Use:   "list [tool]",
		Short: "List all tools and their install status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !state.app.HasConfig() {
				fmt.Fprintln(cmd.ErrOrStderr(), "No config found. Run 'omni init' to get started.")
				return nil
			}
			if err := validateFormat(format, "table", "json"); err != nil {
				return err
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			items, err := state.app.QueryTools(cmd.Context(), app.ToolListOptions{
				Provider: providerFilter,
				Group:    groupFilter,
				Profile:  profileFilter,
				Name:     name,
				State:    stateFilter,
			})
			if err != nil {
				return err
			}

			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(toolListOutput(items))
			}
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tools match filters.")
				return nil
			}
			tbl := newTable("NAME", "PROVIDER", "GROUP", "STATE", "VERSION")
			for _, item := range items {
				tbl.AddRow(
					item.Tool.Name,
					item.Tool.Provider,
					displayGroup(item.Group),
					string(item.State),
					displayToolVersion(item.Tool),
				)
			}
			tbl.Render(cmd.OutOrStdout())
			return nil
		},
	}

	addProviderFlag(cmd, &providerFilter, "filter by provider")
	cmd.Flags().StringVar(&groupFilter, "group", "", "filter by group")
	cmd.Flags().StringVar(&profileFilter, "profile", "", "filter by profile")
	cmd.Flags().StringVar(&stateFilter, "state", "", "filter by state (installed, missing, outdated, ignored, unclaimed, out-of-sync, failed)")
	addFormatFlag(cmd, &format, "table", "table", "json")
	return cmd
}

type toolJSON struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Package       string `json:"package"`
	Group         string `json:"group,omitempty"`
	State         string `json:"state"`
	Installed     bool   `json:"installed"`
	InstalledWith string `json:"installed_with,omitempty"`
	Version       string `json:"version,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
	Description   string `json:"description,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	FailureCount  int    `json:"failure_count,omitempty"`
	Tracked       bool   `json:"tracked"`
}

func toolListOutput(items []app.ToolListItem) []toolJSON {
	out := make([]toolJSON, 0, len(items))
	for _, item := range items {
		t := item.Tool
		out = append(out, toolJSON{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.Package,
			Group:         displayGroup(item.Group),
			State:         string(item.State),
			Installed:     t.Installed,
			InstalledWith: t.InstalledWith,
			Version:       nullString(t.Version),
			LatestVersion: nullString(t.LatestVersion),
			Description:   nullString(t.Description),
			LastError:     nullString(t.LastError),
			FailureCount:  t.FailureCount,
			Tracked:       t.Tracked,
		})
	}
	return out
}

func displayToolVersion(t *database.ToolCache) string {
	if t.Outdated && t.LatestVersion.Valid {
		current := nullString(t.Version)
		if current == "" {
			return t.LatestVersion.String
		}
		return current + " -> " + t.LatestVersion.String
	}
	return nullString(t.Version)
}

func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func displayGroup(group string) string {
	if strings.TrimSpace(group) == "" {
		return "base"
	}
	return group
}
