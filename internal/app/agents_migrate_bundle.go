package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

const (
	maxBundleOwners         = 256
	maxBundleEntries        = 4096
	maxBundleDepth          = 32
	maxBundleManifestBytes  = 1 << 20
	maxAllManifestBytes     = 16 << 20
	maxBundleFileBytes      = 64 << 20
	maxBundleRuntimeBytes   = 512 << 20
	maxMigrationRuntimeByte = 1 << 30
	maxMarketplaceCatalogs  = 4096
)

type agentBundlePlan struct {
	Decls      config.LegacyAgentDecls
	Owners     []agentBundleOwner
	Suppressed []string
	Wrappers   []agentBundleWrapper
	Blockers   []string
}

type agentBundleOwner struct {
	Name       string
	Original   string
	Root       string
	Targets    []string
	Dependency apmPackageDep
	Children   map[string]agentBundleChild
	Files      []agentBundleFile
	Wrapper    *agentBundleWrapper
	HookWrap   bool
}

type agentBundleChild struct {
	Kind        string
	ID          string
	Fingerprint string
	SourcePath  string
	MCP         *apmMCPDep
	LSP         *apmLSPDep
}

type agentBundleFile struct {
	Source string
	Dest   string
	Mode   os.FileMode
	Size   int64
	Hash   string
	Data   []byte
}

type agentBundleWrapper struct {
	Hash     string
	Path     string
	Manifest []byte
	Files    []agentBundleFile
}

type selectedBundleOwner struct {
	kind     string
	key      string
	entry    legacyEntry
	raw      json.RawMessage
	original string
	root     string
}

type bundleScanBudget struct {
	manifestBytes int64
	runtimeBytes  int64
}

var placeholderReference = regexp.MustCompile(`\$\{(?:env:)?[A-Za-z_][A-Za-z0-9_]*\}`)
var symbolicSecretReference = regexp.MustCompile(`^(?:Bearer )?\$\{(?:env:)?[A-Za-z_][A-Za-z0-9_]*\}$`)

func planAgentBundles(decls config.LegacyAgentDecls, evidence config.LegacyAgentEvidence, stateDir string) (agentBundlePlan, error) {
	plan := agentBundlePlan{Decls: cloneLegacyAgentDecls(decls)}
	selectedCount := len(decls.Plugins) + len(decls.Packages)
	if selectedCount > maxBundleOwners {
		plan.Blockers = []string{fmt.Sprintf("selected owners exceed limit %d", maxBundleOwners)}
		return plan, errors.New(plan.Blockers[0])
	}
	selected := make([]selectedBundleOwner, 0, len(decls.Plugins)+len(decls.Packages))
	budget := &bundleScanBudget{}
	for _, item := range []struct {
		kind string
		all  map[string]json.RawMessage
	}{{"plugin", decls.Plugins}, {"package", decls.Packages}} {
		for _, key := range slices.Sorted(maps.Keys(item.all)) {
			entry, err := decodeLegacyEntry(item.all[key], item.kind, key)
			if err != nil {
				plan.Blockers = append(plan.Blockers, err.Error())
				continue
			}
			original, root, err := resolveBundleRoot(key, item.all[key], entry, decls, evidence, budget)
			if err != nil {
				plan.Blockers = append(plan.Blockers, err.Error())
				continue
			}
			selected = append(selected, selectedBundleOwner{kind: item.kind, key: key, entry: entry, raw: item.all[key], original: original, root: root})
		}
	}
	identityBlockers := duplicateOwnerBlockers(selected)
	if len(identityBlockers) != 0 {
		sort.Strings(identityBlockers)
		plan.Blockers = identityBlockers
		return plan, errors.New(strings.Join(identityBlockers, "\n"))
	}
	owners := make([]agentBundleOwner, 0, len(selected))
	for _, selectedOwner := range selected {
		owner, blockers := inspectBundleOwner(selectedOwner, budget)
		plan.Blockers = append(plan.Blockers, blockers...)
		owners = append(owners, owner)
	}

	// A selected package rooted at a plugin's skill directory is a legacy child,
	// not a second owner.
	drop := map[int]bool{}
	for i := range selected {
		if selected[i].kind != "package" {
			continue
		}
		for j := range owners {
			if selected[j].kind != "plugin" || !pathWithin(selected[i].root, selected[j].root) || selected[i].root == selected[j].root {
				continue
			}
			for _, child := range owners[j].Children {
				if child.Kind != "skill" || filepath.Clean(child.SourcePath) != filepath.Clean(selected[i].root) {
					continue
				}
				got := fingerprintFiles(owners[i].Files, selected[i].root)
				if got != child.Fingerprint {
					plan.Blockers = append(plan.Blockers, fmt.Sprintf("package %q overrides skill %q owned by %q with different content", selected[i].key, child.ID, selected[j].key))
				} else {
					drop[i] = true
					delete(plan.Decls.Packages, selected[i].key)
					plan.Suppressed = append(plan.Suppressed, fmt.Sprintf("package %s owned by %s", selected[i].key, selected[j].key))
				}
			}
		}
	}

	claims := map[string][]string{}
	for i, owner := range owners {
		if drop[i] {
			continue
		}
		for key := range owner.Children {
			claims[key] = append(claims[key], owner.Name)
		}
	}
	for key, names := range claims {
		sort.Strings(names)
		names = slices.Compact(names)
		if len(names) > 1 {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("child %q is claimed by owners %s", strings.ReplaceAll(key, "\x00", "/"), strings.Join(names, ", ")))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(plan.Decls.MCPServers)) {
		if unsafeMigrationScalar(name) {
			plan.Blockers = append(plan.Blockers, "mcp identifier contains CR/LF/NUL")
			continue
		}
		raw := plan.Decls.MCPServers[name]
		matched := false
		for i := range owners {
			if drop[i] {
				continue
			}
			child, ok := owners[i].Children[childKey("mcp", name)]
			if !ok {
				continue
			}
			matched = true
			dep, referencesOwner, blockers := legacyMCPForOwner(raw, name, owners[i])
			plan.Blockers = append(plan.Blockers, blockers...)
			if !referencesOwner {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("mcp %q collides with owner %q without path evidence", name, owners[i].Name))
				continue
			}
			if fingerprintMCP(dep, owners[i]) != child.Fingerprint {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("mcp %q explicitly overrides owner %q with a different definition", name, owners[i].Name))
				continue
			}
			delete(plan.Decls.MCPServers, name)
			plan.Suppressed = append(plan.Suppressed, fmt.Sprintf("mcp %s owned by %s", name, owners[i].Name))
		}
		if !matched {
			_, _, blockers := legacyMCPForOwner(raw, name, agentBundleOwner{})
			plan.Blockers = append(plan.Blockers, blockers...)
		}
	}

	for i := range owners {
		if drop[i] {
			continue
		}
		owner := owners[i]
		if selected[i].kind == "plugin" {
			delete(plan.Decls.Plugins, selected[i].key)
		} else {
			delete(plan.Decls.Packages, selected[i].key)
		}
		// ponytail: wrappers snapshot source bytes; rerun migrate to refresh a changed bundle.
		wrapper, err := buildBundleWrapper(owner, stateDir)
		if err != nil {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("owner %q: %v", owner.Name, err))
		} else {
			owner.Wrapper = &wrapper
			owner.Dependency = apmPackageDep{Path: wrapper.Path, Targets: owner.Targets}
			plan.Wrappers = append(plan.Wrappers, wrapper)
		}
		plan.Owners = append(plan.Owners, owner)
	}
	sort.Slice(plan.Owners, func(i, j int) bool { return plan.Owners[i].Name < plan.Owners[j].Name })
	sort.Slice(plan.Wrappers, func(i, j int) bool { return plan.Wrappers[i].Hash < plan.Wrappers[j].Hash })
	sort.Strings(plan.Suppressed)
	sort.Strings(plan.Blockers)
	plan.Blockers = slices.Compact(plan.Blockers)
	if len(plan.Blockers) != 0 {
		return plan, errors.New(strings.Join(plan.Blockers, "\n"))
	}
	return plan, nil
}

func duplicateOwnerBlockers(selected []selectedBundleOwner) []string {
	var blockers []string
	seenRoots, seenSources, seenNames := map[string]string{}, map[string]string{}, map[string]string{}
	for _, owner := range selected {
		label := owner.kind + " " + owner.key
		checks := []struct {
			kind string
			key  string
			seen map[string]string
		}{
			{"canonical root", filepath.Clean(owner.root), seenRoots},
			{"source identity", canonicalBundleOriginal(owner.original), seenSources},
			{"normalized package name", sanitizeBundleName(owner.key), seenNames},
		}
		for _, check := range checks {
			if previous, ok := check.seen[check.key]; ok {
				blockers = append(blockers, fmt.Sprintf("owners %q and %q share %s %q", previous, label, check.kind, check.key))
			} else {
				check.seen[check.key] = label
			}
		}
	}
	return blockers
}

func cloneLegacyAgentDecls(in config.LegacyAgentDecls) config.LegacyAgentDecls {
	return config.LegacyAgentDecls{
		MCPServers:   maps.Clone(in.MCPServers),
		Plugins:      maps.Clone(in.Plugins),
		Marketplaces: maps.Clone(in.Marketplaces),
		Packages:     maps.Clone(in.Packages),
	}
}

func resolveBundleRoot(name string, raw json.RawMessage, entry legacyEntry, decls config.LegacyAgentDecls, evidence config.LegacyAgentEvidence, budget *bundleScanBudget) (string, string, error) {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", "", fmt.Errorf("owner %q: decode path evidence: %w", name, err)
	}
	for _, key := range []string{"path", "install_path", "installPath", "source_path", "sourcePath", "installLocation"} {
		if value, _ := fields[key].(string); value != "" {
			return resolveEvidencePath(name, value, evidence)
		}
	}
	if entry.Source != "" {
		if rel, ok := evidence.Paths[canonicalBundleOriginal(entry.Source)]; ok {
			root, err := secureSnapshotPath(evidence.SnapshotDir, rel)
			return canonicalBundleOriginal(entry.Source), root, err
		}
	}
	if entry.Marketplace != "" {
		marketRaw, ok := decls.Marketplaces[entry.Marketplace]
		if !ok {
			return "", "", fmt.Errorf("owner %q: marketplace %q is not selected", name, entry.Marketplace)
		}
		marketEntry, err := decodeLegacyEntry(marketRaw, "marketplace", entry.Marketplace)
		if err != nil {
			return "", "", err
		}
		matches, err := marketplaceEvidenceRoots(name, entry, marketEntry, evidence, budget)
		if err != nil {
			return "", "", fmt.Errorf("owner %q: %w", name, err)
		}
		if len(matches) == 1 {
			return matches[0].Original, matches[0].Root, nil
		}
		if len(matches) > 1 {
			return "", "", fmt.Errorf("owner %q: ambiguous marketplace cache roots", name)
		}
	}
	return "", "", fmt.Errorf("owner %q: missing copied path/cache evidence", name)
}

