package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Canonical settings.d fragment layout produced by ExtractIncludeFragments.
// Order matters: fragments merge after the parent in $include order, and
// dots.json must merge after groups.json so its dots-only group projections
// land in the groups declared there.
var extractFragments = []string{
	"settings.d/agents.json",
	"settings.d/tools.json",
	"settings.d/groups.json",
	"settings.d/dots.json",
}

// ExtractReport describes what ExtractIncludeFragments moved.
type ExtractReport struct {
	// Moved maps fragment path (relative to the config dir) to the top-level
	// keys or projections now owned by it.
	Moved map[string]string
	// Unchanged is true when the config already matched the extracted layout.
	Unchanged bool
}

// ExtractIncludeFragments decomposes the effective config into the canonical
// settings.d fragment layout: agents.json (agents), tools.json (tools),
// groups.json (groups without dots), dots.json (dots-only group projections).
// The moved keys are removed from the main settings.json and $include is
// updated. Duplicate definitions across parent and fragments collapse to the
// effective merged value, so running this also repairs divergent copies.
// The function is idempotent.
func ExtractIncludeFragments(path string) (*ExtractReport, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	writePath := path
	if resolved, err := resolveConfigWritePath(path); err == nil {
		writePath = resolved
	}
	baseDir := filepath.Dir(writePath)

	mainRaw := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(writePath); err == nil {
		if err := unmarshalJSONObject(data, &mainRaw); err != nil {
			return nil, fmt.Errorf("parsing config %q: %w", writePath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading config %q: %w", writePath, err)
	}

	groupsSansDots, dotsOnly := splitGroupsAndDots(cfg.Groups)
	fragmentValues := map[string]any{
		"settings.d/agents.json": nil,
		"settings.d/tools.json":  nil,
		"settings.d/groups.json": nil,
		"settings.d/dots.json":   nil,
	}
	fragmentKeys := map[string]string{
		"settings.d/agents.json": "agents",
		"settings.d/tools.json":  "tools",
		"settings.d/groups.json": "groups",
		"settings.d/dots.json":   "groups",
	}
	if !agentsConfigEmpty(&cfg.Agents) {
		fragmentValues["settings.d/agents.json"] = cfg.Agents
	}
	if len(cfg.Tools) > 0 {
		fragmentValues["settings.d/tools.json"] = cfg.Tools
	}
	if len(groupsSansDots) > 0 {
		fragmentValues["settings.d/groups.json"] = groupsSansDots
	}
	if len(dotsOnly) > 0 {
		fragmentValues["settings.d/dots.json"] = dotsOnly
	}

	report := &ExtractReport{Moved: make(map[string]string)}
	for _, fragment := range extractFragments {
		value := fragmentValues[fragment]
		if value == nil {
			continue
		}
		fragmentPath := filepath.Join(baseDir, filepath.FromSlash(fragment))
		if resolved, err := resolveConfigWritePath(fragmentPath); err == nil {
			fragmentPath = resolved
		}
		if err := os.MkdirAll(filepath.Dir(fragmentPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating fragment dir: %w", err)
		}
		valueRaw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", fragment, err)
		}
		existingRaw := make(map[string]json.RawMessage)
		var existingData []byte
		if data, err := os.ReadFile(fragmentPath); err == nil {
			existingData = data
			if err := unmarshalJSONObject(data, &existingRaw); err != nil {
				return nil, fmt.Errorf("parsing fragment %q: %w", fragmentPath, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reading fragment %q: %w", fragmentPath, err)
		}
		rendered := renderFragmentRaw(existingRaw, map[string]json.RawMessage{
			fragmentKeys[fragment]: valueRaw,
		})
		if bytes.Equal(existingData, rendered) {
			continue
		}
		if err := atomicWrite(fragmentPath, rendered); err != nil {
			return nil, err
		}
		report.Moved[fragment] = fragmentKeys[fragment]
	}

	// Rebuild $include: keep foreign includes in place, then the canonical
	// fragments (that exist) in canonical order at the end so they win merges.
	var includes []string
	if includeRaw := mainRaw["$include"]; len(includeRaw) > 0 {
		if err := json.Unmarshal(includeRaw, &includes); err != nil {
			return nil, fmt.Errorf("parsing config includes: %w", err)
		}
	}
	var newIncludes []string
	for _, include := range includes {
		normalized := filepath.ToSlash(strings.TrimSpace(include))
		if normalized == "" || slices.Contains(extractFragments, normalized) {
			continue
		}
		newIncludes = append(newIncludes, include)
	}
	for _, fragment := range extractFragments {
		fragmentPath := filepath.Join(baseDir, filepath.FromSlash(fragment))
		if _, err := os.Lstat(fragmentPath); err == nil {
			newIncludes = append(newIncludes, fragment)
		}
	}

	patch := make(map[string]json.RawMessage)
	includesRaw, err := json.Marshal(newIncludes)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(includes, newIncludes) {
		patch["$include"] = includesRaw
	}
	for _, key := range []string{"agents", "tools", "groups"} {
		if _, ok := mainRaw[key]; ok {
			patch[key] = json.RawMessage("null")
		}
	}
	if len(patch) == 0 && len(report.Moved) == 0 {
		report.Unchanged = true
		return report, nil
	}
	if len(patch) > 0 {
		if err := PatchRaw(writePath, patch); err != nil {
			return nil, err
		}
	}
	return report, nil
}

// splitGroupsAndDots projects groups into a dots-free copy plus dots-only
// counterparts for groups that carry dot entries.
func splitGroupsAndDots(groups []*GroupConfig) (sansDots, dotsOnly []*GroupConfig) {
	for _, group := range groups {
		if group == nil {
			continue
		}
		copied := *group
		copied.Dots = nil
		sansDots = append(sansDots, &copied)
		if len(group.Dots) == 0 {
			continue
		}
		dotsOnly = append(dotsOnly, &GroupConfig{
			Name: group.Name,
			Dots: group.Dots,
		})
	}
	return sansDots, dotsOnly
}

func agentsConfigEmpty(agents *AgentsConfig) bool {
	if agents == nil {
		return true
	}
	return len(agents.Packages) == 0 &&
		len(agents.McpServers) == 0 &&
		len(agents.Marketplaces) == 0 &&
		len(agents.Plugins) == 0 &&
		len(agents.Ignore.Skills) == 0 &&
		len(agents.Ignore.McpServers) == 0 &&
		len(agents.Ignore.Plugins) == 0 &&
		len(agents.Ignore.Marketplaces) == 0
}
