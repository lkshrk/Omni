package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
	textutil "github.com/lkshrk/omni/internal/text"
)

func ProviderScanDisplayLabel(providerName, concreteName string) string {
	providerName = strings.TrimSpace(providerName)
	concreteName = strings.TrimSpace(concreteName)
	if providerName == "" {
		return concreteName
	}
	if concreteName == "" || concreteName == providerName {
		return providerName
	}
	return providerName + "/" + concreteName
}

func RefreshProviderScanProgressText(label string, index, total int) string {
	index, total = normalizedProgress(index, total)
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Sprintf("Scanning… (%d/%d)", index, total)
	}
	return fmt.Sprintf("Scanning %s… (%d/%d)", label, index, total)
}

func DotsSyncProgressLineText(event dots.SyncProgressEvent) string {
	index, total := normalizedProgress(event.Index, event.Total)
	if event.Entry == "" {
		return fmt.Sprintf("checking dots %d/%d", index, total)
	}
	return fmt.Sprintf("checking dots %d/%d: %s", index, total, event.Entry)
}

func DotsSyncActivityProgressText(event dots.SyncProgressEvent) string {
	index, total := normalizedProgress(event.Index, event.Total)
	progress := fmt.Sprintf("%d/%d", index, total)
	switch {
	case event.Entry == "":
		return "Syncing dots " + progress + "…"
	case event.Err != nil:
		return fmt.Sprintf("Syncing dots %s: %s failed", progress, event.Entry)
	case event.Done:
		return fmt.Sprintf("Synced dots %s: %s", progress, event.Entry)
	default:
		return fmt.Sprintf("Checking dots %s: %s…", progress, event.Entry)
	}
}

func ProviderScanFailureStatus(providerName string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "scan timed out for " + providerName
	}
	return "scan failed for " + providerName + ": " + err.Error()
}

func RefreshToolsStatus(labels []string, done, total int) string {
	done, total = normalizedDoneTotal(done, total)
	status := "Refreshing tools…"
	if total > 0 {
		status += " " + strconv.Itoa(done) + "/" + strconv.Itoa(total)
	}
	labels = normalizedLabels(labels)
	if len(labels) == 0 || total <= 0 {
		return status
	}
	return status + ": " + strings.Join(labels, ", ")
}

func RefreshProviderScanLabels(providers map[string]bool, labels map[string]string) []string {
	names := make([]string, 0, len(providers))
	for providerName := range providers {
		if providerName == "" {
			continue
		}
		if label := labels[providerName]; label != "" {
			names = append(names, label)
		} else {
			names = append(names, providerName)
		}
	}
	return names
}

func RefreshProviderScanLabel(providerName string, labels map[string]string) string {
	if label := strings.TrimSpace(labels[providerName]); label != "" {
		return label
	}
	return providerName
}

type ImportProviderCountSummary struct {
	Provider  string
	Count     int
	CountText string
}

func ImportCommandSummaryText(result *ImportResult, dryRun bool) string {
	action := "Imported"
	if dryRun {
		action = "Would import"
	}
	return fmt.Sprintf("%s %s:", action, importAddedCountText(result))
}

func ImportSkippedSummaryText(result *ImportResult) string {
	if result == nil || len(result.Skipped) == 0 {
		return ""
	}
	return "Skipped " + textutil.PluralCount(len(result.Skipped), "already configured tool", "already configured tools")
}

func ImportSettingsSummaryText(result *ImportResult) string {
	return "Imported " + importAddedCountText(result) + " into settings.json"
}

func ImportProviderCountSummaries(result *ImportResult, providers []ProviderInfo) []ImportProviderCountSummary {
	if result == nil || len(result.Added) == 0 {
		return nil
	}
	byProvider := make(map[string]int)
	for _, tool := range result.Added {
		byProvider[tool.Provider]++
	}
	summaries := make([]ImportProviderCountSummary, 0, len(byProvider))
	for _, provider := range providers {
		count := byProvider[provider.Name]
		if count == 0 {
			continue
		}
		summaries = append(summaries, ImportProviderCountSummary{
			Provider:  provider.Name,
			Count:     count,
			CountText: textutil.PluralCount(count, "tool", "tools"),
		})
	}
	return summaries
}

