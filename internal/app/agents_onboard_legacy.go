package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const onboardSchemaVersion = 1

type OnboardSourcePreimage struct {
	ID                 string `json:"id"`
	AbsolutePath       string `json:"absolute_path"`
	Kind               string `json:"kind"`
	Size               int64  `json:"size"`
	Mode               uint32 `json:"mode"`
	ContentFingerprint string `json:"content_fingerprint"`
}

type OnboardCandidate struct {
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	Name               string          `json:"name"`
	SourceHandle       string          `json:"source_handle"`
	SourceTargets      []string        `json:"source_targets"`
	Payload            json.RawMessage `json:"payload"`
	ContentFingerprint string          `json:"content_fingerprint"`
	SourcePreimageIDs  []string        `json:"source_preimage_ids"`
	SecretBlocked      bool            `json:"secret_blocked,omitempty"`
	Dots               *OnboardDotsRef `json:"dots,omitempty"`
}

type OnboardInventory struct {
	SchemaVersion   int                     `json:"schema_version"`
	CandidateSetID  string                  `json:"candidate_set_id"`
	SourcePreimages []OnboardSourcePreimage `json:"source_preimages"`
	Candidates      []OnboardCandidate      `json:"candidates"`
}

type legacyDocument struct {
	Path string
	Raw  map[string]json.RawMessage
	Data []byte
	Info os.FileInfo
}

type LegacyInventory struct {
	Envelope    OnboardInventory
	PreimageSet string
	Documents   []string
	Pointers    map[string]string // candidate ID -> owning document JSON pointer
}

func ExtractLegacyCandidates(configPath string) (LegacyInventory, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return LegacyInventory{}, err
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		empty := OnboardInventory{SchemaVersion: onboardSchemaVersion, SourcePreimages: []OnboardSourcePreimage{}, Candidates: []OnboardCandidate{}}
		empty.CandidateSetID = canonicalDigest(map[string]any{"preimages": empty.SourcePreimages, "candidates": empty.Candidates})
		return LegacyInventory{Envelope: empty, PreimageSet: digest("empty-preimages"), Pointers: map[string]string{}}, nil
	}
	var docs []legacyDocument
	visited := map[string]bool{}
	active := map[string]bool{}
	if err := readLegacyDocument(abs, true, visited, active, &docs); err != nil {
		return LegacyInventory{}, err
	}
	envelope := OnboardInventory{SchemaVersion: onboardSchemaVersion, SourcePreimages: []OnboardSourcePreimage{}, Candidates: []OnboardCandidate{}}
	pointers := map[string]string{}
	for _, doc := range docs {
		preID := canonicalDigest(map[string]any{"path": doc.Path, "kind": "file"})[:24]
		envelope.SourcePreimages = append(envelope.SourcePreimages, OnboardSourcePreimage{ID: preID, AbsolutePath: doc.Path, Kind: "file", Size: doc.Info.Size(), Mode: uint32(doc.Info.Mode().Perm()), ContentFingerprint: digestBytes(doc.Data)})
		var agents map[string]json.RawMessage
		if len(doc.Raw["agents"]) > 0 && !isNull(doc.Raw["agents"]) {
			if err := json.Unmarshal(doc.Raw["agents"], &agents); err != nil {
				return LegacyInventory{}, fmt.Errorf("parse %s $.agents: %w", doc.Path, err)
			}
		}
		for _, collection := range []struct{ field, kind string }{{"packages", "package"}, {"skills", "skill"}, {"mcp_servers", "mcp"}, {"marketplaces", "marketplace"}, {"plugins", "plugin"}} {
			var entries []json.RawMessage
			if raw := agents[collection.field]; len(raw) > 0 && !isNull(raw) {
				if err := json.Unmarshal(raw, &entries); err != nil {
					return LegacyInventory{}, fmt.Errorf("parse %s $.agents.%s: %w", doc.Path, collection.field, err)
				}
			}
			for i, raw := range entries {
				pointer := fmt.Sprintf("/agents/%s/%d", collection.field, i)
				candidate, err := legacyCandidate(collection.kind, raw, doc.Path, pointer, preID, false)
				if err != nil {
					return LegacyInventory{}, err
				}
				envelope.Candidates = append(envelope.Candidates, candidate)
				pointers[candidate.ID] = doc.Path + "#" + pointer
			}
		}
		var ignore map[string][]string
		if raw := agents["ignore"]; len(raw) > 0 && !isNull(raw) {
			if err := json.Unmarshal(raw, &ignore); err != nil {
				return LegacyInventory{}, fmt.Errorf("parse %s $.agents.ignore: %w", doc.Path, err)
			}
		}
		for field, names := range ignore {
			kind := strings.TrimSuffix(field, "s")
			if field == "mcp_servers" {
				kind = "mcp"
			}
			for i, name := range names {
				raw, _ := json.Marshal(map[string]any{"name": name, "disposition": "excluded"})
				pointer := fmt.Sprintf("/agents/ignore/%s/%d", field, i)
				candidate, err := legacyCandidate(kind, raw, doc.Path, pointer, preID, true)
				if err != nil {
					return LegacyInventory{}, err
				}
				envelope.Candidates = append(envelope.Candidates, candidate)
				pointers[candidate.ID] = doc.Path + "#" + pointer
			}
		}
	}
	if err := appendLegacyConditionalCandidates(&envelope, pointers, docs); err != nil {
		return LegacyInventory{}, err
	}
	applyLegacyEffectiveResolution(&envelope, docs)
	sort.Slice(envelope.SourcePreimages, func(i, j int) bool {
		return envelope.SourcePreimages[i].ID < envelope.SourcePreimages[j].ID
	})
	sort.Slice(envelope.Candidates, func(i, j int) bool { return envelope.Candidates[i].ID < envelope.Candidates[j].ID })
	preimagesJSON, _ := json.Marshal(envelope.SourcePreimages)
	preimageSet := digestBytes(preimagesJSON)
	envelope.CandidateSetID = canonicalDigest(map[string]any{"preimages": envelope.SourcePreimages, "candidates": envelope.Candidates})
	paths := make([]string, 0, len(docs))
	for _, doc := range docs {
		paths = append(paths, doc.Path)
	}
	return LegacyInventory{Envelope: envelope, PreimageSet: preimageSet, Documents: paths, Pointers: pointers}, nil
}

