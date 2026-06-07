package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

func TestProviderScanDisplayLabel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		concrete string
		want     string
	}{
		{name: "plain", provider: "brew", want: "brew"},
		{name: "ecosystem concrete", provider: "system", concrete: "brew", want: "system/brew"},
		{name: "same concrete", provider: "system", concrete: "system", want: "system"},
		{name: "empty provider", concrete: "brew", want: "brew"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderScanDisplayLabel(tt.provider, tt.concrete); got != tt.want {
				t.Fatalf("ProviderScanDisplayLabel(%q, %q) = %q, want %q", tt.provider, tt.concrete, got, tt.want)
			}
		})
	}
}

func TestRefreshToolsStatus(t *testing.T) {
	got := RefreshToolsStatus([]string{"node/bun", "system/brew"}, 1, 2)
	want := "Refreshing tools… 1/2: node/bun, system/brew"
	if got != want {
		t.Fatalf("RefreshToolsStatus = %q, want %q", got, want)
	}
}

func TestRefreshProviderScanLabels(t *testing.T) {
	labels := RefreshProviderScanLabels(
		map[string]bool{"node": true, "system": true, "": true},
		map[string]string{"node": "node/bun"},
	)
	got := RefreshToolsStatus(labels, 0, 2)
	want := "Refreshing tools… 0/2: node/bun, system"
	if got != want {
		t.Fatalf("RefreshProviderScanLabels status = %q, want %q", got, want)
	}
}

func TestRefreshProviderScanLabel(t *testing.T) {
	labels := map[string]string{
		"node":   " node/bun ",
		"system": " ",
	}
	if got := RefreshProviderScanLabel("node", labels); got != "node/bun" {
		t.Fatalf("RefreshProviderScanLabel node = %q, want node/bun", got)
	}
	if got := RefreshProviderScanLabel("system", labels); got != "system" {
		t.Fatalf("RefreshProviderScanLabel system = %q, want provider fallback", got)
	}
}

func TestRefreshToolProgressStatus(t *testing.T) {
	got := RefreshToolProgressStatus("node/bun", "typescript", 1, 2)
	want := "Refreshing tools… 1/2: node/bun/typescript"
	if got != want {
		t.Fatalf("RefreshToolProgressStatus = %q, want %q", got, want)
	}
}

func TestRefreshProviderScanProgressText(t *testing.T) {
	got := RefreshProviderScanProgressText("system/brew", 1, 3)
	want := "Scanning system/brew… (1/3)"
	if got != want {
		t.Fatalf("RefreshProviderScanProgressText = %q, want %q", got, want)
	}
}

func TestProviderMatchProgressText(t *testing.T) {
	if got := providerMatchProgressText("ripgrep", nil); got != "" {
		t.Fatalf("providerMatchProgressText empty = %q, want empty", got)
	}
	got := providerMatchProgressText("ripgrep", []config.ToolInstallSpec{{Provider: "brew", Package: "rg"}})
	want := "matched provider: ripgrep -> brew/rg"
	if got != want {
		t.Fatalf("providerMatchProgressText single = %q, want %q", got, want)
	}
	got = providerMatchProgressText("ripgrep", []config.ToolInstallSpec{{Provider: "brew", Package: "rg"}, {Provider: "apt"}})
	want = "matched providers: ripgrep -> brew/rg, apt/ripgrep"
	if got != want {
		t.Fatalf("providerMatchProgressText multi = %q, want %q", got, want)
	}
}

