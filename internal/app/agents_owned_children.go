package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type agentsChildKind string

const (
	agentsChildMCP agentsChildKind = "mcp"
	agentsChildLSP agentsChildKind = "lsp"
)

type agentsOwnedChild struct {
	Kind        agentsChildKind
	Name        string
	Owner       string
	OwnerRoot   string
	Fingerprint string
	MCP         *apmMCPDep
	LSP         *apmLSPDep
}

type agentsChildCollision struct {
	Child      agentsOwnedChild
	Standalone bool
	Exact      bool
	Message    string
}

type agentsOwnershipEvidence struct {
	Children    []agentsOwnedChild
	Unavailable []string
	Manifests   []agentsModuleManifestIdentity
}

type agentsModuleManifestIdentity struct {
	Path         string
	Hash         string
	ModuleRoot   string
	RelativePath string
	Info         os.FileInfo
	Parents      []agentsModulePathIdentity
	Absent       bool
	Directory    bool
	Lock         *agentsPackageLockEvidence
}

// agentsModulePathIdentity pins each existing parent used to prove a candidate absent.
type agentsModulePathIdentity struct {
	Path string
	Info os.FileInfo
}

// sameAgentsModuleDirectoryIdentity strengthens SameFile against rapid inode reuse.
// Directory contents are pinned separately; mode, size, and nanosecond mtime pin the root itself.
func sameAgentsModuleDirectoryIdentity(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) &&
		sameAgentsModuleDirectoryMetadata(before.Mode(), before.Size(), before.ModTime(), after)
}

func sameAgentsModuleDirectoryMetadata(mode os.FileMode, size int64, modTime time.Time, current os.FileInfo) bool {
	return current != nil && mode.IsDir() && current.IsDir() && mode == current.Mode() && size == current.Size() &&
		modTime.Equal(current.ModTime())
}

func agentsChildKey(kind agentsChildKind, name string) string {
	return strings.ToLower(string(kind)) + "\x00" + strings.ToLower(name)
}

// Kept for migration callers while ownership comparison shares one case-insensitive key.
func childKey(kind, name string) string { return agentsChildKey(agentsChildKind(kind), name) }

func fingerprintMCP(dep apmMCPDep, owner agentBundleOwner) string {
	return agentsHashJSON(agentsNormalizeMCP(dep, owner.Original, owner.Root))
}

func fingerprintLSP(dep apmLSPDep, owner agentBundleOwner) string {
	return agentsHashJSON(agentsNormalizeLSP(dep, owner.Original, owner.Root))
}

func normalizeOwnerString(value string, owner agentBundleOwner) string {
	return agentsNormalizeOwnerString(value, owner.Original, owner.Root)
}

func agentsMCPFingerprint(dep apmMCPDep, ownerRoot string) string {
	return agentsMCPFingerprintRoots(dep, ownerRoot)
}

func agentsMCPFingerprintRoots(dep apmMCPDep, ownerRoots ...string) string {
	return agentsHashJSON(agentsNormalizeRawMapping(agentsMCPMapping(dep), ownerRoots...))
}

func agentsLSPFingerprint(dep apmLSPDep, ownerRoot string) string {
	return agentsLSPFingerprintRoots(dep, ownerRoot)
}

func agentsLSPFingerprintRoots(dep apmLSPDep, ownerRoots ...string) string {
	return agentsHashJSON(agentsNormalizeRawMapping(agentsLSPMapping(dep), ownerRoots...))
}

func (dep *apmMCPDep) UnmarshalYAML(node *yaml.Node) error {
	type plain apmMCPDep
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	raw, err := agentsDecodeRawMapping(node)
	if err != nil {
		return err
	}
	*dep = apmMCPDep(decoded)
	dep.Raw = raw
	return nil
}

func (dep *apmLSPDep) UnmarshalYAML(node *yaml.Node) error {
	type plain apmLSPDep
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	raw, err := agentsDecodeRawMapping(node)
	if err != nil {
		return err
	}
	*dep = apmLSPDep(decoded)
	dep.Raw = raw
	return nil
}

