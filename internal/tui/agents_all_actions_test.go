package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsRowUpgrade_SkillsIgnoredRow_NotHandled(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusIgnored, mark: agentsMarkNone}

	handled, cmds := m.agentsRowUpgrade(e)

	if handled {
		t.Fatalf("expected handled=false for ignored skills row, got true")
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no cmds for ignored skills row, got %d", len(cmds))
	}
	if m.skillsRunning {
		t.Fatalf("expected skillsRunning to stay false for ignored skills row")
	}
}

func TestAgentsRowUpgrade_SkillsAvailableRow_NotHandled(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusAvailable, mark: agentsMarkNone}

	handled, cmds := m.agentsRowUpgrade(e)

	if handled {
		t.Fatalf("expected handled=false for available (find) skills row, got true")
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no cmds for available skills row, got %d", len(cmds))
	}
	if m.skillsRunning {
		t.Fatalf("expected skillsRunning to stay false for available skills row")
	}
}

func TestAgentsRowUpgrade_SkillsInstalledRow_Handled(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusInstalled, mark: agentsMarkNone}

	handled, cmds := m.agentsRowUpgrade(e)

	if !handled {
		t.Fatalf("expected handled=true for installed skills row")
	}
	if len(cmds) == 0 {
		t.Fatalf("expected cmds for installed skills row, got none")
	}
	if !m.skillsRunning {
		t.Fatalf("expected skillsRunning=true for installed skills row")
	}
}

func TestAgentsRowHints_SkillsIgnoredRow_NoUpdateHint(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusIgnored, mark: agentsMarkNone}

	hints := agentsRowHints(m, e)

	for _, h := range hints {
		if h.key == "u" {
			t.Fatalf("expected no 'u' hint for ignored skills row, got hints: %+v", hints)
		}
	}
}

func TestAgentsRowHints_SkillsAvailableRow_NoUpdateHint(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusAvailable, mark: agentsMarkNone}

	hints := agentsRowHints(m, e)

	for _, h := range hints {
		if h.key == "u" {
			t.Fatalf("expected no 'u' hint for available skills row, got hints: %+v", hints)
		}
	}
}

func TestAgentsRowHints_SkillsInstalledRow_HasUpdateHint(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusInstalled, mark: agentsMarkNone}

	hints := agentsRowHints(m, e)

	found := false
	for _, h := range hints {
		if h.key == "u" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'u' hint for installed skills row, got hints: %+v", hints)
	}
}

func TestAgentsRowUpgrade_McpRowNeverHandled(t *testing.T) {
	// mcpAgentRowStatus (agents_status.go) only ever returns agentsStatusOutOfSync
	// or agentsStatusInstalled — never agentsStatusUpdates — so agentsRowUpgrade
	// has no case for agentsSectionMcp at all. Pin that 'u' is unhandled for any
	// mcp row regardless of status/mark.
	m := agentsAllModel(nil, nil, nil)
	for _, e := range []agentsAllRow{
		{feature: agentsSectionMcp, status: agentsStatusInstalled, mark: agentsMarkNone},
		{feature: agentsSectionMcp, status: agentsStatusOutOfSync, mark: agentsMarkMissing},
		{feature: agentsSectionMcp, status: agentsStatusIgnored, mark: agentsMarkNone},
	} {
		handled, cmds := m.agentsRowUpgrade(e)
		if handled {
			t.Fatalf("expected handled=false for mcp row with status=%v, got true", e.status)
		}
		if len(cmds) != 0 {
			t.Fatalf("expected no cmds for mcp row with status=%v, got %d", e.status, len(cmds))
		}
	}
}

func TestAgentsRowInstall_Skills_SetsOnlySkillAddRunning(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "alpha-mcp-skill", Source: "o/alpha-mcp-skill", Installed: false}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('i'))
	if !got.skillAddRunning {
		t.Fatal("expected skillAddRunning=true after 'i' on a missing skills row")
	}
	if got.mcpRunning {
		t.Fatal("expected mcpRunning to stay false")
	}
	if got.pluginRunning {
		t.Fatal("expected pluginRunning to stay false")
	}
}

