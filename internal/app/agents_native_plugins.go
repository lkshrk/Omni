package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type nativePlugin struct {
	Name        string
	Marketplace string
	Target      string
}

type nativeMarketplace struct {
	Name   string
	Source string
}

type nativeMCP struct {
	Name       string
	Definition legacyEntry
	Target     string
}

// recoverNativeAgentPlan inventories native plugin and MCP state without changing it.
func (a *App) recoverNativeAgentPlan(ctx context.Context) (agentBundlePlan, string, error) {
	var plugins []nativePlugin
	var marketplaces []nativeMarketplace
	var servers []nativeMCP
	for _, cli := range []string{"claude", "codex"} {
		if !a.commandAvailable(cli) {
			continue
		}
		listed, err := a.listNativePlugins(ctx, cli)
		if err != nil {
			return agentBundlePlan{}, "", err
		}
		plugins = append(plugins, listed...)
		listedMarketplaces, err := a.listNativeMarketplaces(ctx, cli)
		if err != nil {
			return agentBundlePlan{}, "", fmt.Errorf("%w (installed: %s)", err, nativePluginIdentities(plugins))
		}
		marketplaces = append(marketplaces, listedMarketplaces...)
		listedServers, err := a.listNativeMCP(ctx, cli)
		if err != nil {
			return agentBundlePlan{}, "", err
		}
		servers = append(servers, listedServers...)
	}

	sources := map[string]string{}
	missingSources := map[string]bool{}
	for _, marketplace := range marketplaces {
		if marketplace.Name == "" {
			continue
		}
		if marketplace.Source == "" {
			missingSources[marketplace.Name] = true
			continue
		}
		if existing, ok := sources[marketplace.Name]; ok && existing != marketplace.Source {
			return agentBundlePlan{}, "", fmt.Errorf("native marketplace %q has ambiguous sources %q and %q (installed: %s)", marketplace.Name, existing, marketplace.Source, nativePluginIdentities(plugins))
		}
		sources[marketplace.Name] = marketplace.Source
	}

	targets := map[string]map[string]bool{}
	for _, plugin := range plugins {
		identity := plugin.Name + "@" + plugin.Marketplace
		if plugin.Name == "" || plugin.Marketplace == "" {
			return agentBundlePlan{}, "", fmt.Errorf("native plugin has invalid identity %q", identity)
		}
		if missingSources[plugin.Marketplace] {
			return agentBundlePlan{}, "", fmt.Errorf("native plugin %q has a marketplace entry without a source", identity)
		}
		if sources[plugin.Marketplace] == "" {
			return agentBundlePlan{}, "", fmt.Errorf("native plugin %q has no unambiguous marketplace source", identity)
		}
		if targets[identity] == nil {
			targets[identity] = map[string]bool{}
		}
		targets[identity][plugin.Target] = true
	}

	plan := agentBundlePlan{Decls: config.LegacyAgentDecls{
		MCPServers:   map[string]json.RawMessage{},
		Plugins:      map[string]json.RawMessage{},
		Marketplaces: map[string]json.RawMessage{},
	}}
	usedMarketplaces := map[string]bool{}
	for identity, targetSet := range targets {
		name, marketplace := splitNativePluginIdentity(identity)
		var agents []string
		for target := range targetSet {
			agents = append(agents, target)
		}
		sort.Strings(agents)
		plan.Decls.Plugins[identity] = mustNativeJSON(legacyEntry{Name: name, Marketplace: marketplace, Agents: agents})
		usedMarketplaces[marketplace] = true
	}
	for marketplace := range usedMarketplaces {
		plan.Decls.Marketplaces[marketplace] = mustNativeJSON(legacyEntry{Name: marketplace, Source: sources[marketplace]})
	}
	mcpByName := map[string]legacyEntry{}
	for _, server := range servers {
		definition := server.Definition
		definition.Name = server.Name
		definition.Agents = nil
		normalizeNativeMCP(&definition)
		if prior, ok := mcpByName[server.Name]; ok {
			comparable := prior
			comparable.Agents = nil
			if !bytes.Equal(mustNativeJSON(comparable), mustNativeJSON(definition)) {
				return agentBundlePlan{}, "", fmt.Errorf("native MCP server %q has conflicting definitions", server.Name)
			}
			definition.Agents = prior.Agents
		}
		if !slices.Contains(definition.Agents, server.Target) {
			definition.Agents = append(definition.Agents, server.Target)
			sort.Strings(definition.Agents)
		}
		mcpByName[server.Name] = definition
	}
	for name, server := range mcpByName {
		plan.Decls.MCPServers[name] = mustNativeJSON(server)
	}
	manifest, commands, err := renderAPMTemplatePlan(plan)
	if err != nil {
		return agentBundlePlan{}, "", err
	}
	var rendered strings.Builder
	rendered.WriteString(agentsMigrationMarker + "\n" + manifest)
	for _, command := range commands {
		rendered.WriteString("# " + command + "\n")
	}
	return plan, rendered.String(), nil
}

