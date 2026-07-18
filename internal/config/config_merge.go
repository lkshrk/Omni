package config

import (
	"fmt"
	"strings"
)

// MergeRootConfig deep-merges src into dst. Later fragments win on scalar and
// map fields; groups append and dedupe by name; agent arrays merge by identity.
func MergeRootConfig(dst, src *RootConfig) {
	if dst == nil || src == nil {
		return
	}
	if src.Version > dst.Version {
		dst.Version = src.Version
	}
	mergeSettings(&dst.Settings, &src.Settings)
	mergeHostSettings(dst, src.HostSettings)
	mergeTools(dst, src.Tools)
	mergeGroups(dst, src.Groups)
	mergeHosts(dst, src.Hosts)
	mergeIgnore(&dst.Ignore, &src.Ignore)
	mergeAgents(&dst.Agents, &src.Agents)
}

func mergeSettings(dst, src *Settings) {
	if dst == nil || src == nil {
		return
	}
	if src.AutoImport {
		dst.AutoImport = src.AutoImport
	}
	if strings.TrimSpace(src.UpdateQuarantine) != "" {
		dst.UpdateQuarantine = src.UpdateQuarantine
	}
	if len(src.ProviderUpdateQuarantine) > 0 {
		if dst.ProviderUpdateQuarantine == nil {
			dst.ProviderUpdateQuarantine = make(map[string]string, len(src.ProviderUpdateQuarantine))
		}
		for key, value := range src.ProviderUpdateQuarantine {
			dst.ProviderUpdateQuarantine[key] = value
		}
	}
	if len(src.Ecosystems) > 0 {
		if dst.Ecosystems == nil {
			dst.Ecosystems = make(map[string]EcosystemSettings, len(src.Ecosystems))
		}
		for name, eco := range src.Ecosystems {
			dst.Ecosystems[name] = eco
		}
	}
	if strings.TrimSpace(src.FallbackBinDir) != "" {
		dst.FallbackBinDir = src.FallbackBinDir
	}
	if strings.TrimSpace(src.DotsRepo) != "" {
		dst.DotsRepo = src.DotsRepo
	}
	if src.DotsDisabled != nil {
		dst.DotsDisabled = src.DotsDisabled
	}
	if src.AgentsDisabled != nil {
		dst.AgentsDisabled = src.AgentsDisabled
	}
	if src.SkillsDisabled != nil {
		dst.SkillsDisabled = src.SkillsDisabled
	}
	if src.McpDisabled != nil {
		dst.McpDisabled = src.McpDisabled
	}
	if src.PluginsDisabled != nil {
		dst.PluginsDisabled = src.PluginsDisabled
	}
	if src.AgentsUse != nil {
		dst.AgentsUse = append([]string(nil), src.AgentsUse...)
	}
	if src.DotsGit.AutoCommit {
		dst.DotsGit.AutoCommit = true
	}
	if src.DotsGit.AutoPush {
		dst.DotsGit.AutoPush = true
	}
	if src.DisabledProviders != nil {
		dst.DisabledProviders = append([]string(nil), src.DisabledProviders...)
	}
	if src.ProviderPriority != nil {
		dst.ProviderPriority = append([]string(nil), src.ProviderPriority...)
	}
	if src.Providers != nil {
		dst.Providers = cloneProviders(src.Providers)
	}
}

func mergeHostSettings(dst *RootConfig, src map[string]Settings) {
	if len(src) == 0 {
		return
	}
	if dst.HostSettings == nil {
		dst.HostSettings = make(map[string]Settings, len(src))
	}
	for host, settings := range src {
		current := dst.HostSettings[host]
		mergeSettings(&current, &settings)
		dst.HostSettings[host] = current
	}
}

func mergeTools(dst *RootConfig, src map[string]ToolSpec) {
	if len(src) == 0 {
		return
	}
	if dst.Tools == nil {
		dst.Tools = make(map[string]ToolSpec, len(src))
	}
	for name, spec := range src {
		dst.Tools[name] = spec
	}
}