func resolveEvidencePath(name, original string, evidence config.LegacyAgentEvidence) (string, string, error) {
	original = canonicalBundleOriginal(original)
	rel, ok := evidence.Paths[original]
	if !ok {
		return "", "", fmt.Errorf("owner %q: referenced path %q was not copied into the snapshot", name, original)
	}
	root, err := secureSnapshotPath(evidence.SnapshotDir, rel)
	if err != nil {
		return "", "", fmt.Errorf("owner %q: %w", name, err)
	}
	return original, root, nil
}

func canonicalBundleOriginal(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func secureSnapshotPath(snapshotDir, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid snapshot-relative path %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("snapshot path traversal %q", rel)
	}
	root, err := filepath.Abs(snapshotDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, clean)
	if !pathWithin(path, root) {
		return "", fmt.Errorf("snapshot path escapes root: %q", rel)
	}
	if err := rejectSnapshotSymlinks(root, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect copied root %q: %w", rel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("copied root %q must be a real directory", rel)
	}
	return path, nil
}

type marketplaceEvidenceRoot struct {
	Original string
	Root     string
}

type migrationMarketplaceRegistration struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Ref    string `json:"ref"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
}

type migrationMarketplaceCatalog struct {
	Name    string                   `json:"name"`
	Owner   apmCatalogAuthor         `json:"owner"`
	Plugins []migrationCatalogPlugin `json:"plugins"`
}

type migrationCatalogPlugin struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Ref     string          `json:"ref"`
	Source  json.RawMessage `json:"source"`
}

type migrationCatalogSource struct {
	Type       string `json:"type"`
	Source     string `json:"source"`
	Kind       string `json:"kind"`
	Repo       string `json:"repo"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
	Path       string `json:"path"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
	SHA        string `json:"sha"`
}

func marketplaceEvidenceRoots(name string, entry, marketplace legacyEntry, evidence config.LegacyAgentEvidence, budget *bundleScanBudget) ([]marketplaceEvidenceRoot, error) {
	if evidence.MarketplacesJSON == "" {
		return nil, nil
	}
	path, err := secureSnapshotFile(evidence.SnapshotDir, evidence.MarketplacesJSON)
	if err != nil {
		return nil, err
	}
	raw, err := readMigrationManifest(path, "marketplaces.json", budget)
	if err != nil {
		return nil, err
	}
	var index struct {
		Marketplaces []migrationMarketplaceRegistration `json:"marketplaces"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, fmt.Errorf("parse marketplaces.json: %w", err)
	}
	var registrations []migrationMarketplaceRegistration
	for _, registration := range index.Marketplaces {
		if strings.EqualFold(registration.Name, entry.Marketplace) {
			registrations = append(registrations, registration)
		}
	}
	if len(registrations) != 1 {
		return nil, fmt.Errorf("marketplace %q has %d registrations, want exactly one", entry.Marketplace, len(registrations))
	}
	registration := registrations[0]
	if registration.Owner == "" || registration.Repo == "" {
		return nil, fmt.Errorf("marketplace %q registration has no owner identity", entry.Marketplace)
	}
	registrationSource := registration.Owner + "/" + registration.Repo
	if marketplace.Source != "" && !filepath.IsAbs(marketplace.Source) && !sameSourceIdentity(marketplace.Source, registrationSource) {
		return nil, fmt.Errorf("selected marketplace %q source does not match registration owner/repo", entry.Marketplace)
	}
	registrationRef := registration.Ref
	if registrationRef == "" {
		registrationRef = registration.Branch
	}
	if marketplace.Ref != "" && registrationRef != "" && marketplace.Ref != registrationRef {
		return nil, fmt.Errorf("selected marketplace %q ref does not match registration", entry.Marketplace)
	}
	if entry.Owner != "" && !strings.EqualFold(entry.Owner, registration.Owner) {
		return nil, fmt.Errorf("plugin %q owner does not match marketplace %q", name, entry.Marketplace)
	}

	var catalogMatches []migrationMarketplaceCatalog
	var candidateOriginals []string
	for _, original := range slices.Sorted(maps.Keys(evidence.Paths)) {
		if !strings.Contains(filepath.ToSlash(original), "/cache/marketplace/") || strings.HasSuffix(original, ".meta.json") || filepath.Ext(original) != ".json" {
			continue
		}
		candidateOriginals = append(candidateOriginals, original)
	}
	if len(candidateOriginals) > maxMarketplaceCatalogs {
		return nil, fmt.Errorf("marketplace catalog candidate limit %d exceeded", maxMarketplaceCatalogs)
	}
	for _, original := range candidateOriginals {
		catalogPath, err := secureSnapshotFile(evidence.SnapshotDir, evidence.Paths[original])
		if err != nil {
			return nil, err
		}
		catalogRaw, err := readMigrationManifest(catalogPath, "marketplace catalog "+original, budget)
		if err != nil {
			return nil, err
		}
		var catalog migrationMarketplaceCatalog
		if err := json.Unmarshal(catalogRaw, &catalog); err != nil {
			return nil, fmt.Errorf("parse marketplace catalog %q: %w", original, err)
		}
		if !strings.EqualFold(catalog.Name, registration.Name) {
			continue
		}
		if catalog.Owner.Name == "" || !strings.EqualFold(registration.Owner, catalog.Owner.Name) {
			continue
		}
		catalogMatches = append(catalogMatches, catalog)
	}
	if len(catalogMatches) != 1 {
		return nil, fmt.Errorf("marketplace %q has %d matching cached catalogs, want exactly one", entry.Marketplace, len(catalogMatches))
	}
	var plugins []migrationCatalogPlugin
	for _, plugin := range catalogMatches[0].Plugins {
		if strings.EqualFold(plugin.Name, name) {
			plugins = append(plugins, plugin)
		}
	}
	if len(plugins) != 1 {
		return nil, fmt.Errorf("marketplace %q has %d catalog records for plugin %q", entry.Marketplace, len(plugins), name)
	}
	original, sourceRef, err := catalogSourceRoot(plugins[0], marketplace)
	if err != nil {
		return nil, err
	}
	if entry.Source != "" && !sameSourceIdentity(entry.Source, original) {
		return nil, fmt.Errorf("plugin %q source does not match cached catalog", name)
	}
	wantRef := entry.Ref
	if wantRef != "" && sourceRef != "" && wantRef != sourceRef {
		return nil, fmt.Errorf("plugin %q ref does not match cached catalog", name)
	}
	if !filepath.IsAbs(original) {
		return resolveCopiedCatalogRoots(name, original, registration, evidence, budget)
	}
	canonical := canonicalBundleOriginal(original)
	rel, ok := evidence.Paths[canonical]
	if !ok {
		return nil, fmt.Errorf("plugin %q catalog source root %q was not copied into the snapshot", name, canonical)
	}
	root, err := secureSnapshotPath(evidence.SnapshotDir, rel)
	if err != nil {
		return nil, err
	}
	return []marketplaceEvidenceRoot{{Original: canonical, Root: root}}, nil
}

func resolveCopiedCatalogRoots(name, relative string, registration migrationMarketplaceRegistration, evidence config.LegacyAgentEvidence, budget *bundleScanBudget) ([]marketplaceEvidenceRoot, error) {
	if filepath.IsAbs(relative) || !validBundleRel(filepath.Clean(filepath.FromSlash(relative))) {
		return nil, fmt.Errorf("catalog plugin %q has unsafe relative source", name)
	}
	set := map[string]marketplaceEvidenceRoot{}
	for _, original := range slices.Sorted(maps.Keys(evidence.Paths)) {
		copied := evidence.Paths[original]
		root, ok, err := snapshotEvidenceDirectory(evidence.SnapshotDir, copied)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		proven, err := copiedRootDeclaresMarketplace(root, registration, budget)
		if err != nil {
			return nil, err
		}
		if !proven {
			continue
		}
		candidate := marketplaceEvidenceRoot{Original: filepath.Join(original, filepath.FromSlash(relative)), Root: filepath.Join(root, filepath.FromSlash(relative))}
		if !pathWithin(candidate.Root, root) {
			continue
		}
		info, err := os.Lstat(candidate.Root)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		matched, err := copiedRootDeclaresOwner(candidate.Root, name, budget)
		if err != nil {
			return nil, err
		}
		if matched {
			set[candidate.Root] = candidate
		}
	}
	roots := make([]marketplaceEvidenceRoot, 0, len(set))
	for _, root := range set {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Root < roots[j].Root })
	if len(roots) != 1 {
		return nil, fmt.Errorf("plugin %q has %d copied marketplace/plugin roots, want exactly one", name, len(roots))
	}
	return roots, nil
}

func copiedRootDeclaresMarketplace(root string, registration migrationMarketplaceRegistration, budget *bundleScanBudget) (bool, error) {
	rel := registration.Path
	if rel == "" {
		rel = "marketplace.json"
	}
	if filepath.IsAbs(rel) || !validBundleRel(filepath.Clean(filepath.FromSlash(rel))) {
		return false, fmt.Errorf("marketplace %q has unsafe manifest path", registration.Name)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := rejectSnapshotSymlinks(root, path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect copied marketplace root %q: %w", root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("copied marketplace manifest %q is not regular", path)
	}
	raw, err := readMigrationManifest(path, "copied marketplace manifest", budget)
	if err != nil {
		return false, err
	}
	var catalog migrationMarketplaceCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return false, fmt.Errorf("parse copied marketplace manifest %q: %w", path, err)
	}
	return strings.EqualFold(catalog.Name, registration.Name) && strings.EqualFold(catalog.Owner.Name, registration.Owner), nil
}

