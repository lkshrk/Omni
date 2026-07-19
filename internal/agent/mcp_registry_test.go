package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type registryMcpStub struct{ id string }

func (s *registryMcpStub) ID() string      { return s.id }
func (s *registryMcpStub) Available() bool { return true }
func (s *registryMcpStub) List(context.Context) ([]InstalledMcpServer, error) {
	return nil, nil
}
func (s *registryMcpStub) Add(context.Context, config.McpServer) error { return nil }
func (s *registryMcpStub) Remove(context.Context, string) error        { return nil }

func TestNewRegistryRejectsDuplicateTargetIDs(t *testing.T) {
	_, err := newRegistry([]Target{{ID: "duplicate"}, {ID: "duplicate"}})
	if err == nil || !strings.Contains(err.Error(), `duplicate target ID "duplicate"`) {
		t.Fatalf("NewRegistry() error = %v, want duplicate target ID", err)
	}
}

func TestWithDefaultMcpAdaptersUsesCanonicalProductionSet(t *testing.T) {
	r := mustRegistry(t, WithDefaultMcpAdapters(nil, nil))
	got := r.McpAdapters()
	want := []string{"claude-code", "codex", "grok"}
	if len(got) != len(want) {
		t.Fatalf("Registry.McpAdapters() count = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID() != id {
			t.Fatalf("Registry.McpAdapters()[%d].ID() = %q, want %q", i, got[i].ID(), id)
		}
	}
}

func TestNewRegistryRejectsUnknownMcpAdapterID(t *testing.T) {
	_, err := NewRegistry(WithMcpAdapters([]McpAdapter{&registryMcpStub{id: "missing"}}))
	if err == nil || !strings.Contains(err.Error(), `unknown target ID "missing"`) {
		t.Fatalf("NewRegistry() error = %v, want unknown target ID", err)
	}
}

func TestNewRegistryRejectsDuplicateMcpAdapterID(t *testing.T) {
	_, err := NewRegistry(WithMcpAdapters([]McpAdapter{
		&registryMcpStub{id: "codex"},
		&registryMcpStub{id: "codex"},
	}))
	if err == nil || !strings.Contains(err.Error(), `duplicate MCP adapter ID "codex"`) {
		t.Fatalf("NewRegistry() error = %v, want duplicate MCP adapter ID", err)
	}
}

func TestRegistryMcpAdaptersUsesCanonicalTargetOrder(t *testing.T) {
	claude := &registryMcpStub{id: "claude-code"}
	codex := &registryMcpStub{id: "codex"}
	grok := &registryMcpStub{id: "grok"}
	r := mustRegistry(t, WithMcpAdapters([]McpAdapter{grok, codex, claude}))

	got := r.McpAdapters()
	want := []McpAdapter{claude, codex, grok}
	if len(got) != len(want) {
		t.Fatalf("Registry.McpAdapters() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Registry.McpAdapters()[%d].ID() = %q, want %q", i, got[i].ID(), want[i].ID())
		}
	}
	target, ok := r.ByID("codex")
	if !ok || target.MCP != codex {
		t.Fatalf("Registry.ByID(codex).MCP = %v, want codex adapter", target.MCP)
	}

	got[0] = nil
	if fresh := r.McpAdapters(); fresh[0] != claude {
		t.Fatal("Registry.McpAdapters() exposed mutable registry state")
	}
}

func TestRegistryMcpAdaptersExcludesMissingCapabilities(t *testing.T) {
	r := mustRegistry(t)
	if got := r.McpAdapters(); len(got) != 0 {
		t.Fatalf("Registry.McpAdapters() = %v, want none", got)
	}
}
