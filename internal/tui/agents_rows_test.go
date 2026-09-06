package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

func agentsRowsModel(t *testing.T) Model {
	t.Helper()
	m := baseModel(nil)
	m.width, m.height = 120, 40
	m.mode = viewSkills
	m.cursorHidden = false
	m.agentsRowsKnown = true
	m.agentsRows = []app.AgentsPackageRow{
		{Name: "alpha", Source: "acme/alpha", Version: "1.2.3", Targets: []string{"claude"}, DeployedFiles: 12, Status: app.AgentsPackageInstalled},
		{Name: "bravo", Source: "acme/bravo", Status: app.AgentsPackageMissing},
		{Name: "ghost", Source: "acme/ghost", Version: "0.9.0", Status: app.AgentsPackageOrphaned},
	}
	return m
}

func agentsSectionedModel(t *testing.T) Model {
	t.Helper()
	m := agentsRowsModel(t)
	m.agentsMCPRows = []app.AgentsServiceRow{
		{Name: "litellm-tools", Detail: "http", Targets: []string{"claude", "codex"}, Status: app.AgentsPackageInstalled},
		{Name: "ghost-mcp", Detail: "stdio", Targets: []string{"claude"}, Status: app.AgentsPackageUnavailable},
	}
	m.agentsLSPRows = []app.AgentsServiceRow{
		{Name: "gopls", Detail: "gopls", Targets: []string{"claude"}, Status: app.AgentsPackageInstalled},
	}
	return m
}

