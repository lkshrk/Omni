package app

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

const (
	replacedSectionTitle = "Replaced by this manifest (delete by hand after sync):"
	retainedSectionTitle = "Retained (not migrated):"
	managedSectionTitle  = "Already managed by APM:"

	legacyObservationTarget = "snapshot"
)

// AgentsMigrationPreview is the read-only extractor output: one manifest plus what it replaces, keeps, and skips.
type AgentsMigrationPreview struct {
	Manifest string
	Replaced []agentDisposition
	Retained []agentDisposition
	Managed  []agentDisposition
	Blockers []string
}

func (p AgentsMigrationPreview) Render() string {
	var out strings.Builder
	out.WriteString(p.Manifest)
	writeMigrationSection(&out, replacedSectionTitle, p.Replaced, func(d agentDisposition) string {
		return migrationRow(d, replacedEvidence(d))
	})
	writeMigrationSection(&out, retainedSectionTitle, p.Retained, func(d agentDisposition) string {
		return migrationRow(d, d.Reason)
	})
	writeMigrationSection(&out, managedSectionTitle, p.Managed, func(d agentDisposition) string {
		return migrationRow(d, "")
	})
	return out.String()
}

func writeMigrationSection(out *strings.Builder, title string, dispositions []agentDisposition, row func(agentDisposition) string) {
	if len(dispositions) == 0 {
		return
	}
	rows := make([]string, 0, len(dispositions))
	for _, disposition := range dispositions {
		rows = append(rows, row(disposition))
	}
	sort.Strings(rows)
	out.WriteString(title + "\n")
	for _, line := range slices.Compact(rows) {
		out.WriteString("  " + line + "\n")
	}
}

func migrationRow(disposition agentDisposition, last string) string {
	observation := disposition.Observation
	row := observation.Target + "  " + observation.Kind + "  " + observation.Identity
	if last = strings.TrimSpace(last); last != "" {
		row += "  " + last
	}
	return row
}

func replacedEvidence(disposition agentDisposition) string {
	evidence := strings.Join(disposition.Observation.Evidence, ", ")
	if disposition.Action != agentActionSuppress {
		return evidence
	}
	owned := "owned by " + disposition.Owner
	if evidence == "" {
		return owned
	}
	return owned + "; " + evidence
}

func renderAgentsMigrationPreview(plan agentBundlePlan, dispositions []agentDisposition) (AgentsMigrationPreview, error) {
	deps := make([]apmPackageDep, 0, len(plan.Owners))
	for _, owner := range plan.Owners {
		deps = append(deps, owner.Dependency)
	}
	render, err := buildAPMRender(plan.Decls, deps)
	if err != nil {
		return AgentsMigrationPreview{}, err
	}
	body, err := encodeAPMManifest(render.Manifest)
	if err != nil {
		return AgentsMigrationPreview{}, err
	}
	var manifest strings.Builder
	manifest.WriteString(agentsMigrationMarker + "\n")
	manifest.WriteString(body)
	for _, command := range render.Commands {
		manifest.WriteString("# " + command + "\n")
	}
	for _, name := range slices.Sorted(maps.Keys(render.MCPReach)) {
		reach := render.MCPReach[name]
		if len(reach) == 0 || len(reach) >= len(render.Manifest.Targets) {
			continue
		}
		manifest.WriteString("# reach: " + strings.Join(reach, ", ") + " (apm deploys to all MCP targets): " + name + "\n")
	}

	preview := AgentsMigrationPreview{Manifest: manifest.String(), Blockers: plan.Blockers}
	for _, disposition := range dispositions {
		switch disposition.Action {
		case agentActionImport, agentActionSuppress:
			preview.Replaced = append(preview.Replaced, disposition)
		case agentActionRetain:
			preview.Retained = append(preview.Retained, disposition)
		case agentActionManaged:
			preview.Managed = append(preview.Managed, disposition)
		}
	}
	for _, suppressed := range plan.Suppressed {
		preview.Retained = append(preview.Retained, legacySuppressedDisposition(suppressed))
	}
	return preview, nil
}