func TestAgentsRowInstall_Mcp_SetsOnlyMcpRunning(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "beta-mcp", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusMissing}}},
		nil,
	)
	cursor := firstRowCursor(m, agentsSectionMcp)
	if cursor < 0 {
		t.Fatal("expected an mcp row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('i'))
	if !got.mcpRunning {
		t.Fatal("expected mcpRunning=true after 'i' on a missing mcp row")
	}
	if got.skillAddRunning {
		t.Fatal("expected skillAddRunning to stay false")
	}
	if got.pluginRunning {
		t.Fatal("expected pluginRunning to stay false")
	}
}

func TestAgentsRowInstall_Plugin_SetsOnlyPluginRunning(t *testing.T) {
	m := agentsAllModel(
		nil, nil,
		[]app.PluginRow{{Name: "gamma-plugin", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusMissing}}},
	)
	cursor := firstRowCursor(m, agentsSectionPlugins)
	if cursor < 0 {
		t.Fatal("expected a plugin row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('i'))
	if !got.pluginRunning {
		t.Fatal("expected pluginRunning=true after 'i' on a missing plugin row")
	}
	if got.skillAddRunning {
		t.Fatal("expected skillAddRunning to stay false")
	}
	if got.mcpRunning {
		t.Fatal("expected mcpRunning to stay false")
	}
}

func TestOpenMcpGroupMembershipPicker_SeedsCurrentGroups(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "delta-mcp", Groups: []string{"work", "personal"}, PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)
	m.mcpCursor = 0

	m.openMcpGroupMembershipPicker()

	got := m.mcpMemberships["delta-mcp"]
	if len(got) != 2 || got[0] != "work" || got[1] != "personal" {
		t.Fatalf("mcpMemberships[delta-mcp] = %v, want [work personal]", got)
	}
}

func TestOpenPluginGroupMembershipPicker_SeedsCurrentGroups(t *testing.T) {
	m := agentsAllModel(
		nil, nil,
		[]app.PluginRow{{Name: "epsilon-plugin", Groups: []string{"ops"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.pluginCursor = 0

	m.openPluginGroupMembershipPicker()

	got := m.pluginMemberships["epsilon-plugin"]
	if len(got) != 1 || got[0] != "ops" {
		t.Fatalf("pluginMemberships[epsilon-plugin] = %v, want [ops]", got)
	}
}

func TestOpenMarketplaceGroupMembershipPicker_SeedsCurrentGroups(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "theta-market", Groups: []string{"ops"}, Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	m.marketplaceCursor = 0

	m.openMarketplaceGroupMembershipPicker()

	got := m.marketplaceMemberships["theta-market"]
	if len(got) != 1 || got[0] != "ops" {
		t.Fatalf("marketplaceMemberships[theta-market] = %v, want [ops]", got)
	}
	if m.pickerMembershipKind != pickerMembershipMarketplace {
		t.Errorf("pickerMembershipKind = %q, want %q", m.pickerMembershipKind, pickerMembershipMarketplace)
	}
}

func TestAgentsAll_IgnoreReclassification_ManifestChangeReflectsInRowsList(t *testing.T) {
	m := agentsAllModel(
		nil, nil,
		[]app.PluginRow{{Name: "zeta-plugin", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.agentsIgnore = config.AgentsIgnore{}

	before := agentsAllRowsList(m)
	for _, e := range before {
		if e.feature == agentsSectionPlugins && e.status == agentsStatusIgnored {
			t.Fatalf("expected zeta-plugin not ignored before manifest reload, got ignored row: %+v", e)
		}
	}

	m.agentsIgnore.Plugins = []string{"zeta-plugin"}

	after := agentsAllRowsList(m)
	found := false
	for _, e := range after {
		if e.feature == agentsSectionPlugins && e.sortName == "zeta-plugin" {
			if e.status != agentsStatusIgnored {
				t.Fatalf("expected zeta-plugin classified as agentsStatusIgnored after ignore reload, got %v", e.status)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected a zeta-plugin row after ignore reload")
	}
}

// TestAgentsAll_StaleIgnore_ShadowedSkillRowStaysInstalled covers a manifest
// skill package that is both plugin-shadowed (installed via an installed
// plugin, not the lockfile) and still listed in a stale ignore.skills entry
// from before the plugin was installed: the shadow takes precedence, so the
// row must classify as installed/shadowed, not ignored.
func TestAgentsAll_StaleIgnore_ShadowedSkillRowStaysInstalled(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "iota-skill", Source: "o/iota-skill", Installed: false, ShadowedByPlugin: true}},
		nil, nil,
	)
	m.agentsIgnore.Skills = []string{"iota-skill"}

	rows := agentsAllRowsList(m)
	found := false
	for _, e := range rows {
		if e.feature == agentsSectionSkills && e.sortName == "iota-skill" {
			found = true
			if e.status != agentsStatusInstalled || e.mark != agentsMarkShadowed {
				t.Fatalf("expected iota-skill classified as installed/shadowed despite stale ignore entry, got status=%v mark=%v", e.status, e.mark)
			}
		}
	}
	if !found {
		t.Fatal("expected an iota-skill row")
	}
}

// TestAgentsAll_StaleIgnore_ShadowedMcpRowStaysInstalled mirrors the skills
// case for mcp servers: a manifest mcp entry shadowed by an installed plugin
// must keep expanding into per-agent rows (shadowed status), not collapse
// into a single ignored row, even when it's still in a stale ignore.mcp_servers
// entry.
func TestAgentsAll_StaleIgnore_ShadowedMcpRowStaysInstalled(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:             "kappa-mcp",
			Agents:           []string{"claude"},
			ShadowedByPlugin: true,
			PerAgentStatus:   map[string]app.McpStatus{"claude": app.McpStatusShadowed},
		}},
		nil,
	)
	m.agentsIgnore.McpServers = []string{"kappa-mcp"}

	rows := agentsAllRowsList(m)
	found := false
	for _, e := range rows {
		if e.feature == agentsSectionMcp && e.sortName == "kappa-mcp" {
			found = true
			if e.status != agentsStatusInstalled || e.mark != agentsMarkShadowed {
				t.Fatalf("expected kappa-mcp classified as installed/shadowed despite stale ignore entry, got status=%v mark=%v", e.status, e.mark)
			}
		}
	}
	if !found {
		t.Fatal("expected a kappa-mcp row")
	}
}

func TestHandleAgentsDeleteConfirmKeyMsg_AnyOtherKeyCancelsArmedDelete(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "eta-skill", Source: "o/eta-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	armed := drive(m, pressRune('d'))
	if !armed.agentsDeleteConfirm {
		t.Fatal("expected 'd' to arm agentsDeleteConfirm")
	}

	for _, key := range []rune{'j', 'z'} {
		got := drive(armed, pressRune(key))
		if got.agentsDeleteConfirm {
			t.Fatalf("expected pressing %q while armed to cancel agentsDeleteConfirm, got still armed", key)
		}
	}
}

func TestAgentsAll_DeleteTargetsItemLevelNameRegardlessOfWhichPerAgentRowArmed(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:   "theta-mcp",
			Agents: []string{"claude", "cursor"},
			PerAgentStatus: map[string]app.McpStatus{
				"claude": app.McpStatusInstalled,
				"cursor": app.McpStatusInstalled,
			},
		}},
		nil,
	)

	var secondAgentCursor = -1
	rows := agentsAllRowsList(m)
	seen := 0
	for i, e := range rows {
		if e.feature == agentsSectionMcp && e.sortName == "theta-mcp" {
			seen++
			if seen == 2 {
				secondAgentCursor = i
			}
		}
	}
	if secondAgentCursor < 0 {
		t.Fatalf("expected two expanded rows for theta-mcp, saw %d", seen)
	}
	m.agentsAllCursor = secondAgentCursor

	got := drive(m, pressRune('d'))
	if !got.agentsDeleteConfirm {
		t.Fatal("expected 'd' to arm agentsDeleteConfirm from the second agent's row")
	}
	if got.agentsDeleteName != "theta-mcp" {
		t.Fatalf("agentsDeleteName = %q, want item-level name theta-mcp (not per-agent-mangled)", got.agentsDeleteName)
	}
}

func TestAgentsRowHints_Table(t *testing.T) {
	hasKey := func(hints []hintItem, key string) bool {
		for _, h := range hints {
			if h.key == key {
				return true
			}
		}
		return false
	}
	hintLabel := func(hints []hintItem, key string) string {
		for _, h := range hints {
			if h.key == key {
				return h.desc
			}
		}
		return ""
	}

	cases := []struct {
		name       string
		entry      agentsAllRow
		wantU      bool
		wantI      bool
		wantG      bool
		wantX      bool
		wantD      bool
		wantXLabel string
	}{
		{
			name:       "skills installed",
			entry:      agentsAllRow{feature: agentsSectionSkills, status: agentsStatusInstalled, mark: agentsMarkNone},
			wantU:      true,
			wantI:      false,
			wantG:      true,
			wantX:      true,
			wantD:      true,
			wantXLabel: "ignore",
		},
		{
			name:  "skills out of sync missing",
			entry: agentsAllRow{feature: agentsSectionSkills, status: agentsStatusOutOfSync, mark: agentsMarkMissing},
			wantU: true,
			wantI: true,
			wantG: true,
			wantX: true,
			wantD: true,
		},
		{
			name:       "skills ignored has no update hint",
			entry:      agentsAllRow{feature: agentsSectionSkills, status: agentsStatusIgnored, mark: agentsMarkNone},
			wantU:      false,
			wantI:      false,
			wantG:      true,
			wantX:      true,
			wantD:      true,
			wantXLabel: "unignore",
		},
		{
			name:  "skills available find row has no update, install, group, or delete hint, but ignore is eligible",
			entry: agentsAllRow{feature: agentsSectionSkills, status: agentsStatusAvailable, mark: agentsMarkNone},
			wantU: false,
			wantI: false,
			wantG: false,
			wantX: true,
			wantD: false,
		},
		{
			name:  "mcp installed",
			entry: agentsAllRow{feature: agentsSectionMcp, status: agentsStatusInstalled, mark: agentsMarkNone},
			wantU: false,
			wantI: false,
			wantG: true,
			wantX: true,
			wantD: true,
		},
		{
			name:  "mcp out of sync missing",
			entry: agentsAllRow{feature: agentsSectionMcp, status: agentsStatusOutOfSync, mark: agentsMarkMissing},
			wantU: false,
			wantI: true,
			wantG: true,
			wantX: true,
			wantD: true,
		},
		{
			name:  "plugin updates available",
			entry: agentsAllRow{feature: agentsSectionPlugins, status: agentsStatusUpdates, mark: agentsMarkNone},
			wantU: true,
			wantI: false,
			wantG: true,
			wantX: true,
			wantD: true,
		},
		{
			name:  "plugin installed",
			entry: agentsAllRow{feature: agentsSectionPlugins, status: agentsStatusInstalled, mark: agentsMarkNone},
			wantU: false,
			wantI: false,
			wantG: true,
			wantX: true,
			wantD: true,
		},
		{
			name:  "plugin out of sync missing",
			entry: agentsAllRow{feature: agentsSectionPlugins, status: agentsStatusOutOfSync, mark: agentsMarkMissing},
			wantU: false,
			wantI: true,
			wantG: true,
			wantX: true,
			wantD: true,
		},
		{
			name:       "plugin ignored has no update hint regardless of eligibility",
			entry:      agentsAllRow{feature: agentsSectionPlugins, status: agentsStatusIgnored, mark: agentsMarkNone},
			wantU:      false,
			wantI:      false,
			wantG:      true,
			wantX:      true,
			wantD:      true,
			wantXLabel: "unignore",
		},
	}

	m := agentsAllModel(nil, nil, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hints := agentsRowHints(m, tc.entry)
			if got := hasKey(hints, "u"); got != tc.wantU {
				t.Errorf("'u' present = %v, want %v (hints: %+v)", got, tc.wantU, hints)
			}
			if got := hasKey(hints, "i"); got != tc.wantI {
				t.Errorf("'i' present = %v, want %v (hints: %+v)", got, tc.wantI, hints)
			}
			if got := hasKey(hints, "g"); got != tc.wantG {
				t.Errorf("'g' present = %v, want %v (hints: %+v)", got, tc.wantG, hints)
			}
			if got := hasKey(hints, "x"); got != tc.wantX {
				t.Errorf("'x' present = %v, want %v (hints: %+v)", got, tc.wantX, hints)
			}
			if got := hasKey(hints, "d"); got != tc.wantD {
				t.Errorf("'d' present = %v, want %v (hints: %+v)", got, tc.wantD, hints)
			}
			if tc.wantXLabel != "" {
				if got := hintLabel(hints, "x"); got != tc.wantXLabel {
					t.Errorf("'x' label = %q, want %q", got, tc.wantXLabel)
				}
			}
		})
	}
}

func TestAgentsRowToggleIgnore_SkillsAvailableRow_Handled(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.skillFindResults = []app.FindResult{{Source: "owner/found-skill", Skill: "found-skill"}}
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: 0, status: agentsStatusAvailable, mark: agentsMarkNone}

	handled, cmds := m.agentsRowToggleIgnore(e)

	if !handled {
		t.Fatalf("expected handled=true for available (find) skills row: a not-yet-installed item can be preemptively ignored")
	}
	if len(cmds) == 0 {
		t.Fatalf("expected ignore cmds for available skills row")
	}
}

func TestAgentsRowToggleIgnore_SkillsIgnoredRow_StillHandled(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "caveman", Source: "owner/caveman", Installed: true}},
		nil, nil,
	)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: 0, status: agentsStatusIgnored, mark: agentsMarkNone}

	handled, cmds := m.agentsRowToggleIgnore(e)

	if !handled {
		t.Fatalf("expected handled=true for ignored skills row (unignore path)")
	}
	if len(cmds) == 0 {
		t.Fatalf("expected cmds for ignored skills row, got none")
	}
}