func TestAgentsViewRendersServiceSections(t *testing.T) {
	view := agentsSectionedModel(t).viewSkillsBody()
	for _, want := range []string{"Packages", "MCP servers", "LSP servers", "litellm-tools", "http", "ghost-mcp", "unavailable", "gopls"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsEmptySectionsAreOmitted(t *testing.T) {
	m := agentsSectionedModel(t)
	m.agentsLSPRows = nil
	view := m.viewSkillsBody()
	if strings.Contains(view, "LSP servers") {
		t.Fatalf("empty section rendered:\n%s", view)
	}
	if !strings.Contains(view, "MCP servers") {
		t.Fatalf("non-empty section dropped:\n%s", view)
	}
}

func TestAgentsCursorTraversesEverySection(t *testing.T) {
	m := agentsSectionedModel(t)
	if got := m.agentsRowCount(); got != 6 {
		t.Fatalf("row count = %d", got)
	}
	for i := 0; i < 5; i++ {
		m.handleAgentsNavigationKeyMsg(tea.KeyPressMsg{Code: 'j'})
	}
	if m.agentsCursor != 5 {
		t.Fatalf("cursor = %d", m.agentsCursor)
	}
	m.handleAgentsNavigationKeyMsg(tea.KeyPressMsg{Code: 'j'})
	if m.agentsCursor != 0 {
		t.Fatalf("cursor did not wrap: %d", m.agentsCursor)
	}
	m.scrollBy(3)
	if m.agentsCursor != 3 {
		t.Fatalf("wheel cursor = %d", m.agentsCursor)
	}
}

func TestAgentsSummaryCountsEverySurface(t *testing.T) {
	m := agentsSectionedModel(t)
	summary := agentsSummaryText(m)
	for _, want := range []string{"1 unavailable", "3 pkg", "2 mcp", "1 lsp"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
	if !strings.Contains(m.viewSkillsBody(), summary) {
		t.Fatal("summary not rendered")
	}
}

func TestAgentsViewRendersPackageRows(t *testing.T) {
	view := agentsRowsModel(t).viewSkillsBody()
	for _, want := range []string{"alpha", "1.2.3", "claude", "12f", "installed", "bravo", "missing", "ghost", "orphaned"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsCursorMovesThroughRows(t *testing.T) {
	m := agentsRowsModel(t)
	if !m.handleAgentsNavigationKeyMsg(tea.KeyPressMsg{Code: 'j'}) {
		t.Fatal("down key not handled")
	}
	if m.agentsCursor != 1 {
		t.Fatalf("cursor = %d", m.agentsCursor)
	}
	m.handleAgentsNavigationKeyMsg(tea.KeyPressMsg{Code: 'k'})
	m.handleAgentsNavigationKeyMsg(tea.KeyPressMsg{Code: 'k'})
	if m.agentsCursor != 2 {
		t.Fatalf("wrapped cursor = %d", m.agentsCursor)
	}
}

func TestAgentsRefreshKeyChecksReadinessBeforeRowsOrOutdated(t *testing.T) {
	m := agentsRowsModel(t)
	m.app = app.New(filepath.Join(t.TempDir(), "settings.json"))
	m.ctx = context.Background()
	handled, cmds := m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if !handled {
		t.Fatal("R not handled")
	}
	if m.apmRunning || m.apmCommand != "" {
		t.Fatalf("R dispatched an APM command: %q", m.apmCommand)
	}
	if len(cmds) != 2 || !m.agentsReadinessPending || m.agentsRowsGen != 0 || m.agentsOutdatedGen != 1 {
		t.Fatalf("cmds = %#v", cmds)
	}
}

func TestAgentsOutdatedResultDecoratesRowsAndIgnoresStale(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsOutdatedGen = 2
	stale := drive(m, agentsOutdatedMsg{gen: 1, result: app.AgentsOutdatedResult{Rows: []apm.OutdatedRow{{Package: "acme/alpha", Latest: "2.0.0"}}}})
	if stale.agentsRows[0].UpdateAvailable {
		t.Fatal("stale update result applied")
	}
	fresh := drive(m, agentsOutdatedMsg{gen: 2, result: app.AgentsOutdatedResult{Rows: []apm.OutdatedRow{{Package: "acme/alpha", Current: "1.2.3", Latest: "2.0.0"}}, Unknown: 1}})
	if !fresh.agentsRows[0].UpdateAvailable || fresh.agentsRows[0].LatestVersion != "2.0.0" || fresh.agentsOutdatedUnknown != 1 {
		t.Fatalf("fresh result not applied: %#v", fresh.agentsRows[0])
	}
	view := fresh.viewSkillsBody()
	for _, want := range []string{"↑", "1.2.3 → 2.0.0", "update available", "1 updates", "1 package updates could not be checked"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsOutdatedCheckStateAndMutationGate(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsOutdatedChecking = true
	if !strings.Contains(m.viewSkillsBody(), "checking package updates") {
		t.Fatal("checking state not rendered")
	}
	for _, key := range []string{"R", "S", "U", "u", "x"} {
		copy := m
		handled, cmds := copy.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
		if !handled || len(cmds) != 1 || copy.apmRunning {
			t.Fatalf("key %q escaped update-check gate", key)
		}
	}
}

func TestAgentsStartupDispatchesReadinessBeforeRowsAndOutdated(t *testing.T) {
	m := agentsRowsModel(t)
	m.app = app.New(filepath.Join(t.TempDir(), "settings.json"))
	m.ctx = context.Background()
	cmds := m.refreshAgents()
	if len(cmds) != 1 || !m.agentsReadinessPending || m.agentsReadinessGen != 1 || m.agentsRowsGen != 0 || m.agentsOutdatedGen != 1 {
		t.Fatalf("startup state readiness=%d rows=%d outdated=%d checking=%v", m.agentsReadinessGen, m.agentsRowsGen, m.agentsOutdatedGen, m.agentsReadinessPending)
	}
}

func TestAgentsReadinessLoadsOutdatedOnlyWhenReady(t *testing.T) {
	m := agentsRowsModel(t)
	m.app = app.New(filepath.Join(t.TempDir(), "settings.json"))
	m.ctx = context.Background()
	m.agentsReadiness = app.AgentsReadiness{State: app.AgentsReadinessTemplateOnly, CTA: app.AgentsCTASync}
	// Native rows come from the agent clients rather than APM, so they load even when APM is not ready.
	if cmds := m.loadAgentsAfterReadiness(); len(cmds) != 2 || m.agentsRowsGen != 1 || m.agentsNativeGen != 1 || m.agentsOutdatedGen != 0 {
		t.Fatalf("template-only dispatched rows=%d natives=%d outdated=%d cmds=%d", m.agentsRowsGen, m.agentsNativeGen, m.agentsOutdatedGen, len(cmds))
	}
	m.agentsReadiness = app.AgentsReadiness{State: app.AgentsReadinessReady}
	if cmds := m.loadAgentsAfterReadiness(); len(cmds) != 3 || m.agentsRowsGen != 2 || m.agentsNativeGen != 2 || m.agentsOutdatedGen != 1 || !m.agentsOutdatedChecking {
		t.Fatalf("ready dispatched rows=%d natives=%d outdated=%d cmds=%d", m.agentsRowsGen, m.agentsNativeGen, m.agentsOutdatedGen, len(cmds))
	}
}

func TestAgentsReadinessGuidanceUsesManualCTAs(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsReadinessErr = &app.APMRepairError{Kind: app.APMRepairVersionMismatch, Err: errors.New("apm version mismatch")}
	if got := agentsReadinessGuidance(m); !strings.Contains(got, "APM readiness check failed") || strings.Contains(got, "Automatic") {
		t.Fatalf("failure guidance = %q", got)
	}
	m.agentsReadinessErr = nil
	m.agentsReadiness = app.AgentsReadiness{State: app.AgentsReadinessTemplateOnly, CTA: app.AgentsCTASync}
	if got := agentsReadinessGuidance(m); !strings.Contains(got, "S sync") || strings.Contains(got, "R retry") {
		t.Fatalf("template guidance = %q", got)
	}
	m.agentsReadiness = app.AgentsReadiness{State: app.AgentsReadinessInvalid, Details: []string{"APM lockfile exists without a live manifest"}}
	if got := agentsReadinessGuidance(m); !strings.Contains(got, "inspect APM files") || strings.Contains(got, "repair pinned") {
		t.Fatalf("invalid guidance = %q", got)
	}
	m.agentsReadiness = app.AgentsReadiness{State: app.AgentsReadinessInvalid, Details: []string{"apm is not installed", "run omni doctor --fix"}}
	if got := agentsReadinessGuidance(m); !strings.Contains(got, "run omni doctor --fix") {
		t.Fatalf("missing apm guidance = %q", got)
	}
}

func TestDashboardAgentsReadinessPendingOrFailedIsNeverHealthy(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsReadinessPending = true
	for _, row := range []statusListRow{statusAgentsAttentionRow(m), statusAgentUpdatesAttentionRow(m)} {
		if !row.needsAttention || !strings.Contains(row.summary, "Checking APM readiness") {
			t.Fatalf("pending dashboard row = %#v", row)
		}
	}
	m.agentsReadinessPending = false
	m.agentsReadinessErr = &app.APMRepairError{Kind: app.APMRepairVersionMismatch, Err: errors.New("APM version mismatch")}
	for _, row := range []statusListRow{statusAgentsAttentionRow(m), statusAgentUpdatesAttentionRow(m)} {
		if !row.needsAttention || !strings.Contains(row.summary, "APM readiness failure") || row.action.kind != statusActionOpenAgents {
			t.Fatalf("failed dashboard row = %#v", row)
		}
	}
}

func TestAgentsRefreshAlwaysRetriesAutomaticReadiness(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "missing", err: &app.APMRepairError{Kind: app.APMRepairMissing, Err: errors.New("missing")}},
		{name: "version mismatch", err: &app.APMRepairError{Kind: app.APMRepairVersionMismatch, Err: errors.New("mismatch")}},
		{name: "unparseable", err: &app.APMRepairError{Kind: app.APMRepairVersionUnparseable, Err: errors.New("unparseable")}},
		{name: "permission", err: os.ErrPermission},
		{name: "context", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := agentsRowsModel(t)
			m.app = app.New(filepath.Join(t.TempDir(), "settings.json"))
			m.ctx = context.Background()
			m.agentsReadinessErr = test.err
			_, cmds := m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: 'R', Text: "R"})
			if m.apmRunning || len(cmds) != 2 || !m.agentsReadinessPending || m.agentsOutdatedChecking {
				t.Fatalf("automatic retry running=%v pending=%v outdated=%v cmds=%d", m.apmRunning, m.agentsReadinessPending, m.agentsOutdatedChecking, len(cmds))
			}
		})
	}
}

func TestAgentsReadinessRefreshInvalidatesStaleOutdatedResult(t *testing.T) {
	m := agentsRowsModel(t)
	m.app = app.New(filepath.Join(t.TempDir(), "settings.json"))
	m.ctx = context.Background()
	m.agentsRows[0].UpdateAvailable = false
	m.agentsOutdatedGen = 7
	m.agentsOutdatedChecking = true
	m.refreshAgents()
	if m.agentsOutdatedGen != 8 || m.agentsOutdatedChecking {
		t.Fatalf("refresh invalidation gen=%d checking=%v", m.agentsOutdatedGen, m.agentsOutdatedChecking)
	}
	stale := drive(m, agentsOutdatedMsg{gen: 7, result: app.AgentsOutdatedResult{Rows: []apm.OutdatedRow{{Package: "acme/alpha", Latest: "9.0.0"}}}})
	if stale.agentsRows[0].UpdateAvailable {
		t.Fatal("stale outdated result survived readiness refresh")
	}
}

func TestAgentsOutdatedTimeoutIsNonFatal(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsOutdatedGen = 1
	m.agentsOutdatedChecking = true
	next := drive(m, agentsOutdatedMsg{gen: 1, err: context.DeadlineExceeded})
	if next.agentsOutdatedChecking || !errors.Is(next.agentsOutdatedErr, context.DeadlineExceeded) || len(next.agentsRows) != 3 {
		t.Fatalf("timeout state checking=%v err=%v rows=%d", next.agentsOutdatedChecking, next.agentsOutdatedErr, len(next.agentsRows))
	}
	if !strings.Contains(next.viewSkillsBody(), "Update check failed") || !strings.Contains(next.viewSkillsBody(), "R to retry") {
		t.Fatalf("timeout not rendered:\n%s", next.viewSkillsBody())
	}
}

func TestAgentsUpdatesSectionIsFirstAndNavigationUsesItsStableOrder(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsRows[1].Status = app.AgentsPackageInstalled
	m.agentsRows[1].UpdateAvailable = true
	m.agentsRows[1].LatestVersion = "2.0.0"
	visible := m.agentsVisiblePackages()
	if got := []string{visible[0].Name, visible[1].Name, visible[2].Name}; !slices.Equal(got, []string{"bravo", "alpha", "ghost"}) {
		t.Fatalf("visible order = %v", got)
	}
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	updates, packages := strings.Index(view, "Updates Available"), strings.Index(view, "Packages")
	if updates < 0 || packages < 0 || updates >= packages || strings.Index(view, "bravo") >= strings.Index(view, "alpha") {
		t.Fatalf("sections not update-first:\n%s", view)
	}
	row, ok := m.agentsSelectedRow()
	if !ok || row.pkg.Name != "bravo" {
		t.Fatalf("cursor selected %#v", row)
	}
	m.handleAgentsNavigationKeyMsg(tea.KeyPressMsg{Code: 'j'})
	row, _ = m.agentsSelectedRow()
	if row.pkg.Name != "alpha" {
		t.Fatalf("next cursor selected %#v", row)
	}
}

func TestAgentsCompactFooterAlwaysIncludesRefresh(t *testing.T) {
	for _, setup := range []func(*Model){
		func(m *Model) { m.agentsRowsKnown = false },
		func(m *Model) { m.agentsRows = nil },
		func(m *Model) { m.agentsRowsErr = errors.New("load failed") },
	} {
		m := agentsRowsModel(t)
		setup(&m)
		found := false
		for _, binding := range tabShortHelpBindings(&m) {
			found = found || binding.Help().Key == "R"
		}
		if !found {
			t.Fatalf("refresh missing from compact footer: %#v", tabShortHelpBindings(&m))
		}
	}
}

func TestAgentsRefreshClearsStaleOpStateAndRespectsBusy(t *testing.T) {
	m := agentsRowsModel(t)
	m.apmCommand = "omni agents sync"
	m.apmOutput = "note: stale"
	m.apmErr = errors.New("stale failure")
	m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if m.apmCommand != "" || m.apmOutput != "" || m.apmErr != nil {
		t.Fatalf("stale op state kept: %q %q %v", m.apmCommand, m.apmOutput, m.apmErr)
	}

	busy := agentsRowsModel(t)
	busy.apmRunning = true
	busy.apmCommand = "omni agents sync"
	gen := busy.agentsRowsGen
	busy.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if busy.agentsRowsGen != gen || busy.apmCommand == "" {
		t.Fatal("R reloaded mid-operation")
	}
}

func TestAgentsSummaryHiddenBeforeFirstLoad(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsRowsKnown = false
	if strings.Contains(m.viewSkillsBody(), agentsSummaryText(m)) {
		t.Fatalf("summary rendered before the first load:\n%s", m.viewSkillsBody())
	}
}

func TestAgentsRowsReloadAfterAPMCommandDone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTUIFile(t, filepath.Join(home, ".apm", "apm.yml"), "dependencies:\n  apm:\n  - git: acme/alpha\n")

	m := baseModel(nil)
	m.width, m.height = 120, 40
	m.mode = viewSkills
	m.app = app.New(filepath.Join(home, "settings.json"))
	m.ctx = context.Background()
	m.apmRunning = true
	m.agentsRowsKnown = true
	m.agentsSyncActionable = 9
	m.agentsOutdatedChecking = true
	outdatedGen := m.agentsOutdatedGen

	updated, cmd := m.Update(apmCommandDoneMsg{command: "omni agents sync", stdout: "done"})
	next := updated.(Model)
	if next.apmRunning {
		t.Fatal("still running")
	}
	if next.agentsRowsKnown || next.agentsSyncActionable != 0 || next.agentsOutdatedChecking || next.agentsOutdatedGen != outdatedGen+1 {
		t.Fatalf("mutation refresh state known=%v actionable=%d checking=%v outdatedGen=%d", next.agentsRowsKnown, next.agentsSyncActionable, next.agentsOutdatedChecking, next.agentsOutdatedGen)
	}
	_ = cmd // readiness probing is covered separately; feed an accepted non-ready result to test row reload.
	readyModel, readyCmd := next.Update(agentsReadinessMsg{gen: next.agentsReadinessGen, readiness: app.AgentsReadiness{State: app.AgentsReadinessLiveIncomplete, CTA: app.AgentsCTASync}})
	next = readyModel.(Model)
	var rows agentsRowsMsg
	for _, msg := range runBatchCmd(readyCmd) {
		if got, ok := msg.(agentsRowsMsg); ok {
			rows = got
		}
	}
	if rows.err != nil || len(rows.status.Packages) != 1 || rows.status.Packages[0].Name != "alpha" {
		t.Fatalf("rows = %#v err %v", rows.status.Packages, rows.err)
	}

	loaded := drive(next, rows)
	if !loaded.agentsRowsKnown || len(loaded.agentsRows) != 1 {
		t.Fatalf("model rows = %#v", loaded.agentsRows)
	}
	if !strings.Contains(loaded.viewSkillsBody(), "alpha") {
		t.Fatalf("view missing reloaded row:\n%s", loaded.viewSkillsBody())
	}
}

func TestAgentsFooterSurfacesShadowNoteAndStaysVisible(t *testing.T) {
	m := agentsRowsModel(t)
	m.height = 14
	m.agentsRows = append(m.agentsRows, m.agentsRows...)
	m.agentsRows = append(m.agentsRows, m.agentsRows...)
	m.agentsCursor = 0
	m.apmCommand = "omni agents sync"
	m.apmNotices = []string{"note: 4 package file(s) shadowed by user-managed files"}
	view := m.viewSkillsBody()
	for _, want := range []string{"4 package file(s) shadowed", "no action is needed", "omni agents sync", "installed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("footer missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsStatusColumnSurvivesNarrowWidth(t *testing.T) {
	m := agentsRowsModel(t)
	m.width = 80
	m.agentsRows[0].Source = "https://git.example.invalid/a-very/long/organisation/path/to/a/package/repository"
	view := m.viewSkillsBody()
	if !strings.Contains(view, "installed") || !strings.Contains(view, "orphaned") {
		t.Fatalf("status column clipped at width 80:\n%s", view)
	}
}

func TestAgentsStaleRowsMsgIgnored(t *testing.T) {
	m := agentsRowsModel(t)
	m.agentsRowsGen = 4
	stale := drive(m, agentsRowsMsg{gen: 3})
	if len(stale.agentsRows) != 3 {
		t.Fatalf("stale reload clobbered rows: %#v", stale.agentsRows)
	}
	fresh := drive(m, agentsRowsMsg{gen: 4})
	if len(fresh.agentsRows) != 0 {
		t.Fatalf("fresh reload ignored: %#v", fresh.agentsRows)
	}
}

func TestAgentsTraceLogPopupClosesWithEsc(t *testing.T) {
	m := agentsRowsModel(t)
	m.traceLogLoading = true
	if !m.traceLogPopupActive() {
		t.Fatal("trace log popup not active on the agents tab")
	}
	closed := drive(m, pressEsc())
	if closed.traceLogPopupActive() {
		t.Fatal("esc did not close the trace log popup")
	}
}

func TestAgentsMouseWheelMovesCursor(t *testing.T) {
	m := agentsRowsModel(t)
	m.scrollBy(1)
	if m.agentsCursor != 1 {
		t.Fatalf("wheel scroll cursor = %d", m.agentsCursor)
	}
}

func TestAgentsRowsErrorKeepsPreviousRows(t *testing.T) {
	m := drive(agentsRowsModel(t), agentsRowsMsg{err: errors.New("parse APM manifest: bad")})
	if len(m.agentsRows) != 3 {
		t.Fatalf("rows dropped: %#v", m.agentsRows)
	}
	if !strings.Contains(m.viewSkillsBody(), "parse APM manifest") {
		t.Fatalf("error not surfaced:\n%s", m.viewSkillsBody())
	}
}

func TestAgentsViewRendersDriftedRows(t *testing.T) {
	m := agentsSectionedModel(t)
	m.agentsMCPRows[0].Status = app.AgentsPackageDrifted
	view := m.viewSkillsBody()
	if !strings.Contains(view, "drifted") || !strings.Contains(view, iconDrifted) {
		t.Fatalf("drifted row not rendered:\n%s", view)
	}
	if !strings.Contains(view, "1 drifted") {
		t.Fatalf("summary missing the drifted count:\n%s", view)
	}
}

func TestAgentsHarnessNoticesStayVisibleBesideAPMNotices(t *testing.T) {
	m := agentsSectionedModel(t)
	m.height = 12
	m.apmCommand = "omni agents sync"
	m.apmNotices = []string{"note: 4 package file(s) shadowed by user-managed files"}
	m.agentsNotices = []string{"claude: ~/.claude.json could not be parsed, skipped"}
	view := m.viewSkillsBody()
	for _, want := range []string{"4 package file(s) shadowed", "no action is needed", "claude: ~/.claude.json could not be parsed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("footer missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsHarnessNoticesAreCapped(t *testing.T) {
	got := agentsHarnessNoticeLines([]string{"a", "b", "c", "d"})
	if len(got) != 3 || got[2] != "…more harness notices" {
		t.Fatalf("capped notices = %v", got)
	}
}

func TestAgentsRowsMsgStoresHarnessNotices(t *testing.T) {
	m := drive(agentsRowsModel(t), agentsRowsMsg{status: app.AgentsStatus{Notices: []string{"codex: unreadable"}}})
	if len(m.agentsNotices) != 1 || m.agentsNotices[0] != "codex: unreadable" {
		t.Fatalf("notices = %v", m.agentsNotices)
	}
}

func TestAgentsErrorNoticeSurvivesProgressLines(t *testing.T) {
	output := "[>] Installing 14 package(s)...\n[i] one\n[i] two\n[i] three\n[i] four\n[x] Install failed after 0.0s.\n"
	got := capAgentsNotices(apmMarkedLines(output))
	if len(got) != agentsMaxNoticeLines+1 {
		t.Fatalf("notices = %#v", got)
	}
	if got[0] != "[x] Install failed after 0.0s." {
		t.Fatalf("error line displaced by progress: %#v", got)
	}
	if got[len(got)-1] != "3 more APM messages hidden; press e to view the full trace log" {
		t.Fatalf("overflow summary = %q", got[len(got)-1])
	}
}

func TestAgentsSyncNoticesMergeStructuredAndMarkedLines(t *testing.T) {
	got := capAgentsNotices(append(
		[]string{"note: 4 package file(s) shadowed by user-managed files"},
		apmMarkedLines("[i] Installing\n[x] Install failed after 0.0s.\n")...,
	))
	want := []string{
		"[x] Install failed after 0.0s.",
		"note: 4 package file(s) shadowed by user-managed files",
		"[i] Installing",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notices = %#v, want %#v", got, want)
	}
}

func TestAgentsNoticesDeduplicate(t *testing.T) {
	got := capAgentsNotices([]string{"[x] boom", "  [x] boom  ", ""})
	if len(got) != 1 || got[0] != "[x] boom" {
		t.Fatalf("notices = %#v", got)
	}
}

func TestAgentsFooterNoticesWrapInsteadOfClipping(t *testing.T) {
	m := agentsRowsModel(t)
	m.width = 60
	long := strings.TrimSpace(strings.Repeat("diagnostic ", 20))
	m.apmNotices = []string{long}
	lines := m.agentsWrappedNotice(long, m.palette.styleOutdated)
	if len(lines) <= 2 {
		t.Fatalf("long notice was still capped: %#v", lines)
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > m.width {
			t.Fatalf("wrapped line is %d wide at width %d: %q", w, m.width, line)
		}
	}
	if got := strings.Count(strings.Join(lines, " "), "diagnostic"); got != 20 {
		t.Fatalf("wrapped notice lost content: got %d occurrences in %#v", got, lines)
	}
	// A wrap breaks between words; a clip would cut mid-word at the terminal edge.
	if strings.Contains(ansi.Strip(lines[0]), "diagnosti\n") || strings.HasSuffix(strings.TrimSpace(ansi.Strip(lines[0])), "diagnosti") {
		t.Fatalf("wrapped mid-word: %q", lines[0])
	}
	if !strings.Contains(m.viewSkillsBody(), "diagnostic") {
		t.Fatal("notice missing from the footer")
	}
}

func TestAgentsNoticePresentationExplainsSeverityAndAction(t *testing.T) {
	info := "[!] User-scope primitives are fully supported by claude, kiro, and gemini"
	if got := agentsNoticeRank(info); got != 2 {
		t.Fatalf("support information rank = %d, want informational", got)
	}
	if got := agentsNoticeText(info); strings.HasPrefix(got, "Warning:") || !strings.Contains(got, "fully supported") {
		t.Fatalf("support information = %q", got)
	}

	skipped := "[!] 24 files skipped -- local files exist, not managed by APM"
	got := agentsNoticeText(skipped)
	for _, want := range []string{"24 existing local files", "APM does not own them", "press e", "only if APM should manage them"} {
		if !strings.Contains(got, want) {
			t.Fatalf("skipped warning missing %q: %q", want, got)
		}
	}
}

func TestAgentsNoticeFooterFitsShortNarrowFrame(t *testing.T) {
	m := agentsRowsModel(t)
	m.width = 28
	m.height = 12
	m.apmNotices = []string{
		"[!] 24 files skipped -- local files exist, not managed by APM",
		"[!] User-scope primitives are fully supported by claude, kiro, and gemini",
	}
	view := m.viewString()
	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("view is %d lines tall at height %d:\n%s", got, m.height, view)
	}
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "e: full APM log") {
		t.Fatalf("clipped frame lacks an explicit continuation action:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if (strings.Contains(line, "local") || strings.Contains(line, "APM")) && lipgloss.Width(line) > m.width {
			t.Fatalf("line is wider than frame: %q", line)
		}
	}
}

func agentsFilterModel(t *testing.T) Model {
	t.Helper()
	m := agentsSectionedModel(t)
	m.mode = viewSkills
	return m
}

func TestAgentsFilterNarrowsEverySection(t *testing.T) {
	m := agentsFilterModel(t)
	m.openAgentsFilter()
	m.filter.SetValue("gh")
	if got := m.agentsRowCount(); got != 2 {
		t.Fatalf("visible rows = %d", got)
	}
	view := m.viewSkillsBody()
	for _, want := range []string{"ghost", "ghost-mcp", "2/6 shown"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "LSP servers") || strings.Contains(view, "litellm-tools") {
		t.Fatalf("filter did not hide non-matching rows or sections:\n%s", view)
	}
}

func TestAgentsFilterIsCaseInsensitiveOverNameAndDetail(t *testing.T) {
	m := agentsFilterModel(t)
	m.openAgentsFilter()
	m.filter.SetValue("ACME/ALPHA")
	if got := m.agentsRowCount(); got != 1 {
		t.Fatalf("source match = %d rows", got)
	}
	m.filter.SetValue("HTTP")
	if got := m.agentsRowCount(); got != 1 {
		t.Fatalf("detail match = %d rows", got)
	}
}

func TestAgentsFilterKeystrokesDoNotQuitOrSwitchTabs(t *testing.T) {
	m := agentsFilterModel(t)
	handled, _ := m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !handled || !m.agentsSearchActive || !m.filter.Focused() {
		t.Fatalf("/ did not open the filter: handled=%v active=%v focused=%v", handled, m.agentsSearchActive, m.filter.Focused())
	}
	if !m.focusedTextInputActive() {
		t.Fatal("focus gate does not cover the agents filter")
	}
	got := drive(m, pressRune('q'))
	if got.mode != viewSkills {
		t.Fatalf("typing q left the agents tab: mode %v", got.mode)
	}
	if got.filter.Value() != "q" {
		t.Fatalf("filter value = %q", got.filter.Value())
	}
	if tabbed := drive(got, tea.KeyPressMsg{Code: tea.KeyTab}); tabbed.mode != viewSkills {
		t.Fatalf("tab switched away while filtering: mode %v", tabbed.mode)
	}
}

func TestAgentsFilterDoesNotLeakIntoOtherTabs(t *testing.T) {
	m := agentsFilterModel(t)
	m.filter.SetValue("tools-query")
	m.mode = viewList
	var cmds []tea.Cmd
	m.switchMainTab(viewSkills, &cmds)
	m.openAgentsFilter()
	m.filter.SetValue("agents-query")

	m.switchMainTab(viewList, &cmds)
	if m.filter.Value() != "tools-query" {
		t.Fatalf("tools filter clobbered: %q", m.filter.Value())
	}
	m.switchMainTab(viewSkills, &cmds)
	if m.filter.Value() != "agents-query" {
		t.Fatalf("agents filter not restored: %q", m.filter.Value())
	}
}

func TestAgentsFilterEscRestoresEveryRow(t *testing.T) {
	m := agentsFilterModel(t)
	m.openAgentsFilter()
	m.filter.SetValue("gh")
	m.handleAgentsSearchKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.agentsSearchActive || m.filter.Value() != "" {
		t.Fatalf("esc left the filter active: %v %q", m.agentsSearchActive, m.filter.Value())
	}
	if got := m.agentsRowCount(); got != 6 {
		t.Fatalf("rows after esc = %d", got)
	}
	if strings.Contains(m.viewSkillsBody(), "shown") {
		t.Fatal("summary still reports a filter")
	}
}

func TestAgentsHintsListEveryNewKey(t *testing.T) {
	m := agentsFilterModel(t)
	m.help.SetWidth(m.width)
	footer := stripANSIEscapeSequences(m.viewString())
	for _, want := range []string{"filter", "add", "sync all"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("hint bar missing %q:\n%s", want, footer)
		}
	}
	popup := stripANSIEscapeSequences(renderHelpPopupWithWidth(m, m.width))
	for _, want := range []string{"/", "a", "u", "x", "R", "e"} {
		if !strings.Contains(popup, want) {
			t.Fatalf("help popup missing %q:\n%s", want, popup)
		}
	}
}

func TestAgentsCursorRowAlwaysRendersItsDetails(t *testing.T) {
	m := agentsFilterModel(t)
	m.agentsRows[0].License = "MIT"
	m.agentsRows[0].Marketplace = "caveman"
	m.agentsRows[0].Description = "An alpha package."
	m.agentsCursor = 0
	m.cursorHidden = false

	view := m.viewSkillsBody()
	for _, want := range []string{"An alpha package.", "source: acme/alpha", "license: MIT", "via: caveman", "files: 12"} {
		if !strings.Contains(view, want) {
			t.Fatalf("details missing %q without any toggle:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"ref:", "version:", "targets:"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("details still contain %q:\n%s", unwanted, view)
		}
	}
	// Description leads the block, exactly as a tool row's details do.
	if strings.Index(view, "An alpha package.") > strings.Index(view, "source: acme/alpha") {
		t.Fatalf("description is not the first detail line:\n%s", view)
	}
	if !strings.Contains(view, listTextPrefix()+"source: acme/alpha") {
		t.Fatalf("details are not indented with listTextPrefix:\n%s", view)
	}
}

func TestAgentsPackageWithoutDescriptionUsesToolsFallback(t *testing.T) {
	m := agentsFilterModel(t)
	m.cursorHidden = false
	m.agentsRows[0].Description = ""
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(view, listTextPrefix()+"no description available") {
		t.Fatalf("missing description fallback:\n%s", view)
	}
}

func TestAgentsDetailsFollowTheCursor(t *testing.T) {
	m := agentsFilterModel(t)
	m.cursorHidden = false
	m.agentsRows[0].Description = "first row"
	m.agentsRows[1].Description = "second row"
	if view := m.viewSkillsBody(); !strings.Contains(view, "first row") || strings.Contains(view, "second row") {
		t.Fatalf("details are not on the cursor row:\n%s", view)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	view := m.viewSkillsBody()
	if strings.Contains(view, "first row") || !strings.Contains(view, "second row") {
		t.Fatalf("details did not follow the cursor:\n%s", view)
	}
}

func TestAgentsServiceRowDetailsHideSecrets(t *testing.T) {
	m := agentsFilterModel(t)
	m.agentsMCPRows[0] = app.AgentsServiceRow{
		Name: "litellm-tools", Detail: "http", URLHost: "api.invalid",
		Harnesses: []string{"claude", "codex"}, Targets: []string{"claude"}, Status: app.AgentsPackageInstalled,
	}
	m.agentsCursor = len(m.agentsRows)
	m.cursorHidden = false
	view := m.viewSkillsBody()
	for _, want := range []string{"transport: http", "host: api.invalid", "deployed to: claude,codex"} {
		if !strings.Contains(view, want) {
			t.Fatalf("service details missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsEnterIsUnusedOnPackageRows(t *testing.T) {
	m := agentsFilterModel(t)
	m.cursorHidden = false
	handled, cmds := m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	if handled || len(cmds) != 0 {
		t.Fatalf("enter is still bound on a package row: handled=%v cmds=%d", handled, len(cmds))
	}
}

func agentsRowOpModel(t *testing.T) (Model, *executor.MatchMockExecutor) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "apm"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "APM CLI version 0.29.0\n"}},
	).WithFallback(executor.MockCall{Stdout: "[*] done\n"})
	a := app.New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)

	m := agentsFilterModel(t)
	m.app = a
	m.ctx = context.Background()
	m.cursorHidden = false
	m.agentsRows = []app.AgentsPackageRow{
		{Name: "floating", Source: "acme/floating", Version: "1.0.0", Status: app.AgentsPackageInstalled},
		{Name: "pinned", Source: "acme/pinned", Ref: "v2", Version: "2.0.0", Status: app.AgentsPackageInstalled},
		{Name: "local", Source: "_local/local", LocalPath: "/src/local", Version: "1.0.0", Status: app.AgentsPackageInstalled},
		{Name: "stray", Source: "acme/stray", Status: app.AgentsPackageOrphaned},
	}
	m.agentsMCPRows = []app.AgentsServiceRow{{Name: "mcp-one", Detail: "stdio", Status: app.AgentsPackageInstalled}}
	m.agentsLSPRows = nil
	return m, mock
}

func agentsPressRowKey(t *testing.T, m *Model, key string) []tea.Cmd {
	t.Helper()
	handled, cmds := m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	if !handled {
		t.Fatalf("%q not handled", key)
	}
	return cmds
}

func TestAgentsRowUpdateDispatchesTheSpecWithYes(t *testing.T) {
	m, mock := agentsRowOpModel(t)
	m.agentsCursor = 0
	cmds := agentsPressRowKey(t, &m, "u")
	if m.apmCommand != "apm update -g --yes acme/floating" {
		t.Fatalf("command = %q", m.apmCommand)
	}
	runBatchCmd(tea.Batch(cmds...))
	var got []string
	for _, call := range mock.Calls {
		got = append(got, strings.Join(call.Args, " "))
	}
	if !slices.Contains(got, "update -g --yes acme/floating") {
		t.Fatalf("calls = %v", got)
	}
}

func TestAgentsRowUpdateHintsInsteadOfRunningApm(t *testing.T) {
	for name, tc := range map[string]struct {
		cursor int
		want   string
	}{
		"pinned": {1, "pinned to v2"},
		"local":  {2, "local path"},
		"orphan": {3, "not declared in apm.yml"},
		"mcp":    {4, "edited in ~/.apm/apm.yml"},
	} {
		t.Run(name, func(t *testing.T) {
			m, mock := agentsRowOpModel(t)
			m.agentsCursor = tc.cursor
			cmds := agentsPressRowKey(t, &m, "u")
			runBatchCmd(tea.Batch(cmds...))
			if m.apmCommand != "" || len(mock.Calls) != 0 {
				t.Fatalf("dispatched apm: %q %#v", m.apmCommand, mock.Calls)
			}
			if !strings.Contains(m.statusMsg, tc.want) {
				t.Fatalf("status = %q, want %q", m.statusMsg, tc.want)
			}
		})
	}
}

func TestAgentsRowUninstallConfirmLifecycle(t *testing.T) {
	m, mock := agentsRowOpModel(t)
	m.agentsCursor = 0
	agentsPressRowKey(t, &m, "x")
	if m.agentsConfirmIdx != 0 || len(mock.Calls) != 0 {
		t.Fatalf("first x should only arm: idx=%d calls=%#v", m.agentsConfirmIdx, mock.Calls)
	}
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(view, listHintPrefix()+"x confirm uninstall") {
		t.Fatalf("confirm hint is not inline on the selected row:\n%s", view)
	}
	if strings.Contains(view, "x uninstall") || strings.Contains(m.statusMsg, "confirm uninstall") {
		t.Fatalf("normal row hint or footer status survived confirmation: status=%q\n%s", m.statusMsg, view)
	}
	if !m.hasActiveConfirmation() {
		t.Fatal("agents confirm not visible to hasActiveConfirmation")
	}

	timedOut := drive(m, confirmTimeoutMsg{gen: m.confirmGen})
	if timedOut.agentsConfirmIdx != -1 {
		t.Fatal("timeout did not clear the confirm")
	}

	switched, _ := agentsRowOpModel(t)
	switched.agentsCursor = 0
	agentsPressRowKey(t, &switched, "x")
	var cmds []tea.Cmd
	switched.switchMainTab(viewList, &cmds)
	if switched.agentsConfirmIdx != -1 {
		t.Fatal("tab switch did not clear the confirm")
	}

	m, mock = agentsRowOpModel(t)
	m.agentsCursor = 0
	agentsPressRowKey(t, &m, "x")
	accept := agentsPressRowKey(t, &m, "x")
	if m.apmCommand != "apm uninstall -g acme/floating" {
		t.Fatalf("command = %q", m.apmCommand)
	}
	runBatchCmd(tea.Batch(accept...))
	var got []string
	for _, call := range mock.Calls {
		got = append(got, strings.Join(call.Args, " "))
	}
	if !slices.Contains(got, "uninstall -g acme/floating") {
		t.Fatalf("calls = %v", got)
	}
	if !strings.Contains(strings.Join(m.agentsRemovalHint, " "), "next 'omni agents sync' reinstalls it") {
		t.Fatalf("removal hint = %v", m.agentsRemovalHint)
	}
	if !strings.Contains(m.viewSkillsBody(), "remove it from the host template") {
		t.Fatalf("removal hint not rendered:\n%s", m.viewSkillsBody())
	}
}

func TestAgentsRowUninstallUsesTheLocalPathSpec(t *testing.T) {
	m, _ := agentsRowOpModel(t)
	m.agentsCursor = 2
	agentsPressRowKey(t, &m, "x")
	agentsPressRowKey(t, &m, "x")
	if m.apmCommand != "apm uninstall -g /src/local" {
		t.Fatalf("command = %q", m.apmCommand)
	}
}

func TestAgentsRowOpsBlockedWhileAPMRuns(t *testing.T) {
	m, mock := agentsRowOpModel(t)
	m.agentsCursor = 0
	m.apmRunning = true
	agentsPressRowKey(t, &m, "u")
	if len(mock.Calls) != 0 || !m.statusIsErr {
		t.Fatalf("row op ran during another op: %#v", mock.Calls)
	}
}

func TestAgentsRowOpsBlockedWhileReadinessRuns(t *testing.T) {
	m, mock := agentsRowOpModel(t)
	m.agentsCursor = 0
	m.agentsReadinessPending = true
	agentsPressRowKey(t, &m, "u")
	if len(mock.Calls) != 0 || m.apmRunning {
		t.Fatalf("row op ran during readiness: %#v", mock.Calls)
	}
}

func TestAgentsRowOpNoticesDropScopeWideSummaries(t *testing.T) {
	output := "[>] Resolving acme/floating...\n[i] Updated 3 APM dependencies.\n[-] other (removed)\n[!] 1 dependency unpinned\n[x] boom\n"
	got := agentsRowOpNotices(output)
	for _, unwanted := range []string{"Updated 3 APM dependencies.", "(removed)"} {
		if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, unwanted) }) {
			t.Fatalf("scope-wide line survived: %#v", got)
		}
	}
	if !slices.Contains(got, "[x] boom") || !slices.Contains(got, "[!] 1 dependency unpinned") {
		t.Fatalf("error/warning dropped: %#v", got)
	}
}