func TestRefreshDescriptionAndDiscoveryProgressText(t *testing.T) {
	desc := RefreshDescriptionsProgressText(RefreshDescriptionsProgressEvent{Name: "ripgrep", Index: -1})
	if want := "Refreshing descriptions 0/-1: tool/ripgrep…"; desc != want {
		t.Fatalf("RefreshDescriptionsProgressText fallback = %q, want %q", desc, want)
	}
	desc = RefreshDescriptionsProgressText(RefreshDescriptionsProgressEvent{Provider: "brew", Name: "ripgrep", Index: 2, Total: 4})
	if want := "Refreshing descriptions 2/4: brew/ripgrep…"; desc != want {
		t.Fatalf("RefreshDescriptionsProgressText provider = %q, want %q", desc, want)
	}

	installed := RefreshInstalledProgressText(RefreshInstalledProgressEvent{Provider: "brew", ProviderLabel: "system/brew", Name: "ripgrep", Index: 1, Total: 3})
	if want := "Refreshing tools… 1/3: system/brew/ripgrep"; installed != want {
		t.Fatalf("RefreshInstalledProgressText = %q, want %q", installed, want)
	}
	installed = RefreshInstalledProgressText(RefreshInstalledProgressEvent{Provider: "brew", Name: "ripgrep", Index: 1, Total: 3})
	if want := "Refreshing tools… 1/3: brew/ripgrep"; installed != want {
		t.Fatalf("RefreshInstalledProgressText provider fallback = %q, want %q", installed, want)
	}
	discovered := RefreshDiscoveredProgressText(RefreshDiscoveredProgressEvent{Index: 0, Total: 0})
	if want := "Finding local tools 0/0…"; discovered != want {
		t.Fatalf("RefreshDiscoveredProgressText fallback = %q, want %q", discovered, want)
	}
	discovered = RefreshDiscoveredProgressText(RefreshDiscoveredProgressEvent{Provider: " brew ", Index: 2, Total: 5})
	if want := "Finding local tools 2/5: brew…"; discovered != want {
		t.Fatalf("RefreshDiscoveredProgressText provider = %q, want %q", discovered, want)
	}
}

func TestImportSummaryText(t *testing.T) {
	result := &ImportResult{
		Added: []ImportedTool{
			{Name: "typescript", Provider: "node"},
			{Name: "ripgrep", Provider: "system"},
		},
		Skipped: []ImportedTool{{Name: "eslint", Provider: "node"}},
	}
	if got := ImportCommandSummaryText(result, false); got != "Imported 2 tools:" {
		t.Fatalf("ImportCommandSummaryText = %q, want imported count", got)
	}
	if got := ImportCommandSummaryText(result, true); got != "Would import 2 tools:" {
		t.Fatalf("dry-run ImportCommandSummaryText = %q, want preview count", got)
	}
	if got := ImportSkippedSummaryText(result); got != "Skipped 1 already configured tool" {
		t.Fatalf("ImportSkippedSummaryText = %q, want skipped count", got)
	}
	if got := ImportSettingsSummaryText(result); got != "Imported 2 tools into settings.json" {
		t.Fatalf("ImportSettingsSummaryText = %q, want settings summary", got)
	}
	if got := ImportSkippedSummaryText(&ImportResult{}); got != "" {
		t.Fatalf("empty ImportSkippedSummaryText = %q, want empty", got)
	}
}

func TestImportProviderCountSummaries(t *testing.T) {
	result := &ImportResult{Added: []ImportedTool{
		{Name: "typescript", Provider: "node"},
		{Name: "eslint", Provider: "node"},
		{Name: "ripgrep", Provider: "system"},
		{Name: "fd", Provider: "unknown"},
	}}
	providers := []ProviderInfo{
		{Name: "system"},
		{Name: "node"},
		{Name: "python"},
	}

	got := ImportProviderCountSummaries(result, providers)
	want := []ImportProviderCountSummary{
		{Provider: "system", Count: 1, CountText: "1 tool"},
		{Provider: "node", Count: 2, CountText: "2 tools"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportProviderCountSummaries = %#v, want %#v", got, want)
	}
}

func TestSummarizeSyncResultCountsProviderUnavailable(t *testing.T) {
	result := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall},
		{Kind: isync.OpAlreadyInstalled},
		{Kind: isync.OpFailed},
		{Kind: isync.OpProviderUnavailable, Tool: provider.Tool{Name: "fd", Provider: "brew"}},
		{Kind: isync.OpIgnored},
	}}

	summary := SummarizeSyncResult(result)
	if summary.Installed != 1 || summary.Failed != 1 || summary.AlreadyInstalled != 1 || summary.Ignored != 1 {
		t.Fatalf("SummarizeSyncResult counts = %+v, want installed=1 failed=1 already=1 ignored=1", summary)
	}
	if len(summary.ProviderUnavailable) != 1 || summary.ProviderUnavailable[0].Tool.Name != "fd" {
		t.Fatalf("ProviderUnavailable = %+v, want fd", summary.ProviderUnavailable)
	}
	if got := SummarizeSyncResult(nil); got.Installed != 0 || len(got.ProviderUnavailable) != 0 {
		t.Fatalf("nil SummarizeSyncResult = %+v, want zero summary", got)
	}
}

