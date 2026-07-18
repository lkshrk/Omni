package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func printSkippedUnavailable(cmd *cobra.Command, skipped []string) {
	for _, s := range skipped {
		fmt.Fprintf(cmdOut(cmd), "  ! %s: skipped, agent CLI not found on PATH\n", s)
	}
}

func newAgentsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage AI-agent resources (skills)",
	}
	cmd.AddCommand(
		newAgentsSkillsCmd(state),
		newAgentsMcpCmd(state),
		newAgentsPluginsCmd(state),
		newAgentsAddCmd(state),
		newAgentsFindCmd(state),
	)
	return cmd
}

// agentErrsFailure turns already-printed per-agent "  ! agent/name: err"
// lines into a command failure: without it the process exits 0 on a partial
// failure and scripted callers chain onto an operation that did not happen.
func agentErrsFailure(n int) error {
	if n == 0 {
		return nil
	}
	return fmt.Errorf("%d agent operation(s) failed", n)
}

func newAgentsAddCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "add <source>",
		Short: "Add and install a skill package (owner/repo or github URL)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, err := state.app.AddSkillPackage(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", pkg.Source)
			return nil
		},
	}
}

func newAgentsFindCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "find <query>",
		Short: "Search skills.sh for skill packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := state.app.FindSkillPackages(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			for _, r := range results {
				fmt.Fprintf(cmdOut(cmd), "%s  (%s)  %s\n", r.Source, r.Skill, r.Installs)
			}
			return nil
		},
	}
}

func newAgentsSkillsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Restore and import agent skills",
	}
	cmd.AddCommand(
		newAgentsRestoreSkillsCmd(state),
		newAgentsImportSkillsCmd(state),
		newAgentsUpdateSkillsCmd(state),
		newAgentsRemoveSkillPackageCmd(state),
		newAgentsUninstallSkillPackageCmd(state),
		newAgentsSkillsGroupCmd(state),
	)
	return cmd
}

func newAgentsSkillsGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <source> <group>...",
		Short: "Set a skill package's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]
			groups := args[1:]
			if err := state.app.SetSkillGroups(source, groups, nil, ""); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", source, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsRemoveSkillPackageCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <source>",
		Short: "Remove a skill package from the manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveSkillPackage(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s from manifest\n", args[0])
			return nil
		},
	}
}

func newAgentsUninstallSkillPackageCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <source>",
		Short: "Uninstall a skill package from agent skill directories",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.UninstallSkillPackage(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "uninstalled %s\n", args[0])
			return nil
		},
	}
}

func newAgentsUpdateSkillsCmd(state *rootState) *cobra.Command {
	var opts app.UpdateSkillsOptions
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update manifest skills to their latest upstream versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			output, dryRun, err := state.app.UpdateSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.DryRun {
				fmt.Fprintln(cmdOut(cmd), dryRun)
				return nil
			}
			fmt.Fprint(cmdOut(cmd), output)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the skills update command without running it")
	return cmd
}

func newAgentsRestoreSkillsCmd(state *rootState) *cobra.Command {
	var opts app.RestoreSkillsOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Install the manifest skill set onto this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, lines, err := state.app.RestoreSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmdOut(cmd), "warn: %s\n", w)
			}
			if opts.DryRun {
				for _, l := range lines {
					fmt.Fprintln(cmdOut(cmd), l)
				}
				for _, s := range res.ShadowedByPlugin {
					fmt.Fprintf(cmdOut(cmd), "  skipped (provided by plugin): %s\n", s)
				}
				return nil
			}
			fmt.Fprintln(cmdOut(cmd), app.RestoreSkillsSummaryText(res))
			for _, f := range res.Failed {
				fmt.Fprintf(cmdOut(cmd), "  ! %s: %s\n", f.Name, f.Message)
			}
			for _, d := range res.Drift {
				fmt.Fprintf(cmdOut(cmd), "  ~ drift: %s changed since lock\n", d)
			}
			for _, s := range res.ShadowedByPlugin {
				fmt.Fprintf(cmdOut(cmd), "  skipped (provided by plugin): %s\n", s)
			}
			return agentErrsFailure(len(res.Failed))
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the skills add commands without running them")
	return cmd
}

