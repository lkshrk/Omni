package app

import (
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/config"
)

// SkillRow is a display row for the agents skills table.
type SkillRow struct {
	Name      string
	Source    string
	Ref       string
	Agents    []string // agents whose global skills dir contains this skill
	Updated   string   // YYYY-MM-DD from the lockfile; empty when unknown
	Installed bool     // present in the upstream lockfile
}

// SkillRows joins the declared manifest with the upstream lockfile and a
// per-agent filesystem check to build the agents skills table.
func (a *App) SkillRows() ([]SkillRow, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return nil, err
	}
	rows := make([]SkillRow, 0, len(cfg.Agents.Skills))
	for _, s := range cfg.Agents.Skills {
		row := SkillRow{Name: s.Name, Source: s.Source, Ref: s.Ref}
		if e, ok := lock.Skills[s.Name]; ok {
			row.Installed = true
			row.Updated = skillUpdatedDate(e.UpdatedAt)
		}
		row.Agents = agentsWithSkill(home, s.Name)
		rows = append(rows, row)
	}
	return rows, nil
}

// skillUpdatedDate trims an ISO timestamp to its date component.
func skillUpdatedDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// agentsWithSkill reports which agents have the named skill in their global
// skills directory. Best-effort: absent dirs simply yield no match.
func agentsWithSkill(home, name string) []string {
	var out []string
	for _, a := range supportedAgents {
		if _, err := os.Lstat(filepath.Join(agentSkillsPath(home, a), name)); err == nil {
			out = append(out, a.ID)
		}
	}
	return out
}
