package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

// ForeignAPMEntries — manifest entries this host's config does not declare. Sync preserves them verbatim rather than deleting them, so importing is the only way one ever becomes omni-managed.
type ForeignAPMEntries struct {
	Mcp      []config.McpServer
	Packages []config.SkillPackage
	// dropped — manifest targets omni has no agent id for, per package identity; keeping none of them would widen the entry to every agent.
	dropped map[string][]string
	// registry — mcp entries APM resolves from a registry, which omni's config cannot express.
	registry map[string]bool
}

func (e ForeignAPMEntries) empty() bool {
	return len(e.Mcp) == 0 && len(e.Packages) == 0
}

func (a *App) ForeignAPMEntries() (ForeignAPMEntries, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return ForeignAPMEntries{}, err
	}
	path, err := a.globalAPMManifestPath()
	if err != nil {
		return ForeignAPMEntries{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ForeignAPMEntries{}, nil
		}
		return ForeignAPMEntries{}, fmt.Errorf("reading APM manifest: %w", err)
	}
	root, err := apmManifestRoot(raw)
	if err != nil {
		return ForeignAPMEntries{}, err
	}
	deps := mappingValue(root, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		return ForeignAPMEntries{}, nil
	}
	owned := readAPMOwnedIdentities(path)
	out := ForeignAPMEntries{dropped: map[string][]string{}, registry: map[string]bool{}}
	managedPackages := unionIdentities(managedPackageIdentities(cfg), owned.ownedPackages())
	for _, node := range manifestSequence(deps, "apm") {
		id := apmPackageIdentity(node)
		if id == "" || managedPackages[id] {
			continue
		}
		pkg, dropped, ok := foreignPackageEntry(node)
		if !ok {
			continue
		}
		out.Packages = append(out.Packages, pkg)
		if len(dropped) > 0 {
			out.dropped[id] = dropped
		}
	}
	managedMcp := unionIdentities(managedMcpIdentities(cfg), owned.ownedMcp())
	for _, node := range manifestSequence(deps, "mcp") {
		id := apmMcpIdentity(node)
		if id == "" || managedMcp[id] {
			continue
		}
		var dep apmMcpDependency
		if err := node.Decode(&dep); err != nil {
			continue
		}
		if dep.Registry {
			out.registry[id] = true
		}
		out.Mcp = append(out.Mcp, foreignMcpServer(dep))
	}
	sort.Slice(out.Packages, func(i, j int) bool { return out.Packages[i].Source < out.Packages[j].Source })
	sort.Slice(out.Mcp, func(i, j int) bool { return out.Mcp[i].Name < out.Mcp[j].Name })
	return out, nil
}

func manifestSequence(deps *yaml.Node, key string) []*yaml.Node {
	seq := mappingValue(deps, key)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	return seq.Content
}

func foreignPackageEntry(node *yaml.Node) (pkg config.SkillPackage, dropped []string, ok bool) {
	switch node.Kind {
	case yaml.ScalarNode:
		source, ref, _ := strings.Cut(strings.TrimSpace(node.Value), "#")
		return config.SkillPackage{Source: strings.Trim(source, "/"), Ref: ref}, nil, source != ""
	case yaml.MappingNode:
		var dep apmPackageDependency
		if err := node.Decode(&dep); err != nil || strings.TrimSpace(dep.Git) == "" {
			return config.SkillPackage{}, nil, false
		}
		agents, unsupported := legacyAgentTargets(dep.Targets)
		return config.SkillPackage{
			Source: normalizeAPMPackageIdentity(dep.Git),
			Ref:    strings.TrimSpace(dep.Ref),
			Agents: agents,
		}, unsupported, true
	}
	return config.SkillPackage{}, nil, false
}

// The env inversion mirrors what the generator writes: a ${env:NAME} reference is a name-list entry, and anything else stays a literal so the adoption vetting gets to refuse it.
func foreignMcpServer(dep apmMcpDependency) config.McpServer {
	s := config.McpServer{Name: dep.Name, Transport: dep.Transport, Headers: dep.Headers}
	if urlMcpTransport(dep.Transport) {
		s.URL = dep.URL
	} else {
		s.Command = strings.TrimSpace(strings.Join(append([]string{dep.Command}, dep.Args...), " "))
	}
	for key, value := range dep.Env {
		if value == "${env:"+key+"}" {
			s.Env = append(s.Env, key)
			continue
		}
		if s.EnvLiteral == nil {
			s.EnvLiteral = make(map[string]string, len(dep.Env))
		}
		s.EnvLiteral[key] = value
	}
	sort.Strings(s.Env)
	return s
}

