package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

func (a *App) doctorAgents(ctx context.Context, result *DoctorResult, cfg *config.RootConfig) {
	if !a.AgentsEnabled(cfg) {
		result.addCheck("agents", "Agent features", DoctorStatusOK, "disabled (agents_disabled)")
		return
	}
	check := DoctorCheck{ID: "agents", Label: "Agent features", Status: DoctorStatusOK}
	var summary []string
	appendGroup := func(name string, g DoctorDetailGroup, healthy bool) {
		check.Groups = append(check.Groups, g)
		state := "ok"
		if !healthy {
			state = "warn"
			check.Status = DoctorStatusWarn
		}
		summary = append(summary, name+" "+state)
	}

	g, healthy := a.doctorAgentsSkills(ctx, cfg)
	appendGroup("skills", g, healthy)
	g, healthy = a.doctorAgentsMcp(ctx, cfg)
	appendGroup("mcp", g, healthy)
	g, healthy = a.doctorAgentsPlugins(ctx, cfg)
	appendGroup("plugins", g, healthy)

	check.Message = strings.Join(summary, ", ")
	result.Checks = append(result.Checks, check)
}

func (a *App) doctorAgentsSkills(ctx context.Context, cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "skills"}
	if !a.SkillsEnabled(cfg) {
		g.Items = append(g.Items, "disabled (skills_disabled)")
		return g, true
	}
	healthy := true
	needsGit := false
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Ref != "" || agent.SourceRequiresGit(pkg.Source) {
			needsGit = true
			break
		}
	}
	if !needsGit {
		g.Items = append(g.Items, "git: not required")
	} else if _, err := lookPath("git"); err != nil {
		g.Items = append(g.Items, "git: not found on PATH")
		healthy = false
	} else {
		g.Items = append(g.Items, "git: ok")
	}
	rows, unmanaged, err := a.SkillPackageRowState(ctx)
	if err != nil {
		g.Items = append(g.Items, fmt.Sprintf("packages: %v", err))
		return g, false
	}
	counts := classifySkillRows(rows)
	g.Items = append(g.Items, fmt.Sprintf(
		"packages: %d in manifest, %d installed, %d missing", len(rows), counts.Installed, len(counts.Missing)))
	if len(counts.Missing) > 0 {
		healthy = false
	}
	for _, r := range rows {
		if r.Error != "" {
			g.Items = append(g.Items, fmt.Sprintf("packages: %s: %s", r.Source, r.Error))
			healthy = false
			continue
		}
		for _, item := range skillDriftItems(r) {
			g.Items = append(g.Items, item)
			healthy = false
		}
		if w := r.UnknownAgentsWarning(); w != "" {
			g.Items = append(g.Items, w)
			healthy = false
		}
	}
	// An available update is not breakage, so it reports without flipping the group.
	if item := skillOutdatedItem(rows); item != "" {
		g.Items = append(g.Items, item)
	}
	// An unadopted legacy install is intent not yet expressed, not breakage.
	if len(unmanaged) > 0 {
		g.Items = append(g.Items, fmt.Sprintf(
			"%d legacy skill package(s) not in manifest (\"omni agents skills import\" claims them)", len(unmanaged)))
	}
	report, err := a.skillStoreFix(ctx, cfg, true)
	for _, item := range skillStoreItems(report) {
		g.Items = append(g.Items, item)
		healthy = false
	}
	if err != nil {
		g.Items = append(g.Items, "store: "+err.Error())
		healthy = false
	}
	return g, healthy
}

// Each item carries its fix, so the remedy travels with the finding.
func skillStoreItems(report SkillStoreFixReport) []string {
	var items []string
	if n := len(report.Debris); n > 0 {
		items = append(items, fmt.Sprintf(
			"store: %d leftover artifact(s) from an interrupted operation; \"omni doctor --fix\" removes them", n))
	}
	if n := len(report.DanglingLinks); n > 0 {
		items = append(items, fmt.Sprintf(
			"store: %d skill link(s) point at a removed package; \"omni doctor --fix\" removes them", n))
	}
	if n := len(report.OrphanedPackages); n > 0 {
		items = append(items, fmt.Sprintf(
			"store: %d installed package(s) no manifest entry references; \"omni doctor --fix\" removes them", n))
	}
	if n := len(report.RebuiltMetadata); n > 0 {
		items = append(items, fmt.Sprintf(
			"store: %d package(s) missing local install metadata; \"omni doctor --fix\" rebuilds it", n))
	}
	return items
}