func TestSetupBootstrapToolsMessageUsesSyncSummary(t *testing.T) {
	result := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall},
		{Kind: isync.OpInstall},
		{Kind: isync.OpAlreadyInstalled},
	}}
	if got := SetupBootstrapToolsMessage(result); got != "host tools applied, 2 installed" {
		t.Fatalf("SetupBootstrapToolsMessage = %q, want installed count", got)
	}
	if got := SetupBootstrapToolsMessage(&isync.SyncResult{}); got != "host tools applied" {
		t.Fatalf("empty SetupBootstrapToolsMessage = %q, want base message", got)
	}
	if got := SetupBootstrapToolsMessage(nil); got != "host tools applied" {
		t.Fatalf("nil SetupBootstrapToolsMessage = %q, want base message", got)
	}
}

func TestSetupBootstrapDotsMessageCountsOperations(t *testing.T) {
	ops := []dots.Op{
		{Kind: dots.OpLink},
		{Kind: dots.OpRepair},
	}
	if got := SetupBootstrapDotsMessage(ops); got != "dotfiles applied, 2 operation(s)" {
		t.Fatalf("SetupBootstrapDotsMessage = %q, want operation count", got)
	}
	if got := SetupBootstrapDotsMessage(nil); got != "dotfiles applied" {
		t.Fatalf("nil SetupBootstrapDotsMessage = %q, want base message", got)
	}
}

func TestDotsSyncedSummaryTextCountsOperations(t *testing.T) {
	ops := []dots.Op{
		{Kind: dots.OpLink},
		{Kind: dots.OpRepair},
	}
	if got := DotsSyncedSummaryText(ops); got != "Dotfiles synced (2 operation(s))." {
		t.Fatalf("DotsSyncedSummaryText = %q, want operation count", got)
	}
	if got := DotsSyncedSummaryText(nil); got != "Dotfiles synced (0 operation(s))." {
		t.Fatalf("nil DotsSyncedSummaryText = %q, want zero count", got)
	}
}

func TestDotsOperationReportClassifiesOperationsAndSummaries(t *testing.T) {
	report := DotsOperationReport([]dots.Op{
		{Kind: dots.OpLink, Src: "/repo/nvim", Dst: "/home/user/.config/nvim"},
		{Kind: dots.OpRepair, Dst: "/home/user/.zshrc"},
		{Kind: dots.OpConflict, Dst: "/home/user/.gitconfig", Err: errors.New("changed locally")},
	}, false)

	wantStdout := []string{
		"  ✓ linked:   /home/user/.config/nvim → /repo/nvim",
		"  ✓ repaired: /home/user/.zshrc",
		"",
		"2 symlink(s) updated.",
	}
	if !reflect.DeepEqual(report.Stdout, wantStdout) {
		t.Fatalf("stdout lines = %#v, want %#v", report.Stdout, wantStdout)
	}
	wantStderr := []string{
		"  ✗ conflict: /home/user/.gitconfig — changed locally",
		"1 conflict(s). Choose use repo version or use local version before syncing.",
	}
	if !reflect.DeepEqual(report.Stderr, wantStderr) {
		t.Fatalf("stderr lines = %#v, want %#v", report.Stderr, wantStderr)
	}
}

func TestDotsOperationReportDryRunAndNoChanges(t *testing.T) {
	dryRun := DotsOperationReport([]dots.Op{{Kind: dots.OpDryLink, Dst: "/home/user/.zshrc"}}, true)
	wantDryRun := []string{
		"  → would link:   /home/user/.zshrc",
		"",
		"Dry-run — no changes made.",
	}
	if !reflect.DeepEqual(dryRun.Stdout, wantDryRun) {
		t.Fatalf("dry-run stdout = %#v, want %#v", dryRun.Stdout, wantDryRun)
	}

	noChanges := DotsOperationReport(nil, false)
	if !reflect.DeepEqual(noChanges.Stdout, []string{"All symlinks up to date."}) {
		t.Fatalf("no-change stdout = %#v", noChanges.Stdout)
	}
}

