package app_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func groupHasDot(g *config.GroupConfig, name string) bool {
	if g == nil {
		return false
	}
	for _, d := range g.Dots {
		if d.Name == name {
			return true
		}
	}
	return false
}

// End-to-end: the membership picker's save path (SetToolGroups) persists a tool
// in BOTH the host group and one reusable group, and it survives a reload.
func TestSetToolGroups_PersistsHostAndReusableGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work"},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SetToolGroups("ripgrep", []string{"testhost", "work"}, nil, "testhost"); err != nil {
		t.Fatalf("SetToolGroups: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	host := logicalTestGroupByName(cfg, "testhost")
	if host == nil || !logicalTestGroupHasTool(host, "ripgrep") {
		t.Fatalf("ripgrep should remain in host group, groups=%+v", cfg.Groups)
	}
	work := logicalTestGroupByName(cfg, "work")
	if work == nil || !logicalTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("ripgrep should be in reusable group work, groups=%+v", cfg.Groups)
	}
}

// End-to-end for dots: SetDotGroups persists a dot in host + one reusable group.
func TestSetDotGroups_PersistsHostAndReusableGroup(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := cfgDir + "/settings.json"
	host := dotsTestHostGroupName()

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: host, Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
		{Name: "work"},
	}
	rootCfg.Hosts = map[string][]string{host: {"work"}}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if _, err := a.SetDotGroups("nvim", []string{host, "work"}, nil, host); err != nil {
		t.Fatalf("SetDotGroups: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !groupHasDot(logicalTestGroupByName(cfg, host), "nvim") {
		t.Fatalf("nvim should remain in host group, groups=%+v", cfg.Groups)
	}
	if !groupHasDot(logicalTestGroupByName(cfg, "work"), "nvim") {
		t.Fatalf("nvim should be in reusable group work, groups=%+v", cfg.Groups)
	}
}

// MoveToolToGroup is a relocate even from a valid multi-group state: it collapses
// the tool to exactly the target group (used by claim/reassign flows).
func TestMoveToolToGroup_CollapsesMultiGroupMembership(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("ripgrep")}, // ripgrep starts in host + one reusable
			{Name: "personal"},
		},
		Hosts: map[string][]string{"testhost": {"work", "personal"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.MoveToolToGroup("ripgrep", "personal"); err != nil {
		t.Fatalf("MoveToolToGroup: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, g := range []string{"testhost", "work"} {
		if grp := logicalTestGroupByName(cfg, g); grp != nil && logicalTestGroupHasTool(grp, "ripgrep") {
			t.Fatalf("ripgrep should be relocated out of %q, groups=%+v", g, cfg.Groups)
		}
	}
	personal := logicalTestGroupByName(cfg, "personal")
	if personal == nil || !logicalTestGroupHasTool(personal, "ripgrep") {
		t.Fatalf("ripgrep should be in personal, groups=%+v", cfg.Groups)
	}
}
