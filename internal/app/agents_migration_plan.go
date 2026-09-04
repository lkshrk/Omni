package app

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

const (
	agentKindPlugin      = "plugin"
	agentKindMarketplace = "marketplace"
	agentKindMCP         = "mcp"

	agentActionImport   = "import"
	agentActionSuppress = "suppress-owned"
	agentActionRetain   = "retain"
	agentActionManaged  = "managed"

	agentReasonNoSource   = "marketplace has no APM source"
	agentReasonPerTarget  = "differs across targets; apm has no per-target MCP scoping"
	agentReasonAmbiguous  = "several installed plugins declare this MCP"
	agentReasonUnsafeName = "identifier or source contains CR/LF/NUL"
)

type agentObservation struct {
	Source      string
	Target      string
	Kind        string
	Identity    string
	Definition  legacyEntry
	Version     string
	InstallRoot string
	Evidence    []string
}

type agentDisposition struct {
	Observation agentObservation
	Action      string
	Owner       string
	Reason      string
}

// resolveAgentDispositions classifies observations without touching them; every ambiguity retains one item.
func resolveAgentDispositions(obs []agentObservation) []agentDisposition {
	sorted := slices.Clone(obs)
	sort.SliceStable(sorted, func(i, j int) bool { return lessAgentObservation(sorted[i], sorted[j]) })

	marketplaceSource, marketplaceReason := resolveMarketplaceSources(sorted)
	owners := pluginMCPOwners(sorted)

	out := make([]agentDisposition, 0, len(sorted))
	emittedMarketplace := map[string]bool{}
	doneMCP := map[string]bool{}
	for i, observation := range sorted {
		switch {
		case unsafeAgentObservation(observation):
			out = append(out, agentDisposition{Observation: observation, Action: agentActionRetain, Reason: agentReasonUnsafeName})
		case observation.Kind == agentKindMarketplace:
			if reason := marketplaceReason[observation.Identity]; reason != "" {
				out = append(out, agentDisposition{Observation: observation, Action: agentActionRetain, Reason: reason})
				continue
			}
			if emittedMarketplace[observation.Identity] {
				continue
			}
			emittedMarketplace[observation.Identity] = true
			observation.Definition.Source = marketplaceSource[observation.Identity]
			out = append(out, agentDisposition{Observation: observation, Action: agentActionImport})
		case observation.Kind == agentKindPlugin:
			reason := marketplaceReason[observation.Definition.Marketplace]
			if _, known := marketplaceSource[observation.Definition.Marketplace]; !known && reason == "" {
				reason = agentReasonNoSource
			}
			if reason != "" {
				out = append(out, agentDisposition{Observation: observation, Action: agentActionRetain, Reason: reason})
				continue
			}
			out = append(out, agentDisposition{Observation: observation, Action: agentActionImport})
		case observation.Kind == agentKindMCP:
			if doneMCP[observation.Identity] {
				continue
			}
			doneMCP[observation.Identity] = true
			out = append(out, resolveMCPGroup(agentObservationGroup(sorted[i:], observation.Identity), owners)...)
		default:
			out = append(out, agentDisposition{Observation: observation, Action: agentActionRetain, Reason: "unknown observation kind"})
		}
	}
	return out
}

func agentObservationGroup(rest []agentObservation, identity string) []agentObservation {
	group := make([]agentObservation, 0, 2)
	for _, observation := range rest {
		if observation.Kind == agentKindMCP && observation.Identity == identity && !unsafeAgentObservation(observation) {
			group = append(group, observation)
		}
	}
	return group
}

func resolveMCPGroup(group []agentObservation, candidates []pluginMCPOwner) []agentDisposition {
	out := make([]agentDisposition, 0, len(group))
	kept := make([]agentObservation, 0, len(group))
	for _, observation := range group {
		owner, owned := mcpOwnerOf(observation, candidates)
		switch {
		case owned && owner == "":
			out = append(out, agentDisposition{Observation: observation, Action: agentActionRetain, Reason: agentReasonAmbiguous})
		case owned:
			out = append(out, agentDisposition{Observation: observation, Action: agentActionSuppress, Owner: owner})
		default:
			kept = append(kept, observation)
		}
	}
	if len(kept) == 0 {
		return out
	}
	canonical := canonicalMCPDefinition(kept[0].Definition)
	equivalent := true
	for _, observation := range kept[1:] {
		if canonicalMCPDefinition(observation.Definition) != canonical {
			equivalent = false
			break
		}
	}
	primary := kept[0]
	if equivalent {
		targets := make([]string, 0, len(kept))
		for _, observation := range kept {
			targets = append(targets, observation.Target)
			primary.Evidence = append(primary.Evidence, observation.Evidence...)
		}
		primary.Definition.Agents = sortedUnique(targets)
		primary.Evidence = sortedUnique(primary.Evidence)
		return append(out, agentDisposition{Observation: primary, Action: agentActionImport})
	}
	primary.Definition.Agents = []string{primary.Target}
	out = append(out, agentDisposition{Observation: primary, Action: agentActionImport})
	for _, observation := range kept[1:] {
		out = append(out, agentDisposition{Observation: observation, Action: agentActionRetain, Reason: agentReasonPerTarget})
	}
	return out
}

