package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestOutdatedInfoByManager_UsesNPMRegistryTime(t *testing.T) {
	outdated := `{"typescript":{"latest":"5.4.0"}}`
	client := staticJSONClient(`{"time":{"5.4.0":"2026-05-28T12:00:00.000Z"}}`)

	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	m.AddRule(executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "10.0.0"}})
	m.AddRule(executor.MatchRule{Pattern: "pnpm outdated -g --json", Response: executor.MockCall{Stdout: outdated, Err: errors.New("exit 1")}})
	p := newWithRegistry(m, "pnpm", "https://registry.test", client)

	got, err := p.OutdatedInfoByManager(context.Background())
	if err != nil {
		t.Fatalf("OutdatedInfoByManager: %v", err)
	}
	info := got["pnpm"]["typescript"]
	if info.LatestVersion != "5.4.0" {
		t.Fatalf("LatestVersion = %q, want 5.4.0", info.LatestVersion)
	}
	want := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	if info.AvailableAt == nil || !info.AvailableAt.Equal(want) || info.DateSource != "npm_registry_time" {
		t.Fatalf("info = %+v, want npm registry time %s", info, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func staticJSONClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
}
