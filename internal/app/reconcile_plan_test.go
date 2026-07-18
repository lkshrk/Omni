package app_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
	isync "github.com/lkshrk/omni/internal/sync"
)

func TestDashboardToolSummary_ClassifiesToolsAndDiscoveredRows(t *testing.T) {
	summary := app.BuildDashboardToolSummary(app.DashboardToolSummaryInput{
		Tools: []*app.ToolView{
			{Name: "git", Provider: "brew", Tracked: true, Installed: true, Outdated: true, LatestVersion: "2.0"},
			{Name: "fd", Provider: "brew", Tracked: true, Installed: false},
			{Name: "jq", Provider: "system", Tracked: true, Installed: true, InstalledWith: "apt"},
			{Name: "bat", Provider: "brew", Tracked: true, Installed: true},
			{Name: "slack", Provider: "brew", Tracked: true, Installed: true},
		},
		DiscoveredTools: []*app.ToolView{
			{Name: "ripgrep", Provider: "brew", Installed: true},
		},
		IgnoredTools:           map[string]bool{"slack": true},
		EffectiveSystemManager: "brew",
		EffectivePythonManager: "uv",
		EffectiveNodeManager:   "bun",
		ToolProviderPins:       map[string]string{},
	})

	if summary.Tracked != 5 || summary.Updates != 1 || summary.OutOfSync != 3 || summary.Installed != 5 || summary.Ignored != 1 {
		t.Fatalf("summary counts = %+v, want tracked=5 updates=1 outOfSync=3 installed=5 ignored=1", summary)
	}
	if !reflect.DeepEqual(summary.UpdateNames, []string{"git (2.0)"}) {
		t.Fatalf("update names = %v", summary.UpdateNames)
	}
	if !reflect.DeepEqual(summary.OutOfSyncNames, []string{"fd missing", "jq provider mismatch", "ripgrep local only"}) {
		t.Fatalf("out-of-sync names = %v", summary.OutOfSyncNames)
	}
	if !reflect.DeepEqual(summary.InstalledNames, []string{"bat", "git", "jq", "ripgrep", "slack"}) {
		t.Fatalf("installed names = %v", summary.InstalledNames)
	}
	if !reflect.DeepEqual(summary.IgnoredNames, []string{"slack"}) {
		t.Fatalf("ignored names = %v", summary.IgnoredNames)
	}
}

func TestReconcileSummaryCountsToolsDotsAndIssues(t *testing.T) {
	result := &app.ReconcileResult{
		SyncAll: &app.SyncAllResult{
			SyncResult: &isync.SyncResult{Ops: []isync.SyncOp{
				{Kind: isync.OpInstall},
				{Kind: isync.OpFailed},
			}},
			ClaimedNames: []string{"ripgrep"},
			Failures:     []app.BulkToolError{{Name: "fd", Provider: "brew"}},
		},
		UpgradeAll: &app.UpgradeAllResult{
			Upgraded: []string{"git", "go"},
			Failures: []app.BulkToolError{{Name: "node", Provider: "node"}},
		},
		DotsOps:       []dots.Op{{Kind: dots.OpLink}, {Kind: dots.OpSkip}},
		DotsCommitted: true,
		DotsEntries: []app.DotStatus{
			{Health: app.HealthConflict},
			{Health: app.HealthMissing},
			{Health: app.HealthNoSource},
			{Health: app.HealthOK},
		},
	}

	summary := app.SummarizeReconcile(result)
	if summary.Installed != 1 || summary.Claimed != 1 || summary.Upgraded != 2 || summary.DotOps != 2 || !summary.DotsCommitted {
		t.Fatalf("SummarizeReconcile = %+v, want installed=1 claimed=1 upgraded=2 dotOps=2 committed", summary)
	}

	issues := app.SummarizeReconcileIssues(result)
	if issues.SyncFailures != 1 || issues.UpgradeFailures != 1 || issues.DotsConflicts != 1 || issues.DotsMissing != 2 {
		t.Fatalf("SummarizeReconcileIssues = %+v, want 1 sync failure, 1 upgrade failure, 1 conflict, 2 missing", issues)
	}
	if !issues.HasIssues() || issues.Total() != 5 {
		t.Fatalf("issue helpers = HasIssues:%v Total:%d, want true/5", issues.HasIssues(), issues.Total())
	}
	if app.SummarizeReconcileIssues(nil).HasIssues() {
		t.Fatal("nil reconcile result should not have issues")
	}
}