func resolveMarketplaceSources(sorted []agentObservation) (map[string]string, map[string]string) {
	seen := map[string][]string{}
	for _, observation := range sorted {
		if observation.Kind != agentKindMarketplace || unsafeAgentObservation(observation) {
			continue
		}
		source := strings.TrimSpace(observation.Definition.Source)
		if source == "" {
			if _, ok := seen[observation.Identity]; !ok {
				seen[observation.Identity] = nil
			}
			continue
		}
		if !slices.Contains(seen[observation.Identity], source) {
			seen[observation.Identity] = append(seen[observation.Identity], source)
		}
	}
	sources, reasons := map[string]string{}, map[string]string{}
	for name, list := range seen {
		sort.Strings(list)
		switch len(list) {
		case 0:
			sources[name], reasons[name] = "", agentReasonNoSource
		case 1:
			sources[name] = list[0]
		default:
			sources[name] = ""
			reasons[name] = "marketplace sources differ across targets: " + strings.Join(list, ", ")
		}
	}
	return sources, reasons
}

type pluginMCPOwner struct {
	Identity     string
	Target       string
	Root         string
	Fingerprints []string
}

func pluginMCPOwners(sorted []agentObservation) []pluginMCPOwner {
	var owners []pluginMCPOwner
	for _, observation := range sorted {
		if observation.Kind != agentKindPlugin || observation.InstallRoot == "" {
			continue
		}
		root := filepath.Clean(observation.InstallRoot)
		fingerprints := pluginDeclaredMCPFingerprints(observation.Target, root)
		if len(fingerprints) == 0 {
			continue
		}
		owners = append(owners, pluginMCPOwner{Identity: observation.Identity, Target: observation.Target, Root: root, Fingerprints: fingerprints})
	}
	return owners
}

// mcpOwnerOf reports the single plugin whose manifest declares an equivalent MCP; "" with true marks several claimants.
func mcpOwnerOf(observation agentObservation, candidates []pluginMCPOwner) (string, bool) {
	var found []string
	for _, candidate := range candidates {
		if candidate.Target != observation.Target {
			continue
		}
		if slices.Contains(candidate.Fingerprints, nativeMCPFingerprint(observation.Definition, candidate.Root)) {
			found = append(found, candidate.Identity)
		}
	}
	switch found = sortedUnique(found); len(found) {
	case 0:
		return "", false
	case 1:
		return found[0], true
	default:
		return "", true
	}
}

func pluginManifestFiles(target string) []string {
	if target == "claude" {
		return []string{filepath.Join(".claude-plugin", "plugin.json"), ".mcp.json"}
	}
	return []string{filepath.Join(".codex-plugin", "mcp.json"), "apm.yml"}
}

func pluginDeclaredMCPFingerprints(target, root string) []string {
	var out []string
	for _, rel := range pluginManifestFiles(target) {
		raw, _, err := readRegularBounded(filepath.Join(root, rel), maxBundleManifestBytes)
		if err != nil {
			continue
		}
		for _, entry := range parsePluginMCPDeclarations(raw, rel) {
			out = append(out, nativeMCPFingerprint(entry, root))
		}
	}
	return sortedUnique(out)
}

type pluginMCPDecl struct {
	Transport   string            `json:"transport" yaml:"transport"`
	Type        string            `json:"type" yaml:"type"`
	Command     string            `json:"command" yaml:"command"`
	Args        []string          `json:"args" yaml:"args"`
	Cwd         string            `json:"cwd" yaml:"cwd"`
	URL         string            `json:"url" yaml:"url"`
	Env         map[string]string `json:"env" yaml:"env"`
	EnvVars     []string          `json:"env_vars" yaml:"env_vars"`
	Headers     map[string]string `json:"headers" yaml:"headers"`
	HTTPHeaders map[string]string `json:"http_headers" yaml:"http_headers"`
}

func (d pluginMCPDecl) entry() legacyEntry {
	entry := legacyEntry{Transport: d.Transport, Command: d.Command, Args: d.Args, Cwd: d.Cwd, URL: d.URL, Env: d.EnvVars, EnvLiteral: d.Env, Headers: d.Headers}
	if entry.Transport == "" {
		entry.Transport = d.Type
	}
	for name, value := range d.HTTPHeaders {
		if entry.Headers == nil {
			entry.Headers = map[string]string{}
		}
		entry.Headers[name] = value
	}
	return entry
}

