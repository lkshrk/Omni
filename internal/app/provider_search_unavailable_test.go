package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

type searchableProvider struct {
	name          string
	available     bool
	searchResults []provider.SearchResult
	installCalls  []provider.Tool
	installed     map[string]bool
}

func (p *searchableProvider) Name() string        { return p.name }
func (p *searchableProvider) Description() string { return p.name + " stub" }
func (p *searchableProvider) Available(_ context.Context) (bool, error) {
	return p.available, nil
}
func (p *searchableProvider) Install(_ context.Context, t provider.Tool) error {
	p.installCalls = append(p.installCalls, t)
	if p.installed == nil {
		p.installed = make(map[string]bool)
	}
	p.installed[t.Name] = true
	return nil
}
func (p *searchableProvider) Uninstall(_ context.Context, t provider.Tool) error {
	if p.installed != nil {
		delete(p.installed, t.Name)
	}
	return nil
}
func (p *searchableProvider) Upgrade(_ context.Context, _ provider.Tool) error { return nil }
func (p *searchableProvider) IsInstalled(_ context.Context, t provider.Tool) (bool, string, error) {
	if p.installed != nil && p.installed[t.Name] {
		return true, "1.0.0", nil
	}
	return false, "", nil
}
func (p *searchableProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}
func (p *searchableProvider) Search(_ context.Context, query string) ([]provider.SearchResult, error) {
	var out []provider.SearchResult
	for _, r := range p.searchResults {
		if r.Name == query {
			out = append(out, r)
		}
	}
	return out, nil
}

type unavailableProvider struct {
	name string
}

func (p *unavailableProvider) Name() string        { return p.name }
func (p *unavailableProvider) Description() string { return p.name + " stub" }
func (p *unavailableProvider) Available(_ context.Context) (bool, error) {
	return false, nil
}
func (p *unavailableProvider) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (p *unavailableProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (p *unavailableProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (p *unavailableProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *unavailableProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func TestSync_UnavailableProvider_SearchesAvailableProviders(t *testing.T) {
	t.Parallel()
	brew := &unavailableProvider{name: "brew"}
	npm := &searchableProvider{
		name:      "npm",
		available: true,
		searchResults: []provider.SearchResult{
			{
				Name:     "codex",
				Provider: "npm",
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "openai",
					Repo:  "codex",
					URL:   "https://github.com/openai/codex",
				},
			},
		},
	}
	a, cfgPath := newImportApp(t, brew, npm)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"codex": {
				Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "codex"}},
				Git:       "https://github.com/openai/codex",
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("codex")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var installed bool
	for _, op := range result.Ops {
		if op.Tool.Name == "codex" && op.Kind == isync.OpInstall {
			installed = true
		}
	}
	if !installed {
		t.Errorf("codex was not installed; ops = %+v", result.Ops)
	}

	var npmInstalledCodex bool
	for _, call := range npm.installCalls {
		if call.Name == "codex" {
			npmInstalledCodex = true
		}
	}
	if !npmInstalledCodex {
		t.Errorf("npm.Install was not called with codex; install calls: %v", npm.installCalls)
	}
}

func TestSync_AvailableProvider_NotBypassed(t *testing.T) {
	t.Parallel()
	brew := &searchableProvider{
		name:      "brew",
		available: true,
		searchResults: []provider.SearchResult{
			{Name: "ripgrep", Provider: "brew"},
		},
	}
	apt := &searchableProvider{
		name:      "apt",
		available: true,
		searchResults: []provider.SearchResult{
			{Name: "ripgrep", Provider: "apt"},
		},
	}

	a, cfgPath := newImportApp(t, brew, apt)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {
				Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "ripgrep"}},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, op := range result.Ops {
		if op.Tool.Name == "ripgrep" && op.Kind == isync.OpInstall {
			if op.Tool.Provider != "brew" {
				t.Errorf("ripgrep was installed via %q, want brew", op.Tool.Provider)
			}
		}
	}
	for _, call := range apt.installCalls {
		if call.Name == "ripgrep" {
			t.Errorf("apt.Install was called for ripgrep despite brew being available (A2 violation)")
		}
	}
}

func TestSync_UnavailableProvider_FallbackLastResort(t *testing.T) {
	t.Parallel()
	brew := &unavailableProvider{name: "brew"}
	apt := &searchableProvider{
		name:          "apt",
		available:     true,
		searchResults: nil,
	}

	var fallbackInstalled bool
	exec := &recordingFallbackExecutor{onInstall: func() { fallbackInstalled = true }}

	a, cfgPath := newImportApp(t, brew, apt)
	a.SetFallbackExecutor(exec)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{
						Type:  config.FallbackSourceGitHub,
						Owner: "example",
						Repo:  "mytool",
					},
					Status: config.FallbackStatusUnverified,
					Commands: config.FallbackCommands{
						Install: "echo install",
						Check:   "command -v mytool",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if !fallbackInstalled {
		t.Errorf("fallback executor was never called for mytool; ops = %+v", result.Ops)
	}

	for _, op := range result.Ops {
		if op.Tool.Name == "mytool" && op.Kind == isync.OpFailed {
			if op.Err != nil && isNoNativeRouteError(op.Err) {
				t.Errorf("mytool op carries native-unavailable error (fallback was not attempted): %v", op.Err)
			}
		}
	}
}

func TestSync_UnavailableProvider_WeakMatchNotInstalled(t *testing.T) {
	t.Parallel()
	brew := &unavailableProvider{name: "brew"}
	npm := &searchableProvider{
		name:      "npm",
		available: true,
		searchResults: []provider.SearchResult{
			{Name: "mytool", Provider: "npm"},
		},
	}

	a, cfgPath := newImportApp(t, brew, npm)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	_, err := a.Sync(context.Background(), isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, call := range npm.installCalls {
		if call.Name == "mytool" {
			t.Errorf("npm.Install called for mytool despite only having a weak match (A4 violation)")
		}
	}
}

type recordingFallbackExecutor struct {
	onInstall func()
}

func (e *recordingFallbackExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	if e.onInstall != nil {
		e.onInstall()
	}
	return "", "", nil
}

func isNoNativeRouteError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, keyword := range []string{"no native install route", "provider unavailable", "native install candidates unavailable"} {
		if len(msg) >= len(keyword) {
			for i := 0; i <= len(msg)-len(keyword); i++ {
				if msg[i:i+len(keyword)] == keyword {
					return true
				}
			}
		}
	}
	return false
}
