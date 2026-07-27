package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryCatalogState struct {
	value string
}

func catalogCacheJSON(t *testing.T, fetchedAt time.Time, results []FindResult) string {
	t.Helper()
	data, err := json.Marshal(catalogCache{FetchedAt: fetchedAt, Results: results})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (s *memoryCatalogState) GetState(context.Context, string) (string, error) {
	if s.value == "" {
		return "", errors.New("missing")
	}
	return s.value, nil
}

func (s *memoryCatalogState) SetState(_ context.Context, _ string, value string) error {
	s.value = value
	return nil
}

type keyedCatalogState struct {
	values map[string]string
}

func (s *keyedCatalogState) GetState(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", errors.New("missing")
	}
	return value, nil
}

func (s *keyedCatalogState) SetState(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func TestFindSkillPackagesCachesNormalizedQuery(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("q"); got != "react native" {
			t.Errorf("query = %q", got)
		}
		_, _ = w.Write([]byte(`{"query":"react native","skills":[{"source":"owner/repo","skillId":"review","name":"review","installs":12}],"count":1}`))
	}))
	defer server.Close()
	state := new(memoryCatalogState)

	first, err := findSkillPackages(context.Background(), server.Client(), server.URL, state, "  React   Native ", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := findSkillPackages(context.Background(), server.Client(), server.URL, state, "react native", "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(first) != 1 || len(second) != 1 || first[0].Skill != "review" {
		t.Fatalf("calls=%d first=%v second=%v", calls, first, second)
	}
}

func TestFindSkillPackagesReturnsStaleCacheWithWarning(t *testing.T) {
	state := new(memoryCatalogState)
	state.value = catalogCacheJSON(t, time.Now().Add(-2*time.Hour), []FindResult{{
		Source: "owner/repo", Skill: "cached", Installs: "4 installs",
	}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	results, err := findSkillPackages(context.Background(), server.Client(), server.URL, state, "query", "")
	if !IsCatalogWarning(err) || len(results) != 1 || results[0].Skill != "cached" {
		t.Fatalf("results=%v err=%v", results, err)
	}
}

func TestFindSkillPackagesWithoutCacheReturnsNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if results, err := findSkillPackages(context.Background(), server.Client(), server.URL, new(memoryCatalogState), "query", ""); err == nil || results != nil {
		t.Fatalf("results=%v err=%v", results, err)
	}
}

func TestFindSkillPackagesRejectsMalformedCatalogResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[{"source":"owner/repo","skillId":"../escape","installs":1}]}`))
	}))
	defer server.Close()
	if _, err := findSkillPackages(context.Background(), server.Client(), server.URL, new(memoryCatalogState), "query", ""); err == nil {
		t.Fatal("malformed catalog response succeeded")
	}
}

func TestFindSkillPackagesSkipsUnusableRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[
			{"source":"owner/repo","skillId":"../escape","installs":1},
			{"source":"https://github.com/owner/repo","skillId":"ok","installs":2},
			{"source":"owner/good","skillId":"good","installs":3}
		]}`))
	}))
	defer server.Close()

	results, err := findSkillPackages(context.Background(), server.Client(), server.URL, new(memoryCatalogState), "query", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "owner/good" {
		t.Fatalf("results = %+v, want only the usable owner/good row", results)
	}
}

func TestFindSkillPackagesTruncatesOverlongResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rows := make([]string, 0, 15)
		for i := range 15 {
			rows = append(rows, fmt.Sprintf(`{"source":"owner/repo%d","skillId":"s%d","installs":%d}`, i, i, i))
		}
		_, _ = w.Write([]byte(`{"skills":[` + strings.Join(rows, ",") + `]}`))
	}))
	defer server.Close()

	results, err := findSkillPackages(context.Background(), server.Client(), server.URL, new(memoryCatalogState), "query", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != catalogSearchLimit {
		t.Fatalf("results = %d, want truncation to %d", len(results), catalogSearchLimit)
	}
}

func TestFindSkillPackagesEmptyResultSetIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	defer server.Close()

	results, err := findSkillPackages(context.Background(), server.Client(), server.URL, new(memoryCatalogState), "query", "")
	if err != nil || len(results) != 0 {
		t.Fatalf("results=%v err=%v, want an empty success", results, err)
	}
}

func TestFindSkillPackagesHonorsCatalogEndpointOverride(t *testing.T) {
	if got := skillsCatalogEndpoint(); got != defaultSkillsCatalogURL {
		t.Fatalf("skillsCatalogEndpoint() = %q, want the default %q", got, defaultSkillsCatalogURL)
	}
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("q"))
		_, _ = w.Write([]byte(`{"skills":[{"source":"owner/repo","skillId":"review","installs":7}]}`))
	}))
	defer server.Close()

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	t.Setenv("OMNI_SKILLS_CATALOG_URL", server.URL)
	results, err := a.FindSkillPackages(t.Context(), "review", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0] != "review" {
		t.Fatalf("stub catalog queries = %v, want the search to reach the override", queries)
	}
	if len(results) != 1 || results[0].Source != "owner/repo" {
		t.Fatalf("results = %+v, want the stub catalog row", results)
	}
}

func TestFindSkillPackagesOwnerFilterIsSentAndCachedSeparately(t *testing.T) {
	var owners []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owners = append(owners, r.URL.Query().Get("owner"))
		_, _ = w.Write([]byte(`{"skills":[{"source":"owner/repo","skillId":"review","installs":1}]}`))
	}))
	defer server.Close()
	state := &keyedCatalogState{values: map[string]string{}}

	if _, err := findSkillPackages(context.Background(), server.Client(), server.URL, state, "query", "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := findSkillPackages(context.Background(), server.Client(), server.URL, state, "query", ""); err != nil {
		t.Fatal(err)
	}
	if len(owners) != 2 || owners[0] != "acme" || owners[1] != "" {
		t.Fatalf("owner params = %v, want [acme \"\"] (no shared cache entry)", owners)
	}
	if catalogCacheKey(server.URL, "query", "acme") == catalogCacheKey(server.URL, "query", "") {
		t.Error("cache key must include the owner filter")
	}
}