func parsePluginMCPDeclarations(raw []byte, rel string) []legacyEntry {
	if strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml") {
		var doc struct {
			Dependencies struct {
				MCP []pluginMCPDecl `yaml:"mcp"`
			} `yaml:"dependencies"`
			DevDependencies struct {
				MCP []pluginMCPDecl `yaml:"mcp"`
			} `yaml:"devDependencies"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil
		}
		out := make([]legacyEntry, 0, len(doc.Dependencies.MCP)+len(doc.DevDependencies.MCP))
		for _, decl := range append(slices.Clone(doc.Dependencies.MCP), doc.DevDependencies.MCP...) {
			out = append(out, decl.entry())
		}
		return out
	}
	var doc struct {
		Servers map[string]pluginMCPDecl `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	if len(doc.Servers) == 0 {
		var direct map[string]pluginMCPDecl
		if err := json.Unmarshal(raw, &direct); err != nil {
			return nil
		}
		doc.Servers = direct
	}
	out := make([]legacyEntry, 0, len(doc.Servers))
	for _, name := range slices.Sorted(maps.Keys(doc.Servers)) {
		decl := doc.Servers[name]
		if decl.Command == "" && decl.URL == "" {
			continue
		}
		out = append(out, decl.entry())
	}
	return out
}

// nativeMCPFingerprint keys transport, runtime paths relative to root, and env/header names only.
func nativeMCPFingerprint(entry legacyEntry, root string) string {
	envKeys := sortedUnique(append(slices.Clone(entry.Env), slices.Collect(maps.Keys(entry.EnvLiteral))...))
	args := make([]string, 0, len(entry.Args))
	for _, arg := range entry.Args {
		args = append(args, agentPathRelativeToRoot(arg, root))
	}
	payload := []any{
		nativeMCPTransportOf(entry),
		agentPathRelativeToRoot(entry.Command, root),
		args,
		agentPathRelativeToRoot(entry.Cwd, root),
		strings.TrimSpace(entry.URL),
		envKeys,
		slices.Sorted(maps.Keys(entry.Headers)),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return entry.Command + "\x00" + entry.URL
	}
	return string(raw)
}

func nativeMCPTransportOf(entry legacyEntry) string {
	if transport := normalizeNativeMCPTransport(entry.Transport); transport != "" {
		return transport
	}
	if strings.TrimSpace(entry.URL) != "" {
		return "http"
	}
	return "stdio"
}

// agentPathRelativeToRoot collapses a plugin-rooted path to its relative form so a manifest and its installed copy agree.
func agentPathRelativeToRoot(value, root string) string {
	if value == "" {
		return "."
	}
	if root == "" || !filepath.IsAbs(value) {
		return value
	}
	clean := filepath.Clean(value)
	if clean == root {
		return "."
	}
	if after, ok := strings.CutPrefix(clean, root+string(filepath.Separator)); ok {
		return filepath.ToSlash(after)
	}
	return value
}

func canonicalMCPDefinition(entry legacyEntry) string {
	entry.Name, entry.Agents = "", nil
	return string(mustNativeJSON(entry))
}

func unsafeAgentObservation(observation agentObservation) bool {
	entry := observation.Definition
	values := []string{observation.Identity, observation.Target, entry.Name, entry.Source, entry.Marketplace, entry.Command, entry.Cwd, entry.URL}
	values = append(values, entry.Args...)
	values = append(values, entry.Env...)
	for name, value := range entry.EnvLiteral {
		values = append(values, name, value)
	}
	for name, value := range entry.Headers {
		values = append(values, name, value)
	}
	return slices.ContainsFunc(values, unsafeMigrationScalar)
}

func lessAgentObservation(a, b agentObservation) bool {
	for _, pair := range [][2]string{{a.Kind, b.Kind}, {a.Identity, b.Identity}, {a.Target, b.Target}, {a.Source, b.Source}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return canonicalMCPDefinition(a.Definition) < canonicalMCPDefinition(b.Definition)
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	sort.Strings(out)
	return slices.Compact(out)
}

// nativeAgentDecls turns the imported dispositions into declarations the apm.yml renderer understands.
func nativeAgentDecls(dispositions []agentDisposition) config.LegacyAgentDecls {
	decls := config.LegacyAgentDecls{
		MCPServers:   map[string]json.RawMessage{},
		Plugins:      map[string]json.RawMessage{},
		Marketplaces: map[string]json.RawMessage{},
	}
	pluginTargets := map[string][]string{}
	marketplaceSource := map[string]string{}
	for _, disposition := range dispositions {
		observation := disposition.Observation
		if disposition.Action != agentActionImport {
			continue
		}
		switch observation.Kind {
		case agentKindMarketplace:
			marketplaceSource[observation.Identity] = observation.Definition.Source
		case agentKindPlugin:
			pluginTargets[observation.Identity] = append(pluginTargets[observation.Identity], observation.Target)
		case agentKindMCP:
			entry := observation.Definition
			entry.Name = observation.Identity
			decls.MCPServers[entry.Name] = mustNativeJSON(entry)
		}
	}
	for _, identity := range slices.Sorted(maps.Keys(pluginTargets)) {
		name, marketplace := splitNativePluginIdentity(identity)
		decls.Plugins[identity] = mustNativeJSON(legacyEntry{Name: name, Marketplace: marketplace, Agents: sortedUnique(pluginTargets[identity])})
		if source, ok := marketplaceSource[marketplace]; ok {
			decls.Marketplaces[marketplace] = mustNativeJSON(legacyEntry{Name: marketplace, Source: source})
		}
	}
	return decls
}
