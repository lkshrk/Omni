package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// apmDeployRoot — at user scope APM deploys relative to $HOME, so every path its lockfile records is resolved against this directory.
func (a *App) apmDeployRoot() (string, error) {
	if home := strings.TrimSpace(a.lookupEnv("HOME")); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home for APM state: %w", err)
	}
	return home, nil
}

func (a *App) globalAPMManifestPath() (string, error) {
	home, err := a.apmDeployRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".apm", "apm.yml"), nil
}

func (a *App) effectiveAgentTargets(cfg *config.RootConfig) []string {
	if use := a.effectiveSettings(cfg).AgentsUse; use != nil {
		return use
	}
	home := strings.TrimSpace(a.lookupEnv("HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	agents := a.installedAgents(home)
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	return ids
}

var apmTargetsByAgentID = map[string]string{
	"antigravity":    "antigravity",
	"claude-code":    "claude",
	"codex":          "codex",
	"cursor":         "cursor",
	"gemini-cli":     "gemini",
	"github-copilot": "copilot",
	"grok":           "grok-build",
	// Experimental in APM's target tiers, but a valid --target and a valid per-dependency targets entry.
	"hermes-agent": "hermes",
	"kiro-cli":     "kiro",
	"opencode":     "opencode",
	"windsurf":     "windsurf",
}

// User-scope deploy roots per target (apm_cli.integration.targets.KNOWN_TARGETS, 0.28.0), limited to what apmTargetsByAgentID can emit.
var apmUserRootsByTarget = map[string][]string{
	"antigravity": {filepath.Join(".gemini", "antigravity-cli")},
	"claude": {
		filepath.Join(".claude", "agents"),
		filepath.Join(".claude", "commands"),
		filepath.Join(".claude", "hooks"),
		filepath.Join(".claude", "rules"),
		filepath.Join(".claude", "skills"),
	},
	"codex":      {".agents", filepath.Join(".codex", "agents")},
	"copilot":    {".agents", ".copilot"},
	"cursor":     {".cursor"},
	"gemini":     {".agents", ".gemini"},
	"grok-build": {".grok"},
	"hermes":     {".hermes"},
	"kiro":       {".kiro"},
	"opencode":   {".agents", filepath.Join(".config", "opencode")},
	"windsurf":   {".agents", filepath.Join(".codeium", "windsurf")},
}

// Files APM merges package hooks into, so a failed batch leaves entries behind in a file that existed long before it.
var apmHookConfigByTarget = map[string]string{
	"claude":   filepath.Join(".claude", "settings.json"),
	"codex":    filepath.Join(".codex", "hooks.json"),
	"cursor":   filepath.Join(".cursor", "hooks.json"),
	"gemini":   filepath.Join(".gemini", "settings.json"),
	"windsurf": filepath.Join(".codeium", "windsurf", "hooks.json"),
}

// User-scope MCP registrations APM rewrites in place (apm_cli.adapters.client, 0.28.0), for the targets whose adapter declares supports_user_scope; cursor, opencode and grok-build write none.
var apmMcpConfigByTarget = map[string]string{
	"antigravity": filepath.Join(".gemini", "config", "mcp_config.json"),
	"claude":      ".claude.json",
	"codex":       filepath.Join(".codex", "config.toml"),
	"copilot":     filepath.Join(".copilot", "mcp-config.json"),
	"gemini":      filepath.Join(".gemini", "settings.json"),
	"hermes":      filepath.Join(".hermes", "config.yaml"),
	"kiro":        filepath.Join(".kiro", "settings", "mcp.json"),
	"windsurf":    filepath.Join(".codeium", "windsurf", "mcp_config.json"),
}

// apmMcpConfigsForTargets returns absolute paths; claude, codex and hermes each relocate theirs through an env override.
func (a *App) apmMcpConfigsForTargets(targets []string) []string {
	root, err := a.apmDeployRoot()
	if err != nil {
		return nil
	}
	var files []string
	for _, target := range targets {
		target = strings.TrimSpace(target)
		rel := apmMcpConfigByTarget[target]
		if rel == "" {
			continue
		}
		path := filepath.Join(root, rel)
		switch target {
		case "claude":
			if resolved := a.claudeUserMcpConfigPath(); resolved != "" {
				path = resolved
			}
		case "codex":
			if home := strings.TrimSpace(a.lookupEnv("CODEX_HOME")); home != "" {
				path = filepath.Join(home, "config.toml")
			}
		case "hermes":
			if home := strings.TrimSpace(a.lookupEnv("HERMES_HOME")); home != "" {
				path = filepath.Join(home, "config.yaml")
			}
		}
		if !slices.Contains(files, path) {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

func apmUserRootsForTargets(targets []string) []string {
	var roots []string
	for _, target := range targets {
		for _, root := range apmUserRootsByTarget[strings.TrimSpace(target)] {
			if !slices.Contains(roots, root) {
				roots = append(roots, root)
			}
		}
	}
	sort.Strings(roots)
	return roots
}

func apmHookConfigsForTargets(targets []string) []string {
	var files []string
	for _, target := range targets {
		if file := apmHookConfigByTarget[strings.TrimSpace(target)]; file != "" && !slices.Contains(files, file) {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files
}

// apmSelectableTargets — the widest target set any surface install on this host can deploy into.
func (a *App) apmSelectableTargets(cfg *config.RootConfig) []string {
	targets, _ := migrateAPMTargets(a.effectiveAgentTargets(cfg))
	sort.Strings(targets)
	return targets
}

// legacyAgentTargets inverts migrateAPMTargets for the import direction; APM targets omni has no agent id for come back as unsupported rather than being invented.
func legacyAgentTargets(targets []string) (agents, unsupported []string) {
	inverse := make(map[string]string, len(apmTargetsByAgentID))
	for agentID, target := range apmTargetsByAgentID {
		inverse[target] = agentID
	}
	for _, target := range targets {
		agentID := inverse[strings.TrimSpace(target)]
		if agentID == "" {
			unsupported = append(unsupported, target)
			continue
		}
		if !slices.Contains(agents, agentID) {
			agents = append(agents, agentID)
		}
	}
	sort.Strings(agents)
	return agents, unsupported
}

func migrateAPMTargets(legacy []string) ([]string, []string) {
	targets := make([]string, 0, len(legacy))
	unsupported := make([]string, 0)
	for _, target := range legacy {
		mapped := apmTargetsByAgentID[target]
		if mapped == "" {
			unsupported = append(unsupported, target)
			continue
		}
		if !slices.Contains(targets, mapped) {
			targets = append(targets, mapped)
		}
	}
	return targets, unsupported
}
