package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
)

type OnboardResolution struct {
	Decision        string            `json:"decision"`
	ApprovedTargets []string          `json:"approved_targets,omitempty"`
	EnvBindings     map[string]string `json:"env_bindings,omitempty"`
}

type OnboardItem struct {
	ID                 string            `json:"id"`
	Kind               string            `json:"kind"`
	Name               string            `json:"name"`
	Source             string            `json:"source"`
	ProposedTargets    []string          `json:"proposed_targets,omitempty"`
	TargetOptions      []string          `json:"target_options,omitempty"`
	Blockers           []string          `json:"blockers,omitempty"`
	Payload            json.RawMessage   `json:"payload,omitempty"`
	ContentFingerprint string            `json:"content_fingerprint"`
	Dots               *OnboardDotsRef   `json:"dots,omitempty"`
	Resolution         OnboardResolution `json:"resolution"`
}

type OnboardMarketplace struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type OnboardPlan struct {
	SchemaVersion    int                     `json:"schema_version"`
	PlanID           string                  `json:"plan_id"`
	ResolutionID     string                  `json:"resolution_id"`
	OperationID      string                  `json:"operation_id"`
	CreatedAt        string                  `json:"created_at"`
	CandidateSetID   string                  `json:"candidate_set_id"`
	PreimageSet      string                  `json:"preimage_set"`
	SourcePreimages  []OnboardSourcePreimage `json:"source_preimages"`
	Items            []OnboardItem           `json:"items"`
	Marketplaces     []OnboardMarketplace    `json:"marketplaces,omitempty"`
	ProposedManifest string                  `json:"-"`
	Warnings         []string                `json:"warnings,omitempty"`
	Blockers         []string                `json:"blockers,omitempty"`
}

func bindOnboardPlan(plan *OnboardPlan) error {
	if plan == nil {
		return errors.New("onboarding plan is required")
	}
	immutable := *plan
	immutable.Items = append([]OnboardItem(nil), plan.Items...)
	immutable.PlanID, immutable.ResolutionID, immutable.OperationID, immutable.CreatedAt = "", "", "", ""
	immutable.ProposedManifest = ""
	for i := range immutable.Items {
		if !hexID(immutable.Items[i].ID, 64) {
			return fmt.Errorf("invalid onboarding item ID %q", immutable.Items[i].ID)
		}
		immutable.Items[i].Resolution = OnboardResolution{}
	}
	planID := canonicalDigest(immutable)
	if plan.PlanID != "" && plan.PlanID != planID {
		return errors.New("onboarding plan immutable fields do not match plan_id")
	}
	plan.PlanID = planID
	resolutions := make([]OnboardResolution, len(plan.Items))
	for i := range plan.Items {
		plan.Items[i].Resolution.ApprovedTargets = sortedUnique(plan.Items[i].Resolution.ApprovedTargets)
		resolutions[i] = plan.Items[i].Resolution
	}
	plan.ResolutionID = canonicalDigest(map[string]any{"plan_id": plan.PlanID, "resolutions": resolutions})
	plan.OperationID = plan.ResolutionID[:32]
	return nil
}