func importAddedCountText(result *ImportResult) string {
	count := 0
	if result != nil {
		count = len(result.Added)
	}
	return textutil.PluralCount(count, "tool", "tools")
}

type SyncAllSummary struct {
	Installed                   int
	Claimed                     int
	NormalizedProviderOverrides int
}

type UpgradeAllSummary struct {
	Upgraded    int
	Quarantined int
	Failed      int
	Skipped     int
}

type SyncResultSummary struct {
	Installed           int
	Failed              int
	AlreadyInstalled    int
	Ignored             int
	ProviderUnavailable []isync.SyncOp
}

func SummarizeSyncResult(result *isync.SyncResult) SyncResultSummary {
	if result == nil {
		return SyncResultSummary{}
	}
	var summary SyncResultSummary
	for _, op := range result.Ops {
		switch op.Kind {
		case isync.OpInstall:
			if op.Err == nil {
				summary.Installed++
			}
		case isync.OpFailed:
			summary.Failed++
		case isync.OpAlreadyInstalled:
			summary.AlreadyInstalled++
		case isync.OpIgnored:
			summary.Ignored++
		case isync.OpProviderUnavailable:
			summary.ProviderUnavailable = append(summary.ProviderUnavailable, op)
		}
	}
	return summary
}

func SyncResultSummaryText(result *isync.SyncResult, prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "sync complete"
	}
	summary := SummarizeSyncResult(result)
	if summary.Installed > 0 {
		return fmt.Sprintf("%s — %d installed", prefix, summary.Installed)
	}
	return prefix
}

type SyncResultSummaryLineOptions struct {
	IncludeInstalled        bool
	IncludeAlreadyInstalled bool
	IncludeFailed           bool
	FailureSuffix           string
}

func SyncResultSummaryLines(result *isync.SyncResult, opts SyncResultSummaryLineOptions) []string {
	if result == nil {
		return nil
	}
	summary := SummarizeSyncResult(result)
	lines := make([]string, 0, 3)
	if opts.IncludeInstalled && summary.Installed > 0 {
		lines = append(lines, textutil.PluralCount(summary.Installed, "tool", "tools")+" installed.")
	}
	if opts.IncludeAlreadyInstalled && summary.AlreadyInstalled > 0 {
		lines = append(lines, textutil.PluralCount(summary.AlreadyInstalled, "tool", "tools")+" already installed.")
	}
	if opts.IncludeFailed && summary.Failed > 0 {
		line := textutil.PluralCount(summary.Failed, "tool", "tools") + " failed."
		if suffix := strings.TrimSpace(opts.FailureSuffix); suffix != "" {
			line += " " + suffix
		}
		lines = append(lines, line)
	}
	return lines
}

func SyncProviderUnavailableLines(result *isync.SyncResult) []string {
	if result == nil {
		return nil
	}
	summary := SummarizeSyncResult(result)
	lines := make([]string, 0, len(summary.ProviderUnavailable))
	for _, op := range summary.ProviderUnavailable {
		lines = append(lines, SyncProviderUnavailableLine(op))
	}
	return lines
}

func SyncProviderUnavailableLine(op isync.SyncOp) string {
	providerName := strings.TrimSpace(op.Tool.Provider)
	if providerName == "" {
		providerName = "no configured provider"
	}
	return fmt.Sprintf("provider unavailable: %s (skipping %s)", providerName, op.Tool.Name)
}

type SyncOperationLineOptions struct {
	DryRun         bool
	IncludeVersion bool
	BootstrapStyle bool
}