// ImportForeignAPMEntry adopts one preserved manifest entry into the fleet config; the name matches an mcp server, the source a package. Authoring, not sync: this is the one direction that writes omni's config.
func (a *App) ImportForeignAPMEntry(ctx context.Context, name string) (string, []string, error) {
	entries, err := a.ForeignAPMEntries()
	if err != nil {
		return "", nil, err
	}
	target := strings.TrimSpace(name)
	for _, server := range entries.Mcp {
		if server.Name != target {
			continue
		}
		// Omni's config has no registry field, so an imported entry would regenerate as a plain one and lose the resolution APM does for it.
		if entries.registry[server.Name] {
			return "", nil, fmt.Errorf("mcp server %q is a registry entry: omni's config cannot declare one, so it stays preserved in the manifest; declare it manually to manage it here",
				server.Name)
		}
		if err := a.importForeignMcpServer(ctx, server); err != nil {
			return "", nil, err
		}
		return "mcp server " + server.Name, nil, nil
	}
	for _, pkg := range entries.Packages {
		if pkg.Source != target && normalizeAPMPackageIdentity(pkg.Source) != normalizeAPMPackageIdentity(target) {
			continue
		}
		dropped := entries.dropped[normalizeAPMPackageIdentity(pkg.Source)]
		warnings, err := a.importForeignPackage(pkg, dropped)
		if err != nil {
			return "", nil, err
		}
		return "package " + pkg.Source, warnings, nil
	}
	if entries.empty() {
		return "", nil, fmt.Errorf("no entries in the APM manifest are undeclared by omni's config")
	}
	return "", nil, fmt.Errorf("%q is not an undeclared entry in the APM manifest", target)
}

// The same vetting an agent CLI's registration gets: a manifest anyone can hand-edit is no more trusted a source of credentials than a live one.
func (a *App) importForeignMcpServer(ctx context.Context, server config.McpServer) error {
	env, refusals := a.McpAdoptCheck(InstalledMcpServer{
		Name:       server.Name,
		Transport:  server.Transport,
		Command:    server.Command,
		URL:        server.URL,
		Headers:    server.Headers,
		EnvLiteral: server.EnvLiteral,
	})
	if len(refusals) > 0 {
		return errors.New(strings.Join(refusals, "; "))
	}
	server.Env = append(server.Env, env...)
	sort.Strings(server.Env)
	server.EnvLiteral = nil
	_, err := a.AddMcpServer(ctx, server)
	return err
}

// A hand-edited manifest's git: and ref: values can carry a remote helper, a credential-bearing URL, or a git option, so they get the vetting a typed argument gets.
func (a *App) importForeignPackage(pkg config.SkillPackage, dropped []string) ([]string, error) {
	locator := pkg.Source
	if ref := strings.TrimSpace(pkg.Ref); ref != "" {
		// A ref carrying the locator's own separators reparses as a different source than the manifest declared.
		if strings.ContainsAny(ref, "#@") {
			return nil, fmt.Errorf("package %q has a ref %q containing '#' or '@'; declare it manually", pkg.Source, ref)
		}
		locator += "#" + ref
	}
	parsed, err := agent.ParseSkillSource(locator)
	if err != nil {
		return nil, err
	}
	if err := refuseCredentialSource(pkg.Source); err != nil {
		return nil, err
	}
	if err := refuseCredentialSource(parsed.Source); err != nil {
		return nil, err
	}
	if len(dropped) > 0 && len(pkg.Agents) == 0 {
		return nil, fmt.Errorf("package %q targets only %s, which omni has no agent for; declare it manually to choose the agents",
			pkg.Source, strings.Join(dropped, ", "))
	}
	pkg.Source, pkg.Ref = parsed.Source, parsed.Ref
	if err := a.withConfig(func(c *config.RootConfig) error {
		merged, _ := upsertNormalizedPackages(c.Agents.Packages, pkg)
		c.Agents.Packages = merged
		return nil
	}); err != nil {
		return nil, err
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	return []string{fmt.Sprintf("apm targets %s dropped: omni has no agent for them; imported for %s only",
		strings.Join(dropped, ", "), strings.Join(pkg.Agents, ", "))}, nil
}

// A userinfo component is a credential the manifest would persist into the fleet config in plaintext and then hand to every host that syncs it.
func refuseCredentialSource(source string) error {
	raw := strings.TrimSpace(source)
	if !strings.Contains(raw, "://") {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid skill source %q: %w", source, err)
	}
	if parsed.User != nil {
		return fmt.Errorf("skill source %q carries credentials in its URL", source)
	}
	return nil
}