func buildOnboardManifest(existing []byte, items []OnboardItem) ([]byte, []OnboardMarketplace, []string, error) {
	doc, original, err := parseOnboardManifest(existing)
	if err != nil {
		return nil, nil, nil, err
	}
	root := doc.Content[0]
	if err := validateUniqueYAMLMappingKeys(root); err != nil {
		return nil, nil, []string{err.Error()}, nil
	}
	if current := yamlMapValue(root, "dependencies"); current != nil && current.Kind != yaml.MappingNode {
		return nil, nil, []string{"ambiguous-existing-dependencies"}, nil
	}
	deps := ensureYAMLMapping(root, "dependencies")
	for _, key := range []string{"apm", "mcp"} {
		if current := yamlMapValue(deps, key); current != nil && current.Kind != yaml.SequenceNode {
			return nil, nil, []string{"ambiguous-existing-dependencies." + key}, nil
		}
	}
	apmSeq := ensureYAMLSequence(deps, "apm")
	mcpSeq := ensureYAMLSequence(deps, "mcp")
	if err := validateYAMLDependencyIdentities(apmSeq, "apm"); err != nil {
		return nil, nil, []string{err.Error()}, nil
	}
	if err := validateYAMLDependencyIdentities(mcpSeq, "mcp"); err != nil {
		return nil, nil, []string{err.Error()}, nil
	}
	var markets []OnboardMarketplace
	var blockers []string
	changed := false
	for _, item := range items {
		if item.Resolution.Decision != "migrate" && item.Resolution.Decision != "move-to-apm" && item.Resolution.Decision != "map-secret" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			blockers = append(blockers, item.ID+":invalid-payload")
			continue
		}
		switch item.Kind {
		case "marketplace":
			name, _ := payload["name"].(string)
			source, _ := payload["source"].(string)
			if name == "" || source == "" {
				blockers = append(blockers, item.ID+":invalid-marketplace")
				continue
			}
			markets = append(markets, OnboardMarketplace{Name: name, Source: source})
		case "mcp":
			entry, convertErr := onboardMCPEntry(payload, item.Resolution)
			if convertErr != nil {
				blockers = append(blockers, item.ID+":"+convertErr.Error())
				continue
			}
			changedNow, mergeErr := mergeYAMLSequence(mcpSeq, "mcp", entry)
			if mergeErr != nil {
				blockers = append(blockers, item.ID+":"+mergeErr.Error())
			}
			changed = changed || changedNow
		case "unsupported":
			blockers = append(blockers, item.ID+":unsupported")
		default:
			entry, convertErr := onboardAPMEntry(item, payload)
			if convertErr != nil {
				blockers = append(blockers, item.ID+":"+convertErr.Error())
				continue
			}
			changedNow, mergeErr := mergeYAMLSequence(apmSeq, "apm", entry)
			if mergeErr != nil {
				blockers = append(blockers, item.ID+":"+mergeErr.Error())
			}
			changed = changed || changedNow
		}
	}
	if len(blockers) > 0 {
		return nil, markets, blockers, nil
	}
	if !changed {
		return original, markets, nil, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, nil, nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, nil, nil, err
	}
	return out.Bytes(), markets, nil, nil
}

func onboardManifestHasOmniImports(data []byte) bool {
	var manifest struct {
		Dependencies struct {
			APM []any `yaml:"apm"`
		} `yaml:"dependencies"`
	}
	if yaml.Unmarshal(data, &manifest) != nil {
		return false
	}
	for _, raw := range manifest.Dependencies.APM {
		path, _ := normalizeYAMLMap(raw)["path"].(string)
		if filepath.Base(filepath.Dir(filepath.Clean(path))) == "omni-imports" {
			return true
		}
	}
	return false
}

func parseOnboardManifest(data []byte) (yaml.Node, []byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("name: omni-migrated\nversion: 1.0.0\ndependencies:\n  apm: []\n  mcp: []\n")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return yaml.Node{}, nil, fmt.Errorf("parse APM manifest: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return yaml.Node{}, nil, errors.New("APM manifest root must be a mapping")
	}
	return doc, append([]byte(nil), data...), nil
}

func ensureYAMLMapping(parent *yaml.Node, key string) *yaml.Node {
	if node := yamlMapValue(parent, key); node != nil {
		return node
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, scalarNode(key), node)
	return node
}

func ensureYAMLSequence(parent *yaml.Node, key string) *yaml.Node {
	if node := yamlMapValue(parent, key); node != nil {
		return node
	}
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	parent.Content = append(parent.Content, scalarNode(key), node)
	return node
}

