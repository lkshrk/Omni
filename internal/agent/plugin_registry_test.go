package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type registryPluginStub struct{ id string }

func (s *registryPluginStub) ID() string      { return s.id }
func (s *registryPluginStub) Available() bool { return true }
func (s *registryPluginStub) ListPlugins(context.Context) ([]InstalledPlugin, error) {
	return nil, nil
}
func (s *registryPluginStub) InstallPlugin(context.Context, config.Plugin) error { return nil }
func (s *registryPluginStub) RemovePlugin(context.Context, config.Plugin) error  { return nil }
func (s *registryPluginStub) UpdatePlugin(context.Context, string, string) error { return nil }
func (s *registryPluginStub) ListMarketplaces(context.Context) ([]InstalledMarketplace, error) {
	return nil, nil
}
func (s *registryPluginStub) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *registryPluginStub) UpdateMarketplaces(context.Context) error { return nil }

func TestNewRegistryRejectsUnknownPluginAdapterID(t *testing.T) {
	_, err := NewRegistry(WithPluginAdapters([]PluginAdapter{&registryPluginStub{id: "missing"}}))
	if err == nil || !strings.Contains(err.Error(), `unknown target ID "missing"`) {
		t.Fatalf("NewRegistry() error = %v, want unknown target ID", err)
	}
}

func TestWithDefaultPluginAdaptersUsesCanonicalProductionSet(t *testing.T) {
	r := mustRegistry(t, WithDefaultPluginAdapters(nil, nil))
	got := r.PluginAdapters()
	want := []string{"claude-code", "codex", "grok", "hermes-agent"}
	if len(got) != len(want) {
		t.Fatalf("Registry.PluginAdapters() count = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID() != id {
			t.Fatalf("Registry.PluginAdapters()[%d].ID() = %q, want %q", i, got[i].ID(), id)
		}
	}
}

func TestNewRegistryRejectsDuplicatePluginAdapterID(t *testing.T) {
	_, err := NewRegistry(WithPluginAdapters([]PluginAdapter{
		&registryPluginStub{id: "codex"},
		&registryPluginStub{id: "codex"},
	}))
	if err == nil || !strings.Contains(err.Error(), `duplicate plugin adapter ID "codex"`) {
		t.Fatalf("NewRegistry() error = %v, want duplicate plugin adapter ID", err)
	}
}

func TestNewRegistryRejectsNilPluginAdapter(t *testing.T) {
	_, err := NewRegistry(WithPluginAdapters([]PluginAdapter{nil}))
	if err == nil || !strings.Contains(err.Error(), "nil plugin adapter") {
		t.Fatalf("NewRegistry() error = %v, want nil plugin adapter", err)
	}
}

func TestRegistryPluginAdaptersUsesCanonicalTargetOrder(t *testing.T) {
	claude := &registryPluginStub{id: "claude-code"}
	codex := &registryPluginStub{id: "codex"}
	grok := &registryPluginStub{id: "grok"}
	r := mustRegistry(t, WithPluginAdapters([]PluginAdapter{grok, codex, claude}))

	got := r.PluginAdapters()
	want := []PluginAdapter{claude, codex, grok}
	if len(got) != len(want) {
		t.Fatalf("Registry.PluginAdapters() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Registry.PluginAdapters()[%d].ID() = %q, want %q", i, got[i].ID(), want[i].ID())
		}
	}
	target, ok := r.ByID("codex")
	if !ok || target.Plugins != codex {
		t.Fatalf("Registry.ByID(codex).Plugins = %v, want codex adapter", target.Plugins)
	}

	got[0] = nil
	if fresh := r.PluginAdapters(); fresh[0] != claude {
		t.Fatal("Registry.PluginAdapters() exposed mutable registry state")
	}
}

func TestRegistryPluginAdaptersExcludesMissingCapabilities(t *testing.T) {
	r := mustRegistry(t)
	if got := r.PluginAdapters(); len(got) != 0 {
		t.Fatalf("Registry.PluginAdapters() = %v, want none", got)
	}
}
