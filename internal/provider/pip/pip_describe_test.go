package pip

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func pipTool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "pip", Package: name}
}

func TestDescribe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payload := map[string]any{"info": map[string]any{"summary": "The uncompromising code formatter."}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), pipTool("black"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "The uncompromising code formatter." {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_URLPathEscapesPackage(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		payload := map[string]any{"info": map[string]any{"summary": ""}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	pkg := "pkg/../name?version=1"
	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	_, _ = p.Describe(context.Background(), pipTool(pkg))

	want := "/pypi/" + url.PathEscape(pkg) + "/json"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestDescribe_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	desc, err := p.Describe(context.Background(), pipTool("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "" {
		t.Errorf("expected empty desc on 404, got %q", desc)
	}
}

func TestDescribe_HTTPError(t *testing.T) {
	p := newWithPyPI(executor.NewMatchMock(), "http://127.0.0.1:0", &http.Client{})
	_, err := p.Describe(context.Background(), pipTool("black"))
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}
