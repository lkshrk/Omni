package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

type AgentsOwnedChildFix struct {
	Kind   string
	Name   string
	Owner  string
	Fields []string
	Exact  bool
	Reason string
}

type AgentsOwnedChildrenFixReport struct {
	TemplatePath string
	Removed      []AgentsOwnedChildFix
	Kept         []AgentsOwnedChildFix
	Unavailable  []string
	SyncRequired bool
}

type agentsSourceRange struct{ start, end int }

type agentsPathIdentity struct {
	path   string
	info   os.FileInfo
	target string
}

type agentsFileContentIdentity struct {
	path   string
	hash   string
	absent bool
	info   os.FileInfo
}

func (a *App) FixAgentsOwnedChildren(ctx context.Context, dryRun bool) (AgentsOwnedChildrenFixReport, error) {
	return a.fixAgentsOwnedChildren(ctx, dryRun, nil)
}

func (a *App) fixAgentsOwnedChildren(ctx context.Context, dryRun bool, afterEvidence func()) (report AgentsOwnedChildrenFixReport, retErr error) {
	templatePath, err := AgentsTemplatePath()
	report = AgentsOwnedChildrenFixReport{TemplatePath: templatePath}
	if err != nil {
		if absent, liveErr := agentsOwnedChildrenLiveManifestAbsent(); liveErr == nil && absent {
			return report, nil
		}
		return report, err
	}
	if _, _, _, resolveErr := resolveAgentsTemplateTarget(templatePath); resolveErr != nil {
		absent, liveErr := agentsOwnedChildrenLiveManifestAbsent()
		if liveErr == nil && absent {
			return report, nil
		}
		if liveErr != nil {
			return report, errors.Join(resolveErr, liveErr)
		}
		return report, resolveErr
	}
	lock, err := config.AcquireWriteLock(templatePath)
	if err != nil {
		return report, fmt.Errorf("lock agents template: %w", err)
	}
	defer func() { _ = lock.Close() }()
	retErr = apm.WithGlobalWorkspaceLock(ctx, func(context.Context) error {
		var err error
		report, err = a.fixAgentsOwnedChildrenLocked(templatePath, dryRun, afterEvidence)
		return err
	})
	return report, retErr
}

func agentsOwnedChildrenLiveManifestAbsent() (bool, error) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(filepath.Join(dir, "apm.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("global APM manifest is not a regular file")
	}
	return false, nil
}

