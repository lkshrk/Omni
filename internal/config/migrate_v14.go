package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// isAgentConfigDotPath reports whether path (a DotEntry.Path value, e.g.
// "~/.claude" or "~/.claude/agents/foo.md") targets a machine-managed agent
// path from agentDotsManagedPaths, or a location underneath one.
// ".agents/.skill-lock.json" and other files directly under ".agents" are
// intentionally not matched here: only ".agents/skills" (the installed-skills
// store) is machine-managed, the lockfile stays a trackable user dotfile.
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

// dropAgentConfigDots removes DotEntry values whose Path targets an agent
// config dir (or a child path beneath one) from dots, preserving order of the
// remaining entries.
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

// migrateConfigV13ToV14 drops dotfiles entries already tracking an agent
// config dir (e.g. ".claude", ".codex") or a path beneath one. edaa0e1 stopped
// dots discovery from surfacing these going forward; this is the one-time
// cleanup for configs that tracked them before that change. Config-tracking
// data only — actual files on disk are never touched.
//
// ".agents/.skill-lock.json" is exempt from this sweep going forward (see
// isAgentConfigDotPath); prior runs of this same migration, before that
// narrowing, may have already stripped a tracked lockfile entry for other
// users. Migrations run once per config, keyed strictly by cfg.Version
// advancing through configMigrations (see loader.go) — there is no re-run
// mechanism once a config is past this version, so those past runs cannot be
// automatically corrected by this change. No v15 migration is added to
// "restore" anything: there is no record of what, if anything, was stripped.
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
