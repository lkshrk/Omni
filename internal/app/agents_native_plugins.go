package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type nativePlugin struct {
	Name        string
	Marketplace string
	Target      string
	Version     string
	InstallRoot string
	Disabled    bool
}

type nativeMarketplace struct {
	Name   string
	Source string
}

type nativeMCP struct {
	Name         string
	Definition   legacyEntry
	Target       string
	SecretFields []string
}

// inventoryNativeAgents reads native plugin, marketplace, and MCP state without changing it.
func (a *App) inventoryNativeAgents(ctx context.Context) ([]agentObservation, error) {
	var out []agentObservation
	var plugins []nativePlugin
	for _, cli := range []string{"claude", "codex"} {
		if !a.commandAvailable(cli) {
			continue
		}
		listedPlugins, err := a.listNativePlugins(ctx, cli)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, listedPlugins...)
		for _, plugin := range listedPlugins {
			evidence := []string{nativePluginCommand(cli, "list", "--json")}
			if plugin.InstallRoot != "" {
				evidence = append(evidence, plugin.InstallRoot)
			}
			out = append(out, agentObservation{
				Source:      cli,
				Target:      plugin.Target,
				Kind:        agentKindPlugin,
				Identity:    plugin.Name + "@" + plugin.Marketplace,
				Definition:  legacyEntry{Name: plugin.Name, Marketplace: plugin.Marketplace},
				Version:     plugin.Version,
				InstallRoot: plugin.InstallRoot,
				Disabled:    plugin.Disabled,
				Evidence:    evidence,
			})
		}
		listedMarketplaces, err := a.listNativeMarketplaces(ctx, cli)
		if err != nil {
			return nil, fmt.Errorf("%w (installed: %s)", err, nativePluginIdentities(plugins))
		}
		for _, marketplace := range listedMarketplaces {
			if marketplace.Name == "" {
				continue
			}
			out = append(out, agentObservation{
				Source:     cli,
				Target:     cli,
				Kind:       agentKindMarketplace,
				Identity:   marketplace.Name,
				Definition: legacyEntry{Name: marketplace.Name, Source: canonicalNativeMarketplaceSource(marketplace.Source)},
				Evidence:   []string{nativePluginCommand(cli, "marketplace", "list", "--json")},
			})
		}
		listedServers, err := a.listNativeMCP(ctx, cli)
		if err != nil {
			return nil, err
		}
		for _, server := range listedServers {
			definition := server.Definition
			definition.Name = server.Name
			definition.Agents = nil
			normalizeNativeMCP(&definition)
			out = append(out, agentObservation{
				Source:       cli,
				Target:       server.Target,
				Kind:         agentKindMCP,
				Identity:     server.Name,
				Definition:   definition,
				SecretFields: server.SecretFields,
				Evidence:     []string{nativeMCPEvidence(cli)},
			})
		}
	}
	return out, nil
}

func nativeMCPEvidence(cli string) string {
	if cli != "claude" {
		return "codex mcp list --json"
	}
	path, err := claudeConfigFile()
	if err != nil {
		return "claude mcp config"
	}
	return path
}

func claudeConfigFile() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude MCP inventory: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

func canonicalNativeMarketplaceSource(source string) string {
	return strings.TrimSpace(source)
}

