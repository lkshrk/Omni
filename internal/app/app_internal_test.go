package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
)

type internalProviderStub struct {
	name     string
	concrete string
	err      error
	metadata map[string]provider.InstalledMetadata
}

func (s *internalProviderStub) Name() string { return s.name }

func (s *internalProviderStub) Description() string { return s.name }

func (s *internalProviderStub) Available(context.Context) (bool, error) { return true, nil }

func (s *internalProviderStub) Install(context.Context, provider.Tool) error { return nil }

func (s *internalProviderStub) Uninstall(context.Context, provider.Tool) error { return nil }

func (s *internalProviderStub) Upgrade(context.Context, provider.Tool) error { return nil }

func (s *internalProviderStub) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return false, "", nil
}

func (s *internalProviderStub) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func (s *internalProviderStub) ResolvedName(context.Context) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.concrete, nil
}

func (s *internalProviderStub) InstalledMetadataMap(context.Context) (map[string]provider.InstalledMetadata, error) {
	return s.metadata, nil
}

func TestTapFromPackage(t *testing.T) {
	tests := []struct {
		pkg  string
		want string
	}{
		{"hashicorp/tap/terraform", "hashicorp/tap"},
		{"homebrew-cask/font-fira-code/something", "homebrew-cask/font-fira-code"},
		{"git", ""},
		{"owner/repo", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := tapFromPackage(tt.pkg)
		if got != tt.want {
			t.Errorf("tapFromPackage(%q) = %q, want %q", tt.pkg, got, tt.want)
		}
	}
}

func TestShortHostname(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"macbook.corp.local", "macbook"},
		{"macbook", "macbook"},
		{"a.b.c.d", "a"},
		{"", "localhost"},
	}
	for _, tt := range tests {
		got := shortHostname(tt.input)
		if got != tt.want {
			t.Errorf("shortHostname(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSettingsManagerHelpSubject(t *testing.T) {
	tests := []struct {
		ecosystem string
		want      string
	}{
		{ecosystem: provider.EcosystemNode, want: "JS package manager"},
		{ecosystem: provider.EcosystemPython, want: "Python tool manager"},
		{ecosystem: "", want: "Manager"},
		{ecosystem: "ruby", want: "Ruby manager"},
	}
	for _, tt := range tests {
		t.Run(tt.ecosystem, func(t *testing.T) {
			if got := settingsManagerHelpSubject(tt.ecosystem); got != tt.want {
				t.Fatalf("settingsManagerHelpSubject(%q) = %q, want %q", tt.ecosystem, got, tt.want)
			}
		})
	}
}

func TestProbeFirstPrefersAvailableHintThenPriority(t *testing.T) {
	binDir := t.TempDir()
	writeProbeExecutable := func(name string) {
		t.Helper()
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write executable %s: %v", name, err)
		}
	}
	writeProbeExecutable("pnpm")
	writeProbeExecutable("npm")
	t.Setenv("PATH", binDir)

	if got := probeFirst("pnpm", []string{"npm"}); got != "pnpm" {
		t.Fatalf("probeFirst available hint = %q, want pnpm", got)
	}
	if got := probeFirst("bun", []string{"pnpm", "npm"}); got != "pnpm" {
		t.Fatalf("probeFirst missing hint fallback = %q, want pnpm", got)
	}
	if got := probeFirst("", []string{"bun"}); got != "" {
		t.Fatalf("probeFirst missing candidates = %q, want empty", got)
	}
}

func TestCloneSettingsStringMapCopiesInput(t *testing.T) {
	if got := cloneSettingsStringMap(nil); got != nil {
		t.Fatalf("cloneSettingsStringMap(nil) = %#v, want nil", got)
	}
	input := map[string]string{"node": "pnpm"}
	got := cloneSettingsStringMap(input)
	got["node"] = "bun"
	got["python"] = "uv"
	if input["node"] != "pnpm" {
		t.Fatalf("input node mutated to %q, want pnpm", input["node"])
	}
	if _, ok := input["python"]; ok {
		t.Fatalf("input gained python key after clone mutation: %#v", input)
	}
}

func TestExpandHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := expandHomePath("~"); got != home {
		t.Fatalf("expandHomePath(~) = %q, want %q", got, home)
	}
	if got := expandHomePath("~/settings.json"); got != filepath.Join(home, "settings.json") {
		t.Fatalf("expandHomePath(~/settings.json) = %q, want under HOME", got)
	}
	if got := expandHomePath("/tmp/settings.json"); got != "/tmp/settings.json" {
		t.Fatalf("expandHomePath absolute = %q, want unchanged", got)
	}
	if got := expandHomePath("~other/settings.json"); got != "~other/settings.json" {
		t.Fatalf("expandHomePath user home = %q, want unchanged", got)
	}
}