func agentsDecodeRawMapping(node *yaml.Node) (map[string]any, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("service declaration must be a mapping")
	}
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := json.Marshal(raw); err != nil {
		return nil, fmt.Errorf("service declaration contains unsupported values: %w", err)
	}
	return raw, nil
}

func agentsMCPMapping(dep apmMCPDep) map[string]any {
	if dep.Raw != nil {
		return dep.Raw
	}
	dep.Raw = nil
	return agentsMarshaledMapping(dep)
}

func agentsLSPMapping(dep apmLSPDep) map[string]any {
	if dep.Raw != nil {
		return dep.Raw
	}
	dep.Raw = nil
	return agentsMarshaledMapping(dep)
}

func agentsMarshaledMapping(value any) map[string]any {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return map[string]any{"<unsupported>": err.Error()}
	}
	var mapping map[string]any
	if err := yaml.Unmarshal(raw, &mapping); err != nil {
		return map[string]any{"<unsupported>": err.Error()}
	}
	return mapping
}

func agentsNormalizeRawMapping(mapping map[string]any, ownerRoots ...string) map[string]any {
	normalized := make(map[string]any, len(mapping))
	for key, value := range mapping {
		normalized[key] = agentsNormalizeRawValue(value, ownerRoots...)
	}
	return normalized
}

func agentsNormalizeRawValue(value any, ownerRoots ...string) any {
	switch value := value.(type) {
	case string:
		return agentsNormalizeRawOwnerString(value, ownerRoots...)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = agentsNormalizeRawValue(value[i], ownerRoots...)
		}
		return out
	case map[string]any:
		return agentsNormalizeRawMapping(value, ownerRoots...)
	default:
		return value
	}
}

func agentsNormalizeRawOwnerString(value string, ownerRoots ...string) string {
	for _, placeholder := range []string{"${CLAUDE_PLUGIN_ROOT}", "${CODEX_PLUGIN_ROOT}", "${PLUGIN_ROOT}"} {
		value = strings.ReplaceAll(value, placeholder, "<root>")
	}
	if option, path, ok := splitPathOption(value); ok {
		value = option + agentsNormalizeAbsoluteOwnerPath(path, ownerRoots...)
	} else {
		value = agentsNormalizeAbsoluteOwnerPath(value, ownerRoots...)
	}
	return filepath.ToSlash(value)
}

func agentsNormalizeMCP(dep apmMCPDep, ownerRoots ...string) apmMCPDep {
	dep.Args = slices.Clone(dep.Args)
	dep.Headers = maps.Clone(dep.Headers)
	dep.Env = maps.Clone(dep.Env)
	dep.Tools = slices.Clone(dep.Tools)
	dep.Command = agentsNormalizeOwnerString(dep.Command, ownerRoots...)
	dep.Cwd = agentsNormalizeOwnerString(dep.Cwd, ownerRoots...)
	dep.URL = agentsNormalizeOwnerString(dep.URL, ownerRoots...)
	for i := range dep.Args {
		dep.Args[i] = agentsNormalizeOwnerString(dep.Args[i], ownerRoots...)
	}
	for key, value := range dep.Headers {
		dep.Headers[key] = agentsNormalizeOwnerString(value, ownerRoots...)
	}
	for key, value := range dep.Env {
		dep.Env[key] = agentsNormalizeOwnerString(value, ownerRoots...)
	}
	return dep
}

func agentsNormalizeLSP(dep apmLSPDep, ownerRoots ...string) apmLSPDep {
	dep.Args = slices.Clone(dep.Args)
	dep.Env = maps.Clone(dep.Env)
	dep.ExtensionToLanguage = maps.Clone(dep.ExtensionToLanguage)
	dep.Command = agentsNormalizeOwnerString(dep.Command, ownerRoots...)
	dep.Cwd = agentsNormalizeOwnerString(dep.Cwd, ownerRoots...)
	for i := range dep.Args {
		dep.Args[i] = agentsNormalizeOwnerString(dep.Args[i], ownerRoots...)
	}
	for key, value := range dep.Env {
		dep.Env[key] = agentsNormalizeOwnerString(value, ownerRoots...)
	}
	return dep
}