func TestAgentsRowHints_SkillsAvailableRow_HasIgnoreHint(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusAvailable, mark: agentsMarkNone}

	hints := agentsRowHints(m, e)

	found := false
	for _, h := range hints {
		if h.key == "x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an 'x' hint for available skills row, got hints: %+v", hints)
	}
}

// agentsEligibilityMatrix drives agentsInstallEligible/agentsGroupEligible/
// agentsDeleteEligible against every (status, mark) combination and asserts
// hint-visible (agentsRowHints) and action-handled (the corresponding
// agentsRow* func) agree — the two must never independently drift again.
func TestAgentsEligibilityMatrix_InstallGroupDelete(t *testing.T) {
	statuses := []agentsRowStatus{
		agentsStatusUpdates,
		agentsStatusOutOfSync,
		agentsStatusInstalled,
		agentsStatusAvailable,
		agentsStatusIgnored,
	}
	marks := []agentsSyncMark{agentsMarkNone, agentsMarkMissing, agentsMarkOrphan}

	hasKey := func(hints []hintItem, key string) bool {
		for _, h := range hints {
			if h.key == key {
				return true
			}
		}
		return false
	}

	cells := 0
	for _, status := range statuses {
		for _, mark := range marks {
			cells++
			t.Run("", func(t *testing.T) {
				// Skills feature exercises agentsRowInstall/agentsRowGroup end to end;
				// bounds checks are satisfied via a single skills row at localIdx 0.
				m := agentsAllModel(
					[]app.SkillPackageRow{{Name: "matrix-skill", Source: "o/matrix-skill", Installed: status != agentsStatusOutOfSync}},
					nil, nil,
				)
				e := agentsAllRow{feature: agentsSectionSkills, localIdx: 0, status: status, mark: mark}

				hints := agentsRowHints(m, e)

				wantInstall := agentsInstallEligible(e)
				if got := hasKey(hints, "i"); got != wantInstall {
					t.Errorf("hint 'i' present=%v, want %v (status=%v mark=%v)", got, wantInstall, status, mark)
				}
				installHandled, _ := (&m).agentsRowInstall(e)
				if installHandled != wantInstall {
					t.Errorf("agentsRowInstall handled=%v, want %v (status=%v mark=%v)", installHandled, wantInstall, status, mark)
				}

				wantGroup := agentsGroupEligible(e)
				if got := hasKey(hints, "g"); got != wantGroup {
					t.Errorf("hint 'g' present=%v, want %v (status=%v mark=%v)", got, wantGroup, status, mark)
				}
				groupHandled, _ := (&m).agentsRowGroup(e)
				if groupHandled != wantGroup {
					t.Errorf("agentsRowGroup handled=%v, want %v (status=%v mark=%v)", groupHandled, wantGroup, status, mark)
				}

				wantDelete := agentsDeleteEligible(e)
				if got := hasKey(hints, "d"); got != wantDelete {
					t.Errorf("hint 'd' present=%v, want %v (status=%v mark=%v)", got, wantDelete, status, mark)
				}
				m2 := m
				deleteHandled, _ := (&m2).agentsRowArmDelete(e)
				if deleteHandled != wantDelete {
					t.Errorf("agentsRowArmDelete handled=%v, want %v (status=%v mark=%v)", deleteHandled, wantDelete, status, mark)
				}

				wantIgnore := agentsIgnoreEligible(e)
				if got := hasKey(hints, "x"); got != wantIgnore {
					t.Errorf("hint 'x' present=%v, want %v (status=%v mark=%v)", got, wantIgnore, status, mark)
				}
				m3 := m
				ignoreHandled, _ := (&m3).agentsRowToggleIgnore(e)
				if ignoreHandled != wantIgnore {
					t.Errorf("agentsRowToggleIgnore handled=%v, want %v (status=%v mark=%v)", ignoreHandled, wantIgnore, status, mark)
				}
			})
		}
	}
	if cells != len(statuses)*len(marks) {
		t.Fatalf("expected %d matrix cells, computed %d", len(statuses)*len(marks), cells)
	}
}

// TestAgentsEligibility_ShadowedRow_InstallFalseDeleteTrue covers the one
// (status, mark) combination the matrix above deliberately excludes:
// agentsMarkShadowed only ever pairs with agentsStatusInstalled (see
// skillPackageRowStatus/mcpAgentRowStatus), so it can't be folded into a
// matrix keyed on independently-varying status/mark. Install must stay
// false — a shadowed row has nothing missing to install — but delete stays
// true so removing the stale manifest entry remains a cleanup path.
func TestAgentsEligibility_ShadowedRow_InstallFalseDeleteTrue(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "shadow-skill", Source: "o/shadow-skill", ShadowedByPlugin: true}},
		nil, nil,
	)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: 0, status: agentsStatusInstalled, mark: agentsMarkShadowed}

	if agentsInstallEligible(e) {
		t.Fatalf("expected agentsInstallEligible=false for a shadowed row")
	}
	if !agentsDeleteEligible(e) {
		t.Fatalf("expected agentsDeleteEligible=true for a shadowed row")
	}

	installHandled, _ := (&m).agentsRowInstall(e)
	if installHandled {
		t.Fatalf("expected agentsRowInstall handled=false for a shadowed row")
	}
}

func TestAgentsOrphanSkillRow_UninstallDeleteEligible(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "Leonxlnx/taste-skill", Source: "Leonxlnx/taste-skill", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	if unmanagedStart < 0 {
		t.Fatal("expected unmanaged skills rows")
	}
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, status: agentsStatusOutOfSync, mark: agentsMarkOrphan}

	if !agentsDeleteEligible(e) {
		t.Fatal("expected orphan skill row to be delete-eligible")
	}
	if agentsDeleteHintLabel(e) != "uninstall" {
		t.Fatalf("hint label = %q, want uninstall", agentsDeleteHintLabel(e))
	}
	hints := agentsRowHints(m, e)
	var gotUninstall bool
	for _, h := range hints {
		if h.key == "d" && h.desc == "uninstall" {
			gotUninstall = true
		}
	}
	if !gotUninstall {
		t.Fatalf("expected 'd' uninstall hint, got %+v", hints)
	}

	cursor := -1
	for i, row := range agentsAllRowsList(m) {
		if row.feature == agentsSectionSkills && row.mark == agentsMarkOrphan && row.sortName == "Leonxlnx/taste-skill" {
			cursor = i
			break
		}
	}
	if cursor < 0 {
		t.Fatal("expected orphan skill row in agentsAllRowsList")
	}
	m.agentsAllCursor = cursor
	armed := drive(m, pressRune('d'))
	if !armed.agentsDeleteConfirm {
		t.Fatal("expected 'd' to arm agentsDeleteConfirm on orphan skill row")
	}
	if !armed.agentsDeleteUninstall {
		t.Fatal("expected agentsDeleteUninstall=true for orphan skill row")
	}
}

func TestAgentsInstallEligible_Marketplace_AlwaysFalse(t *testing.T) {
	statuses := []agentsRowStatus{agentsStatusUpdates, agentsStatusOutOfSync, agentsStatusInstalled, agentsStatusAvailable, agentsStatusIgnored}
	marks := []agentsSyncMark{agentsMarkNone, agentsMarkMissing, agentsMarkOrphan}
	for _, status := range statuses {
		for _, mark := range marks {
			e := agentsAllRow{feature: agentsSectionMarketplaces, status: status, mark: mark}
			if agentsInstallEligible(e) {
				t.Errorf("agentsInstallEligible(status=%v mark=%v) = true, want false for marketplaces", status, mark)
			}
		}
	}
}

func TestAgentsGroupEligible_Marketplace_MatchesPluginParity(t *testing.T) {
	statuses := []agentsRowStatus{agentsStatusUpdates, agentsStatusOutOfSync, agentsStatusInstalled, agentsStatusAvailable, agentsStatusIgnored}
	marks := []agentsSyncMark{agentsMarkNone, agentsMarkMissing, agentsMarkOrphan}
	for _, status := range statuses {
		for _, mark := range marks {
			e := agentsAllRow{feature: agentsSectionMarketplaces, status: status, mark: mark}
			want := status != agentsStatusAvailable && mark != agentsMarkOrphan
			if got := agentsGroupEligible(e); got != want {
				t.Errorf("agentsGroupEligible(status=%v mark=%v) = %v, want %v for marketplaces", status, mark, got, want)
			}
		}
	}
}

