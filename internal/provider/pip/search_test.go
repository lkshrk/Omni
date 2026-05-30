package pip

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestOutdatedInfoMap_UsesPyPIUploadTime(t *testing.T) {
	client := staticJSONClient(`{"releases":{"24.3.0":[{"upload_time_iso_8601":"2026-05-28T12:00:00.000Z"},{"upload_time_iso_8601":"2026-05-28T12:05:00.000Z"}]}}`)

	m := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "pip3 list --outdated --format=json",
		Response: executor.MockCall{Stdout: `[{"name":"black","latest_version":"24.3.0"}]`},
	})
	p := newWithPyPI(m, "https://pypi.test", client)

	got, err := p.OutdatedInfoMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedInfoMap: %v", err)
	}
	info := got["black"]
	if info.LatestVersion != "24.3.0" {
		t.Fatalf("LatestVersion = %q, want 24.3.0", info.LatestVersion)
	}
	want := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	if info.AvailableAt == nil || !info.AvailableAt.Equal(want) || info.DateSource != "pypi_upload_time" {
		t.Fatalf("info = %+v, want PyPI upload time %s", info, want)
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