func (a *App) listNativeMCP(ctx context.Context, cli string) ([]nativeMCP, error) {
	if cli == "claude" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("claude MCP inventory: resolve home: %w", err)
		}
		raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
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
				URL     string            `json:"url"`
				Env     map[string]string `json:"env"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("claude MCP inventory: parse ~/.claude.json: %w", err)
		}
		out := make([]nativeMCP, 0, len(file.Servers))
		for name, server := range file.Servers {
			definition := legacyEntry{Name: name}
			definition.EnvLiteral, err = safeNativeValues("claude", name, "environment", server.Env)
			if err != nil {
				return nil, err
			}
			var headerErr error
			definition.Headers, headerErr = symbolicNativeHeaders("claude", name, server.Headers)
			if headerErr != nil {
				return nil, headerErr
			}
			if server.URL == "" {
				definition.Transport = "stdio"
				definition.Command = strings.TrimSpace(strings.Join(append([]string{server.Command}, server.Args...), " "))
			} else {
				definition.Transport, definition.URL = "http", server.URL
				if strings.TrimSpace(server.Type) == "sse" {
					definition.Transport = "sse"
				}
			}
			out = append(out, nativeMCP{Name: name, Definition: definition, Target: "claude"})
		}
		return out, nil
	}

	stdout, _, err := a.fallbackExecutor().Run(ctx, "codex", "mcp", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex mcp list --json: %w", err)
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
		definition := legacyEntry{Name: server.Name, Transport: server.Transport.Type, URL: server.Transport.URL, Cwd: server.Transport.Cwd}
		if definition.Transport == "streamable_http" {
			definition.Transport = "http"
		}
		if definition.Transport != "stdio" && definition.Transport != "http" && definition.Transport != "sse" {
			return nil, fmt.Errorf("codex MCP server %q has unsupported transport %q", server.Name, definition.Transport)
		}
		definition.EnvLiteral, err = safeNativeValues("codex", server.Name, "environment", server.Transport.Env)
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
			definition.Command = strings.TrimSpace(strings.Join(append([]string{server.Transport.Command}, server.Transport.Args...), " "))
		} else {
			definition.Headers, err = symbolicNativeHeaders("codex", server.Name, server.Transport.HTTPHeaders)
			if err != nil {
				return nil, err
			}
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
		out = append(out, nativeMCP{Name: server.Name, Definition: definition, Target: "codex"})
	}
	return out, nil
}

func symbolicNativeHeaders(client, server string, headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		if sensitiveField(name) && !symbolicSecretReference.MatchString(strings.TrimSpace(value)) {
			return nil, fmt.Errorf("%s MCP server %q has literal sensitive header %q", client, server, name)
		}
		out[name] = value
	}
	return out, nil
}

func safeNativeValues(client, server, kind string, values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for name, value := range values {
		if !validNativeEnvName(name) {
			return nil, fmt.Errorf("%s MCP server %q has invalid %s name %q", client, server, kind, name)
		}
		if sensitiveField(name) && !symbolicSecretReference.MatchString(strings.TrimSpace(value)) {
			return nil, fmt.Errorf("%s MCP server %q has literal sensitive %s field %q", client, server, kind, name)
		}
		out[name] = value
	}
	return out, nil
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

func normalizeNativeMCP(entry *legacyEntry) {
	if len(entry.Headers) == 0 {
		entry.Headers = nil
	}
	if len(entry.EnvLiteral) == 0 {
		entry.EnvLiteral = nil
	}
}

func nativeAgentPlanEmpty(plan agentBundlePlan) bool {
	return len(plan.Decls.Plugins) == 0 && len(plan.Decls.MCPServers) == 0
}

func (a *App) listNativePlugins(ctx context.Context, cli string) ([]nativePlugin, error) {
	args := []string{"plugin", "list", "--json"}
	if cli == "claude" {
		args[0] = "plugins"
	}
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, cli, args...)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", cli, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	var entries []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		MarketplaceName string `json:"marketplaceName"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := decodeNativeList(stdout, "installed", &entries); err != nil {
		return nil, fmt.Errorf("%s %s: parse json: %w", cli, strings.Join(args, " "), err)
	}
	out := make([]nativePlugin, 0, len(entries))
	for _, entry := range entries {
		if entry.Enabled != nil && !*entry.Enabled {
			continue
		}
		name, marketplace := entry.Name, entry.MarketplaceName
		if entry.ID != "" {
			name, marketplace = splitNativePluginIdentity(entry.ID)
		}
		identity := name + "@" + marketplace
		if name == "" || marketplace == "" {
			return nil, fmt.Errorf("%s %s: invalid installed plugin identity %q", cli, strings.Join(args, " "), identity)
		}
		out = append(out, nativePlugin{Name: name, Marketplace: marketplace, Target: cli})
	}
	return out, nil
}

func (a *App) listNativeMarketplaces(ctx context.Context, cli string) ([]nativeMarketplace, error) {
	args := []string{"plugin", "marketplace", "list", "--json"}
	if cli == "claude" {
		args[0] = "plugins"
	}
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
