package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func searchResultModel(tools []*app.ToolView) Model {
	m := baseModel(nil)
	m.searchTools = tools
	m.groupNames = []string{"work"}
	m.applyFilter()
	return m
}

func TestLogicalMigration_SearchResultAddToConfigOpensGroupPicker(t *testing.T) {
	m := searchResultModel([]*app.ToolView{{
		Name:     "ripgrep",
		Provider: "brew",
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
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)

	m := modelForCmds(a)
	m.searchTools = []*app.ToolView{{
		Name:     "ripgrep",
		Provider: "brew",
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
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" {
		t.Fatalf("ripgrep providers = %+v, want brew", spec.Providers)
	}
	work := groupByName(cfg, "work")
	if work == nil || !groupHasTool(work, "ripgrep") {
		t.Fatalf("work group does not contain ripgrep: %+v", cfg.Groups)
	}
}

func TestLogicalMigration_ConfiguredEmptyToolInstallKeyAddsHighConfidenceProviderMatch(t *testing.T) {
	prov := &searchOKProvider{
		okProvider: okProvider{name: "brew"},
		results: []provider.SearchResult{{
			Name:     "prettier",
			Provider: "brew",
		}},
	}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{tuiTestHostGroup("prettier")},
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := modelForCmds(a)
	m.allTools = []*app.ToolView{{
		Name:      "prettier",
		Provider:  "",
		Installed: false,
		Tracked:   true,
	}}
	m.applyFilter()
	if len(m.visibleTools) != 1 || m.visibleTools[0].Name != "prettier" || m.visibleTools[0].Provider != "" {
		t.Fatalf("visibleTools = %+v, want missing configured prettier row with empty provider", m.visibleTools)
	}
	if m.displaySection(m.visibleTools[0]) != sectionOutOfSync || m.syncStatusOf(m.visibleTools[0]) != syncMissing {
		t.Fatalf("row classification = section:%v sync:%v, want missing out-of-sync", m.displaySection(m.visibleTools[0]), m.syncStatusOf(m.visibleTools[0]))
	}

	tm, cmd := m.Update(pressRune('i'))
	got := tm.(Model)
	if !got.loading {
		t.Fatal("loading should be true after pressing install on missing configured tool")
	}
	if got.rowOpKey != toolKey("prettier", "") {
		t.Fatalf("rowOpKey = %q, want empty-provider prettier operation", got.rowOpKey)
	}
	if got.rowOpStatus != "Installing prettier…" {
		t.Fatalf("rowOpStatus = %q, want install status", got.rowOpStatus)
	}
	if cmd == nil {
		t.Fatal("install key should dispatch an install command")
	}
	msg := runLastBatchCommand(t, cmd)
	done, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("command msg = %T, want opCompleteMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("install command error = %v", done.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 1 || providers[0].Provider != "brew" {
		t.Fatalf("providers = %+v, want high-confidence brew match saved", providers)
	}
	if len(done.tools) != 1 || done.tools[0].Provider != "brew" || !done.tools[0].Installed {
		t.Fatalf("done.tools = %+v, want installed brew row", done.tools)
	}
}

func TestLogicalMigration_SearchInstallAndAddAsksGroup(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)

	m := modelForCmds(a)
	m.searchTools = []*app.ToolView{{
		Name:     "ripgrep",
		Provider: "brew",
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

func TestLogicalMigration_SearchInstallEnterAsksGroup(t *testing.T) {
	m := searchResultModel([]*app.ToolView{{
		Name:     "ripgrep",
		Provider: "brew",
		Tracked:  false,
	}})

	got := drive(m, pressEnter())
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", got.mode)
	}
	if !got.pickerPurposeInstall {
		t.Fatal("pickerPurposeInstall should be true")
	}
}

func TestLogicalMigration_SearchInstallAndAddPrivilegedOpensAdminTerminal(t *testing.T) {
	prov := &okProvider{name: "apt"}
	a, _ := newCmdApp(t, prov, nil)

	m := modelForCmds(a)
	m.searchTools = []*app.ToolView{{
		Name:            "vim",
		Provider:        "apt",
		Package:         "vim",
		Tracked:         false,
		Privilege:       string(provider.PrivilegeRequired),
		PrivilegeReason: "apt install vim",
	}}
	m.groupNames = []string{"work"}
	m.applyFilter()

	tm, _ := m.Update(pressRune('i'))
	got := tm.(Model)
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", got.mode)
	}

	tm, _ = got.Update(pressRune('j'))
	got = tm.(Model)
	if got.pickerCursor >= len(got.pickerGroups) || got.pickerGroups[got.pickerCursor] != "work" {
		t.Fatalf("picker cursor selected %q from %v, want work", got.pickerGroups[got.pickerCursor], got.pickerGroups)
	}

	tm, cmd := got.Update(pressEnter())
	got = tm.(Model)
	if cmd != nil {
		t.Fatal("privileged install picker should not dispatch normal install command")
	}
	if got.mode != viewAdminTerminal || got.adminTerminal == nil {
		t.Fatalf("mode=%v adminTerminal=%v, want admin terminal prompt", got.mode, got.adminTerminal != nil)
	}
	if got.loading {
		t.Fatal("loading should be false while waiting for admin terminal confirmation")
	}
	if got.adminTerminal.returnMode != viewList {
		t.Fatalf("returnMode = %v, want viewList", got.adminTerminal.returnMode)
	}
	if !got.adminTerminal.addToConfig || got.adminTerminal.addGroup != "work" {
		t.Fatalf("admin install-add state = add:%v group:%q, want add to work", got.adminTerminal.addToConfig, got.adminTerminal.addGroup)
	}
}

func TestLogicalMigration_AdminTerminalInstallAndAddPersistsGroup(t *testing.T) {
	brew := &okProvider{name: "brew"}
	a := newSearchCmdApp(t, brew)
	cfgPath := a.ConfigPath
	m := modelForCmds(a)

	msg := m.doCompleteAdminTerminalAction(adminTerminalState{
		action:       provider.PrivilegeActionInstall,
		name:         "vim",
		providerName: "brew",
		pkg:          "vim",
		options:      map[string]string{"brew_kind": "formula"},
		rowKey:       toolKey("vim", "brew"),
		addToConfig:  true,
		addGroup:     "work",
	})()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.message != "installed vim and added to config" {
		t.Fatalf("message = %q, want install-and-add success", got.message)
	}
	if !slices.Contains(got.removeDiscoveredKeys, toolKey("vim", "brew")) {
		t.Fatalf("removeDiscoveredKeys = %v, want vim/brew", got.removeDiscoveredKeys)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := groupByName(cfg, "work")
	if work == nil || !groupHasTool(work, "vim") {
		t.Fatalf("work group does not contain vim: %+v", cfg.Groups)
	}
	spec := cfg.Tools["vim"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" {
		t.Fatalf("providers = %+v, want brew", spec.Providers)
	}
	if spec.Providers[0].Options["brew_kind"] != "formula" {
		t.Fatalf("providers[0].Options[brew_kind] = %q, want formula", spec.Providers[0].Options["brew_kind"])
	}
}

func opCompleteFromCmd(cmd tea.Cmd) (opCompleteMsg, bool) {
	msg := cmd()
	if done, ok := msg.(opCompleteMsg); ok {
		return done, true
	}
	if deferred, ok := msg.(runAfterRenderMsg); ok {
		return opCompleteFromCmd(deferred.cmd)
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			if done, ok := opCompleteFromCmd(child); ok {
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
	for {
		msg := cmd()
		switch msg := msg.(type) {
		case runAfterRenderMsg:
			cmd = msg.cmd
		case tea.BatchMsg:
			if len(msg) == 0 {
				t.Fatal("empty command batch")
			}
			cmd = msg[len(msg)-1]
		default:
			return msg
		}
	}
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
