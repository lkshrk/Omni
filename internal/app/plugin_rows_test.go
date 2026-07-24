package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestPluginRows_ListErrorIsSurfaced(t *testing.T) {
	t.Parallel()
	adapter := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listErr:   errors.New("parse json: expected object, got null"),
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "market", Source: "owner/repo"}},
		Plugins:      []config.Plugin{{Name: "plugin", Marketplace: "market"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{adapter}))
	rows, _, err := a.PluginRows(t.Context())
	if err == nil || !strings.Contains(err.Error(), "expected object, got null") {
		t.Fatalf("PluginRows error = %v, want adapter parse error", err)
	}
	if rows != nil {
		t.Fatalf("PluginRows returned misleading rows after adapter parse error: %+v", rows)
	}
}

func TestPluginRows_ManagedRowReportsPerAgentStatus(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	codex := &stubPluginAdapter{id: "codex", available: false}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))
	rows, unmanaged, err := a.PluginRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "caveman" || rows[0].Marketplace != "caveman" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].PerAgentStatus["claude-code"] != app.PluginStatusMissing {
		t.Fatalf("expected missing (not installed on claude), got %v", rows[0].PerAgentStatus)
	}
	if rows[0].PerAgentStatus["codex"] != app.PluginStatusAgentUnavailable {
		t.Fatalf("expected agent-unavailable for codex, got %v", rows[0].PerAgentStatus)
	}
	if len(unmanaged) != 0 {
		t.Fatalf("expected no unmanaged entries, got %v", unmanaged)
	}
}

func TestPluginRows_VersionAndOutdated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		version       string
		latestVersion string
		wantOutdated  bool
	}{
		{"outdated when versions differ", "1.0.0", "2.0.0", true},
		{"not outdated when equal", "1.0.0", "1.0.0", false},
		{"not outdated when latest unknown", "1.0.0", "", false},
		{"not outdated when version unknown", "", "2.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claude := &stubPluginAdapter{
				id:        "claude-code",
				available: true,
				listedPlugins: []app.InstalledPlugin{
					{Name: "caveman", Marketplace: "caveman", Version: tc.version, LatestVersion: tc.latestVersion},
				},
			}
			agents := config.AgentsConfig{
				Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
				Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
			}
			a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
			rows, _, err := a.PluginRows(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %+v", rows)
			}
			if rows[0].Version != tc.version || rows[0].LatestVersion != tc.latestVersion {
				t.Fatalf("got Version=%q LatestVersion=%q, want Version=%q LatestVersion=%q",
					rows[0].Version, rows[0].LatestVersion, tc.version, tc.latestVersion)
			}
			if rows[0].Outdated() != tc.wantOutdated {
				t.Fatalf("Outdated() = %v, want %v", rows[0].Outdated(), tc.wantOutdated)
			}
		})
	}
}

