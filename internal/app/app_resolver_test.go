package app

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

type availabilityCountingProvider struct {
	name      string
	available bool
	calls     int
}

func (p *availabilityCountingProvider) Name() string        { return p.name }
func (p *availabilityCountingProvider) Description() string { return p.name + " stub" }
func (p *availabilityCountingProvider) Available(context.Context) (bool, error) {
	p.calls++
	return p.available, nil
}
func (p *availabilityCountingProvider) Install(context.Context, provider.Tool) error   { return nil }
func (p *availabilityCountingProvider) Uninstall(context.Context, provider.Tool) error { return nil }
func (p *availabilityCountingProvider) Upgrade(context.Context, provider.Tool) error   { return nil }
func (p *availabilityCountingProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *availabilityCountingProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func TestResolveToolsCachesProviderAvailabilityPerPass(t *testing.T) {
	ctx := context.Background()
	unavailable := &availabilityCountingProvider{name: "missing", available: false}
	available := &availabilityCountingProvider{name: "available", available: true}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, unavailable, available); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	group := &config.GroupConfig{Name: "base"}
	cfg := &config.RootConfig{
		Tools:  make(map[string]config.ToolSpec),
		Groups: []*config.GroupConfig{group},
	}
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("tool%d", i)
		cfg.Tools[name] = config.ToolSpec{
			Provider: "missing",
			Variants: []config.ToolInstallSpec{
				{Provider: "available"},
			},
		}
		group.Tools = append(group.Tools, config.ToolEntry{Name: name})
	}

	resolved, warnings := a.resolveTools(ctx, cfg, cfg.Groups)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(resolved) != 8 {
		t.Fatalf("resolved tools = %d, want 8", len(resolved))
	}
	for _, tool := range resolved {
		if tool.entry.Provider != "available" {
			t.Fatalf("resolved provider = %q, want available", tool.entry.Provider)
		}
	}
	if unavailable.calls != 1 {
		t.Fatalf("unavailable Available calls = %d, want 1", unavailable.calls)
	}
	if available.calls != 1 {
		t.Fatalf("available Available calls = %d, want 1", available.calls)
	}

	_, _ = a.resolveTools(ctx, cfg, cfg.Groups)
	if unavailable.calls != 2 || available.calls != 2 {
		t.Fatalf("availability cache escaped pass: missing=%d available=%d, want 2/2", unavailable.calls, available.calls)
	}
}