func snapshotEvidenceDirectory(snapshotDir, rel string) (string, bool, error) {
	if strings.ContainsRune(rel, 0) || filepath.IsAbs(rel) {
		return "", false, fmt.Errorf("invalid snapshot-relative path %q", rel)
	}
	root, err := filepath.Abs(snapshotDir)
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(root, filepath.Clean(rel))
	if !pathWithin(path, root) {
		return "", false, fmt.Errorf("snapshot path escapes root: %q", rel)
	}
	if err := rejectSnapshotSymlinks(root, path); err != nil {
		return "", false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, info.IsDir(), nil
}

func copiedRootDeclaresOwner(root, name string, budget *bundleScanBudget) (bool, error) {
	for _, rel := range []string{"apm.yml", ".claude-plugin/plugin.json", "plugin.json", ".codex-plugin/plugin.json"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspect copied plugin identity %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("copied plugin identity %q is not a regular file", path)
		}
		raw, err := readMigrationManifest(path, "copied plugin identity "+rel, budget)
		if err != nil {
			return false, err
		}
		var identity struct {
			Name string `json:"name" yaml:"name"`
		}
		if strings.HasSuffix(rel, ".yml") {
			err = yaml.Unmarshal(raw, &identity)
		} else {
			err = json.Unmarshal(raw, &identity)
		}
		if err != nil {
			return false, fmt.Errorf("parse copied plugin identity %q: %w", path, err)
		}
		return strings.EqualFold(identity.Name, name), nil
	}
	return false, nil
}

func readMigrationManifest(path, label string, budget *bundleScanBudget) ([]byte, error) {
	raw, info, err := readRegularBounded(path, maxBundleManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	budget.manifestBytes += info.Size()
	if budget.manifestBytes > maxAllManifestBytes {
		return nil, fmt.Errorf("migration manifest/config byte limit exceeded while reading %s", label)
	}
	return raw, nil
}

func catalogSourceRoot(plugin migrationCatalogPlugin, marketplace legacyEntry) (string, string, error) {
	var relative string
	if json.Unmarshal(plugin.Source, &relative) == nil {
		ref := plugin.Ref
		if ref == "" {
			ref = marketplace.Ref
		}
		if filepath.IsAbs(relative) {
			return relative, ref, nil
		}
		if marketplace.Source == "" || !filepath.IsAbs(marketplace.Source) {
			return filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))), ref, nil
		}
		return filepath.Join(marketplace.Source, filepath.FromSlash(relative)), ref, nil
	}
	var source migrationCatalogSource
	if err := json.Unmarshal(plugin.Source, &source); err != nil {
		return "", "", fmt.Errorf("parse catalog plugin source: %w", err)
	}
	base := source.Repo
	if base == "" {
		base = source.Repository
	}
	if base == "" {
		base = source.URL
	}
	if base == "" && filepath.IsAbs(marketplace.Source) {
		base = marketplace.Source
	}
	if base == "" {
		return "", "", fmt.Errorf("catalog plugin source has no canonical root")
	}
	if source.Path != "" {
		if !filepath.IsAbs(base) {
			return "", "", fmt.Errorf("catalog subpath source has no copied absolute root")
		}
		base = filepath.Join(base, filepath.FromSlash(source.Path))
	}
	ref := source.Ref
	if ref == "" {
		ref = source.Commit
	}
	if ref == "" {
		ref = source.SHA
	}
	if ref == "" {
		ref = plugin.Ref
	}
	if ref == "" {
		ref = marketplace.Ref
	}
	return base, ref, nil
}

func sameSourceIdentity(left, right string) bool {
	if filepath.IsAbs(left) || filepath.IsAbs(right) {
		return canonicalBundleOriginal(left) == canonicalBundleOriginal(right)
	}
	return apmNormalizeRepo(left) == apmNormalizeRepo(right)
}

func secureSnapshotFile(snapshotDir, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid snapshot-relative file %q", rel)
	}
	root, err := filepath.Abs(snapshotDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, filepath.Clean(rel))
	if !pathWithin(path, root) {
		return "", fmt.Errorf("snapshot file escapes root: %q", rel)
	}
	if err := rejectSnapshotSymlinks(root, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("snapshot file %q must be regular", rel)
	}
	return path, nil
}

func rejectSnapshotSymlinks(root, target string) error {
	root, target = filepath.Clean(root), filepath.Clean(target)
	if !pathWithin(target, root) {
		return fmt.Errorf("snapshot path escapes root")
	}
	current := root
	parts := []string{}
	if rel, err := filepath.Rel(root, target); err == nil && rel != "." {
		parts = strings.Split(rel, string(filepath.Separator))
	}
	for _, part := range append([]string{""}, parts...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot path has symlinked ancestor %q", current)
		}
	}
	return nil
}

func inspectBundleOwner(selected selectedBundleOwner, budget *bundleScanBudget) (agentBundleOwner, []string) {
	owner := agentBundleOwner{
		Name:     selected.key,
		Original: selected.original,
		Root:     selected.root,
		Targets:  apmTargets(selected.entry.Agents),
		Children: map[string]agentBundleChild{},
	}
	var blockers []string
	entries := 0
	var runtimeBytes int64
	err := filepath.WalkDir(owner.Root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxBundleEntries {
			return fmt.Errorf("filesystem entry limit %d exceeded", maxBundleEntries)
		}
		rel, err := filepath.Rel(owner.Root, path)
		if err != nil {
			return err
		}
		if rel != "." && len(strings.Split(filepath.ToSlash(rel), "/")) > maxBundleDepth {
			return fmt.Errorf("depth limit %d exceeded at %q", maxBundleDepth, rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink runtime entry %q is not supported", rel)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported device or special file %q", rel)
		}
		if info.Size() > maxBundleFileBytes {
			return fmt.Errorf("runtime file %q exceeds %d bytes", rel, maxBundleFileBytes)
		}
		runtimeBytes += info.Size()
		budget.runtimeBytes += info.Size()
		if runtimeBytes > maxBundleRuntimeBytes || budget.runtimeBytes > maxMigrationRuntimeByte {
			return fmt.Errorf("runtime byte limit exceeded at %q", rel)
		}
		if isBundleConfig(rel) {
			if info.Size() > maxBundleManifestBytes {
				return fmt.Errorf("manifest/config %q exceeds %d bytes", rel, maxBundleManifestBytes)
			}
			budget.manifestBytes += info.Size()
			if budget.manifestBytes > maxAllManifestBytes {
				return fmt.Errorf("migration manifest/config byte limit exceeded")
			}
		}
		hash, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("read runtime file %q: %w", rel, err)
		}
		owner.Files = append(owner.Files, agentBundleFile{Source: path, Dest: filepath.ToSlash(rel), Mode: info.Mode(), Size: info.Size(), Hash: hash})
		return nil
	})
	if err != nil {
		return owner, []string{fmt.Sprintf("owner %q: %v", owner.Name, err)}
	}
	sort.Slice(owner.Files, func(i, j int) bool { return owner.Files[i].Dest < owner.Files[j].Dest })
	for i := range owner.Files {
		file := &owner.Files[i]
		if !strings.HasPrefix(file.Dest, "hooks/") || filepath.Ext(file.Dest) != ".json" {
			continue
		}
		rewritten, needsWrapper, hookBlockers := rewriteHookCommands(owner, file.Source, budget, &runtimeBytes)
		blockers = append(blockers, hookBlockers...)
		owner.HookWrap = owner.HookWrap || needsWrapper
		if rewritten != nil {
			file.Data = rewritten
			file.Size = int64(len(rewritten))
			file.Hash = hashBytes(rewritten)
		}
	}

	for _, dir := range []struct{ kind, name string }{{"skill", "skills"}, {"agent", "agents"}, {"command", "commands"}, {"hook", "hooks"}} {
		base := filepath.Join(owner.Root, dir.name)
		items, _ := os.ReadDir(base)
		for _, item := range items {
			path := filepath.Join(base, item.Name())
			if dir.kind == "skill" {
				if !item.IsDir() {
					continue
				}
				if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
					continue
				}
			} else if item.IsDir() || (dir.kind == "hook" && filepath.Ext(item.Name()) != ".json") {
				continue
			}
			id := strings.TrimSuffix(item.Name(), filepath.Ext(item.Name()))
			child := agentBundleChild{Kind: dir.kind, ID: id, SourcePath: path, Fingerprint: fingerprintFiles(owner.Files, path)}
			blockers = append(blockers, addBundleChild(&owner, child)...)
		}
	}
	if _, err := os.Stat(filepath.Join(owner.Root, "SKILL.md")); err == nil {
		child := agentBundleChild{Kind: "skill", ID: owner.Name, SourcePath: owner.Root, Fingerprint: fingerprintFiles(owner.Files, owner.Root)}
		blockers = append(blockers, addBundleChild(&owner, child)...)
	}
	for _, file := range owner.Files {
		if strings.HasPrefix(file.Dest, "bin/") && file.Mode&0o111 != 0 {
			blockers = append(blockers, addBundleChild(&owner, agentBundleChild{Kind: "binary", ID: strings.TrimPrefix(file.Dest, "bin/"), SourcePath: file.Source, Fingerprint: file.Hash})...)
		}
	}

	for _, rel := range []string{"apm.yml", "apm.yaml", ".mcp.json", "mcp.json", ".codex-plugin/mcp.json", ".lsp.json", "lsp.json", ".codex-plugin/lsp.json"} {
		path := filepath.Join(owner.Root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		mcp, lsp, parseBlockers := readBundleServices(owner, rel, path)
		blockers = append(blockers, parseBlockers...)
		for _, dep := range mcp {
			dep := dep
			blockers = append(blockers, addBundleChild(&owner, agentBundleChild{Kind: "mcp", ID: dep.Name, SourcePath: path, Fingerprint: fingerprintMCP(dep, owner), MCP: &dep})...)
		}
		for _, dep := range lsp {
			dep := dep
			blockers = append(blockers, addBundleChild(&owner, agentBundleChild{Kind: "lsp", ID: dep.Name, SourcePath: path, Fingerprint: fingerprintLSP(dep, owner), LSP: &dep})...)
		}
	}
	for _, rel := range []string{".claude-plugin/plugin.json", "plugin.json", ".codex-plugin/plugin.json"} {
		path := filepath.Join(owner.Root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		mcp, lsp, parseBlockers := readBundleServices(owner, rel, path)
		blockers = append(blockers, parseBlockers...)
		for _, dep := range mcp {
			dep := dep
			blockers = append(blockers, addBundleChild(&owner, agentBundleChild{Kind: "mcp", ID: dep.Name, SourcePath: path, Fingerprint: fingerprintMCP(dep, owner), MCP: &dep})...)
		}
		for _, dep := range lsp {
			dep := dep
			blockers = append(blockers, addBundleChild(&owner, agentBundleChild{Kind: "lsp", ID: dep.Name, SourcePath: path, Fingerprint: fingerprintLSP(dep, owner), LSP: &dep})...)
		}
	}
	sort.Strings(blockers)
	return owner, blockers
}

func isBundleConfig(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return base == "apm.yml" || base == "apm.yaml" || base == "plugin.json" || base == "mcp.json" || base == ".mcp.json" || base == ".lsp.json" || base == "lsp.json" || (base == "hooks.json" && strings.Contains(filepath.ToSlash(rel), "hooks/"))
}

func addBundleChild(owner *agentBundleOwner, child agentBundleChild) []string {
	if child.ID == "" || unsafeMigrationScalar(child.ID) {
		return []string{fmt.Sprintf("owner %q declares unsafe %s identifier", owner.Name, child.Kind)}
	}
	key := childKey(child.Kind, child.ID)
	if previous, ok := owner.Children[key]; ok {
		if previous.Fingerprint == child.Fingerprint {
			return nil
		}
		return []string{fmt.Sprintf("owner %q declares %s %q more than once with different definitions", owner.Name, child.Kind, child.ID)}
	}
	owner.Children[key] = child
	return nil
}

func readBundleServices(owner agentBundleOwner, rel, path string) ([]apmMCPDep, []apmLSPDep, []string) {
	raw, _, err := readRegularBounded(path, maxBundleManifestBytes)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("owner %q: read %s: %v", owner.Name, rel, err)}
	}
	var doc map[string]any
	if strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml") {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, nil, []string{fmt.Sprintf("owner %q: parse %s: %v", owner.Name, rel, err)}
		}
		blockers := validateAPMManifest(owner, doc)
		var allMCP []apmMCPDep
		var allLSP []apmLSPDep
		for _, bucket := range []string{"dependencies", "devDependencies"} {
			dependencies, ok := stringMap(doc[bucket])
			if !ok {
				continue
			}
			mcp, mcpBlockers := serviceList(owner, dependencies["mcp"], "mcp")
			lsp, lspBlockers := lspServiceList(owner, dependencies["lsp"])
			allMCP, allLSP = append(allMCP, mcp...), append(allLSP, lsp...)
			blockers = append(blockers, mcpBlockers...)
			blockers = append(blockers, lspBlockers...)
		}
		return allMCP, allLSP, blockers
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, []string{fmt.Sprintf("owner %q: parse %s: %v", owner.Name, rel, err)}
	}
	var blockers []string
	if unsupported := unsupportedNativeFields(doc); len(unsupported) != 0 {
		blockers = append(blockers, fmt.Sprintf("owner %q: unsupported native fields in %s: %s", owner.Name, rel, strings.Join(unsupported, ", ")))
	}
	for _, field := range []string{"extensions", "dependencies"} {
		if value, exists := doc[field]; exists && nonEmptyNativeValue(value) {
			blockers = append(blockers, fmt.Sprintf("owner %q: non-empty native field %q cannot be represented losslessly", owner.Name, field))
		}
	}
	blockers = append(blockers, validateNativeComponentPaths(owner, doc)...)
	mcp := serviceMap(owner, doc["mcpServers"], "mcp", &blockers)
	lsp := lspServiceMap(owner, doc["lspServers"], &blockers)
	if len(lsp) == 0 && strings.Contains(strings.ToLower(filepath.Base(rel)), "lsp") && doc["lspServers"] == nil {
		lsp = lspServiceMap(owner, doc, &blockers)
	}
	return mcp, lsp, blockers
}

