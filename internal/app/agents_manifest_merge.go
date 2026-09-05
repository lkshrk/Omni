package app

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	manifestKindPackage     = "apm"
	manifestKindMCP         = "mcp"
	manifestKindLSP         = "lsp"
	manifestKindMarketplace = "marketplace"
)

type manifestMCPCandidate struct {
	Dep   apmMCPDep
	Reach []string
}

type manifestCandidates struct {
	Packages     []apmPackageDep
	MCP          []manifestMCPCandidate
	LSP          []apmLSPDep
	Marketplaces []marketplaceDecl
}

type manifestMergeEntry struct {
	Kind     string
	Identity string
	Targets  []string
}

type manifestMergeRejection struct {
	Kind     string
	Identity string
	Reason   string
}

type manifestMergeReport struct {
	Appended  []manifestMergeEntry
	Unioned   []manifestMergeEntry
	Rejected  []manifestMergeRejection
	Decisions []string
}

type manifestLineEdit struct {
	start int
	end   int
	order int
	lines []string
}

// mergeAgentsManifest appends only genuinely new declarations; every byte of an untouched declaration survives verbatim.
func mergeAgentsManifest(existing []byte, candidates manifestCandidates) ([]byte, manifestMergeReport, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, manifestMergeReport{}, fmt.Errorf("parse agents manifest: %w", err)
	}
	if len(doc.Content) == 0 {
		return generateAgentsManifest(existing, candidates)
	}
	root := yamlDocumentMapping(&doc)
	if root == nil {
		return nil, manifestMergeReport{}, fmt.Errorf("agents manifest is not a mapping document")
	}
	if yamlHasUnsafeOwnershipSyntax(root) {
		return nil, manifestMergeReport{}, fmt.Errorf("agents manifest uses anchors, aliases, or merges; refusing to edit it")
	}
	m := &manifestMerger{lines: strings.Split(string(existing), "\n"), root: root}
	if err := m.merge(candidates); err != nil {
		return nil, manifestMergeReport{}, err
	}
	return []byte(strings.Join(applyManifestEdits(m.lines, m.edits), "\n")), m.report, nil
}

type manifestMerger struct {
	lines  []string
	root   *yaml.Node
	edits  []manifestLineEdit
	report manifestMergeReport
}

func (m *manifestMerger) merge(candidates manifestCandidates) error {
	deps := yamlMappingValue(m.root, "dependencies")
	if deps != nil && deps.Kind != yaml.MappingNode {
		return fmt.Errorf("agents manifest dependencies is not a mapping")
	}
	newItems := map[string][]any{}
	if err := m.mergePackages(deps, candidates.Packages, newItems); err != nil {
		return err
	}
	if err := m.mergeServices(deps, candidates, newItems); err != nil {
		return err
	}
	if err := m.placeNewItems(deps, newItems); err != nil {
		return err
	}
	m.mergeMarketplaces(candidates.Marketplaces)
	return nil
}

func (m *manifestMerger) mergePackages(deps *yaml.Node, candidates []apmPackageDep, newItems map[string][]any) error {
	seq := yamlMappingValue(deps, manifestKindPackage)
	index, err := manifestPackageIndex(seq)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		identity, err := apmPackageIdentity(candidate)
		if err != nil {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindPackage, Reason: err.Error()})
			continue
		}
		item, found := index[identity]
		if !found {
			newItems[manifestKindPackage] = append(newItems[manifestKindPackage], candidate)
			m.report.Appended = append(m.report.Appended, manifestMergeEntry{Kind: manifestKindPackage, Identity: identity, Targets: slices.Clone(candidate.Targets)})
			continue
		}
		var current apmPackageDep
		if err := item.Decode(&current); err != nil {
			return fmt.Errorf("decode apm dependency %s: %w", identity, err)
		}
		if field, ok := packageDefinitionConflict(current, candidate); ok {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindPackage, Identity: identity, Reason: "declared " + field + " differs from the candidate"})
			continue
		}
		union := sortedUnique(append(slices.Clone(current.Targets), candidate.Targets...))
		if slices.Equal(union, sortedUnique(current.Targets)) {
			continue
		}
		if err := m.unionTargets(item, union); err != nil {
			return err
		}
		m.report.Unioned = append(m.report.Unioned, manifestMergeEntry{Kind: manifestKindPackage, Identity: identity, Targets: union})
	}
	return nil
}