// legacySuppressedDisposition reads back the "<kind> <identity> owned by <owner>" lines planAgentBundles records.
func legacySuppressedDisposition(suppressed string) agentDisposition {
	head, owner, found := strings.Cut(suppressed, " owned by ")
	if !found {
		return agentDisposition{Observation: agentObservation{Target: legacyObservationTarget, Identity: suppressed}, Action: agentActionRetain}
	}
	kind, identity, _ := strings.Cut(head, " ")
	return agentDisposition{
		Observation: agentObservation{Source: legacyObservationTarget, Target: legacyObservationTarget, Kind: kind, Identity: identity},
		Action:      agentActionRetain,
		Owner:       owner,
		Reason:      "owned by " + owner,
	}
}

func mergeLegacyAgentDecls(base, extra config.LegacyAgentDecls) config.LegacyAgentDecls {
	return config.LegacyAgentDecls{
		Packages:     mergeLegacyDeclMap(base.Packages, extra.Packages),
		Plugins:      mergeLegacyDeclMap(base.Plugins, extra.Plugins),
		Marketplaces: mergeLegacyDeclMap(base.Marketplaces, extra.Marketplaces),
		MCPServers:   mergeLegacyDeclMap(base.MCPServers, extra.MCPServers),
	}
}

// The snapshot declaration wins: it is what the operator wrote, not what a client happens to hold today.
func mergeLegacyDeclMap(base, extra map[string]json.RawMessage) map[string]json.RawMessage {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(base)+len(extra))
	maps.Copy(out, extra)
	maps.Copy(out, base)
	return out
}

// apmManagedIndex answers whether APM already deploys an observed item; a missing lockfile manages nothing.
type apmManagedIndex struct {
	modules string
	servers map[string]bool
	plugins map[string][]string
}

func loadAPMManagedIndex() (apmManagedIndex, error) {
	lock, err := readAPMLockfile()
	if err != nil {
		return apmManagedIndex{}, err
	}
	if len(lock.Dependencies) == 0 && len(lock.MCPServers) == 0 {
		return apmManagedIndex{}, nil
	}
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return apmManagedIndex{}, err
	}
	index := apmManagedIndex{
		modules: filepath.Join(dir, "apm_modules"),
		servers: make(map[string]bool, len(lock.MCPServers)),
		plugins: make(map[string][]string, len(lock.Dependencies)),
	}
	for _, name := range lock.MCPServers {
		index.servers[name] = true
	}
	for _, dep := range lock.Dependencies {
		repo := apmNormalizeRepo(dep.RepoURL)
		for _, name := range []string{dep.Name, dep.MarketplacePluginName} {
			if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
				index.plugins[name] = append(index.plugins[name], repo)
			}
		}
	}
	return index, nil
}

func (i apmManagedIndex) managesMCP(entry legacyEntry) bool {
	if i.modules == "" {
		return false
	}
	if i.servers[entry.Name] {
		return true
	}
	paths := []string{entry.Command, entry.Cwd}
	if len(entry.Args) > 0 {
		paths = append(paths, entry.Args[0])
	}
	return slices.ContainsFunc(paths, i.underModules)
}

func (i apmManagedIndex) underModules(value string) bool {
	if value = strings.TrimSpace(value); value == "" {
		return false
	}
	clean := filepath.Clean(value)
	return clean == i.modules || strings.HasPrefix(clean, i.modules+string(os.PathSeparator))
}

// A native plugin is managed when a lock dependency carries its name and, when the marketplace source is known, its repo.
func (i apmManagedIndex) managesPlugin(name, source string) bool {
	repos, ok := i.plugins[strings.ToLower(name)]
	if !ok {
		return false
	}
	if strings.TrimSpace(source) == "" {
		return true
	}
	return slices.Contains(repos, apmNormalizeRepo(source))
}

func subtractAPMManaged(dispositions []agentDisposition, index apmManagedIndex) []agentDisposition {
	if index.modules == "" {
		return dispositions
	}
	sources := map[string]string{}
	for _, disposition := range dispositions {
		if disposition.Observation.Kind == agentKindMarketplace {
			sources[disposition.Observation.Identity] = disposition.Observation.Definition.Source
		}
	}
	out := slices.Clone(dispositions)
	for i, disposition := range out {
		if disposition.Action != agentActionImport {
			continue
		}
		observation := disposition.Observation
		switch observation.Kind {
		case agentKindMCP:
			if index.managesMCP(observation.Definition) {
				out[i].Action = agentActionManaged
			}
		case agentKindPlugin:
			name, marketplace := splitNativePluginIdentity(observation.Identity)
			if index.managesPlugin(name, sources[marketplace]) {
				out[i].Action = agentActionManaged
			}
		}
	}
	return out
}
