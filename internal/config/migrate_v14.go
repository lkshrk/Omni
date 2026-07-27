package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Only ".agents/skills" is machine-managed under ".agents"; the lockfile beside it stays a trackable user dotfile.
func isAgentConfigDotPath(path string) bool {
	trimmed := strings.TrimPrefix(path, "~/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return false
	}
	for _, dir := range agentDotsManagedPaths {
		if trimmed == dir || strings.HasPrefix(trimmed, dir+"/") {
			return true
		}
	}
	return false
}

func dropAgentConfigDots(dots []DotEntry) []DotEntry {
	if len(dots) == 0 {
		return dots
	}
	out := dots[:0:0]
	for _, d := range dots {
		if isAgentConfigDotPath(d.Path) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// Drops config-tracked dotfile entries under an agent config dir; files on disk are never touched.
func migrateConfigV13ToV14(cfg *RootConfig) error {
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		g.Dots = dropAgentConfigDots(g.Dots)
	}
	cfg.Version = 14
	return nil
}

func migrateRawConfigV13ToV14(raw map[string]json.RawMessage) error {
	groupsRaw, ok := raw["groups"]
	if ok && !isJSONNull(groupsRaw) {
		var groups []map[string]json.RawMessage
		if err := json.Unmarshal(groupsRaw, &groups); err != nil {
			return fmt.Errorf("parsing groups: %w", err)
		}
		for i, group := range groups {
			dotsRaw, ok := group["dots"]
			if !ok || isJSONNull(dotsRaw) {
				continue
			}
			var dots []DotEntry
			if err := json.Unmarshal(dotsRaw, &dots); err != nil {
				return fmt.Errorf("parsing groups[%d].dots: %w", i, err)
			}
			filtered, err := json.Marshal(dropAgentConfigDots(dots))
			if err != nil {
				return fmt.Errorf("encoding groups[%d].dots: %w", i, err)
			}
			group["dots"] = filtered
		}
		out, err := json.Marshal(groups)
		if err != nil {
			return fmt.Errorf("encoding groups: %w", err)
		}
		raw["groups"] = out
	}
	raw["version"] = json.RawMessage(`14`)
	return nil
}
