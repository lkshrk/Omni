package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

func (a *App) globalAPMManifestPath() (string, error) {
	home := strings.TrimSpace(a.lookupEnv("HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home for APM manifest: %w", err)
		}
		home = userHome
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

func migrateAPMTargets(legacy []string) ([]string, []string) {
	targets := make([]string, 0, len(legacy))
	unsupported := make([]string, 0)
	for _, target := range legacy {
		mapped := map[string]string{
			"antigravity":    "antigravity",
			"claude-code":    "claude",
			"codex":          "codex",
			"cursor":         "cursor",
			"gemini-cli":     "gemini",
			"github-copilot": "copilot",
			"grok":           "grok-build",
			"kiro-cli":       "kiro",
			"opencode":       "opencode",
			"windsurf":       "windsurf",
		}[target]
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
