package apk_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func TestDescribe_Success(t *testing.T) {
	out := "ripgrep-14.1.1-r0 description:\nripgrep is a line-oriented search tool\n"
	p, _ := newAPK(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "ripgrep is a line-oriented search tool" {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_Error(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.Describe(context.Background(), tool("ripgrep")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDescribe_NotFound(t *testing.T) {
	out := "ripgrep-14.1.1-r0 webpage:\nhttps://example.com\n"
	p, _ := newAPK(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc, got %q", desc)
	}
}

func TestBulkDescribe_Success(t *testing.T) {
	out := "ripgrep-14.1.1-r0 description:\nFast line-oriented search tool\n\ncurl-8.7.1-r0 description:\nA URL transfer tool\n"
	p, _ := newAPK(executor.MockCall{Stdout: out})
	m, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("ripgrep"), tool("curl")})
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if m["ripgrep"] != "Fast line-oriented search tool" {
		t.Errorf("ripgrep = %q", m["ripgrep"])
	}
	if m["curl"] != "A URL transfer tool" {
		t.Errorf("curl = %q", m["curl"])
	}
}

func TestBulkDescribe_LongestPrefixWins(t *testing.T) {
	// "python3" must not steal the description of "python3-dev".
	out := "python3-3.11.0-r0 description:\nPython 3 interpreter\n\npython3-dev-3.11.0-r0 description:\nPython 3 development headers\n"
	p, _ := newAPK(executor.MockCall{Stdout: out})
	m, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("python3"), tool("python3-dev")})
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if m["python3"] != "Python 3 interpreter" {
		t.Errorf("python3 = %q", m["python3"])
	}
	if m["python3-dev"] != "Python 3 development headers" {
		t.Errorf("python3-dev = %q", m["python3-dev"])
	}
}

func TestBulkDescribe_Empty(t *testing.T) {
	p, _ := newAPK()
	m, err := p.BulkDescribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map for empty input, got %v", m)
	}
}

func TestBulkDescribe_Error(t *testing.T) {
	p, _ := newAPK(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("ripgrep")}); err == nil {
		t.Fatal("expected error, got nil")
	}
}
