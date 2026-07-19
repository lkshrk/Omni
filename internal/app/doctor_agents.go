package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

func (a *App) doctorAgents(result *DoctorResult, cfg *config.RootConfig) {
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

	g, healthy := a.doctorAgentsSkills(cfg)
	appendGroup("skills", g, healthy)
	g, healthy = a.doctorAgentsMcp(cfg)
	appendGroup("mcp", g, healthy)
	g, healthy = a.doctorAgentsPlugins(cfg)
	appendGroup("plugins", g, healthy)

	check.Message = strings.Join(summary, ", ")
	result.Checks = append(result.Checks, check)
}

func (a *App) doctorAgentsSkills(cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "skills"}
	if !a.SkillsEnabled(cfg) {
		g.Items = append(g.Items, "disabled (skills_disabled)")
		return g, true
	}
	healthy := true
	runner := skillRunner(nodeManager(cfg))
	if _, err := lookPath(runner); err != nil {
		g.Items = append(g.Items, fmt.Sprintf("runner %s: not found on PATH", runner))
		healthy = false
	} else {
		g.Items = append(g.Items, fmt.Sprintf("runner %s: ok", runner))
	}
	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		g.Items = append(g.Items, fmt.Sprintf("packages: %v", err))
		return g, false
	}
	installed := 0
	for _, r := range rows {
		if r.Installed {
			installed++
		}
	}
	missing := len(rows) - installed
	g.Items = append(g.Items, fmt.Sprintf("packages: %d in manifest, %d installed, %d missing", len(rows), installed, missing))
	if missing > 0 {
		healthy = false
	}
	return g, healthy
}

func (a *App) doctorAgentsMcp(cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "mcp servers"}
	if !a.McpEnabled(cfg) {
		g.Items = append(g.Items, "disabled (mcp_disabled)")
		return g, true
	}
	healthy := doctorAdapterItems(&g, adapterAvailability(a.mcpAdapters()))
	g.Items = append(g.Items, fmt.Sprintf("servers: %d in manifest", len(cfg.Agents.McpServers)))
	return g, healthy
}

func (a *App) doctorAgentsPlugins(cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "plugins"}
	if !a.PluginsEnabled(cfg) {
		g.Items = append(g.Items, "disabled (plugins_disabled)")
		return g, true
	}
	healthy := doctorAdapterItems(&g, adapterAvailability(a.pluginAdapters()))
	g.Items = append(g.Items, fmt.Sprintf("plugins: %d in manifest, marketplaces: %d", len(cfg.Agents.Plugins), len(cfg.Agents.Marketplaces)))
	if _, err := lookPath("claude"); err == nil {
		g.Items = append(g.Items, doctorClaudeShaSourceItem())
	}
	return g, healthy
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