func TestSyncResultSummaryTextUsesSyncSummary(t *testing.T) {
	result := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall},
		{Kind: isync.OpInstall, Err: errors.New("boom")},
		{Kind: isync.OpAlreadyInstalled},
	}}
	if got := SyncResultSummaryText(result, "install complete"); got != "install complete — 1 installed" {
		t.Fatalf("SyncResultSummaryText = %q, want installed count", got)
	}
	if got := SyncResultSummaryText(&isync.SyncResult{}, "install complete"); got != "install complete" {
		t.Fatalf("empty SyncResultSummaryText = %q, want prefix only", got)
	}
	if got := SyncResultSummaryText(nil, "install complete"); got != "install complete" {
		t.Fatalf("nil SyncResultSummaryText = %q, want prefix only", got)
	}
}

func TestSyncOperationLineFormatsCommandAndBootstrapStyles(t *testing.T) {
	tool := provider.Tool{Name: "fd", Provider: "brew"}
	errBoom := errors.New("boom")

	tests := []struct {
		name string
		op   isync.SyncOp
		opts SyncOperationLineOptions
		want string
	}{
		{name: "command install", op: isync.SyncOp{Kind: isync.OpInstall, Tool: tool, Version: "9.0.0"}, opts: SyncOperationLineOptions{IncludeVersion: true}, want: "  ✓ installed: fd (brew) 9.0.0"},
		{name: "command dry run", op: isync.SyncOp{Kind: isync.OpInstall, Tool: tool, Version: "9.0.0"}, opts: SyncOperationLineOptions{DryRun: true, IncludeVersion: true}, want: "  → would install: fd (brew)"},
		{name: "command failed", op: isync.SyncOp{Kind: isync.OpFailed, Tool: tool, Err: errBoom}, opts: SyncOperationLineOptions{IncludeVersion: true}, want: "  ✗ failed: fd (brew): boom"},
		{name: "command already installed", op: isync.SyncOp{Kind: isync.OpAlreadyInstalled, Tool: tool, Version: "9.0.0"}, opts: SyncOperationLineOptions{IncludeVersion: true}, want: "  ✓ already installed: fd (brew) 9.0.0"},
		{name: "command prune", op: isync.SyncOp{Kind: isync.OpUninstall, Tool: tool}, opts: SyncOperationLineOptions{IncludeVersion: true}, want: "  ✗ pruned: fd (brew)"},
		{name: "provider unavailable", op: isync.SyncOp{Kind: isync.OpProviderUnavailable, Tool: tool}, opts: SyncOperationLineOptions{IncludeVersion: true}, want: "  ! provider unavailable: brew (skipping fd)"},
		{name: "bootstrap install", op: isync.SyncOp{Kind: isync.OpInstall, Tool: tool, Version: "9.0.0"}, opts: SyncOperationLineOptions{BootstrapStyle: true}, want: "  ✓ installed: fd (brew)"},
		{name: "bootstrap install error", op: isync.SyncOp{Kind: isync.OpInstall, Tool: tool, Err: errBoom}, opts: SyncOperationLineOptions{BootstrapStyle: true}, want: "  ✗ brew/fd: boom"},
		{name: "bootstrap already installed", op: isync.SyncOp{Kind: isync.OpAlreadyInstalled, Tool: tool, Version: "9.0.0"}, opts: SyncOperationLineOptions{BootstrapStyle: true}, want: "  ✓ already installed: fd (brew)"},
		{name: "ignored omitted", op: isync.SyncOp{Kind: isync.OpIgnored, Tool: tool}, opts: SyncOperationLineOptions{IncludeVersion: true}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SyncOperationLine(tt.op, tt.opts); got != tt.want {
				t.Fatalf("SyncOperationLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSyncResultSummaryLinesPluralizesCounts(t *testing.T) {
	result := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall},
		{Kind: isync.OpAlreadyInstalled},
		{Kind: isync.OpAlreadyInstalled},
		{Kind: isync.OpFailed},
		{Kind: isync.OpFailed},
	}}

	got := SyncResultSummaryLines(result, SyncResultSummaryLineOptions{
		IncludeInstalled:        true,
		IncludeAlreadyInstalled: true,
		IncludeFailed:           true,
		FailureSuffix:           "Retry later.",
	})
	want := []string{
		"1 tool installed.",
		"2 tools already installed.",
		"2 tools failed. Retry later.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SyncResultSummaryLines = %v, want %v", got, want)
	}

	got = SyncResultSummaryLines(result, SyncResultSummaryLineOptions{IncludeFailed: true})
	want = []string{"2 tools failed."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered SyncResultSummaryLines = %v, want %v", got, want)
	}
	if got := SyncResultSummaryLines(nil, SyncResultSummaryLineOptions{IncludeInstalled: true}); got != nil {
		t.Fatalf("nil SyncResultSummaryLines = %v, want nil", got)
	}
}

