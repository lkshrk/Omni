package app

import "context"

// AgentsNativeRow is one natively installed artifact APM does not manage, addressed the same way
// `omni agents ignore` addresses it.
type AgentsNativeRow struct {
	Target      string
	Kind        string
	Identity    string
	Source      string
	Ignored     bool
	IgnoreHost  string
	Reason      string
	Adoptable   bool
	InstallRoot string
}

// Selector addresses this row for ignore, unignore and removal, using the host the row was read on
// so a caller never has to resolve the hostname itself.
func (r AgentsNativeRow) Selector() AgentIgnoreSelector {
	return AgentIgnoreSelector{Host: r.IgnoreHost, Target: r.Target, Kind: r.Kind, ID: r.Identity}
}

// AgentsNativeRows lists unowned native artifacts plus the ones an ignore entry already covers, so the
// view can show what is suppressed rather than hiding it.
func (a *App) AgentsNativeRows(ctx context.Context) ([]AgentsNativeRow, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	report, err := a.agentsDriftReport(ctx, cfg, 0)
	if err != nil {
		return nil, err
	}

	host := currentHostname()
	rows := make([]AgentsNativeRow, 0, len(report.Unowned)+len(report.Ignored))
	for _, d := range report.Unowned {
		rows = append(rows, nativeRow(d, false, host))
	}
	for _, d := range report.Ignored {
		rows = append(rows, nativeRow(d, true, host))
	}
	return rows, nil
}

func nativeRow(d agentDisposition, ignored bool, host string) AgentsNativeRow {
	return AgentsNativeRow{
		Target:      d.Observation.Target,
		Kind:        d.Observation.Kind,
		Identity:    d.Observation.Identity,
		Source:      d.Observation.Source,
		Ignored:     ignored,
		IgnoreHost:  host,
		Reason:      d.Reason,
		Adoptable:   d.Action == agentActionImport,
		InstallRoot: d.Observation.InstallRoot,
	}
}
