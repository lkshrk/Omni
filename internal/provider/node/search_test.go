package node

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestSearch_ReturnsResults(t *testing.T) {
	body := `{"objects":[{"package":{"name":"typescript","version":"5.7.3","description":"TypeScript language"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := newWithRegistry(executor.NewMatchMock(), "", srv.URL, srv.Client())
	results, err := p.Search(context.Background(), "typescript")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Name != "typescript" || results[0].Version != "5.7.3" {
		t.Errorf("got %+v", results[0])
	}
	if results[0].Provider != "node" {
		t.Errorf("Provider = %q, want node", results[0].Provider)
	}
}

func TestSearch_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"objects":[]}`)
	}))
	defer srv.Close()

	p := newWithRegistry(executor.NewMatchMock(), "", srv.URL, srv.Client())
	results, err := p.Search(context.Background(), "zzznomatch")
	if err != nil {
		t.Fatalf("Search: %v", err)
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

	p := newWithRegistry(executor.NewMatchMock(), "", srv.URL, srv.Client())
	_, err := p.Search(context.Background(), "typescript")
	if err == nil {
		t.Fatal("expected error from HTTP 500, got nil")
	}
}

func TestSearch_QueryEscaped(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"objects":[]}`)
	}))
	defer srv.Close()

	p := newWithRegistry(executor.NewMatchMock(), "", srv.URL, srv.Client())
	_, _ = p.Search(context.Background(), "hello world")
	if got != "text=hello+world&size=20" {
		t.Errorf("query = %q, want text=hello+world&size=20", got)
	}
}
