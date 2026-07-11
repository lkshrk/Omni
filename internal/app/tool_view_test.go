package app_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
)

func TestClassifyToolView(t *testing.T) {
	tests := []struct {
		name       string
		tool       *database.ToolCache
		context    app.ToolClassificationContext
		wantSec    app.ToolViewSection
		wantSync   app.ToolSyncStatus
		wantProv   string
		wantSource string
	}{
		{
			name:     "ignored wins",
			tool:     &database.ToolCache{Name: "fd", Provider: "brew", Tracked: true, Installed: false},
			context:  app.ToolClassificationContext{Ignored: true},
			wantSec:  app.ToolViewSectionIgnored,
			wantSync: app.ToolSyncMissing,
		},
		{
			name:     "outdated update section",
			tool:     &database.ToolCache{Name: "git", Provider: "brew", Tracked: true, Installed: true, Outdated: true},
			wantSec:  app.ToolViewSectionUpdates,
			wantSync: app.ToolSyncOK,
		},
		{
			name:     "discovered local tool is out of sync",
			tool:     &database.ToolCache{Name: "rg", Provider: "brew", Installed: true},
			context:  app.ToolClassificationContext{Discovered: true},
			wantSec:  app.ToolViewSectionOutOfSync,
			wantSync: app.ToolSyncUnclaimed,
		},
		{
			name:     "missing tracked tool is out of sync",
			tool:     &database.ToolCache{Name: "bat", Provider: "brew", Tracked: true, Installed: false},
			wantSec:  app.ToolViewSectionOutOfSync,
			wantSync: app.ToolSyncMissing,
		},
		{
			name: "pinned provider mismatch is out of sync",
			tool: &database.ToolCache{Name: "typescript", Provider: "node", Tracked: true, Installed: true, InstalledWith: "bun"},
			context: app.ToolClassificationContext{
				ToolProviderPins:     map[string]string{"typescript": "npm"},
				EffectiveNodeManager: "bun",
			},
			wantSec:    app.ToolViewSectionOutOfSync,
			wantSync:   app.ToolSyncWrongProvider,
			wantProv:   "npm",
			wantSource: "configured",
		},
		{
			name: "effective manager mismatch is out of sync",
			tool: &database.ToolCache{Name: "typescript", Provider: "node", Tracked: true, Installed: true, InstalledWith: "npm"},
			context: app.ToolClassificationContext{
				EffectiveNodeManager: "bun",
			},
			wantSec:    app.ToolViewSectionOutOfSync,
			wantSync:   app.ToolSyncWrongProvider,
			wantProv:   "bun",
			wantSource: "default",
		},
		{
			name:     "installed up to date",
			tool:     &database.ToolCache{Name: "git", Provider: "brew", Tracked: true, Installed: true},
			wantSec:  app.ToolViewSectionInstalled,
			wantSync: app.ToolSyncOK,
		},
		{
			name:     "search result is available",
			tool:     &database.ToolCache{Name: "jq", Provider: "brew", Tracked: false, Installed: false},
			wantSec:  app.ToolViewSectionAvailable,
			wantSync: app.ToolSyncUnclaimed,
		},
		{
			name:     "self-updating cask reads as an update, not quarantined",
			tool:     &database.ToolCache{Name: "battle-net", Provider: "brew", Tracked: true, Installed: true, Outdated: true, UpdateBlocked: app.UpdateBlockSelfUpdates},
			wantSec:  app.ToolViewSectionUpdates,
			wantSync: app.ToolSyncOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := app.ClassifyToolView(tt.tool, tt.context)
			if got.Section != tt.wantSec || got.SyncStatus != tt.wantSync {
				t.Fatalf("ClassifyToolView = %#v, want section=%s sync=%s", got, tt.wantSec, tt.wantSync)
			}
			gotProvider, gotSource := app.DesiredConcreteProviderForTool(tt.tool, tt.context)
			if gotProvider != tt.wantProv || gotSource != tt.wantSource {
				t.Fatalf("DesiredConcreteProviderForTool = (%q, %q), want (%q, %q)", gotProvider, gotSource, tt.wantProv, tt.wantSource)
			}
		})
	}
}