func newAgentsImportSkillsCmd(state *rootState) *cobra.Command {
	var opts app.ImportSkillsOptions
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import CLI/UI-added skills from the lockfile into the manifest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			diff, err := state.app.ImportSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmdOut(cmd), app.ImportDiffSummaryText(diff))
			for _, n := range diff.Added {
				fmt.Fprintf(cmdOut(cmd), "  + %s\n", n)
			}
			for _, n := range diff.Updated {
				fmt.Fprintf(cmdOut(cmd), "  ~ %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show the manifest diff without writing")
	return cmd
}

func newAgentsMcpCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers in the agent manifest",
	}
	cmd.AddCommand(
		newAgentsMcpListCmd(state),
		newAgentsMcpAddCmd(state),
		newAgentsMcpRemoveCmd(state),
		newAgentsMcpRestoreCmd(state),
		newAgentsMcpImportCmd(state),
		newAgentsMcpGroupCmd(state),
	)
	return cmd
}

func newAgentsMcpGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <name> <group>...",
		Short: "Set an MCP server's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.SetMcpGroups(cmd.Context(), name, groups); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", name, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsMcpListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed and unmanaged MCP servers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			managed, unmanaged, err := state.app.McpServerRows(cmd.Context())
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, row := range managed {
				agentIDs := make([]string, 0, len(row.PerAgentStatus))
				for id := range row.PerAgentStatus {
					agentIDs = append(agentIDs, id)
				}
				sort.Strings(agentIDs)
				agentParts := make([]string, 0, len(agentIDs))
				for _, id := range agentIDs {
					marker := "-"
					switch row.PerAgentStatus[id] {
					case app.McpStatusInstalled:
						marker = "✓"
					case app.McpStatusShadowed:
						marker = "via-plugin"
					}
					agentParts = append(agentParts, fmt.Sprintf("%s(%s)", id, marker))
				}
				line := row.Name + "  " + row.Transport
				if len(agentParts) > 0 {
					line += "  " + strings.Join(agentParts, " ")
				}
				fmt.Fprintln(w, line)
			}
			agentIDs := make([]string, 0, len(unmanaged))
			for id := range unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "\n-- unmanaged (%s) --\n", id)
				for _, s := range unmanaged[id] {
					fmt.Fprintf(w, "%s  %s\n", s.Name, s.Transport)
				}
			}
			return nil
		},
	}
}

func newAgentsMcpAddCmd(state *rootState) *cobra.Command {
	var (
		name       string
		transport  string
		command    string
		url        string
		envVars    []string
		envLiteral []string
		agents     []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an MCP server to the manifest and install it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			switch transport {
			case "stdio":
				if command == "" {
					return fmt.Errorf("--command is required for stdio transport")
				}
				if url != "" {
					return fmt.Errorf("--url is not valid for stdio transport")
				}
			case "http", "sse":
				if url == "" {
					return fmt.Errorf("--url is required for %s transport", transport)
				}
				if command != "" {
					return fmt.Errorf("--command is not valid for %s transport", transport)
				}
			default:
				return fmt.Errorf("--transport must be stdio, http, or sse")
			}
			var envLit map[string]string
			if len(envLiteral) > 0 {
				envLit = make(map[string]string, len(envLiteral))
				for _, kv := range envLiteral {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return fmt.Errorf("--env-literal %q: must be KEY=VALUE", kv)
					}
					if _, dup := envLit[k]; dup {
						return fmt.Errorf("--env-literal %q: duplicate key %q", kv, k)
					}
					envLit[k] = v
				}
			}
			s := config.McpServer{
				Name:       name,
				Transport:  transport,
				Command:    command,
				URL:        url,
				Env:        envVars,
				EnvLiteral: envLit,
				Agents:     agents,
			}
			res, err := state.app.AddMcpServer(cmd.Context(), s)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", name)
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "server name (required)")
	cmd.Flags().StringVar(&transport, "transport", "", "transport type: stdio, http, or sse (required)")
	cmd.Flags().StringVar(&command, "command", "", "command for stdio transport")
	cmd.Flags().StringVar(&url, "url", "", "URL for http/sse transport")
	cmd.Flags().StringArrayVar(&envVars, "env", nil, "env var name to forward (repeatable)")
	cmd.Flags().StringArrayVar(&envLiteral, "env-literal", nil, "env var as KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsMcpRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an MCP server from the manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := state.app.RemoveMcpServer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
}

