package app

import (
	"os"
	"os/exec"
	"path/filepath"
)

// AgentInfo describes one AI coding agent the skills CLI can target. configDir
// is the home-relative base whose existence signals the agent is installed; the
// agent's global skills dir (where `skills add -g` symlinks) is configDir/skills.
// Data mirrors the upstream skills CLI "Supported Agents" table (Global Path).
type AgentInfo struct {
	ID        string // skills-CLI identifier passed to `-a`
	Display   string
	configDir string // home-relative dir whose existence signals the agent is installed
	configEnv string // optional env var overriding the absolute config dir
	// binary is the canonical PATH executable name for this agent, when known
	// with confidence. Left empty for agents without a well-known CLI binary.
	binary string
	// extraSkillsDirs are additional home-relative skills dirs the agent also
	// reads (codex reads the shared ~/.agents/skills on top of ~/.codex/skills).
	extraSkillsDirs []string
}

// supportedAgents mirrors vercel-labs/skills "Supported Agents" (Global Path,
// minus the trailing /skills). Project-only agents with no global path are
// omitted. Agents that share a global dir are listed individually.
var supportedAgents = []AgentInfo{
	{ID: "aider-desk", Display: "AiderDesk", configDir: ".aider-desk"},
	{ID: "amp", Display: "Amp", configDir: ".config/agents"},
	{ID: "replit", Display: "Replit", configDir: ".config/agents"},
	{ID: "universal", Display: "Universal", configDir: ".config/agents"},
	{ID: "antigravity", Display: "Antigravity", configDir: ".gemini/antigravity"},
	{ID: "antigravity-cli", Display: "Antigravity CLI", configDir: ".gemini/antigravity-cli"},
	{ID: "astrbot", Display: "AstrBot", configDir: ".astrbot/data"},
	{ID: "autohand-code", Display: "Autohand Code CLI", configDir: ".autohand"},
	{ID: "augment", Display: "Augment", configDir: ".augment"},
	{ID: "bob", Display: "IBM Bob", configDir: ".bob"},
	{ID: "claude-code", Display: "Claude Code", configDir: ".claude", configEnv: "CLAUDE_CONFIG_DIR", binary: "claude"},
	{ID: "openclaw", Display: "OpenClaw", configDir: ".openclaw"},
	{ID: "cline", Display: "Cline", configDir: ".agents", binary: "cline"},
	{ID: "dexto", Display: "Dexto", configDir: ".agents"},
	{ID: "kimi-code-cli", Display: "Kimi Code CLI", configDir: ".agents"},
	{ID: "loaf", Display: "Loaf", configDir: ".agents"},
	{ID: "warp", Display: "Warp", configDir: ".agents"},
	{ID: "zed", Display: "Zed", configDir: ".agents"},
	{ID: "codearts-agent", Display: "CodeArts Agent", configDir: ".codeartsdoer"},
	{ID: "codebuddy", Display: "CodeBuddy", configDir: ".codebuddy"},
	{ID: "codemaker", Display: "Codemaker", configDir: ".codemaker"},
	{ID: "codestudio", Display: "Code Studio", configDir: ".codestudio"},
	{ID: "codex", Display: "Codex", configDir: ".codex", configEnv: "CODEX_HOME", binary: "codex", extraSkillsDirs: []string{".agents/skills"}},
	{ID: "command-code", Display: "Command Code", configDir: ".commandcode"},
	{ID: "continue", Display: "Continue", configDir: ".continue"},
	{ID: "cortex", Display: "Cortex Code", configDir: ".snowflake/cortex"},
	{ID: "crush", Display: "Crush", configDir: ".config/crush"},
	{ID: "cursor", Display: "Cursor", configDir: ".cursor", binary: "cursor"},
	{ID: "deepagents", Display: "Deep Agents", configDir: ".deepagents/agent"},
	{ID: "devin", Display: "Devin for Terminal", configDir: ".config/devin"},
	{ID: "droid", Display: "Droid", configDir: ".factory"},
	{ID: "firebender", Display: "Firebender", configDir: ".firebender"},
	{ID: "forgecode", Display: "ForgeCode", configDir: ".forge"},
	{ID: "gemini-cli", Display: "Gemini CLI", configDir: ".gemini", binary: "gemini"},
	{ID: "github-copilot", Display: "GitHub Copilot", configDir: ".copilot"},
	{ID: "goose", Display: "Goose", configDir: ".config/goose"},
	{ID: "grok", Display: "Grok", configDir: ".grok", configEnv: "GROK_HOME", binary: "grok"},
	{ID: "hermes-agent", Display: "Hermes Agent", configDir: ".hermes"},
	{ID: "inference-sh", Display: "inference.sh", configDir: ".inferencesh"},
	{ID: "jazz", Display: "Jazz", configDir: ".jazz"},
	{ID: "junie", Display: "Junie", configDir: ".junie"},
	{ID: "iflow-cli", Display: "iFlow CLI", configDir: ".iflow"},
	{ID: "kilo", Display: "Kilo Code", configDir: ".kilocode"},
	{ID: "kiro-cli", Display: "Kiro CLI", configDir: ".kiro"},
	{ID: "kode", Display: "Kode", configDir: ".kode"},
	{ID: "lingma", Display: "Lingma", configDir: ".lingma"},
	{ID: "mcpjam", Display: "MCPJam", configDir: ".mcpjam"},
	{ID: "mistral-vibe", Display: "Mistral Vibe", configDir: ".vibe"},
	{ID: "moxby", Display: "Moxby", configDir: ".moxby"},
	{ID: "mux", Display: "Mux", configDir: ".mux"},
	{ID: "opencode", Display: "OpenCode", configDir: ".config/opencode", binary: "opencode"},
	{ID: "openhands", Display: "OpenHands", configDir: ".openhands"},
	{ID: "ona", Display: "Ona", configDir: ".ona"},
	{ID: "pi", Display: "Pi", configDir: ".pi/agent"},
	{ID: "qoder", Display: "Qoder", configDir: ".qoder"},
	{ID: "qoder-cn", Display: "Qoder CN", configDir: ".qoder-cn"},
	{ID: "qwen-code", Display: "Qwen Code", configDir: ".qwen"},
	{ID: "reasonix", Display: "Reasonix", configDir: ".reasonix"},
	{ID: "rovodev", Display: "Rovo Dev", configDir: ".rovodev"},
	{ID: "roo", Display: "Roo Code", configDir: ".roo"},
	{ID: "tabnine-cli", Display: "Tabnine CLI", configDir: ".tabnine/agent"},
	{ID: "terramind", Display: "Terramind", configDir: ".terramind"},
	{ID: "tinycloud", Display: "Tinycloud", configDir: ".tinycloud"},
	{ID: "trae", Display: "Trae", configDir: ".trae"},
	{ID: "trae-cn", Display: "Trae CN", configDir: ".trae-cn"},
	{ID: "windsurf", Display: "Windsurf", configDir: ".codeium/windsurf"},
	{ID: "zencoder", Display: "Zencoder", configDir: ".zencoder"},
	{ID: "zenflow", Display: "Zenflow", configDir: ".zencoder"},
	{ID: "neovate", Display: "Neovate", configDir: ".neovate"},
	{ID: "pochi", Display: "Pochi", configDir: ".pochi"},
	{ID: "adal", Display: "AdaL", configDir: ".adal"},
}