func (m *manifestMerger) mergeServices(deps *yaml.Node, candidates manifestCandidates, newItems map[string][]any) error {
	rootTargets := manifestRootTargets(m.root)
	mcpIndex, err := manifestNameIndex(yamlMappingValue(deps, manifestKindMCP))
	if err != nil {
		return err
	}
	for _, candidate := range candidates.MCP {
		identity := candidate.Dep.Name
		if identity == "" {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindMCP, Reason: "mcp candidate has no name"})
			continue
		}
		m.noteReach(manifestKindMCP, identity, candidate.Reach, rootTargets)
		item, found := mcpIndex[identity]
		if !found {
			newItems[manifestKindMCP] = append(newItems[manifestKindMCP], candidate.Dep)
			m.report.Appended = append(m.report.Appended, manifestMergeEntry{Kind: manifestKindMCP, Identity: identity})
			continue
		}
		var current apmMCPDep
		if err := item.Decode(&current); err != nil {
			return fmt.Errorf("decode mcp dependency %s: %w", identity, err)
		}
		current.Raw, candidate.Dep.Raw = nil, nil
		if !reflect.DeepEqual(current, candidate.Dep) {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindMCP, Identity: identity, Reason: "declared definition differs from the candidate"})
		}
	}

	lspIndex, err := manifestNameIndex(yamlMappingValue(deps, manifestKindLSP))
	if err != nil {
		return err
	}
	for _, candidate := range candidates.LSP {
		identity := candidate.Name
		if identity == "" {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindLSP, Reason: "lsp candidate has no name"})
			continue
		}
		item, found := lspIndex[identity]
		if !found {
			newItems[manifestKindLSP] = append(newItems[manifestKindLSP], candidate)
			m.report.Appended = append(m.report.Appended, manifestMergeEntry{Kind: manifestKindLSP, Identity: identity})
			continue
		}
		var current apmLSPDep
		if err := item.Decode(&current); err != nil {
			return fmt.Errorf("decode lsp dependency %s: %w", identity, err)
		}
		current.Raw, candidate.Raw = nil, nil
		if !reflect.DeepEqual(current, candidate) {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindLSP, Identity: identity, Reason: "declared definition differs from the candidate"})
		}
	}
	return nil
}

func (m *manifestMerger) noteReach(kind, identity string, reach, rootTargets []string) {
	if len(rootTargets) == 0 {
		return
	}
	var missing []string
	for _, target := range reach {
		if !slices.Contains(rootTargets, target) {
			missing = append(missing, target)
		}
	}
	if len(missing) == 0 {
		return
	}
	m.report.Decisions = append(m.report.Decisions, fmt.Sprintf("%s %s reaches %s, which manifest targets do not cover; widening targets would redeploy every other service", kind, identity, strings.Join(sortedUnique(missing), ", ")))
}

func (m *manifestMerger) mergeMarketplaces(candidates []marketplaceDecl) {
	existing := map[string]string{}
	for _, decl := range parseTemplateMarketplaces([]byte(strings.Join(m.lines, "\n"))) {
		existing[decl.name] = decl.source
	}
	var added []string
	for _, candidate := range candidates {
		if candidate.name == "" || candidate.source == "" {
			m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindMarketplace, Identity: candidate.name, Reason: "marketplace candidate needs both a name and a source"})
			continue
		}
		source, found := existing[candidate.name]
		if found {
			if source != candidate.source {
				m.report.Rejected = append(m.report.Rejected, manifestMergeRejection{Kind: manifestKindMarketplace, Identity: candidate.name, Reason: "declared source differs from the candidate"})
			}
			continue
		}
		existing[candidate.name] = candidate.source
		added = append(added, "# "+candidate.Render())
		m.report.Appended = append(m.report.Appended, manifestMergeEntry{Kind: manifestKindMarketplace, Identity: candidate.name})
	}
	if len(added) == 0 {
		return
	}
	at := len(m.lines)
	if at > 0 && strings.TrimSpace(m.lines[at-1]) == "" {
		at--
	}
	m.edit(at, at, added)
}

