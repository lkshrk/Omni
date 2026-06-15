package app

import (
	"os"
	"path/filepath"
)

// AgentInfo describes one AI coding agent the skills CLI can target. Data is
// derived from the upstream skills CLI src/agents.ts: configDir is the
// home-relative config directory whose existence signals the agent is
// installed, and skillsSub is the skills subdirectory the CLI symlinks into.
type AgentInfo struct {
	ID        string // skills-CLI identifier passed to `-a`
	Display   string
	configDir string // home-relative config dir; existence => installed
	configEnv string // optional env var overriding the absolute config dir
	skillsSub string // skills dir relative to the config dir
}

// supportedAgents lists the agents omni knows how to detect and target. The
// upstream CLI supports many more; this is the curated, verified subset.
var supportedAgents = []AgentInfo{
	{ID: "claude-code", Display: "Claude Code", configDir: ".claude", configEnv: "CLAUDE_CONFIG_DIR", skillsSub: "skills"},
	{ID: "codex", Display: "Codex", configDir: ".codex", configEnv: "CODEX_HOME", skillsSub: "skills"},
	{ID: "cursor", Display: "Cursor", configDir: ".cursor", skillsSub: "skills"},
	{ID: "opencode", Display: "OpenCode", configDir: ".config/opencode", skillsSub: "skills"},
	{ID: "gemini-cli", Display: "Gemini CLI", configDir: ".gemini", skillsSub: "skills"},
	{ID: "windsurf", Display: "Windsurf", configDir: ".codeium/windsurf", skillsSub: "skills"},
	{ID: "github-copilot", Display: "GitHub Copilot", configDir: ".copilot", skillsSub: "skills"},
	{ID: "continue", Display: "Continue", configDir: ".continue", skillsSub: "skills"},
	{ID: "goose", Display: "Goose", configDir: ".config/goose", skillsSub: "skills"},
	{ID: "amp", Display: "Amp", configDir: ".config/agents", skillsSub: "skills"},
}

func agentConfigPath(home string, a AgentInfo) string {
	if a.configEnv != "" {
		if v := os.Getenv(a.configEnv); v != "" {
			return v
		}
	}
	return filepath.Join(home, a.configDir)
}

func agentSkillsPath(home string, a AgentInfo) string {
	return filepath.Join(agentConfigPath(home, a), a.skillsSub)
}

// InstalledAgents returns the supported agents whose config directory exists on
// this machine, in catalog order.
func InstalledAgents(home string) []AgentInfo {
	out := make([]AgentInfo, 0, len(supportedAgents))
	for _, a := range supportedAgents {
		if _, err := os.Stat(agentConfigPath(home, a)); err == nil {
			out = append(out, a)
		}
	}
	return out
}