func (a *App) fixAgentsOwnedChildrenLocked(templatePath string, dryRun bool, afterEvidence func()) (report AgentsOwnedChildrenFixReport, retErr error) {
	report = AgentsOwnedChildrenFixReport{TemplatePath: templatePath}
	target, identities, info, err := resolveAgentsTemplateTarget(templatePath)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return report, fmt.Errorf("read agents template: %w", err)
	}
	manifest, evidence, inputs, err := agentsOwnedTemplateEvidence(target, raw)
	if err != nil {
		return report, err
	}
	if afterEvidence != nil {
		afterEvidence()
	}
	report.Unavailable = append([]string(nil), evidence.Unavailable...)
	if err := checkAgentsOwnedChildOwners(evidence.Children); err != nil {
		return report, err
	}
	collisions := classifyAgentsOwnedChildren(manifest, evidence.Children)
	owners := agentsOwnedChildOwners(evidence.Children)
	exact := make(map[string]agentsChildCollision)
	for _, collision := range collisions {
		entry := agentsOwnedFixEntry(manifest, collision)
		if len(owners[agentsChildKey(collision.Child.Kind, collision.Child.Name)]) > 1 {
			entry.Reason = "multiple packages claim this service"
			report.Kept = append(report.Kept, entry)
			continue
		}
		if !collision.Exact {
			report.Kept = append(report.Kept, entry)
			continue
		}
		exact[agentsChildKey(collision.Child.Kind, collision.Child.Name)] = collision
	}
	if len(evidence.Unavailable) > 0 {
		reason := "ownership evidence unavailable for package(s): " + strings.Join(evidence.Unavailable, ", ")
		for _, collision := range exact {
			entry := agentsOwnedFixEntry(manifest, collision)
			entry.Reason = reason
			report.Kept = append(report.Kept, entry)
		}
		sortAgentsOwnedFixReport(&report)
		return report, fmt.Errorf("cannot verify package-owned MCP/LSP declarations for %s; no declarations were removed", strings.Join(evidence.Unavailable, ", "))
	}
	if len(exact) == 0 {
		sortAgentsOwnedFixReport(&report)
		return report, nil
	}

	ranges, unsupported, err := agentsOwnedChildRemovalRanges(target, raw, exact)
	if err != nil {
		return report, err
	}
	if len(unsupported) > 0 {
		for _, collision := range exact {
			entry := agentsOwnedFixEntry(manifest, collision)
			entry.Reason = unsupported[agentsChildKey(collision.Child.Kind, collision.Child.Name)]
			if entry.Reason == "" {
				entry.Reason = "another exact duplicate has an unsafe YAML layout"
			}
			report.Kept = append(report.Kept, entry)
		}
		sortAgentsOwnedFixReport(&report)
		return report, fmt.Errorf("agents template uses an unsupported YAML layout; no package-owned declarations were removed")
	}
	for _, collision := range exact {
		report.Removed = append(report.Removed, agentsOwnedFixEntry(manifest, collision))
	}
	sortAgentsOwnedFixReport(&report)
	if err := verifyAgentsPackageOwnershipEvidence(raw, evidence.Manifests, inputs); err != nil {
		return AgentsOwnedChildrenFixReport{TemplatePath: templatePath, Kept: report.Removed}, err
	}
	if dryRun {
		return report, nil
	}
	updated := removeAgentsSourceRanges(raw, ranges)
	if err := verifyAgentsTemplateIdentities(identities, target, info); err != nil {
		return AgentsOwnedChildrenFixReport{TemplatePath: templatePath, Kept: report.Removed}, err
	}
	if err := writeAgentsOwnedTemplate(target, updated, info.Mode(), identities, info, evidence.Manifests, inputs); err != nil {
		return AgentsOwnedChildrenFixReport{TemplatePath: templatePath, Kept: report.Removed}, err
	}
	report.SyncRequired = len(report.Removed) > 0
	return report, nil
}

func agentsOwnedTemplateEvidence(templatePath string, raw []byte) (apmManifest, agentsOwnershipEvidence, []agentsFileContentIdentity, error) {
	var manifest apmManifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return manifest, agentsOwnershipEvidence{}, nil, agentsInvalidYAMLError("agents template", templatePath, err)
	}
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return manifest, agentsOwnershipEvidence{}, nil, err
	}
	livePath, lockPath := filepath.Join(dir, "apm.yml"), filepath.Join(dir, "apm.lock.yaml")
	liveIdentity, _, err := readAgentsFileIdentity(livePath)
	if err != nil {
		return manifest, agentsOwnershipEvidence{}, nil, fmt.Errorf("read global APM manifest: %w", err)
	}
	lockIdentity, lockRaw, err := readAgentsFileIdentity(lockPath)
	if err != nil {
		return manifest, agentsOwnershipEvidence{}, nil, fmt.Errorf("read global APM lockfile: %w", err)
	}
	var lock apmLockfile
	if len(lockRaw) > 0 {
		if err := yaml.Unmarshal(lockRaw, &lock); err != nil {
			return manifest, agentsOwnershipEvidence{}, nil, agentsInvalidYAMLError("APM lockfile", lockPath, err)
		}
	}
	inputs := []agentsFileContentIdentity{
		{path: templatePath, hash: manifestHash(raw)}, liveIdentity, lockIdentity,
	}
	return manifest, readAPMModuleManifests(dir, joinAPMPackages(manifest, lock)), inputs, nil
}