func TestToolProviderDisplayLabel(t *testing.T) {
	tests := []struct {
		name  string
		input app.ToolProviderDisplayInput
		want  string
	}{
		{name: "concrete system manager", input: app.ToolProviderDisplayInput{Provider: "brew"}, want: "brew"},
		{name: "system installed with concrete manager", input: app.ToolProviderDisplayInput{Provider: "system", InstalledWith: "brew"}, want: "brew"},
		{name: "system default manager", input: app.ToolProviderDisplayInput{Provider: "system", EffectiveSystemManager: "brew"}, want: "brew"},
		{name: "system without manager", input: app.ToolProviderDisplayInput{Provider: "system"}, want: "system"},
		{name: "python concrete manager normalizes pip", input: app.ToolProviderDisplayInput{Provider: "pip"}, want: "pip3"},
		{name: "node default manager", input: app.ToolProviderDisplayInput{Provider: "node", EffectiveNodeManager: "bun"}, want: "bun"},
		{name: "node default manager same as ecosystem", input: app.ToolProviderDisplayInput{Provider: "node", EffectiveNodeManager: "node"}, want: "node"},
		{name: "unknown provider", input: app.ToolProviderDisplayInput{Provider: "cargo"}, want: "cargo"},
		{name: "explicit provider override", input: app.ToolProviderDisplayInput{Provider: "node", ExplicitProvider: "npm", EffectiveNodeManager: "bun"}, want: "npm"},
		{name: "explicit provider wins when installed with ecosystem", input: app.ToolProviderDisplayInput{Provider: "node", InstalledWith: "node", ExplicitProvider: "pnpm"}, want: "pnpm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := app.ToolProviderDisplayLabel(tt.input); got != tt.want {
				t.Fatalf("ToolProviderDisplayLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolHasPrivilegeMarker(t *testing.T) {
	tests := []struct {
		name    string
		tool    *database.ToolCache
		context app.ToolClassificationContext
		want    bool
	}{
		{name: "nil tool", want: false},
		{name: "cached privilege metadata", tool: &database.ToolCache{Name: "parsec", Provider: "system", InstalledWith: "brew", Privilege: "maybe"}, want: true},
		{name: "concrete sudo backed provider", tool: &database.ToolCache{Name: "vim", Provider: "apt"}, want: true},
		{name: "concrete brew provider", tool: &database.ToolCache{Name: "git", Provider: "brew"}, want: false},
		{name: "logical system installed with sudo backed manager", tool: &database.ToolCache{Name: "vim", Provider: "system", InstalledWith: "dnf"}, want: true},
		{name: "logical system defaulting to sudo backed manager", tool: &database.ToolCache{Name: "vim", Provider: "system"}, context: app.ToolClassificationContext{EffectiveSystemManager: "apk"}, want: true},
		{name: "logical system defaulting to brew", tool: &database.ToolCache{Name: "ripgrep", Provider: "system"}, context: app.ToolClassificationContext{EffectiveSystemManager: "brew"}, want: false},
		{name: "non system ecosystem ignores system manager", tool: &database.ToolCache{Name: "typescript", Provider: "node"}, context: app.ToolClassificationContext{EffectiveSystemManager: "apt", EffectiveNodeManager: "bun"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := app.ToolHasPrivilegeMarker(tt.tool, tt.context); got != tt.want {
				t.Fatalf("ToolHasPrivilegeMarker = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToolProviderEcosystem(t *testing.T) {
	tests := []struct{ input, want string }{
		{"system", "system"},
		{"brew", "system"},
		{"apt", "system"},
		{"apk", "system"},
		{"dnf", "system"},
		{"pacman", "system"},
		{"zypper", "system"},
		{"python", "python"},
		{"pip", "python"},
		{"pip3", "python"},
		{"uv", "python"},
		{"node", "node"},
		{"npm", "node"},
		{"bun", "node"},
		{"pnpm", "node"},
		{"cargo", "cargo"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		if got := app.ToolProviderEcosystem(tt.input); got != tt.want {
			t.Errorf("ToolProviderEcosystem(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToolProviderDisplayRole(t *testing.T) {
	tests := []struct {
		input string
		want  app.ToolProviderDisplayRole
	}{
		{input: "system", want: app.ToolProviderDisplayRoleSystem},
		{input: "brew", want: app.ToolProviderDisplayRoleSystem},
		{input: "node", want: app.ToolProviderDisplayRoleNode},
		{input: "bun", want: app.ToolProviderDisplayRoleNode},
		{input: "python", want: app.ToolProviderDisplayRolePython},
		{input: "pip", want: app.ToolProviderDisplayRolePython},
		{input: "cargo", want: app.ToolProviderDisplayRoleOther},
	}
	for _, tt := range tests {
		if got := app.ToolProviderDisplayRoleFor(tt.input); got != tt.want {
			t.Errorf("ToolProviderDisplayRoleFor(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildToolViewList_PlacesIgnoreLabelToolsInIgnoredBucket(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "system", Tracked: true, Installed: true},
		{Name: "bat", Provider: "system", Tracked: true, Installed: true},
	}
	got := app.BuildToolViewList(app.ToolViewListOptions{
		Tools:        tools,
		IgnoreLabels: map[string]string{"bat": "tool"},
	})
	assertToolViewNames(t, got.Tools, []string{"git", "bat"})
	if got.Tools[len(got.Tools)-1].Name != "bat" {
		t.Fatalf("ignored tool = %q, want bat last in ignored bucket", got.Tools[len(got.Tools)-1].Name)
	}
	assertToolViewCounts(t, got.Counts, map[app.ToolViewSection]int{
		app.ToolViewSectionInstalled: 1,
		app.ToolViewSectionIgnored:   1,
	})
}

func TestBuildToolViewList_OrdersCountsAndDedupesSources(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "system", Tracked: true, Installed: true},
		{Name: "bat", Provider: "system", Tracked: true},
		{Name: "typescript", Provider: "node", Tracked: true, Installed: true, Outdated: true},
		{Name: "ignored-a", Provider: "system", Tracked: true, Installed: true},
	}
	discovered := []*database.ToolCache{
		{Name: "utm", Provider: "system", Installed: true},
		{Name: "git", Provider: "brew", Installed: true},
	}
	search := []*database.ToolCache{
		{Name: "jq", Provider: "system"},
		{Name: "bat", Provider: "system"},
		{Name: "utm", Provider: "system"},
	}

	got := app.BuildToolViewList(app.ToolViewListOptions{
		Tools:           tools,
		DiscoveredTools: discovered,
		SearchTools:     search,
		IgnoredTools:    map[string]bool{"ignored-a": true},
	})

	assertToolViewNames(t, got.Tools, []string{"typescript", "bat", "utm", "git", "jq", "ignored-a"})
	assertToolViewCounts(t, got.Counts, map[app.ToolViewSection]int{
		app.ToolViewSectionUpdates:   1,
		app.ToolViewSectionOutOfSync: 2,
		app.ToolViewSectionInstalled: 1,
		app.ToolViewSectionAvailable: 1,
		app.ToolViewSectionIgnored:   1,
	})
}

func TestBuildToolViewList_FiltersProviderAndGroupWithoutDroppingMatchingSearch(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "system", Tracked: true, Installed: true},
		{Name: "eslint", Provider: "node", Tracked: true, Installed: true},
		{Name: "ruff", Provider: "python", Tracked: true, Installed: true},
	}
	discovered := []*database.ToolCache{
		{Name: "utm", Provider: "system", Installed: true},
	}
	search := []*database.ToolCache{
		{Name: "ripgrep", Provider: "system"},
		{Name: "prettier", Provider: "node"},
	}

	got := app.BuildToolViewList(app.ToolViewListOptions{
		Tools:           tools,
		DiscoveredTools: discovered,
		SearchTools:     search,
		GroupFilter:     "work",
		ProviderFilter:  "system",
		ToolMemberships: map[string][]string{
			"git\x00system":     {"work"},
			"eslint\x00node":    {"work"},
			"ruff\x00python":    {"personal"},
			"ripgrep\x00system": {"work"},
		},
	})

	assertToolViewNames(t, got.Tools, []string{"git", "ripgrep"})
	assertToolViewCounts(t, got.Counts, map[app.ToolViewSection]int{
		app.ToolViewSectionInstalled: 1,
		app.ToolViewSectionAvailable: 1,
	})
}

func TestBuildToolViewList_FiltersQueryByToolNameAndProvider(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "system", Tracked: true, Installed: true},
		{Name: "eslint", Provider: "node", Tracked: true, Installed: true},
		{Name: "ruff", Provider: "python", Tracked: true, Installed: true},
	}
	discovered := []*database.ToolCache{
		{Name: "node-exporter", Provider: "system", Installed: true},
		{Name: "utm", Provider: "system", Installed: true},
	}

	got := app.BuildToolViewList(app.ToolViewListOptions{
		Tools:           tools,
		DiscoveredTools: discovered,
		Query:           "NODE",
	})

	assertToolViewNames(t, got.Tools, []string{"node-exporter", "eslint"})
	assertToolViewCounts(t, got.Counts, map[app.ToolViewSection]int{
		app.ToolViewSectionOutOfSync: 1,
		app.ToolViewSectionInstalled: 1,
	})
}

func assertToolViewNames(t *testing.T, tools []*database.ToolCache, want []string) {
	t.Helper()
	if len(tools) != len(want) {
		t.Fatalf("tool count = %d, want %d: %#v", len(tools), len(want), tools)
	}
	for i, name := range want {
		if tools[i].Name != name {
			t.Fatalf("tools[%d].Name = %q, want %q; all = %v", i, tools[i].Name, name, toolViewNames(tools))
		}
	}
}

func assertToolViewCounts(t *testing.T, got map[app.ToolViewSection]int, want map[app.ToolViewSection]int) {
	t.Helper()
	for _, section := range []app.ToolViewSection{
		app.ToolViewSectionUpdates,
		app.ToolViewSectionOutOfSync,
		app.ToolViewSectionInstalled,
		app.ToolViewSectionAvailable,
		app.ToolViewSectionIgnored,
	} {
		if got[section] != want[section] {
			t.Fatalf("count[%s] = %d, want %d; all counts = %#v", section, got[section], want[section], got)
		}
	}
}

func toolViewNames(tools []*database.ToolCache) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
