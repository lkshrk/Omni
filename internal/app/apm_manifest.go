package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

// APM refuses to parse a manifest without these, so a generated file carries them even when nothing else is declared.
const (
	apmManifestDefaultName    = "omni-host"
	apmManifestDefaultVersion = "0.0.0"
)

// apmPackageDependency is APM's object-form git dependency. Any field outside {alias, allow_insecure, git, path, ref, skills, targets, type} is a hard parse error.
type apmPackageDependency struct {
	Git     string   `yaml:"git"`
	Ref     string   `yaml:"ref,omitempty"`
	Targets []string `yaml:"targets,omitempty"`
}

// apmMcpDependency mirrors dependencies.mcp exactly. Unknown keys are swept into APM's extra passthrough and flattened verbatim into every deployed agent config, and a targets key has no scoping effect at all.
type apmMcpDependency struct {
	Name      string            `yaml:"name"`
	Registry  bool              `yaml:"registry"`
	Transport string            `yaml:"transport"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
}

// apmManifestPlan — Managed* hold every identity the Omni config declares, so an entry this host resolves away is dropped while an entry Omni never declared is preserved verbatim.
type apmManifestPlan struct {
	Packages        []apmPackageDependency
	Mcp             []apmMcpDependency
	ManagedPackages map[string]bool
	ManagedMcp      map[string]bool
	// A skipped surface is left untouched; regenerating it from an incomplete set would prune entries.
	SkipPackages bool
	SkipMcp      bool
}

// Managed* count only the entries omni owns; the totals include the foreign entries the merge preserves.
type apmManifestRender struct {
	Content             []byte
	PackageCount        int
	McpCount            int
	ManagedPackageCount int
	ManagedMcpCount     int
}

func renderAPMManifest(existing []byte, plan apmManifestPlan) (apmManifestRender, error) {
	root, err := apmManifestRoot(existing)
	if err != nil {
		return apmManifestRender{}, err
	}
	ensureManifestScalar(root, "name", apmManifestDefaultName)
	ensureManifestScalar(root, "version", apmManifestDefaultVersion)

	deps := mappingValue(root, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		deps = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(root, "dependencies", deps)
	}

	var render apmManifestRender
	if plan.SkipPackages {
		render.PackageCount, render.ManagedPackageCount = manifestSequenceCounts(deps, "apm", plan.ManagedPackages, apmPackageIdentity)
	} else {
		packageNodes, err := manifestEntryNodes(plan.Packages)
		if err != nil {
			return apmManifestRender{}, err
		}
		render.PackageCount, render.ManagedPackageCount = mergeManagedSequence(deps, "apm", packageNodes, plan.ManagedPackages, apmPackageIdentity)
	}

	if plan.SkipMcp {
		render.McpCount, render.ManagedMcpCount = manifestSequenceCounts(deps, "mcp", plan.ManagedMcp, apmMcpIdentity)
	} else {
		mcpNodes, mcpErr := manifestEntryNodes(plan.Mcp)
		if mcpErr != nil {
			return apmManifestRender{}, mcpErr
		}
		render.McpCount, render.ManagedMcpCount = mergeManagedSequence(deps, "mcp", mcpNodes, plan.ManagedMcp, apmMcpIdentity)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return apmManifestRender{}, fmt.Errorf("rendering APM manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return apmManifestRender{}, fmt.Errorf("rendering APM manifest: %w", err)
	}
	render.Content = buf.Bytes()
	return render, nil
}

func manifestSequenceCounts(deps *yaml.Node, key string, managed map[string]bool, identity func(*yaml.Node) string) (total, owned int) {
	for _, node := range manifestSequence(deps, key) {
		total++
		if id := identity(node); id != "" && managed[id] {
			owned++
		}
	}
	return total, owned
}

// apmConvergePlan — entries for the surfaces the caller regenerates, and nothing at all for the surfaces it does not.
type apmConvergePlan struct {
	Packages     []apmPackageDependency
	Mcp          []apmMcpDependency
	SyncPackages bool
	SyncMcp      bool
	// RetiredMcp is a name the config no longer declares, which the merge would otherwise keep as foreign.
	RetiredMcp string
	DryRun     bool
}

// Managed* count omni's own entries after this convergence; PriorManaged* what the last generation recorded.
type apmConvergeResult struct {
	Existing             []byte
	Content              []byte
	PackageCount         int
	McpCount             int
	ManagedPackages      int
	ManagedMcp           int
	PriorManagedPackages int
	PriorManagedMcp      int
	// PendingOwned is what a synced surface records as applied once its own install succeeds. Advancing that half here instead would drop a pruned identity the failed install never retired, leaving nothing for the next sync to retry from.
	PendingOwned apmSurfaceIdentities
}

// A surface the plan does not name keeps both its manifest entries and its ownership record, so a verb reconciling one surface never prunes the other.
func (a *App) convergeAPMManifest(cfg *config.RootConfig, manifestPath string, plan apmConvergePlan) (apmConvergeResult, error) {
	existing, err := os.ReadFile(manifestPath)
	if err != nil && !os.IsNotExist(err) {
		return apmConvergeResult{}, fmt.Errorf("reading APM manifest: %w", err)
	}
	owned := readAPMOwnedIdentities(manifestPath)
	managedMcp := unionIdentities(managedMcpIdentities(cfg), owned.ownedMcp())
	if name := strings.TrimSpace(plan.RetiredMcp); name != "" {
		managedMcp[name] = true
	}
	render, err := renderAPMManifest(existing, apmManifestPlan{
		Packages:        plan.Packages,
		Mcp:             plan.Mcp,
		ManagedPackages: unionIdentities(managedPackageIdentities(cfg), owned.ownedPackages()),
		ManagedMcp:      managedMcp,
		SkipPackages:    !plan.SyncPackages,
		SkipMcp:         !plan.SyncMcp,
	})
	if err != nil {
		return apmConvergeResult{}, err
	}
	result := apmConvergeResult{
		Existing:             existing,
		Content:              render.Content,
		PackageCount:         render.PackageCount,
		McpCount:             render.McpCount,
		ManagedPackages:      render.ManagedPackageCount,
		ManagedMcp:           render.ManagedMcpCount,
		PriorManagedPackages: len(owned.Applied.Packages),
		PriorManagedMcp:      len(owned.Applied.Mcp),
		PendingOwned:         owned.Applied,
	}
	if plan.SyncPackages {
		result.PendingOwned.Packages = sortedBoolMapKeys(managedPackageIdentities(cfg))
	}
	if plan.SyncMcp {
		result.PendingOwned.Mcp = sortedBoolMapKeys(managedMcpIdentities(cfg))
	}
	if plan.DryRun {
		return result, nil
	}
	// Written unconditionally: the manifest is this host's declarative intent, and the next sync regenerates it from the same config anyway.
	if err := writeAPMManifest(manifestPath, render.Content); err != nil {
		return result, err
	}
	if err := advanceAPMRendered(manifestPath, result.PendingOwned, plan.SyncPackages, plan.SyncMcp); err != nil {
		return result, err
	}
	return result, nil
}

// apmManagedSurfaces counts omni's entries per surface, now and as the last generation recorded them; declaredPackages is every package entry the manifest carries, foreign ones included.
type apmManagedSurfaces struct {
	declaredPackages int
	packages         int
	priorPackages    int
	mcp              int
	priorMcp         int
}

// managedAPMSurfaces reads the counts off a manifest no convergence rewrote, which is the frozen replay's case.
func managedAPMSurfaces(cfg *config.RootConfig, manifestPath string, existing []byte) (apmManagedSurfaces, error) {
	owned := readAPMOwnedIdentities(manifestPath)
	surfaces := apmManagedSurfaces{priorPackages: len(owned.Applied.Packages), priorMcp: len(owned.Applied.Mcp)}
	if len(bytes.TrimSpace(existing)) == 0 {
		return surfaces, nil
	}
	root, err := apmManifestRoot(existing)
	if err != nil {
		return apmManagedSurfaces{}, err
	}
	deps := mappingValue(root, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		return surfaces, nil
	}
	surfaces.declaredPackages, surfaces.packages = manifestSequenceCounts(deps, "apm",
		unionIdentities(managedPackageIdentities(cfg), owned.ownedPackages()), apmPackageIdentity)
	_, surfaces.mcp = manifestSequenceCounts(deps, "mcp",
		unionIdentities(managedMcpIdentities(cfg), owned.ownedMcp()), apmMcpIdentity)
	return surfaces, nil
}

// apmManifestDelta names what a sync would change, per surface. The dry run falls back to it whenever the generated manifest differs from the one on disk: APM would otherwise preview the stale file.
type apmManifestDelta struct {
	PackagesAdded     []string
	PackagesRemoved   []string
	PackagesUnchanged []string
	McpAdded          []string
	McpRemoved        []string
	McpUnchanged      []string
}

func (d apmManifestDelta) lines() []string {
	var out []string
	for _, section := range []struct {
		label                     string
		added, removed, unchanged []string
	}{
		{"packages", d.PackagesAdded, d.PackagesRemoved, d.PackagesUnchanged},
		{"mcp", d.McpAdded, d.McpRemoved, d.McpUnchanged},
	} {
		for _, name := range section.added {
			out = append(out, fmt.Sprintf("%s: would add %s", section.label, name))
		}
		for _, name := range section.removed {
			out = append(out, fmt.Sprintf("%s: would remove %s", section.label, name))
		}
		for _, name := range section.unchanged {
			out = append(out, fmt.Sprintf("%s: unchanged %s", section.label, name))
		}
	}
	return out
}

func apmManifestDiff(existing, generated []byte) (apmManifestDelta, error) {
	before, err := apmManifestIdentities(existing)
	if err != nil {
		return apmManifestDelta{}, err
	}
	after, err := apmManifestIdentities(generated)
	if err != nil {
		return apmManifestDelta{}, err
	}
	var delta apmManifestDelta
	delta.PackagesAdded, delta.PackagesRemoved, delta.PackagesUnchanged = diffIdentities(before["apm"], after["apm"])
	delta.McpAdded, delta.McpRemoved, delta.McpUnchanged = diffIdentities(before["mcp"], after["mcp"])
	return delta, nil
}

func diffIdentities(before, after []string) (added, removed, unchanged []string) {
	prior := identitySet(before)
	for _, name := range after {
		if prior[name] {
			unchanged = append(unchanged, name)
			continue
		}
		added = append(added, name)
	}
	current := identitySet(after)
	for _, name := range before {
		if !current[name] {
			removed = append(removed, name)
		}
	}
	return added, removed, unchanged
}

func apmManifestIdentities(raw []byte) (map[string][]string, error) {
	out := map[string][]string{"apm": nil, "mcp": nil}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	root, err := apmManifestRoot(raw)
	if err != nil {
		return nil, err
	}
	deps := mappingValue(root, "dependencies")
	if deps == nil || deps.Kind != yaml.MappingNode {
		return out, nil
	}
	for key, identity := range map[string]func(*yaml.Node) string{"apm": apmPackageIdentity, "mcp": apmMcpIdentity} {
		for _, node := range manifestSequence(deps, key) {
			if id := identity(node); id != "" {
				out[key] = append(out[key], id)
			}
		}
	}
	return out, nil
}

// writeAPMManifest — the file must exist even with zero dependencies; a missing manifest makes apm install exit 1.
func writeAPMManifest(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating APM manifest directory: %w", err)
	}
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if apmManifestEquivalent(existing, content) {
			return nil
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("reading APM manifest: %w", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("writing APM manifest: %w", err)
	}
	return nil
}

// The APM CLI reindents the whole file on its own installs, so a text diff reports churn that is not there.
func apmManifestEquivalent(a, b []byte) bool {
	var left, right any
	if yaml.Unmarshal(a, &left) != nil || yaml.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func apmManifestRoot(existing []byte) (*yaml.Node, error) {
	if strings.TrimSpace(string(existing)) == "" {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, fmt.Errorf("parsing existing APM manifest: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = *doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("existing APM manifest is not a YAML mapping")
	}
	return &doc, nil
}

// mergeManagedSequence rewrites the entries Omni declares in place and appends the rest, leaving every foreign entry — and its position — untouched.
func mergeManagedSequence(
	deps *yaml.Node,
	key string,
	generated []*yaml.Node,
	managed map[string]bool,
	identity func(*yaml.Node) string,
) (total, owned int) {
	byIdentity := make(map[string]*yaml.Node, len(generated))
	order := make([]string, 0, len(generated))
	for _, node := range generated {
		id := identity(node)
		if _, seen := byIdentity[id]; seen {
			continue
		}
		byIdentity[id] = node
		order = append(order, id)
	}

	merged := make([]*yaml.Node, 0, len(generated))
	placed := make(map[string]bool, len(generated))
	if existing := mappingValue(deps, key); existing != nil && existing.Kind == yaml.SequenceNode {
		for _, item := range existing.Content {
			id := identity(item)
			if id == "" || !managed[id] {
				merged = append(merged, item)
				continue
			}
			if node, ok := byIdentity[id]; ok && !placed[id] {
				merged = append(merged, node)
				placed[id] = true
			}
		}
	}
	for _, id := range order {
		if placed[id] {
			continue
		}
		merged = append(merged, byIdentity[id])
		placed[id] = true
	}
	setMappingValue(deps, key, &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: merged})
	return len(merged), len(placed)
}

// The ref is regenerated with the rest of the entry, so only the repo coordinate identifies a package.
func apmPackageIdentity(node *yaml.Node) string {
	switch node.Kind {
	case yaml.ScalarNode:
		return normalizeAPMPackageIdentity(node.Value)
	case yaml.MappingNode:
		if git := mappingValue(node, "git"); git != nil {
			return normalizeAPMPackageIdentity(git.Value)
		}
	}
	return ""
}

func normalizeAPMPackageIdentity(raw string) string {
	source, _, _ := strings.Cut(strings.TrimSpace(raw), "#")
	return strings.Trim(source, "/")
}

func apmMcpIdentity(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	if name := mappingValue(node, "name"); name != nil {
		return strings.TrimSpace(name.Value)
	}
	return ""
}

func manifestEntryNodes[T any](entries []T) ([]*yaml.Node, error) {
	nodes := make([]*yaml.Node, 0, len(entries))
	for _, entry := range entries {
		var node yaml.Node
		if err := node.Encode(entry); err != nil {
			return nil, fmt.Errorf("encoding APM manifest entry: %w", err)
		}
		nodes = append(nodes, &node)
	}
	return nodes, nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func ensureManifestScalar(m *yaml.Node, key, fallback string) {
	if v := mappingValue(m, key); v != nil && strings.TrimSpace(v.Value) != "" {
		return
	}
	setMappingValue(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fallback})
}
