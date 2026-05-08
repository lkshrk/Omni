package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func nodeTool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "node", Package: name}
}

func TestDescribe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"description": "Fast, disk space efficient package manager"})
	}))
	defer srv.Close()

	p := newWithRegistry(executor.NewMatchMock(), "pnpm", srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), nodeTool("pnpm"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty description")
	}
}

func TestDescribe_FallsBackToReadmeIntro(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"readme": "# corepack\n\nCorepack is a zero-runtime-dependency Node.js script.\n\n## Usage\nMore docs.",
		})
	}))
	defer srv.Close()

	p := newWithRegistry(executor.NewMatchMock(), "pnpm", srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), nodeTool("corepack"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "Corepack is a zero-runtime-dependency Node.js script." {
		t.Fatalf("desc = %q, want README intro", desc)
	}
}

func TestDescribe_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := newWithRegistry(executor.NewMatchMock(), "pnpm", srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), nodeTool("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc on 404, got %q", desc)
	}
}

func TestDescribe_HTTPError(t *testing.T) {
	p := newWithRegistry(executor.NewMatchMock(), "pnpm", "http://127.0.0.1:0", &http.Client{})
	_, err := p.Describe(context.Background(), nodeTool("pkg"))
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestCmdStr(t *testing.T) {
	if got := cmdStr("npm", []string{"install", "-g", "pkg"}); got != "npm install -g pkg" {
		t.Errorf("cmdStr() = %q", got)
	}
}