func TestNormalizeToolState(t *testing.T) {
	tests := []struct {
		raw  string
		want ToolListState
	}{
		{raw: "", want: ""},
		{raw: " Installed ", want: ToolStateInstalled},
		{raw: "missing", want: ToolStateMissing},
		{raw: "updates", want: ToolStateOutdated},
		{raw: "quarantine", want: ToolStateQuarantined},
		{raw: "metadata", want: ToolStateBlockedMetadata},
		{raw: "ignore", want: ToolStateIgnored},
		{raw: "orphans", want: ToolStateUnclaimed},
		{raw: "sync", want: ToolStateOutOfSync},
		{raw: "failures", want: ToolStateFailed},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := normalizeToolState(tt.raw)
			if err != nil {
				t.Fatalf("normalizeToolState(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeToolState(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
	if _, err := normalizeToolState("not-a-state"); err == nil {
		t.Fatal("normalizeToolState accepted unknown state")
	}
}

func TestProviderMatchInstallCandidateSkipsEcosystemProviders(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.registry = provider.NewRegistry()
	a.registry.RegisterWithMetadata(&internalProviderStub{name: "system"}, provider.Metadata{Kind: provider.ProviderKindEcosystem})
	a.registry.RegisterWithMetadata(&internalProviderStub{name: "brew"}, provider.Metadata{Kind: provider.ProviderKindConcrete})

	if a.providerMatchInstallCandidate("system") {
		t.Fatal("system provider family was accepted as an install candidate")
	}
	if !a.providerMatchInstallCandidate("brew") {
		t.Fatal("brew concrete provider was rejected as an install candidate")
	}
	if !a.providerMatchInstallCandidate("unknown-provider") {
		t.Fatal("unknown provider should be left to provider search result handling")
	}
	if a.providerMatchInstallCandidate("") {
		t.Fatal("empty provider was accepted as an install candidate")
	}
}

func TestRefreshInstalledScanLabelUsesExplicitInstallWithForEcosystem(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.registry = provider.NewRegistry()
	system := &internalProviderStub{name: "system", concrete: "apt"}
	a.registry.RegisterWithMetadata(system, provider.Metadata{Kind: provider.ProviderKindEcosystem})

	got := a.refreshInstalledScanLabel(context.Background(), config.ToolEntry{
		Name:        "ripgrep",
		Provider:    "system",
		InstallWith: "brew",
	}, "system", system, map[string]string{})

	if got != "system/brew" {
		t.Fatalf("refreshInstalledScanLabel = %q, want system/brew", got)
	}
}

func TestRefreshInstalledScanLabelCachesConcreteResolver(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.registry = provider.NewRegistry()
	system := &internalProviderStub{name: "system", concrete: "apt"}
	resolved := make(map[string]string)

	first := a.refreshInstalledScanLabel(context.Background(), config.ToolEntry{Name: "ripgrep", Provider: "system"}, "system", system, resolved)
	system.concrete = "brew"
	second := a.refreshInstalledScanLabel(context.Background(), config.ToolEntry{Name: "fd", Provider: "system"}, "system", system, resolved)

	if first != "system/apt" || second != "system/apt" {
		t.Fatalf("labels = %q/%q, want cached system/apt", first, second)
	}
	if got, ok := (&refreshInstalledScanProgress{resolvedConcrete: resolved}).resolvedProviderName("system"); !ok || got != "apt" {
		t.Fatalf("resolvedProviderName = %q/%v, want apt/true", got, ok)
	}
}

func TestRefreshInstalledScanLabelFallsBackOnResolverError(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.registry = provider.NewRegistry()
	system := &internalProviderStub{name: "system", err: errors.New("resolver failed")}
	resolved := make(map[string]string)

	got := a.refreshInstalledScanLabel(context.Background(), config.ToolEntry{Name: "ripgrep", Provider: "system"}, "system", system, resolved)

	if got != "system" {
		t.Fatalf("refreshInstalledScanLabel = %q, want system fallback", got)
	}
	if cached, ok := resolved["system"]; !ok || cached != "" {
		t.Fatalf("resolved cache = %q/%v, want empty cached fallback", cached, ok)
	}
}

func TestNewRefreshProviderScanPlanSkipsConcreteCoveredByEcosystem(t *testing.T) {
	plan := newRefreshProviderScanPlan(
		[]*database.ToolCache{{Name: "typescript", Provider: "npm", Tracked: true}},
		[]string{"node", "npm"},
		map[string]int{"node": 2, "npm": 1},
		map[string]string{"node": "npm"},
		nil,
	)

	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %#v, want only ecosystem scan", plan.Steps)
	}
	step := plan.Steps[0]
	if step.Provider != "node" || step.Label != "node/npm" || step.Count != 2 {
		t.Fatalf("step = %#v, want node/npm count 2", step)
	}
}

func TestNewRefreshProviderScanPlanCountsTrackedRowsWhenConfigCountsMissing(t *testing.T) {
	plan := newRefreshProviderScanPlan(
		[]*database.ToolCache{
			{Name: "fd", Provider: "brew", Tracked: true},
			{Name: "ripgrep", Provider: "brew", Tracked: true},
			{Name: "ignored", Provider: "brew", Tracked: false},
			{Name: "htop", Provider: "apt", Tracked: true},
			nil,
		},
		[]string{"brew", "zypper"},
		nil,
		nil,
		nil,
	)

	counts := plan.CountsByProvider()
	if counts["brew"] != 2 {
		t.Fatalf("brew count = %d, want tracked row count 2", counts["brew"])
	}
	if counts["zypper"] != 1 {
		t.Fatalf("zypper count = %d, want default count 1", counts["zypper"])
	}
	if counts["apt"] != 1 {
		t.Fatalf("apt count = %d, want discovered tracked row count 1", counts["apt"])
	}
}

func TestDotStatusStateUsesExplicitStateAndHealthFallback(t *testing.T) {
	tests := []struct {
		name   string
		status DotStatus
		want   DotState
	}{
		{name: "explicit state wins", status: DotStatus{State: DotStateModified, Health: HealthOK}, want: DotStateModified},
		{name: "ok health", status: DotStatus{Health: HealthOK}, want: DotStateSynced},
		{name: "missing health", status: DotStatus{Health: HealthMissing}, want: DotStateMissing},
		{name: "conflict health", status: DotStatus{Health: HealthConflict}, want: DotStateConflict},
		{name: "no source health", status: DotStatus{Health: HealthNoSource}, want: DotStateNoSource},
		{name: "unknown health", status: DotStatus{Health: DotHealth("custom")}, want: DotState("custom")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusState(tt.status); got != tt.want {
				t.Fatalf("DotStatusState = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDotStatusSectionsClassifiesStates(t *testing.T) {
	sections := DotStatusSections([]DotStatus{
		{Name: "modified", State: DotStateModified},
		{Name: "conflict", State: DotStateConflict},
		{Name: "synced", State: DotStateSynced},
		{Name: "ignored", State: DotStateIgnored},
		{Name: "ambiguous", State: DotStateAmbiguous},
		{Name: "transient", State: DotStateUntrackedConflict},
		{Name: "grouped-transient", State: DotStateUntrackedConflict, Group: "base"},
	})

	got := make(map[string][]string, len(sections))
	for _, section := range sections {
		for _, status := range section.Statuses {
			got[section.Title] = append(got[section.Title], status.Name)
		}
	}
	want := map[string][]string{
		"Conflict":    {"conflict", "ambiguous", "grouped-transient"},
		"Out Of Sync": {"modified", "transient"},
		"Synced":      {"synced"},
		"Ignored":     {"ignored"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DotStatusSections = %#v, want %#v", got, want)
	}
}

func TestSortDotStatusesOrdersBySectionAndName(t *testing.T) {
	statuses := []DotStatus{
		{Name: "z-synced", State: DotStateSynced},
		{Name: "b-modified", State: DotStateModified},
		{Name: "ignored", State: DotStateIgnored},
		{Name: "transient", State: DotStateUntrackedConflict},
		{Name: "a-modified", State: DotStateModified},
		{Name: "grouped-transient", State: DotStateUntrackedConflict, Group: "base"},
	}

	SortDotStatuses(statuses)

	var got []string
	for _, status := range statuses {
		got = append(got, status.Name)
	}
	want := []string{"grouped-transient", "a-modified", "b-modified", "transient", "z-synced", "ignored"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SortDotStatuses order = %v, want %v", got, want)
	}
}

func TestDotSyncAllPendingNamesSkipsNonSyncableStatuses(t *testing.T) {
	got := DotSyncAllPendingNames([]DotStatus{
		{Name: "missing", State: DotStateMissing},
		{Name: "synced", State: DotStateSynced},
		{Name: "transient", State: DotStateUntrackedConflict},
		{Name: "ignored", State: DotStateIgnored},
		{Name: "inactive", State: DotStateInactive},
		{Name: "disabled", State: DotStateDisabled},
		{Name: "", State: DotStateMissing},
	})

	want := map[string]bool{"missing": true, "synced": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DotSyncAllPendingNames = %v, want %v", got, want)
	}
}

func TestDotSyncAllEntryOrderUsesStatusSections(t *testing.T) {
	got := DotSyncAllEntryOrder([]DotStatus{
		{Name: "z-synced", State: DotStateSynced},
		{Name: "ignored", State: DotStateIgnored},
		{Name: "missing", State: DotStateMissing},
		{Name: "conflict", State: DotStateConflict},
		{Name: "transient", State: DotStateUntrackedConflict},
		{Name: "", State: DotStateModified},
	})

	want := []string{"conflict", "missing", "transient", "z-synced", "ignored"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DotSyncAllEntryOrder = %v, want %v", got, want)
	}
}

func TestDotStatusVariantEligibleSkipsUnsupportedStates(t *testing.T) {
	tests := []struct {
		name   string
		status DotStatus
		want   bool
	}{
		{name: "synced", status: DotStatus{Name: "nvim", State: DotStateSynced}, want: true},
		{name: "missing", status: DotStatus{Name: "nvim", State: DotStateMissing}, want: true},
		{name: "empty name", status: DotStatus{State: DotStateSynced}, want: false},
		{name: "transient candidate", status: DotStatus{Name: "nvim", State: DotStateUntrackedConflict}, want: false},
		{name: "ignored", status: DotStatus{Name: "nvim", State: DotStateIgnored}, want: false},
		{name: "inactive", status: DotStatus{Name: "nvim", State: DotStateInactive}, want: false},
		{name: "disabled", status: DotStatus{Name: "nvim", State: DotStateDisabled}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusVariantEligible(tt.status); got != tt.want {
				t.Fatalf("DotStatusVariantEligible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDotStatusIgnoredUsesCanonicalState(t *testing.T) {
	tests := []struct {
		name   string
		status DotStatus
		want   bool
	}{
		{name: "explicit ignored", status: DotStatus{State: DotStateIgnored}, want: true},
		{name: "legacy ignored health", status: DotStatus{Health: DotHealth(DotStateIgnored)}, want: true},
		{name: "synced", status: DotStatus{State: DotStateSynced}, want: false},
		{name: "empty", status: DotStatus{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusIgnored(tt.status); got != tt.want {
				t.Fatalf("DotStatusIgnored = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDotStatusNeedsAttentionSkipsSyncedAndIgnored(t *testing.T) {
	tests := []struct {
		name   string
		status DotStatus
		want   bool
	}{
		{name: "missing", status: DotStatus{State: DotStateMissing}, want: true},
		{name: "conflict", status: DotStatus{State: DotStateConflict}, want: true},
		{name: "synced", status: DotStatus{State: DotStateSynced}, want: false},
		{name: "ignored", status: DotStatus{State: DotStateIgnored}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusNeedsAttention(tt.status); got != tt.want {
				t.Fatalf("DotStatusNeedsAttention = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDotStatusSyncActionLabelUsesState(t *testing.T) {
	tests := []struct {
		name   string
		status DotStatus
		want   string
	}{
		{name: "missing", status: DotStatus{State: DotStateMissing}, want: "use repo"},
		{name: "broken", status: DotStatus{State: DotStateBroken}, want: "repair"},
		{name: "local only", status: DotStatus{State: DotStateLocalOnly}, want: "use local"},
		{name: "repo only", status: DotStatus{State: DotStateRepoOnly}, want: "use repo"},
		{name: "default", status: DotStatus{State: DotStateModified}, want: "sync"},
		{name: "legacy health", status: DotStatus{Health: HealthMissing}, want: "use repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusSyncActionLabel(tt.status); got != tt.want {
				t.Fatalf("DotStatusSyncActionLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDotStatusFileCountsUsesExplicitCountsAndStateFallback(t *testing.T) {
	tests := []struct {
		name   string
		status DotStatus
		want   DotFileCounts
	}{
		{
			name:   "explicit counts win",
			status: DotStatus{State: DotStateSynced, FileCount: 4, Counts: DotFileCounts{Synced: 2, OutOfSync: 1, Ignored: 1}},
			want:   DotFileCounts{Synced: 2, OutOfSync: 1, Ignored: 1},
		},
		{name: "synced file count", status: DotStatus{State: DotStateSynced, FileCount: 3}, want: DotFileCounts{Synced: 3}},
		{name: "missing file count", status: DotStatus{State: DotStateMissing, FileCount: 3}, want: DotFileCounts{OutOfSync: 3}},
		{name: "zero file count", status: DotStatus{State: DotStateMissing}, want: DotFileCounts{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusFileCounts(tt.status); got != tt.want {
				t.Fatalf("DotStatusFileCounts = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDotStatusesFileCountsSkipsIgnoredEntries(t *testing.T) {
	got := DotStatusesFileCounts([]DotStatus{
		{Name: "synced", State: DotStateSynced, FileCount: 2},
		{Name: "missing", State: DotStateMissing, FileCount: 3},
		{Name: "ignored", State: DotStateIgnored, Counts: DotFileCounts{Ignored: 5}},
	})
	want := DotFileCounts{Synced: 2, OutOfSync: 3}
	if got != want {
		t.Fatalf("DotStatusesFileCounts = %+v, want %+v", got, want)
	}
}

func TestDotChildFileCountsUsesChildStateFallback(t *testing.T) {
	tests := []struct {
		name        string
		child       DotChild
		parentState DotState
		want        DotFileCounts
	}{
		{name: "explicit counts win", child: DotChild{State: DotStateMissing, FileCount: 4, Counts: DotFileCounts{Ignored: 2}}, parentState: DotStateSynced, want: DotFileCounts{Ignored: 2}},
		{name: "child state synced", child: DotChild{State: DotStateSynced, FileCount: 4}, parentState: DotStateMissing, want: DotFileCounts{Synced: 4}},
		{name: "parent state fallback", child: DotChild{FileCount: 4}, parentState: DotStateSynced, want: DotFileCounts{Synced: 4}},
		{name: "out of sync fallback", child: DotChild{FileCount: 4}, parentState: DotStateMissing, want: DotFileCounts{OutOfSync: 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotChildFileCounts(tt.child, tt.parentState); got != tt.want {
				t.Fatalf("DotChildFileCounts = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDotStateIcon(t *testing.T) {
	tests := []struct {
		state DotState
		want  string
	}{
		{state: DotStateSynced, want: "✓"},
		{state: DotStateConflict, want: "✗"},
		{state: DotStateNoSource, want: "?"},
		{state: DotStateIgnored, want: "·"},
		{state: DotStateModified, want: "!"},
	}
	for _, tt := range tests {
		if got := DotStateIcon(tt.state); got != tt.want {
			t.Fatalf("DotStateIcon(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestDotStatusHasActionUsesExplicitActionsAndFallbackState(t *testing.T) {
	explicit := DotStatus{State: DotStateSynced, Actions: []DotAction{DotActionUseRepo}}
	if !DotStatusHasAction(explicit, DotActionUseRepo) {
		t.Fatal("explicit action should be available")
	}
	if DotStatusHasAction(explicit, DotActionRemove) {
		t.Fatal("explicit action list should suppress state fallback")
	}

	tests := []struct {
		name   string
		status DotStatus
		action DotAction
		want   bool
	}{
		{name: "modified sync", status: DotStatus{State: DotStateModified}, action: DotActionSync, want: true},
		{name: "modified use repo", status: DotStatus{State: DotStateModified}, action: DotActionUseRepo, want: false},
		{name: "untracked conflict use repo", status: DotStatus{State: DotStateUntrackedConflict}, action: DotActionUseRepo, want: true},
		{name: "untracked conflict remove", status: DotStatus{State: DotStateUntrackedConflict}, action: DotActionRemove, want: false},
		{name: "synced remove", status: DotStatus{State: DotStateSynced}, action: DotActionRemove, want: true},
		{name: "conflict use local", status: DotStatus{State: DotStateConflict}, action: DotActionUseLocal, want: true},
		{name: "no source ignore", status: DotStatus{State: DotStateNoSource}, action: DotActionIgnore, want: true},
		{name: "ignored unignore", status: DotStatus{State: DotStateIgnored}, action: DotActionUnignore, want: true},
		{name: "ignored sync", status: DotStatus{State: DotStateIgnored}, action: DotActionSync, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotStatusHasAction(tt.status, tt.action); got != tt.want {
				t.Fatalf("DotStatusHasAction = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDotsHistorySummarySeparatesChangedAndUnchangedOps(t *testing.T) {
	tests := []struct {
		name string
		ops  []dots.Op
		want string
	}{
		{
			name: "only skipped",
			ops:  []dots.Op{{Kind: dots.OpSkip}, {Kind: dots.OpSkip}},
			want: "sync completed: no changes, 2 dotfiles unchanged",
		},
		{
			name: "mixed",
			ops:  []dots.Op{{Kind: dots.OpLink}, {Kind: dots.OpSkip}, {Kind: dots.OpSkip}},
			want: "sync completed: 1 dotfile changed, 2 dotfiles unchanged",
		},
		{
			name: "only changed",
			ops:  []dots.Op{{Kind: dots.OpLink}, {Kind: dots.OpRepair}},
			want: "sync completed: 2 dotfiles changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dotsHistorySummary("sync", tt.ops, nil); got != tt.want {
				t.Fatalf("dotsHistorySummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeDotsHistoryEntryUpdatesLegacySuccessSummary(t *testing.T) {
	entry := DotsHistoryEntry{
		Operation: "sync",
		Status:    "success",
		Summary:   "sync completed with 2 dotfile ops",
		Ops:       []DotsHistoryOp{{Kind: "skip"}, {Kind: "skip"}},
	}
	got := normalizeDotsHistoryEntry(entry)
	if got.Summary != "sync completed: no changes, 2 dotfiles unchanged" {
		t.Fatalf("normalized summary = %q, want no-change summary", got.Summary)
	}
}

func TestNormalizeDotsHistoryEntryPreservesFailureSummary(t *testing.T) {
	entry := DotsHistoryEntry{
		Operation: "sync",
		Status:    "partial",
		Summary:   "sync failed",
		Ops:       []DotsHistoryOp{{Kind: "conflict"}, {Kind: "skip"}},
	}
	got := normalizeDotsHistoryEntry(entry)
	if got.Summary != "sync failed" {
		t.Fatalf("normalized failure summary = %q, want original failure summary", got.Summary)
	}
}

func TestCountDotsWatchSyncOps(t *testing.T) {
	got := CountDotsWatchSyncOps([]dots.Op{
		{Kind: dots.OpLink},
		{Kind: dots.OpUnlinkSkip},
		{Kind: dots.OpUnlinkConflict},
		{Kind: dots.OpConflict},
		{Kind: dots.OpSkip},
	})
	if got.Changes != 3 || got.Conflicts != 1 {
		t.Fatalf("CountDotsWatchSyncOps = %+v, want 3 changes and 1 conflict", got)
	}
}

func TestDotsDisableDetail(t *testing.T) {
	tests := []struct {
		name string
		ops  []dots.Op
		want string
	}{
		{name: "no ops", want: "dots disabled"},
		{
			name: "unlink and conflict",
			ops: []dots.Op{
				{Kind: dots.OpUnlink},
				{Kind: dots.OpUnlink},
				{Kind: dots.OpUnlinkConflict},
				{Kind: dots.OpSkip},
			},
			want: "2 unlinked, 1 conflicts",
		},
		{
			name: "no matching unlink ops",
			ops:  []dots.Op{{Kind: dots.OpSkip}},
			want: "0 unlinked",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DotsDisableDetail(tt.ops); got != tt.want {
				t.Fatalf("DotsDisableDetail = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMachineGroupName(t *testing.T) {
	if got := machineGroupName("macbook.corp.local"); got != "macbook" {
		t.Errorf("machineGroupName = %q, want macbook", got)
	}
}

func TestBackupConfigOnLaunch_CopiesExistingFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	body := []byte(`{"$schema":"x","settings":{}}` + "\n")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: src}
	a.backupConfigOnLaunch()
	got, err := os.ReadFile(src + settingsBackupSuffix)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("backup mismatch:\n got %q\nwant %q", got, body)
	}
}

func TestInitProviderRegistry_RegistersScript(t *testing.T) {
	a := &App{}
	a.initProviderRegistry(config.Settings{})
	p, ok := a.registry.Get("script")
	if !ok {
		t.Fatal("script provider not registered")
	}
	if p.Name() != "script" {
		t.Errorf("registered provider Name() = %q, want script", p.Name())
	}
}

func TestBackupConfigOnLaunch_MissingSourceIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	a := &App{ConfigPath: src}
	a.backupConfigOnLaunch()
	if _, err := os.Stat(src + settingsBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup file when source missing, got err=%v", err)
	}
}

func TestBackupConfigOnLaunch_OverwritesPriorBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	bak := src + settingsBackupSuffix
	if err := os.WriteFile(bak, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := []byte(`{"settings":{}}`)
	if err := os.WriteFile(src, fresh, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: src}
	a.backupConfigOnLaunch()
	got, err := os.ReadFile(bak)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(fresh) {
		t.Fatalf("backup not overwritten: got %q, want %q", got, fresh)
	}
}

func TestResolveRepoPath_ExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo, err := resolveRepoPath("$HOME/dotfiles")
	if err != nil {
		t.Fatalf("resolveRepoPath: %v", err)
	}

	if repo != filepath.Join(home, "dotfiles") {
		t.Fatalf("resolveRepoPath = %q, want %q", repo, filepath.Join(home, "dotfiles"))
	}
}

func TestNormalisePath_ExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := normalisePath("$HOME/.config/zsh")
	want := filepath.ToSlash(filepath.Join("~", ".config", "zsh"))
	if got != want {
		t.Fatalf("normalisePath($HOME/.config/zsh) = %q, want %q", got, want)
	}
}

func TestExpandAndStat_ExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoDir := filepath.Join(home, "dotfiles")
	if err := os.Mkdir(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := expandAndStat("$HOME/dotfiles")
	if err != nil {
		t.Fatalf("expandAndStat: %v", err)
	}
	if got != filepath.Clean(repoDir) {
		t.Fatalf("expandAndStat = %q, want %q", got, filepath.Clean(repoDir))
	}
}

func TestEffectiveHostGroupsIncludesHostGroupFirst(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example.com")
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
			"slack":   {Provider: "system", InstallWith: "brew"},
			"fd":      {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "shared", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			{Name: "work", Tools: []config.ToolEntry{{Name: "slack"}}},
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fd"}}},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}

	effective, active, ok := effectiveHostGroups(cfg, cfg.Groups, "testhost.example.com")
	if !ok {
		t.Fatal("effectiveHostGroups ok=false")
	}
	if got := groupBaseNames(effective); len(got) != 2 || got[0] != "testhost" || got[1] != "work" {
		t.Fatalf("effective groups = %v, want [testhost work]", got)
	}
	if got := groupBaseNames(active); len(got) != 2 || got[0] != "testhost" || got[1] != "work" {
		t.Fatalf("active groups = %v, want [testhost work]", got)
	}
}

func TestDotsHistoryIDUniqueness(t *testing.T) {
	// Generate IDs rapidly — the atomic counter suffix must prevent collisions
	// even when time.Now().UnixNano() returns the same value.
	const n = 100
	ids := make(map[string]struct{}, n)
	for range n {
		id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), dotsHistoryIDCounter.Add(1))
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate dots history ID: %s", id)
		}
		ids[id] = struct{}{}
	}
}
