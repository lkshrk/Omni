package config

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// LegacyAgentDecls holds a host's pre-migration agent declarations, keyed by source for packages and by name otherwise.
type LegacyAgentDecls struct {
	MCPServers   map[string]json.RawMessage
	Plugins      map[string]json.RawMessage
	Marketplaces map[string]json.RawMessage
	Packages     map[string]json.RawMessage
}

// LegacyAgentEvidence is the offline path evidence copied into an agents migration snapshot.
type LegacyAgentEvidence struct {
	SnapshotDir      string
	Paths            map[string]string // canonical original path -> snapshot-relative copy from paths.json
	MarketplacesJSON string
}

const snapshotConfigPrefix = "omni-config-"

const (
	maxSnapshotPaths         = 4096
	maxSnapshotFileBytes     = 1 << 20
	maxSnapshotManifestBytes = 16 << 20
)

type snapshotPayload struct {
	Agents struct {
		Packages     []json.RawMessage `json:"packages"`
		MCPServers   []json.RawMessage `json:"mcp_servers"`
		Marketplaces []json.RawMessage `json:"marketplaces"`
		Plugins      []json.RawMessage `json:"plugins"`
	} `json:"agents"`
	Groups []snapshotGroup     `json:"groups"`
	Hosts  map[string][]string `json:"hosts"`
}

type snapshotGroup struct {
	Name         string   `json:"name"`
	MCPServers   []string `json:"mcp_servers"`
	Plugins      []string `json:"plugins"`
	Marketplaces []string `json:"marketplaces"`
	Skills       []string `json:"skills"`
}