func newAgentsMcpRestoreCmd(state *rootState) *cobra.Command {
	var opts app.RestoreMcpOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Install the manifest MCP servers onto this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := state.app.RestoreMcpServers(cmd.Context(), opts)
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, msg := range res.Warnings {
				fmt.Fprintf(w, "warn: %s\n", msg)
			}
			if opts.DryRun {
				for _, s := range res.WouldInstall {
					fmt.Fprintf(w, "would install: %s\n", s)
				}
				return nil
			}
			for _, s := range res.Installed {
				fmt.Fprintf(w, "installed: %s\n", s)
			}
			for _, s := range res.AlreadyInstalled {
				fmt.Fprintf(w, "already installed: %s\n", s)
			}
			for _, s := range res.ShadowedByPlugin {
				fmt.Fprintf(w, "skipped (provided by plugin): %s\n", s)
			}
			for _, e := range res.Errors {
				fmt.Fprintf(w, "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
			}
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print what would be installed without running")
	return cmd
}

func newAgentsMcpImportCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "import [<name>]",
		Short: "List unmanaged MCP servers, or adopt one into the manifest by name",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			diff, err := state.app.ImportMcpServers(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return importMcpServerByName(cmd, state, diff, args[0])
			}
			w := cmdOut(cmd)
			agentIDs := make([]string, 0, len(diff.Unmanaged))
			for id := range diff.Unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "-- unmanaged (%s) --\n", id)
				for _, s := range diff.Unmanaged[id] {
					fmt.Fprintf(w, "%s\n", s.Name)
				}
			}
			return nil
		},
	}
}

// importMcpServerByName adopts an unmanaged server into the manifest, mirroring the
// TUI's doImportMcpServer (name/transport/command/url only, never env values — the
// InstalledMcpServer the adapter reports carries no env data to begin with).
func importMcpServerByName(cmd *cobra.Command, state *rootState, diff app.McpImportDiff, name string) error {
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for id := range diff.Unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	var match app.InstalledMcpServer
	found := false
	var matchedAgents []string
	for _, id := range agentIDs {
		for _, s := range diff.Unmanaged[id] {
			if s.Name != name {
				continue
			}
			if !found {
				match = s
				found = true
			} else if s.Transport != match.Transport || s.Command != match.Command || s.URL != match.URL {
				return fmt.Errorf("mcp server %q is unmanaged under multiple agents with conflicting configuration; import each manually", name)
			}
			matchedAgents = append(matchedAgents, id)
		}
	}
	if !found {
		return fmt.Errorf("mcp server %q is not unmanaged in any agent CLI", name)
	}

	s := config.McpServer{
		Name:      match.Name,
		Transport: match.Transport,
		Command:   match.Command,
		URL:       match.URL,
		Agents:    matchedAgents,
	}
	res, err := state.app.AddMcpServer(cmd.Context(), s)
	if err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.ServerName, e.Err)
	}
	printSkippedUnavailable(cmd, res.SkippedUnavailable)
	fmt.Fprintf(cmdOut(cmd), "imported %s\n", s.Name)
	return agentErrsFailure(len(res.Errors))
}

func newAgentsPluginsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage agent plugins and their marketplaces",
	}
	cmd.AddCommand(
		newAgentsPluginsListCmd(state),
		newAgentsPluginsAddCmd(state),
		newAgentsPluginsRemoveCmd(state),
		newAgentsPluginsRestoreCmd(state),
		newAgentsPluginsImportCmd(state),
		newAgentsPluginsGroupCmd(state),
		newAgentsPluginsMarketplaceCmd(state),
	)
	return cmd
}

func newAgentsPluginsGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <name> <group>...",
		Short: "Set a plugin's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.SetPluginGroups(cmd.Context(), name, groups); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", name, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsPluginsListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed and unmanaged plugins",
		RunE: func(cmd *cobra.Command, _ []string) error {
			managed, unmanaged, err := state.app.PluginRows(cmd.Context())
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, row := range managed {
				agentIDs := make([]string, 0, len(row.PerAgentStatus))
				for id := range row.PerAgentStatus {
					agentIDs = append(agentIDs, id)
				}
				sort.Strings(agentIDs)
				agentParts := make([]string, 0, len(agentIDs))
				for _, id := range agentIDs {
					marker := "✓"
					if row.PerAgentStatus[id] != app.PluginStatusInstalled {
						marker = "-"
					}
					agentParts = append(agentParts, fmt.Sprintf("%s(%s)", id, marker))
				}
				line := row.Name + "  " + row.Marketplace
				if row.Version != "" {
					line += "  " + row.Version
					if row.Outdated() {
						line += " → " + row.LatestVersion
					}
				}
				if len(agentParts) > 0 {
					line += "  " + strings.Join(agentParts, " ")
				}
				fmt.Fprintln(w, line)
			}
			agentIDs := make([]string, 0, len(unmanaged))
			for id := range unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "\n-- unmanaged (%s) --\n", id)
				for _, p := range unmanaged[id] {
					line := p.Name + "  " + p.Marketplace
					if p.Version != "" {
						line += "  " + p.Version
					}
					fmt.Fprintln(w, line)
				}
			}
			return nil
		},
	}
}

func newAgentsPluginsAddCmd(state *rootState) *cobra.Command {
	var (
		name        string
		marketplace string
		agents      []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a plugin to the manifest and install it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if marketplace == "" {
				return fmt.Errorf("--marketplace is required")
			}
			p := config.Plugin{Name: name, Marketplace: marketplace, Agents: agents}
			res, err := state.app.AddPlugin(cmd.Context(), p)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", name)
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "plugin name (required)")
	cmd.Flags().StringVar(&marketplace, "marketplace", "", "declared marketplace name (required)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsPluginsRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a plugin from the manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := state.app.RemovePlugin(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmdOut(cmd), "  ~ %s/%s: %v\n", w.AgentID, w.Name, w.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
}

func newAgentsPluginsRestoreCmd(state *rootState) *cobra.Command {
	var opts app.RestorePluginOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Install the manifest plugin set onto this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := state.app.RestorePlugins(cmd.Context(), opts)
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, msg := range res.Warnings {
				fmt.Fprintf(w, "warn: %s\n", msg)
			}
			if opts.DryRun {
				for _, p := range res.WouldInstall {
					fmt.Fprintf(w, "would install: %s\n", p)
				}
				return nil
			}
			for _, p := range res.Installed {
				fmt.Fprintf(w, "installed: %s\n", p)
			}
			for _, p := range res.AlreadyInstalled {
				fmt.Fprintf(w, "already installed: %s\n", p)
			}
			for _, e := range res.Errors {
				fmt.Fprintf(w, "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print what would be installed without running")
	return cmd
}

func newAgentsPluginsImportCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "import [<name>]",
		Short: "List unmanaged plugins, or adopt one into the manifest by name",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			diff, err := state.app.ImportPlugins(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return importPluginByName(cmd, state, diff, args[0])
			}
			w := cmdOut(cmd)
			agentIDs := make([]string, 0, len(diff.Unmanaged))
			for id := range diff.Unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "-- unmanaged (%s) --\n", id)
				for _, p := range diff.Unmanaged[id] {
					fmt.Fprintf(w, "%s\n", p.Name)
				}
			}
			return nil
		},
	}
}

