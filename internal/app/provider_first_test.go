package app_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

// recordingProvider is a fake provider that logs Install/Uninstall calls in
// order so tests can assert sequencing.
type recordingProvider struct {
	name          string
	available     bool
	failOn        map[string]bool
	mu            *sync.Mutex
	log           *[]string
	availableWhen func() bool
	afterInstall  func(provider.Tool)
	logAvailable  bool
}

func (p *recordingProvider) Name() string        { return p.name }
func (p *recordingProvider) Description() string { return p.name + " stub" }
func (p *recordingProvider) Available(_ context.Context) (bool, error) {
	if p.logAvailable {
		p.mu.Lock()
		*p.log = append(*p.log, p.name+":available")
		p.mu.Unlock()
	}
	if p.availableWhen != nil {
		return p.availableWhen(), nil
	}
	return p.available, nil
}
func (p *recordingProvider) Install(_ context.Context, t provider.Tool) error {
	p.mu.Lock()
	*p.log = append(*p.log, p.name+":"+t.Name)
	p.mu.Unlock()
	if p.failOn[t.Name] {
		return fmt.Errorf("install %s failed", t.Name)
	}
	if p.afterInstall != nil {
		p.afterInstall(t)
	}
	return nil
}
func (p *recordingProvider) Uninstall(_ context.Context, t provider.Tool) error {
	p.mu.Lock()
	*p.log = append(*p.log, "uninstall:"+p.name+":"+t.Name)
	p.mu.Unlock()
	return nil
}
func (p *recordingProvider) Upgrade(_ context.Context, _ provider.Tool) error { return nil }
func (p *recordingProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *recordingProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func indexOf(log []string, want string) int {
	for i, s := range log {
		if s == want {
			return i
		}
	}
	return -1
}

// TestSync_InstallsProvidersBeforeDependents verifies that a bootstrap provider
// declared in Settings.Providers is installed before any dependent tool that
// relies on it.
//
// Scenario: pip is installed via brew in pass 1. ruff is then installed via the
// configured concrete provider in pass 2. Both bootstrap and group/batch sync
// must install pip before even checking the dependent provider.
func TestSync_InstallsProvidersBeforeDependents(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		groupName string
		opts      isync.SyncOptions
	}{
		{name: "bootstrap"},
		{name: "batch group", groupName: "dev", opts: isync.SyncOptions{Group: "dev"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var log []string
			var pipAvailable atomic.Bool
			brew := &recordingProvider{
				name: "brew", available: true, mu: &mu, log: &log,
				afterInstall: func(tool provider.Tool) {
					if tool.Name == "pip" {
						pipAvailable.Store(true)
					}
				},
			}
			pip := &recordingProvider{name: "pip", availableWhen: pipAvailable.Load, mu: &mu, log: &log, logAvailable: true}
			a, cfgPath := newImportApp(t, brew, pip)
			cfg := &config.RootConfig{
				Settings: config.Settings{Providers: []config.ProviderEntry{{Name: "pip", Provider: "brew"}}},
				Tools:    map[string]config.ToolSpec{"ruff": {Providers: []config.ToolInstallSpec{{Provider: "pip"}}}},
				Groups:   []*config.GroupConfig{{Name: tc.groupName, Tools: []config.ToolEntry{{Name: "ruff"}}}},
			}
			if err := saveAppConfig(t, cfgPath, cfg); err != nil {
				t.Fatalf("saving config: %v", err)
			}
			if _, err := a.Sync(context.Background(), tc.opts); err != nil {
				t.Fatalf("Sync: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			iPip := indexOf(log, "brew:pip")
			iPipAvailable := indexOf(log, "pip:available")
			iRuff := indexOf(log, "pip:ruff")
			if iPip < 0 || iPipAvailable < 0 || iRuff < 0 {
				t.Fatalf("missing events in log %v (want brew:pip, pip:available, and pip:ruff)", log)
			}
			if iPip > iPipAvailable || iPipAvailable > iRuff {
				t.Errorf("want provider install before dependent provider check and tool install, got %v", log)
			}
		})
	}
}

// TestSync_SelfContainedProviderNotPruned verifies that a bootstrap provider
// declared in Settings.Providers is not pruned even when no group tool uses it.
func TestSync_SelfContainedProviderNotPruned(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var log []string
	brew := &recordingProvider{name: "brew", available: true, mu: &mu, log: &log}
	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Settings: config.Settings{
			// uv is declared as a bootstrap provider but no group tool depends on it.
			Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{}}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Sync(context.Background(), isync.SyncOptions{Prune: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, s := range log {
		if s == "uninstall:brew:uv" {
			t.Errorf("self-contained bootstrap provider uv was pruned: %v", log)
		}
	}
}

// TestSync_ProviderFailureContinues verifies that a failed bootstrap-provider
// install does not abort the sync: the provider op gets OpFailed, and tools
// that depended on the unavailable provider get OpProviderUnavailable.
func TestSync_ProviderFailureContinues(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var log []string
	brew := &recordingProvider{
		name:      "brew",
		available: true,
		failOn:    map[string]bool{"uv": true},
		mu:        &mu,
		log:       &log,
	}
	// python is unavailable so ruff cannot be installed.
	python := &recordingProvider{name: "python", available: false, mu: &mu, log: &log}
	a, cfgPath := newImportApp(t, brew, python)

	cfg := &config.RootConfig{
		Settings: config.Settings{
			Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}},
		},
		Tools: map[string]config.ToolSpec{"ruff": {Provider: "python"}},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "ruff"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync should continue past provider failure, got error: %v", err)
	}

	var providerFailed, dependentUnavailable bool
	for _, op := range result.Ops {
		if op.Tool.Name == "uv" && op.Kind == isync.OpFailed {
			providerFailed = true
		}
		if op.Tool.Name == "ruff" && op.Kind == isync.OpProviderUnavailable {
			dependentUnavailable = true
		}
	}
	if !providerFailed {
		t.Errorf("expected uv OpFailed in ops, got: %+v", result.Ops)
	}
	if !dependentUnavailable {
		t.Errorf("expected ruff OpProviderUnavailable in ops, got: %+v", result.Ops)
	}
}