func (m *manifestMerger) placeNewItems(deps *yaml.Node, newItems map[string][]any) error {
	if len(newItems) == 0 {
		return nil
	}
	if deps == nil {
		return m.createDependencies(newItems)
	}
	if len(deps.Content) == 0 {
		return m.replaceEmptyDependencies(deps, newItems)
	}
	if deps.Style&yaml.FlowStyle != 0 {
		return fmt.Errorf("agents manifest dependencies is not a block mapping")
	}
	childIndent := manifestChildIndent(deps)
	for _, kind := range []string{manifestKindPackage, manifestKindMCP, manifestKindLSP} {
		items := newItems[kind]
		if len(items) == 0 {
			continue
		}
		seq := yamlMappingValue(deps, kind)
		if seq != nil && len(seq.Content) > 0 {
			if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
				return fmt.Errorf("agents manifest %s list is not a block sequence", kind)
			}
			indent := m.itemIndent(seq)
			rendered, err := renderManifestItems(items, indent)
			if err != nil {
				return err
			}
			at := m.blockEnd(seq, indent)
			m.edit(at, at, rendered)
			continue
		}
		rendered, err := renderManifestItems(items, childIndent+2)
		if err != nil {
			return err
		}
		block := append([]string{strings.Repeat(" ", childIndent) + kind + ":"}, rendered...)
		if seq != nil {
			m.edit(seq.Line-1, seq.Line, block)
			continue
		}
		at := m.blockEnd(deps, childIndent)
		m.edit(at, at, block)
	}
	return nil
}

// An empty mapping carries no child node to indent against, so the whole line is rewritten as a block.
func (m *manifestMerger) replaceEmptyDependencies(deps *yaml.Node, newItems map[string][]any) error {
	line := deps.Line
	if line < 1 || line > len(m.lines) {
		return fmt.Errorf("agents manifest dependencies is not on a readable line")
	}
	text := m.lines[line-1]
	indent := len(text) - len(strings.TrimLeft(text, " "))
	block, err := renderDependencyBlock(newItems, indent)
	if err != nil {
		return err
	}
	m.edit(line-1, line, block)
	return nil
}

func renderDependencyBlock(newItems map[string][]any, indent int) ([]string, error) {
	pad := strings.Repeat(" ", indent)
	block := []string{pad + "dependencies:"}
	for _, kind := range []string{manifestKindPackage, manifestKindMCP, manifestKindLSP} {
		items := newItems[kind]
		if len(items) == 0 {
			continue
		}
		rendered, err := renderManifestItems(items, indent+4)
		if err != nil {
			return nil, err
		}
		block = append(block, pad+"  "+kind+":")
		block = append(block, rendered...)
	}
	return block, nil
}

func (m *manifestMerger) createDependencies(newItems map[string][]any) error {
	block := []string{"dependencies:"}
	for _, kind := range []string{manifestKindPackage, manifestKindMCP, manifestKindLSP} {
		items := newItems[kind]
		if len(items) == 0 {
			continue
		}
		rendered, err := renderManifestItems(items, 4)
		if err != nil {
			return err
		}
		block = append(block, "  "+kind+":")
		block = append(block, rendered...)
	}
	at := m.blockEnd(m.root, 0)
	m.edit(at, at, block)
	return nil
}

// itemIndent reads the dash column from the source, because a block item node's own column names its first key.
func (m *manifestMerger) itemIndent(seq *yaml.Node) int {
	line := seq.Content[0].Line
	if line >= 1 && line <= len(m.lines) {
		text := m.lines[line-1]
		if indent := len(text) - len(strings.TrimLeft(text, " ")); indent < len(text) && text[indent] == '-' {
			return indent
		}
	}
	return seq.Column - 1
}

// blockEnd returns the line index just past a node's own lines, skipping wrapped scalar continuations.
func (m *manifestMerger) blockEnd(node *yaml.Node, indent int) int {
	line := yamlMaxLine(node)
	for line < len(m.lines) {
		text := m.lines[line]
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			break
		}
		if len(text)-len(strings.TrimLeft(text, " ")) <= indent {
			break
		}
		line++
	}
	return line
}