func yamlMapValue(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

func validateUniqueYAMLMappingKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if seen[key] {
				return fmt.Errorf("ambiguous duplicate YAML key %q", key)
			}
			seen[key] = true
		}
	}
	for _, child := range node.Content {
		if err := validateUniqueYAMLMappingKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func validateYAMLDependencyIdentities(seq *yaml.Node, kind string) error {
	seen := map[string]bool{}
	for _, current := range seq.Content {
		var decoded any
		if err := current.Decode(&decoded); err != nil {
			return errors.New("ambiguous-existing-dependency")
		}
		identity := onboardDependencyIdentity(kind, normalizeYAMLMap(decoded))
		if seen[identity] {
			return fmt.Errorf("ambiguous duplicate dependency identity %s", identity)
		}
		seen[identity] = true
	}
	return nil
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func mergeYAMLSequence(seq *yaml.Node, kind string, value map[string]any) (bool, error) {
	incoming, err := anyYAMLNode(value)
	if err != nil {
		return false, err
	}
	wanted := onboardDependencyIdentity(kind, value)
	for _, current := range seq.Content {
		var decoded any
		if err := current.Decode(&decoded); err != nil {
			return false, errors.New("ambiguous-existing-dependency")
		}
		identity := onboardDependencyIdentity(kind, normalizeYAMLMap(decoded))
		if identity != wanted {
			continue
		}
		var left, right any
		_ = current.Decode(&left)
		_ = incoming.Decode(&right)
		if canonicalDigest(normalizeYAMLMap(left)) == canonicalDigest(normalizeYAMLMap(right)) {
			return false, nil
		}
		return false, errors.New("dependency-conflict")
	}
	seq.Content = append(seq.Content, incoming)
	return true, nil
}

func anyYAMLNode(value any) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

func normalizeYAMLMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	if s, ok := value.(string); ok {
		return map[string]any{"git": s}
	}
	return map[string]any{"invalid": fmt.Sprint(value)}
}

func onboardDependencyIdentity(kind string, entry map[string]any) string {
	if kind == "mcp" {
		return "mcp:" + fmt.Sprint(entry["name"])
	}
	if entry["marketplace"] != nil && entry["name"] != nil {
		return "marketplace:" + fmt.Sprint(entry["marketplace"]) + ":" + fmt.Sprint(entry["name"])
	}
	for _, key := range []string{"git", "path"} {
		if entry[key] != nil {
			return key + ":" + fmt.Sprint(entry[key])
		}
	}
	return "unknown:" + canonicalDigest(entry)
}

func onboardAPMEntry(item OnboardItem, payload map[string]any) (map[string]any, error) {
	if item.Dots != nil {
		path, err := durableOnboardPackagePath(item.ID)
		if err != nil {
			return nil, err
		}
		entry := map[string]any{"path": path}
		if len(item.Resolution.ApprovedTargets) > 0 {
			entry["targets"] = item.Resolution.ApprovedTargets
		}
		return entry, nil
	}
	if item.Kind == "plugin" {
		if marketplace, _ := payload["marketplace"].(string); marketplace != "" {
			return map[string]any{"name": item.Name, "marketplace": marketplace, "targets": item.Resolution.ApprovedTargets}, nil
		}
	}
	source, _ := payload["source"].(string)
	if source == "" {
		return nil, errors.New("missing-source")
	}
	entry := map[string]any{}
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".") || strings.HasPrefix(source, "~") || strings.HasPrefix(source, "file://") {
		abs, err := resolveOnboardLocalSource(source)
		if err != nil {
			return nil, err
		}
		entry["path"] = abs
	} else {
		entry["git"] = source
	}
	for _, key := range []string{"ref", "skills"} {
		if value := payload[key]; value != nil && fmt.Sprint(value) != "" {
			entry[key] = value
		}
	}
	if len(item.Resolution.ApprovedTargets) > 0 {
		entry["targets"] = item.Resolution.ApprovedTargets
	}
	return entry, nil
}

func resolveOnboardLocalSource(source string) (string, error) {
	if strings.HasPrefix(source, "file://~/") {
		source = "~/" + strings.TrimPrefix(source, "file://~/")
	} else if strings.HasPrefix(source, "file://") {
		parsed, err := url.Parse(source)
		if err != nil {
			return "", err
		}
		if parsed.Host != "" && parsed.Host != "localhost" {
			return "", errors.New("file dependency must be local")
		}
		decoded, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return "", err
		}
		source = decoded
	}
	if source == "~" || strings.HasPrefix(source, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if source == "~" {
			return filepath.Clean(home), nil
		}
		return filepath.Clean(filepath.Join(home, strings.TrimPrefix(source, "~/"))), nil
	}
	return filepath.Abs(source)
}

func durableOnboardPackagePath(itemID string) (string, error) {
	workspace, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(workspace, "omni-imports", itemID), nil
}