func agentsNormalizeOwnerString(value string, ownerRoots ...string) string {
	for _, placeholder := range []string{"${CLAUDE_PLUGIN_ROOT}", "${CODEX_PLUGIN_ROOT}", "${PLUGIN_ROOT}"} {
		value = strings.ReplaceAll(value, placeholder, "<root>")
	}
	if option, path, ok := splitPathOption(value); ok {
		value = option + agentsNormalizeAbsoluteOwnerPath(path, ownerRoots...)
	} else {
		value = agentsNormalizeAbsoluteOwnerPath(value, ownerRoots...)
	}
	value = strings.ReplaceAll(value, "${env:", "${")
	return filepath.ToSlash(value)
}

func agentsNormalizeAbsoluteOwnerPath(value string, ownerRoots ...string) string {
	if !filepath.IsAbs(value) {
		return value
	}
	for _, root := range ownerRoots {
		if root == "" || !pathWithin(value, root) {
			continue
		}
		rel, err := filepath.Rel(root, value)
		if err != nil || !validBundleRel(rel) {
			continue
		}
		if rel == "." {
			return "<root>"
		}
		return "<root>/" + filepath.ToSlash(rel)
	}
	return value
}

func agentsHashJSON(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func classifyAgentsOwnedChildren(manifest apmManifest, owned []agentsOwnedChild) []agentsChildCollision {
	variants := make(map[string]map[string]bool)
	for _, child := range owned {
		key := agentsChildKey(child.Kind, child.Name) + "\x00" + strings.ToLower(child.Owner)
		if variants[key] == nil {
			variants[key] = make(map[string]bool)
		}
		variants[key][agentsOwnedChildSemanticFingerprint(child)] = true
	}
	standaloneMCP := make(map[string]apmMCPDep, len(manifest.Dependencies.MCP))
	for _, dep := range manifest.Dependencies.MCP {
		standaloneMCP[agentsChildKey(agentsChildMCP, dep.Name)] = dep
	}
	standaloneLSP := make(map[string]apmLSPDep, len(manifest.Dependencies.LSP))
	for _, dep := range manifest.Dependencies.LSP {
		standaloneLSP[agentsChildKey(agentsChildLSP, dep.Name)] = dep
	}

	out := make([]agentsChildCollision, 0, len(owned))
	for _, child := range owned {
		collision := agentsChildCollision{Child: child}
		conflictingOwnerDefinitions := len(variants[agentsChildKey(child.Kind, child.Name)+"\x00"+strings.ToLower(child.Owner)]) > 1
		switch child.Kind {
		case agentsChildMCP:
			if child.MCP == nil {
				continue
			}
			dep, ok := standaloneMCP[agentsChildKey(child.Kind, child.Name)]
			if !ok {
				continue
			}
			collision.Standalone = true
			collision.Exact = !conflictingOwnerDefinitions && agentsMCPFingerprint(dep, child.OwnerRoot) == agentsOwnedChildSemanticFingerprint(child)
			collision.Message = agentsCollisionMessage(child, collision.Exact, agentsMCPDiffFields(*child.MCP, dep, child.OwnerRoot))
		case agentsChildLSP:
			if child.LSP == nil {
				continue
			}
			dep, ok := standaloneLSP[agentsChildKey(child.Kind, child.Name)]
			if !ok {
				continue
			}
			collision.Standalone = true
			collision.Exact = !conflictingOwnerDefinitions && agentsLSPFingerprint(dep, child.OwnerRoot) == agentsOwnedChildSemanticFingerprint(child)
			collision.Message = agentsCollisionMessage(child, collision.Exact, agentsLSPDiffFields(*child.LSP, dep, child.OwnerRoot))
		default:
			continue
		}
		out = append(out, collision)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Child.Kind != out[j].Child.Kind {
			return out[i].Child.Kind < out[j].Child.Kind
		}
		if !strings.EqualFold(out[i].Child.Name, out[j].Child.Name) {
			return strings.ToLower(out[i].Child.Name) < strings.ToLower(out[j].Child.Name)
		}
		return out[i].Child.Owner < out[j].Child.Owner
	})
	return out
}