func TestPluginRow_OutdatedShaMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  app.PluginRow
		want bool
	}{
		{"versioned plugin matching manifest version -> not outdated even when shas differ",
			app.PluginRow{Version: "6.1.1", LatestVersion: "6.1.1", Sha: "f2cbfbef", LatestSha: "d884ae04edebef577e82ff7c4e143debd0bbec9"}, false},
		{"manifest version newer -> outdated",
			app.PluginRow{Version: "6.1.0", LatestVersion: "6.1.1"}, true},
		{"sha-versioned plugin, catalog sha not prefixed by installed version -> outdated",
			app.PluginRow{Version: "25d22f864ad6", LatestSha: "d884ae04edebef577e82ff7c4e143debd0bbec9"}, true},
		{"sha-versioned plugin, catalog sha prefixed by installed version -> not outdated",
			app.PluginRow{Version: "25d22f864ad6", LatestSha: "25d22f864ad6ffab00112233445566778899aabb"}, false},
		{"no signals -> not outdated", app.PluginRow{}, false},
		{"raw sha fields alone are not a signal (no manifest version, no sha-looking version)",
			app.PluginRow{Sha: "aaa1111", LatestSha: "bbb2222"}, false},
		{"non-sha-looking version with no manifest version -> not outdated",
			app.PluginRow{Version: "1.0.0", LatestSha: "d884ae04edebef577e82ff7c4e143debd0bbec9"}, false},
		{"PathOutdated=true with no version signal -> outdated (the versionless-plugin case)",
			app.PluginRow{PathOutdated: boolPtr(true)}, true},
		{"PathOutdated=false with no version signal -> not outdated",
			app.PluginRow{PathOutdated: boolPtr(false)}, false},
		{"manifest version match wins over PathOutdated",
			app.PluginRow{Version: "1.0.0", LatestVersion: "1.0.0", PathOutdated: boolPtr(true)}, false},
		{"PathOutdated wins over sha-prefix fallback when both present",
			app.PluginRow{Version: "25d22f864ad6", LatestSha: "25d22f864ad6ffab00112233445566778899aabb", PathOutdated: boolPtr(true)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.row.Outdated(); got != tc.want {
				t.Fatalf("Outdated() = %v, want %v", got, tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestPluginRow_UpdateDisplay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		row     app.PluginRow
		want    app.PluginUpdateKind
		current string
		latest  string
	}{
		{"version pair differs -> upgrade with pair",
			app.PluginRow{Version: "6.1.0", LatestVersion: "6.1.1"}, app.PluginVersionUpgrade, "6.1.0", "6.1.1"},
		{"version pair equal, shas differ -> up to date (no sha-drift false positive)",
			app.PluginRow{Version: "6.1.1", LatestVersion: "6.1.1", Sha: "f2cbfbef", LatestSha: "d884ae04"}, app.PluginUpToDate, "", ""},
		{"no version pair, shas differ -> informational sha drift",
			app.PluginRow{Sha: "abcdef1234567", LatestSha: "9876543210fed"}, app.PluginShaDrift, "abcdef1234567", "9876543210fed"},
		{"sha-versioned outdated, no displayable pair -> update available",
			app.PluginRow{Version: "25d22f864ad6", LatestSha: "d884ae04edebef577e82ff7c4e143debd0bbec9"}, app.PluginUpdateAvailable, "", ""},
		{"PathOutdated with no version signal -> update available",
			app.PluginRow{PathOutdated: boolPtr(true)}, app.PluginUpdateAvailable, "", ""},
		{"nothing -> up to date", app.PluginRow{}, app.PluginUpToDate, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.row.Update()
			if got.Kind != tc.want {
				t.Fatalf("Update().Kind = %v, want %v", got.Kind, tc.want)
			}
			if got.Current != tc.current || got.Latest != tc.latest {
				t.Fatalf("Update() pair = (%q,%q), want (%q,%q)", got.Current, got.Latest, tc.current, tc.latest)
			}
			// The status verdict and the display verdict must agree on outdated-ness.
			if outdated := got.Kind != app.PluginUpToDate; outdated != tc.row.Outdated() && got.Kind != app.PluginShaDrift {
				t.Fatalf("Update() outdated=%v disagrees with Outdated()=%v", outdated, tc.row.Outdated())
			}
		})
	}
}

// TestPluginRows_PathOutdatedMergedFromAdapter confirms PluginRows copies a
// versionless plugin's PathOutdated signal from InstalledPlugin into the row
// — this is the actual update-all bug: without it, plugins with no manifest
// version (the common case) never show as outdated regardless of adapter
// signal, so "U" silently has nothing to update.
func TestPluginRows_PathOutdatedMergedFromAdapter(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listedPlugins: []app.InstalledPlugin{
			{Name: "superpowers", Marketplace: "caveman", PathOutdated: boolPtr(true)},
		},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "superpowers", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	rows, _, err := a.PluginRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", rows)
	}
	if !rows[0].Outdated() {
		t.Fatalf("expected versionless plugin with PathOutdated=true to report Outdated()=true, got row %+v", rows[0])
	}
}