func agentsOwnedFixEntry(manifest apmManifest, collision agentsChildCollision) AgentsOwnedChildFix {
	return AgentsOwnedChildFix{
		Kind: strings.ToUpper(string(collision.Child.Kind)), Name: collision.Child.Name,
		Owner: collision.Child.Owner, Fields: agentsOwnedCollisionDiffFields(manifest, collision), Exact: collision.Exact,
	}
}

func sortAgentsOwnedFixReport(report *AgentsOwnedChildrenFixReport) {
	less := func(left, right AgentsOwnedChildFix) bool {
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	}
	sort.Slice(report.Removed, func(i, j int) bool { return less(report.Removed[i], report.Removed[j]) })
	sort.Slice(report.Kept, func(i, j int) bool { return less(report.Kept[i], report.Kept[j]) })
}

func agentsOwnedChildRemovalRanges(path string, raw []byte, exact map[string]agentsChildCollision) ([]agentsSourceRange, map[string]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, agentsInvalidYAMLError("agents template layout", path, err)
	}
	unsupported := make(map[string]string)
	root := yamlDocumentMapping(&doc)
	deps := yamlMappingValue(root, "dependencies")
	if root == nil || deps == nil || deps.Kind != yaml.MappingNode || yamlHasUnsafeOwnershipSyntax(deps) {
		for key := range exact {
			unsupported[key] = "dependencies must be a block mapping without anchors, aliases, or merges"
		}
		return nil, unsupported, nil
	}
	lineOffsets := agentsLineOffsets(raw)
	var ranges []agentsSourceRange
	located := make(map[string]bool)
	for _, kind := range []agentsChildKind{agentsChildMCP, agentsChildLSP} {
		seq := yamlMappingValue(deps, string(kind))
		if seq == nil {
			continue
		}
		kindExact := false
		for key := range exact {
			prefix, _, _ := strings.Cut(key, "\x00")
			kindExact = kindExact || prefix == string(kind)
		}
		if !kindExact {
			continue
		}
		if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 || yamlHasUnsafeOwnershipSyntax(seq) {
			for key := range exact {
				if strings.HasPrefix(key, string(kind)+"\x00") {
					unsupported[key] = "service list must be a block sequence without anchors, aliases, or merges"
				}
			}
			continue
		}
		seqEnd := yamlNextMappingLine(deps, seq.Line)
		if rootEnd := yamlNextMappingLine(root, deps.Line); rootEnd > 0 && (seqEnd == 0 || rootEnd < seqEnd) {
			seqEnd = rootEnd
		}
		if seqEnd == 0 {
			seqEnd = len(lineOffsets)
		}
		seen := make(map[string]bool)
		for i, item := range seq.Content {
			name := yamlScalarValue(item, "name")
			key := agentsChildKey(kind, name)
			if _, ok := exact[key]; !ok {
				continue
			}
			if seen[key] {
				unsupported[key] = "duplicate service names are ambiguous"
				continue
			}
			seen[key] = true
			startLine := item.Line
			endLine := seqEnd
			if i+1 < len(seq.Content) {
				endLine = seq.Content[i+1].Line
				endLine = agentsLeadingCommentLine(raw, lineOffsets, startLine, endLine)
			}
			if !agentsBlockItemRangeSupported(raw, lineOffsets, startLine, endLine, item) {
				unsupported[key] = "service item has comments or an ambiguous source range"
				continue
			}
			ranges = append(ranges, agentsSourceRange{start: lineOffsets[startLine-1], end: lineOffsets[endLine-1]})
			located[key] = true
		}
	}
	for key := range exact {
		if !located[key] && unsupported[key] == "" {
			unsupported[key] = "service item could not be located"
		}
	}
	return ranges, unsupported, nil
}

func agentsLeadingCommentLine(raw []byte, offsets []int, after, before int) int {
	line := before
	for line-1 > after {
		candidate := bytes.TrimSpace(raw[offsets[line-2]:offsets[line-1]])
		if !bytes.HasPrefix(candidate, []byte("#")) {
			break
		}
		line--
	}
	return line
}