func TestAgentsRowInstall_Marketplace_NoOp(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "acme-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusMissing}},
	}
	e := agentsAllRow{feature: agentsSectionMarketplaces, localIdx: 0, agentID: "claude", status: agentsStatusOutOfSync, mark: agentsMarkMissing}

	handled, cmds := m.agentsRowInstall(e)

	if handled {
		t.Fatalf("expected agentsRowInstall handled=false for marketplace row, got true")
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no cmds for marketplace install, got %d", len(cmds))
	}
	if m.marketplaceRunning {
		t.Fatalf("expected marketplaceRunning to stay false after no-op install")
	}
}

func TestAgentsRowGroup_Marketplace_OpensGroupMembershipPicker(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "acme-market", Source: "acme/repo", Groups: []string{"ops"}, Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	e := agentsAllRow{feature: agentsSectionMarketplaces, localIdx: 0, agentID: "claude", status: agentsStatusInstalled, mark: agentsMarkNone}

	handled, cmds := m.agentsRowGroup(e)

	if !handled {
		t.Fatalf("expected agentsRowGroup handled=true for a managed marketplace row")
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no cmds for marketplace group action, got %d", len(cmds))
	}
	if m.mode != viewGroupMembership {
		t.Fatalf("expected mode = viewGroupMembership, got %v", m.mode)
	}
	if m.marketplaceCursor != 0 {
		t.Errorf("marketplaceCursor = %d, want 0", m.marketplaceCursor)
	}
	if m.marketplaceCursorAgentID != "claude" {
		t.Errorf("marketplaceCursorAgentID = %q, want %q", m.marketplaceCursorAgentID, "claude")
	}
}

func TestAgentsRowClaim_Marketplace_ManagedRowNoOp(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "acme-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	e := agentsAllRow{feature: agentsSectionMarketplaces, localIdx: 0, agentID: "claude", status: agentsStatusInstalled, mark: agentsMarkNone}

	handled, _ := m.agentsRowClaim(e)

	if handled {
		t.Fatalf("expected agentsRowClaim handled=false for a managed marketplace row, got true")
	}
	if m.marketplaceRunning {
		t.Fatalf("expected marketplaceRunning to stay false for a managed-row claim attempt")
	}
}

func TestAgentsRowClaim_MarketplaceOrphan_OpensGroupPickerWithoutAdopting(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "orphan-market", Source: "orphan/repo"}},
	}
	e := agentsAllRow{feature: agentsSectionMarketplaces, localIdx: len(m.marketplaceRows), agentID: "claude-code", status: agentsStatusOutOfSync, mark: agentsMarkOrphan}

	handled, cmds := m.agentsRowClaim(e)

	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan marketplace row")
	}
	if m.marketplaceRunning {
		t.Error("claiming should not adopt immediately; marketplaceRunning should stay false until confirm")
	}
	if len(cmds) != 0 {
		t.Errorf("expected no cmds dispatched before confirm, got %+v", cmds)
	}
	if m.mode != viewGroupPicker {
		t.Fatalf("expected mode = viewGroupPicker, got %v", m.mode)
	}
	if m.pickerMembershipKind != pickerMembershipMarketplace {
		t.Errorf("pickerMembershipKind = %q, want %q", m.pickerMembershipKind, pickerMembershipMarketplace)
	}
}

func TestAgentsRowClaim_Marketplace_MissingAgentID_NoOp(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "orphan-market", Source: "orphan/repo"}},
	}
	e := agentsAllRow{feature: agentsSectionMarketplaces, localIdx: len(m.marketplaceRows), agentID: "", status: agentsStatusOutOfSync, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)

	if handled {
		t.Fatalf("expected agentsRowClaim handled=false when agentID is empty, got true")
	}
}

func TestHandleMarketplaceKeyMsg_ClaimUnmanagedRow_OpensGroupPicker(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "orphan-market", Source: "orphan/repo"}},
	}
	m.skillTypeIdx = agentsChipMarketplace
	m.marketplaceCursor = len(m.marketplaceRows)

	got := drive(m, pressRune('c'))

	if got.marketplaceRunning {
		t.Fatalf("expected marketplaceRunning to stay false until the claim is confirmed")
	}
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v after claim keypress, want viewGroupPicker", got.mode)
	}
	if got.pickerMembershipKind != pickerMembershipMarketplace {
		t.Errorf("pickerMembershipKind = %q, want %q", got.pickerMembershipKind, pickerMembershipMarketplace)
	}
}

func TestHandleMarketplaceKeyMsg_DeleteArmsConfirmOnManagedRowOnly(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "acme-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	m.skillTypeIdx = agentsChipMarketplace
	m.marketplaceCursor = 0

	armed := drive(m, pressRune('d'))

	if !armed.marketplaceDeleteConfirm {
		t.Fatalf("expected 'd' on a managed marketplace row to arm marketplaceDeleteConfirm")
	}
	if armed.marketplaceDeleteName != "acme-market" {
		t.Fatalf("marketplaceDeleteName = %q, want %q", armed.marketplaceDeleteName, "acme-market")
	}
}

func TestHandleMarketplaceKeyMsg_DeleteDoesNotArmOnUnmanagedRow(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "orphan-market", Source: "orphan/repo"}},
	}
	m.skillTypeIdx = agentsChipMarketplace
	m.marketplaceCursor = len(m.marketplaceRows)

	got := drive(m, pressRune('d'))

	if got.marketplaceDeleteConfirm {
		t.Fatalf("expected 'd' on an unmanaged/orphan marketplace row to not arm delete confirm")
	}
}

func TestHandleMarketplaceKeyMsg_ConfirmDeleteTriggersRemovalAndClearsConfirm(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "acme-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	m.skillTypeIdx = agentsChipMarketplace
	m.marketplaceCursor = 0

	armed := drive(m, pressRune('d'))
	if !armed.marketplaceDeleteConfirm {
		t.Fatalf("expected marketplaceDeleteConfirm to be armed before confirming")
	}

	got := drive(armed, pressRune('d'))

	if got.marketplaceDeleteConfirm {
		t.Fatalf("expected marketplaceDeleteConfirm to clear after confirming delete")
	}
	if got.marketplaceDeleteName != "" {
		t.Fatalf("expected marketplaceDeleteName to clear after confirming delete, got %q", got.marketplaceDeleteName)
	}
	if !got.marketplaceRunning {
		t.Fatalf("expected marketplaceRunning=true after confirming delete")
	}
}

func TestHandleMarketplaceKeyMsg_BackCancelsArmedDeleteWithoutRemoving(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "acme-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	m.skillTypeIdx = agentsChipMarketplace
	m.marketplaceCursor = 0

	armed := drive(m, pressRune('d'))
	if !armed.marketplaceDeleteConfirm {
		t.Fatalf("expected marketplaceDeleteConfirm to be armed before cancelling")
	}

	got := drive(armed, pressEsc())

	if got.marketplaceDeleteConfirm {
		t.Fatalf("expected marketplaceDeleteConfirm to clear after Back")
	}
	if got.marketplaceRunning {
		t.Fatalf("expected marketplaceRunning to stay false after cancelling delete")
	}
}

func TestClearActiveConfirmation_ClearsMarketplaceDeleteState(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceDeleteConfirm = true
	m.marketplaceDeleteName = "acme-market"

	(&m).clearActiveConfirmation()

	if m.marketplaceDeleteConfirm {
		t.Fatalf("expected clearActiveConfirmation to reset marketplaceDeleteConfirm")
	}
	if m.marketplaceDeleteName != "" {
		t.Fatalf("expected clearActiveConfirmation to reset marketplaceDeleteName, got %q", m.marketplaceDeleteName)
	}
}

func TestAgentsRowToggleIgnore_ArmsConfirmWithoutTogglingImmediately(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "iota-skill", Source: "o/iota-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('x'))

	if !got.agentsIgnoreConfirm {
		t.Fatal("expected first 'x' to arm agentsIgnoreConfirm")
	}
	if got.agentsIgnoreFeature != agentsSectionSkills {
		t.Errorf("agentsIgnoreFeature = %v, want agentsSectionSkills", got.agentsIgnoreFeature)
	}
	if got.agentsIgnoreName != "iota-skill" {
		t.Errorf("agentsIgnoreName = %q, want %q", got.agentsIgnoreName, "iota-skill")
	}
	if got.agentsIgnoreOpKey == "" {
		t.Error("expected agentsIgnoreOpKey to be set while armed")
	}
}

func TestHandleAgentsIgnoreConfirmKeyMsg_SecondXExecutesToggleAndClearsConfirm(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "kappa-skill", Source: "o/kappa-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	armed := drive(m, pressRune('x'))
	if !armed.agentsIgnoreConfirm {
		t.Fatal("expected 'x' to arm agentsIgnoreConfirm")
	}

	cmds := (&armed).handleAgentsIgnoreConfirmKeyMsg(pressRune('x').(tea.KeyPressMsg))

	if armed.agentsIgnoreConfirm {
		t.Fatal("expected second 'x' to clear agentsIgnoreConfirm")
	}
	if armed.agentsIgnoreName != "" {
		t.Fatalf("expected agentsIgnoreName to clear, got %q", armed.agentsIgnoreName)
	}
	if armed.agentsIgnoreOpKey != "" {
		t.Fatalf("expected agentsIgnoreOpKey to clear, got %q", armed.agentsIgnoreOpKey)
	}
	if len(cmds) == 0 {
		t.Fatal("expected toggle/reload/summary cmds after confirming ignore")
	}
}

