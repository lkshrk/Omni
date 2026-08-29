package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type apmModuleManifest struct {
	Name         string          `yaml:"name"`
	Description  string          `yaml:"description"`
	Author       string          `yaml:"author"`
	Homepage     string          `yaml:"homepage"`
	Dependencies apmDependencies `yaml:"dependencies"`
}

// Pinned APM 0.29.0 plugin/service discovery surfaces. Presence of any carrier
// makes a manifestless package's MCP/LSP ownership unknowable.
var apmManifestlessServiceCarriers = []string{
	"plugin.json",
	".claude-plugin",
	".github/plugin/plugin.json",
	".claude-plugin/plugin.json",
	".cursor-plugin/plugin.json",
	"mcp.json",
	".mcp.json",
	".github/.mcp.json",
	"com.microsoft.apm/mcp.json",
	"com.microsoft.apm/lsp.json",
	"lsp.json",
	".lsp.json",
}

// Narrow test seam for deterministic read failures on platforms/root users where chmod is ineffective.
var (
	agentsModuleLstat   = os.Lstat
	agentsModuleReadDir = os.ReadDir
	agentsModuleOpen    = os.Open
)

// An installed package keeps its own manifest under apm_modules, the only local source of a description.
func readAPMModuleManifests(dir string, rows []AgentsPackageRow) agentsOwnershipEvidence {
	var evidence agentsOwnershipEvidence
	root := filepath.Join(dir, "apm_modules")
	for i := range rows {
		if rows[i].ModuleSource == "" {
			if rows[i].Source != "" && rows[i].Source != agentsUnrecognizedSource {
				evidence.Unavailable = append(evidence.Unavailable, rows[i].Name)
			}
			continue
		}
		path, ok := resolveModulePath(root, rows[i].ModuleSource)
		if !ok {
			evidence.Unavailable = append(evidence.Unavailable, rows[i].Name)
			continue
		}
		manifestPath := filepath.Join(path, "apm.yml")
		data, info, err := readAPMModuleRegularFile(root, path, manifestPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if identities, ok := manifestlessSkillOnlyEvidence(root, path, rows[i]); ok {
					evidence.Manifests = append(evidence.Manifests, identities...)
					continue
				}
			}
			evidence.Unavailable = append(evidence.Unavailable, rows[i].Name)
			if !os.IsNotExist(err) {
				rows[i].Issues = append(rows[i].Issues, "package manifest cannot be evaluated: "+manifestPath)
			}
			continue
		}
		sum := sha256.Sum256(data)
		evidence.Manifests = append(evidence.Manifests, agentsModuleManifestIdentity{Path: manifestPath, Hash: hex.EncodeToString(sum[:]), Info: info})
		var manifest apmModuleManifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			evidence.Unavailable = append(evidence.Unavailable, rows[i].Name)
			rows[i].Issues = append(rows[i].Issues, invalidAPMModuleManifestIssue(manifestPath, err))
			continue
		}
		rows[i].Description = strings.TrimSpace(manifest.Description)
		rows[i].Author = strings.TrimSpace(manifest.Author)
		rows[i].Homepage = strings.TrimSpace(manifest.Homepage)
		owner := rows[i].Name
		if owner == "" {
			owner = strings.TrimSpace(manifest.Name)
		}
		for _, dep := range manifest.Dependencies.MCP {
			if dep.Name == "" {
				continue
			}
			dep := dep
			evidence.Children = append(evidence.Children, agentsOwnedChild{
				Kind: agentsChildMCP, Name: dep.Name, Owner: owner, OwnerRoot: path,
				Fingerprint: agentsMCPFingerprint(dep, path), MCP: &dep,
			})
		}
		for _, dep := range manifest.Dependencies.LSP {
			if dep.Name == "" {
				continue
			}
			dep := dep
			evidence.Children = append(evidence.Children, agentsOwnedChild{
				Kind: agentsChildLSP, Name: dep.Name, Owner: owner, OwnerRoot: path,
				Fingerprint: agentsLSPFingerprint(dep, path), LSP: &dep,
			})
		}
	}
	sort.Slice(evidence.Children, func(i, j int) bool {
		if evidence.Children[i].Kind != evidence.Children[j].Kind {
			return evidence.Children[i].Kind < evidence.Children[j].Kind
		}
		if !strings.EqualFold(evidence.Children[i].Name, evidence.Children[j].Name) {
			return strings.ToLower(evidence.Children[i].Name) < strings.ToLower(evidence.Children[j].Name)
		}
		return evidence.Children[i].Owner < evidence.Children[j].Owner
	})
	sort.Strings(evidence.Unavailable)
	evidence.Unavailable = slices.Compact(evidence.Unavailable)
	sort.Slice(evidence.Manifests, func(i, j int) bool { return evidence.Manifests[i].Path < evidence.Manifests[j].Path })
	return evidence
}

