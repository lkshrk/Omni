package config

// agentConfigDirs lists the home-relative config directories of every AI
// coding agent omni's skills feature knows about (mirrors
// internal/app/agents_catalog.go supportedAgents[].configDir). Duplicated
// here because internal/config cannot import internal/app (app imports
// config). Used only for agent-detection consistency with the catalog
// (AgentConfigDirs()); dots-tracking exclusion uses agentDotsManagedPaths
// instead, since ".agents" as a whole is not machine-managed — only
// ".agents/skills" is.
var agentConfigDirs = []string{
	".aider-desk",
	".config/agents",
	".gemini/antigravity",
	".gemini/antigravity-cli",
	".astrbot/data",
	".autohand",
	".augment",
	".bob",
	".claude",
	".openclaw",
	".agents",
	".codeartsdoer",
	".codebuddy",
	".codemaker",
	".codestudio",
	".codex",
	".commandcode",
	".continue",
	".snowflake/cortex",
	".config/crush",
	".cursor",
	".deepagents/agent",
	".config/devin",
	".factory",
	".firebender",
	".forge",
	".gemini",
	".copilot",
	".config/goose",
	".grok",
	".hermes",
	".inferencesh",
	".jazz",
	".junie",
	".iflow",
	".kilocode",
	".kiro",
	".kode",
	".lingma",
	".mcpjam",
	".vibe",
	".moxby",
	".mux",
	".config/opencode",
	".openhands",
	".ona",
	".pi/agent",
	".qoder",
	".qoder-cn",
	".qwen",
	".reasonix",
	".rovodev",
	".roo",
	".tabnine/agent",
	".terramind",
	".tinycloud",
	".trae",
	".trae-cn",
	".codeium/windsurf",
	".zencoder",
	".neovate",
	".pochi",
	".adal",
}

// AgentConfigDirs returns the home-relative config directories of every AI
// coding agent omni's skills feature can target. Mirrors
// internal/app/agents_catalog.go supportedAgents[].configDir; used to keep
// that catalog and this list from silently drifting apart.
func AgentConfigDirs() []string {
	out := make([]string, len(agentConfigDirs))
	copy(out, agentConfigDirs)
	return out
}

// agentDotsManagedPaths lists home-relative paths that dots discovery and the
// v13->v14 migration treat as machine-managed and therefore drop/exclude from
// dotfiles tracking. Derived from agentConfigDirs but with ".agents" narrowed
// to ".agents/skills": the installed-skills store omni writes and manages.
// ".agents/.skill-lock.json" and any other file directly under ".agents" are
// user-owned, trackable dotfiles and must NOT match here.
var agentDotsManagedPaths = replaceAgentConfigDir(agentConfigDirs, ".agents", ".agents/skills")

func replaceAgentConfigDir(dirs []string, from, to string) []string {
	out := make([]string, len(dirs))
	for i, dir := range dirs {
		if dir == from {
			out[i] = to
			continue
		}
		out[i] = dir
	}
	return out
}