func TestAgentsRowHintsLiveOnTheCursorRowOnly(t *testing.T) {
	m, _ := agentsRowOpModel(t)
	m.cursorHidden = false
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(view, "u update") || !strings.Contains(view, "x uninstall") {
		t.Fatalf("row hints missing from the cursor row:\n%s", view)
	}
	if !strings.Contains(view, listHintPrefix()+strings.TrimLeft(strings.SplitN(strings.SplitAfter(view, listHintPrefix())[1], "\n", 2)[0], "")) {
		t.Fatalf("row hints are not indented with listHintPrefix:\n%s", view)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	pinned := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(pinned, "u update") {
		t.Fatalf("pinned row offered update:\n%s", pinned)
	}
	if !strings.Contains(pinned, "pinned to v2") {
		t.Fatalf("pinned row did not explain its limitation:\n%s", pinned)
	}
}

func TestAgentsFooterCarriesOnlyTabWideActions(t *testing.T) {
	m, _ := agentsRowOpModel(t)
	m.help.SetWidth(m.width)
	legend := stripANSIEscapeSequences(m.help.View(tabKeyMap{&m}))
	for _, want := range []string{"sync all", "upgrade all", "add", "logs", "filter"} {
		if !strings.Contains(legend, want) {
			t.Fatalf("footer missing tab-wide action %q: %q", want, legend)
		}
	}
	for _, unwanted := range []string{"update row", "uninstall row", "u update", "x uninstall", "details"} {
		if strings.Contains(legend, unwanted) {
			t.Fatalf("footer still carries row op %q: %q", unwanted, legend)
		}
	}
	popup := stripANSIEscapeSequences(renderHelpPopupWithWidth(m, m.width))
	if !strings.Contains(popup, "update") || !strings.Contains(popup, "uninstall") {
		t.Fatalf("help popup lost the row ops:\n%s", popup)
	}
}

func TestAgentsRowOpErrorRendersOnTheRow(t *testing.T) {
	m, _ := agentsRowOpModel(t)
	m.cursorHidden = false
	m.agentsRowOpSpec = "acme/floating"
	m.apmErr = errors.New("apm uninstall failed: exit status 1")
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	rowIdx := strings.Index(view, "floating")
	errIdx := strings.Index(view, "apm uninstall failed")
	detailIdx := strings.Index(view, "source: acme/floating")
	if rowIdx < 0 || errIdx < 0 || detailIdx < 0 {
		t.Fatalf("row, error and details are not all present:\n%s", view)
	}
	if !(rowIdx < errIdx && errIdx < detailIdx) {
		t.Fatalf("error is not between the row and its details:\n%s", view)
	}
	if strings.Count(view, "apm uninstall failed") != 1 {
		t.Fatalf("row error duplicated into the footer:\n%s", view)
	}
}

func TestAgentsRunningRowOpRendersOnTheSelectedRow(t *testing.T) {
	m, _ := agentsRowOpModel(t)
	m.cursorHidden = false
	m.apmRunning = true
	m.apmCommand = "apm update -g --yes acme/floating"
	m.agentsRowOpSpec = "acme/floating"
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	var operationLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, m.apmCommand) {
			operationLine = line
		}
	}
	if operationLine == "" || !strings.HasPrefix(operationLine, listTextPrefix()) || !strings.Contains(operationLine, "ctrl+c quit") {
		t.Fatalf("running operation is not in the selected row detail block:\n%s", view)
	}
	if strings.Count(view, m.apmCommand) != 1 {
		t.Fatalf("running operation duplicated into the footer:\n%s", view)
	}
}