func mergeGroups(dst *RootConfig, src []*GroupConfig) {
	if len(src) == 0 {
		return
	}
	index := make(map[string]*GroupConfig, len(dst.Groups))
	for _, group := range dst.Groups {
		if group == nil {
			continue
		}
		index[group.BaseName()] = group
	}
	for _, group := range src {
		if group == nil || strings.TrimSpace(group.BaseName()) == "" {
			continue
		}
		if existing, ok := index[group.BaseName()]; ok {
			mergeGroup(existing, group)
			continue
		}
		dst.Groups = append(dst.Groups, group)
		index[group.BaseName()] = group
	}
}

func mergeGroup(dst, src *GroupConfig) {
	dst.Taps = appendUniqueStrings(dst.Taps, src.Taps...)
	dst.Tools = appendUniqueToolEntries(dst.Tools, src.Tools)
	dst.Dots = appendUniqueDotEntries(dst.Dots, src.Dots)
	dst.Skills = appendUniqueStrings(dst.Skills, src.Skills...)
	dst.McpServers = appendUniqueStrings(dst.McpServers, src.McpServers...)
	dst.Plugins = appendUniqueStrings(dst.Plugins, src.Plugins...)
	dst.Marketplaces = appendUniqueStrings(dst.Marketplaces, src.Marketplaces...)
	if strings.TrimSpace(dst.Description) == "" {
		dst.Description = src.Description
	}
	if strings.TrimSpace(dst.Special) == "" {
		dst.Special = src.Special
	}
}

func mergeHosts(dst *RootConfig, src map[string][]string) {
	if len(src) == 0 {
		return
	}
	if dst.Hosts == nil {
		dst.Hosts = make(map[string][]string, len(src))
	}
	for host, groups := range src {
		dst.Hosts[host] = appendUniqueStrings(dst.Hosts[host], groups...)
	}
}

func mergeIgnore(dst, src *GlobalIgnore) {
	if dst == nil || src == nil {
		return
	}
	dst.Tools = appendUniqueStrings(dst.Tools, src.Tools...)
	dst.Dots = appendUniqueStrings(dst.Dots, src.Dots...)
}

func mergeAgents(dst, src *AgentsConfig) {
	if dst == nil || src == nil {
		return
	}
	dst.Packages = mergeSkillPackages(dst.Packages, src.Packages)
	dst.McpServers = mergeMcpServers(dst.McpServers, src.McpServers)
	dst.Marketplaces = mergeMarketplaces(dst.Marketplaces, src.Marketplaces)
	dst.Plugins = mergePlugins(dst.Plugins, src.Plugins)
	dst.Ignore.Skills = appendUniqueStrings(dst.Ignore.Skills, src.Ignore.Skills...)
	dst.Ignore.McpServers = appendUniqueStrings(dst.Ignore.McpServers, src.Ignore.McpServers...)
	dst.Ignore.Plugins = appendUniqueStrings(dst.Ignore.Plugins, src.Ignore.Plugins...)
	dst.Ignore.Marketplaces = appendUniqueStrings(dst.Ignore.Marketplaces, src.Ignore.Marketplaces...)
}

func mergeSkillPackages(dst, src []SkillPackage) []SkillPackage {
	if len(src) == 0 {
		return dst
	}
	index := make(map[string]int, len(dst))
	for i, pkg := range dst {
		index[pkg.Source] = i
	}
	for _, pkg := range src {
		if i, ok := index[pkg.Source]; ok {
			if strings.TrimSpace(dst[i].Ref) == "" {
				dst[i].Ref = pkg.Ref
			}
			dst[i].Agents = appendUniqueStrings(dst[i].Agents, pkg.Agents...)
			continue
		}
		dst = append(dst, pkg)
		index[pkg.Source] = len(dst) - 1
	}
	return dst
}

func mergeMcpServers(dst, src []McpServer) []McpServer {
	if len(src) == 0 {
		return dst
	}
	index := make(map[string]int, len(dst))
	for i, srv := range dst {
		index[srv.Name] = i
	}
	for _, srv := range src {
		if i, ok := index[srv.Name]; ok {
			dst[i] = mergeMcpServer(dst[i], srv)
			continue
		}
		dst = append(dst, srv)
		index[srv.Name] = len(dst) - 1
	}
	return dst
}