func (m *manifestMerger) unionTargets(item *yaml.Node, targets []string) error {
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value != "targets" {
			continue
		}
		key, value := item.Content[i], item.Content[i+1]
		indent := key.Column - 1
		rendered, err := renderManifestField("targets", targets, indent)
		if err != nil {
			return err
		}
		m.edit(key.Line-1, m.blockEnd(value, indent), rendered)
		return nil
	}
	indent := item.Column - 1
	if len(item.Content) > 0 {
		indent = item.Content[0].Column - 1
	}
	rendered, err := renderManifestField("targets", targets, indent)
	if err != nil {
		return err
	}
	at := m.blockEnd(item, indent)
	m.edit(at, at, rendered)
	return nil
}

func (m *manifestMerger) edit(start, end int, lines []string) {
	m.edits = append(m.edits, manifestLineEdit{start: start, end: end, order: len(m.edits), lines: lines})
}

func applyManifestEdits(lines []string, edits []manifestLineEdit) []string {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].order > edits[j].order
	})
	out := slices.Clone(lines)
	for _, edit := range edits {
		tail := slices.Clone(out[edit.end:])
		out = append(append(out[:edit.start:edit.start], edit.lines...), tail...)
	}
	return out
}

func manifestChildIndent(deps *yaml.Node) int {
	if len(deps.Content) > 0 {
		return deps.Content[0].Column - 1
	}
	return deps.Column + 1
}

func manifestRootTargets(root *yaml.Node) []string {
	value := yamlMappingValue(root, "targets")
	if value == nil {
		return nil
	}
	var targets []string
	if err := value.Decode(&targets); err != nil {
		return nil
	}
	return targets
}

func manifestPackageIndex(seq *yaml.Node) (map[string]*yaml.Node, error) {
	index := map[string]*yaml.Node{}
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return index, nil
	}
	for _, item := range seq.Content {
		var dep apmPackageDep
		if err := item.Decode(&dep); err != nil {
			return nil, fmt.Errorf("decode apm dependency: %w", err)
		}
		identity, err := apmPackageIdentity(dep)
		if err != nil {
			return nil, err
		}
		if _, clash := index[identity]; clash {
			return nil, fmt.Errorf("agents manifest declares %s twice", identity)
		}
		index[identity] = item
	}
	return index, nil
}

func manifestNameIndex(seq *yaml.Node) (map[string]*yaml.Node, error) {
	index := map[string]*yaml.Node{}
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return index, nil
	}
	for _, item := range seq.Content {
		name := yamlScalarValue(item, "name")
		if name == "" {
			return nil, fmt.Errorf("agents manifest declares a service without a name")
		}
		if _, clash := index[name]; clash {
			return nil, fmt.Errorf("agents manifest declares service %q twice", name)
		}
		index[name] = item
	}
	return index, nil
}

// apmPackageIdentity keys a package by its source; ref belongs to the definition, so one repo has one entry.
func apmPackageIdentity(dep apmPackageDep) (string, error) {
	switch {
	case dep.Path != "":
		return "path:" + filepath.Clean(dep.Path), nil
	case dep.Git != "":
		return "git:" + dep.Git, nil
	case dep.Marketplace != "" && dep.Name != "":
		return "mkt:" + dep.Marketplace + "/" + dep.Name, nil
	case dep.Name != "":
		return "name:" + dep.Name, nil
	default:
		return "", fmt.Errorf("apm dependency has neither a path, a git source, nor a name")
	}
}

func packageDefinitionConflict(current, candidate apmPackageDep) (string, bool) {
	for _, field := range []struct {
		name            string
		current, wanted string
	}{
		{"git", current.Git, candidate.Git},
		{"path", current.Path, candidate.Path},
		{"name", current.Name, candidate.Name},
		{"marketplace", current.Marketplace, candidate.Marketplace},
		{"ref", current.Ref, candidate.Ref},
	} {
		if field.current != field.wanted {
			return field.name, true
		}
	}
	return "", false
}

func renderManifestItems(items []any, indent int) ([]string, error) {
	var out []string
	for _, item := range items {
		lines, err := renderManifestItem(item, indent)
		if err != nil {
			return nil, err
		}
		out = append(out, lines...)
	}
	return out, nil
}