func yamlDocumentMapping(doc *yaml.Node) *yaml.Node {
	if doc != nil && doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0]
	}
	return nil
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func yamlScalarValue(node *yaml.Node, key string) string {
	value := yamlMappingValue(node, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

func yamlHasUnsafeOwnershipSyntax(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" || node.Alias != nil || node.Tag == "!!merge" {
		return true
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "<<" {
				return true
			}
		}
	}
	for _, child := range node.Content {
		if yamlHasUnsafeOwnershipSyntax(child) {
			return true
		}
	}
	return false
}

func yamlNextMappingLine(mapping *yaml.Node, after int) int {
	next := 0
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		line := mapping.Content[i].Line
		if line > after && (next == 0 || line < next) {
			next = line
		}
	}
	return next
}

func agentsLineOffsets(raw []byte) []int {
	offsets := []int{0}
	for i, b := range raw {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	offsets = append(offsets, len(raw))
	return offsets
}

func agentsBlockItemRangeSupported(raw []byte, offsets []int, startLine, endLine int, item *yaml.Node) bool {
	if item == nil || item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 || startLine < 1 || endLine <= startLine || endLine > len(offsets) {
		return false
	}
	line := raw[offsets[startLine-1]:offsets[startLine]]
	trimmed := bytes.TrimLeft(line, " ")
	if len(trimmed) == len(line) || !bytes.HasPrefix(trimmed, []byte("-")) || bytes.Contains(line[:len(line)-len(trimmed)], []byte("\t")) {
		return false
	}
	if yamlNodeHasComments(item) {
		return false
	}
	for current := startLine; current < endLine; current++ {
		candidate := bytes.TrimSpace(raw[offsets[current-1]:offsets[current]])
		if bytes.HasPrefix(candidate, []byte("#")) {
			return false
		}
	}
	return true
}

func yamlNodeHasComments(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.HeadComment != "" || node.LineComment != "" || node.FootComment != "" {
		return true
	}
	for _, child := range node.Content {
		if yamlNodeHasComments(child) {
			return true
		}
	}
	return false
}

func removeAgentsSourceRanges(raw []byte, ranges []agentsSourceRange) []byte {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start > ranges[j].start })
	out := append([]byte(nil), raw...)
	for _, r := range ranges {
		out = append(out[:r.start], out[r.end:]...)
	}
	return out
}

func resolveAgentsTemplateTarget(path string) (string, []agentsPathIdentity, os.FileInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, nil, err
	}
	var identities []agentsPathIdentity
	remaining := strings.Split(strings.TrimPrefix(filepath.Clean(abs), string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	for links := 0; len(remaining) > 0; {
		current = filepath.Join(current, remaining[0])
		remaining = remaining[1:]
		info, err := os.Lstat(current)
		if err != nil {
			return "", nil, nil, err
		}
		identity := agentsPathIdentity{path: current, info: info}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return "", nil, nil, fmt.Errorf("agents template symlink chain is too deep")
			}
			target, err := os.Readlink(current)
			if err != nil {
				return "", nil, nil, err
			}
			identity.target = target
			identities = append(identities, identity)
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			target = filepath.Clean(target)
			remaining = append(strings.Split(strings.TrimPrefix(target, string(filepath.Separator)), string(filepath.Separator)), remaining...)
			current = string(filepath.Separator)
			continue
		}
		identities = append(identities, identity)
	}
	info := identities[len(identities)-1].info
	if !info.Mode().IsRegular() {
		return "", nil, nil, fmt.Errorf("agents template target %s is not a regular file", current)
	}
	return current, identities, info, nil
}

func verifyAgentsTemplateIdentities(identities []agentsPathIdentity, target string, targetInfo os.FileInfo) error {
	for _, identity := range identities {
		current, err := os.Lstat(identity.path)
		if err != nil || !os.SameFile(current, identity.info) {
			return fmt.Errorf("agents template changed during repair; retry")
		}
		if identity.info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(identity.path)
			if err != nil || link != identity.target {
				return fmt.Errorf("agents template symlink changed during repair; retry")
			}
		}
	}
	current, err := os.Stat(target)
	if err != nil || !os.SameFile(current, targetInfo) {
		return fmt.Errorf("agents template target changed during repair; retry")
	}
	return nil
}