func SyncOperationLine(op isync.SyncOp, opts SyncOperationLineOptions) string {
	switch op.Kind {
	case isync.OpInstall:
		if opts.BootstrapStyle && op.Err != nil {
			return fmt.Sprintf("  ✗ %s/%s: %v", op.Tool.Provider, op.Tool.Name, op.Err)
		}
		if opts.DryRun {
			return fmt.Sprintf("  → would install: %s (%s)", op.Tool.Name, op.Tool.Provider)
		}
		return fmt.Sprintf("  ✓ installed: %s (%s)%s", op.Tool.Name, op.Tool.Provider, syncOperationVersionSuffix(op, opts))
	case isync.OpFailed:
		return fmt.Sprintf("  ✗ failed: %s (%s): %v", op.Tool.Name, op.Tool.Provider, op.Err)
	case isync.OpAlreadyInstalled:
		return fmt.Sprintf("  ✓ already installed: %s (%s)%s", op.Tool.Name, op.Tool.Provider, syncOperationVersionSuffix(op, opts))
	case isync.OpUninstall:
		return fmt.Sprintf("  ✗ pruned: %s (%s)", op.Tool.Name, op.Tool.Provider)
	case isync.OpProviderUnavailable:
		return "  ! " + SyncProviderUnavailableLine(op)
	default:
		return ""
	}
}

func syncOperationVersionSuffix(op isync.SyncOp, opts SyncOperationLineOptions) string {
	if opts.IncludeVersion {
		return " " + op.Version
	}
	return ""
}

func ClaimSuccessStatusText(name, groupName string) string {
	group := groupName
	if group == "" {
		group = currentMachineGroupName()
	}
	return "✓ added " + name + " to config (" + group + ")"
}

func SetupBootstrapToolsMessage(result *isync.SyncResult) string {
	summary := SummarizeSyncResult(result)
	if summary.Installed > 0 {
		return fmt.Sprintf("host tools applied, %d installed", summary.Installed)
	}
	return "host tools applied"
}

func SetupBootstrapDotsMessage(ops []dots.Op) string {
	if len(ops) > 0 {
		return fmt.Sprintf("dotfiles applied, %d operation(s)", len(ops))
	}
	return "dotfiles applied"
}

func DotsSyncedSummaryText(ops []dots.Op) string {
	return fmt.Sprintf("Dotfiles synced (%d operation(s)).", len(ops))
}

type DotsOperationOutput struct {
	Stdout []string
	Stderr []string
}

func DotsOperationReport(ops []dots.Op, dryRun bool) DotsOperationOutput {
	var out DotsOperationOutput
	conflicts := 0
	changes := 0
	skipped := 0
	for _, op := range ops {
		switch op.Kind {
		case dots.OpSkip:
			if op.Err != nil {
				out.Stdout = append(out.Stdout, fmt.Sprintf("  - skipped:  %s — %v", op.Dst, op.Err))
				skipped++
			}
		case dots.OpLink:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  ✓ linked:   %s → %s", op.Dst, op.Src))
			changes++
		case dots.OpRepair:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  ✓ repaired: %s", op.Dst))
			changes++
		case dots.OpAdopt:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  ✓ adopted:  %s → %s", op.Dst, op.Src))
			changes++
		case dots.OpConflict:
			out.Stderr = append(out.Stderr, fmt.Sprintf("  ✗ conflict: %s — %v", op.Dst, op.Err))
			conflicts++
		case dots.OpUnlink:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  ✓ unlinked: %s", op.Dst))
			changes++
		case dots.OpUnlinkSkip:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  - skipped:  %s", op.Dst))
		case dots.OpUnlinkConflict:
			out.Stderr = append(out.Stderr, fmt.Sprintf("  ✗ unlink conflict: %s", op.Dst))
			conflicts++
		case dots.OpDryLink:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  → would link:   %s", op.Dst))
		case dots.OpDryRepair:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  → would repair: %s", op.Dst))
		case dots.OpDryAdopt:
			out.Stdout = append(out.Stdout, fmt.Sprintf("  → would adopt:  %s", op.Dst))
		}
	}
	if dryRun {
		out.Stdout = append(out.Stdout, "", "Dry-run — no changes made.")
		return out
	}
	if changes > 0 {
		out.Stdout = append(out.Stdout, "", fmt.Sprintf("%d symlink(s) updated.", changes))
	}
	if conflicts > 0 {
		out.Stderr = append(out.Stderr, fmt.Sprintf("%d conflict(s). Choose use repo version or use local version before syncing.", conflicts))
	}
	if changes == 0 && conflicts == 0 && skipped > 0 {
		out.Stdout = append(out.Stdout, "No symlinks updated.")
	}
	if changes == 0 && conflicts == 0 && skipped == 0 {
		out.Stdout = append(out.Stdout, "All symlinks up to date.")
	}
	return out
}

