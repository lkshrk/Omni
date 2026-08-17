package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
)

const claudeAgentID = "claude-code"

func (a *App) claudeUserMcpConfigPath() string {
	if dir := strings.TrimSpace(a.lookupEnv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home := strings.TrimSpace(a.lookupEnv("HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// A missing or malformed file reports nothing rather than an error: a config claude rewrites on every launch must never break an unrelated listing.
func (a *App) claudeStateMcpServers() map[string]InstalledMcpServer {
	path := a.claudeUserMcpConfigPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseClaudeStateMcpServers(data)
}

// The one gate on reading claude's user config: its MCP entries describe APM state only when APM drives claude on this host.
func (a *App) claudeStateForAPMAgents(apmAgentIDs []string) map[string]InstalledMcpServer {
	if !slices.Contains(apmAgentIDs, claudeAgentID) {
		return nil
	}
	return a.claudeStateMcpServers()
}

// The one write omni still makes to claude's config: APM's first-contact check is name-based, so an entry omni wrote natively would be adopted stale.
func (a *App) claudeStateRemoveMcpServers(names []string) ([]string, error) {
	path := a.claudeUserMcpConfigPath()
	if path == "" || len(names) == 0 {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(file["mcpServers"], &servers); len(file["mcpServers"]) == 0 || err != nil {
		return nil, nil
	}
	var removed []string
	for _, name := range names {
		if _, ok := servers[name]; !ok {
			continue
		}
		delete(servers, name)
		removed = append(removed, name)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", path, err)
	}
	file["mcpServers"] = encoded
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, append(out, '\n'), mode); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	return removed, nil
}

// mcpPlaceholders — the header and env values omni declared for one server, keyed as claude's config keys them.
type mcpPlaceholders struct {
	headers map[string]string
	env     map[string]string
}

// claudeStateRestoreMcpPlaceholders rewrites any value claude's adapter resolved back to the reference omni
// declared, so a variable that reached the install cannot leave a secret sitting in the config. Returns one
// "server: key" description per restored value, never the value itself.
func (a *App) claudeStateRestoreMcpPlaceholders(want map[string]mcpPlaceholders) ([]string, error) {
	path := a.claudeUserMcpConfigPath()
	if path == "" || len(want) == 0 {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var servers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(file["mcpServers"], &servers); len(file["mcpServers"]) == 0 || err != nil {
		return nil, nil
	}
	var restored []string
	for name, placeholders := range want {
		entry, ok := servers[name]
		if !ok {
			continue
		}
		for section, declared := range map[string]map[string]string{"headers": placeholders.headers, "env": placeholders.env} {
			keys, changed := restoreSectionPlaceholders(entry, section, declared)
			if !changed {
				continue
			}
			for _, key := range keys {
				restored = append(restored, name+": "+section+"."+key)
			}
		}
	}
	if len(restored) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", path, err)
	}
	file["mcpServers"] = encoded
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, append(out, '\n'), mode); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	sort.Strings(restored)
	return restored, nil
}

// restoreSectionPlaceholders rewrites entry[section] in place; only a declared value carrying a reference is compared, so a literal omni declared is left alone.
func restoreSectionPlaceholders(entry map[string]json.RawMessage, section string, declared map[string]string) ([]string, bool) {
	if len(declared) == 0 || len(entry[section]) == 0 {
		return nil, false
	}
	var deployed map[string]string
	if err := json.Unmarshal(entry[section], &deployed); err != nil {
		return nil, false
	}
	var keys []string
	for key, value := range declared {
		if !mcpEnvRefPattern.MatchString(value) {
			continue
		}
		if current, ok := deployed[key]; !ok || current == value {
			continue
		}
		deployed[key] = value
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, false
	}
	encoded, err := json.Marshal(deployed)
	if err != nil {
		return nil, false
	}
	entry[section] = encoded
	return keys, true
}

// An entry is stdio (command/args/env) or remote (type plus url); an omitted type alongside a url is read as "http", but that is inference from the entry's shape rather than something claude stated.
type claudeStateFile struct {
	McpServers map[string]claudeStateMcpServer `json:"mcpServers"`
}

type claudeStateMcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	URL     string            `json:"url"`
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
}

func parseClaudeStateMcpServers(data []byte) map[string]InstalledMcpServer {
	var file claudeStateFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	servers := make(map[string]InstalledMcpServer, len(file.McpServers))
	for name, entry := range file.McpServers {
		s := InstalledMcpServer{Name: name, EnvLiteral: entry.Env}
		if entry.URL != "" {
			s.HeadersKnown = true
			switch strings.TrimSpace(entry.Type) {
			case "sse":
				s.Transport = "sse"
			case "":
				s.Transport = "http"
				s.TransportInferred = true
			default:
				s.Transport = "http"
			}
			s.URL = entry.URL
			s.Headers = entry.Headers
		} else {
			s.Transport = "stdio"
			s.Command = strings.TrimSpace(strings.Join(append([]string{entry.Command}, entry.Args...), " "))
			s.Version = agent.ExtractMcpPinnedVersion(s.Command)
		}
		servers[name] = s
	}
	return servers
}