func TestSyncProviderUnavailableLines(t *testing.T) {
	result := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpProviderUnavailable, Tool: provider.Tool{Name: "fd", Provider: "brew"}},
		{Kind: isync.OpInstall, Tool: provider.Tool{Name: "rg", Provider: "system"}},
		{Kind: isync.OpProviderUnavailable, Tool: provider.Tool{Name: "black", Provider: "python"}},
	}}
	got := SyncProviderUnavailableLines(result)
	want := []string{
		"provider unavailable: brew (skipping fd)",
		"provider unavailable: python (skipping black)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SyncProviderUnavailableLines = %v, want %v", got, want)
	}
	if got := SyncProviderUnavailableLines(nil); got != nil {
		t.Fatalf("nil SyncProviderUnavailableLines = %v, want nil", got)
	}
}

func TestClaimSuccessStatusTextUsesCurrentHostFallback(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.local")
	if got := ClaimSuccessStatusText("fd", "work"); got != "✓ added fd to config (work)" {
		t.Fatalf("ClaimSuccessStatusText explicit = %q", got)
	}
	if got := ClaimSuccessStatusText("fd", ""); got != "✓ added fd to config (desk)" {
		t.Fatalf("ClaimSuccessStatusText fallback = %q, want current host", got)
	}
}

func TestDotsSyncProgressText(t *testing.T) {
	active := dots.SyncProgressEvent{Entry: "nvim", Index: 1, Total: 2}
	if got, want := DotsSyncProgressLineText(active), "syncing dots 1/2: nvim"; got != want {
		t.Fatalf("DotsSyncProgressLineText = %q, want %q", got, want)
	}
	if got, want := DotsSyncActivityProgressText(active), "Syncing dots 1/2: nvim…"; got != want {
		t.Fatalf("DotsSyncActivityProgressText = %q, want %q", got, want)
	}

	initial := dots.SyncProgressEvent{Index: -1, Total: 3}
	if got, want := DotsSyncProgressLineText(initial), "syncing dots 0/3"; got != want {
		t.Fatalf("DotsSyncProgressLineText initial = %q, want %q", got, want)
	}
	if got, want := DotsSyncActivityProgressText(initial), "Syncing dots 0/3…"; got != want {
		t.Fatalf("DotsSyncActivityProgressText initial = %q, want %q", got, want)
	}

	done := dots.SyncProgressEvent{Entry: "nvim", Index: 2, Total: 2, Done: true}
	if got, want := DotsSyncActivityProgressText(done), "Synced dots 2/2: nvim"; got != want {
		t.Fatalf("DotsSyncActivityProgressText done = %q, want %q", got, want)
	}

	failed := dots.SyncProgressEvent{Entry: "nvim", Index: 2, Total: 2, Err: errors.New("stow failed")}
	if got, want := DotsSyncActivityProgressText(failed), "Syncing dots 2/2: nvim failed"; got != want {
		t.Fatalf("DotsSyncActivityProgressText failed = %q, want %q", got, want)
	}
}

func TestProviderScanFailureStatus(t *testing.T) {
	err := errors.Join(
		errors.New("upserting installed status for system/fd: context deadline exceeded"),
		context.DeadlineExceeded,
	)
	got := ProviderScanFailureStatus("system", err)
	if got != "scan timed out for system" {
		t.Fatalf("ProviderScanFailureStatus timeout = %q, want concise timeout", got)
	}
	if got := ProviderScanFailureStatus("brew", errors.New("db write failed")); got != "scan failed for brew: db write failed" {
		t.Fatalf("ProviderScanFailureStatus failure = %q", got)
	}
}