func TestHandleAgentsIgnoreConfirmKeyMsg_AnyOtherKeyCancelsWithoutToggling(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "lambda-skill", Source: "o/lambda-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	for _, key := range []rune{'j', 'z'} {
		armed := drive(m, pressRune('x'))
		if !armed.agentsIgnoreConfirm {
			t.Fatal("expected 'x' to arm agentsIgnoreConfirm")
		}

		got := drive(armed, pressRune(key))
		if got.agentsIgnoreConfirm {
			t.Fatalf("expected pressing %q while armed to cancel agentsIgnoreConfirm, got still armed", key)
		}
		if got.agentsIgnoreName != "" {
			t.Fatalf("expected agentsIgnoreName cleared after cancel via %q, got %q", key, got.agentsIgnoreName)
		}
	}
}

func TestConfirmTimeoutMsg_ClearsAgentsIgnoreConfirmWithoutToggling(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "mu-skill", Source: "o/mu-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	armed := drive(m, pressRune('x'))
	if !armed.agentsIgnoreConfirm {
		t.Fatal("expected 'x' to arm agentsIgnoreConfirm")
	}

	got := drive(armed, confirmTimeoutMsg{gen: armed.confirmGen})

	if got.agentsIgnoreConfirm {
		t.Fatal("expected confirm timeout to clear agentsIgnoreConfirm")
	}
	if got.agentsIgnoreName != "" {
		t.Fatalf("expected agentsIgnoreName cleared by timeout, got %q", got.agentsIgnoreName)
	}
}

func TestAgentsRowHints_IgnoreConfirmArmed_NonIgnoredRowShowsConfirmIgnore(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.agentsIgnoreConfirm = true
	m.agentsIgnoreFeature = agentsSectionSkills
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusInstalled, mark: agentsMarkNone}

	hints := agentsRowHints(m, e)

	if len(hints) != 1 || hints[0].desc != "confirm ignore" {
		t.Fatalf("hints = %+v, want single 'confirm ignore' hint", hints)
	}
}

func TestAgentsRowHints_IgnoreConfirmArmed_IgnoredRowShowsConfirmInclude(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.agentsIgnoreConfirm = true
	m.agentsIgnoreFeature = agentsSectionSkills
	e := agentsAllRow{feature: agentsSectionSkills, status: agentsStatusIgnored, mark: agentsMarkNone}

	hints := agentsRowHints(m, e)

	if len(hints) != 1 || hints[0].desc != "confirm include" {
		t.Fatalf("hints = %+v, want single 'confirm include' hint", hints)
	}
}

func TestActiveConfirmationHelpItems_IgnoreConfirmArmed_NonIgnoredRowShowsConfirmIgnore(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "nu-skill", Source: "o/nu-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor
	m.agentsIgnoreConfirm = true
	m.agentsIgnoreFeature = agentsSectionSkills
	m.agentsIgnoreName = "nu-skill"

	hints := activeConfirmationHelpItems(m)

	if len(hints) != 1 || hints[0].desc != "confirm ignore" {
		t.Fatalf("hints = %+v, want single 'confirm ignore' hint", hints)
	}
}

func TestActiveConfirmationHelpItems_IgnoreConfirmArmed_IgnoredRowShowsConfirmInclude(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "xi-skill", Source: "o/xi-skill", Installed: true}},
		nil, nil,
	)
	m.agentsIgnore = config.AgentsIgnore{Skills: []string{"xi-skill"}}
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor
	m.agentsIgnoreConfirm = true
	m.agentsIgnoreFeature = agentsSectionSkills
	m.agentsIgnoreName = "xi-skill"

	hints := activeConfirmationHelpItems(m)

	if len(hints) != 1 || hints[0].desc != "confirm include" {
		t.Fatalf("hints = %+v, want single 'confirm include' hint", hints)
	}
}

func TestAgentsIgnoreToggle_SpinnerClearsAfterToggledMsg(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "omicron-skill", Source: "o/omicron-skill", Installed: true}},
		nil, nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor
	rows := agentsAllRowsList(m)
	var row agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionSkills {
			row = e
		}
	}
	runKey := agentsRowRunKey(row)

	armed := drive(m, pressRune('x'))
	confirmed := drive(armed, pressRune('x'))

	if confirmed.agentsOpKey != runKey {
		t.Fatalf("agentsOpKey = %q, want %q while op in flight", confirmed.agentsOpKey, runKey)
	}

	got := drive(confirmed, agentsIgnoreToggledMsg{})

	if got.agentsOpKey != "" {
		t.Errorf("agentsOpKey = %q after agentsIgnoreToggledMsg, want empty (spinner cleared)", got.agentsOpKey)
	}
}

func TestAgentsIgnoreFeatureName_Marketplaces(t *testing.T) {
	if got := agentsIgnoreFeatureName(agentsSectionMarketplaces); got != "marketplaces" {
		t.Errorf("agentsIgnoreFeatureName(agentsSectionMarketplaces) = %q, want %q", got, "marketplaces")
	}
}

func TestAgentsAllRowsList_MarketplaceIgnoreParity_ManagedAndUnmanagedRows(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{Name: "managed-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	}
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "orphan-market", Source: "orphan/repo"}},
	}
	m.agentsIgnore = config.AgentsIgnore{Marketplaces: []string{"managed-market", "orphan-market"}}

	rows := agentsAllRowsList(m)

	var managedRow, orphanRow agentsAllRow
	var managedFound, orphanFound bool
	for _, e := range rows {
		if e.feature != agentsSectionMarketplaces {
			continue
		}
		switch e.sortName {
		case "managed-market":
			managedRow, managedFound = e, true
		case "orphan-market":
			orphanRow, orphanFound = e, true
		}
	}
	if !managedFound {
		t.Fatal("expected a row for managed-market")
	}
	if !orphanFound {
		t.Fatal("expected a row for orphan-market")
	}
	if managedRow.status != agentsStatusIgnored {
		t.Errorf("managed-market status = %v, want agentsStatusIgnored", managedRow.status)
	}
	if managedRow.mark != agentsMarkNone {
		t.Errorf("managed-market mark = %v, want agentsMarkNone", managedRow.mark)
	}
	if orphanRow.status != agentsStatusIgnored {
		t.Errorf("orphan-market status = %v, want agentsStatusIgnored", orphanRow.status)
	}
	if orphanRow.mark != agentsMarkNone {
		t.Errorf("orphan-market mark = %v, want agentsMarkNone", orphanRow.mark)
	}
}

func TestAgentsAllRowsList_UnignoredOrphanMarketplaceKeepsOutOfSyncOrphanMark(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "unignored-orphan", Source: "orphan/repo"}},
	}

	rows := agentsAllRowsList(m)
	var found bool
	for _, e := range rows {
		if e.feature == agentsSectionMarketplaces && e.sortName == "unignored-orphan" {
			found = true
			if e.status != agentsStatusOutOfSync {
				t.Errorf("status = %v, want agentsStatusOutOfSync", e.status)
			}
			if e.mark != agentsMarkOrphan {
				t.Errorf("mark = %v, want agentsMarkOrphan", e.mark)
			}
		}
	}
	if !found {
		t.Fatal("expected a row for unignored-orphan")
	}
}

