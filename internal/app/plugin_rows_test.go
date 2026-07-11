package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestPluginRows_ManagedRowReportsPerAgentStatus(t *testing.T) {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.row.Outdated(); got != tc.want {
				t.Fatalf("Outdated() = %v, want %v", got, tc.want)
			}
		})
	}
}