func agentsRegistryModel(t *testing.T) (Model, *executor.MatchMockExecutor) {
	t.Helper()
	m, mock := agentsRowOpModel(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeTUIFile(t, filepath.Join(home, ".apm", "marketplaces.json"),
		`{"marketplaces":[{"name":"superpowers-dev","owner":"obra","repo":"superpowers"}]}`)
	writeTUIFile(t, filepath.Join(home, ".apm", "cache", "marketplace", "superpowers-dev.json"),
		`{"name":"superpowers-dev","owner":{"name":"obra"},"plugins":[{"name":"superpowers","description":"skills","version":"6.3.0","source":"./"},{"name":"zz-brainstorming","description":"ideas","source":{"source":"git-subdir","url":"https://github.com/obra/superpowers","path":"plugins/brainstorming"}}]}`)
	writeTUIFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"),
		"dependencies:\n- repo_url: obra/superpowers\n  name: superpowers\n")
	return m, mock
}

func loadAgentsRegistry(t *testing.T, m Model, cmds []tea.Cmd) Model {
	t.Helper()
	for _, msg := range runBatchCmd(tea.Batch(cmds...)) {
		if got, ok := msg.(agentsRegistryMsg); ok {
			return drive(m, got)
		}
	}
	t.Fatal("no registry message produced")
	return m
}