type legacyEffectiveAgentSettings struct {
	agentsUse                                                    []string
	useSet                                                       bool
	agentsDisabled, skillsDisabled, mcpDisabled, pluginsDisabled bool
}

func applyLegacyEffectiveResolution(envelope *OnboardInventory, docs []legacyDocument) {
	hostname := currentHostname()
	hostKeys := map[string]bool{hostname: true, shortHostname(hostname): true}
	activeGroups := map[string]bool{currentMachineGroupName(): true, hostname: true, shortHostname(hostname): true}
	for _, doc := range docs {
		var hosts map[string][]string
		if json.Unmarshal(doc.Raw["hosts"], &hosts) == nil {
			for host, groups := range hosts {
				if hostKeys[host] {
					for _, group := range groups {
						activeGroups[group] = true
					}
				}
			}
		}
	}
	settings := legacyEffectiveAgentSettings{}
	applySettings := func(raw json.RawMessage) {
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return
		}
		if value, ok := values["agents_use"]; ok {
			settings.useSet = true
			_ = json.Unmarshal(value, &settings.agentsUse)
		}
		for key, dst := range map[string]*bool{"agents_disabled": &settings.agentsDisabled, "skills_disabled": &settings.skillsDisabled, "mcp_disabled": &settings.mcpDisabled, "plugins_disabled": &settings.pluginsDisabled} {
			if value, ok := values[key]; ok {
				_ = json.Unmarshal(value, dst)
			}
		}
	}
	for _, doc := range docs {
		applySettings(doc.Raw["settings"])
		var hosts map[string]json.RawMessage
		if json.Unmarshal(doc.Raw["host_settings"], &hosts) == nil {
			for host, raw := range hosts {
				if hostKeys[host] {
					applySettings(raw)
				}
			}
		}
	}
	grouped, activeRefs := map[string]map[string]bool{}, map[string]map[string]bool{}
	declarations := map[string][]string{"skill": {}, "mcp": {}, "plugin": {}, "marketplace": {}}
	for _, kind := range []string{"skill", "mcp", "plugin", "marketplace"} {
		grouped[kind], activeRefs[kind] = map[string]bool{}, map[string]bool{}
	}
	for _, candidate := range envelope.Candidates {
		kind, identity := candidate.Kind, candidate.Name
		if kind == "package" || kind == "skill" {
			kind = "skill"
			var payload map[string]any
			if json.Unmarshal(candidate.Payload, &payload) == nil {
				if source, _ := payload["source"].(string); source != "" {
					identity = source
				}
			}
		}
		if _, ok := declarations[kind]; ok && !slices.Contains(declarations[kind], identity) {
			declarations[kind] = append(declarations[kind], identity)
		}
	}
	macros := map[string]string{"@agents.packages": "skill", "@agents.mcp_servers": "mcp", "@agents.plugins": "plugin", "@agents.marketplaces": "marketplace"}
	for _, doc := range docs {
		var groups []map[string]any
		if json.Unmarshal(doc.Raw["groups"], &groups) != nil {
			continue
		}
		for _, group := range groups {
			name, _ := group["name"].(string)
			for field, kind := range map[string]string{"skills": "skill", "mcp_servers": "mcp", "plugins": "plugin", "marketplaces": "marketplace"} {
				for _, identity := range stringList(group[field]) {
					identities := []string{identity}
					if macroKind, ok := macros[identity]; ok {
						if macroKind != kind {
							continue
						}
						identities = declarations[kind]
					}
					for _, expanded := range identities {
						grouped[kind][expanded] = true
						if activeGroups[name] {
							activeRefs[kind][expanded] = true
						}
					}
				}
			}
		}
	}
	useTargets, _ := normalizeLegacyTargets(settings.agentsUse)
	for i := range envelope.Candidates {
		candidate := &envelope.Candidates[i]
		var payload map[string]any
		if json.Unmarshal(candidate.Payload, &payload) != nil {
			continue
		}
		identity := candidate.Name
		groupKind := candidate.Kind
		if candidate.Kind == "package" || candidate.Kind == "skill" {
			groupKind = "skill"
			if source, _ := payload["source"].(string); source != "" {
				identity = source
			}
		}
		if grouped[groupKind][identity] && !activeRefs[groupKind][identity] {
			payload["disposition"] = "excluded-inactive-group"
		}
		disabled := settings.agentsDisabled || ((candidate.Kind == "package" || candidate.Kind == "skill") && settings.skillsDisabled) || candidate.Kind == "mcp" && settings.mcpDisabled || (candidate.Kind == "plugin" || candidate.Kind == "marketplace") && settings.pluginsDisabled
		if disabled {
			payload["disposition"] = "excluded-disabled"
		}
		if settings.useSet && candidate.Kind != "marketplace" && candidate.Kind != "unsupported" {
			targets := useTargets
			if len(candidate.SourceTargets) > 0 {
				targets = nil
				for _, target := range candidate.SourceTargets {
					if slices.Contains(useTargets, target) {
						targets = append(targets, target)
					}
				}
			}
			candidate.SourceTargets = sortedUnique(targets)
			if len(candidate.SourceTargets) == 0 {
				if _, excluded := payload["disposition"]; !excluded {
					payload["disposition"] = "excluded-targets"
				}
			} else {
				delete(payload, "target_resolution_required")
			}
		}
		candidate.Payload, _ = json.Marshal(payload)
		candidate.ContentFingerprint = digestBytes(candidate.Payload)
	}
}

