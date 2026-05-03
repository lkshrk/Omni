package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func searchResultModel(tools []*database.ToolCache) Model {
	m := baseModel(nil)
	m.searchTools = tools
	m.groupNames = []string{"work"}
	m.applyFilter()
	return m
}

func TestLogicalMigration_SearchResultAddToConfigOpensGroupPicker(t *testing.T) {
	m := searchResultModel([]*database.ToolCache{{
		Name:     "ripgrep",
		Provider: "system",
		Tracked:  false,
	}})

	got := drive(m, pressRune('c'))
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", got.mode)
	}
	if !got.pickerPurposeClaim {
		t.Fatal("pickerPurposeClaim should be true for search-result add-to-config")
	}
	if got.selectedTool() == nil || got.selectedTool().Name != "ripgrep" {
		t.Fatalf("selectedTool = %+v, want ripgrep search result", got.selectedTool())
	}
	if !slices.Contains(got.pickerGroups, "work") {
		t.Fatalf("pickerGroups = %v, want work group", got.pickerGroups)
	}
}

func TestLogicalMigration_SearchResultGroupSelectionAddsExplicitGroup(t *testing.T) {
	prov := &okProvider{name: "system"}
	a, cfgPath := newCmdApp(t, prov, nil)

	m := modelForCmds(a)
	m.searchTools = []*database.ToolCache{{
		Name:     "ripgrep",
		Provider: "system",
		Tracked:  false,
	}}
	m.groupNames = []string{"work"}
	m.applyFilter()

	tm, cmd := m.Update(pressRune('c'))
	got := tm.(Model)
	if cmd != nil {
		t.Fatalf("opening group picker returned unexpected command %T", cmd())
	}
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", got.mode)
	}

	tm, _ = got.Update(pressRune('j'))
	got = tm.(Model)
	if got.pickerCursor >= len(got.pickerGroups) || got.pickerGroups[got.pickerCursor] != "work" {
		t.Fatalf("picker cursor selected %q from %v, want work", got.pickerGroups[got.pickerCursor], got.pickerGroups)
	}

	tm, cmd = got.Update(pressEnter())
	got = tm.(Model)
	if !got.loading {
		t.Fatal("loading should be true while add-to-config command is in flight")
	}
	if got.mode != viewList {
		t.Fatalf("mode = %v, want viewList after selection", got.mode)
	}
	if !strings.Contains(got.statusMsg, "Adding ripgrep to config") {
		t.Fatalf("statusMsg = %q, want add-to-config status", got.statusMsg)
	}
	if cmd == nil {
		t.Fatal("selecting a group should dispatch an add-to-config command")
	}

	msg := runLastBatchCommand(t, cmd)
	done, ok := msg.(claimDoneMsg)
	if !ok {
		t.Fatalf("command msg = %T, want claimDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("claimDoneMsg.err = %v", done.err)
	}
	if done.name != "ripgrep" || done.groupName != "work" {
		t.Fatalf("claimDoneMsg = {name:%q group:%q}, want ripgrep/work", done.name, done.groupName)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec, ok := cfg.Tools["ripgrep"]
	if !ok {
		t.Fatalf("ripgrep logical spec missing from config: %+v", cfg.Tools)
	}
	if spec.Provider != "system" {
		t.Fatalf("ripgrep provider = %q, want system", spec.Provider)
	}
	work := groupByName(cfg, "work")
	if work == nil || !groupHasTool(work, "ripgrep") {
		t.Fatalf("work group does not contain ripgrep: %+v", cfg.Groups)
	}
}

func TestLogicalMigration_SearchInstallAndAddPersistsLogicalProvider(t *testing.T) {
	prov := &okProvider{name: "system"}
	a, cfgPath := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doInstallAndAdd("ripgrep", "system")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec, ok := cfg.Tools["ripgrep"]
	if !ok {
		t.Fatalf("ripgrep logical spec missing from config: %+v", cfg.Tools)
	}
	if spec.Provider != "system" || spec.InstallWith != "" {
		t.Fatalf("ripgrep spec = %+v, want provider system without install_with pin", spec)
	}
	base := groupByName(cfg, "")
	if base == nil || !groupHasTool(base, "ripgrep") {
		t.Fatalf("base group does not contain ripgrep: %+v", cfg.Groups)
	}
}

func TestLogicalMigration_SearchInstallAndAddAsksGroup(t *testing.T) {
	prov := &okProvider{name: "system"}
	a, cfgPath := newCmdApp(t, prov, nil)

	m := modelForCmds(a)
	m.searchTools = []*database.ToolCache{{
		Name:     "ripgrep",
		Provider: "system",
		Tracked:  false,
	}}
	m.groupNames = []string{"work"}
	m.applyFilter()

	tm, cmd := m.Update(pressRune('i'))
	got := tm.(Model)
	if cmd != nil {
		t.Fatalf("opening install group picker returned unexpected command %T", cmd())
	}
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", got.mode)
	}
	if !got.pickerPurposeInstall {
		t.Fatal("pickerPurposeInstall should be true")
	}

	tm, _ = got.Update(pressRune('j'))
	got = tm.(Model)
	if got.pickerCursor >= len(got.pickerGroups) || got.pickerGroups[got.pickerCursor] != "work" {
		t.Fatalf("picker cursor selected %q from %v, want work", got.pickerGroups[got.pickerCursor], got.pickerGroups)
	}

	tm, cmd = got.Update(pressEnter())
	got = tm.(Model)
	if !got.loading {
		t.Fatal("loading should be true after confirming install group")
	}
	if cmd == nil {
		t.Fatal("expected install-and-add command")
	}
	done, ok := opCompleteFromCmd(cmd)
	if !ok {
		t.Fatalf("expected opCompleteMsg from install-and-add command")
	}
	if done.err != nil {
		t.Fatalf("unexpected op error: %v", done.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := groupByName(cfg, "work")
	if work == nil || !groupHasTool(work, "ripgrep") {
		t.Fatalf("work group does not contain ripgrep: %+v", cfg.Groups)
	}
	base := groupByName(cfg, "")
	if base != nil && groupHasTool(base, "ripgrep") {
		t.Fatalf("base group unexpectedly contains ripgrep: %+v", cfg.Groups)
	}
}

func opCompleteFromCmd(cmd tea.Cmd) (opCompleteMsg, bool) {
	msg := cmd()
	if done, ok := msg.(opCompleteMsg); ok {
		return done, true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			if done, ok := child().(opCompleteMsg); ok {
				return done, true
			}
		}
	}
	return opCompleteMsg{}, false
}

func TestLogicalMigration_ReinstallDefaultConfirmationShowsWrongProviderPrompt(t *testing.T) {
	got := drive(wrongProvModel(), pressRune('r'))
	if got.loading {
		t.Fatal("loading should stay false until reinstall is confirmed")
	}
	if got.listConfirm.action != listConfirmReinstallDefault {
		t.Fatalf("listConfirm.action = %q, want %q", got.listConfirm.action, listConfirmReinstallDefault)
	}
	if got.listConfirm.provider != "node" || got.listConfirm.installedWith != "npm" {
		t.Fatalf("listConfirm provider fields = provider:%q installedWith:%q, want node/npm", got.listConfirm.provider, got.listConfirm.installedWith)
	}
	line := listConfirmationHintsLine(got, got.selectedTool(), "")
	if !strings.Contains(line, "confirm reinstall") {
		t.Fatalf("confirmation line = %q, want reinstall confirmation prompt", line)
	}
	if strings.Contains(line, "cancel") || strings.Contains(line, "esc") {
		t.Fatalf("confirmation line = %q, should not show cancel hint", line)
	}
}

func runLastBatchCommand(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return msg
	}
	if len(batch) == 0 {
		t.Fatal("empty command batch")
	}
	return batch[len(batch)-1]()
}

func groupByName(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, group := range cfg.Groups {
		if group.Name == name {
			return group
		}
	}
	return nil
}

func groupHasTool(group *config.GroupConfig, name string) bool {
	for _, tool := range group.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
