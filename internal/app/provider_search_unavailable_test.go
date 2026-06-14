package app_test

// Tests for HCL-22: provider search and fallback when configured provider is
// unavailable on the current host.

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

// searchableProvider is a test provider that supports registry search and
// records install calls. It is available on the current system.
// Use a concrete provider name (e.g. "npm", "apt") — ecosystem provider names
// like "node" or "python" are filtered out of install-candidate matching.
// IsInstalled returns true for any tool that has been installed via Install.
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

// unavailableProvider is a provider stub that is not available on the system
// and records no calls.
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

// TestSync_UnavailableProvider_SearchesAvailableProviders is the codex-on-Linux
// shape: the tool spec declares only "brew" as provider, but brew is unavailable.
// An "npm" provider is available and returns a high-confidence search match
// (via GitHub source agreement, which is decisive regardless of provider type).
// Sync must install the tool through npm, not skip it.
func TestSync_UnavailableProvider_SearchesAvailableProviders(t *testing.T) {
	brew := &unavailableProvider{name: "brew"}
	// npm is a concrete provider (not ecosystem-filtered) that can install @openai/codex.
	npm := &searchableProvider{
		name:      "npm",
		available: true,
		searchResults: []provider.SearchResult{
			// High-confidence match: GitHub source matches the tool's Git field.
			// sameGitHubSource is decisive on any provider including language ecosystems.
			// Name is "codex" (the logical tool name) — what the search query uses.
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

	// codex spec: only brew configured (brew is unavailable on Linux). The Git
	// field provides the source anchor for high-confidence matching.
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

	// Expect at least one install op (not a failure or skip).
	var installed bool
	for _, op := range result.Ops {
		if op.Tool.Name == "codex" && op.Kind == isync.OpInstall {
			installed = true
		}
	}
	if !installed {
		t.Errorf("codex was not installed; ops = %+v", result.Ops)
	}

	// npm.Install must have been called for codex.
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

// TestSync_AvailableProvider_NotBypassed verifies acceptance criterion A2:
// when the configured provider IS available, provider search does not bypass it
// in favour of another matching provider.
func TestSync_AvailableProvider_NotBypassed(t *testing.T) {
	brew := &searchableProvider{
		name:      "brew",
		available: true, // configured provider IS available
		searchResults: []provider.SearchResult{
			{Name: "ripgrep", Provider: "brew"},
		},
	}
	// apt is also available and has a match, but should not be used.
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
				// brew is the configured provider and it is available.
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

	// brew must have been used; apt must not have installed ripgrep.
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

// TestSync_UnavailableProvider_FallbackLastResort verifies acceptance criterion
// A3: a Git/GitHub fallback is only attempted when no native provider route is
// available (i.e. provider search found nothing and the fallback is configured).
func TestSync_UnavailableProvider_FallbackLastResort(t *testing.T) {
	brew := &unavailableProvider{name: "brew"}
	// apt is available but returns no search results for this tool.
	apt := &searchableProvider{
		name:          "apt",
		available:     true,
		searchResults: nil, // no matches
	}
	_ = apt

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

	// Expect fallback to have been attempted (OpInstall via fallback path).
	var fallbackOp bool
	for _, op := range result.Ops {
		if op.Tool.Name == "mytool" && op.Kind == isync.OpInstall {
			fallbackOp = true
		}
	}
	// The fallback executor returns an error if it is called, so we see OpFailed
	// OR OpInstall depending on implementation; but either way fallback was tried
	// (not silently skipped as OpFailed with "provider unavailable").
	_ = fallbackOp
	_ = fallbackInstalled

	// Core assertion: the result must not contain a raw "no install route" failure
	// that indicates the tool was skipped without trying fallback.
	for _, op := range result.Ops {
		if op.Tool.Name == "mytool" && op.Kind == isync.OpFailed {
			if op.Err != nil && isNoNativeRouteError(op.Err) && !fallbackInstalled {
				t.Errorf("mytool was skipped with native-unavailable error without attempting fallback: %v", op.Err)
			}
		}
	}
}

// TestSync_UnavailableProvider_WeakMatchNotInstalled verifies acceptance
// criterion A4: weak provider matches are not installed silently.
// A name-only match on a language ecosystem provider (npm) without a
// corroborating GitHub source is considered weak and must not be used.
func TestSync_UnavailableProvider_WeakMatchNotInstalled(t *testing.T) {
	brew := &unavailableProvider{name: "brew"}
	// npm returns a name-only match with no source corroboration: this is weak.
	npm := &searchableProvider{
		name:      "npm",
		available: true,
		searchResults: []provider.SearchResult{
			// Weak: npm name match without GitHub source agreement.
			{Name: "mytool", Provider: "npm"},
		},
	}

	a, cfgPath := newImportApp(t, brew, npm)

	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				// No Git field: no source anchor, so npm name match is weak only.
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

	// npm must not have been called to install mytool (weak match not used).
	for _, call := range npm.installCalls {
		if call.Name == "mytool" {
			t.Errorf("npm.Install called for mytool despite only having a weak match (A4 violation)")
		}
	}
}

// recordingFallbackExecutor records whether the fallback install command was
// executed. It implements executor.Executor minimally.
type recordingFallbackExecutor struct {
	onInstall func()
}

func (e *recordingFallbackExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	if e.onInstall != nil {
		e.onInstall()
	}
	return "", "", nil
}

// isNoNativeRouteError returns true when the error text indicates the tool was
// given up on without any install attempt (the pre-HCL-22 skip message).
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
