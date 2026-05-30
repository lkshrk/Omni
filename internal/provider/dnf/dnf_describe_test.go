package dnf_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func TestDescribe_Success(t *testing.T) {
	out := "Search tool like grep and The Silver Searcher"
	p, _ := newDNF(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "Search tool like grep and The Silver Searcher" {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_Error(t *testing.T) {
	p, _ := newDNF(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.Describe(context.Background(), tool("ripgrep")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDescribe_NotFound(t *testing.T) {
	out := "package ripgrep is not installed\n"
	p, _ := newDNF(executor.MockCall{Stdout: out, Err: errors.New("exit 1")})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc, got %q", desc)
	}
}

func TestBulkDescribe_Success(t *testing.T) {
	out := "ripgrep\tFast grep alternative\ncurl\tCommand line URL tool\n"
	p, _ := newDNF(executor.MockCall{Stdout: out})
	m, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("ripgrep"), tool("curl")})
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if m["ripgrep"] != "Fast grep alternative" {
		t.Errorf("ripgrep = %q", m["ripgrep"])
	}
	if m["curl"] != "Command line URL tool" {
		t.Errorf("curl = %q", m["curl"])
	}
}

func TestBulkDescribe_DoesNotCallDNFInfoForPartialLocalSummaries(t *testing.T) {
	out := "package missing is not installed\nripgrep\tFast grep alternative\n"
	p, m := newDNF(executor.MockCall{Stdout: out, Err: errors.New("exit 1")})

	desc, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("ripgrep"), tool("missing")})
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if desc["ripgrep"] != "Fast grep alternative" {
		t.Fatalf("ripgrep = %q", desc["ripgrep"])
	}
	if _, ok := desc["missing"]; ok {
		t.Fatalf("missing package should not have a summary: %v", desc)
	}
	if len(m.Calls) != 1 || m.Calls[0].Name != "rpm" {
		t.Fatalf("calls = %+v, want one rpm call", m.Calls)
	}
}

func TestBulkDescribe_Empty(t *testing.T) {
	p, _ := newDNF()
	m, err := p.BulkDescribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map for empty input, got %v", m)
	}
}

func TestBulkDescribe_Error(t *testing.T) {
	p, _ := newDNF(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("ripgrep")}); err == nil {
		t.Fatal("expected error, got nil")
	}
}