func TestDoAgentsUpdateAll_NoOutdatedPlugins_RunsMarketplacesSynchronously(t *testing.T) {
	m := agentsAllModel(
		nil, nil,
		[]app.PluginRow{{Name: "up-to-date-plugin", Version: "1.0.0", LatestVersion: "1.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.skillsRows = nil

	cmds := (&m).doAgentsUpdateAll()

	if !m.marketplaceRunning {
		t.Error("expected marketplaceRunning=true synchronously when no plugin is outdated")
	}
	if len(cmds) == 0 {
		t.Fatal("expected cmds to be returned")
	}
}

func TestDashboardReconcilePlan_IncludesMissingAgents(t *testing.T) {
	m := agentsAllModel([]app.SkillPackageRow{{Name: "missing", Installed: false}}, nil, nil)
	if !app.DashboardReconcilePlanHasStep(dashboardReconcilePlanItems(m), app.ReconcileStepSyncAgents) {
		t.Fatalf("steps = %#v, want sync-agents step", dashboardReconcilePlanItems(m))
	}
}

func TestDoAgentsUpdateAll_OutdatedPluginsPresent_MarketplacesStillRunSynchronously(t *testing.T) {
	m := agentsAllModel(
		nil, nil,
		[]app.PluginRow{{Name: "old-plugin", Version: "1.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.skillsRows = nil

	cmds := (&m).doAgentsUpdateAll()

	if !m.marketplaceRunning {
		t.Error("expected marketplaceRunning=true synchronously: update-all always refreshes marketplaces before computing outdated plugins")
	}
	if !m.pluginRunning {
		t.Error("expected pluginRunning=true synchronously when the plugins section is enabled")
	}
	if len(cmds) == 0 {
		t.Fatal("expected cmds to be returned")
	}
}

func TestUpdate_AgentsProgressDoneMsg_MarketplaceTrue_KeepsRunningUntilRowsMsg(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRunning = true
	m.marketplaceErr = nil

	got, _ := m.Update(agentsProgressDoneMsg{gen: m.progressGen, marketplace: true})
	got2 := got.(Model)

	if !got2.marketplaceRunning {
		t.Error("expected marketplaceRunning to stay true after a successful done msg until marketplaceRowsMsg lands")
	}

	got3 := drive(got2, marketplaceRowsMsg{})
	if got3.marketplaceRunning {
		t.Error("expected marketplaceRunning=false once marketplaceRowsMsg lands")
	}
}

func TestUpdate_AgentsProgressDoneMsg_MarketplaceError_ClearsRunningImmediately(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRunning = true

	got, _ := m.Update(agentsProgressDoneMsg{gen: m.progressGen, marketplace: true, marketplaceErr: errors.New("refresh failed")})
	got2 := got.(Model)

	if got2.marketplaceRunning {
		t.Error("expected marketplaceRunning=false after an errored done msg (no reload is dispatched)")
	}
	if got2.marketplaceErr == nil {
		t.Error("expected marketplaceErr to be set")
	}
}

func TestUpdate_AgentsProgressDoneMsg_PluginTrueWithoutMarketplace_TriggersMarketplaceReload(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.pluginRunning = true

	got, cmd := m.Update(agentsProgressDoneMsg{gen: m.progressGen, plugin: true, marketplace: false, pluginErr: nil})
	got2 := got.(Model)

	if !got2.pluginRunning {
		t.Error("expected pluginRunning to stay true after a successful done msg until pluginRowsMsg lands")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil batched cmd (plugin + marketplace reload + summary) for plugin-only done message")
	}
}

func TestUpdate_AgentsProgressDoneMsg_StaleGen_NoOp(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRunning = true
	m.progressGen = 5

	got, _ := m.Update(agentsProgressDoneMsg{gen: 1, marketplace: true})
	got2 := got.(Model)

	if !got2.marketplaceRunning {
		t.Error("expected marketplaceRunning to remain true: a stale gen must no-op")
	}
	if got2.progressGen != 5 {
		t.Errorf("progressGen = %d, want unchanged 5 for a stale-gen message", got2.progressGen)
	}
}

func TestAgentsClaimGroupPicker_Marketplace_ConfirmAdoptsAndAssignsGroup(t *testing.T) {
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "unmanaged-market", Source: "owner/unmanaged-market"}},
	}
	e := agentsAllRow{feature: agentsSectionMarketplaces, localIdx: len(m.marketplaceRows), agentID: "claude-code", mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan marketplace row")
	}
	if len(m.pickerGroups) == 0 {
		t.Fatal("expected at least one group option in the picker")
	}
	m.pickerCursor = 0

	var cmds []tea.Cmd
	m.confirmGroupPickerSelection(&cmds)

	if !m.marketplaceRunning {
		t.Error("expected marketplaceRunning=true after confirming claim")
	}
	if len(cmds) == 0 {
		t.Fatal("expected non-empty cmds after confirming claim")
	}
	if m.mode == viewGroupPicker {
		t.Error("expected picker to close on confirm")
	}
}

// newPluginClaimTestApp mirrors newScanPlanTestApp but forces an empty
// plugin-adapter set, so AddMarketplace/AddPlugin only ever write the
// manifest — real adapters would otherwise pick up an actually-installed
// claude/codex CLI on the dev machine and attempt a live network operation.
func newPluginClaimTestApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	a := app.New(filepath.Join(dir, "settings.json"), app.WithPluginAdapters([]app.PluginAdapter{}))
	a.CacheDir = dir
	saveScanPlanTestConfig(t, a, config.Settings{}, "system")
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestPluginNeedsMarketplaceMsg_ArmsOfferConfirm(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "acme-market", Source: "acme/repo"}},
	}
	plugin := app.InstalledPlugin{Name: "acme-plugin", Marketplace: "acme-market"}

	cmd := m.doImportPluginWithGroup("claude-code", plugin, "dev")
	msg := cmd()
	needMsg, ok := msg.(pluginNeedsMarketplaceMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want pluginNeedsMarketplaceMsg", msg)
	}

	got, cmd2 := m.Update(needMsg)
	gm := got.(Model)
	if !gm.pluginMarketplaceOfferConfirm {
		t.Fatal("expected pluginMarketplaceOfferConfirm=true after pluginNeedsMarketplaceMsg")
	}
	if gm.pluginMarketplaceOfferAgentID != "claude-code" || gm.pluginMarketplaceOfferPlugin.Name != "acme-plugin" ||
		gm.pluginMarketplaceOfferGroup != "dev" || gm.pluginMarketplaceOfferMarket != "acme-market" ||
		gm.pluginMarketplaceOfferSource != "acme/repo" {
		t.Errorf("offer fields = %+v, want to match pluginNeedsMarketplaceMsg payload", gm)
	}
	if cmd2 == nil {
		t.Fatal("expected a non-nil cmd (confirmation timeout) after arming the offer")
	}

	hints := agentsRowHints(gm, agentsAllRow{})
	if len(hints) != 2 {
		t.Fatalf("agentsRowHints during offer = %d items, want 2 (confirm + cancel)", len(hints))
	}
	if !strings.Contains(hints[0].desc, "acme-market") {
		t.Errorf("confirm hint desc = %q, want it to mention the marketplace name", hints[0].desc)
	}

	helpItems := activeConfirmationHelpItems(gm)
	if len(helpItems) != 2 {
		t.Fatalf("activeConfirmationHelpItems during offer = %d items, want 2 (confirm + cancel)", len(helpItems))
	}
	if !strings.Contains(helpItems[0].desc, "acme-market") {
		t.Errorf("help confirm desc = %q, want it to mention the marketplace name", helpItems[0].desc)
	}
}

func TestHandlePluginMarketplaceOfferConfirmKeyMsg_ConfirmClaimsMarketplaceThenPlugin(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "acme-market", Source: "acme/repo"}},
	}
	plugin := app.InstalledPlugin{Name: "acme-plugin", Marketplace: "acme-market"}
	needMsg := pluginNeedsMarketplaceMsg{agentID: "claude-code", plugin: plugin, group: "dev", marketplaceName: "acme-market", source: "acme/repo"}
	got, _ := m.Update(needMsg)
	m = got.(Model)

	cmds := m.handlePluginMarketplaceOfferConfirmKeyMsg(pressEnter().(tea.KeyPressMsg))

	if m.pluginMarketplaceOfferConfirm || m.pluginMarketplaceOfferAgentID != "" || m.pluginMarketplaceOfferMarket != "" ||
		m.pluginMarketplaceOfferSource != "" || m.pluginMarketplaceOfferPlugin.Name != "" {
		t.Fatalf("offer state not cleared immediately after confirm keypress: %+v", m)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 cmds (spinner.Tick + doClaimPluginAndMarketplace), got %d", len(cmds))
	}

	msg := cmds[1]()
	doneMsg, ok := msg.(pluginImportAdoptDoneMsg)
	if !ok {
		t.Fatalf("second cmd() = %T, want pluginImportAdoptDoneMsg", msg)
	}
	if doneMsg.err != nil {
		t.Fatalf("doneMsg.err = %v, want nil", doneMsg.err)
	}
	if !doneMsg.reloadMarketplaces {
		t.Fatal("expected reloadMarketplaces=true on the combined claim's done msg")
	}

	got2, cmd3 := m.Update(doneMsg)
	gm2 := got2.(Model)
	if gm2.pluginRunning {
		t.Error("expected pluginRunning=false after pluginImportAdoptDoneMsg")
	}
	if cmd3 == nil {
		t.Fatal("expected a non-nil batched cmd (plugin+marketplace reload) for the successful combined claim")
	}

	marketplaces, err := a.Marketplaces()
	if err != nil {
		t.Fatalf("Marketplaces: %v", err)
	}
	var found bool
	for _, mk := range marketplaces {
		if mk.Name == "acme-market" && mk.Source == "acme/repo" {
			found = true
		}
	}
	if !found {
		t.Error("expected acme-market to be persisted to the manifest after the combined claim")
	}

	rows, _, err := a.PluginRows(context.Background())
	if err != nil {
		t.Fatalf("PluginRows: %v", err)
	}
	var pluginRow app.PluginRow
	for _, row := range rows {
		if row.Name == "acme-plugin" {
			pluginRow = row
		}
	}
	if pluginRow.Name == "" {
		t.Fatal("expected acme-plugin to be persisted after the combined claim")
	}
	if len(pluginRow.Groups) != 1 || pluginRow.Groups[0] != "dev" {
		t.Errorf("plugin groups = %v, want [dev]", pluginRow.Groups)
	}

	marketRows, _, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatalf("MarketplaceRows: %v", err)
	}
	var marketRow app.MarketplaceRow
	for _, row := range marketRows {
		if row.Name == "acme-market" {
			marketRow = row
		}
	}
	if marketRow.Name == "" {
		t.Fatal("expected acme-market row after the combined claim")
	}
	if len(marketRow.Groups) != 1 || marketRow.Groups[0] != "dev" {
		t.Errorf("marketplace groups = %v, want [dev]", marketRow.Groups)
	}
}