func TestSyncAllSummaryText(t *testing.T) {
	result := &SyncAllResult{
		SyncResult: &isync.SyncResult{Ops: []isync.SyncOp{
			{Kind: isync.OpInstall},
			{Kind: isync.OpInstall, Err: errors.New("boom")},
			{Kind: isync.OpAlreadyInstalled},
		}},
		ClaimedNames: []string{"fzf", "fd"},
		NormalizedProviderOverrides: []NormalizedInstallOverride{
			{Name: "typescript", Provider: "node", InstallWith: "bun"},
		},
	}

	summary := SummarizeSyncAll(result)
	if summary.Installed != 1 || summary.Claimed != 2 || summary.NormalizedProviderOverrides != 1 {
		t.Fatalf("SummarizeSyncAll = %+v, want installed=1 claimed=2 normalized=1", summary)
	}

	got := SyncAllSummaryText(result, "Sync all complete")
	want := "Sync all complete — 1 installed, 2 added to config, 1 provider overrides normalized"
	if got != want {
		t.Fatalf("SyncAllSummaryText = %q, want %q", got, want)
	}
}

func TestConsolidateSummaryText(t *testing.T) {
	result := &ConsolidateResult{
		Migrated: []ConsolidateTool{
			{Name: "eslint", FromProvider: "npm"},
			{Name: "typescript", FromProvider: "npm"},
		},
		Failed: []ConsolidateFailure{
			{ConsolidateTool: ConsolidateTool{Name: "prettier", FromProvider: "npm"}, Err: errors.New("install failed")},
		},
		UninstallWarnings: []ConsolidateFailure{
			{ConsolidateTool: ConsolidateTool{Name: "eslint", FromProvider: "npm"}, Err: errors.New("uninstall failed")},
		},
		SettingsUpdated: true,
	}

	got := ConsolidateSummaryText(result, "bun")
	want := "2 tools migrated to bun, 1 tool failed, 1 uninstall warning, settings updated"
	if got != want {
		t.Fatalf("ConsolidateSummaryText = %q, want %q", got, want)
	}
	if got := ConsolidateSummaryText(&ConsolidateResult{}, ""); got != "0 tools migrated" {
		t.Fatalf("empty ConsolidateSummaryText = %q, want zero migrated", got)
	}
	if got := ConsolidateSummaryText(nil, "bun"); got != "" {
		t.Fatalf("nil ConsolidateSummaryText = %q, want empty", got)
	}
}

func TestReconcileSummaryText(t *testing.T) {
	result := &ReconcileResult{
		SyncAll: &SyncAllResult{
			SyncResult:   &isync.SyncResult{Ops: []isync.SyncOp{{Kind: isync.OpInstall}}},
			ClaimedNames: []string{"fd", "fzf"},
		},
		UpgradeAll:    &UpgradeAllResult{Upgraded: []string{"git"}},
		DotsOps:       []dots.Op{{Kind: dots.OpLink}},
		DotsCommitted: true,
	}

	got := ReconcileSummaryText(result, "Reconcile complete")
	want := "Reconcile complete — 1 installed, 2 added to config, 1 upgraded, 1 dotfile op, dotfiles committed"
	if got != want {
		t.Fatalf("ReconcileSummaryText = %q, want %q", got, want)
	}
	if got := ReconcileSummaryText(nil, "Reconcile complete"); got != "Reconcile complete — 0 installed, 0 added to config, 0 upgraded" {
		t.Fatalf("nil ReconcileSummaryText = %q, want zero-count summary", got)
	}
}

func TestReconcileIssueLinesPluralizesCounts(t *testing.T) {
	result := &ReconcileResult{
		SyncAll:    &SyncAllResult{Failures: []BulkToolError{{Name: "fd", Provider: "brew"}}},
		UpgradeAll: &UpgradeAllResult{Failures: []BulkToolError{{Name: "git", Provider: "brew"}, {Name: "go", Provider: "brew"}}},
		DotsEntries: []DotStatus{
			{Health: HealthConflict},
			{Health: HealthMissing},
			{Health: HealthNoSource},
		},
	}

	got := ReconcileIssueLines(result)
	want := []string{
		"1 tool failed to install",
		"2 tools failed to upgrade",
		"1 dot entry has conflicts",
		"2 dot entries missing or no source",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReconcileIssueLines = %v, want %v", got, want)
	}
	if got := ReconcileIssueLines(nil); got != nil {
		t.Fatalf("nil ReconcileIssueLines = %v, want nil", got)
	}
}