func agentConfigPath(home string, a AgentInfo) string {
	if a.configEnv != "" {
		if v := os.Getenv(a.configEnv); v != "" {
			return v
		}
	}
	return filepath.Join(home, a.configDir)
}

// agentSkillsDirs returns every skills dir an agent reads from (configDir/skills
// plus any shared dirs like codex's ~/.agents/skills).
func agentSkillsDirs(home string, a AgentInfo) []string {
	dirs := []string{filepath.Join(agentConfigPath(home, a), "skills")}
	for _, d := range a.extraSkillsDirs {
		dirs = append(dirs, filepath.Join(home, d))
	}
	return dirs
}

// lookPath is a seam over exec.LookPath so tests can fake PATH resolution
// without mutating the real PATH env var.
var lookPath = exec.LookPath

// sharedConfigDirs returns the set of configDir values used by more than one
// catalog entry. A shared dir — whether a cross-vendor store (".agents" is
// the skills CLI's canonical store, always present) or a vendor-shared dir
// (".zencoder" is used by both Zencoder and Zenflow) — cannot identify WHICH
// agent is installed, and InstalledAgents feeds restore/enable targets, so a
// false positive would write skills and config for a product that is not
// installed. Agents mapped to a shared dir therefore need an agent-specific
// signal (their binary); without one they are never auto-detected, and users
// can still target them explicitly via agents_use.
func sharedConfigDirs() map[string]bool {
	counts := make(map[string]int, len(supportedAgents))
	for _, a := range supportedAgents {
		counts[a.configDir]++
	}
	shared := make(map[string]bool, len(counts))
	for dir, n := range counts {
		if n > 1 {
			shared[dir] = true
		}
	}
	return shared
}

// binaryOnPath reports whether a's canonical binary is found on PATH. Agents
// with no known binary (binary == "") always report false, since no PATH
// signal exists for them.
func binaryOnPath(a AgentInfo) bool {
	if a.binary == "" {
		return false
	}
	_, err := lookPath(a.binary)
	return err == nil
}

// dirNonEmpty reports whether dir exists and contains at least one entry.
// Uses os.ReadDir (non-recursive) rather than a deeper walk since config dirs
// are typically shallow.
func dirNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// InstalledAgents returns the supported agents detected as installed on this
// machine, in catalog order. Detection is layered to avoid false positives
// from configDirs shared across multiple catalog entries (see
// sharedConfigDirs — a false positive here becomes a restore target and
// writes state for an agent that is not installed):
//
//   - Shared configDir: installed only if the agent's binary is found on
//     PATH. An agent with no known binary mapped to a shared dir is never
//     auto-detected; target it explicitly via agents_use instead.
//   - Dedicated configDir: installed if the dir exists and, when a binary is
//     set, the binary is also found on PATH; when no binary is set, installed
//     if the dir exists and is non-empty.
func InstalledAgents(home string) []AgentInfo {
	shared := sharedConfigDirs()
	out := make([]AgentInfo, 0, len(supportedAgents))
	for _, a := range supportedAgents {
		path := agentConfigPath(home, a)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if shared[a.configDir] {
			if binaryOnPath(a) {
				out = append(out, a)
			}
			continue
		}
		if a.binary != "" {
			if binaryOnPath(a) {
				out = append(out, a)
			}
			continue
		}
		if dirNonEmpty(path) {
			out = append(out, a)
		}
	}
	return out
}

// agentInfoByID looks up an installed agent's catalog entry by ID.
func agentInfoByID(home, id string) (AgentInfo, bool) {
	for _, a := range InstalledAgents(home) {
		if a.ID == id {
			return a, true
		}
	}
	return AgentInfo{}, false
}