func TestHandlePluginMarketplaceOfferConfirmKeyMsg_OtherKeyCancelsWithoutWriting(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.pluginRunning = true
	plugin := app.InstalledPlugin{Name: "acme-plugin", Marketplace: "acme-market"}
	needMsg := pluginNeedsMarketplaceMsg{agentID: "claude-code", plugin: plugin, marketplaceName: "acme-market", source: "acme/repo"}
	got, _ := m.Update(needMsg)
	m = got.(Model)

	cmds := m.handlePluginMarketplaceOfferConfirmKeyMsg(pressEsc().(tea.KeyPressMsg))

	if m.pluginMarketplaceOfferConfirm {
		t.Error("expected offer confirm cleared after cancel")
	}
	if m.pluginRunning {
		t.Error("expected pluginRunning=false after cancel")
	}
	if m.agentsOpKey != "" {
		t.Errorf("agentsOpKey = %q, want cleared by clearAgentsOp on cancel", m.agentsOpKey)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 cmd (status only) on cancel, got %d", len(cmds))
	}
	if !strings.Contains(m.statusMsg, "cancelled") || !strings.Contains(m.statusMsg, "acme-plugin") {
		t.Errorf("status text = %q, want it to mention cancelled and the plugin name", m.statusMsg)
	}

	marketplaces, err := a.Marketplaces()
	if err != nil {
		t.Fatalf("Marketplaces: %v", err)
	}
	for _, mk := range marketplaces {
		if mk.Name == "acme-market" {
			t.Fatal("expected acme-market NOT to be written to the manifest on cancel")
		}
	}
}

func TestDoClaimPluginAndMarketplace_MarketplaceSucceedsPluginFailsNoRollback(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	plugin := app.InstalledPlugin{Name: " spacey-plugin ", Marketplace: "acme-market"}

	cmd := m.doClaimPluginAndMarketplace("claude-code", plugin, "dev", "acme-market", "acme/repo")
	msg := cmd()
	doneMsg, ok := msg.(pluginImportAdoptDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want pluginImportAdoptDoneMsg", msg)
	}
	if doneMsg.err == nil {
		t.Fatal("expected an error from the plugin-side group assignment failing after a successful marketplace claim")
	}
	if !strings.Contains(doneMsg.err.Error(), "marketplace") || !strings.Contains(doneMsg.err.Error(), "was added") {
		t.Errorf("err = %q, want it to say the marketplace was added", doneMsg.err.Error())
	}
	if !strings.Contains(doneMsg.err.Error(), "failed") || !strings.Contains(doneMsg.err.Error(), plugin.Name) {
		t.Errorf("err = %q, want it to say the plugin claim failed", doneMsg.err.Error())
	}

	marketplaces, err := a.Marketplaces()
	if err != nil {
		t.Fatalf("Marketplaces: %v", err)
	}
	var found bool
	for _, mk := range marketplaces {
		if mk.Name == "acme-market" {
			found = true
		}
	}
	if !found {
		t.Error("expected acme-market to remain persisted despite the plugin claim failing (no rollback)")
	}
}

func TestDoClaimPluginAndMarketplace_ConflictingUnmanagedSources(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "acme-market", Source: "acme/repo-a"}},
		"codex":       {{Name: "acme-market", Source: "acme/repo-b"}},
	}
	plugin := app.InstalledPlugin{Name: "acme-plugin", Marketplace: "acme-market"}

	cmd := m.doClaimPluginAndMarketplace("claude-code", plugin, "", "acme-market", "acme/repo-a")
	msg := cmd()
	doneMsg, ok := msg.(pluginImportAdoptDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want pluginImportAdoptDoneMsg", msg)
	}
	if doneMsg.err == nil {
		t.Fatal("expected a conflicting-sources error")
	}
	if !strings.Contains(doneMsg.err.Error(), "conflicting sources") {
		t.Errorf("err = %q, want it to mention conflicting sources", doneMsg.err.Error())
	}
	if doneMsg.pluginName != "acme-plugin" {
		t.Errorf("pluginName = %q, want %q", doneMsg.pluginName, "acme-plugin")
	}
}

func TestDoImportPluginWithGroup_NoDiscoverableSource_HardErrors(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	plugin := app.InstalledPlugin{Name: "acme-plugin", Marketplace: "nowhere-market"}

	cmd := m.doImportPluginWithGroup("claude-code", plugin, "")
	msg := cmd()
	doneMsg, ok := msg.(pluginImportAdoptDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want pluginImportAdoptDoneMsg (regression: pluginNeedsMarketplaceMsg must not be returned when no source is discoverable)", msg)
	}
	if doneMsg.err == nil {
		t.Fatal("expected a hard error when no marketplace source is discoverable")
	}
	if !strings.Contains(doneMsg.err.Error(), "no discoverable source") {
		t.Errorf("err = %q, want it to contain %q", doneMsg.err.Error(), "no discoverable source")
	}
}

func TestConfirmTimeoutMsg_ClearsArmedPluginMarketplaceOffer(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.pluginMarketplaceOfferConfirm = true
	m.pluginMarketplaceOfferAgentID = "claude-code"
	m.pluginMarketplaceOfferPlugin = app.InstalledPlugin{Name: "acme-plugin"}
	m.pluginMarketplaceOfferMarket = "acme-market"
	m.pluginMarketplaceOfferSource = "acme/repo"
	m.confirmGen = 3

	got := drive(m, confirmTimeoutMsg{gen: 3})

	if got.pluginMarketplaceOfferConfirm {
		t.Error("expected pluginMarketplaceOfferConfirm cleared by a matching-gen timeout")
	}
	if got.pluginMarketplaceOfferAgentID != "" || got.pluginMarketplaceOfferMarket != "" ||
		got.pluginMarketplaceOfferSource != "" || got.pluginMarketplaceOfferPlugin.Name != "" {
		t.Errorf("offer fields not reset after timeout: %+v", got)
	}
	if got.hasActiveConfirmation() {
		t.Error("expected hasActiveConfirmation()=false after the offer is cleared")
	}
}

func TestConfirmTimeoutMsg_StaleGen_LeavesOfferArmed(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.pluginMarketplaceOfferConfirm = true
	m.pluginMarketplaceOfferMarket = "acme-market"
	m.confirmGen = 3

	got := drive(m, confirmTimeoutMsg{gen: 1})

	if !got.pluginMarketplaceOfferConfirm {
		t.Error("expected pluginMarketplaceOfferConfirm to remain armed for a stale-gen timeout")
	}
	if got.pluginMarketplaceOfferMarket != "acme-market" {
		t.Errorf("pluginMarketplaceOfferMarket = %q, want unchanged %q", got.pluginMarketplaceOfferMarket, "acme-market")
	}
}

// TestDoAgentsRefreshAll_SectionGates verifies "R" dispatches a reload command
// set per section-enabled gate: skills contributes 1 cmd (manifest load), mcp /
// plugins / marketplaces each contribute 2 (spinner tick + row load), and the
// dashboard agents summary is always appended regardless of gates. The
// marketplaces gate shares pluginsEnabled (see marketplacesSectionEnabled).
func TestDoAgentsRefreshAll_SectionGates(t *testing.T) {
	cases := []struct {
		name                   string
		agents, skills         bool
		mcp, plugins           bool
		wantCmds               int
		wantSkillsLoaded       bool
		wantMcpRunning         bool
		wantPluginRunning      bool
		wantMarketplaceRunning bool
	}{
		{"all sections enabled", true, true, true, true, 8, true, true, true, true},
		{"agents disabled gates every section", false, true, true, true, 1, false, false, false, false},
		{"skills only", true, true, false, false, 2, true, false, false, false},
		{"mcp only", true, false, true, false, 3, false, true, false, false},
		{"plugins only also enables marketplaces", true, false, false, true, 5, false, false, true, true},
		{"skills and mcp without plugins", true, true, true, false, 4, true, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newScanPlanTestApp(t)
			m := agentsAllModel(nil, nil, nil)
			m.app = a
			m.ctx = context.Background()
			m.agentsEnabled = tc.agents
			m.skillsEnabled = tc.skills
			m.mcpEnabled = tc.mcp
			m.pluginsEnabled = tc.plugins
			m.skillsLoaded = false

			cmds := m.doAgentsRefreshAll()

			if len(cmds) != tc.wantCmds {
				t.Errorf("len(cmds) = %d, want %d", len(cmds), tc.wantCmds)
			}
			for i, c := range cmds {
				if c == nil {
					t.Errorf("cmds[%d] is nil, want every dispatched cmd non-nil with a live app", i)
				}
			}
			if m.skillsLoaded != tc.wantSkillsLoaded {
				t.Errorf("skillsLoaded = %v, want %v", m.skillsLoaded, tc.wantSkillsLoaded)
			}
			if m.mcpRunning != tc.wantMcpRunning {
				t.Errorf("mcpRunning = %v, want %v", m.mcpRunning, tc.wantMcpRunning)
			}
			if m.pluginRunning != tc.wantPluginRunning {
				t.Errorf("pluginRunning = %v, want %v", m.pluginRunning, tc.wantPluginRunning)
			}
			if m.marketplaceRunning != tc.wantMarketplaceRunning {
				t.Errorf("marketplaceRunning = %v, want %v", m.marketplaceRunning, tc.wantMarketplaceRunning)
			}
		})
	}
}