func TestSyncFailureRows(t *testing.T) {
	actionErr := provider.NewExternallyManagedPythonError("pip3", "install", provider.Tool{Name: "ruff", Provider: "python"}, errors.New("exit 1"), "stderr", nil)
	result := &isync.SyncResult{Ops: []isync.SyncOp{
		{
			Tool: provider.Tool{Name: "ripgrep", Provider: "system"},
			Kind: isync.OpFailed,
			Err:  context.Canceled,
		},
		{
			Tool: provider.Tool{Name: "vim", Provider: "system"},
			Kind: isync.OpFailed,
			Err:  errors.New("requires sudo: apt install vim"),
		},
		{
			Tool: provider.Tool{Name: "ruff", Provider: "python"},
			Kind: isync.OpFailed,
			Err:  actionErr,
		},
	}}

	got := SyncFailureRows(result)
	if _, ok := got.RowErrors[toolResultKey("ripgrep", "system")]; ok {
		t.Fatalf("cancelled op should not create a row error: %#v", got.RowErrors)
	}
	if got.RowErrors[toolResultKey("vim", "system")] != "requires sudo: apt install vim" {
		t.Fatalf("RowErrors = %#v, want vim privilege failure", got.RowErrors)
	}
	if got.RowActionErrors[toolResultKey("ruff", "python")] == nil {
		t.Fatalf("RowActionErrors = %#v, want ruff action error", got.RowActionErrors)
	}
	if got.PrivilegedActions[toolResultKey("vim", "system")] != provider.PrivilegeActionInstall {
		t.Fatalf("PrivilegedActions = %#v, want vim install action", got.PrivilegedActions)
	}
}

func TestUpgradeAllFailureRowsAndSummaryText(t *testing.T) {
	rows := UpgradeAllFailureRows(&UpgradeAllResult{Failures: []BulkToolError{
		{Name: "bat", Provider: "system", Message: "requires sudo: apt upgrade bat"},
		{Name: "fd", Provider: "system", Message: "network failed"},
	}})

	if rows.PrivilegedActions[toolResultKey("bat", "system")] != provider.PrivilegeActionUpgrade {
		t.Fatalf("PrivilegedActions = %#v, want bat upgrade action", rows.PrivilegedActions)
	}
	got := BulkToolFailureSummaryText("upgrades complete", rows)
	want := "upgrades complete, 1 need admin approval, 1 failed"
	if got != want {
		t.Fatalf("BulkToolFailureSummaryText = %q, want %q", got, want)
	}
}

func TestUpgradeAllSummaryLines(t *testing.T) {
	got := UpgradeAllSummaryLines(&UpgradeAllResult{
		Upgraded:    []string{"ripgrep"},
		Quarantined: []QuarantinedUpdate{{Name: "bat"}, {Name: "fd"}},
		Failures:    []BulkToolError{{Name: "vim", Provider: "system", Message: "network failed"}},
	})
	want := []string{
		"1 tool upgraded.",
		"2 updates quarantined.",
		"1 tool failed.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UpgradeAllSummaryLines = %v, want %v", got, want)
	}
	if got := UpgradeAllSummaryLines(nil); len(got) != 0 {
		t.Fatalf("nil UpgradeAllSummaryLines = %v, want empty", got)
	}
}

func TestSyncAllFailureRowsMergesDirectAndSyncFailures(t *testing.T) {
	rows := SyncAllFailureRows(&SyncAllResult{
		Failures: []BulkToolError{
			{Name: "fd", Provider: "system", Message: "network failed"},
		},
		SyncResult: &isync.SyncResult{Ops: []isync.SyncOp{
			{
				Tool: provider.Tool{Name: "vim", Provider: "system"},
				Kind: isync.OpFailed,
				Err:  errors.New("requires sudo: apt install vim"),
			},
		}},
	})

	if rows.RowErrors[toolResultKey("fd", "system")] != "network failed" {
		t.Fatalf("RowErrors = %#v, want direct sync-all failure", rows.RowErrors)
	}
	if rows.RowErrors[toolResultKey("vim", "system")] != "requires sudo: apt install vim" {
		t.Fatalf("RowErrors = %#v, want nested sync failure", rows.RowErrors)
	}
	if rows.PrivilegedActions[toolResultKey("vim", "system")] != provider.PrivilegeActionInstall {
		t.Fatalf("PrivilegedActions = %#v, want vim install action", rows.PrivilegedActions)
	}

	got := BulkToolFailureSummaryText("sync complete", rows)
	want := "sync complete, 1 need admin approval, 1 failed"
	if got != want {
		t.Fatalf("BulkToolFailureSummaryText = %q, want %q", got, want)
	}
}