func readLegacyDocument(path string, root bool, visited, active map[string]bool, docs *[]legacyDocument) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve legacy config %q: %w", path, err)
	}
	if active[resolved] {
		return fmt.Errorf("config include cycle at %q", resolved)
	}
	if visited[resolved] {
		return nil
	}
	active[resolved] = true
	defer delete(active, resolved)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read legacy config %q: %w", resolved, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return fmt.Errorf("parse legacy config %q: %w", resolved, err)
	}
	if root {
		var version int
		if err := json.Unmarshal(raw["version"], &version); err != nil || (version != 22 && version != 23 && version != 24) {
			return fmt.Errorf("legacy onboarding supports config versions 22, 23, and v24 native-only planning")
		}
	}
	visited[resolved] = true
	*docs = append(*docs, legacyDocument{Path: resolved, Raw: raw, Data: data, Info: info})
	var includes []string
	if includeRaw := raw["$include"]; len(includeRaw) > 0 {
		if err := json.Unmarshal(includeRaw, &includes); err != nil {
			return fmt.Errorf("parse includes in %q: %w", resolved, err)
		}
	}
	for _, include := range includes {
		if strings.TrimSpace(include) == "" {
			return errors.New("legacy include path is empty")
		}
		child := include
		if !filepath.IsAbs(child) {
			child = filepath.Join(filepath.Dir(resolved), child)
		}
		if err := readLegacyDocument(child, false, visited, active, docs); err != nil {
			return err
		}
	}
	return nil
}

