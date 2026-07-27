package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// Catalog hits carry an owner/source and an install count; the bare skill name throws away what distinguishes one hit from another.
func TestFindResultRowsShowSourceAndInstalls(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.width = 140
	m.skillsSearchActive = true
	m.filter.SetValue("pdf")
	m.skillFindResults = []app.FindResult{
		{Source: "vercel-labs/skills", Skill: "pdf", Installs: "1200 installs"},
		{Source: "acme/toolkit", Skill: "csv", Installs: "7 installs"},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	for _, want := range []string{"pdf", "vercel-labs/skills", "1200 installs", "csv", "acme/toolkit", "7 installs"} {
		if !strings.Contains(out, want) {
			t.Errorf("find-result rows dropped %q:\n%s", want, out)
		}
	}
}

func TestFindResultColumnsDoNotLeakIntoLocalRows(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{{
		Name:      "local-pkg",
		Source:    "acme/local-pkg",
		Installed: true,
		Updated:   "2026-07-25",
	}}, nil, nil)
	m.width = 140
	m.skillFindResults = []app.FindResult{{Source: "vercel-labs/skills", Skill: "pdf", Installs: "1200 installs"}}

	rows := agentsAllRowsList(m)
	var local, find agentsAllRow
	for _, e := range rows {
		switch e.sortName {
		case "local-pkg":
			local = e
		case "pdf":
			find = e
		}
	}
	if got := agentsVersionCellText(m, find); got != "1200 installs" {
		t.Errorf("find row version cell = %q, want the install count", got)
	}
	if got := agentsProvCellText(m, find); got != "vercel-labs/skills" {
		t.Errorf("find row provider cell = %q, want the catalog source", got)
	}
	if got := agentsVersionCellText(m, local); got != "2026-07-25" {
		t.Errorf("local row version cell = %q, want its updated date", got)
	}
}