func mergeMcpServer(dst, src McpServer) McpServer {
	if strings.TrimSpace(dst.Transport) == "" {
		dst.Transport = src.Transport
	}
	if strings.TrimSpace(dst.Command) == "" {
		dst.Command = src.Command
	}
	if strings.TrimSpace(dst.URL) == "" {
		dst.URL = src.URL
	}
	dst.Env = appendUniqueStrings(dst.Env, src.Env...)
	if len(src.EnvLiteral) > 0 {
		if dst.EnvLiteral == nil {
			dst.EnvLiteral = make(map[string]string, len(src.EnvLiteral))
		}
		for key, value := range src.EnvLiteral {
			dst.EnvLiteral[key] = value
		}
	}
	if len(src.Headers) > 0 {
		if dst.Headers == nil {
			dst.Headers = make(map[string]string, len(src.Headers))
		}
		for key, value := range src.Headers {
			dst.Headers[key] = value
		}
	}
	dst.Agents = appendUniqueStrings(dst.Agents, src.Agents...)
	return dst
}

func mergeMarketplaces(dst, src []Marketplace) []Marketplace {
	if len(src) == 0 {
		return dst
	}
	index := make(map[string]int, len(dst))
	for i, mkt := range dst {
		index[mkt.Name] = i
	}
	for _, mkt := range src {
		if i, ok := index[mkt.Name]; ok {
			if strings.TrimSpace(dst[i].Source) == "" {
				dst[i].Source = mkt.Source
			}
			dst[i].Agents = appendUniqueStrings(dst[i].Agents, mkt.Agents...)
			continue
		}
		dst = append(dst, mkt)
		index[mkt.Name] = len(dst) - 1
	}
	return dst
}

func mergePlugins(dst, src []Plugin) []Plugin {
	if len(src) == 0 {
		return dst
	}
	index := make(map[string]int, len(dst))
	for i, plugin := range dst {
		index[plugin.Name] = i
	}
	for _, plugin := range src {
		if i, ok := index[plugin.Name]; ok {
			if strings.TrimSpace(dst[i].Marketplace) == "" {
				dst[i].Marketplace = plugin.Marketplace
			}
			dst[i].Agents = appendUniqueStrings(dst[i].Agents, plugin.Agents...)
			continue
		}
		dst = append(dst, plugin)
		index[plugin.Name] = len(dst) - 1
	}
	return dst
}

func appendUniqueStrings(dst []string, values ...string) []string {
	if len(values) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func appendUniqueToolEntries(dst, values []ToolEntry) []ToolEntry {
	if len(values) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, value := range dst {
		seen[value.Name] = struct{}{}
	}
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		if _, ok := seen[value.Name]; ok {
			continue
		}
		seen[value.Name] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func appendUniqueDotEntries(dst, values []DotEntry) []DotEntry {
	if len(values) == 0 {
		return dst
	}
	// Included fragments merge after their parent, so a same-name entry from a
	// later fragment replaces the earlier one instead of being dropped.
	seen := make(map[string]int, len(dst))
	for i, value := range dst {
		seen[value.Name] = i
	}
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		if i, ok := seen[value.Name]; ok {
			dst[i] = value
			continue
		}
		seen[value.Name] = len(dst)
		dst = append(dst, value)
	}
	return dst
}

// includeMergeNotices reports duplicate definitions between dst and an
// include fragment about to be merged, so lint can surface that the
// fragment's definition silently wins over the parent's.
func includeMergeNotices(dst, src *RootConfig, includeName string) []string {
	if dst == nil || src == nil {
		return nil
	}
	dstGroups := make(map[string]*GroupConfig, len(dst.Groups))
	for _, group := range dst.Groups {
		if group != nil {
			dstGroups[group.BaseName()] = group
		}
	}
	var notices []string
	for _, group := range src.Groups {
		if group == nil {
			continue
		}
		existing, ok := dstGroups[group.BaseName()]
		if !ok {
			continue
		}
		dotNames := make(map[string]bool, len(existing.Dots))
		for _, dot := range existing.Dots {
			dotNames[dot.Name] = true
		}
		for _, dot := range group.Dots {
			if dot.Name != "" && dotNames[dot.Name] {
				notices = append(notices, fmt.Sprintf(
					"group %q dot entry %q is defined in both the parent config and include %q; the include's definition wins — remove one copy",
					group.BaseName(), dot.Name, includeName))
			}
		}
	}
	return notices
}