func TestDashboardToolSyncQueuedNamesLabelsPendingIssues(t *testing.T) {
	names := app.DashboardToolSyncQueuedNames(app.DashboardToolActivityInput{
		Tools: []*app.ToolView{
			{Name: "fd", Provider: "brew", Tracked: true, Installed: false},
			{Name: "git", Provider: "brew", Tracked: true, Installed: true},
		},
		DiscoveredTools: []*app.ToolView{
			{Name: "ripgrep", Provider: "brew", Installed: true},
		},
		PendingKeys: map[string]bool{
			app.ToolRowKey("fd", "brew"):      true,
			app.ToolRowKey("git", "brew"):     true,
			app.ToolRowKey("ripgrep", "brew"): true,
		},
	})

	want := []string{"fd missing", "ripgrep local only"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("DashboardToolSyncQueuedNames = %v, want %v", names, want)
	}
}

func TestDashboardToolSyncBusyUsesQueuedNamesAndProgressFallback(t *testing.T) {
	base := app.DashboardToolActivityInput{
		Loading: true,
		Tools: []*app.ToolView{
			{Name: "fd", Provider: "brew", Tracked: true, Installed: false},
		},
	}

	queued := base
	queued.PendingKeys = map[string]bool{app.ToolRowKey("fd", "brew"): true}
	if !app.DashboardToolSyncBusy(queued) {
		t.Fatal("DashboardToolSyncBusy with queued missing tool = false, want true")
	}

	progress := base
	progress.ProgressText = "Installing tools 1/1"
	if !app.DashboardToolSyncBusy(progress) {
		t.Fatal("DashboardToolSyncBusy with install progress = false, want true")
	}

	upgrade := base
	upgrade.UpgradeBusy = true
	upgrade.ProgressText = "Syncing tools 1/1"
	if app.DashboardToolSyncBusy(upgrade) {
		t.Fatal("DashboardToolSyncBusy while upgrade busy = true, want false")
	}

	idle := base
	idle.Loading = false
	idle.ProgressText = "Syncing tools 1/1"
	if app.DashboardToolSyncBusy(idle) {
		t.Fatal("DashboardToolSyncBusy while not loading = true, want false")
	}
}

func TestDashboardUpgradeNamesSplitsActiveAndWaitingUpdates(t *testing.T) {
	active, waiting := app.DashboardUpgradeNames(app.DashboardToolActivityInput{
		Tools: []*app.ToolView{
			{Name: "bat", Provider: "brew", Tracked: true, Installed: true, Outdated: true, LatestVersion: "1.0"},
			{Name: "fd", Provider: "brew", Tracked: true, Installed: true, Outdated: true},
			{Name: "git", Provider: "brew", Tracked: true, Installed: true, Outdated: true, LatestVersion: "2.46"},
			{Name: "curl", Provider: "brew", Tracked: true, Installed: true},
		},
		ActiveKeys: map[string]bool{
			app.ToolRowKey("fd", "brew"): true,
		},
		PendingKeys: map[string]bool{
			app.ToolRowKey("git", "brew"):  true,
			app.ToolRowKey("curl", "brew"): true,
		},
		RowKey: app.ToolRowKey("bat", "brew"),
	})

	if want := []string{"bat (1.0)", "fd"}; !reflect.DeepEqual(active, want) {
		t.Fatalf("active upgrade names = %v, want %v", active, want)
	}
	if want := []string{"git (2.46)"}; !reflect.DeepEqual(waiting, want) {
		t.Fatalf("waiting upgrade names = %v, want %v", waiting, want)
	}
}

