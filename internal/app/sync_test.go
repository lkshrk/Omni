package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/sync"
)

// tapBrewStub is a full brew stub that also implements the brewTapManager interface.
type tapBrewStub struct {
	existingTaps  []string
	existingTrust []string
	tapsCalled    []string
	trustedCalled []string
}

func (s *tapBrewStub) Name() string                                       { return "brew" }
func (s *tapBrewStub) Description() string                                { return "brew stub" }
func (s *tapBrewStub) Available(_ context.Context) (bool, error)          { return true, nil }
func (s *tapBrewStub) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (s *tapBrewStub) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (s *tapBrewStub) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (s *tapBrewStub) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (s *tapBrewStub) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}
func (s *tapBrewStub) ListTaps(_ context.Context) ([]string, error) { return s.existingTaps, nil }
func (s *tapBrewStub) Tap(_ context.Context, name string) error {
	s.tapsCalled = append(s.tapsCalled, name)
	return nil
}
func (s *tapBrewStub) ListTrusted(_ context.Context) ([]string, error) { return s.existingTrust, nil }
func (s *tapBrewStub) Trust(_ context.Context, name string) error {
	s.trustedCalled = append(s.trustedCalled, name)
	return nil
}

func TestSync_AddsMissingTaps(t *testing.T) {
	stub := &tapBrewStub{existingTaps: []string{}}
	a, cfgPath := newImportApp(t, stub)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"terraform": {Provider: "brew", Package: "hashicorp/tap/terraform", InstallWith: "brew", Taps: []string{"hashicorp/tap"}},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "terraform"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Sync(context.Background(), sync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(stub.tapsCalled) != 1 || stub.tapsCalled[0] != "hashicorp/tap" {
		t.Errorf("Tap called with %v, want [hashicorp/tap]", stub.tapsCalled)
	}
	if len(stub.trustedCalled) != 1 || stub.trustedCalled[0] != "hashicorp/tap" {
		t.Errorf("Trust called with %v, want [hashicorp/tap]", stub.trustedCalled)
	}
}

func TestSync_TrustsAlreadyTappedRepo(t *testing.T) {
	// A tap that is already present must still be trusted (it may predate
	// tap-trust enforcement).
	stub := &tapBrewStub{existingTaps: []string{"hashicorp/tap"}}
	a, cfgPath := newImportApp(t, stub)
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"terraform": {Provider: "brew", Package: "hashicorp/tap/terraform", InstallWith: "brew", Taps: []string{"hashicorp/tap"}},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "terraform"}}}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if _, err := a.Sync(context.Background(), sync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(stub.tapsCalled) != 0 {
		t.Errorf("Tap called %v, want none (already tapped)", stub.tapsCalled)
	}
	if len(stub.trustedCalled) != 1 || stub.trustedCalled[0] != "hashicorp/tap" {
		t.Errorf("Trust called with %v, want [hashicorp/tap]", stub.trustedCalled)
	}
}

func TestSync_SkipsAlreadyTrustedTap(t *testing.T) {
	// A tap already trusted must not be re-trusted (avoids a brew call per sync).
	stub := &tapBrewStub{existingTaps: []string{"hashicorp/tap"}, existingTrust: []string{"hashicorp/tap"}}
	a, cfgPath := newImportApp(t, stub)
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"terraform": {Provider: "brew", Package: "hashicorp/tap/terraform", InstallWith: "brew", Taps: []string{"hashicorp/tap"}},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "terraform"}}}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if _, err := a.Sync(context.Background(), sync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(stub.trustedCalled) != 0 {
		t.Errorf("Trust called %v, want none (already trusted)", stub.trustedCalled)
	}
}

func TestSync_SkipsAlreadyTapped(t *testing.T) {
	stub := &tapBrewStub{existingTaps: []string{"hashicorp/tap"}}
	a, cfgPath := newImportApp(t, stub)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"terraform": {Provider: "brew", Package: "hashicorp/tap/terraform", InstallWith: "brew", Taps: []string{"hashicorp/tap"}},
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "terraform"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Sync(context.Background(), sync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(stub.tapsCalled) != 0 {
		t.Errorf("Tap should not have been called, got %v", stub.tapsCalled)
	}
}

func TestSync_DryRunDoesNotTap(t *testing.T) {
	stub := &tapBrewStub{existingTaps: []string{}}
	a, cfgPath := newImportApp(t, stub)

	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Taps: []string{"hashicorp/tap"},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Sync(context.Background(), sync.SyncOptions{DryRun: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(stub.tapsCalled) != 0 {
		t.Errorf("dry-run should not call Tap, got %v", stub.tapsCalled)
	}
}

func TestSync_NoTapsConfigured_NoTapCalls(t *testing.T) {
	stub := &tapBrewStub{existingTaps: []string{}}
	a, cfgPath := newImportApp(t, stub)

	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("git", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("git"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Sync(context.Background(), sync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(stub.tapsCalled) != 0 {
		t.Errorf("no taps in config, Tap should not be called, got %v", stub.tapsCalled)
	}
}

func TestSyncWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.SyncWithState(context.Background(), sync.SyncOptions{})
	if err != nil {
		t.Fatalf("SyncWithState: %v", err)
	}
	if result.Result == nil || len(result.Result.Installed()) != 1 {
		t.Fatalf("Sync result = %+v, want one installed tool", result.Result)
	}
	toolKey := "ripgrep\x00brew"
	if _, ok := result.State.ToolMemberships[toolKey]; !ok {
		t.Fatalf("ToolMemberships[%q] missing after sync: %v", toolKey, result.State.ToolMemberships)
	}
	found := false
	for _, tool := range result.Tools {
		if tool.Name == "ripgrep" && tool.Provider == "brew" && tool.Installed {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Tools = %+v, want installed ripgrep/system", result.Tools)
	}
}