type BulkToolFailureRows struct {
	RowErrors         map[string]string
	RowActionErrors   map[string]*provider.ActionError
	PrivilegedActions map[string]provider.PrivilegeAction
}

func SummarizeSyncAll(result *SyncAllResult) SyncAllSummary {
	if result == nil {
		return SyncAllSummary{}
	}
	summary := SyncAllSummary{
		Claimed:                     len(result.ClaimedNames),
		NormalizedProviderOverrides: len(result.NormalizedProviderOverrides),
	}
	if result.SyncResult != nil {
		summary.Installed = SummarizeSyncResult(result.SyncResult).Installed
	}
	return summary
}

func SyncAllSummaryText(result *SyncAllResult, prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "sync complete"
	}
	summary := SummarizeSyncAll(result)
	status := fmt.Sprintf("%s — %d installed, %d added to config", prefix, summary.Installed, summary.Claimed)
	if summary.NormalizedProviderOverrides > 0 {
		status += fmt.Sprintf(", %d provider overrides normalized", summary.NormalizedProviderOverrides)
	}
	return status
}

func SummarizeUpgradeAll(result *UpgradeAllResult) UpgradeAllSummary {
	if result == nil {
		return UpgradeAllSummary{}
	}
	return UpgradeAllSummary{
		Upgraded:    len(result.Upgraded),
		Quarantined: len(result.Quarantined),
		Failed:      len(result.Failures),
		Skipped:     len(result.Skipped),
	}
}

func UpgradeAllSummaryLines(result *UpgradeAllResult) []string {
	summary := SummarizeUpgradeAll(result)
	lines := make([]string, 0, 3)
	if summary.Upgraded > 0 {
		lines = append(lines, textutil.PluralCount(summary.Upgraded, "tool", "tools")+" upgraded.")
	}
	if summary.Quarantined > 0 {
		lines = append(lines, textutil.PluralCount(summary.Quarantined, "update", "updates")+" quarantined.")
	}
	if summary.Failed > 0 {
		lines = append(lines, textutil.PluralCount(summary.Failed, "tool", "tools")+" failed.")
	}
	if summary.Skipped > 0 {
		lines = append(lines, textutil.PluralCount(summary.Skipped, "tool", "tools")+" skipped.")
	}
	return lines
}

func ConsolidateSummaryText(result *ConsolidateResult, target string) string {
	if result == nil {
		return ""
	}
	target = strings.TrimSpace(target)
	migrated := textutil.PluralCount(len(result.Migrated), "tool", "tools") + " migrated"
	if target != "" {
		migrated += " to " + target
	}
	parts := []string{migrated}
	if len(result.Failed) > 0 {
		parts = append(parts, textutil.PluralCount(len(result.Failed), "tool", "tools")+" failed")
	}
	if len(result.UninstallWarnings) > 0 {
		parts = append(parts, textutil.PluralCount(len(result.UninstallWarnings), "uninstall warning", "uninstall warnings"))
	}
	if result.SettingsUpdated {
		parts = append(parts, "settings updated")
	}
	return strings.Join(parts, ", ")
}

func ReconcileSummaryText(result *ReconcileResult, prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "reconcile complete"
	}
	summary := SummarizeReconcile(result)
	status := fmt.Sprintf("%s — %d installed, %d added to config, %d upgraded",
		prefix,
		summary.Installed,
		summary.Claimed,
		summary.Upgraded,
	)
	if summary.Quarantined > 0 {
		status += ", " + textutil.PluralCount(summary.Quarantined, "update", "updates") + " quarantined"
	}
	if summary.DotOps > 0 {
		status += ", " + textutil.PluralCount(summary.DotOps, "dotfile op", "dotfile ops")
	}
	if summary.NvmManaged > 0 {
		status += ", " + textutil.PluralCount(summary.NvmManaged, "nvm-managed tool migrated", "nvm-managed tools migrated")
	}
	if summary.NvmRemoved > 0 {
		status += ", " + textutil.PluralCount(summary.NvmRemoved, "runtime removed from config", "runtimes removed from config")
	}
	if summary.DotsBackedUp {
		status += ", dotfile changes backed up"
	} else if summary.DotsSkipped != "" {
		status += ", " + summary.DotsSkipped
	}
	return status
}

