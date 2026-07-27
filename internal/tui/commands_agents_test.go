package tui

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsCmds_NilAppReturnsNil(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	cmds := map[string]tea.Cmd{
		"doSaveAgentsUse":              m.doSaveAgentsUse([]string{"claude"}),
		"doRestoreSkills":              m.doRestoreSkills(),
		"doImportSkills":               m.doImportSkills(),
		"doToggleSkillsFeature":        m.doToggleSkillsFeature(),
		"doToggleMcpFeature":           m.doToggleMcpFeature(),
		"doTogglePluginsFeature":       m.doTogglePluginsFeature(),
		"doUpdateSkills":               m.doUpdateSkills(),
		"doFindSkills":                 m.doFindSkills("q"),
		"doAddSkillPackage":            m.doAddSkillPackage("o/p"),
		"doAdoptSkillPackageWithGroup": m.doAdoptSkillPackageWithGroup("o/p", "", nil, ""),
		"doSetSkillAgents":             m.doSetSkillAgents("o/p", nil),
		"doRemoveSkillPackage":         m.doRemoveSkillPackage("o/p"),
		"doReloadAgentsIgnore":         m.doReloadAgentsIgnore(),
	}
	for name, c := range cmds {
		if c != nil {
			t.Errorf("%s should return nil without an app", name)
		}
	}
}

func TestAgentsCmds_SuccessMsgs(t *testing.T) {
	t.Parallel()
	m := agentsAllProgressModel(t, nil, nil, nil)

	if msg, ok := m.doSaveAgentsUse([]string{"claude"})().(agentsUseSavedMsg); !ok || msg.err != nil {
		t.Errorf("doSaveAgentsUse msg = %#v, want agentsUseSavedMsg with nil err", msg)
	}
	// Adopt is a manifest upsert plus a legacy lockfile read — no acquisition — so an arbitrary source succeeds; group "" skips the group step.
	if msg, ok := m.doAdoptSkillPackageWithGroup("owner/adopted-pkg", "", nil, "")().(skillAddedMsg); !ok || msg.err != nil {
		t.Errorf("doAdoptSkillPackageWithGroup msg = %#v, want skillAddedMsg with nil err", msg)
	}
	// Restore/import are no-ops on an empty manifest; only the msg type is pinned so the closure result path runs deterministically.
	if _, ok := m.doRestoreSkills()().(skillsRestoredMsg); !ok {
		t.Error("doRestoreSkills should yield a skillsRestoredMsg")
	}
	if _, ok := m.doImportSkills()().(skillsImportedMsg); !ok {
		t.Error("doImportSkills should yield a skillsImportedMsg")
	}
	// Toggles persist to the config, so they run last: doToggleSkillsFeature disables skills for every later skill-guarded call on this app.
	if msg, ok := m.doToggleSkillsFeature()().(skillsFeatureToggledMsg); !ok || msg.err != nil || msg.enabled {
		t.Errorf("doToggleSkillsFeature msg = %#v, want toggle to disabled with nil err", msg)
	}
	if msg, ok := m.doToggleMcpFeature()().(mcpFeatureToggledMsg); !ok || msg.err != nil || msg.enabled {
		t.Errorf("doToggleMcpFeature msg = %#v, want toggle to disabled with nil err", msg)
	}
	if msg, ok := m.doTogglePluginsFeature()().(pluginsFeatureToggledMsg); !ok || msg.err != nil || msg.enabled {
		t.Errorf("doTogglePluginsFeature msg = %#v, want toggle to disabled with nil err", msg)
	}
	if msg, ok := m.doReloadAgentsIgnore()().(agentsIgnoreReloadedMsg); !ok || msg.err != nil {
		t.Errorf("doReloadAgentsIgnore msg = %#v, want agentsIgnoreReloadedMsg with nil err", msg)
	}
}

func TestAgentsCmds_RemoveSkillPackage(t *testing.T) {
	t.Parallel()
	m := agentsAllProgressModel(t, &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: "owner/seed-pkg"}}},
	}, nil, nil)

	if msg, ok := m.doRemoveSkillPackage("owner/seed-pkg")().(skillRemovedMsg); !ok || msg.err != nil {
		t.Errorf("doRemoveSkillPackage msg = %#v, want skillRemovedMsg with nil err", msg)
	}
	if msg, ok := m.doRemoveSkillPackage("owner/unknown-pkg")().(skillRemovedMsg); !ok || msg.err == nil {
		t.Errorf("doRemoveSkillPackage(unknown) msg = %#v, want skillRemovedMsg with err", msg)
	}
}

// Runs against an app with SkillsDisabled so the app-layer guard errors before any CLI or network work.
func TestAgentsCmds_SkillsDisabledErrorPaths(t *testing.T) {
	t.Parallel()
	m := agentsAllProgressModel(t, &config.RootConfig{
		Settings: config.Settings{SkillsDisabled: config.BoolPtr(true)},
	}, nil, nil)

	if msg, ok := m.doUpdateSkills()().(skillsUpdatedMsg); !ok || msg.err == nil {
		t.Errorf("doUpdateSkills msg = %#v, want skillsUpdatedMsg with err while disabled", msg)
	}
	if msg, ok := m.doFindSkills("query")().(skillsFoundMsg); !ok || msg.err == nil {
		t.Errorf("doFindSkills msg = %#v, want skillsFoundMsg with err while disabled", msg)
	}
	if msg, ok := m.doAddSkillPackage("owner/pkg")().(skillAddedMsg); !ok || msg.err == nil {
		t.Errorf("doAddSkillPackage msg = %#v, want skillAddedMsg with err while disabled", msg)
	}
	if msg, ok := m.doAdoptSkillPackageWithGroup("owner/pkg", "dev", nil, "")().(skillAddedMsg); !ok || msg.err == nil {
		t.Errorf("doAdoptSkillPackageWithGroup msg = %#v, want skillAddedMsg with err while disabled", msg)
	}
	if msg, ok := m.doSetSkillAgents("owner/pkg", []string{"claude"})().(skillAgentsSavedMsg); !ok || msg.err == nil {
		t.Errorf("doSetSkillAgents msg = %#v, want skillAgentsSavedMsg with err while disabled", msg)
	}
	// RestoreSkills degrades disabled skills to a warning instead of an error.
	if msg, ok := m.doRestoreSkills()().(skillsRestoredMsg); !ok || msg.err != nil || len(msg.res.Warnings) == 0 {
		t.Errorf("doRestoreSkills msg = %#v, want skillsRestoredMsg with a disabled-skip warning", msg)
	}
	if msg, ok := m.doImportSkills()().(skillsImportedMsg); !ok || msg.err == nil {
		t.Errorf("doImportSkills msg = %#v, want skillsImportedMsg with err while disabled", msg)
	}
}

func TestAgentsCmds_ReloadAgentsIgnore_CorruptConfigErrors(t *testing.T) {
	t.Parallel()
	m := agentsAllProgressModel(t, nil, nil, nil)
	if err := os.WriteFile(m.app.ConfigPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupting config: %v", err)
	}

	msg, ok := m.doReloadAgentsIgnore()().(agentsIgnoreReloadedMsg)
	if !ok || msg.err == nil {
		t.Errorf("doReloadAgentsIgnore msg = %#v, want agentsIgnoreReloadedMsg with err for a corrupt config", msg)
	}
}