func agentsOwnedChildSemanticFingerprint(child agentsOwnedChild) string {
	if child.Kind == agentsChildMCP && child.MCP != nil {
		return agentsMCPFingerprint(*child.MCP, child.OwnerRoot)
	}
	if child.Kind == agentsChildLSP && child.LSP != nil {
		return agentsLSPFingerprint(*child.LSP, child.OwnerRoot)
	}
	return child.Fingerprint
}

func agentsCollisionMessage(child agentsOwnedChild, exact bool, fields []string) string {
	prefix := strings.ToUpper(string(child.Kind)) + " " + child.Name
	if exact {
		return prefix + " duplicates a standalone declaration"
	}
	if len(fields) == 0 {
		return prefix + " conflicts with a standalone declaration"
	}
	return fmt.Sprintf("%s conflicts with a standalone declaration (%s differ)", prefix, strings.Join(fields, ", "))
}

func agentsMCPDiffFields(owner, standalone apmMCPDep, root string) []string {
	return agentsRawDiffFields(agentsMCPMapping(owner), agentsMCPMapping(standalone), root)
}

func agentsLSPDiffFields(owner, standalone apmLSPDep, root string) []string {
	return agentsRawDiffFields(agentsLSPMapping(owner), agentsLSPMapping(standalone), root)
}

func agentsRawDiffFields(owner, standalone map[string]any, root string) []string {
	owner = agentsNormalizeRawMapping(owner, root)
	standalone = agentsNormalizeRawMapping(standalone, root)
	keys := make(map[string]bool, len(owner)+len(standalone))
	for key := range owner {
		keys[key] = true
	}
	for key := range standalone {
		keys[key] = true
	}
	var fields []string
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if !reflect.DeepEqual(owner[key], standalone[key]) {
			fields = append(fields, key)
		}
	}
	return fields
}