// LegacyAgentsFromSnapshot parses snapshot copies as raw JSON: never via Load (it rejects agents-bearing configs) and never resolving $include (it points at the live fragments).
func LegacyAgentsFromSnapshot(snapshotDir, host string) (LegacyAgentDecls, LegacyAgentEvidence, error) {
	var decls LegacyAgentDecls
	var evidence LegacyAgentEvidence
	root, err := filepath.Abs(snapshotDir)
	if err != nil {
		return decls, evidence, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return decls, evidence, fmt.Errorf("inspect snapshot root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return decls, evidence, fmt.Errorf("snapshot root must be a real directory")
	}
	var budget int64

	pathsRaw, err := readSnapshotFile(root, "paths.json", &budget)
	if err != nil {
		return decls, evidence, fmt.Errorf("read snapshot paths: %w", err)
	}
	var paths map[string]string
	if err := json.Unmarshal(pathsRaw, &paths); err != nil {
		return decls, evidence, fmt.Errorf("parse snapshot paths: %w", err)
	}
	if len(paths) > maxSnapshotPaths {
		return decls, evidence, fmt.Errorf("snapshot paths exceed limit %d", maxSnapshotPaths)
	}
	evidence = LegacyAgentEvidence{SnapshotDir: root, Paths: map[string]string{}}
	pathNames := slices.Sorted(maps.Keys(paths))
	for _, copied := range pathNames {
		original := paths[copied]
		if !cleanSnapshotRelative(copied) {
			return decls, evidence, fmt.Errorf("snapshot paths: invalid copied path key %q", copied)
		}
		if strings.ContainsAny(original, "\r\n\x00") {
			return decls, evidence, fmt.Errorf("snapshot paths: original path contains CR/LF/NUL")
		}
		canonical := canonicalEvidencePath(original)
		if previous, ok := evidence.Paths[canonical]; ok {
			return decls, evidence, fmt.Errorf("snapshot paths: canonical original %q is listed more than once (%q, %q)", canonical, previous, copied)
		}
		evidence.Paths[canonical] = filepath.Clean(copied)
		if filepath.Base(copied) == "marketplaces.json" {
			if evidence.MarketplacesJSON != "" {
				return decls, evidence, fmt.Errorf("snapshot paths: multiple marketplaces.json copies (%q, %q)", evidence.MarketplacesJSON, copied)
			}
			evidence.MarketplacesJSON = filepath.Clean(copied)
		}
	}

	names := make([]string, 0, len(paths))
	for name := range paths {
		if strings.HasPrefix(name, snapshotConfigPrefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return decls, evidence, fmt.Errorf("snapshot %s: no %s* config copies listed in paths.json", snapshotDir, snapshotConfigPrefix)
	}
	if len(names) > maxSnapshotPaths {
		return decls, evidence, fmt.Errorf("snapshot config files exceed limit %d", maxSnapshotPaths)
	}

	merged := snapshotPayload{Hosts: map[string][]string{}}
	for _, name := range names {
		raw, err := readSnapshotFile(root, name, &budget)
		if err != nil {
			return decls, evidence, fmt.Errorf("read snapshot %s: %w", name, err)
		}
		var payload snapshotPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return decls, evidence, fmt.Errorf("parse snapshot %s: %w", name, err)
		}
		merged.Agents.Packages = append(merged.Agents.Packages, payload.Agents.Packages...)
		merged.Agents.MCPServers = append(merged.Agents.MCPServers, payload.Agents.MCPServers...)
		merged.Agents.Marketplaces = append(merged.Agents.Marketplaces, payload.Agents.Marketplaces...)
		merged.Agents.Plugins = append(merged.Agents.Plugins, payload.Agents.Plugins...)
		merged.Groups = append(merged.Groups, payload.Groups...)
		for h, groups := range payload.Hosts {
			merged.Hosts[h] = groups
		}
	}

	if err := requireKnownHost(merged, snapshotDir, host); err != nil {
		return decls, evidence, err
	}

	byName, err := indexSnapshotEntries(merged, snapshotDir)
	if err != nil {
		return decls, evidence, err
	}

	decls = LegacyAgentDecls{
		MCPServers:   map[string]json.RawMessage{},
		Plugins:      map[string]json.RawMessage{},
		Marketplaces: map[string]json.RawMessage{},
		Packages:     map[string]json.RawMessage{},
	}
	active := append(slices.Clone(merged.Hosts[host]), host)
	for _, group := range merged.Groups {
		if !slices.Contains(active, group.Name) {
			continue
		}
		selections := []struct {
			kind  string
			names []string
			into  map[string]json.RawMessage
		}{
			{"mcp_servers", group.MCPServers, decls.MCPServers},
			{"plugins", group.Plugins, decls.Plugins},
			{"marketplaces", group.Marketplaces, decls.Marketplaces},
			{"skills", group.Skills, decls.Packages},
		}
		for _, sel := range selections {
			for _, name := range sel.names {
				def, ok := byName[sel.kind][name]
				if !ok {
					return LegacyAgentDecls{}, evidence, fmt.Errorf("snapshot %s: group %q references %s %q with no definition in the agents object", snapshotDir, group.Name, sel.kind, name)
				}
				sel.into[name] = def
			}
		}
	}
	return decls, evidence, nil
}

func cleanSnapshotRelative(path string) bool {
	if path == "" || path == "." || filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
		return false
	}
	clean := filepath.Clean(path)
	return clean == path && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func readSnapshotFile(root, rel string, budget *int64) ([]byte, error) {
	if !cleanSnapshotRelative(rel) {
		return nil, fmt.Errorf("invalid snapshot-relative path %q", rel)
	}
	path := filepath.Join(root, rel)
	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("snapshot path has symlink component %q", current)
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, fmt.Errorf("snapshot file %q must be stable and regular", rel)
	}
	if info.Size() > maxSnapshotFileBytes {
		return nil, fmt.Errorf("snapshot file %q exceeds %d bytes", rel, maxSnapshotFileBytes)
	}
	*budget += info.Size()
	if *budget > maxSnapshotManifestBytes {
		return nil, fmt.Errorf("snapshot manifest budget exceeds %d bytes", maxSnapshotManifestBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSnapshotFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotFileBytes {
		return nil, fmt.Errorf("snapshot file %q exceeds %d bytes", rel, maxSnapshotFileBytes)
	}
	return raw, nil
}

func canonicalEvidencePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

// An unrecognised host would otherwise select no groups and yield an empty template that uninstalls everything.
func requireKnownHost(payload snapshotPayload, snapshotDir, host string) error {
	if _, ok := payload.Hosts[host]; ok {
		return nil
	}
	if slices.ContainsFunc(payload.Groups, func(g snapshotGroup) bool { return g.Name == host }) {
		return nil
	}
	known := make([]string, 0, len(payload.Hosts))
	for name := range payload.Hosts {
		known = append(known, name)
	}
	sort.Strings(known)
	if len(known) == 0 {
		return fmt.Errorf("snapshot %s: unknown host %q; the snapshot declares no hosts", snapshotDir, host)
	}
	return fmt.Errorf("snapshot %s: unknown host %q; known hosts: %s", snapshotDir, host, strings.Join(known, ", "))
}

func indexSnapshotEntries(payload snapshotPayload, snapshotDir string) (map[string]map[string]json.RawMessage, error) {
	index := map[string]map[string]json.RawMessage{
		"mcp_servers":  {},
		"plugins":      {},
		"marketplaces": {},
		"skills":       {},
	}
	sets := []struct {
		kind    string
		entries []json.RawMessage
		key     string
	}{
		{"mcp_servers", payload.Agents.MCPServers, "name"},
		{"plugins", payload.Agents.Plugins, "name"},
		{"marketplaces", payload.Agents.Marketplaces, "name"},
		{"skills", payload.Agents.Packages, "source"},
	}
	for _, set := range sets {
		for _, entry := range set.entries {
			var keyed map[string]json.RawMessage
			if err := json.Unmarshal(entry, &keyed); err != nil {
				return nil, fmt.Errorf("snapshot %s: parse agents.%s entry: %w", snapshotDir, set.kind, err)
			}
			var id string
			if err := json.Unmarshal(keyed[set.key], &id); err != nil || id == "" {
				return nil, fmt.Errorf("snapshot %s: agents.%s entry has no %q", snapshotDir, set.kind, set.key)
			}
			if _, exists := index[set.kind][id]; exists {
				return nil, fmt.Errorf("snapshot %s: duplicate agents.%s definition %q", snapshotDir, set.kind, id)
			}
			index[set.kind][id] = entry
		}
	}
	return index, nil
}