func nonEmptyNativeValue(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case []any:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	case string:
		return value != ""
	default:
		return true
	}
}

func validateAPMManifest(owner agentBundleOwner, doc map[string]any) []string {
	var blockers []string
	allowed := []string{"$schema", "manifestVersion", "name", "version", "description", "author", "license", "source", "repository", "homepage", "keywords", "targets", "target", "type", "includes", "registries", "allowExecutables", "dependencies", "devDependencies", "scripts"}
	for key := range doc {
		if !slices.Contains(allowed, key) {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml has unsupported field %q", owner.Name, key))
		}
	}
	for _, required := range []string{"name", "version"} {
		if value, ok := doc[required].(string); !ok || strings.TrimSpace(value) == "" {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml requires string field %q", owner.Name, required))
		}
	}
	if _, hasTarget := doc["target"]; hasTarget {
		if _, hasTargets := doc["targets"]; hasTargets {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml cannot declare both target and targets", owner.Name))
		}
	}
	for _, field := range []string{"target", "targets"} {
		value, exists := doc[field]
		if !exists {
			continue
		}
		switch value := value.(type) {
		case string:
			if strings.TrimSpace(value) == "" {
				blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml %s is empty", owner.Name, field))
			}
		case []any:
			if len(value) == 0 {
				blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml %s is empty", owner.Name, field))
			}
			for _, item := range value {
				if text, ok := item.(string); !ok || strings.TrimSpace(text) == "" {
					blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml %s must contain strings", owner.Name, field))
				}
			}
		default:
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml %s has unsupported type", owner.Name, field))
		}
	}
	if value, exists := doc["type"]; exists {
		text, ok := value.(string)
		if !ok || !slices.Contains([]string{"instructions", "skill", "hybrid", "prompts"}, strings.ToLower(text)) {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml has invalid type", owner.Name))
		}
	}
	if _, exists := doc["registries"]; exists {
		blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml registries cannot be validated offline losslessly", owner.Name))
	}
	if value, exists := doc["allowExecutables"]; exists {
		if _, ok := stringMap(value); !ok {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml allowExecutables must be a mapping", owner.Name))
		}
	}
	for _, bucket := range []string{"dependencies", "devDependencies"} {
		value, exists := doc[bucket]
		if !exists {
			continue
		}
		dependencies, ok := stringMap(value)
		if !ok {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml %s must be a mapping", owner.Name, bucket))
			continue
		}
		for kind := range dependencies {
			if !slices.Contains([]string{"apm", "mcp", "lsp"}, kind) {
				blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml %s has unsupported dependency kind %q", owner.Name, bucket, kind))
			}
		}
		blockers = append(blockers, validateAPMDependencyPaths(owner, dependencies["apm"])...)
	}
	if includes, exists := doc["includes"]; exists {
		switch includes := includes.(type) {
		case string:
			if includes != "auto" {
				blockers = append(blockers, validateDeclaredPath(owner, "includes", includes)...)
			}
		case []any:
			for _, item := range includes {
				path, ok := item.(string)
				if !ok {
					blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml includes must contain strings", owner.Name))
					continue
				}
				blockers = append(blockers, validateDeclaredPath(owner, "includes", path)...)
			}
		default:
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml includes has unsupported type", owner.Name))
		}
	}
	if scripts, exists := doc["scripts"]; exists {
		values, ok := stringMap(scripts)
		if !ok {
			blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml scripts must be a mapping", owner.Name))
		} else {
			for name, raw := range values {
				command, ok := raw.(string)
				if !ok {
					blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml script %q must be a string", owner.Name, name))
					continue
				}
				_, _, err := rewriteHookCommand(owner, command)
				if err != nil {
					blockers = append(blockers, fmt.Sprintf("owner %q: apm.yml script %q is invalid", owner.Name, name))
				}
			}
		}
	}
	return blockers
}

func validateAPMDependencyPaths(owner agentBundleOwner, value any) []string {
	items, ok := value.([]any)
	if value == nil {
		return nil
	}
	if !ok {
		return []string{fmt.Sprintf("owner %q: apm dependencies must be a list", owner.Name)}
	}
	var blockers []string
	for _, item := range items {
		switch item := item.(type) {
		case string:
			if filepath.IsAbs(item) || strings.HasPrefix(item, ".") {
				blockers = append(blockers, validateDeclaredPath(owner, "apm dependency", item)...)
			}
		default:
			fields, ok := stringMap(item)
			if !ok {
				blockers = append(blockers, fmt.Sprintf("owner %q: apm dependency has unsupported shape", owner.Name))
				continue
			}
			for _, key := range []string{"path", "local_path", "localPath"} {
				if path, _ := fields[key].(string); path != "" {
					blockers = append(blockers, validateDeclaredPath(owner, "apm dependency "+key, path)...)
				}
			}
			if git, _ := fields["git"].(string); filepath.IsAbs(git) {
				blockers = append(blockers, validateDeclaredPath(owner, "apm dependency git", git)...)
			}
		}
	}
	return blockers
}

func validateDeclaredPath(owner agentBundleOwner, field, declared string) []string {
	if invalidPathPlaceholder(declared) {
		return []string{fmt.Sprintf("owner %q: %s uses unsupported environment path placeholder", owner.Name, field)}
	}
	if strings.ContainsAny(declared, "*?[") {
		return []string{fmt.Sprintf("owner %q: %s glob cannot be validated losslessly", owner.Name, field)}
	}
	if filepath.IsAbs(declared) && !pathWithin(declared, owner.Root) && !pathWithin(declared, owner.Original) {
		return []string{fmt.Sprintf("owner %q: %s escapes the package root", owner.Name, field)}
	}
	rel, isPath := bundleRuntimePath(declared, owner, true)
	if !isPath || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{fmt.Sprintf("owner %q: %s has path traversal", owner.Name, field)}
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(owner.Root, rel))
	if err != nil || !pathWithin(resolved, owner.Root) {
		return []string{fmt.Sprintf("owner %q: %s references missing or escaping path %q", owner.Name, field, declared)}
	}
	return nil
}

