package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	harnessClaude       = "claude"
	harnessCodex        = "codex"
	harnessClaudeMaxKiB = 4 << 20
)

type harnessMCPConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

type harnessDeployments struct {
	MCP        map[string][]string
	LSP        map[string][]string
	MCPConfigs map[string]harnessMCPConfig
	Notices    []string
}

func (h *harnessDeployments) add(surface map[string][]string, name, label string) {
	if name = strings.TrimSpace(name); name == "" {
		return
	}
	if !slices.Contains(surface[name], label) {
		surface[name] = append(surface[name], label)
		slices.Sort(surface[name])
	}
}

// Reading the harness files by name is the deliberate exception to "the TUI hard-codes no client names": apm records no per-entry deployment.
func readHarnessDeployments(home string) harnessDeployments {
	out := harnessDeployments{
		MCP:        map[string][]string{},
		LSP:        map[string][]string{},
		MCPConfigs: map[string]harnessMCPConfig{},
	}
	out.readClaude(filepath.Join(home, ".claude.json"))
	out.readCodex(filepath.Join(home, ".codex", "config.toml"))
	return out
}

func (h *harnessDeployments) readClaude(path string) {
	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			h.Notices = append(h.Notices, "claude: ~/.claude.json is unreadable, skipped")
		}
		return
	}
	if info.Size() > harnessClaudeMaxKiB {
		h.Notices = append(h.Notices, "claude: ~/.claude.json exceeds 4MiB, skipped")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		h.Notices = append(h.Notices, "claude: ~/.claude.json is unreadable, skipped")
		return
	}
	var doc struct {
		MCPServers map[string]harnessMCPConfig `json:"mcpServers"`
		LSPServers map[string]json.RawMessage  `json:"lspServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		h.Notices = append(h.Notices, "claude: ~/.claude.json could not be parsed, skipped")
		return
	}
	for name, cfg := range doc.MCPServers {
		h.add(h.MCP, name, harnessClaude)
		if strings.TrimSpace(name) != "" {
			h.MCPConfigs[name] = cfg
		}
	}
	for name := range doc.LSPServers {
		h.add(h.LSP, name, harnessClaude)
	}
}

func (h *harnessDeployments) readCodex(path string) {
	file, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			h.Notices = append(h.Notices, "codex: ~/.codex/config.toml is unreadable, skipped")
		}
		return
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if name, ok := codexMCPTableName(scanner.Text()); ok {
			h.add(h.MCP, name, harnessCodex)
		}
	}
	if scanner.Err() != nil {
		h.Notices = append(h.Notices, "codex: ~/.codex/config.toml is unreadable, skipped")
	}
}

// Matches only [mcp_servers.<name>]; a nested table such as [mcp_servers.x.env] is not a server.
func codexMCPTableName(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, "#"); idx >= 0 && !strings.Contains(line[:idx], "\"") {
		line = strings.TrimSpace(line[:idx])
	}
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	body := strings.TrimSpace(line[1 : len(line)-1])
	const prefix = "mcp_servers."
	if !strings.HasPrefix(body, prefix) {
		return "", false
	}
	name := body[len(prefix):]
	if strings.HasPrefix(name, "\"") {
		if !strings.HasSuffix(name, "\"") || len(name) < 2 {
			return "", false
		}
		name = name[1 : len(name)-1]
		if strings.Contains(name, "\"") {
			return "", false
		}
		return name, name != ""
	}
	if name == "" || strings.ContainsAny(name, ".\"") {
		return "", false
	}
	return name, true
}
