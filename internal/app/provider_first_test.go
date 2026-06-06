package app_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

// recordingProvider is a fake provider that logs Install/Uninstall calls in
// order so tests can assert sequencing.
type recordingProvider struct {
	name      string
	available bool
	failOn    map[string]bool
	mu        *sync.Mutex
	log       *[]string
}

func (p *recordingProvider) Name() string        { return p.name }
func (p *recordingProvider) Description() string { return p.name + " stub" }
func (p *recordingProvider) Available(_ context.Context) (bool, error) {
	return p.available, nil
}
func (p *recordingProvider) Install(_ context.Context, t provider.Tool) error {
	p.mu.Lock()
	*p.log = append(*p.log, p.name+":"+t.Name)
	p.mu.Unlock()
	if p.failOn[t.Name] {
		return fmt.Errorf("install %s failed", t.Name)
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
// Scenario: uv is installed via brew in pass 1. ruff is then installed via the
// configured concrete provider in pass 2. We assert brew:uv appears before
// pip:ruff in the install log.
func TestSync_InstallsProvidersBeforeDependents(t *testing.T) {
	var mu sync.Mutex
	var log []string
	// "brew" installs the bootstrap provider "uv" (pass 1).
	// "pip" installs the dependent tool "ruff" (pass 2).
	brew := &recordingProvider{name: "brew", available: true, mu: &mu, log: &log}
	pip := &recordingProvider{name: "pip", available: true, mu: &mu, log: &log}
	a, cfgPath := newImportApp(t, brew, pip)

	cfg := &config.RootConfig{
		Settings: config.Settings{
			// uv is a Python manager: install it via brew before other tools.
			Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}},
		},
		// ruff uses a concrete provider.
		Tools: map[string]config.ToolSpec{"ruff": {Providers: []config.ToolInstallSpec{{Provider: "pip"}}}},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "ruff"}},
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if _, err := a.Sync(context.Background(), isync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// Pass 1 installs the bootstrap provider (uv) via brew.
	// Pass 2 installs ruff via pip.
	// The log entry for the bootstrap provider uses the ProviderEntry.Name as the tool name.
	iUV := indexOf(log, "brew:uv")
	iRuff := indexOf(log, "pip:ruff")
	if iUV < 0 || iRuff < 0 {
		t.Fatalf("missing installs in log %v (want brew:uv and pip:ruff)", log)
	}
	if iUV > iRuff {
		t.Errorf("bootstrap provider uv (log idx %d) installed after dependent ruff (log idx %d): %v", iUV, iRuff, log)
	}
}

// TestSync_SelfContainedProviderNotPruned verifies that a bootstrap provider
// declared in Settings.Providers is not pruned even when no group tool uses it.
func TestSync_SelfContainedProviderNotPruned(t *testing.T) {
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