func renderManifestItem(value any, indent int) ([]string, error) {
	body, err := encodeManifestFragment(value)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body))
	for i, line := range body {
		if i == 0 {
			out = append(out, strings.Repeat(" ", indent)+"- "+line)
			continue
		}
		out = append(out, strings.Repeat(" ", indent+2)+line)
	}
	return out, nil
}

func renderManifestField(key string, value any, indent int) ([]string, error) {
	body, err := encodeManifestFragment(map[string]any{key: value})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body))
	for _, line := range body {
		out = append(out, strings.Repeat(" ", indent)+line)
	}
	return out, nil
}

func encodeManifestFragment(value any) ([]string, error) {
	var buf strings.Builder
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("render manifest entry: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("render manifest entry: %w", err)
	}
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n"), nil
}

func yamlMaxLine(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	max := node.Line
	for _, child := range node.Content {
		if line := yamlMaxLine(child); line > max {
			max = line
		}
	}
	return max
}

func generateAgentsManifest(existing []byte, candidates manifestCandidates) ([]byte, manifestMergeReport, error) {
	var report manifestMergeReport
	manifest := apmManifest{Name: "omni-migrated", Version: "1.0.0"}
	var reach []string
	for _, dep := range candidates.Packages {
		identity, err := apmPackageIdentity(dep)
		if err != nil {
			report.Rejected = append(report.Rejected, manifestMergeRejection{Kind: manifestKindPackage, Reason: err.Error()})
			continue
		}
		manifest.Dependencies.APM = append(manifest.Dependencies.APM, dep)
		reach = append(reach, dep.Targets...)
		report.Appended = append(report.Appended, manifestMergeEntry{Kind: manifestKindPackage, Identity: identity, Targets: slices.Clone(dep.Targets)})
	}
	for _, candidate := range candidates.MCP {
		if candidate.Dep.Name == "" {
			report.Rejected = append(report.Rejected, manifestMergeRejection{Kind: manifestKindMCP, Reason: "mcp candidate has no name"})
			continue
		}
		manifest.Dependencies.MCP = append(manifest.Dependencies.MCP, candidate.Dep)
		reach = append(reach, candidate.Reach...)
		report.Appended = append(report.Appended, manifestMergeEntry{Kind: manifestKindMCP, Identity: candidate.Dep.Name})
	}
	for _, dep := range candidates.LSP {
		if dep.Name == "" {
			report.Rejected = append(report.Rejected, manifestMergeRejection{Kind: manifestKindLSP, Reason: "lsp candidate has no name"})
			continue
		}
		manifest.Dependencies.LSP = append(manifest.Dependencies.LSP, dep)
		report.Appended = append(report.Appended, manifestMergeEntry{Kind: manifestKindLSP, Identity: dep.Name})
	}
	manifest.Targets = sortedUnique(reach)
	body, err := encodeAPMManifest(manifest)
	if err != nil {
		return nil, manifestMergeReport{}, err
	}
	var out strings.Builder
	out.WriteString(agentsMigrationMarker + "\n")
	out.WriteString(body)

	declared := map[string]string{}
	for _, decl := range parseTemplateMarketplaces(existing) {
		declared[decl.name] = decl.source
		out.WriteString("# " + decl.Render() + "\n")
	}
	for _, candidate := range candidates.Marketplaces {
		if candidate.name == "" || candidate.source == "" {
			report.Rejected = append(report.Rejected, manifestMergeRejection{Kind: manifestKindMarketplace, Identity: candidate.name, Reason: "marketplace candidate needs both a name and a source"})
			continue
		}
		if source, found := declared[candidate.name]; found {
			if source != candidate.source {
				report.Rejected = append(report.Rejected, manifestMergeRejection{Kind: manifestKindMarketplace, Identity: candidate.name, Reason: "declared source differs from the candidate"})
			}
			continue
		}
		declared[candidate.name] = candidate.source
		out.WriteString("# " + candidate.Render() + "\n")
		report.Appended = append(report.Appended, manifestMergeEntry{Kind: manifestKindMarketplace, Identity: candidate.name})
	}
	return []byte(out.String()), report, nil
}