func writeAgentsOwnedTemplate(path string, data []byte, mode os.FileMode, identities []agentsPathIdentity, targetInfo os.FileInfo, manifests []agentsModuleManifestIdentity, inputs []agentsFileContentIdentity) (retErr error) {
	return writeAgentsOwnedTemplateWith(path, data, mode, identities, targetInfo, manifests, inputs, os.CreateTemp, os.Rename)
}

func writeAgentsOwnedTemplateWith(path string, data []byte, mode os.FileMode, identities []agentsPathIdentity, targetInfo os.FileInfo, manifests []agentsModuleManifestIdentity, inputs []agentsFileContentIdentity, createTemp func(string, string) (*os.File, error), rename func(string, string) error) (retErr error) {
	dir := filepath.Dir(path)
	file, err := createTemp(dir, ".omni-agents-owned-*")
	if err != nil {
		return fmt.Errorf("create agents template temp file: %w", err)
	}
	temp := file.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, file.Close())
		}
		if err := os.Remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	if err := file.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := verifyAgentsTemplateIdentities(identities, path, targetInfo); err != nil {
		return err
	}
	if err := verifyAgentsPackageOwnershipEvidence(data, manifests, inputs); err != nil {
		return err
	}
	if err := verifyAgentsFileIdentities(inputs); err != nil {
		return err
	}
	if err := rename(temp, path); err != nil {
		return fmt.Errorf("replace agents template: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func readAgentsFileIdentity(path string) (agentsFileContentIdentity, []byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return agentsFileContentIdentity{path: path, absent: true}, nil, nil
	}
	if err != nil {
		return agentsFileContentIdentity{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return agentsFileContentIdentity{}, nil, fmt.Errorf("%s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentsFileContentIdentity{}, nil, err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return agentsFileContentIdentity{}, nil, fmt.Errorf("%s changed while reading", path)
	}
	return agentsFileContentIdentity{path: path, hash: manifestHash(raw), info: info}, raw, nil
}

func verifyAgentsFileIdentities(identities []agentsFileContentIdentity) error {
	for _, identity := range identities {
		current, err := os.Lstat(identity.path)
		if identity.absent && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || identity.absent || identity.info != nil && !os.SameFile(identity.info, current) ||
			!current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("agents ownership input changed during repair; retry")
		}
		raw, err := os.ReadFile(identity.path)
		if err != nil || manifestHash(raw) != identity.hash {
			return fmt.Errorf("agents ownership input changed during repair; retry")
		}
	}
	return nil
}

func agentsInvalidYAMLError(label, path string, err error) error {
	location := path
	fields := strings.Fields(err.Error())
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "line" {
			continue
		}
		line, parseErr := strconv.Atoi(strings.Trim(fields[i+1], ":"))
		if parseErr == nil {
			location += fmt.Sprintf(":%d", line)
		}
		break
	}
	return fmt.Errorf("invalid %s: %s", label, location)
}