func legacyCandidate(kind string, raw json.RawMessage, sourcePath, pointer, preID string, excluded bool) (OnboardCandidate, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return OnboardCandidate{}, fmt.Errorf("legacy item %s#%s must be an object", sourcePath, pointer)
	}
	name := firstString(object, "name", "source")
	if name == "" {
		return OnboardCandidate{}, fmt.Errorf("legacy item %s#%s has no name/source", sourcePath, pointer)
	}
	name = safeCandidateName(name)
	if source, ok := object["source"].(string); ok && (strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")) {
		if abs, absErr := filepath.Abs(filepath.Join(filepath.Dir(sourcePath), source)); absErr == nil {
			object["source"] = abs
		}
	}
	rawTargets := stringList(object["agents"])
	targets, err := normalizeLegacyTargets(rawTargets)
	if err != nil {
		return OnboardCandidate{}, fmt.Errorf("legacy item %s#%s: %w", sourcePath, pointer, err)
	}
	if len(rawTargets) == 0 {
		object["target_resolution_required"] = true
	}
	delete(object, "agents")
	secretBlocked := false
	if kind == "mcp" {
		secretBlocked = normalizeLegacyMCP(object)
	}
	secretBlocked = sanitizeLegacyPayload(object, kind == "mcp") || secretBlocked
	if excluded {
		object["disposition"] = "excluded"
	}
	payload, err := json.Marshal(object)
	if err != nil {
		return OnboardCandidate{}, err
	}
	fingerprint := digestBytes(payload)
	handleRaw := digest("import-candidates-v1", "omni-legacy", kind, pointer)
	handle := base64.RawURLEncoding.EncodeToString([]byte(handleRaw))
	id := digest("import-candidates-v1", "omni-legacy", handle, strings.ToLower(name), kind)
	return OnboardCandidate{ID: id, Kind: kind, Name: name, SourceHandle: handle, SourceTargets: targets, Payload: payload, ContentFingerprint: fingerprint, SourcePreimageIDs: []string{preID}, SecretBlocked: secretBlocked}, nil
}

func appendLegacyConditionalCandidates(envelope *OnboardInventory, pointers map[string]string, docs []legacyDocument) error {
	active := map[string]bool{currentMachineGroupName(): true, shortHostname(currentHostname()): true}
	for _, doc := range docs {
		var hosts map[string][]string
		if json.Unmarshal(doc.Raw["hosts"], &hosts) == nil {
			for host, groups := range hosts {
				if host == currentMachineGroupName() || host == shortHostname(currentHostname()) {
					for _, group := range groups {
						active[group] = true
					}
				}
			}
		}
	}
	for _, doc := range docs {
		preID := canonicalDigest(map[string]any{"path": doc.Path, "kind": "file"})[:24]
		var groups []map[string]any
		if raw := doc.Raw["groups"]; len(raw) > 0 && !isNull(raw) {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return fmt.Errorf("parse legacy groups in %s: %w", doc.Path, err)
			}
		}
		for i, group := range groups {
			name, _ := group["name"].(string)
			if name == "" {
				continue
			}
			if !hasLegacyGroupFields(group) {
				continue
			}
			if active[name] {
				continue
			}
			payload, _ := json.Marshal(map[string]any{"name": name, "unsupported_reason": "conditional-group-host", "target_resolution_required": true, "group": group})
			pointer := fmt.Sprintf("/groups/%d", i)
			candidate, err := legacyCandidate("unsupported", payload, doc.Path, pointer, preID, false)
			if err != nil {
				return err
			}
			envelope.Candidates = append(envelope.Candidates, candidate)
			pointers[candidate.ID] = doc.Path + "#" + pointer
		}
		var hosts map[string]map[string]any
		if raw := doc.Raw["host_settings"]; len(raw) > 0 && !isNull(raw) {
			if err := json.Unmarshal(raw, &hosts); err != nil {
				return fmt.Errorf("parse legacy host settings in %s: %w", doc.Path, err)
			}
		}
		for host, value := range hosts {
			if host == shortHostname(currentHostname()) || !bytesContainLegacyAgents(mustJSON(value)) {
				continue
			}
			payload, _ := json.Marshal(map[string]any{"name": "host-" + host, "unsupported_reason": "conditional-group-host", "target_resolution_required": true, "host": host})
			pointer := "/host_settings/" + escapeJSONPointer(host)
			candidate, err := legacyCandidate("unsupported", payload, doc.Path, pointer, preID, false)
			if err != nil {
				return err
			}
			envelope.Candidates = append(envelope.Candidates, candidate)
			pointers[candidate.ID] = doc.Path + "#" + pointer
		}
	}
	return nil
}

