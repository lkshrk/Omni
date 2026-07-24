package app

import (
	"fmt"
	"sort"
	"strings"

	textutil "github.com/lkshrk/omni/internal/text"
)

type DashboardReconcileStepID string

const (
	ReconcileStepSyncTools     DashboardReconcileStepID = "sync-tools"
	ReconcileStepUpgradeTools  DashboardReconcileStepID = "upgrade-tools"
	ReconcileStepSyncDots      DashboardReconcileStepID = "sync-dots"
	ReconcileStepCommitDots    DashboardReconcileStepID = "commit-dots"
	ReconcileStepFixIgnore     DashboardReconcileStepID = "fix-ignore"
	ReconcileStepFixNvmManaged DashboardReconcileStepID = "fix-nvm-managed"
	ReconcileStepSyncAgents    DashboardReconcileStepID = "sync-agents"
)

type DashboardReconcilePlanInput struct {
	Tools           []*ToolView
	DiscoveredTools []*ToolView
	IgnoredTools    map[string]bool

	ToolsBusy       bool
	UpgradeBusy     bool
	AgentsOutOfSync int
	AgentsBusy      bool

	DotsRepo       string
	DotsConfigured bool
	DotsDisabled   bool
	DotsBusy       bool
	DotsEntries    []DotStatus
	DotsGitStatus  string

	Doctor *DoctorResult

	NvmManaged map[string]bool
}

type DashboardReconcilePlanStep struct {
	ID     DashboardReconcileStepID
	Label  string
	Detail string
}

type DashboardToolSummaryInput struct {
	Tools           []*ToolView
	DiscoveredTools []*ToolView
	IgnoredTools    map[string]bool

	ToolProviderPins       map[string]string
	EffectiveSystemManager string
	EffectivePythonManager string
	EffectiveNodeManager   string
	NvmManaged             map[string]bool
}

type DashboardToolActivityInput struct {
	Tools           []*ToolView
	DiscoveredTools []*ToolView
	IgnoredTools    map[string]bool

	PendingKeys map[string]bool
	ActiveKeys  map[string]bool
	RowKey      string

	Loading      bool
	UpgradeBusy  bool
	ProgressText string

	ToolProviderPins       map[string]string
	EffectiveSystemManager string
	EffectivePythonManager string
	EffectiveNodeManager   string

	NvmManaged map[string]bool
}

type DashboardToolSummary struct {
	Tracked        int
	Updates        int
	OutOfSync      int
	Installed      int
	Available      int
	Ignored        int
	UpdateNames    []string
	OutOfSyncNames []string
	InstalledNames []string
	AvailableNames []string
	IgnoredNames   []string
}

func ToolRowKey(name, providerName string) string {
	return name + "\x00" + providerName
}

func BuildDashboardToolSummary(input DashboardToolSummaryInput) DashboardToolSummary {
	var summary DashboardToolSummary
	dashboardVisitToolRows(input.Tools, input.DiscoveredTools, func(tool *ToolView, discovered bool) {
		dashboardAccumulateToolSummary(&summary, input, tool, discovered)
	})
	sort.Strings(summary.UpdateNames)
	sort.Strings(summary.OutOfSyncNames)
	sort.Strings(summary.InstalledNames)
	sort.Strings(summary.AvailableNames)
	sort.Strings(summary.IgnoredNames)
	return summary
}

func DashboardToolSyncQueuedNames(input DashboardToolActivityInput) []string {
	var names []string
	dashboardVisitToolRows(input.Tools, input.DiscoveredTools, func(tool *ToolView, discovered bool) {
		if !input.PendingKeys[ToolRowKey(tool.Name, tool.Provider)] {
			return
		}
		classification := dashboardToolActivityClassification(input, tool, discovered)
		if classification.Section == ToolViewSectionOutOfSync || !tool.Installed {
			names = append(names, dashboardToolSyncIssueName(tool, classification.SyncStatus))
		}
	})
	sort.Strings(names)
	return names
}

func DashboardUpgradeNames(input DashboardToolActivityInput) (active, waiting []string) {
	dashboardVisitToolRows(input.Tools, input.DiscoveredTools, func(tool *ToolView, discovered bool) {
		key := ToolRowKey(tool.Name, tool.Provider)
		classification := dashboardToolActivityClassification(input, tool, discovered)
		if classification.Section != ToolViewSectionUpdates {
			return
		}
		name := dashboardToolUpdateName(tool)
		switch {
		case input.ActiveKeys[key] || input.RowKey == key:
			active = append(active, name)
		case input.PendingKeys[key]:
			waiting = append(waiting, name)
		}
	})
	sort.Strings(active)
	sort.Strings(waiting)
	return active, waiting
}