func validateNativeComponentPaths(owner agentBundleOwner, doc map[string]any) []string {
	var blockers []string
	for _, field := range []string{"commands", "agents", "skills", "hooks"} {
		value, exists := doc[field]
		if !exists {
			continue
		}
		paths, ok := nativePathStrings(value)
		if !ok {
			blockers = append(blockers, fmt.Sprintf("owner %q: native field %q cannot be represented losslessly", owner.Name, field))
			continue
		}
		for _, declared := range paths {
			if invalidPathPlaceholder(declared) {
				blockers = append(blockers, fmt.Sprintf("owner %q: native field %q uses unsupported environment path placeholder", owner.Name, field))
				continue
			}
			if strings.ContainsRune(declared, 0) || filepath.IsAbs(declared) {
				blockers = append(blockers, fmt.Sprintf("owner %q: native field %q has invalid absolute path", owner.Name, field))
				continue
			}
			rel, isPath := bundleRuntimePath(declared, owner, true)
			if !isPath || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				blockers = append(blockers, fmt.Sprintf("owner %q: native field %q has path traversal", owner.Name, field))
				continue
			}
			if !strings.HasPrefix(filepath.ToSlash(rel), field) {
				blockers = append(blockers, fmt.Sprintf("owner %q: native field %q uses unsupported non-conventional path", owner.Name, field))
				continue
			}
			resolved, err := filepath.EvalSymlinks(filepath.Join(owner.Root, rel))
			if err != nil || !pathWithin(resolved, owner.Root) {
				blockers = append(blockers, fmt.Sprintf("owner %q: native field %q references missing or escaping path", owner.Name, field))
			}
		}
	}
	return blockers
}