// flattenCmdMsgs resolves cmd (recursing into tea.BatchMsg) into the flat
// list of messages it would deliver, without feeding them back into Update.
func flattenCmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		type result struct {
			index int
			msgs  []tea.Msg
		}
		results := make(chan result, len(batch))
		parts := make([][]tea.Msg, len(batch))
		pending := 0
		for i, c := range batch {
			if c == nil {
				continue
			}
			pending++
			go func(index int, cmd tea.Cmd) {
				results <- result{index: index, msgs: flattenCmdMsgs(cmd)}
			}(i, c)
		}
		for range pending {
			part := <-results
			parts[part.index] = part.msgs
		}
		var out []tea.Msg
		for _, part := range parts {
			out = append(out, part...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestPluginAgentsSavedMsg_SuccessKeepsSpinnerUntilPluginRowsAndReloadsMarketplaces(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.pluginRunning = true
	opKey := agentsRowRunKey(agentsAllRow{feature: agentsSectionPlugins, localIdx: 0, agentID: "claude-code"})
	m.startAgentsOp(opKey)

	got, cmd := m.Update(pluginAgentsSavedMsg{})
	gm := got.(Model)

	if !gm.pluginRunning {
		t.Error("pluginRunning should stay true after successful pluginAgentsSavedMsg until pluginRowsMsg lands")
	}
	if gm.agentsOpKey != opKey {
		t.Errorf("agentsOpKey = %q, want %q kept until the plugin row reload lands", gm.agentsOpKey, opKey)
	}
	if cmd == nil {
		t.Fatal("expected reload cmds after successful pluginAgentsSavedMsg")
	}
	var sawPluginRows, sawMarketplaceRows bool
	for _, msg := range flattenCmdMsgs(cmd) {
		switch msg.(type) {
		case pluginRowsMsg:
			sawPluginRows = true
		case marketplaceRowsMsg:
			sawMarketplaceRows = true
		}
	}
	if !sawPluginRows {
		t.Error("expected a plugin row reload (pluginRowsMsg) to be dispatched")
	}
	if !sawMarketplaceRows {
		t.Error("expected a marketplace row reload (marketplaceRowsMsg) to be dispatched: installing a plugin may install its marketplace")
	}

	afterMarket := drive(gm, marketplaceRowsMsg{})
	if !afterMarket.pluginRunning {
		t.Error("pluginRunning should survive marketplaceRowsMsg landing first")
	}
	if afterMarket.agentsOpKey != opKey {
		t.Errorf("agentsOpKey = %q after marketplaceRowsMsg, want %q: another section's reload must not kill the plugin row spinner", afterMarket.agentsOpKey, opKey)
	}

	afterPlugin := drive(afterMarket, pluginRowsMsg{})
	if afterPlugin.pluginRunning {
		t.Error("pluginRunning should be false once pluginRowsMsg lands")
	}
	if afterPlugin.agentsOpKey != "" {
		t.Errorf("agentsOpKey = %q after pluginRowsMsg, want empty", afterPlugin.agentsOpKey)
	}
}

func TestPluginAgentsSavedMsg_ErrorClearsRunningAndOpImmediately(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.pluginRunning = true
	m.startAgentsOp(agentsRowRunKey(agentsAllRow{feature: agentsSectionPlugins, localIdx: 0, agentID: "claude-code"}))

	got, _ := m.Update(pluginAgentsSavedMsg{err: errors.New("save failed")})
	gm := got.(Model)

	if gm.pluginRunning {
		t.Error("pluginRunning should be false immediately after an errored pluginAgentsSavedMsg")
	}
	if gm.agentsOpKey != "" {
		t.Errorf("agentsOpKey = %q after error, want empty (no reload is dispatched to clear it later)", gm.agentsOpKey)
	}
	if gm.pluginErr == nil {
		t.Error("pluginErr should be set")
	}
}

func TestPluginRestoreDoneMsg_SuccessReloadsMarketplaceRowsToo(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.pluginRunning = true

	got, cmd := m.Update(pluginRestoreDoneMsg{})
	gm := got.(Model)

	if !gm.pluginRunning {
		t.Error("pluginRunning should stay true after successful pluginRestoreDoneMsg until pluginRowsMsg lands")
	}
	var sawMarketplaceRows bool
	for _, msg := range flattenCmdMsgs(cmd) {
		if _, ok := msg.(marketplaceRowsMsg); ok {
			sawMarketplaceRows = true
		}
	}
	if !sawMarketplaceRows {
		t.Error("expected a marketplace row reload (marketplaceRowsMsg) after plugin restore: restoring plugins may install their marketplaces")
	}
}

func TestPluginRemoveDoneMsg_ErrorStillReloadsRows(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.pluginRunning = true

	got, cmd := m.Update(pluginRemoveDoneMsg{name: "demo", err: errors.New("adapter uninstall failed")})
	gm := got.(Model)

	if gm.pluginRunning {
		t.Error("pluginRunning should be false after an errored pluginRemoveDoneMsg")
	}
	if gm.pluginErr == nil {
		t.Error("pluginErr should be set")
	}
	if cmd == nil {
		t.Fatal("expected a command batch (row reload) even on error: the manifest entry may already be deleted")
	}
	var sawPluginRows bool
	for _, msg := range flattenCmdMsgs(cmd) {
		if _, ok := msg.(pluginRowsMsg); ok {
			sawPluginRows = true
		}
	}
	if !sawPluginRows {
		t.Error("expected a plugin row reload (pluginRowsMsg) after an errored remove")
	}
}

func TestPluginRemoveDoneMsg_SuccessReloadsRows(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.pluginRunning = true
	m.pluginErr = errors.New("stale")

	got, cmd := m.Update(pluginRemoveDoneMsg{name: "demo"})
	gm := got.(Model)

	if gm.pluginErr != nil {
		t.Errorf("pluginErr = %v after successful remove, want nil", gm.pluginErr)
	}
	var sawPluginRows bool
	for _, msg := range flattenCmdMsgs(cmd) {
		if _, ok := msg.(pluginRowsMsg); ok {
			sawPluginRows = true
		}
	}
	if !sawPluginRows {
		t.Error("expected a plugin row reload (pluginRowsMsg) after a successful remove")
	}
}

func TestPluginRemoveDoneMsg_WarningSetsStatusAndReloadsRows(t *testing.T) {
	a := newPluginClaimTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.pluginRunning = true

	got, cmd := m.Update(pluginRemoveDoneMsg{name: "demo", warning: "codex: uninstall unverified"})
	gm := got.(Model)

	if gm.pluginErr != nil {
		t.Errorf("pluginErr = %v for a warning-only remove, want nil", gm.pluginErr)
	}
	if !strings.Contains(gm.statusMsg, "uninstall unverified") {
		t.Errorf("status text = %q, want it to contain the adapter warning", gm.statusMsg)
	}
	var sawPluginRows bool
	for _, msg := range flattenCmdMsgs(cmd) {
		if _, ok := msg.(pluginRowsMsg); ok {
			sawPluginRows = true
		}
	}
	if !sawPluginRows {
		t.Error("expected a plugin row reload (pluginRowsMsg) alongside the warning status")
	}
}

func TestPluginWarningsText_JoinsEntries(t *testing.T) {
	if got := pluginWarningsText(nil); got != "" {
		t.Errorf("pluginWarningsText(nil) = %q, want empty", got)
	}
	warnings := []app.PluginError{
		{AgentID: "codex", Err: errors.New("uninstall unverified")},
		{AgentID: "gemini", Err: errors.New("manifest stale")},
	}
	want := "codex: uninstall unverified; gemini: manifest stale"
	if got := pluginWarningsText(warnings); got != want {
		t.Errorf("pluginWarningsText = %q, want %q", got, want)
	}
}