func hasLegacyGroupFields(group map[string]any) bool {
	for _, key := range []string{"skills", "mcp_servers", "plugins", "marketplaces"} {
		if values, ok := group[key].([]any); ok && len(values) > 0 {
			return true
		}
	}
	return false
}
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
func escapeJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func sanitizeLegacyPayload(object map[string]any, mcp bool) bool {
	blocked := false
	for key, value := range object {
		lower := strings.ToLower(key)
		if mcp && slices.Contains([]string{"auth", "authorization"}, lower) {
			object[key], blocked = map[string]string{"blocked": "literal-secret"}, true
			continue
		}
		if child, ok := value.(map[string]any); ok && sanitizeLegacyPayload(child, mcp) {
			blocked = true
			continue
		}
		if text, ok := value.(string); ok {
			if parsed, err := url.Parse(text); err == nil {
				querySecret := false
				for name, values := range parsed.Query() {
					lowerName := strings.ToLower(name)
					if len(values) > 0 && (strings.Contains(lowerName, "token") || strings.Contains(lowerName, "password") || strings.Contains(lowerName, "secret") || strings.Contains(lowerName, "auth") || strings.Contains(lowerName, "key")) {
						querySecret = true
						break
					}
				}
				if parsed.User != nil || querySecret {
					object[key], blocked = map[string]string{"blocked": "literal-secret"}, true
					continue
				}
			}
			if mcp && (strings.Contains(strings.ToLower(text), "bearer ") || strings.Contains(strings.ToLower(text), "token=") || strings.Contains(strings.ToLower(text), "password=")) {
				object[key], blocked = map[string]string{"blocked": "literal-secret"}, true
			}
		}
	}
	return blocked
}

func normalizeLegacyMCP(object map[string]any) bool {
	blocked := false
	env := map[string]any{}
	if names, ok := object["env"].([]any); ok {
		for _, raw := range names {
			if name, ok := raw.(string); ok && validOnboardEnvName(name) {
				env[name] = "${" + name + "}"
			}
		}
	}
	if literals, ok := object["env_literal"].(map[string]any); ok {
		for name := range literals {
			env[name] = map[string]string{"blocked": "literal-secret"}
			blocked = true
		}
	}
	if len(env) > 0 {
		object["env"] = env
	} else {
		delete(object, "env")
	}
	delete(object, "env_literal")
	if headers, ok := object["headers"].(map[string]any); ok {
		for name, value := range headers {
			if text, ok := value.(string); ok && strings.HasPrefix(text, "${") {
				continue
			}
			headers[name] = map[string]string{"blocked": "literal-secret"}
			blocked = true
		}
	}
	return blocked
}

func validOnboardEnvName(name string) bool {
	if name == "" || !((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z') || name[0] == '_') {
		return false
	}
	for i := 1; i < len(name); i++ {
		b := name[i]
		if !((b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_') {
			return false
		}
	}
	return true
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func safeCandidateName(name string) string {
	parsed, err := url.Parse(name)
	if err == nil && parsed.User != nil {
		parsed.User = nil
		return parsed.String()
	}
	return name
}
func stringList(value any) []string {
	values, _ := value.([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	sort.Strings(out)
	return out
}
func bytesContainLegacyAgents(raw []byte) bool {
	s := string(raw)
	for _, key := range []string{"skills", "mcp_servers", "plugins", "marketplaces", "agents", "agents_use", "agents_disabled", "skills_disabled", "mcp_disabled", "plugins_disabled"} {
		if strings.Contains(s, `"`+key+`"`) {
			return true
		}
	}
	return false
}
func isNull(raw []byte) bool         { return strings.TrimSpace(string(raw)) == "null" }
func digestBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func digest(parts ...string) string  { return digestBytes([]byte(strings.Join(parts, "\x00"))) }

func canonicalDigest(value any) string {
	raw, _ := json.Marshal(value)
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&canonical)
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(canonical)
	return digestBytes(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}))
}

func normalizeLegacyTargets(targets []string) ([]string, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		switch target {
		case "claude", "claude-code":
			target = "claude"
		}
		if !slices.Contains(out, target) {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out, nil
}
