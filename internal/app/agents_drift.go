package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

const (
	driftSectionTitle        = "Native items APM does not manage:"
	driftIgnoredEntriesTitle = "Ignored entries:"
	driftStaleEntriesTitle   = "Ignore entries matching nothing:"
	driftCoverageTitle       = "Coverage incomplete (drift may be under-reported):"
)

type agentsDriftReport struct {
	Unowned       []agentDisposition
	Ignored       []agentDisposition
	IgnoreEntries []config.AgentIgnoreEntry
	Stale         []config.AgentIgnoreEntry
	Retained      []agentDisposition
	Incomplete    []nativeClientCoverage
}

// AgentsDrift reports native agent state APM does not own; it never plans, so partial evidence still reports.
func (a *App) AgentsDrift(ctx context.Context, all bool) (string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", err
	}
	report, err := a.agentsDriftReport(ctx, cfg, 0)
	if err != nil {
		return "", err
	}
	return report.Render(all), nil
}

func (a *App) agentsDriftReport(ctx context.Context, cfg *config.RootConfig, perClient time.Duration) (agentsDriftReport, error) {
	inventory := a.gatherNativeAgentsWithin(ctx, perClient)
	managed, err := loadAPMManagedIndex()
	if err != nil {
		return agentsDriftReport{}, err
	}
	dispositions := subtractAPMManaged(resolveAgentDispositions(inventory.Observations), managed)
	return buildAgentsDriftReport(dispositions, hostAgentIgnoreEntries(cfg), inventory.Incomplete()), nil
}

func hostAgentIgnoreEntries(cfg *config.RootConfig) []config.AgentIgnoreEntry {
	if cfg == nil || cfg.Agents == nil {
		return nil
	}
	full := currentHostname()
	short := shortHostname(full)
	var out []config.AgentIgnoreEntry
	for _, entry := range cfg.Agents.Ignored {
		host := strings.TrimSpace(entry.Host)
		if strings.EqualFold(host, full) || strings.EqualFold(host, short) {
			out = append(out, entry)
		}
	}
	return out
}

func agentIgnoreMatches(entry config.AgentIgnoreEntry, observation agentObservation) bool {
	return entry.Target == observation.Target && entry.Kind == observation.Kind && entry.ID == observation.Identity
}

func buildAgentsDriftReport(dispositions []agentDisposition, entries []config.AgentIgnoreEntry, incomplete []nativeClientCoverage) agentsDriftReport {
	report := agentsDriftReport{Incomplete: incomplete}
	used := make([]bool, len(entries))
	for _, disposition := range dispositions {
		switch disposition.Action {
		case agentActionImport, agentActionSuppress:
			if i := slices.IndexFunc(entries, func(entry config.AgentIgnoreEntry) bool {
				return agentIgnoreMatches(entry, disposition.Observation)
			}); i >= 0 {
				used[i] = true
				report.Ignored = append(report.Ignored, disposition)
				continue
			}
			report.Unowned = append(report.Unowned, disposition)
		case agentActionRetain:
			report.Retained = append(report.Retained, disposition)
		}
	}
	for i, entry := range entries {
		if used[i] {
			report.IgnoreEntries = append(report.IgnoreEntries, entry)
			continue
		}
		report.Stale = append(report.Stale, entry)
	}
	return report
}

func (r agentsDriftReport) Render(all bool) string {
	var out strings.Builder
	writeDriftCoverage(&out, r.Incomplete)
	if len(r.Unowned) == 0 {
		out.WriteString("No native agent drift.\n")
	} else {
		writeMigrationSection(&out, driftSectionTitle, r.Unowned, func(d agentDisposition) string {
			return migrationRow(d, replacedEvidence(d))
		})
	}
	fmt.Fprintf(&out, "Ignored: %d\n", len(r.Ignored))
	writeDriftEntries(&out, driftStaleEntriesTitle, r.Stale)
	if !all {
		return out.String()
	}
	writeDriftEntries(&out, driftIgnoredEntriesTitle, r.IgnoreEntries)
	writeMigrationSection(&out, retainedSectionTitle, r.Retained, func(d agentDisposition) string {
		return migrationRow(d, d.Reason)
	})
	return out.String()
}

func writeDriftCoverage(out *strings.Builder, incomplete []nativeClientCoverage) {
	if len(incomplete) == 0 {
		return
	}
	rows := make([]string, 0, len(incomplete))
	for _, coverage := range incomplete {
		rows = append(rows, coverage.Client+": "+driftCoverageReason(coverage))
	}
	sort.Strings(rows)
	out.WriteString(driftCoverageTitle + "\n")
	for _, row := range rows {
		out.WriteString("  " + row + "\n")
	}
}

func driftCoverageReason(coverage nativeClientCoverage) string {
	if coverage.Err == nil {
		return "not readable"
	}
	return strings.TrimSpace(strings.ReplaceAll(coverage.Err.Error(), "\n", "; "))
}

func writeDriftEntries(out *strings.Builder, title string, entries []config.AgentIgnoreEntry) {
	if len(entries) == 0 {
		return
	}
	rows := make([]string, 0, len(entries))
	for _, entry := range entries {
		row := entry.Target + "  " + entry.Kind + "  " + entry.ID
		if reason := strings.TrimSpace(entry.Reason); reason != "" {
			row += "  " + reason
		}
		rows = append(rows, row)
	}
	sort.Strings(rows)
	out.WriteString(title + "\n")
	for _, row := range slices.Compact(rows) {
		out.WriteString("  " + row + "\n")
	}
}