func reconcileAgentsOwnedChildren(
	packages []AgentsPackageRow,
	manifest apmManifest,
	evidence agentsOwnershipEvidence,
	mcpInput, lspInput agentsServiceInput,
) ([]AgentsServiceRow, []AgentsServiceRow) {
	mcpRows, lspRows := joinAPMServices(mcpInput), joinAPMServices(lspInput)
	if len(manifest.Dependencies.MCP)+len(manifest.Dependencies.LSP) > 0 {
		agentsApplyUnavailableOwnership(packages, evidence.Unavailable)
	}
	if len(evidence.Children) == 0 {
		agentsFinalizePackageRows(packages)
		return mcpRows, lspRows
	}

	childrenByKey := make(map[string][]agentsOwnedChild, len(evidence.Children))
	for _, child := range evidence.Children {
		key := agentsChildKey(child.Kind, child.Name)
		childrenByKey[key] = append(childrenByKey[key], child)
	}
	collisionsByKey := make(map[string][]agentsChildCollision)
	for _, collision := range classifyAgentsOwnedChildren(manifest, evidence.Children) {
		key := agentsChildKey(collision.Child.Kind, collision.Child.Name)
		collisionsByKey[key] = append(collisionsByKey[key], collision)
	}

	packageIndexes := make(map[string][]int, len(packages))
	for i := range packages {
		packageIndexes[strings.ToLower(packages[i].Name)] = append(packageIndexes[strings.ToLower(packages[i].Name)], i)
	}
	keepTopLevel := make(map[string]bool, len(childrenByKey))
	conflictDetail := make(map[string]string, len(childrenByKey))
	for key, children := range childrenByKey {
		sort.Slice(children, func(i, j int) bool {
			if children[i].Owner != children[j].Owner {
				return children[i].Owner < children[j].Owner
			}
			return agentsOwnedChildSemanticFingerprint(children[i]) < agentsOwnedChildSemanticFingerprint(children[j])
		})
		owners := make([]string, 0, len(children))
		fingerprintsByOwner := make(map[string]map[string]bool)
		for _, child := range children {
			owners = append(owners, child.Owner)
			owner := strings.ToLower(child.Owner)
			if fingerprintsByOwner[owner] == nil {
				fingerprintsByOwner[owner] = make(map[string]bool)
			}
			fingerprintsByOwner[owner][agentsOwnedChildSemanticFingerprint(child)] = true
		}
		owners = slices.Compact(owners)
		conflictingOwnerDefinitions := false
		for _, fingerprints := range fingerprintsByOwner {
			conflictingOwnerDefinitions = conflictingOwnerDefinitions || len(fingerprints) > 1
		}
		ambiguous := len(owners) > 1 || conflictingOwnerDefinitions
		if ambiguous {
			keepTopLevel[key] = len(collisionsByKey[key]) != 0
			if len(owners) > 1 {
				conflictDetail[key] = "conflicts with packages " + strings.Join(owners, ", ")
			} else {
				conflictDetail[key] = "conflicts with package " + owners[0]
			}
		}
		provided := make(map[string]bool)
		for _, child := range children {
			indexes := packageIndexes[strings.ToLower(child.Owner)]
			status := agentsOwnedChildRuntimeStatus(child, mcpInput, lspInput)
			for _, index := range indexes {
				providedKey := fmt.Sprintf("%d\x00%s", index, key)
				if !provided[providedKey] {
					packages[index].Provides = append(packages[index].Provides, AgentsProvidedChild{
						Kind: string(child.Kind), Name: child.Name, Status: status,
					})
					provided[providedKey] = true
				} else {
					for providedIndex := range packages[index].Provides {
						providedChild := &packages[index].Provides[providedIndex]
						if providedChild.Kind == string(child.Kind) && strings.EqualFold(providedChild.Name, child.Name) &&
							agentsPackageStatusRank(status) > agentsPackageStatusRank(providedChild.Status) {
							providedChild.Status = status
						}
					}
				}
				agentsRollPackageStatus(&packages[index], status)
				if status != AgentsPackageInstalled {
					packages[index].Issues = append(packages[index].Issues, fmt.Sprintf("%s %s is %s", strings.ToUpper(string(child.Kind)), child.Name, status))
				}
				if ambiguous {
					if len(owners) > 1 {
						packages[index].Issues = append(packages[index].Issues, fmt.Sprintf("%s %s has multiple owners: %s", strings.ToUpper(string(child.Kind)), child.Name, strings.Join(owners, ", ")))
					} else {
						packages[index].Issues = append(packages[index].Issues, fmt.Sprintf("%s %s has conflicting definitions in package %s", strings.ToUpper(string(child.Kind)), child.Name, child.Owner))
					}
					agentsRollPackageStatus(&packages[index], AgentsPackageDrifted)
				}
			}
		}
		if ambiguous {
			continue
		}
		for _, collision := range collisionsByKey[key] {
			for _, index := range packageIndexes[strings.ToLower(collision.Child.Owner)] {
				packages[index].Issues = append(packages[index].Issues, collision.Message)
				agentsRollPackageStatus(&packages[index], AgentsPackageDrifted)
			}
			if !collision.Exact {
				keepTopLevel[key] = true
				conflictDetail[key] = "conflicts with package " + collision.Child.Owner
			}
		}
	}

	agentsFinalizePackageRows(packages)

	return agentsPartitionOwnedRows(mcpRows, agentsChildMCP, childrenByKey, keepTopLevel, conflictDetail),
		agentsPartitionOwnedRows(lspRows, agentsChildLSP, childrenByKey, keepTopLevel, conflictDetail)
}

