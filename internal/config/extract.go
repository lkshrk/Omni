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

// Order matters: dots.json must merge after groups.json so its dots-only projections land in the groups declared there.
var extractFragments = []string{
	"settings.d/agents.json",
	"settings.d/tools.json",
	"settings.d/groups.json",
	"settings.d/dots.json",
}

type ExtractReport struct {
	// Moved keys are fragment paths relative to the config directory.
	Moved     map[string]string
	Unchanged bool
}

// ExtractIncludeFragments — Idempotent: duplicate definitions across parent and fragments collapse to the effective merged value, so this also repairs divergent copies.
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
		// Strip against the shape PatchRaw will write: it migrates on the way out, and a migration introducing "agents" would leave the key behind.
		if err := migrateRawVersion(mainRaw); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading config %q: %w", writePath, err)
	}

	groupsSansDots, dotsOnly := splitGroupsAndDots(cfg.Groups)
	fragmentValues := map[string]any{
		"settings.d/tools.json":  nil,
		"settings.d/groups.json": nil,
		"settings.d/dots.json":   nil,
	}
	fragmentKeys := map[string]string{
		"settings.d/tools.json":  "tools",
		"settings.d/groups.json": "groups",
		"settings.d/dots.json":   "groups",
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

	// Canonical fragments go last so they win merges; foreign includes keep their place.
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
