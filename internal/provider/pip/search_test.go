package pip

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestSearch_Found(t *testing.T) {
	body := `{"info":{"name":"black","version":"24.3.0","summary":"The uncompromising code formatter."}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	results, err := p.Search(context.Background(), "black")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Name != "black" || results[0].Version != "24.3.0" {
		t.Errorf("got %+v", results[0])
	}
	if results[0].Provider != "pip" {
		t.Errorf("Provider = %q, want pip", results[0].Provider)
	}
}

func TestSearch_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	results, err := p.Search(context.Background(), "zzznomatch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	_, err := p.Search(context.Background(), "black")
	if err == nil {
		t.Fatal("expected error from HTTP 500, got nil")
	}
}

func TestSearch_URLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"info":{"name":"black","version":"24.3.0","summary":""}}`)
	}))
	defer srv.Close()

	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	_, _ = p.Search(context.Background(), "black")
	if gotPath != "/pypi/black/json" {
		t.Errorf("path = %q, want /pypi/black/json", gotPath)
	}
}

func TestSearch_URLPathEscapesQuery(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = fmt.Fprint(w, `{"info":{"name":"black","version":"24.3.0","summary":""}}`)
	}))
	defer srv.Close()

	query := "pkg/../name?version=1"
	p := newWithPyPI(executor.NewMatchMock(), srv.URL, srv.Client())
	_, _ = p.Search(context.Background(), query)

	want := "/pypi/" + url.PathEscape(query) + "/json"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}
