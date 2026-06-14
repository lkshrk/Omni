package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// DriftClass identifies the kind of config-vs-reality mismatch.
type DriftClass string

const (
	// DriftProviderUnusable means the configured provider is unavailable/unregistered,
	// the tool has no usable native or fallback route, and the tool is not present.
	DriftProviderUnusable DriftClass = "provider-unusable"

	// DriftWrongProvider means the tool is installed by a different provider than configured.
	DriftWrongProvider DriftClass = "wrong-provider"

	// DriftUnavailableButPresent means the configured provider is unavailable yet the tool is present on PATH.
	DriftUnavailableButPresent DriftClass = "unavailable-but-present"
)

// DriftFinding describes a single config-vs-reality mismatch for one tool.
type DriftFinding struct {
	Tool       string
	Provider   string // configured provider
	Class      DriftClass
	Suggestion string
	Extra      string // optional context (e.g. actual installed-with value)
}

// driftReport is the raw output of buildDriftReport; kept separate so tests can inspect findings directly.
type driftReport struct {
	findings []DriftFinding
	warnings []string // resolver warnings surfaced as detail lines
}

// doctorDrift runs drift analysis and appends the result to the doctor report.
func (a *App) doctorDrift(ctx context.Context, result *DoctorResult, cfg *config.RootConfig) {
	report, err := a.buildDriftReport(ctx, cfg)
	if err != nil {
		result.addCheck("drift", "Drift", DoctorStatusWarn,
			"drift check could not complete", err.Error())
		return
	}
	a.addDriftCheck(result, report)
}

// buildDriftReport returns the raw drift findings; package-private so tests can inspect directly.
func (a *App) buildDriftReport(ctx context.Context, cfg *config.RootConfig) (*driftReport, error) {
	// Use currentResolvedTools (not currentResolvedToolEntries) to get the resolved install route per tool.
	resolved, warnings := a.currentResolvedTools(ctx, cfg)
	if len(resolved) == 0 {
		return &driftReport{warnings: warnings}, nil
	}

	availMap, err := a.providerAvailabilityMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking provider availability: %w", err)
	}

	cached, err := a.cachedToolMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading tool cache: %w", err)
	}

	var findings []DriftFinding
	for _, rt := range resolved {
		t := rt.entry
		if t.Provider == "" {
			continue
		}

		provAvail, provRegistered := availMap[t.Provider]
		tc := cached[t.Name]

		switch {
		case !provRegistered || !provAvail:
			// Skip if the resolver already found a usable native or fallback route;
			// the config correctly handles the unavailability in that case.
			if rt.route.Kind == installRouteNative || rt.route.Kind == installRouteFallbackEligible {
				continue
			}

			if tc != nil && tc.Installed && tc.InstalledWith != "" {
				// Provider unavailable but tool is present via another manager (e.g. corepack).
				findings = append(findings, DriftFinding{
					Tool:     t.Name,
					Provider: t.Provider,
					Class:    DriftUnavailableButPresent,
					Extra:    tc.InstalledWith,
					Suggestion: fmt.Sprintf(
						"reconfigure provider: tool %q is present via %q but pinned to unavailable %q — update provider in config or add %q as a fallback",
						t.Name, tc.InstalledWith, t.Provider, tc.InstalledWith),
				})
			} else if tc == nil || !tc.Installed {
				findings = append(findings, DriftFinding{
					Tool:     t.Name,
					Provider: t.Provider,
					Class:    DriftProviderUnusable,
					Suggestion: fmt.Sprintf(
						"provider %q is not available on this host for tool %q — add a fallback provider or change the configured provider",
						t.Provider, t.Name),
				})
			}

		case tc != nil && tc.Installed && tc.InstalledWith != "" && tc.InstalledWith != t.Provider:
			// InstalledWith="" (PATH-detected tools) is intentionally excluded — no concrete manager claim means no mismatch.
			findings = append(findings, DriftFinding{
				Tool:     t.Name,
				Provider: t.Provider,
				Class:    DriftWrongProvider,
				Extra:    tc.InstalledWith,
				Suggestion: fmt.Sprintf(
					"tool %q is configured for %q but installed via %q — run 'omni tools sync' to reconcile or update the provider in config",
					t.Name, t.Provider, tc.InstalledWith),
			})
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Class != findings[j].Class {
			return findings[i].Class < findings[j].Class
		}
		return findings[i].Tool < findings[j].Tool
	})

	return &driftReport{findings: findings, warnings: warnings}, nil
}

// addDriftCheck converts a driftReport into a DoctorCheck and appends it to result.
func (a *App) addDriftCheck(result *DoctorResult, report *driftReport) {
	// Always surface resolver warnings — resolution problems are meaningful even without drift findings.
	extraDetails := make([]string, 0, len(report.warnings))
	for _, w := range report.warnings {
		extraDetails = append(extraDetails, "resolver warning: "+w)
	}

	if len(report.findings) == 0 {
		result.addCheck("drift", "Drift", DoctorStatusOK, "no config-vs-reality drift detected", extraDetails...)
		return
	}

	groups := make(map[DriftClass][]DriftFinding)
	for _, f := range report.findings {
		groups[f.Class] = append(groups[f.Class], f)
	}

	var detailGroups []DoctorDetailGroup
	classOrder := []DriftClass{DriftProviderUnusable, DriftWrongProvider, DriftUnavailableButPresent}
	for _, class := range classOrder {
		findings, ok := groups[class]
		if !ok {
			continue
		}
		header := driftClassLabel(class)
		items := make([]string, 0, len(findings)*2)
		for _, f := range findings {
			item := f.Tool + " [" + f.Provider + "]"
			if f.Extra != "" {
				item += " (actual: " + f.Extra + ")"
			}
			items = append(items, item, "  suggestion: "+f.Suggestion)
		}
		detailGroups = append(detailGroups, DoctorDetailGroup{Header: header, Items: items})
	}

	check := DoctorCheck{
		ID:      "drift",
		Label:   "Drift",
		Status:  DoctorStatusWarn,
		Message: fmt.Sprintf("%d drift finding(s) — config does not match reality", len(report.findings)),
		Details: extraDetails,
		Groups:  detailGroups,
	}
	result.Checks = append(result.Checks, check)
}

func driftClassLabel(class DriftClass) string {
	switch class {
	case DriftProviderUnusable:
		return "Provider unavailable (tool unreachable)"
	case DriftWrongProvider:
		return "Wrong-provider install"
	case DriftUnavailableButPresent:
		return "Provider unavailable but tool present another way"
	default:
		return string(class)
	}
}

// providerAvailabilityMap returns name → available for every registered provider.
func (a *App) providerAvailabilityMap(ctx context.Context) (map[string]bool, error) {
	if a.registry == nil {
		return map[string]bool{}, nil
	}
	all := a.registry.All()
	m := make(map[string]bool, len(all))
	for _, p := range all {
		avail, err := p.Available(ctx)
		if err != nil {
			// Treat availability-check error as unavailable; don't abort the whole check.
			avail = false
		}
		m[p.Name()] = avail
	}
	return m, nil
}

// cachedToolMap returns a name-keyed map of ToolCache rows from the DB.
func (a *App) cachedToolMap(ctx context.Context) (map[string]*database.ToolCache, error) {
	db := a.readDB()
	if db == nil {
		return map[string]*database.ToolCache{}, nil
	}
	rows, err := db.List(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]*database.ToolCache, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if _, exists := m[row.Name]; !exists {
			m[row.Name] = row
		}
	}
	return m, nil
}