func TestSyncAllPhaseProgressText(t *testing.T) {
	got := SyncAllPhaseProgressText("reading installed packages…", 2)
	want := "Syncing tools 0/2: checking installed state…"
	if got != want {
		t.Fatalf("SyncAllPhaseProgressText = %q, want %q", got, want)
	}
}

func TestSyncAllToolProgressText(t *testing.T) {
	tests := []struct {
		name    string
		message string
		err     error
		want    string
	}{
		{name: "adding", message: "Adding fzf to config…", want: "Syncing tools 1/2: adding discovered fzf to config…"},
		{name: "installing", message: "Installing fzf…", want: "Syncing tools 1/2: installing missing fzf…"},
		{name: "admin skipped", message: "Skipped installing fzf", err: errors.New("requires sudo: apt install fzf"), want: "Syncing tools 1/2: admin approval needed for fzf"},
		{name: "failed", message: "Failed installing fzf", want: "Syncing tools 1/2: failed installing missing fzf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SyncAllToolProgressText(isync.ProgressEvent{
				Tool:    provider.Tool{Name: "fzf", Provider: "brew"},
				Message: tt.message,
				Err:     tt.err,
			}, 1, 2)
			if got != tt.want {
				t.Fatalf("SyncAllToolProgressText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSyncAllProgressTotalCountsAddAndInstallOnly(t *testing.T) {
	discovered := []*database.ToolCache{{Name: "fzf", Provider: "brew", Installed: true}}
	tools := []*database.ToolCache{
		{Name: "bat", Provider: "brew", Tracked: true, Installed: false},
		{Name: "ripgrep", Provider: "brew", Tracked: true, Installed: true, Outdated: true},
	}

	if got := SyncAllProgressTotal(tools, discovered); got != 2 {
		t.Fatalf("SyncAllProgressTotal = %d, want 2", got)
	}
}

func TestUpgradeAllProgressText(t *testing.T) {
	tests := []struct {
		name          string
		message       string
		targetVersion string
		err           error
		want          string
	}{
		{name: "started", message: "Upgrading bat…", want: "Upgrading tools 1/2: bat…"},
		{name: "started with target version", message: "Upgrading bat…", targetVersion: "1.2.3", want: "Upgrading tools 1/2: bat (1.2.3)…"},
		{name: "done with target version", message: "Upgraded bat", targetVersion: "1.2.3", want: "Upgrading tools 1/2: bat (1.2.3) upgraded"},
		{name: "done", message: "Upgraded bat", want: "Upgrading tools 1/2: bat upgraded"},
		{name: "admin skipped", message: "Skipped upgrading bat", err: errors.New("requires sudo: apt upgrade bat"), want: "Upgrading tools 1/2: bat needs admin approval"},
		{name: "failed", message: "Failed upgrading bat", want: "Upgrading tools 1/2: bat failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UpgradeAllProgressText(isync.ProgressEvent{
				Tool:          provider.Tool{Name: "bat", Provider: "brew"},
				Message:       tt.message,
				TargetVersion: tt.targetVersion,
				Err:           tt.err,
			}, 1, 2)
			if got != tt.want {
				t.Fatalf("UpgradeAllProgressText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpgradeAllProgressTotalCountsInstalledOutdatedTools(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "bat", Installed: true, Outdated: true},
		{Name: "fd", Installed: true, Outdated: false},
		{Name: "ripgrep", Installed: false, Outdated: true},
		nil,
	}

	if got := UpgradeAllProgressTotal(tools); got != 1 {
		t.Fatalf("UpgradeAllProgressTotal = %d, want 1", got)
	}
}