func ReconcileIssueLines(result *ReconcileResult) []string {
	issues := SummarizeReconcileIssues(result)
	if !issues.HasIssues() {
		return nil
	}
	lines := make([]string, 0, 5)
	if issues.NvmFailures > 0 {
		lines = append(lines, textutil.PluralCount(issues.NvmFailures, "nvm-managed tool", "nvm-managed tools")+" failed to migrate")
	}
	if issues.SyncFailures > 0 {
		lines = append(lines, textutil.PluralCount(issues.SyncFailures, "tool", "tools")+" failed to install")
	}
	if issues.UpgradeFailures > 0 {
		lines = append(lines, textutil.PluralCount(issues.UpgradeFailures, "tool", "tools")+" failed to upgrade")
	}
	if issues.DotsConflicts > 0 {
		lines = append(lines, textutil.PluralCount(issues.DotsConflicts, "dot entry", "dot entries")+" "+reconcileIssueVerb(issues.DotsConflicts)+" conflicts")
	}
	if issues.DotsMissing > 0 {
		lines = append(lines, textutil.PluralCount(issues.DotsMissing, "dot entry", "dot entries")+" missing or no source")
	}
	return lines
}

func reconcileIssueVerb(count int) string {
	if count == 1 {
		return "has"
	}
	return "have"
}

func SyncFailureRows(result *isync.SyncResult) BulkToolFailureRows {
	rows := BulkToolFailureRows{}
	if result == nil {
		return rows
	}
	failures := make([]BulkToolError, 0)
	for _, op := range result.Failed() {
		if op.Err == nil || errors.Is(op.Err, context.Canceled) || op.Tool.Name == "" || op.Tool.Provider == "" {
			continue
		}
		failures = append(failures, bulkToolErrorFromError(op.Tool.Name, op.Tool.Provider, op.Err))
		if action, ok := syncFailurePrivilegeAction(op); ok {
			if rows.PrivilegedActions == nil {
				rows.PrivilegedActions = make(map[string]provider.PrivilegeAction)
			}
			rows.PrivilegedActions[toolResultKey(op.Tool.Name, op.Tool.Provider)] = action
		}
	}
	rows.merge(bulkToolFailureRows(failures))
	return rows
}

func SyncAllFailureRows(result *SyncAllResult) BulkToolFailureRows {
	rows := BulkToolFailureRows{}
	if result == nil {
		return rows
	}
	rows.merge(bulkToolFailureRows(result.Failures))
	syncRows := SyncFailureRows(result.SyncResult)
	rows.merge(syncRows)
	return rows
}

func UpgradeAllFailureRows(result *UpgradeAllResult) BulkToolFailureRows {
	rows := BulkToolFailureRows{}
	if result == nil {
		return rows
	}
	rows.merge(bulkToolFailureRows(result.Failures))
	for _, failure := range result.Failures {
		if failure.Name == "" || failure.Provider == "" || !PrivilegeFailureRequiresApproval(failure.Message) {
			continue
		}
		if rows.PrivilegedActions == nil {
			rows.PrivilegedActions = make(map[string]provider.PrivilegeAction)
		}
		rows.PrivilegedActions[toolResultKey(failure.Name, failure.Provider)] = provider.PrivilegeActionUpgrade
	}
	return rows
}

func BulkToolFailureSummaryText(base string, rows BulkToolFailureRows) string {
	if len(rows.RowErrors) == 0 {
		return base
	}
	adminNeeded := 0
	for key := range rows.RowErrors {
		if _, ok := rows.PrivilegedActions[key]; ok {
			adminNeeded++
		}
	}
	failed := len(rows.RowErrors) - adminNeeded
	switch {
	case adminNeeded > 0 && failed > 0:
		return fmt.Sprintf("%s, %d need admin approval, %d failed", base, adminNeeded, failed)
	case adminNeeded > 0:
		return fmt.Sprintf("%s, %d need admin approval", base, adminNeeded)
	default:
		return fmt.Sprintf("%s, %d failed", base, len(rows.RowErrors))
	}
}

