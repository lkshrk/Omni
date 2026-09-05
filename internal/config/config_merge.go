package config

import (
	"fmt"
	"slices"
	"strings"
)

// MergeRootConfig — Later fragments win on scalar and map fields; groups append and dedupe by name, agent arrays merge by identity.
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
	mergeAgents(dst, src.Agents)
}

func mergeAgents(dst *RootConfig, src *AgentsConfig) {
	if src == nil {
		return
	}
	if dst.Agents == nil {
		dst.Agents = &AgentsConfig{}
	}
	for _, entry := range src.Ignored {
		index := slices.IndexFunc(dst.Agents.Ignored, func(existing AgentIgnoreEntry) bool {
			return existing.Host == entry.Host && existing.Target == entry.Target &&
				existing.Kind == entry.Kind && existing.ID == entry.ID
		})
		if index >= 0 {
			dst.Agents.Ignored[index] = entry
			continue
		}
		dst.Agents.Ignored = append(dst.Agents.Ignored, entry)
	}
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
	// Fragments merge after their parent, so a same-name entry replaces the earlier one instead of being dropped.
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

// Lets lint surface that a fragment's definition silently wins over the parent's.
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