func TestAgentsRegistryModeRendersAndMarksInstalled(t *testing.T) {
	m, _ := agentsRegistryModel(t)
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	if !m.agentsRegistryMode || len(m.agentsRegistry) != 2 {
		t.Fatalf("registry = %#v", m.agentsRegistry)
	}
	view := m.viewSkillsBody()
	for _, want := range []string{"Registry", "superpowers", "zz-brainstorming", "superpowers-dev", "installed", "available", "2/2 plugins"} {
		if !strings.Contains(view, want) {
			t.Fatalf("registry view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Packages") || strings.Contains(view, "MCP servers") {
		t.Fatalf("normal sections still rendered:\n%s", view)
	}
}

func TestAgentsRegistryFiltersAndInstalls(t *testing.T) {
	m, mock := agentsRegistryModel(t)
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	m.filter.SetValue("brain")
	if got := m.agentsRowCount(); got != 1 {
		t.Fatalf("filtered registry = %d", got)
	}
	m.agentsCursor = 0
	m.handleAgentsRegistryEnter()
	if m.agentsConfirmIdx != 0 || len(mock.Calls) != 0 {
		t.Fatalf("first enter should only arm: %d %#v", m.agentsConfirmIdx, mock.Calls)
	}
	cmds := m.handleAgentsRegistryEnter()
	if m.apmCommand != "apm install -g zz-brainstorming@superpowers-dev" {
		t.Fatalf("command = %q", m.apmCommand)
	}
	runBatchCmd(tea.Batch(cmds...))
	var got []string
	for _, call := range mock.Calls {
		got = append(got, strings.Join(call.Args, " "))
	}
	if !slices.Contains(got, "install -g zz-brainstorming@superpowers-dev") {
		t.Fatalf("calls = %v", got)
	}
	if !strings.Contains(strings.Join(m.agentsRemovalHint, " "), "declare it in the host template") {
		t.Fatalf("install hint = %v", m.agentsRemovalHint)
	}
}

func TestAgentsRegistryBlocksInstalledEntriesAndExits(t *testing.T) {
	m, mock := agentsRegistryModel(t)
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	m.filter.SetValue("superpowers")
	entries := m.agentsVisibleRegistry()
	for i, entry := range entries {
		if entry.Name == "superpowers" {
			m.agentsCursor = i
		}
	}
	m.handleAgentsRegistryEnter()
	if len(mock.Calls) != 0 || !strings.Contains(m.statusMsg, "already installed") {
		t.Fatalf("installed entry dispatched: %#v status=%q", mock.Calls, m.statusMsg)
	}
	m.handleAgentsSearchKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.agentsRegistryMode || m.agentsSearchActive || len(m.agentsRegistry) != 0 {
		t.Fatalf("esc did not leave registry mode: %+v", m.agentsRegistryMode)
	}
	if !strings.Contains(m.viewSkillsBody(), "Packages") {
		t.Fatalf("normal sections not restored:\n%s", m.viewSkillsBody())
	}
}

func TestAgentsRegistryReportsAnEmptyCache(t *testing.T) {
	m, _ := agentsRowOpModel(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeTUIFile(t, filepath.Join(home, ".apm", "marketplaces.json"),
		`{"marketplaces":[{"name":"uncached","owner":"acme","repo":"uncached"}]}`)
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	if !strings.Contains(m.viewSkillsBody(), "apm marketplace update") {
		t.Fatalf("empty-cache notice missing:\n%s", m.viewSkillsBody())
	}
}

func TestAgentsRegistryAndFilterAreMutuallyExclusive(t *testing.T) {
	m, _ := agentsRegistryModel(t)
	m.openAgentsFilter()
	m.filter.SetValue("gh")
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	if m.filter.Value() != "" {
		t.Fatalf("registry mode inherited the row filter: %q", m.filter.Value())
	}
	m.filter.SetValue("brain")
	handled, _ := m.handleAgentsGlobalActionKeyMsg(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !handled || !m.agentsRegistryMode || m.filter.Value() != "brain" {
		t.Fatalf("/ disturbed registry mode: handled=%v registry=%v query=%q", handled, m.agentsRegistryMode, m.filter.Value())
	}
}

func TestAgentsRegistryCursorRespondsToRealKeyMessages(t *testing.T) {
	m, mock := agentsRegistryModel(t)
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	if m.agentsCursor != 0 || m.cursorHidden {
		t.Fatalf("registry opened without a visible selection: cursor=%d hidden=%v", m.agentsCursor, m.cursorHidden)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.agentsCursor != 1 {
		t.Fatalf("down did not move the selection: %d", m.agentsCursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.agentsCursor != 0 {
		t.Fatalf("up did not move the selection: %d", m.agentsCursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if m.agentsCursor != 1 {
		t.Fatalf("ctrl+n did not move the selection: %d", m.agentsCursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.agentsCursor != m.agentsRowCount()-1 {
		t.Fatalf("end did not jump to the last entry: %d", m.agentsCursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.agentsCursor != 0 {
		t.Fatalf("home did not jump to the first entry: %d", m.agentsCursor)
	}

	// j and k are text, not navigation, while the query has focus.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = drive(m, pressRune('j'))
	if m.filter.Value() != "j" {
		t.Fatalf("j navigated instead of typing: value=%q", m.filter.Value())
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyBackspace})

	// Selection lands on the second entry, and enter installs that one.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	entries := m.agentsVisibleRegistry()
	want := entries[m.agentsCursor]
	if want.Installed {
		t.Fatalf("test needs an installable entry at index 1, got %+v", want)
	}
	m = drive(m, pressEnter())
	if m.agentsConfirmIdx != m.agentsCursor {
		t.Fatalf("enter did not arm the install confirm: %d", m.agentsConfirmIdx)
	}
	m = drive(m, pressEnter())
	if m.apmCommand != "apm install -g "+want.Spec() {
		t.Fatalf("enter installed %q, want %q", m.apmCommand, want.Spec())
	}
	_ = mock
}

func TestAgentsRegistryFilterReclampsTheSelection(t *testing.T) {
	m, _ := agentsRegistryModel(t)
	m = loadAgentsRegistry(t, m, m.openAgentsRegistry())
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.agentsCursor == 0 {
		t.Fatal("end did not move off the first entry")
	}
	for _, r := range "brain" {
		m = drive(m, pressRune(r))
	}
	if got := m.agentsRowCount(); got != 1 {
		t.Fatalf("filter narrowed to %d entries", got)
	}
	if m.agentsCursor >= m.agentsRowCount() {
		t.Fatalf("selection %d outside the narrowed list", m.agentsCursor)
	}
}

func TestAgentsFilterModeNavigatesWhileTyping(t *testing.T) {
	m := agentsFilterModel(t)
	m.mode = viewSkills
	m.openAgentsFilter()
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.agentsCursor != 1 {
		t.Fatalf("down did not move the row cursor while filtering: %d", m.agentsCursor)
	}
	// k types; a narrower list resets the selection to the top rather than leaving it out of range.
	m = drive(m, pressRune('k'))
	if m.filter.Value() != "k" {
		t.Fatalf("k navigated instead of typing: value=%q", m.filter.Value())
	}
	if m.agentsCursor >= max(m.agentsRowCount(), 1) {
		t.Fatalf("selection %d outside the narrowed list", m.agentsCursor)
	}
}

func TestAgentsHeaderAndSearchFollowTheDotsPattern(t *testing.T) {
	m := agentsFilterModel(t)
	m.cursorHidden = false
	view := m.viewSkillsBody()
	if strings.Contains(view, "Manifest:") {
		t.Fatalf("header still carries a label:\n%s", view)
	}
	wantPath := m.palette.styleHelp.PaddingLeft(2).Render(truncatePath(tildePath(agentsWorkspacePath(m)), max(rowAvailableWidth(m.width)-2, 1)))
	if !strings.Contains(view, wantPath) {
		t.Fatalf("path line is not rendered the dots way:\n%s", stripANSIEscapeSequences(view))
	}
	dotsModel := baseModel(nil)
	setDotsRepoForTest(&dotsModel, agentsWorkspacePath(m))
	dotsModel.dotsLoaded = true
	dotsModel.dotsEntries = []app.DotStatus{{Name: "zsh", TargetPath: "~/.zshrc", State: dots.StateSynced}}
	dotsView := stripANSIEscapeSequences(renderDots(dotsModel))
	lineAfterPathIsBlank := func(rendered, path string) bool {
		lines := strings.Split(rendered, "\n")
		for i, line := range lines[:len(lines)-1] {
			if strings.Contains(line, path) {
				return strings.TrimSpace(lines[i+1]) == ""
			}
		}
		t.Fatalf("path %q missing from view:\n%s", path, rendered)
		return false
	}
	agentsBlank := lineAfterPathIsBlank(stripANSIEscapeSequences(view), "~/.apm/apm.yml")
	dotsBlank := lineAfterPathIsBlank(dotsView, "~/.apm/apm.yml")
	if agentsBlank != dotsBlank || agentsBlank {
		t.Fatalf("agents path spacing differs from dots: agents blank=%t dots blank=%t", agentsBlank, dotsBlank)
	}

	m.openAgentsFilter()
	view = m.viewSkillsBody()
	if !strings.Contains(view, renderDotsSearchControl(m)) {
		t.Fatalf("search control does not match the dots control:\n%s", stripANSIEscapeSequences(view))
	}
	plain := strings.Split(stripANSIEscapeSequences(view), "\n")
	if !strings.HasPrefix(plain[0], "  /") {
		t.Fatalf("search control is not the first body line: %q", plain[0])
	}
	if strings.TrimSpace(plain[1]) != "~/.apm/apm.yml" {
		t.Fatalf("path line does not follow the search control: %q", plain[1])
	}
}
