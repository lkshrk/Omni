package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Target struct {
	ID      string
	Display string
	MCP     McpAdapter
	Plugins PluginAdapter

	configDir       string
	configEnv       string
	binary          string
	extraSkillsDirs []string
}

type Option func(*Registry) error

func WithMcpAdapters(adapters []McpAdapter) Option {
	return func(r *Registry) error {
		for _, adapter := range adapters {
			if adapter == nil {
				return fmt.Errorf("nil MCP adapter")
			}
			id := adapter.ID()
			i, ok := r.byID[id]
			if !ok {
				return fmt.Errorf("unknown target ID %q for MCP adapter", id)
			}
			if r.targets[i].MCP != nil {
				return fmt.Errorf("duplicate MCP adapter ID %q", id)
			}
			r.targets[i].MCP = adapter
		}
		return nil
	}
}

// WithDefaultMcpAdapters registers only the agents APM cannot serve; every other target's MCP state is APM's.
func WithDefaultMcpAdapters(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) Option {
	return WithMcpAdapters([]McpAdapter{
		NewCodexMcpAdapter(execFn, lookupEnv),
		NewHermesMcpAdapter(execFn, lookupEnv),
	})
}

func WithPluginAdapters(adapters []PluginAdapter) Option {
	return func(r *Registry) error {
		for _, adapter := range adapters {
			if adapter == nil {
				return fmt.Errorf("nil plugin adapter")
			}
			id := adapter.ID()
			i, ok := r.byID[id]
			if !ok {
				return fmt.Errorf("unknown target ID %q for plugin adapter", id)
			}
			if r.targets[i].Plugins != nil {
				return fmt.Errorf("duplicate plugin adapter ID %q", id)
			}
			r.targets[i].Plugins = adapter
		}
		return nil
	}
}

func WithDefaultPluginAdapters(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) Option {
	return WithPluginAdapters([]PluginAdapter{
		NewClaudeCodePluginAdapter(execFn, lookupEnv),
		NewCodexPluginAdapter(execFn, lookupEnv),
		NewGrokPluginAdapter(execFn, lookupEnv),
		NewHermesPluginAdapter(execFn, lookupEnv),
	})
}

type Registry struct {
	targets []Target
	byID    map[string]int
}

func NewRegistry(opts ...Option) (*Registry, error) {
	return newRegistry(supportedTargets, opts...)
}

func newRegistry(targets []Target, opts ...Option) (*Registry, error) {
	r := &Registry{targets: slices.Clone(targets)}
	r.byID = make(map[string]int, len(r.targets))
	for i, target := range r.targets {
		if _, exists := r.byID[target.ID]; exists {
			return nil, fmt.Errorf("duplicate target ID %q", target.ID)
		}
		r.byID[target.ID] = i
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) All() []Target {
	return slices.Clone(r.targets)
}

func (t Target) SkillDirs(home string) []string {
	return t.skillDirs(home)
}

func (r *Registry) ByID(id string) (Target, bool) {
	i, ok := r.byID[id]
	if !ok {
		return Target{}, false
	}
	return r.targets[i], true
}

func (r *Registry) McpAdapters() []McpAdapter {
	adapters := make([]McpAdapter, 0)
	for _, target := range r.targets {
		if target.MCP != nil {
			adapters = append(adapters, target.MCP)
		}
	}
	return adapters
}

func (r *Registry) PluginAdapters() []PluginAdapter {
	adapters := make([]PluginAdapter, 0)
	for _, target := range r.targets {
		if target.Plugins != nil {
			adapters = append(adapters, target.Plugins)
		}
	}
	return adapters
}

func (r *Registry) ConfigDotCandidateNames() []string {
	seen := make(map[string]struct{})
	var names []string
	for _, target := range r.targets {
		name, ok := strings.CutPrefix(target.configDir, ".config/")
		if !ok || name == "" || strings.Contains(name, "/") {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func (r *Registry) Installed(home string) []Target {
	shared := r.sharedConfigDirs()
	installed := make([]Target, 0, len(r.targets))
	for _, target := range r.targets {
		path := target.configPath(home)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if shared[target.configDir] {
			if target.binaryOnPath() {
				installed = append(installed, target)
			}
			continue
		}
		if target.binary != "" {
			if target.binaryOnPath() {
				installed = append(installed, target)
			}
			continue
		}
		if dirNonEmpty(path) {
			installed = append(installed, target)
		}
	}
	return installed
}

func (r *Registry) InstalledByID(home, id string) (Target, bool) {
	for _, target := range r.Installed(home) {
		if target.ID == id {
			return target, true
		}
	}
	return Target{}, false
}

func (r *Registry) sharedConfigDirs() map[string]bool {
	counts := make(map[string]int, len(r.targets))
	for _, target := range r.targets {
		counts[target.configDir]++
	}
	shared := make(map[string]bool, len(counts))
	for dir, count := range counts {
		if count > 1 {
			shared[dir] = true
		}
	}
	return shared
}

func (t Target) configPath(home string) string {
	if t.configEnv != "" {
		if path := os.Getenv(t.configEnv); path != "" {
			return path
		}
	}
	return filepath.Join(home, t.configDir)
}

func (t Target) HasAnySkill(home string, names []string) bool {
	dirs := []string{filepath.Join(t.configPath(home), "skills")}
	for _, dir := range t.extraSkillsDirs {
		dirs = append(dirs, filepath.Join(home, dir))
	}
	for _, dir := range dirs {
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
	}
	return false
}

func (t Target) binaryOnPath() bool {
	if t.binary == "" {
		return false
	}
	_, err := lookPath(t.binary)
	return err == nil
}

func dirNonEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// Project-only targets with no global path are omitted; targets sharing a global directory are listed individually.
var supportedTargets = []Target{
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
	{ID: "hermes-agent", Display: "Hermes Agent", configDir: ".hermes", configEnv: "HERMES_HOME", binary: "hermes"},
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