func agentsApplyUnavailableOwnership(packages []AgentsPackageRow, unavailable []string) {
	missing := make(map[string]bool, len(unavailable))
	for _, name := range unavailable {
		missing[strings.ToLower(name)] = true
	}
	for i := range packages {
		if !missing[strings.ToLower(packages[i].Name)] {
			continue
		}
		packages[i].Issues = append(packages[i].Issues, "package ownership evidence unavailable")
		agentsRollPackageStatus(&packages[i], AgentsPackageUnavailable)
	}
}

func agentsFinalizePackageRows(packages []AgentsPackageRow) {
	for i := range packages {
		sort.Slice(packages[i].Provides, func(left, right int) bool {
			if packages[i].Provides[left].Kind != packages[i].Provides[right].Kind {
				return packages[i].Provides[left].Kind < packages[i].Provides[right].Kind
			}
			return packages[i].Provides[left].Name < packages[i].Provides[right].Name
		})
		sort.Strings(packages[i].Issues)
		packages[i].Issues = slices.Compact(packages[i].Issues)
	}
	sort.SliceStable(packages, func(i, j int) bool {
		if left, right := agentsPackageStatusRank(packages[i].Status), agentsPackageStatusRank(packages[j].Status); left != right {
			return left < right
		}
		if packages[i].Name != packages[j].Name {
			return packages[i].Name < packages[j].Name
		}
		return packages[i].Source < packages[j].Source
	})
}

func agentsOwnedChildRuntimeStatus(child agentsOwnedChild, mcpInput, lspInput agentsServiceInput) AgentsPackageStatus {
	in := mcpInput
	decl := agentsServiceDecl{name: child.Name}
	if child.Kind == agentsChildMCP && child.MCP != nil {
		decl.detail, decl.command, decl.url = child.MCP.Transport, child.MCP.Command, child.MCP.URL
		decl.remote = child.MCP.URL != ""
	} else if child.Kind == agentsChildLSP && child.LSP != nil {
		in = lspInput
		decl.command = child.LSP.Command
	} else {
		return AgentsPackageUnavailable
	}
	lockedName, locked := agentsEqualFoldString(in.locked, child.Name)
	if !locked {
		return AgentsPackageMissing
	}
	cfg := agentsEqualFoldConfig(in.configs, lockedName)
	deployed := agentsEqualFoldHarnessConfig(in.configsOnClaude, lockedName)
	return agentsServiceJoinedStatus(decl, cfg, deployed, in.resolves)
}

func agentsEqualFoldString(values []string, name string) (string, bool) {
	for _, value := range values {
		if strings.EqualFold(value, name) {
			return value, true
		}
	}
	return "", false
}

func agentsEqualFoldConfig(values map[string]apmServiceConfig, name string) apmServiceConfig {
	if value, ok := values[name]; ok {
		return value
	}
	for key, value := range values {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return apmServiceConfig{}
}

func agentsEqualFoldHarnessConfig(values map[string]harnessMCPConfig, name string) map[string]harnessMCPConfig {
	for key, value := range values {
		if strings.EqualFold(key, name) {
			return map[string]harnessMCPConfig{name: value}
		}
	}
	return nil
}

func agentsRollPackageStatus(row *AgentsPackageRow, child AgentsPackageStatus) {
	if agentsPackageStatusRank(child) > agentsPackageStatusRank(row.Status) {
		row.Status = child
	}
}

func agentsPartitionOwnedRows(
	rows []AgentsServiceRow,
	kind agentsChildKind,
	children map[string][]agentsOwnedChild,
	keep map[string]bool,
	details map[string]string,
) []AgentsServiceRow {
	out := rows[:0]
	for _, row := range rows {
		key := agentsChildKey(kind, row.Name)
		if len(children[key]) == 0 {
			out = append(out, row)
			continue
		}
		if !keep[key] {
			continue
		}
		row.Status = AgentsPackageDrifted
		row.Detail = details[key]
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if left, right := agentsPackageStatusRank(out[i].Status), agentsPackageStatusRank(out[j].Status); left != right {
			return left < right
		}
		return out[i].Name < out[j].Name
	})
	return out
}