func SyncAllPhaseProgressText(phase string, total int) string {
	label := strings.TrimSpace(strings.TrimSuffix(phase, "…"))
	switch label {
	case "reading installed packages":
		label = "checking installed state"
	case "checking providers":
		label = "checking providers"
	}
	if label == "" {
		label = "checking installed state"
	}
	if total > 0 {
		return fmt.Sprintf("Syncing tools 0/%d: %s…", total, label)
	}
	return "Syncing tools: " + label + "…"
}

func SyncAllProgressTotal(tools []*ToolView, discovered []*ToolView) int {
	count := 0
	for _, tool := range tools {
		if tool != nil && tool.Tracked && !tool.Installed {
			count++
		}
	}
	for _, tool := range discovered {
		if tool != nil && tool.Name != "" && tool.Provider != "" {
			count++
		}
	}
	return count
}

func (rows *BulkToolFailureRows) merge(other BulkToolFailureRows) {
	if rows == nil {
		return
	}
	if len(other.RowErrors) > 0 {
		if rows.RowErrors == nil {
			rows.RowErrors = make(map[string]string, len(other.RowErrors))
		}
		for key, message := range other.RowErrors {
			rows.RowErrors[key] = message
		}
	}
	if len(other.RowActionErrors) > 0 {
		if rows.RowActionErrors == nil {
			rows.RowActionErrors = make(map[string]*provider.ActionError, len(other.RowActionErrors))
		}
		for key, actionErr := range other.RowActionErrors {
			rows.RowActionErrors[key] = actionErr
		}
	}
	if len(other.PrivilegedActions) > 0 {
		if rows.PrivilegedActions == nil {
			rows.PrivilegedActions = make(map[string]provider.PrivilegeAction, len(other.PrivilegedActions))
		}
		for key, action := range other.PrivilegedActions {
			rows.PrivilegedActions[key] = action
		}
	}
}

func bulkToolFailureRows(failures []BulkToolError) BulkToolFailureRows {
	rows := BulkToolFailureRows{}
	for _, failure := range failures {
		if failure.Name == "" || failure.Provider == "" {
			continue
		}
		key := toolResultKey(failure.Name, failure.Provider)
		if failure.Message != "" {
			if rows.RowErrors == nil {
				rows.RowErrors = make(map[string]string)
			}
			rows.RowErrors[key] = failure.Message
		}
		if failure.ActionError != nil {
			if rows.RowActionErrors == nil {
				rows.RowActionErrors = make(map[string]*provider.ActionError)
			}
			rows.RowActionErrors[key] = failure.ActionError
		}
	}
	return rows
}

func syncFailurePrivilegeAction(op isync.SyncOp) (provider.PrivilegeAction, bool) {
	if op.Err == nil || errors.Is(op.Err, context.Canceled) || !PrivilegeFailureRequiresApproval(op.Err.Error()) {
		return "", false
	}
	switch op.Kind {
	case isync.OpInstall, isync.OpFailed:
		return provider.PrivilegeActionInstall, true
	case isync.OpUninstall:
		return provider.PrivilegeActionUninstall, true
	default:
		return "", false
	}
}

func toolResultKey(name, providerName string) string {
	return name + "\x00" + providerName
}

func SyncAllToolProgressText(event isync.ProgressEvent, current, total int) string {
	label := syncAllToolProgressLabel(event)
	if total > 0 {
		return fmt.Sprintf("Syncing tools %d/%d: %s", current, total, label)
	}
	return "Syncing tools: " + label
}

