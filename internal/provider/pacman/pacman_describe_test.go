package pacman_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func TestDescribe_Success(t *testing.T) {
	out := "Repository      : extra\nName            : ripgrep\nDescription     : A search tool that combines the usability of ag with the raw speed of grep\nArchitecture    : x86_64\n"
	p, _ := newPacman(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "A search tool that combines the usability of ag with the raw speed of grep" {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_Error(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.Describe(context.Background(), tool("ripgrep")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDescribe_NotFound(t *testing.T) {
	out := "Repository      : extra\nName            : ripgrep\n"
	p, _ := newPacman(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc, got %q", desc)
	}
}

func TestBulkDescribe_Success(t *testing.T) {
	out := "Name            : ripgrep\nDescription     : Fast grep alternative\n\nName            : curl\nDescription     : Command line URL tool\n"
	p, _ := newPacman(executor.MockCall{Stdout: out})
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
	p, _ := newPacman()
	m, err := p.BulkDescribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map for empty input, got %v", m)
	}
}

func TestBulkDescribe_Error(t *testing.T) {
	p, _ := newPacman(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.BulkDescribe(context.Background(), []provider.Tool{tool("ripgrep")}); err == nil {
		t.Fatal("expected error, got nil")
	}
}