// An unknown verdict is not a finding.
func skillOutdatedItem(rows []SkillPackageRow) string {
	var names []string
	for _, r := range rows {
		if r.Outdated == SkillOutdatedBehind {
			names = append(names, r.Source)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return fmt.Sprintf("%d package(s) behind their source (\"omni agents skills upgrade\" refreshes them): %s",
		len(names), strings.Join(names, ", "))
}

func skillDriftItems(r SkillPackageRow) []string {
	agents := make([]string, 0, len(r.PerAgentStatus))
	for id, status := range r.PerAgentStatus {
		if status == SkillStatusDrifted {
			agents = append(agents, id)
		}
	}
	sort.Strings(agents)
	items := make([]string, 0, len(agents))
	for _, id := range agents {
		items = append(items, fmt.Sprintf(
			"%s: drifted on %s; \"omni agents skills sync\" converges an identical copy, "+
				"differing content needs %s or \"omni agents skills import\"",
			r.Source, id, skillDriftRemedy))
	}
	return items
}

func (a *App) doctorAgentsMcp(ctx context.Context, cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "mcp servers"}
	if !a.McpEnabled(cfg) {
		g.Items = append(g.Items, "disabled (mcp_disabled)")
		return g, true
	}
	healthy := doctorAdapterItems(&g, adapterAvailability(a.mcpAdapters()))
	g.Items = append(g.Items, fmt.Sprintf("servers: %d in manifest", len(cfg.Agents.McpServers)))
	rows, unmanaged, err := a.McpServerRows(ctx)
	if err != nil {
		g.Items = append(g.Items, fmt.Sprintf("servers: %v", err))
		return g, false
	}
	for _, item := range mcpDriftItems(rows) {
		g.Items = append(g.Items, item)
		healthy = false
	}
	// A server another tool registered is intent not yet expressed, not breakage.
	if item := unmanagedCountItem(
		countUnmanaged(unmanaged, cfg.Agents.Ignore.McpServers, func(s InstalledMcpServer) string { return s.Name }),
		"mcp server(s) not in manifest", "omni agents mcp import"); item != "" {
		g.Items = append(g.Items, item)
	}
	return g, healthy
}

func (a *App) doctorAgentsPlugins(ctx context.Context, cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "plugins"}
	if !a.PluginsEnabled(cfg) {
		g.Items = append(g.Items, "disabled (plugins_disabled)")
		return g, true
	}
	healthy := doctorAdapterItems(&g, adapterAvailability(a.pluginAdapters()))
	g.Items = append(g.Items, fmt.Sprintf("plugins: %d in manifest, marketplaces: %d", len(cfg.Agents.Plugins), len(cfg.Agents.Marketplaces)))
	rows, unmanaged, err := a.PluginRows(ctx)
	if err != nil {
		g.Items = append(g.Items, fmt.Sprintf("plugins: %v", err))
		return g, false
	}
	for _, item := range pluginDriftItems(rows) {
		g.Items = append(g.Items, item)
		healthy = false
	}
	// An available update is not breakage, so it reports without flipping the group.
	if item := pluginOutdatedItem(rows); item != "" {
		g.Items = append(g.Items, item)
	}
	if item := unmanagedCountItem(
		countUnmanaged(unmanaged, cfg.Agents.Ignore.Plugins, func(p InstalledPlugin) string { return p.Name }),
		"plugin(s) not in manifest", "omni agents plugins import"); item != "" {
		g.Items = append(g.Items, item)
	}
	if _, err := lookPath("claude"); err == nil {
		g.Items = append(g.Items, doctorClaudeShaSourceItem())
	}
	return g, healthy
}

func countUnmanaged[T any](byAgent map[string][]T, ignored []string, name func(T) string) int {
	ignoredNames := make(map[string]bool, len(ignored))
	for _, entry := range ignored {
		ignoredNames[entry] = true
	}
	seen := make(map[string]bool)
	for _, entries := range byAgent {
		for _, entry := range entries {
			entryName := name(entry)
			if !ignoredNames[entryName] {
				seen[entryName] = true
			}
		}
	}
	return len(seen)
}

func unmanagedCountItem(n int, label, verb string) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s (%q claims them)", n, label, verb)
}

func mcpDriftItems(rows []McpServerRow) []string {
	var items []string
	for _, r := range rows {
		agents := make([]string, 0, len(r.DriftFields))
		for id := range r.DriftFields {
			agents = append(agents, id)
		}
		sort.Strings(agents)
		for _, id := range agents {
			items = append(items, fmt.Sprintf(
				"%s: drifted on %s (%s differ from the manifest); resolve with %s",
				r.Name, id, strings.Join(r.DriftFields[id], ", "), mcpDriftRemedy))
		}
	}
	return items
}

func pluginDriftItems(rows []PluginRow) []string {
	var items []string
	for _, r := range rows {
		agents := make([]string, 0, len(r.DriftMarketplaces))
		for id := range r.DriftMarketplaces {
			agents = append(agents, id)
		}
		sort.Strings(agents)
		for _, id := range agents {
			items = append(items, fmt.Sprintf(
				"%s: drifted on %s (installed from %s, manifest declares %s); resolve with %s",
				r.Name, id, r.DriftMarketplaces[id], r.Marketplace, pluginDriftRemedy))
		}
	}
	return items
}

func pluginOutdatedItem(rows []PluginRow) string {
	var names []string
	for _, r := range rows {
		if !r.Drifted && r.Outdated() {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	// Plugin updates have no CLI verb yet, so the hint names the Agents tab rather than a command that does not exist.
	return fmt.Sprintf("%d plugin(s) behind their marketplace (update them from the Agents tab): %s",
		len(names), strings.Join(names, ", "))
}

func doctorClaudeShaSourceItem() string {
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
		if data, readErr := os.ReadFile(path); readErr == nil {
			var file struct {
				Plugins map[string][]struct {
					GitCommitSha string `json:"gitCommitSha"`
				} `json:"plugins"`
			}
			if json.Unmarshal(data, &file) == nil {
				return "update detection: sha source ok"
			}
		}
	}
	return "update detection: sha source unavailable (plugin update checks limited to declared versions)"
}

type adapterProbe struct {
	id        string
	available bool
}

func adapterAvailability[T interface {
	ID() string
	Available() bool
}](adapters []T) []adapterProbe {
	out := make([]adapterProbe, 0, len(adapters))
	for _, ad := range adapters {
		out = append(out, adapterProbe{id: ad.ID(), available: ad.Available()})
	}
	return out
}

func doctorAdapterItems(g *DoctorDetailGroup, probes []adapterProbe) bool {
	healthy := true
	for _, p := range probes {
		if p.available {
			g.Items = append(g.Items, fmt.Sprintf("agent %s: ok", p.id))
		} else {
			g.Items = append(g.Items, fmt.Sprintf("agent %s: binary not found on PATH", p.id))
			healthy = false
		}
	}
	return healthy
}