func nativePathStrings(value any) ([]string, bool) {
	switch value := value.(type) {
	case string:
		return []string{value}, true
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func unsupportedNativeFields(doc map[string]any) []string {
	var out []string
	for key := range doc {
		lower := strings.ToLower(key)
		if slices.Contains([]string{"$schema", "name", "version", "description", "author", "license", "repository", "homepage", "tags", "keywords", "extensions", "dependencies", "commands", "agents", "skills", "hooks", "mcpservers", "lspservers"}, lower) {
			continue
		}
		if strings.Contains(lower, "native") || strings.Contains(lower, "permission") || strings.Contains(lower, "capabilit") || strings.Contains(lower, "executable") || strings.Contains(lower, "runtime") {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func stringMap(value any) (map[string]any, bool) {
	switch value := value.(type) {
	case map[string]any:
		return value, true
	case map[any]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			out[fmt.Sprint(key)] = child
		}
		return out, true
	default:
		return nil, false
	}
}

func serviceList(owner agentBundleOwner, value any, kind string) ([]apmMCPDep, []string) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("owner %q: %s dependencies must be a list", owner.Name, kind)}
	}
	var out []apmMCPDep
	var blockers []string
	for _, item := range items {
		fields, ok := stringMap(item)
		if !ok {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s dependency must be an object", owner.Name, kind))
			continue
		}
		name, ok := fields["name"].(string)
		if !ok || name == "" {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s dependency name must be a string", owner.Name, kind))
			continue
		}
		dep, depBlockers, ok := parseMCPFields(fields, name, owner)
		blockers = append(blockers, depBlockers...)
		if ok {
			out = append(out, dep)
		}
	}
	return out, blockers
}

func serviceMap(owner agentBundleOwner, value any, kind string, blockers *[]string) []apmMCPDep {
	if value == nil {
		return nil
	}
	items, ok := stringMap(value)
	if !ok {
		*blockers = append(*blockers, fmt.Sprintf("owner %q: %s server map must be an object", owner.Name, kind))
		return nil
	}
	var out []apmMCPDep
	for _, name := range slices.Sorted(maps.Keys(items)) {
		fields, ok := stringMap(items[name])
		if !ok {
			*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q must be an object", owner.Name, kind, name))
			continue
		}
		dep, depBlockers, ok := parseMCPFields(fields, name, owner)
		*blockers = append(*blockers, depBlockers...)
		if ok {
			out = append(out, dep)
		}
	}
	return out
}

func parseMCPFields(fields map[string]any, name string, owner agentBundleOwner) (apmMCPDep, []string, bool) {
	dep := apmMCPDep{Name: name, Registry: false}
	var blockers []string
	dep.Transport = strictOptionalString(fields, "transport", owner.Name, "mcp", name, &blockers)
	if dep.Transport == "" {
		dep.Transport = strictOptionalString(fields, "type", owner.Name, "mcp", name, &blockers)
	}
	dep.URL = strictOptionalString(fields, "url", owner.Name, "mcp", name, &blockers)
	dep.Version = strictOptionalString(fields, "version", owner.Name, "mcp", name, &blockers)
	dep.Package = strictOptionalString(fields, "package", owner.Name, "mcp", name, &blockers)
	dep.Command = strictOptionalString(fields, "command", owner.Name, "mcp", name, &blockers)
	dep.Cwd = strictOptionalString(fields, "cwd", owner.Name, "mcp", name, &blockers)
	dep.Args = strictStringSlice(fields, "args", owner.Name, "mcp", name, &blockers)
	dep.Headers = strictStringMap(fields, "headers", owner.Name, "mcp", name, &blockers)
	dep.Env = strictStringMap(fields, "env", owner.Name, "mcp", name, &blockers)
	dep.Tools = strictStringSlice(fields, "tools", owner.Name, "mcp", name, &blockers)
	if registry, exists := fields["registry"]; exists {
		value, ok := registry.(bool)
		if !ok || value {
			blockers = append(blockers, fmt.Sprintf("owner %q: mcp %q registry must be false", owner.Name, name))
		}
	}
	if dep.Transport == "" {
		if dep.Command != "" {
			dep.Transport = "stdio"
		} else if dep.URL != "" {
			dep.Transport = "http"
		}
	}
	for key := range fields {
		if !slices.Contains([]string{"name", "registry", "transport", "type", "version", "package", "url", "command", "args", "cwd", "headers", "env", "tools"}, key) {
			blockers = append(blockers, fmt.Sprintf("owner %q: mcp %q has unsupported field %q", owner.Name, name, key))
		}
	}
	blockers = append(blockers, secretBlockers(owner.Name, "mcp", name, "header", dep.Headers)...)
	blockers = append(blockers, secretBlockers(owner.Name, "mcp", name, "environment", dep.Env)...)
	blockers = append(blockers, validateBundleMCPRuntime(owner, dep)...)
	if name == "" || (dep.Command == "" && dep.URL == "") {
		blockers = append(blockers, fmt.Sprintf("owner %q: incomplete mcp %q", owner.Name, name))
		return dep, blockers, false
	}
	return dep, blockers, true
}

func validateBundleMCPRuntime(owner agentBundleOwner, dep apmMCPDep) []string {
	return validateBundleRuntime(owner, "mcp", dep.Name, dep.Command, dep.Cwd, dep.Args)
}

func validateBundleRuntime(owner agentBundleOwner, kind, name, command, cwd string, args []string) []string {
	var blockers []string
	cwdRel := "."
	if cwd != "" {
		var ok bool
		cwdRel, ok = bundleRuntimePathAt(cwd, owner, ".", true)
		if !ok || !validBundleRel(cwdRel) {
			return []string{fmt.Sprintf("owner %q: %s %q has invalid cwd", owner.Name, kind, name)}
		}
		if resolved, err := filepath.EvalSymlinks(filepath.Join(owner.Root, cwdRel)); err != nil || !pathWithin(resolved, owner.Root) {
			return []string{fmt.Sprintf("owner %q: %s %q references missing or escaping runtime cwd", owner.Name, kind, name)}
		}
	}
	values := []struct {
		value string
		force bool
	}{{command, false}}
	for _, arg := range args {
		values = append(values, struct {
			value string
			force bool
		}{arg, false})
	}
	for _, candidate := range values {
		value := candidate.value
		if invalidPathPlaceholder(value) {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s %q uses unsupported environment path placeholder", owner.Name, kind, name))
			continue
		}
		_, pathValue, optionPath := splitPathOption(value)
		if optionPath {
			value = pathValue
		}
		if strings.ContainsRune(value, 0) {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s %q contains a NUL path", owner.Name, kind, name))
			continue
		}
		if filepath.IsAbs(value) && !pathWithin(value, owner.Original) && !pathWithin(value, owner.Root) {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s %q has absolute child path", owner.Name, kind, name))
			continue
		}
		rel, isPath := bundleRuntimePathAt(value, owner, cwdRel, candidate.force)
		if !isPath || rel == "." {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s %q has path traversal", owner.Name, kind, name))
			continue
		}
		path := filepath.Join(owner.Root, rel)
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !pathWithin(resolved, owner.Root) {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s %q references missing or escaping runtime path", owner.Name, kind, name))
		}
	}
	sort.Strings(blockers)
	return blockers
}

func bundleRuntimePath(value string, owner agentBundleOwner, force bool) (string, bool) {
	return bundleRuntimePathAt(value, owner, ".", force)
}

func bundleRuntimePathAt(value string, owner agentBundleOwner, baseRel string, force bool) (string, bool) {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, "://") || placeholderReference.MatchString(value) && !strings.Contains(value, "PLUGIN_ROOT}") {
		return "", false
	}
	normalized := normalizeOwnerString(value, owner)
	if normalized == "<root>" {
		return ".", true
	}
	if strings.HasPrefix(normalized, "<root>/") {
		return filepath.FromSlash(strings.TrimPrefix(normalized, "<root>/")), true
	}
	if strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, "../") {
		return filepath.Clean(filepath.Join(baseRel, filepath.FromSlash(normalized))), true
	}
	if !filepath.IsAbs(normalized) && (force || strings.ContainsAny(normalized, `/\`) || filepath.Ext(normalized) != "") {
		return filepath.Clean(filepath.Join(baseRel, filepath.FromSlash(normalized))), true
	}
	if !filepath.IsAbs(normalized) {
		candidate := filepath.Clean(filepath.Join(baseRel, filepath.FromSlash(normalized)))
		if _, err := os.Stat(filepath.Join(owner.Root, candidate)); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func validBundleRel(rel string) bool {
	return rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func invalidPathPlaceholder(value string) bool {
	for _, match := range placeholderReference.FindAllString(value, -1) {
		if match != "${CLAUDE_PLUGIN_ROOT}" && match != "${CODEX_PLUGIN_ROOT}" && match != "${PLUGIN_ROOT}" {
			return true
		}
	}
	return false
}

func lspServiceList(owner agentBundleOwner, value any) ([]apmLSPDep, []string) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("owner %q: lsp dependencies must be a list", owner.Name)}
	}
	var out []apmLSPDep
	var blockers []string
	for _, item := range items {
		fields, ok := stringMap(item)
		if !ok {
			blockers = append(blockers, fmt.Sprintf("owner %q: lsp dependency must be an object", owner.Name))
			continue
		}
		name, ok := fields["name"].(string)
		if !ok || name == "" {
			blockers = append(blockers, fmt.Sprintf("owner %q: lsp dependency name must be a string", owner.Name))
			continue
		}
		dep, depBlockers, ok := parseLSPFields(fields, name, owner)
		blockers = append(blockers, depBlockers...)
		if ok {
			out = append(out, dep)
		}
	}
	return out, blockers
}

func lspServiceMap(owner agentBundleOwner, value any, blockers *[]string) []apmLSPDep {
	if value == nil {
		return nil
	}
	items, ok := stringMap(value)
	if !ok {
		*blockers = append(*blockers, fmt.Sprintf("owner %q: lsp server map must be an object", owner.Name))
		return nil
	}
	var out []apmLSPDep
	for _, name := range slices.Sorted(maps.Keys(items)) {
		if strings.HasPrefix(name, "$") {
			continue
		}
		fields, ok := stringMap(items[name])
		if !ok {
			*blockers = append(*blockers, fmt.Sprintf("owner %q: lsp %q must be an object", owner.Name, name))
			continue
		}
		dep, depBlockers, valid := parseLSPFields(fields, name, owner)
		*blockers = append(*blockers, depBlockers...)
		if valid {
			out = append(out, dep)
		}
	}
	return out
}

func parseLSPFields(fields map[string]any, name string, owner agentBundleOwner) (apmLSPDep, []string, bool) {
	var blockers []string
	dep := apmLSPDep{
		Name:                name,
		Transport:           strictOptionalString(fields, "transport", owner.Name, "lsp", name, &blockers),
		Command:             strictOptionalString(fields, "command", owner.Name, "lsp", name, &blockers),
		Args:                strictStringSlice(fields, "args", owner.Name, "lsp", name, &blockers),
		Env:                 strictStringMap(fields, "env", owner.Name, "lsp", name, &blockers),
		Cwd:                 strictOptionalString(fields, "cwd", owner.Name, "lsp", name, &blockers),
		ExtensionToLanguage: strictStringMap(fields, "extensionToLanguage", owner.Name, "lsp", name, &blockers),
		Initialization:      fields["initializationOptions"],
		WorkspaceFolder:     strictOptionalString(fields, "workspaceFolder", owner.Name, "lsp", name, &blockers),
		StartupTimeout:      strictOptionalInt(fields, "startupTimeout", owner.Name, "lsp", name, &blockers),
		ShutdownTimeout:     strictOptionalInt(fields, "shutdownTimeout", owner.Name, "lsp", name, &blockers),
		RestartOnCrash:      strictOptionalBool(fields, "restartOnCrash", owner.Name, "lsp", name, &blockers),
		MaxRestarts:         strictOptionalInt(fields, "maxRestarts", owner.Name, "lsp", name, &blockers),
	}
	allowed := []string{"name", "transport", "command", "args", "env", "cwd", "extensionToLanguage", "initializationOptions", "workspaceFolder", "startupTimeout", "shutdownTimeout", "restartOnCrash", "maxRestarts"}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			blockers = append(blockers, fmt.Sprintf("owner %q: lsp %q has unsupported field %q", owner.Name, name, key))
		}
	}
	blockers = append(blockers, secretBlockers(owner.Name, "lsp", name, "environment", dep.Env)...)
	blockers = append(blockers, validateBundleLSPRuntime(owner, dep)...)
	if name == "" || dep.Command == "" {
		blockers = append(blockers, fmt.Sprintf("owner %q: incomplete lsp %q", owner.Name, name))
		return dep, blockers, false
	}
	return dep, blockers, true
}

func validateBundleLSPRuntime(owner agentBundleOwner, dep apmLSPDep) []string {
	return validateBundleRuntime(owner, "lsp", dep.Name, dep.Command, dep.Cwd, dep.Args)
}

func strictOptionalString(fields map[string]any, key, owner, kind, name string, blockers *[]string) string {
	value, exists := fields[key]
	if !exists || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must be a string", owner, kind, name, key))
		return ""
	}
	return text
}

func strictStringSlice(fields map[string]any, key, owner, kind, name string, blockers *[]string) []string {
	value, exists := fields[key]
	if !exists || value == nil {
		return nil
	}
	if direct, ok := value.([]string); ok {
		return slices.Clone(direct)
	}
	items, ok := value.([]any)
	if !ok {
		*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must be a string list", owner, kind, name, key))
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must contain only strings", owner, kind, name, key))
			return nil
		}
		out = append(out, text)
	}
	return out
}

func strictStringMap(fields map[string]any, key, owner, kind, name string, blockers *[]string) map[string]string {
	value, exists := fields[key]
	if !exists || value == nil {
		return nil
	}
	if direct, ok := value.(map[string]string); ok {
		return maps.Clone(direct)
	}
	items, ok := value.(map[string]any)
	if !ok {
		*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must be a string map", owner, kind, name, key))
		return nil
	}
	out := make(map[string]string, len(items))
	for mapKey, item := range items {
		text, ok := item.(string)
		if !ok || unsafeMigrationScalar(mapKey) {
			*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must contain safe string keys and values", owner, kind, name, key))
			return nil
		}
		out[mapKey] = text
	}
	return out
}

func strictOptionalInt(fields map[string]any, key, owner, kind, name string, blockers *[]string) int {
	value, exists := fields[key]
	if !exists || value == nil {
		return 0
	}
	switch value := value.(type) {
	case int:
		return value
	case float64:
		if float64(int(value)) == value {
			return int(value)
		}
	}
	*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must be an integer", owner, kind, name, key))
	return 0
}

func strictOptionalBool(fields map[string]any, key, owner, kind, name string, blockers *[]string) bool {
	value, exists := fields[key]
	if !exists || value == nil {
		return false
	}
	result, ok := value.(bool)
	if !ok {
		*blockers = append(*blockers, fmt.Sprintf("owner %q: %s %q field %q must be a boolean", owner, kind, name, key))
		return false
	}
	return result
}

func secretBlockers(owner, kind, name, fieldKind string, values map[string]string) []string {
	var blockers []string
	for key, value := range values {
		if sensitiveField(key) && !symbolicSecretReference.MatchString(value) {
			blockers = append(blockers, fmt.Sprintf("owner %q: %s %q has literal sensitive %s field %q", owner, kind, name, fieldKind, key))
		}
	}
	sort.Strings(blockers)
	return blockers
}

func sensitiveField(name string) bool {
	name = strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(name))
	for _, word := range []string{"authorization", "cookie", "token", "secret", "password", "apikey"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func legacyMCPForOwner(raw json.RawMessage, name string, owner agentBundleOwner) (apmMCPDep, bool, []string) {
	var entry legacyEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return apmMCPDep{}, false, []string{fmt.Sprintf("decode mcp server %q: %v", name, err)}
	}
	dep := apmMCPDep{Name: entry.Name, Registry: false, Transport: entry.Transport, URL: entry.URL, Headers: maps.Clone(entry.Headers), Cwd: entry.Cwd}
	fields := strings.Fields(entry.Command)
	if len(fields) != 0 {
		dep.Command, dep.Args = fields[0], slices.Clone(fields[1:])
	}
	dep.Env = map[string]string{}
	for _, env := range entry.Env {
		dep.Env[env] = "${" + env + "}"
	}
	for key, value := range entry.EnvLiteral {
		dep.Env[key] = value
	}
	if len(dep.Env) == 0 {
		dep.Env = nil
	}
	blockers := append(secretBlockers(owner.Name, "mcp", name, "header", dep.Headers), secretBlockers(owner.Name, "mcp", name, "environment", dep.Env)...)
	references := owner.Root != "" && mcpReferencesOwner(dep, owner)
	return dep, references, blockers
}

func mcpReferencesOwner(dep apmMCPDep, owner agentBundleOwner) bool {
	values := []string{dep.Command, dep.Cwd, dep.URL}
	values = append(values, dep.Args...)
	for _, value := range dep.Env {
		values = append(values, value)
	}
	for _, value := range dep.Headers {
		values = append(values, value)
	}
	for _, value := range values {
		if ownerValueReferencesRoot(value, owner) {
			return true
		}
	}
	return false
}

func ownerValueReferencesRoot(value string, owner agentBundleOwner) bool {
	if strings.Contains(value, "${CLAUDE_PLUGIN_ROOT}") || strings.Contains(value, "${CODEX_PLUGIN_ROOT}") || strings.Contains(value, "${PLUGIN_ROOT}") {
		return true
	}
	if _, path, ok := splitPathOption(value); ok {
		value = path
	}
	return filepath.IsAbs(value) && (pathWithin(value, owner.Original) || pathWithin(value, owner.Root))
}

func splitPathOption(value string) (string, string, bool) {
	index := strings.IndexByte(value, '=')
	if index <= 0 || index == len(value)-1 {
		return "", "", false
	}
	return value[:index+1], value[index+1:], true
}

func fingerprintFiles(files []agentBundleFile, root string) string {
	root = filepath.Clean(root)
	h := sha256.New()
	for _, file := range files {
		if !pathWithin(file.Source, root) {
			continue
		}
		rel, _ := filepath.Rel(root, file.Source)
		_, _ = io.WriteString(h, filepath.ToSlash(rel)+"\x00"+file.Hash+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(path string) (string, error) {
	file, info, err := openRegularBounded(path, maxBundleFileBytes)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, maxBundleFileBytes+1))
	if err != nil {
		return "", err
	}
	if n != info.Size() {
		return "", fmt.Errorf("file changed while reading")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func openRegularBounded(path string, max int64) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("non-symlink regular file required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Size() > max {
		_ = file.Close()
		return nil, nil, fmt.Errorf("file changed, is not regular, or exceeds %d bytes", max)
	}
	return file, info, nil
}

func readRegularBounded(path string, max int64) ([]byte, os.FileInfo, error) {
	file, info, err := openRegularBounded(path, max)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, nil, fmt.Errorf("file changed while reading")
	}
	return raw, info, nil
}

func rewriteHookCommands(owner agentBundleOwner, path string, budget *bundleScanBudget, ownerRuntimeBytes *int64) ([]byte, bool, []string) {
	raw, _, err := readRegularBounded(path, maxBundleManifestBytes)
	if err != nil {
		return nil, false, []string{fmt.Sprintf("owner %q: read hook config: %v", owner.Name, err)}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, []string{fmt.Sprintf("owner %q: parse hook config: %v", owner.Name, err)}
	}
	changed, needsWrapper, blockers := rewriteHookValue(owner, value)
	if !changed {
		return nil, needsWrapper, blockers
	}
	rewritten, err := json.Marshal(value)
	if err != nil {
		return nil, needsWrapper, append(blockers, fmt.Sprintf("owner %q: render hook config: %v", owner.Name, err))
	}
	rewritten = append(rewritten, '\n')
	if len(rewritten) > maxBundleManifestBytes {
		return nil, needsWrapper, append(blockers, fmt.Sprintf("owner %q: rewritten hook config exceeds %d bytes", owner.Name, maxBundleManifestBytes))
	}
	if delta := int64(len(rewritten) - len(raw)); delta > 0 {
		nextManifest := budget.manifestBytes + delta
		nextOwnerRuntime := *ownerRuntimeBytes + delta
		nextMigrationRuntime := budget.runtimeBytes + delta
		if nextManifest > maxAllManifestBytes {
			return nil, needsWrapper, append(blockers, fmt.Sprintf("owner %q: rewritten hook config exceeds migration manifest budget", owner.Name))
		}
		if nextOwnerRuntime > maxBundleRuntimeBytes {
			return nil, needsWrapper, append(blockers, fmt.Sprintf("owner %q: rewritten hook config exceeds owner runtime byte limit", owner.Name))
		}
		if nextMigrationRuntime > maxMigrationRuntimeByte {
			return nil, needsWrapper, append(blockers, fmt.Sprintf("owner %q: rewritten hook config exceeds migration runtime byte limit", owner.Name))
		}
		budget.manifestBytes = nextManifest
		*ownerRuntimeBytes = nextOwnerRuntime
		budget.runtimeBytes = nextMigrationRuntime
	}
	return rewritten, needsWrapper, blockers
}

func rewriteHookValue(owner agentBundleOwner, value any) (bool, bool, []string) {
	changed, needsWrapper := false, false
	var blockers []string
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.EqualFold(key, "command") {
				command, ok := child.(string)
				if !ok {
					blockers = append(blockers, fmt.Sprintf("owner %q: hook command must be a string", owner.Name))
					continue
				}
				rewritten, wraps, err := rewriteHookCommand(owner, command)
				needsWrapper = needsWrapper || wraps
				if err != nil {
					blockers = append(blockers, err.Error())
					continue
				}
				if rewritten != command {
					value[key], changed = rewritten, true
				}
				continue
			}
			childChanged, childWraps, childBlockers := rewriteHookValue(owner, child)
			changed, needsWrapper = changed || childChanged, needsWrapper || childWraps
			blockers = append(blockers, childBlockers...)
		}
	case []any:
		for _, child := range value {
			childChanged, childWraps, childBlockers := rewriteHookValue(owner, child)
			changed, needsWrapper = changed || childChanged, needsWrapper || childWraps
			blockers = append(blockers, childBlockers...)
		}
	}
	return changed, needsWrapper, blockers
}

func rewriteHookCommand(owner agentBundleOwner, command string) (string, bool, error) {
	trimmed := strings.TrimLeft(command, " \t")
	if trimmed == "" {
		return command, false, fmt.Errorf("owner %q: hook command is empty", owner.Name)
	}
	if strings.ContainsAny(trimmed, "'\"\\\r\n;|&><") {
		return command, false, fmt.Errorf("owner %q: hook command cannot be tokenized losslessly", owner.Name)
	}
	tokens := strings.Fields(trimmed)
	needsWrapper, changed := false, false
	for i, token := range tokens {
		option, candidate, optionPath := splitPathOption(token)
		if !optionPath {
			candidate = token
		}
		if invalidPathPlaceholder(candidate) {
			return command, false, fmt.Errorf("owner %q: hook command uses unsupported environment path placeholder", owner.Name)
		}
		if strings.HasPrefix(token, "-") && !optionPath {
			continue
		}
		rel, isPath := bundleRuntimePathAt(candidate, owner, ".", false)
		if !isPath {
			continue
		}
		resolved := filepath.Join(owner.Root, rel)
		if target, err := filepath.EvalSymlinks(resolved); err != nil || !pathWithin(target, owner.Root) {
			return command, false, fmt.Errorf("owner %q: hook command references missing or escaping runtime path", owner.Name)
		}
		rewritten := rewriteWrapperRuntimeString(candidate, owner, false)
		if optionPath {
			rewritten = option + rewritten
		}
		changed = changed || rewritten != token
		tokens[i] = rewritten
		needsWrapper = needsWrapper || !strings.Contains(candidate, "${CLAUDE_PLUGIN_ROOT}")
	}
	if !changed {
		return command, needsWrapper, nil
	}
	prefix := command[:len(command)-len(trimmed)]
	return prefix + strings.Join(tokens, " "), needsWrapper, nil
}

func buildBundleWrapper(owner agentBundleOwner, stateDir string) (agentBundleWrapper, error) {
	if stateDir == "" {
		return agentBundleWrapper{}, fmt.Errorf("state directory is required for normalized wrapper")
	}
	manifest, sourceManifest, err := wrapperManifestBase(owner)
	if err != nil {
		return agentBundleWrapper{}, err
	}
	for _, key := range slices.Sorted(maps.Keys(owner.Children)) {
		child := owner.Children[key]
		if sourceManifest != "" && filepath.Clean(child.SourcePath) == filepath.Clean(sourceManifest) {
			continue
		}
		if child.MCP != nil {
			if err := appendWrapperDependency(manifest, "dependencies", "mcp", rewriteWrapperMCP(*child.MCP, owner)); err != nil {
				return agentBundleWrapper{}, err
			}
		}
		if child.LSP != nil {
			if err := appendWrapperDependency(manifest, "dependencies", "lsp", rewriteWrapperLSP(*child.LSP, owner)); err != nil {
				return agentBundleWrapper{}, err
			}
		}
	}
	manifestRaw, err := yaml.Marshal(manifest)
	if err != nil {
		return agentBundleWrapper{}, err
	}
	files := make([]agentBundleFile, 0, len(owner.Files))
	for _, file := range owner.Files {
		file.Dest = wrapperDestination(owner, file.Dest)
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Dest < files[j].Dest })
	h := sha256.New()
	_, _ = h.Write(manifestRaw)
	for _, file := range files {
		_, _ = io.WriteString(h, fmt.Sprintf("%s\x00%s\x00%04o\x00", file.Dest, file.Hash, normalizedBundleMode(file.Mode)))
	}
	hash := hex.EncodeToString(h.Sum(nil))
	return agentBundleWrapper{Hash: hash, Path: filepath.Join(stateDir, "agents-migration", "bundles", hash), Manifest: manifestRaw, Files: files}, nil
}

func wrapperManifestBase(owner agentBundleOwner) (map[string]any, string, error) {
	for _, file := range owner.Files {
		if file.Dest != "apm.yml" && file.Dest != "apm.yaml" {
			continue
		}
		raw, _, err := readRegularBounded(file.Source, maxBundleManifestBytes)
		if err != nil {
			return nil, "", err
		}
		var manifest map[string]any
		if err := yaml.Unmarshal(raw, &manifest); err != nil {
			return nil, "", err
		}
		if err := rewriteSourceAPMManifest(manifest, owner); err != nil {
			return nil, "", err
		}
		return manifest, file.Source, nil
	}
	return map[string]any{"name": sanitizeBundleName(owner.Name), "version": "0.0.0+omni-migration", "dependencies": map[string]any{}}, "", nil
}

func rewriteSourceAPMManifest(manifest map[string]any, owner agentBundleOwner) error {
	for _, bucket := range []string{"dependencies", "devDependencies"} {
		deps, ok := stringMap(manifest[bucket])
		if !ok {
			continue
		}
		if items, ok := deps["apm"].([]any); ok {
			for i, item := range items {
				fields, mapped := stringMap(item)
				if !mapped {
					if text, isString := item.(string); isString && (filepath.IsAbs(text) || strings.HasPrefix(text, ".")) {
						items[i] = rewriteWrapperRuntimeString(text, owner, true)
					}
					continue
				}
				for _, key := range []string{"path", "local_path", "localPath", "git"} {
					if text, _ := fields[key].(string); text != "" && (key != "git" || filepath.IsAbs(text)) {
						fields[key] = rewriteWrapperRuntimeString(text, owner, true)
					}
				}
				items[i] = fields
			}
			deps["apm"] = items
		}
		mcp, mcpBlockers := serviceList(owner, deps["mcp"], "mcp")
		lsp, lspBlockers := lspServiceList(owner, deps["lsp"])
		if len(mcpBlockers)+len(lspBlockers) != 0 {
			return errors.New(strings.Join(append(mcpBlockers, lspBlockers...), "\n"))
		}
		if deps["mcp"] != nil {
			deps["mcp"] = structsToAny(mcp, func(dep apmMCPDep) any { return rewriteWrapperMCP(dep, owner) })
		}
		if deps["lsp"] != nil {
			deps["lsp"] = structsToAny(lsp, func(dep apmLSPDep) any { return rewriteWrapperLSP(dep, owner) })
		}
		manifest[bucket] = deps
	}
	if includes, exists := manifest["includes"]; exists {
		switch includes := includes.(type) {
		case string:
			if includes != "auto" {
				manifest["includes"] = []any{rewriteWrapperRuntimeString(includes, owner, true)}
			}
		case []any:
			for i, item := range includes {
				text, ok := item.(string)
				if !ok {
					return fmt.Errorf("includes must contain only strings")
				}
				includes[i] = rewriteWrapperRuntimeString(text, owner, true)
			}
			manifest["includes"] = includes
		default:
			return fmt.Errorf("includes must be a string or string list")
		}
	}
	if scripts, ok := stringMap(manifest["scripts"]); ok {
		for name, value := range scripts {
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("script %q must be a string", name)
			}
			rewritten, _, err := rewriteHookCommand(owner, text)
			if err != nil {
				return fmt.Errorf("script %q: %w", name, err)
			}
			scripts[name] = rewritten
		}
		manifest["scripts"] = scripts
	}
	return nil
}

func structsToAny[T any](items []T, rewrite func(T) any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		raw, _ := yaml.Marshal(rewrite(item))
		var mapped map[string]any
		_ = yaml.Unmarshal(raw, &mapped)
		out = append(out, mapped)
	}
	return out
}

func appendWrapperDependency(manifest map[string]any, bucket, kind string, dep any) error {
	dependencies, _ := stringMap(manifest[bucket])
	if dependencies == nil {
		dependencies = map[string]any{}
	}
	items, _ := dependencies[kind].([]any)
	raw, err := yaml.Marshal(dep)
	if err != nil {
		return err
	}
	var mapped map[string]any
	if err := yaml.Unmarshal(raw, &mapped); err != nil {
		return err
	}
	dependencies[kind] = append(items, mapped)
	manifest[bucket] = dependencies
	return nil
}

func sanitizeBundleName(name string) string {
	name = strings.ToLower(name)
	var out strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	if out.Len() == 0 {
		return "omni-bundle"
	}
	return out.String()
}

func wrapperDestination(owner agentBundleOwner, rel string) string {
	rel = filepath.ToSlash(rel)
	for _, dir := range []string{".apm/", "skills/", "hooks/", "agents/", "commands/"} {
		if strings.HasPrefix(rel, dir) {
			return rel
		}
	}
	if rel == "SKILL.md" {
		return "skills/" + sanitizeBundleName(owner.Name) + "/SKILL.md"
	}
	return "runtime/" + rel
}

func rewriteWrapperMCP(dep apmMCPDep, owner agentBundleOwner) apmMCPDep {
	dep.Command, dep.Cwd, dep.Args = rewriteWrapperServiceRuntime(dep.Command, dep.Cwd, dep.Args, owner)
	dep.URL = apmPlaceholders(dep.URL)
	for key, value := range dep.Headers {
		dep.Headers[key] = apmPlaceholders(value)
	}
	for key, value := range dep.Env {
		dep.Env[key] = apmPlaceholders(rewriteWrapperRuntimeString(value, owner, false))
	}
	return dep
}

func rewriteWrapperLSP(dep apmLSPDep, owner agentBundleOwner) apmLSPDep {
	dep.Command, dep.Cwd, dep.Args = rewriteWrapperServiceRuntime(dep.Command, dep.Cwd, dep.Args, owner)
	for key, value := range dep.Env {
		dep.Env[key] = apmPlaceholders(rewriteWrapperRuntimeString(value, owner, false))
	}
	dep.WorkspaceFolder = rewriteWrapperRuntimeString(dep.WorkspaceFolder, owner, dep.WorkspaceFolder != "")
	return dep
}

func rewriteWrapperServiceRuntime(command, cwd string, args []string, owner agentBundleOwner) (string, string, []string) {
	sourceCwd := "."
	if cwd != "" {
		if rel, ok := bundleRuntimePathAt(cwd, owner, ".", true); ok {
			sourceCwd = rel
		}
	}
	wrapperCwd := "."
	if sourceCwd != "." {
		wrapperCwd = wrapperPathForSourceRel(owner, filepath.ToSlash(sourceCwd))
	}
	rewrite := func(value string, force bool) string {
		option, candidate, optionPath := splitPathOption(value)
		if !optionPath {
			candidate = value
		}
		sourceRel, isPath := bundleRuntimePathAt(candidate, owner, sourceCwd, force)
		if !isPath {
			return apmPlaceholders(normalizeOwnerString(value, owner))
		}
		destination := wrapperPathForSourceRel(owner, filepath.ToSlash(sourceRel))
		rebased, err := filepath.Rel(filepath.FromSlash(wrapperCwd), filepath.FromSlash(destination))
		if err != nil {
			return destination
		}
		rewritten := filepath.ToSlash(rebased)
		if optionPath {
			return option + rewritten
		}
		return rewritten
	}
	rewrittenArgs := slices.Clone(args)
	for i := range rewrittenArgs {
		rewrittenArgs[i] = rewrite(rewrittenArgs[i], false)
	}
	rewrittenCwd := ""
	if cwd != "" {
		rewrittenCwd = wrapperCwd
	}
	return rewrite(command, false), rewrittenCwd, rewrittenArgs
}

func rewriteWrapperRuntimeString(value string, owner agentBundleOwner, force bool) string {
	normalized := normalizeOwnerString(value, owner)
	if rel, isPath := bundleRuntimePath(value, owner, force); isPath {
		if rel == "." {
			return "."
		}
		return wrapperPathForSourceRel(owner, filepath.ToSlash(rel))
	}
	return apmPlaceholders(normalized)
}

func wrapperPathForSourceRel(owner agentBundleOwner, rel string) string {
	return wrapperDestination(owner, strings.TrimPrefix(filepath.ToSlash(rel), "./"))
}

func materializeAgentBundleWrappers(plan agentBundlePlan) error {
	prepared, err := prepareAgentBundleWrappers(plan)
	if err != nil {
		return err
	}
	defer discardPreparedAgentBundleWrappers(prepared)
	return publishPreparedAgentBundleWrappers(prepared)
}

type preparedAgentBundleWrapper struct {
	wrapper agentBundleWrapper
	temp    string
}

func prepareAgentBundleWrappers(plan agentBundlePlan) ([]preparedAgentBundleWrapper, error) {
	if len(plan.Blockers) != 0 {
		return nil, errors.New(strings.Join(plan.Blockers, "\n"))
	}
	prepared := make([]preparedAgentBundleWrapper, 0, len(plan.Wrappers))
	for _, wrapper := range plan.Wrappers {
		item, err := prepareAgentBundleWrapper(wrapper)
		if err != nil {
			discardPreparedAgentBundleWrappers(prepared)
			return nil, err
		}
		prepared = append(prepared, item)
	}
	return prepared, nil
}

func prepareAgentBundleWrapper(wrapper agentBundleWrapper) (preparedAgentBundleWrapper, error) {
	item := preparedAgentBundleWrapper{wrapper: wrapper}
	if wrapper.Path == "" || filepath.Base(wrapper.Path) != wrapper.Hash || !validAgentBundleHash(wrapper.Hash) {
		return item, fmt.Errorf("invalid wrapper path")
	}
	if info, err := os.Lstat(wrapper.Path); err == nil {
		if !info.IsDir() {
			return item, fmt.Errorf("wrapper %s is corrupt", wrapper.Hash)
		}
		ok, err := wrapperMatches(wrapper)
		if err != nil || !ok {
			return item, fmt.Errorf("wrapper %s is corrupt", wrapper.Hash)
		}
		return item, nil
	} else if !os.IsNotExist(err) {
		return item, err
	}
	parent := filepath.Dir(wrapper.Path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return item, err
	}
	temp, err := os.MkdirTemp(parent, ".omni-bundle-*")
	if err != nil {
		return item, err
	}
	item.temp = temp
	fail := func(err error) (preparedAgentBundleWrapper, error) {
		_ = os.RemoveAll(temp)
		return preparedAgentBundleWrapper{}, err
	}
	if err := writeSyncedFile(filepath.Join(temp, "apm.yml"), wrapper.Manifest, 0o600); err != nil {
		return fail(err)
	}
	for _, file := range wrapper.Files {
		data := file.Data
		if data == nil {
			data, _, err = readRegularBounded(file.Source, maxBundleFileBytes)
			if err != nil {
				return fail(err)
			}
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != file.Size || hex.EncodeToString(sum[:]) != file.Hash {
			return fail(fmt.Errorf("bundle source changed during materialization: %s", file.Dest))
		}
		if err := writeSyncedFile(filepath.Join(temp, filepath.FromSlash(file.Dest)), data, normalizedBundleMode(file.Mode)); err != nil {
			return fail(err)
		}
	}
	if err := syncDir(temp); err != nil {
		return fail(err)
	}
	return item, nil
}

func publishPreparedAgentBundleWrappers(prepared []preparedAgentBundleWrapper) error {
	parents := map[string]bool{}
	for i := range prepared {
		if prepared[i].temp == "" {
			continue
		}
		wrapper := prepared[i].wrapper
		if err := os.Rename(prepared[i].temp, wrapper.Path); err != nil {
			if ok, compareErr := wrapperMatches(wrapper); compareErr == nil && ok {
				_ = os.RemoveAll(prepared[i].temp)
				prepared[i].temp = ""
				continue
			}
			return err
		}
		prepared[i].temp = ""
		parents[filepath.Dir(wrapper.Path)] = true
	}
	for parent := range parents {
		if err := syncDir(parent); err != nil {
			return err
		}
	}
	return nil
}

func discardPreparedAgentBundleWrappers(prepared []preparedAgentBundleWrapper) {
	for _, item := range prepared {
		if item.temp != "" {
			_ = os.RemoveAll(item.temp)
		}
	}
}

func validAgentBundleHash(name string) bool {
	if len(name) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if runtime.GOOS == "windows" {
		return nil
	}
	return dir.Sync()
}

func wrapperMatches(wrapper agentBundleWrapper) (bool, error) {
	type fileIdentity struct {
		hash string
		mode os.FileMode
	}
	want := map[string]fileIdentity{"apm.yml": {hashBytes(wrapper.Manifest), 0o600}}
	for _, file := range wrapper.Files {
		want[filepath.ToSlash(file.Dest)] = fileIdentity{file.Hash, normalizedBundleMode(file.Mode)}
	}
	got := map[string]fileIdentity{}
	err := filepath.WalkDir(wrapper.Path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular wrapper file")
		}
		rel, _ := filepath.Rel(wrapper.Path, path)
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		got[filepath.ToSlash(rel)] = fileIdentity{hash, info.Mode().Perm()}
		return nil
	})
	return err == nil && maps.Equal(got, want), err
}

func normalizedBundleMode(mode os.FileMode) os.FileMode {
	if mode&0o111 != 0 {
		return 0o700
	}
	return 0o600
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func pathWithin(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