func syncAllToolProgressLabel(event isync.ProgressEvent) string {
	name := event.Tool.Name
	message := strings.TrimSpace(strings.TrimSuffix(event.Message, "…"))
	switch {
	case strings.HasPrefix(message, "Adding "):
		return "adding discovered " + name + " to config…"
	case strings.HasPrefix(message, "Added "):
		return "added discovered " + name + " to config"
	case strings.HasPrefix(message, "Would add "):
		return "would add discovered " + name + " to config"
	case strings.HasPrefix(message, "Failed adding "):
		return "failed adding discovered " + name + " to config"
	case strings.HasPrefix(message, "Installing "):
		return "installing missing " + name + "…"
	case strings.HasPrefix(message, "Installed "):
		return "installed missing " + name
	case strings.HasPrefix(message, "Admin approval needed for "):
		return "admin approval needed for " + name
	case strings.HasPrefix(message, "Skipped installing "):
		if progressEventNeedsAdmin(event) {
			return "admin approval needed for " + name
		}
		return "skipped missing " + name
	case strings.HasPrefix(message, "Failed installing "):
		return "failed installing missing " + name
	case strings.HasPrefix(message, "Cancelled installing "):
		return "cancelled installing missing " + name
	default:
		if message == "" {
			return name
		}
		return strings.ToLower(message)
	}
}

func UpgradeAllProgressText(event isync.ProgressEvent, current, total int) string {
	label := upgradeAllProgressLabel(event)
	if total > 0 {
		return fmt.Sprintf("Upgrading tools %d/%d: %s", current, total, label)
	}
	return "Upgrading tools: " + label
}

func UpgradeAllProgressTotal(tools []*ToolView) int {
	count := 0
	for _, tool := range tools {
		if tool != nil && tool.Installed && tool.Outdated {
			count++
		}
	}
	return count
}

func ToolNameWithVersion(name, version string) string {
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	if name == "" || version == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, version)
}

func upgradeAllProgressLabel(event isync.ProgressEvent) string {
	name := ToolNameWithVersion(event.Tool.Name, event.TargetVersion)
	message := strings.TrimSpace(strings.TrimSuffix(event.Message, "…"))
	switch {
	case strings.HasPrefix(message, "Upgrading "):
		return name + "…"
	case strings.HasPrefix(message, "Upgraded "):
		return name + " upgraded"
	case strings.HasPrefix(message, "Admin approval needed for "):
		return name + " needs admin approval"
	case strings.HasPrefix(message, "Skipped upgrading "):
		if progressEventNeedsAdmin(event) {
			return name + " needs admin approval"
		}
		return name + " skipped"
	case strings.HasPrefix(message, "Failed upgrading "):
		return name + " failed"
	default:
		if message == "" {
			return name
		}
		return strings.ToLower(message)
	}
}

func progressEventNeedsAdmin(event isync.ProgressEvent) bool {
	if event.Err == nil {
		return false
	}
	return isPrivilegeErrorText(event.Err.Error())
}

func isPrivilegeErrorText(message string) bool {
	// Routed through the single classifier in privilege.go so the two consumers cannot drift.
	return isPrivilegedInstallFailure(message)
}

func RefreshToolProgressStatus(providerLabel, toolName string, done, total int) string {
	status := RefreshToolsStatus(nil, done, total)
	if total <= 0 {
		return status
	}
	active := strings.TrimSpace(providerLabel)
	toolName = strings.TrimSpace(toolName)
	if toolName != "" {
		if active == "" {
			active = toolName
		} else {
			active += "/" + toolName
		}
	}
	if active == "" {
		return status
	}
	return status + ": " + active
}

func RefreshDiscoveredProgressText(event RefreshDiscoveredProgressEvent) string {
	index, total := normalizedProgress(event.Index, event.Total)
	providerName := strings.TrimSpace(event.Provider)
	if providerName == "" {
		return fmt.Sprintf("Finding local tools %d/%d…", index, total)
	}
	return fmt.Sprintf("Finding local tools %d/%d: %s…", index, total, providerName)
}

func normalizedProgress(index, total int) (int, int) {
	if total <= 0 {
		total = index
	}
	if index < 0 {
		index = 0
	}
	if total < 0 {
		total = 0
	}
	return index, total
}

func normalizedDoneTotal(done, total int) (int, int) {
	if done < 0 {
		done = 0
	}
	if total < 0 {
		total = 0
	}
	if done > total {
		done = total
	}
	return done, total
}

func normalizedLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if label = strings.TrimSpace(label); label != "" {
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}