func (a *App) listNativeMCP(ctx context.Context, cli string) ([]nativeMCP, error) {
	if cli == "claude" {
		path, err := claudeConfigFile()
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("claude MCP inventory: %w", err)
		}
		var file struct {
			Servers map[string]struct {
				Type    string            `json:"type"`
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Cwd     string            `json:"cwd"`
				URL     string            `json:"url"`
				Env     map[string]string `json:"env"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("claude MCP inventory: parse %s: %w", path, err)
		}
		out := make([]nativeMCP, 0, len(file.Servers))
		for name, server := range file.Servers {
			definition := legacyEntry{Name: name, Cwd: server.Cwd}
			var literals []string
			definition.EnvLiteral, literals, err = safeNativeValues("claude", name, server.Env)
			if err != nil {
				return nil, err
			}
			var headerLiterals []string
			definition.Headers, headerLiterals = symbolicNativeHeaders(server.Headers)
			literals = append(literals, headerLiterals...)
			if server.URL == "" {
				definition.Transport = "stdio"
				definition.Command, definition.Args = server.Command, slices.Clone(server.Args)
			} else {
				definition.Transport, definition.URL = "http", server.URL
				if strings.TrimSpace(server.Type) == "sse" {
					definition.Transport = "sse"
				}
			}
			normalizeNativeMCP(&definition)
			out = append(out, nativeMCP{Name: name, Definition: definition, Target: "claude", SecretFields: sortedUnique(literals)})
		}
		return out, nil
	}

	stdout, stderr, err := a.fallbackExecutor().Run(ctx, "codex", "mcp", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex mcp list --json: %w: %s", err, strings.TrimSpace(stderr))
	}
	var entries []struct {
		Name      string `json:"name"`
		Transport struct {
			Type           string            `json:"type"`
			URL            string            `json:"url"`
			Command        string            `json:"command"`
			Args           []string          `json:"args"`
			Env            map[string]string `json:"env"`
			EnvVars        []string          `json:"env_vars"`
			Cwd            string            `json:"cwd"`
			BearerTokenEnv string            `json:"bearer_token_env_var"`
			HTTPHeaders    map[string]string `json:"http_headers"`
			EnvHTTPHeaders map[string]string `json:"env_http_headers"`
		} `json:"transport"`
		Enabled *bool `json:"enabled"`
	}
	if strings.TrimSpace(stdout) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("codex mcp list --json: parse json: %w", err)
	}
	if entries == nil && strings.TrimSpace(stdout) != "" {
		return nil, fmt.Errorf("codex mcp list --json: parse json: expected array")
	}
	out := make([]nativeMCP, 0, len(entries))
	for _, server := range entries {
		if server.Enabled != nil && !*server.Enabled {
			continue
		}
		definition := legacyEntry{Name: server.Name, Transport: normalizeNativeMCPTransport(server.Transport.Type), URL: server.Transport.URL, Cwd: server.Transport.Cwd}
		if definition.Transport != "stdio" && definition.Transport != "http" && definition.Transport != "sse" {
			return nil, fmt.Errorf("codex MCP server %q has unsupported transport %q", server.Name, definition.Transport)
		}
		var literals []string
		definition.EnvLiteral, literals, err = safeNativeValues("codex", server.Name, server.Transport.Env)
		if err != nil {
			return nil, err
		}
		definition.Env = append(definition.Env, server.Transport.EnvVars...)
		sort.Strings(definition.Env)
		definition.Env = slices.Compact(definition.Env)
		for _, env := range definition.Env {
			if !validNativeEnvName(env) {
				return nil, fmt.Errorf("codex MCP server %q has invalid env name %q", server.Name, env)
			}
		}
		if definition.Transport == "stdio" {
			definition.Command, definition.Args = server.Transport.Command, slices.Clone(server.Transport.Args)
		} else {
			var headerLiterals []string
			definition.Headers, headerLiterals = symbolicNativeHeaders(server.Transport.HTTPHeaders)
			literals = append(literals, headerLiterals...)
			if len(server.Transport.EnvHTTPHeaders) > 0 {
				if definition.Headers == nil {
					definition.Headers = map[string]string{}
				}
				for name, env := range server.Transport.EnvHTTPHeaders {
					if !validNativeEnvName(env) {
						return nil, fmt.Errorf("codex MCP server %q header %q has invalid env name", server.Name, name)
					}
					definition.Headers[name] = "${" + env + "}"
				}
			}
			if env := server.Transport.BearerTokenEnv; env != "" {
				if !validNativeEnvName(env) {
					return nil, fmt.Errorf("codex MCP server %q bearer_token_env_var is invalid", server.Name)
				}
				if definition.Headers == nil {
					definition.Headers = map[string]string{}
				}
				if _, exists := definition.Headers["Authorization"]; exists {
					return nil, fmt.Errorf("codex MCP server %q has conflicting Authorization fields", server.Name)
				}
				definition.Headers["Authorization"] = "Bearer ${" + env + "}"
			}
		}
		normalizeNativeMCP(&definition)
		out = append(out, nativeMCP{Name: server.Name, Definition: definition, Target: "codex", SecretFields: sortedUnique(literals)})
	}
	return out, nil
}

func symbolicNativeHeaders(headers map[string]string) (map[string]string, []string) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	var literals []string
	for name, value := range headers {
		if sensitiveField(name) && !symbolicSecretReference.MatchString(strings.TrimSpace(value)) {
			literals = append(literals, "header "+name)
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		out = nil
	}
	return out, literals
}

func safeNativeValues(client, server string, values map[string]string) (map[string]string, []string, error) {
	if len(values) == 0 {
		return nil, nil, nil
	}
	out := make(map[string]string, len(values))
	var literals []string
	for name, value := range values {
		if !validNativeEnvName(name) {
			return nil, nil, fmt.Errorf("%s MCP server %q has invalid env name %q", client, server, name)
		}
		if sensitiveField(name) && !symbolicSecretReference.MatchString(strings.TrimSpace(value)) {
			literals = append(literals, "env "+name)
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		out = nil
	}
	return out, literals, nil
}

func validNativeEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func normalizeNativeMCPTransport(transport string) string {
	switch strings.TrimSpace(transport) {
	case "streamable_http", "streamable-http":
		return "http"
	default:
		return strings.TrimSpace(transport)
	}
}

func normalizeNativeMCP(entry *legacyEntry) {
	entry.Transport = normalizeNativeMCPTransport(entry.Transport)
	entry.Command = apmPlaceholders(entry.Command)
	entry.Cwd = apmPlaceholders(entry.Cwd)
	entry.URL = apmPlaceholders(entry.URL)
	if len(entry.Args) == 0 {
		entry.Args = nil
	}
	for i, arg := range entry.Args {
		entry.Args[i] = apmPlaceholders(arg)
	}
	if len(entry.Headers) == 0 {
		entry.Headers = nil
	}
	for name, value := range entry.Headers {
		entry.Headers[name] = apmPlaceholders(value)
	}
	if len(entry.EnvLiteral) == 0 {
		entry.EnvLiteral = nil
	}
	for name, value := range entry.EnvLiteral {
		entry.EnvLiteral[name] = apmPlaceholders(value)
	}
	if len(entry.Env) == 0 {
		entry.Env = nil
		return
	}
	sort.Strings(entry.Env)
	entry.Env = slices.Compact(entry.Env)
}

// Claude spells the plugin command "plugins"; both the call and its evidence line go through this.
func nativePluginArgs(cli string, tail ...string) []string {
	head := "plugin"
	if cli == "claude" {
		head = "plugins"
	}
	return append([]string{head}, tail...)
}

func nativePluginCommand(cli string, tail ...string) string {
	return cli + " " + strings.Join(nativePluginArgs(cli, tail...), " ")
}

func (a *App) listNativePlugins(ctx context.Context, cli string) ([]nativePlugin, error) {
	args := nativePluginArgs(cli, "list", "--json")
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, cli, args...)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", cli, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	var entries []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		MarketplaceName string `json:"marketplaceName"`
		Version         string `json:"version"`
		InstallPath     string `json:"installPath"`
		Source          struct {
			URL string `json:"url"`
		} `json:"source"`
		Enabled *bool `json:"enabled"`
	}
	if err := decodeNativeList(stdout, "installed", &entries); err != nil {
		return nil, fmt.Errorf("%s %s: parse json: %w", cli, strings.Join(args, " "), err)
	}
	out := make([]nativePlugin, 0, len(entries))
	for _, entry := range entries {
		name, marketplace := entry.Name, entry.MarketplaceName
		if entry.ID != "" {
			name, marketplace = splitNativePluginIdentity(entry.ID)
		}
		identity := name + "@" + marketplace
		if name == "" || marketplace == "" {
			return nil, fmt.Errorf("%s %s: invalid installed plugin identity %q", cli, strings.Join(args, " "), identity)
		}
		root := entry.InstallPath
		if cli != "claude" {
			root = entry.Source.URL
		}
		out = append(out, nativePlugin{Name: name, Marketplace: marketplace, Target: cli, Version: entry.Version, InstallRoot: root, Disabled: entry.Enabled != nil && !*entry.Enabled})
	}
	return out, nil
}

func (a *App) listNativeMarketplaces(ctx context.Context, cli string) ([]nativeMarketplace, error) {
	args := nativePluginArgs(cli, "marketplace", "list", "--json")
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, cli, args...)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", cli, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	var entries []struct {
		Name              string `json:"name"`
		Source            string `json:"source"`
		Repo              string `json:"repo"`
		URL               string `json:"url"`
		MarketplaceSource struct {
			Source string `json:"source"`
		} `json:"marketplaceSource"`
	}
	if err := decodeNativeList(stdout, "marketplaces", &entries); err != nil {
		return nil, fmt.Errorf("%s %s: parse json: %w", cli, strings.Join(args, " "), err)
	}
	out := make([]nativeMarketplace, 0, len(entries))
	for _, entry := range entries {
		source := entry.MarketplaceSource.Source
		if cli == "claude" {
			source = entry.URL
			if entry.Source == "github" && entry.Repo != "" {
				source = entry.Repo
			}
		}
		out = append(out, nativeMarketplace{Name: entry.Name, Source: source})
	}
	return out, nil
}

func decodeNativeList(stdout, field string, target any) error {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		return json.Unmarshal([]byte(trimmed), target)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return err
	}
	raw, ok := envelope[field]
	if !ok || string(raw) == "null" {
		return fmt.Errorf("expected %s array", field)
	}
	return json.Unmarshal(raw, target)
}

func splitNativePluginIdentity(identity string) (string, string) {
	i := strings.LastIndex(identity, "@")
	if i <= 0 || i == len(identity)-1 {
		return identity, ""
	}
	return identity[:i], identity[i+1:]
}

func nativePluginIdentities(plugins []nativePlugin) string {
	identities := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		identity := plugin.Name + "@" + plugin.Marketplace
		if !slices.Contains(identities, identity) {
			identities = append(identities, identity)
		}
	}
	sort.Strings(identities)
	if len(identities) == 0 {
		return "none"
	}
	return strings.Join(identities, ", ")
}

func mustNativeJSON(value legacyEntry) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