var yamlLineNumber = regexp.MustCompile(`(?:^|[ :])line ([0-9]+)(?:[ :]|$)`)

func invalidAPMModuleManifestIssue(path string, err error) string {
	if match := yamlLineNumber.FindStringSubmatch(err.Error()); len(match) == 2 {
		return fmt.Sprintf("invalid package manifest %s:%s", path, match[1])
	}
	return "invalid package manifest " + path
}

func readAPMModuleManifest(root, module, manifestPath string) (data []byte, retErr error) {
	data, _, retErr = readAPMModuleRegularFile(root, module, manifestPath)
	return data, retErr
}

func readAPMModuleRegularFile(root, module, manifestPath string) (data []byte, identity os.FileInfo, retErr error) {
	info, err := agentsModuleLstat(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("module manifest is not a regular file")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	resolvedModule, err := filepath.EvalSymlinks(module)
	if err != nil || !agentsPathWithinRoot(resolvedRoot, resolvedModule) {
		return nil, nil, fmt.Errorf("module escapes modules root")
	}
	resolvedManifest, err := filepath.EvalSymlinks(manifestPath)
	if err != nil || !agentsPathWithinRoot(resolvedModule, resolvedManifest) || !agentsPathWithinRoot(resolvedRoot, resolvedManifest) {
		return nil, nil, fmt.Errorf("module manifest escapes module root")
	}
	file, err := agentsModuleOpen(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, nil, fmt.Errorf("module manifest changed while opening")
	}
	resolvedAfterOpen, err := filepath.EvalSymlinks(manifestPath)
	if err != nil || resolvedAfterOpen != resolvedManifest || !agentsPathWithinRoot(resolvedModule, resolvedAfterOpen) {
		return nil, nil, fmt.Errorf("module manifest changed while opening")
	}
	data, err = io.ReadAll(io.LimitReader(file, maxBundleManifestBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxBundleManifestBytes {
		return nil, nil, fmt.Errorf("module manifest exceeds size limit")
	}
	return data, opened, nil
}

func manifestlessSkillOnlyEvidence(root, module string, row AgentsPackageRow) ([]agentsModuleManifestIdentity, bool) {
	lock := row.lockEvidence
	if lock == nil || (lock.PackageType != "claude_skill" && lock.PackageType != "skill_bundle") ||
		strings.TrimSpace(lock.ResolvedCommit) == "" || len(lock.DeployedFiles) == 0 {
		return nil, false
	}

	skills := make(map[string]struct{})
	for _, deployed := range lock.DeployedFiles {
		if deployed == "" || strings.TrimSpace(deployed) != deployed || strings.Contains(deployed, "\\") ||
			pathpkg.IsAbs(deployed) || pathpkg.Clean(deployed) != deployed {
			return nil, false
		}
		parts := strings.Split(deployed, "/")
		if len(parts) < 3 || (parts[0] != ".agents" && parts[0] != ".claude") || parts[1] != "skills" ||
			parts[2] == "" || parts[2] == "." || parts[2] == ".." || strings.EqualFold(parts[2], "SKILL.md") {
			return nil, false
		}
		for prior := range skills {
			if strings.EqualFold(prior, parts[2]) && prior != parts[2] {
				return nil, false
			}
		}
		skills[parts[2]] = struct{}{}
	}
	if len(skills) == 0 || (lock.PackageType == "claude_skill" && len(skills) != 1) {
		return nil, false
	}

	rootInfo, err := agentsModuleLstat(module)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false
	}
	resolvedModule, err := filepath.EvalSymlinks(module)
	if err != nil || !agentsPathWithinRoot(resolvedRoot, resolvedModule) {
		return nil, false
	}
	currentRootInfo, err := agentsModuleLstat(module)
	if err != nil || !sameAgentsModuleDirectoryIdentity(rootInfo, currentRootInfo) {
		return nil, false
	}

	lockCopy := *lock
	lockCopy.DeployedFiles = slices.Clone(lock.DeployedFiles)
	sort.Strings(lockCopy.DeployedFiles)
	identities := []agentsModuleManifestIdentity{{
		Path: module, ModuleRoot: module, Info: rootInfo, Directory: true, Lock: &lockCopy,
	}}

	skillNames := make([]string, 0, len(skills))
	for name := range skills {
		skillNames = append(skillNames, name)
	}
	sort.Strings(skillNames)
	if lock.PackageType == "claude_skill" {
		skillNames = []string{""}
	}
	for _, name := range skillNames {
		rel := "SKILL.md"
		if name != "" {
			rel = pathpkg.Join("skills", name, "SKILL.md")
		}
		identity, exists, ok := inspectAPMModuleEvidencePath(module, rel)
		if !ok || !exists || identity.Info == nil || !identity.Info.Mode().IsRegular() {
			return nil, false
		}
		data, info, err := readAPMModuleRegularFile(root, module, identity.Path)
		if err != nil || !os.SameFile(identity.Info, info) {
			return nil, false
		}
		sum := sha256.Sum256(data)
		identity.Hash = hex.EncodeToString(sum[:])
		identity.Info = info
		identities = append(identities, identity)
	}

	for _, rel := range append([]string{"apm.yml"}, apmManifestlessServiceCarriers...) {
		identity, exists, ok := inspectAPMModuleEvidencePath(module, rel)
		if !ok || exists {
			return nil, false
		}
		identities = append(identities, identity)
	}
	return identities, true
}

// inspectAPMModuleEvidencePath performs one bounded, exact-case walk beneath a resolved module.
// It captures every existing parent so later mutation checks can revalidate negative evidence.
func inspectAPMModuleEvidencePath(module, rel string) (agentsModuleManifestIdentity, bool, bool) {
	identity := agentsModuleManifestIdentity{Path: filepath.Join(module, filepath.FromSlash(rel)), ModuleRoot: module, RelativePath: rel}
	if rel == "" || strings.Contains(rel, "\\") || pathpkg.IsAbs(rel) || pathpkg.Clean(rel) != rel {
		return identity, false, false
	}
	resolvedModule, err := filepath.EvalSymlinks(module)
	if err != nil {
		return identity, false, false
	}
	current := module
	segments := strings.Split(rel, "/")
	for i, segment := range segments {
		currentInfo, err := agentsModuleLstat(current)
		if err != nil || !currentInfo.IsDir() || currentInfo.Mode()&os.ModeSymlink != 0 {
			return identity, false, false
		}
		identity.Parents = append(identity.Parents, agentsModulePathIdentity{Path: current, Info: currentInfo})
		entries, err := agentsModuleReadDir(current)
		if err != nil || len(entries) > maxBundleEntries {
			return identity, false, false
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		matches := 0
		exact := false
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), segment) {
				matches++
				exact = exact || entry.Name() == segment
			}
		}
		if matches == 0 {
			identity.Absent = true
			return identity, false, true
		}
		if matches != 1 || !exact {
			return identity, false, false
		}
		current = filepath.Join(current, segment)
		info, err := agentsModuleLstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return identity, false, false
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || !agentsPathWithinRoot(resolvedModule, resolved) {
			return identity, false, false
		}
		if i < len(segments)-1 && !info.IsDir() {
			return identity, false, false
		}
		if i == len(segments)-1 {
			identity.Path = current
			identity.Info = info
			identity.Directory = info.IsDir()
			return identity, true, true
		}
	}
	return identity, false, false
}

// The lock lowercases repo_url while the deployed directory keeps the source's casing, so each segment
// falls back to a case-insensitive match.
func resolveModulePath(root, source string) (string, bool) {
	if source == "" || strings.Contains(source, "\\") {
		return "", false
	}
	segments := strings.Split(source, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	root = filepath.Clean(root)
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	absRoot, absErr := filepath.Abs(root)
	if err != nil || absErr != nil || filepath.Clean(resolvedRoot) != filepath.Clean(absRoot) {
		return "", false
	}
	current := root
	for _, segment := range segments {
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", false
		}
		var matches []string
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), segment) {
				if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
					return "", false
				}
				matches = append(matches, entry.Name())
			}
		}
		if len(matches) != 1 {
			return "", false
		}
		current = filepath.Join(current, matches[0])
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || !agentsPathWithinRoot(resolvedRoot, resolved) {
			return "", false
		}
	}
	return current, true
}

func agentsPathWithinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