func TestDashboardReconcilePlan_EmptyHealthySnapshotHasNoSteps(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{
		Tools: []*app.ToolView{
			{Name: "git", Provider: "brew", Tracked: true, Installed: true},
		},
		DotsRepo: "/repo",
		DotsEntries: []app.DotStatus{
			{Name: "zsh", State: dots.StateSynced, Counts: app.DotFileCounts{Synced: 1}},
		},
	})

	if len(steps) != 0 {
		t.Fatalf("steps = %#v, want no actionable reconcile steps", steps)
	}
}

func TestDashboardReconcilePlan_PlansActionableToolDotAndIgnoreSteps(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{
		Tools: []*app.ToolView{
			{Name: "fd", Provider: "brew", Tracked: true, Installed: false},
			{Name: "git", Provider: "brew", Tracked: true, Installed: true, Outdated: true},
		},
		DiscoveredTools: []*app.ToolView{
			{Name: "ripgrep", Provider: "brew", Installed: true},
		},
		DotsRepo: "/repo",
		DotsEntries: []app.DotStatus{
			{Name: "nvim", State: dots.StateConflict, Counts: app.DotFileCounts{OutOfSync: 2}},
		},
		DotsGitStatus: " M dotfiles/zsh/.zshrc\n?? dotfiles/nvim/init.lua",
		Doctor: &app.DoctorResult{Checks: []app.DoctorCheck{
			{ID: "dots.ignore", Status: app.DoctorStatusWarn, Message: "2 ignore patterns need cleanup"},
		}},
	})

	wantIDs := []app.DashboardReconcileStepID{
		app.ReconcileStepSyncTools,
		app.ReconcileStepUpgradeTools,
		app.ReconcileStepSyncDots,
		app.ReconcileStepCommitDots,
		app.ReconcileStepFixIgnore,
	}
	if got := reconcileStepIDs(steps); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("step IDs = %#v, want %#v; steps=%#v", got, wantIDs, steps)
	}
	if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepSyncTools) {
		t.Fatalf("steps = %#v, want sync tools step", steps)
	}

	assertReconcileDetail(t, steps, app.ReconcileStepSyncTools, "install 1 missing tool, add 1 local tool")
	assertReconcileDetail(t, steps, app.ReconcileStepUpgradeTools, "1 outdated tool")
	assertReconcileDetail(t, steps, app.ReconcileStepSyncDots, "2 out-of-sync entries")
	assertReconcileDetail(t, steps, app.ReconcileStepCommitDots, "2 repo changes")
	assertReconcileDetail(t, steps, app.ReconcileStepFixIgnore, "2 ignore patterns need cleanup")
}

func TestDashboardReconcilePlan_IncludesMissingAgents(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{AgentsOutOfSync: 2})
	if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepSyncAgents) {
		t.Fatalf("steps = %#v, want sync-agents step", steps)
	}
	assertReconcileDetail(t, steps, app.ReconcileStepSyncAgents, "2 missing agent items")
}

func TestDashboardReconcilePlan_IncludesFixNvmManagedStepFromDoctorDrift(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{
		Doctor: &app.DoctorResult{
			Checks: []app.DoctorCheck{{
				ID:     "drift",
				Status: app.DoctorStatusWarn,
				Groups: []app.DoctorDetailGroup{{
					Header: "nvm-managed binary (configured for system provider)",
					Items:  []string{"pnpm [brew]", "  suggestion: migrate"},
				}},
			}},
		},
	})
	if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepFixNvmManaged) {
		t.Fatalf("steps = %#v, want fix-nvm-managed from doctor drift", steps)
	}
}

func TestDashboardReconcilePlan_IncludesFixNvmManagedStep(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{
		Tools: []*app.ToolView{
			{Name: "pnpm", Provider: "brew", Tracked: true, Installed: true},
		},
		NvmManaged: map[string]bool{"pnpm": true},
	})
	if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepFixNvmManaged) {
		t.Fatalf("steps = %#v, want fix-nvm-managed step", steps)
	}
	assertReconcileDetail(t, steps, app.ReconcileStepFixNvmManaged, "1 nvm-managed tool")
	if got := reconcileStepIDs(steps)[0]; got != app.ReconcileStepFixNvmManaged {
		t.Fatalf("first step = %q, want fix-nvm-managed first", got)
	}
}

