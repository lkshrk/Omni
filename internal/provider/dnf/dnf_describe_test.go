package dnf_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func TestDescribe_Success(t *testing.T) {
	out := "Name         : ripgrep\nVersion      : 14.1.1\nSummary      : Search tool like grep and The Silver Searcher\nDescription  : ripgrep is a line-oriented search tool\n"
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
	out := "Name         : ripgrep\nVersion      : 14.1.1\n"
	p, _ := newDNF(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc, got %q", desc)
	}
}

func TestBulkDescribe_Success(t *testing.T) {
	out := "Name         : ripgrep\nSummary      : Fast grep alternative\n\nName         : curl\nSummary      : Command line URL tool\n"
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