func DashboardToolSyncBusy(input DashboardToolActivityInput) bool {
	if !input.Loading || input.UpgradeBusy {
		return false
	}
	if len(DashboardToolSyncQueuedNames(input)) > 0 {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(input.ProgressText))
	return strings.Contains(text, "sync") || strings.Contains(text, "install") || strings.Contains(text, "add")
}

func dashboardVisitToolRows(tools, discoveredTools []*ToolView, visit func(*ToolView, bool)) {
	discoveredKeys := make(map[string]bool, len(discoveredTools))
	for _, tool := range discoveredTools {
		if tool != nil {
			discoveredKeys[ToolRowKey(tool.Name, tool.Provider)] = true
		}
	}
	seen := make(map[string]bool, len(tools)+len(discoveredTools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		key := ToolRowKey(tool.Name, tool.Provider)
		visit(tool, discoveredKeys[key])
		seen[key] = true
	}
	for _, tool := range discoveredTools {
		if tool == nil || seen[ToolRowKey(tool.Name, tool.Provider)] {
			continue
		}
		visit(tool, true)
	}
}

func dashboardToolActivityClassification(input DashboardToolActivityInput, tool *ToolView, discovered bool) ToolViewClassification {
	return ClassifyToolView(tool, ToolClassificationContext{
		Ignored:                input.IgnoredTools[tool.Name],
		Discovered:             discovered,
		ToolProviderPins:       input.ToolProviderPins,
		EffectiveSystemManager: input.EffectiveSystemManager,
		EffectivePythonManager: input.EffectivePythonManager,
		EffectiveNodeManager:   input.EffectiveNodeManager,
		NvmManaged:             input.NvmManaged,
	})
}

func dashboardAccumulateToolSummary(summary *DashboardToolSummary, input DashboardToolSummaryInput, tool *ToolView, discovered bool) {
	if tool.Tracked {
		summary.Tracked++
	}
	if tool.Installed {
		summary.Installed++
		summary.InstalledNames = append(summary.InstalledNames, dashboardToolName(tool))
	}
	ctx := ToolClassificationContext{
		Ignored:                input.IgnoredTools[tool.Name],
		Discovered:             discovered,
		ToolProviderPins:       input.ToolProviderPins,
		EffectiveSystemManager: input.EffectiveSystemManager,
		EffectivePythonManager: input.EffectivePythonManager,
		EffectiveNodeManager:   input.EffectiveNodeManager,
		NvmManaged:             input.NvmManaged,
	}
	classification := ClassifyToolView(tool, ctx)
	switch classification.Section {
	case ToolViewSectionUpdates:
		summary.Updates++
		summary.UpdateNames = append(summary.UpdateNames, dashboardToolUpdateName(tool))
	case ToolViewSectionOutOfSync:
		summary.OutOfSync++
		summary.OutOfSyncNames = append(summary.OutOfSyncNames, dashboardToolSyncIssueName(tool, classification.SyncStatus))
	case ToolViewSectionAvailable:
		summary.Available++
		summary.AvailableNames = append(summary.AvailableNames, dashboardToolName(tool))
	case ToolViewSectionIgnored:
		summary.Ignored++
		summary.IgnoredNames = append(summary.IgnoredNames, dashboardToolName(tool))
	}
}

func dashboardToolName(tool *ToolView) string {
	if tool == nil {
		return ""
	}
	return tool.Name
}

func dashboardToolUpdateName(tool *ToolView) string {
	if tool == nil {
		return ""
	}
	latest := strings.TrimSpace(tool.LatestVersion)
	if latest == "" {
		latest = "?"
	}
	if latest == "?" {
		return tool.Name
	}
	return fmt.Sprintf("%s (%s)", tool.Name, latest)
}

func dashboardToolSyncIssueName(tool *ToolView, status ToolSyncStatus) string {
	name := dashboardToolName(tool)
	switch status {
	case ToolSyncMissing:
		return name + " missing"
	case ToolSyncUnclaimed:
		return name + " local only"
	case ToolSyncWrongProvider:
		return name + " provider mismatch"
	case ToolSyncNvmManaged:
		return name + " nvm-managed"
	default:
		return name
	}
}

func DashboardReconcilePlan(input DashboardReconcilePlanInput) []DashboardReconcilePlanStep {
	steps := make([]DashboardReconcilePlanStep, 0, 7)
	if !input.ToolsBusy {
		if nvm := dashboardNvmManagedCount(input); nvm > 0 {
			steps = append(steps, DashboardReconcilePlanStep{
				ID:     ReconcileStepFixNvmManaged,
				Label:  "Fix nvm-managed tools",
				Detail: textutil.PluralCount(nvm, "nvm-managed tool", "nvm-managed tools"),
			})
		}
		if missing, discovered := dashboardToolSyncCounts(input); missing > 0 || discovered > 0 {
			steps = append(steps, DashboardReconcilePlanStep{
				ID:     ReconcileStepSyncTools,
				Label:  "Sync tools",
				Detail: dashboardToolSyncDetail(missing, discovered),
			})
		}
		if updates := dashboardToolUpdateCount(input); updates > 0 && !input.UpgradeBusy {
			steps = append(steps, DashboardReconcilePlanStep{
				ID:     ReconcileStepUpgradeTools,
				Label:  "Upgrade tools",
				Detail: textutil.PluralCount(updates, "outdated tool", "outdated tools"),
			})
		}
	}
	if input.AgentsOutOfSync > 0 && !input.AgentsBusy {
		steps = append(steps, DashboardReconcilePlanStep{
			ID:     ReconcileStepSyncAgents,
			Label:  "Sync agents",
			Detail: textutil.PluralCount(input.AgentsOutOfSync, "missing agent item", "missing agent items"),
		})
	}
	if dashboardDotsConfigured(input) && !input.DotsBusy {
		if counts := DotStatusesFileCounts(input.DotsEntries); counts.OutOfSync > 0 {
			steps = append(steps, DashboardReconcilePlanStep{
				ID:     ReconcileStepSyncDots,
				Label:  "Sync dotfiles",
				Detail: textutil.PluralCount(counts.OutOfSync, "out-of-sync entry", "out-of-sync entries"),
			})
		}
		if detail := dashboardDotsCommitDetail(input.DotsGitStatus); detail != "" {
			steps = append(steps, DashboardReconcilePlanStep{
				ID:     ReconcileStepCommitDots,
				Label:  "Commit dotfiles",
				Detail: detail,
			})
		}
	}
	if detail, ok := DotsIgnoreFinding(input.Doctor); ok {
		steps = append(steps, DashboardReconcilePlanStep{
			ID:     ReconcileStepFixIgnore,
			Label:  "Fix ignore patterns",
			Detail: detail,
		})
	}
	return steps
}

func DashboardReconcilePlanHasStep(steps []DashboardReconcilePlanStep, id DashboardReconcileStepID) bool {
	for _, step := range steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

func DotsIgnoreFinding(result *DoctorResult) (string, bool) {
	if result == nil {
		return "", false
	}
	for _, check := range result.Checks {
		if check.ID == "dots.ignore" && check.Status == DoctorStatusWarn {
			return check.Message, true
		}
	}
	return "", false
}

func dashboardNvmManagedCount(input DashboardReconcilePlanInput) int {
	if n := len(sortedNvmManagedNames(input.NvmManaged, input.IgnoredTools)); n > 0 {
		return n
	}
	return DoctorNvmManagedDriftCount(input.Doctor)
}

func dashboardToolSyncCounts(input DashboardReconcilePlanInput) (missing, discovered int) {
	for _, tool := range input.Tools {
		if tool != nil && tool.Tracked && !tool.Installed && !input.IgnoredTools[tool.Name] {
			missing++
		}
	}
	for _, tool := range input.DiscoveredTools {
		if tool != nil && tool.Name != "" && tool.Provider != "" && !input.IgnoredTools[tool.Name] {
			discovered++
		}
	}
	return missing, discovered
}

func dashboardToolSyncDetail(missing, discovered int) string {
	parts := make([]string, 0, 2)
	if missing > 0 {
		parts = append(parts, "install "+textutil.PluralCount(missing, "missing tool", "missing tools"))
	}
	if discovered > 0 {
		parts = append(parts, "add "+textutil.PluralCount(discovered, "local tool", "local tools"))
	}
	return strings.Join(parts, ", ")
}

func dashboardToolUpdateCount(input DashboardReconcilePlanInput) int {
	seen := make(map[string]bool, len(input.Tools)+len(input.DiscoveredTools))
	count := 0
	for _, tool := range input.Tools {
		if dashboardToolHasUpdate(tool, input.IgnoredTools) {
			count++
		}
		if tool != nil {
			seen[tool.Name+"\x00"+tool.Provider] = true
		}
	}
	for _, tool := range input.DiscoveredTools {
		if tool == nil || seen[tool.Name+"\x00"+tool.Provider] {
			continue
		}
		if dashboardToolHasUpdate(tool, input.IgnoredTools) {
			count++
		}
	}
	return count
}

func dashboardToolHasUpdate(tool *ToolView, ignored map[string]bool) bool {
	return tool != nil && !ignored[tool.Name] && tool.Installed && tool.Outdated
}

func dashboardDotsConfigured(input DashboardReconcilePlanInput) bool {
	if input.DotsConfigured {
		return !input.DotsDisabled
	}
	return !input.DotsDisabled && strings.TrimSpace(input.DotsRepo) != ""
}

func dashboardDotsCommitDetail(gitStatus string) string {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(gitStatus), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return textutil.PluralCount(count, "repo change", "repo changes")
}