func validateStagedOnboardPackage(pkg string, item OnboardItem) error {
	switch item.Kind {
	case "package":
		if info, err := os.Lstat(filepath.Join(pkg, "apm.yml")); err != nil || !info.Mode().IsRegular() {
			return errors.New("staged package lost apm.yml")
		}
	case "plugin":
		if !regularOnboardFile(filepath.Join(pkg, "plugin.json")) && !regularOnboardFile(filepath.Join(pkg, ".claude-plugin", "plugin.json")) {
			return errors.New("staged plugin has no APM-supported plugin marker")
		}
	}
	deployable := false
	err := filepath.WalkDir(pkg, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pkg, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if stagedOnboardDeployablePath(item.Kind, rel) {
			deployable = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if item.Kind == "package" && !deployable {
		data, err := os.ReadFile(filepath.Join(pkg, "apm.yml"))
		if err != nil {
			return err
		}
		var manifest struct {
			Dependencies map[string][]any `yaml:"dependencies"`
		}
		if yaml.Unmarshal(data, &manifest) == nil {
			for _, deps := range manifest.Dependencies {
				if len(deps) > 0 {
					deployable = true
				}
			}
		}
	}
	if !deployable {
		return fmt.Errorf("staged %s %q has zero deployable APM primitives", item.Kind, item.Name)
	}
	return nil
}

func regularOnboardFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func stagedOnboardDeployablePath(kind, rel string) bool {
	lower := strings.ToLower(rel)
	if lower == "apm.yml" || lower == "plugin.json" || lower == ".claude-plugin/plugin.json" || lower == ".codex-plugin/plugin.json" {
		return false
	}
	if kind == "plugin" {
		return (strings.HasPrefix(lower, "skills/") && strings.HasSuffix(lower, "/skill.md")) ||
			(strings.HasPrefix(lower, "agents/") && strings.HasSuffix(lower, ".md")) ||
			(strings.HasPrefix(lower, "commands/") && strings.HasSuffix(lower, ".md")) ||
			(strings.HasPrefix(lower, "hooks/") && strings.HasSuffix(lower, ".json")) || lower == ".mcp.json"
	}
	return lower == "skill.md" || strings.HasSuffix(lower, ".agent.md") || strings.HasSuffix(lower, ".prompt.md") ||
		(strings.HasPrefix(lower, ".apm/skills/") && strings.HasSuffix(lower, "/skill.md")) ||
		(strings.HasPrefix(lower, ".apm/agents/") && strings.HasSuffix(lower, ".md")) ||
		(strings.HasPrefix(lower, ".apm/prompts/") && strings.HasSuffix(lower, ".prompt.md")) ||
		(strings.HasPrefix(lower, ".apm/hooks/") && strings.HasSuffix(lower, ".json")) ||
		(strings.HasPrefix(lower, "hooks/") && strings.HasSuffix(lower, ".json")) ||
		strings.HasPrefix(lower, ".apm/instructions/") || strings.HasPrefix(lower, ".apm/contexts/")
}

func onboardMCPEntry(payload map[string]any, resolution OnboardResolution) (map[string]any, error) {
	name, _ := payload["name"].(string)
	transport, _ := payload["transport"].(string)
	if name == "" || transport == "" {
		return nil, errors.New("invalid-mcp")
	}
	entry := map[string]any{"name": name, "registry": false, "transport": transport}
	if len(resolution.ApprovedTargets) > 0 {
		entry["targets"] = resolution.ApprovedTargets
	}
	if transport == "stdio" {
		command, _ := payload["command"].(string)
		parts, err := splitOnboardCommand(command)
		if err != nil || len(parts) == 0 {
			return nil, errors.New("invalid-command")
		}
		entry["command"] = parts[0]
		if len(parts) > 1 {
			entry["args"] = parts[1:]
		}
	} else if u, _ := payload["url"].(string); u != "" {
		entry["url"] = u
	} else {
		return nil, errors.New("missing-url")
	}
	for _, key := range []string{"env", "headers"} {
		if value, ok := payload[key].(map[string]any); ok {
			clean := map[string]any{}
			for field, raw := range value {
				if blocked, ok := raw.(map[string]any); ok && blocked["blocked"] != nil {
					envName := resolution.EnvBindings[field]
					if envName == "" {
						return nil, errors.New("secret-mapping-required")
					}
					clean[field] = "${env:" + envName + "}"
				} else {
					if text, ok := raw.(string); ok {
						clean[field] = normalizeOnboardPlaceholderString(text)
					} else {
						clean[field] = raw
					}
				}
			}
			if len(clean) > 0 {
				entry[key] = clean
			}
		}
	}
	return entry, nil
}

func normalizeOnboardPlaceholderString(value string) string {
	for start := strings.Index(value, "${"); start >= 0; {
		end := strings.Index(value[start+2:], "}")
		if end < 0 {
			break
		}
		end += start + 2
		name := value[start+2 : end]
		name = strings.TrimPrefix(name, "env:")
		if validOnboardEnvName(name) {
			value = value[:start] + "${env:" + name + "}" + value[end+1:]
			start += len("${env:" + name + "}")
		} else {
			start = end + 1
		}
		if start >= len(value) {
			break
		}
		next := strings.Index(value[start:], "${")
		if next < 0 {
			break
		}
		start += next
	}
	return value
}

func splitOnboardCommand(command string) ([]string, error) {
	var out []string
	var token strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if token.Len() > 0 {
			out = append(out, token.String())
			token.Reset()
		}
	}
	for _, r := range command {
		if escaped {
			token.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' {
			flush()
			continue
		}
		token.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated command quoting")
	}
	flush()
	return out, nil
}

func atomicWriteOnboardFile(path string, data []byte, mode os.FileMode) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".omni-onboard-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove staged onboarding file: %w", err))
		}
	}()
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func sortedMarketplaces(values []OnboardMarketplace) []OnboardMarketplace {
	out := append([]OnboardMarketplace(nil), values...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Source < out[j].Source
		}
		return out[i].Name < out[j].Name
	})
	return out
}
