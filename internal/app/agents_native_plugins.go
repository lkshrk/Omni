package app

import (
	"context"
	"encoding/json"
	"fmt"
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

// recoverNativePluginPlan inventories native plugin state without changing it.
func (a *App) recoverNativePluginPlan(ctx context.Context) (agentBundlePlan, string, error) {
	var plugins []nativePlugin
	var marketplaces []nativeMarketplace
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
