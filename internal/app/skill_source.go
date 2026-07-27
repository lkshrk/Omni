package app

import (
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

// The config dir, not the process CWD: the manifest travels between hosts, so "./skills" must name the same package everywhere.
func (a *App) skillSourceBase() string {
	base := a.configDir()
	if absolute, err := filepath.Abs(base); err == nil {
		return absolute
	}
	return base
}

func resolveSkillSource(base, source string) string {
	if !strings.HasPrefix(source, ".") {
		return source
	}
	return filepath.Clean(filepath.Join(base, filepath.FromSlash(source)))
}

func (a *App) parseSkillPackage(input string) (config.SkillPackage, error) {
	parsed, err := agent.ParseSkillSource(input)
	if err != nil {
		return config.SkillPackage{}, err
	}
	return config.SkillPackage{
		Source: resolveSkillSource(a.skillSourceBase(), parsed.Source),
		Ref:    parsed.Ref,
		Skills: parsed.Skills,
	}, nil
}

// Two spellings of the same package share one identity, so manifest lookups cannot miss.
func (a *App) skillSourceIdentity(source string) string {
	parsed, err := agent.ParseSkillSource(source)
	if err != nil {
		return source
	}
	return resolveSkillSource(a.skillSourceBase(), parsed.Source)
}