// importPluginByName adopts an unmanaged plugin into the manifest: identity
// only (name + marketplace), never version. If the reported marketplace is
// not yet declared, it is adopted too, but only with a real source an
// already-targeted agent CLI reports — never a fabricated placeholder, since
// that would later be replayed as a real `marketplace add <fake>` on restore.
// If no real source is discoverable, the caller is told to declare it first.
func importPluginByName(cmd *cobra.Command, state *rootState, diff app.PluginImportDiff, name string) error {
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for id := range diff.Unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	var match app.InstalledPlugin
	found := false
	var matchedAgents []string
	for _, id := range agentIDs {
		for _, p := range diff.Unmanaged[id] {
			if p.Name != name {
				continue
			}
			if !found {
				match = p
				found = true
			} else if p.Marketplace != match.Marketplace {
				return fmt.Errorf("plugin %q is unmanaged under multiple agents with conflicting marketplaces; import each manually", name)
			}
			matchedAgents = append(matchedAgents, id)
		}
	}
	if !found {
		return fmt.Errorf("plugin %q is not unmanaged in any agent CLI", name)
	}

	failed := 0
	marketplaces, err := state.app.Marketplaces()
	if err != nil {
		return err
	}
	declared := false
	for _, m := range marketplaces {
		if m.Name == match.Marketplace {
			declared = true
			break
		}
	}
	if !declared {
		source, ok, err := state.app.FindUndeclaredMarketplace(cmd.Context(), match.Marketplace, matchedAgents)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("plugin %q references undeclared marketplace %q with no discoverable source; declare it first: omni agents plugins marketplace add %s --source <src>", name, match.Marketplace, match.Marketplace)
		}
		mres, err := state.app.AddMarketplace(cmd.Context(), config.Marketplace{Name: match.Marketplace, Source: source, Agents: matchedAgents})
		if err != nil {
			return err
		}
		for _, e := range mres.Errors {
			fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
		}
		printSkippedUnavailable(cmd, mres.SkippedUnavailable)
		failed += len(mres.Errors)
	}

	p := config.Plugin{Name: match.Name, Marketplace: match.Marketplace, Agents: matchedAgents}
	res, err := state.app.AddPlugin(cmd.Context(), p)
	if err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
	}
	printSkippedUnavailable(cmd, res.SkippedUnavailable)
	fmt.Fprintf(cmdOut(cmd), "imported %s\n", p.Name)
	return agentErrsFailure(failed + len(res.Errors))
}

func newAgentsPluginsMarketplaceCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "marketplace",
		Short: "Manage plugin marketplaces in the agent manifest",
	}
	cmd.AddCommand(
		newAgentsPluginsMarketplaceListCmd(state),
		newAgentsPluginsMarketplaceAddCmd(state),
		newAgentsPluginsMarketplaceRemoveCmd(state),
		newAgentsPluginsMarketplaceGroupCmd(state),
	)
	return cmd
}

func newAgentsPluginsMarketplaceGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "group <name> <group>...",
		Short: "Set a marketplace's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.SetMarketplaceGroups(cmd.Context(), name, groups); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", name, strings.Join(groups, ", "))
			return nil
		},
	}
}

func newAgentsPluginsMarketplaceListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List declared marketplaces",
		RunE: func(cmd *cobra.Command, _ []string) error {
			marketplaces, err := state.app.Marketplaces()
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, m := range marketplaces {
				fmt.Fprintf(w, "%s  %s\n", m.Name, m.Source)
			}
			return nil
		},
	}
}

func newAgentsPluginsMarketplaceAddCmd(state *rootState) *cobra.Command {
	var (
		source string
		agents []string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Declare a marketplace and add it to targeted agent CLIs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			m := config.Marketplace{Name: args[0], Source: source, Agents: agents}
			res, err := state.app.AddMarketplace(cmd.Context(), m)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			printSkippedUnavailable(cmd, res.SkippedUnavailable)
			return agentErrsFailure(len(res.Errors))
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "marketplace source, owner/repo or URL (required)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsPluginsMarketplaceRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a marketplace from the manifest only (agents keep theirs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveMarketplace(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			return nil
		},
	}
}