func verifyAgentsModuleManifestIdentities(manifests []agentsModuleManifestIdentity) error {
	for _, identity := range manifests {
		if identity.Directory {
			current, err := agentsModuleLstat(identity.Path)
			if err != nil || current.Mode()&os.ModeSymlink != 0 || !sameAgentsModuleDirectoryIdentity(identity.Info, current) {
				return fmt.Errorf("package ownership evidence changed; retry")
			}
			if identity.Lock != nil {
				source := apmPackageSource(identity.Lock.RepoURL, identity.Lock.VirtualPath)
				modulesRoot := identity.Path
				for range strings.Split(source, "/") {
					modulesRoot = filepath.Dir(modulesRoot)
				}
				resolved, ok := resolveModulePath(modulesRoot, source)
				if !ok || filepath.Clean(resolved) != filepath.Clean(identity.Path) {
					return fmt.Errorf("package ownership evidence changed; retry")
				}
			}
			continue
		}
		if identity.Absent {
			current, exists, ok := inspectAPMModuleEvidencePath(identity.ModuleRoot, identity.RelativePath)
			if !ok || exists || !current.Absent || len(current.Parents) != len(identity.Parents) {
				return fmt.Errorf("package ownership evidence changed; retry")
			}
			for i := range identity.Parents {
				if current.Parents[i].Path != identity.Parents[i].Path || !sameAgentsModuleDirectoryIdentity(identity.Parents[i].Info, current.Parents[i].Info) {
					return fmt.Errorf("package ownership evidence changed; retry")
				}
			}
			continue
		}
		current, err := agentsModuleLstat(identity.Path)
		if err != nil || identity.Info == nil || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity.Info, current) {
			return fmt.Errorf("package ownership evidence changed: installed package manifest changed during agents repair; retry")
		}
		if identity.ModuleRoot != "" {
			walked, exists, ok := inspectAPMModuleEvidencePath(identity.ModuleRoot, identity.RelativePath)
			if !ok || !exists || walked.Info == nil || !os.SameFile(identity.Info, walked.Info) || len(walked.Parents) != len(identity.Parents) {
				return fmt.Errorf("package ownership evidence changed; retry")
			}
			for i := range identity.Parents {
				if walked.Parents[i].Path != identity.Parents[i].Path || !sameAgentsModuleDirectoryIdentity(identity.Parents[i].Info, walked.Parents[i].Info) {
					return fmt.Errorf("package ownership evidence changed; retry")
				}
			}
		}
		raw, err := os.ReadFile(identity.Path)
		if err != nil || manifestHash(raw) != identity.Hash {
			return fmt.Errorf("package ownership evidence changed: installed package manifest changed during agents repair; retry")
		}
	}
	return nil
}

func verifyAgentsPackageOwnershipEvidence(template []byte, identities []agentsModuleManifestIdentity, inputs []agentsFileContentIdentity) error {
	if err := verifyAgentsModuleManifestIdentities(identities); err != nil {
		return err
	}
	var lockIdentity *agentsFileContentIdentity
	for i := range inputs {
		if filepath.Base(inputs[i].path) == "apm.lock.yaml" {
			lockIdentity = &inputs[i]
			break
		}
	}
	if lockIdentity == nil {
		return fmt.Errorf("package ownership evidence changed; retry")
	}
	current, raw, err := readAgentsFileIdentity(lockIdentity.path)
	if err != nil || current.absent != lockIdentity.absent || current.hash != lockIdentity.hash ||
		lockIdentity.info != nil && (current.info == nil || !os.SameFile(lockIdentity.info, current.info)) {
		return fmt.Errorf("agents ownership input changed: package ownership evidence changed; retry")
	}
	var manifest apmManifest
	var lock apmLockfile
	if yaml.Unmarshal(template, &manifest) != nil || yaml.Unmarshal(raw, &lock) != nil {
		return fmt.Errorf("package ownership evidence changed; retry")
	}
	rows := joinAPMPackages(manifest, lock)
	for _, identity := range identities {
		if identity.Lock == nil {
			continue
		}
		matches := 0
		for i := range rows {
			if sameAgentsPackageLockEvidence(identity.Lock, rows[i].lockEvidence) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("package ownership evidence changed; retry")
		}
	}
	return nil
}

func sameAgentsPackageLockEvidence(left, right *agentsPackageLockEvidence) bool {
	if left == nil || right == nil || left.RepoURL != right.RepoURL || left.Name != right.Name ||
		left.VirtualPath != right.VirtualPath || left.LocalPath != right.LocalPath ||
		left.PackageType != right.PackageType || left.ResolvedCommit != right.ResolvedCommit {
		return false
	}
	leftFiles, rightFiles := append([]string(nil), left.DeployedFiles...), append([]string(nil), right.DeployedFiles...)
	sort.Strings(leftFiles)
	sort.Strings(rightFiles)
	return slices.Equal(leftFiles, rightFiles)
}
