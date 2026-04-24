package apt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func TestDescribe_Success(t *testing.T) {
	out := "Package: curl\nVersion: 7.88.1\nDescription-en: command line tool for transferring data with URL syntax\n more details here\n"
	p, _ := newAPT(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("curl"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "command line tool for transferring data with URL syntax" {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_FallsBackToGenericDescription(t *testing.T) {
	out := "Package: curl\nDescription: a transfer tool\n"
	p, _ := newAPT(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("curl"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "a transfer tool" {
		t.Errorf("Describe() = %q, want 'a transfer tool'", desc)
	}
}

func TestDescribe_Error(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.Describe(context.Background(), tool("curl")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDescribe_NotFound(t *testing.T) {
	out := "Package: curl\nVersion: 7.88.1\n"
	p, _ := newAPT(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("curl"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc, got %q", desc)
	}
}

func TestBulkDescribe_Success(t *testing.T) {
	out := "Package: ripgrep\nDescription-en: Fast grep alternative\n\nPackage: curl\nDescription-en: Command line URL tool\n"
	p, _ := newAPT(executor.MockCall{Stdout: out})
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

func TestBulkDescribe_FallsBackToGeneric(t *testing.T) {
	out := "Package: curl\nDescription: a transfer tool\n"
	p, _ := newAPT(executor.MockCall{Stdout: out})
	m, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("curl")})
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if m["curl"] != "a transfer tool" {
		t.Errorf("curl = %q, want 'a transfer tool'", m["curl"])
	}
}

func TestBulkDescribe_Empty(t *testing.T) {
	p, _ := newAPT()
	m, err := p.BulkDescribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map for empty input, got %v", m)
	}
}

func TestBulkDescribe_Error(t *testing.T) {
	p, _ := newAPT(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("curl")}); err == nil {
		t.Fatal("expected error, got nil")
	}
}
