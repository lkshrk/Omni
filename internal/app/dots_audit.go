package app

import (
	"fmt"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

// doctorDotsIgnorePatterns analyses per-entry ignore patterns for redundancy,
// contradictions, and dead rules, reporting findings as a single doctor check.
func (a *App) doctorDotsIgnorePatterns(result *DoctorResult, cfg *config.RootConfig) {
	var groups []DoctorDetailGroup
	var totalFindings int
	for _, group := range cfg.Groups {
		for _, entry := range group.Dots {
			if len(entry.Ignore) == 0 {
				continue
			}
			findings := dots.AuditIgnoreList(entry.Ignore)
			if len(findings) == 0 {
				continue
			}
			totalFindings += len(findings)
			g := DoctorDetailGroup{
				Header: fmt.Sprintf("%s: %d pattern issue(s)", entry.Name, len(findings)),
			}
			for _, f := range findings {
				g.Items = append(g.Items, fmt.Sprintf("[%s] %s", f.Type, f.Message))
			}
			groups = append(groups, g)
		}
	}
	if totalFindings == 0 {
		result.addCheck("dots.ignore", "Dots ignore", DoctorStatusOK, "all ignore patterns look clean")
		return
	}
	check := DoctorCheck{
		ID:      "dots.ignore",
		Label:   "Dots ignore",
		Status:  DoctorStatusWarn,
		Message: fmt.Sprintf("%d pattern issue(s) across dot entries", totalFindings),
		Groups:  groups,
	}
	result.Checks = append(result.Checks, check)
}

// DotsFixIgnorePatterns removes dead patterns identified by audit checks from
// the config. Returns the names of entries that were modified.
func (a *App) DotsFixIgnorePatterns() ([]string, error) {
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return nil, err
	}
	var modified []string
	for gi := range cfg.Groups {
		for di := range cfg.Groups[gi].Dots {
			entry := &cfg.Groups[gi].Dots[di]
			if len(entry.Ignore) == 0 {
				continue
			}
			cleaned, changed := cleanIgnoreList(entry.Ignore)
			if !changed {
				continue
			}
			entry.Ignore = cleaned
			modified = append(modified, entry.Name)
		}
	}
	if len(modified) == 0 {
		return nil, nil
	}
	if err := config.Save(a.ConfigPath, cfg); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}
	return modified, nil
}

func cleanIgnoreList(patterns []string) ([]string, bool) {
	cleaned := append([]string(nil), patterns...)
	changed := false
	for {
		findings := dots.AuditIgnoreList(cleaned)
		if len(findings) == 0 {
			return cleaned, changed
		}
		removeSet := make(map[int]struct{})
		for _, f := range findings {
			if len(f.Indices) == 0 {
				continue
			}
			removeSet[f.Indices[0]] = struct{}{}
		}
		if len(removeSet) == 0 {
			return cleaned, changed
		}
		next := make([]string, 0, len(cleaned)-len(removeSet))
		for i, p := range cleaned {
			if _, remove := removeSet[i]; !remove {
				next = append(next, p)
			}
		}
		if len(next) == len(cleaned) {
			return cleaned, changed
		}
		cleaned = next
		changed = true
	}
}