func TestDashboardReconcilePlan_UsesExplicitDotsConfiguredFlag(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{
		DotsConfigured: true,
		DotsEntries: []app.DotStatus{
			{Name: "nvim", State: dots.StateConflict, Counts: app.DotFileCounts{OutOfSync: 1}},
		},
	})

	if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepSyncDots) {
		t.Fatalf("steps = %#v, want sync dots step from explicit configured flag", steps)
	}
	assertReconcileDetail(t, steps, app.ReconcileStepSyncDots, "1 out-of-sync entry")
}

func TestDashboardReconcilePlan_SuppressesDotsStepsWhenDotsUnavailable(t *testing.T) {
	base := app.DashboardReconcilePlanInput{
		DotsRepo: "/repo",
		DotsEntries: []app.DotStatus{
			{Name: "nvim", State: dots.StateConflict, Counts: app.DotFileCounts{OutOfSync: 1}},
		},
		DotsGitStatus: " M dotfiles/zsh/.zshrc",
	}

	for _, tc := range []struct {
		name  string
		input app.DashboardReconcilePlanInput
	}{
		{name: "unconfigured", input: withDotsRepo(base, "")},
		{name: "disabled", input: withDotsDisabled(base)},
		{name: "busy", input: withDotsBusy(base)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := app.DashboardReconcilePlan(tc.input)
			for _, id := range []app.DashboardReconcileStepID{app.ReconcileStepSyncDots, app.ReconcileStepCommitDots} {
				if app.DashboardReconcilePlanHasStep(steps, id) {
					t.Fatalf("steps = %#v, want no %s step", steps, id)
				}
			}
		})
	}
}

func TestDashboardReconcilePlan_IgnoresSuppressedTools(t *testing.T) {
	steps := app.DashboardReconcilePlan(app.DashboardReconcilePlanInput{
		Tools: []*app.ToolView{
			{Name: "fd", Provider: "brew", Tracked: true, Installed: false},
			{Name: "git", Provider: "brew", Tracked: true, Installed: true, Outdated: true},
		},
		DiscoveredTools: []*app.ToolView{
			{Name: "ripgrep", Provider: "brew", Installed: true},
		},
		IgnoredTools: map[string]bool{
			"fd":      true,
			"git":     true,
			"ripgrep": true,
		},
	})

	for _, id := range []app.DashboardReconcileStepID{app.ReconcileStepSyncTools, app.ReconcileStepUpgradeTools} {
		if app.DashboardReconcilePlanHasStep(steps, id) {
			t.Fatalf("steps = %#v, want no %s step", steps, id)
		}
	}
}

func reconcileStepIDs(steps []app.DashboardReconcilePlanStep) []app.DashboardReconcileStepID {
	ids := make([]app.DashboardReconcileStepID, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID)
	}
	return ids
}

func assertReconcileDetail(t *testing.T, steps []app.DashboardReconcilePlanStep, id app.DashboardReconcileStepID, want string) {
	t.Helper()
	for _, step := range steps {
		if step.ID == id {
			if strings.TrimSpace(step.Detail) != want {
				t.Fatalf("%s detail = %q, want %q", id, step.Detail, want)
			}
			return
		}
	}
	t.Fatalf("missing %s step in %#v", id, steps)
}

func withDotsRepo(input app.DashboardReconcilePlanInput, repo string) app.DashboardReconcilePlanInput {
	input.DotsRepo = repo
	return input
}

func withDotsDisabled(input app.DashboardReconcilePlanInput) app.DashboardReconcilePlanInput {
	input.DotsDisabled = true
	return input
}

func withDotsBusy(input app.DashboardReconcilePlanInput) app.DashboardReconcilePlanInput {
	input.DotsBusy = true
	return input
}
