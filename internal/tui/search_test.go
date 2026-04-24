package tui

// Tests for the search pipeline: debounceSearch, doSearch, startSearch.

import (
	"context"
	"errors"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// ── search provider stubs ──────────────────────────────────────────────────────

// searchProvider is a Provider that also implements Searcher.
// results and searchErr control what Search returns.
type searchProvider struct {
	name      string
	results   []provider.SearchResult
	searchErr error
}

func (p *searchProvider) Name() string                                       { return p.name }
func (p *searchProvider) Description() string                                { return p.name + " search stub" }
func (p *searchProvider) Available(_ context.Context) (bool, error)          { return true, nil }
func (p *searchProvider) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (p *searchProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (p *searchProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (p *searchProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *searchProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}
func (p *searchProvider) Search(_ context.Context, _ string) ([]provider.SearchResult, error) {
	return p.results, p.searchErr
}

// ── TestDebounceSearch_EmitsMsg ────────────────────────────────────────────────

// TestDebounceSearch_EmitsMsg verifies that debounceSearch sleeps briefly and
// then emits a debouncedSearchMsg with the correct query and generation.
func TestDebounceSearch_EmitsMsg(t *testing.T) {
	cmd := debounceSearch("rg", 1)
	msg := cmd()
	got, ok := msg.(debouncedSearchMsg)
	if !ok {
		t.Fatalf("expected debouncedSearchMsg, got %T", msg)
	}
	if got.query != "rg" {
		t.Errorf("query = %q, want %q", got.query, "rg")
	}
	if got.gen != 1 {
		t.Errorf("gen = %d, want 1", got.gen)
	}
}

// ── TestDoSearch_ReturnsResults ────────────────────────────────────────────────

// TestDoSearch_ReturnsResults verifies that doSearch calls app.Search and
// returns a searchResultsMsg with the converted ToolCache results.
func TestDoSearch_ReturnsResults(t *testing.T) {
	prov := &searchProvider{
		name: "brew",
		results: []provider.SearchResult{
			{Name: "ripgrep", Provider: "brew", Version: "14.1.0", Description: "fast grep"},
			{Name: "fd", Provider: "brew", Version: "10.0.0", Description: "fast find"},
		},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	ctx := context.Background()
	msg := m.doSearch(ctx, "rg", 3)()
	got, ok := msg.(searchResultsMsg)
	if !ok {
		t.Fatalf("expected searchResultsMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.gen != 3 {
		t.Errorf("gen = %d, want 3", got.gen)
	}
	if got.query != "rg" {
		t.Errorf("query = %q, want rg", got.query)
	}
	if got.providerFilter != "" {
		t.Errorf("providerFilter = %q, want empty", got.providerFilter)
	}
	if len(got.tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(got.tools))
	}
	if got.tools[0].Name != "ripgrep" {
		t.Errorf("tools[0].Name = %q, want ripgrep", got.tools[0].Name)
	}
	if got.tools[0].Provider != "system" {
		t.Errorf("tools[0].Provider = %q, want system", got.tools[0].Provider)
	}
	if got.tools[1].Name != "fd" {
		t.Errorf("tools[1].Name = %q, want fd", got.tools[1].Name)
	}
}

// ── TestDoSearch_CancelledReturnsNil ──────────────────────────────────────────

// TestDoSearch_CancelledReturnsNil verifies that when the context is already
// cancelled before the search runs, doSearch returns nil (no message dispatched).
func TestDoSearch_CancelledReturnsNil(t *testing.T) {
	// Use a provider that would return results, but context is cancelled first.
	prov := &searchProvider{
		name:    "brew",
		results: []provider.SearchResult{{Name: "git", Provider: "brew"}},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before running

	msg := m.doSearch(ctx, "git", 1)()
	if msg != nil {
		t.Errorf("expected nil for cancelled context, got %T: %v", msg, msg)
	}
}

// ── TestDoSearch_ErrorReturnsMsg ──────────────────────────────────────────────

// TestDoSearch_ErrorReturnsMsg verifies that when app.Search returns an error,
// doSearch returns a searchResultsMsg with the err field set.
func TestDoSearch_ErrorReturnsMsg(t *testing.T) {
	prov := &searchProvider{
		name:      "brew",
		searchErr: errors.New("network timeout"),
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	ctx := context.Background()
	msg := m.doSearch(ctx, "curl", 2)()
	got, ok := msg.(searchResultsMsg)
	if !ok {
		t.Fatalf("expected searchResultsMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected err to be set")
	}
	if got.gen != 2 {
		t.Errorf("gen = %d, want 2", got.gen)
	}
	if got.query != "curl" {
		t.Errorf("query = %q, want curl", got.query)
	}
	if got.providerFilter != "" {
		t.Errorf("providerFilter = %q, want empty", got.providerFilter)
	}
}

// ── TestStartSearch_SetsSearchingFlag ─────────────────────────────────────────

// TestStartSearch_SetsSearchingFlag verifies that calling startSearch sets
// m.searching to true, clears m.statusMsg (so stale "found N" text from the
// previous search doesn't show under the spinner), and returns commands.
// The "Searching…" label is rendered by activityLabel — startSearch only
// sets statusMsg = "" so that activityLabel is unambiguously shown.
func TestStartSearch_SetsSearchingFlag(t *testing.T) {
	prov := &searchProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)

	fi := textinput.New()
	m := Model{
		app:           a,
		ctx:           context.Background(),
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        fi,
		upgradingKeys: make(map[string]bool),
	}

	cmds := m.startSearch("foo")
	if !m.searching {
		t.Error("searching should be true after startSearch")
	}
	if m.statusMsg != "" {
		t.Errorf("statusMsg = %q, want empty (label handled by activityLabel)", m.statusMsg)
	}
	if len(cmds) == 0 {
		t.Error("expected at least one command returned by startSearch")
	}
}

// ── TestDebounceSearch_DelayIsRoughly300ms ────────────────────────────────────

// TestDebounceSearch_DelayIsRoughly300ms verifies the debounce delay is at
// least 250ms (allowing for timer imprecision) and under 1s (sanity bound).
func TestDebounceSearch_DelayIsRoughly300ms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}
	start := time.Now()
	cmd := debounceSearch("test", 0)
	cmd()
	elapsed := time.Since(start)
	if elapsed < 250*time.Millisecond {
		t.Errorf("debounce delay too short: %v (want >= 250ms)", elapsed)
	}
	if elapsed > 1*time.Second {
		t.Errorf("debounce delay too long: %v (want < 1s)", elapsed)
	}
}

// ── TestDoSearch_VersionAndDescriptionPopulated ───────────────────────────────

// TestDoSearch_VersionAndDescriptionPopulated verifies that Version and
// Description from SearchResult are stored in the ToolCache NullString fields.
func TestDoSearch_VersionAndDescriptionPopulated(t *testing.T) {
	prov := &searchProvider{
		name: "brew",
		results: []provider.SearchResult{
			{Name: "jq", Provider: "brew", Version: "1.7.1", Description: "JSON processor"},
		},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doSearch(context.Background(), "jq", 0)()
	got, ok := msg.(searchResultsMsg)
	if !ok {
		t.Fatalf("expected searchResultsMsg, got %T", msg)
	}
	if len(got.tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(got.tools))
	}
	tc := got.tools[0]
	if !tc.Version.Valid || tc.Version.String != "1.7.1" {
		t.Errorf("Version = %v, want {String:1.7.1, Valid:true}", tc.Version)
	}
	if !tc.Description.Valid || tc.Description.String != "JSON processor" {
		t.Errorf("Description = %v, want {String:JSON processor, Valid:true}", tc.Description)
	}
}

// ── TestStartSearch_CancelsInFlightSearch ──────────────────────────────────────

// TestStartSearch_CancelsInFlightSearch verifies that calling startSearch a
// second time cancels the previous search context (m.searchCancel is replaced).
func TestStartSearch_CancelsInFlightSearch(t *testing.T) {
	prov := &searchProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)

	fi := textinput.New()
	m := Model{
		app:           a,
		ctx:           context.Background(),
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        fi,
		upgradingKeys: make(map[string]bool),
	}

	// First startSearch plants a cancel func.
	m.startSearch("foo")
	firstCancel := m.searchCancel
	if firstCancel == nil {
		t.Fatal("searchCancel should be set after first startSearch")
	}

	// Second startSearch should cancel the first and plant a new cancel func.
	m.startSearch("bar")
	if m.searchCancel == nil {
		t.Fatal("searchCancel should be set after second startSearch")
	}
}

// ── TestStartSearch_ClearsStaleStatusMsg ──────────────────────────────────────

// TestStartSearch_ClearsStaleStatusMsg verifies that starting a new search
// clears any stale statusMsg from the previous one. Without this, the spinner
// would show "found 5" (or an error) under the activity indicator instead of
// letting activityLabel render "Searching…".
func TestStartSearch_ClearsStaleStatusMsg(t *testing.T) {
	prov := &searchProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)

	fi := textinput.New()
	m := Model{
		app:           a,
		ctx:           context.Background(),
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        fi,
		upgradingKeys: make(map[string]bool),
		statusMsg:     "found 5", // stale status from a previous search
		statusIsErr:   false,
	}

	m.startSearch("bar")

	if m.statusMsg != "" {
		t.Errorf("statusMsg = %q after startSearch, want empty so activityLabel shows 'Searching…'", m.statusMsg)
	}
	if m.statusIsErr {
		t.Error("statusIsErr should be false after startSearch")
	}
}

// ── TestDebouncedSearchMsg_CacheHit_ClearsSearching ───────────────────────────

// TestDebouncedSearchMsg_CacheHit_ClearsSearching is a regression test for the
// bug where m.searching remained true when a debouncedSearchMsg resolved to a
// cache hit. The prior search was cancelled (context cancel + searchGen bump),
// but m.searching was not cleared in the cache-hit branch, leaving the spinner
// running forever.
func TestDebouncedSearchMsg_CacheHit_ClearsSearching(t *testing.T) {
	prov := &searchProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	fi := textinput.New()
	m := Model{
		app:           a,
		ctx:           context.Background(),
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        fi,
		upgradingKeys: make(map[string]bool),
		searchCache:   make(map[string]searchCacheEntry),
	}

	// Simulate: prior search was in flight when the user re-typed the same query.
	m.searching = true
	m.searchGen = 1
	m.searchCache[searchCacheKey("foo", "")] = searchCacheEntry{
		tools: []*database.ToolCache{{Name: "foobar", Provider: "brew"}},
		at:    time.Now(),
	}

	newModel, _ := m.Update(debouncedSearchMsg{query: "foo", gen: 1})
	m2 := newModel.(Model)

	if m2.searching {
		t.Error("searching should be false after cache hit — spinner was stuck")
	}
	if m2.statusMsg == "" {
		t.Error("expected a status message (found N) after cache hit")
	}
}

func TestDoSearch_CapturesProviderFilter(t *testing.T) {
	prov := &searchProvider{
		name:    "brew",
		results: []provider.SearchResult{{Name: "rg", Provider: "brew"}},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	m.providerNames = []string{"brew"}
	m.providerTabIdx = 1

	msg := m.doSearch(context.Background(), "rg", 7)()
	got, ok := msg.(searchResultsMsg)
	if !ok {
		t.Fatalf("expected searchResultsMsg, got %T", msg)
	}
	if got.providerFilter != "brew" {
		t.Errorf("providerFilter = %q, want brew", got.providerFilter)
	}
}

func TestSearchCache_IsScopedByProviderFilter(t *testing.T) {
	prov := &searchProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	fi := textinput.New()
	m := Model{
		app:            a,
		ctx:            context.Background(),
		keys:           DefaultKeyMap(),
		spinner:        spinner.New(),
		filter:         fi,
		upgradingKeys:  make(map[string]bool),
		searchCache:    make(map[string]searchCacheEntry),
		providerNames:  []string{"brew", "pip"},
		providerTabIdx: 2,
		searchGen:      1,
	}
	m.searchCache[searchCacheKey("foo", "brew")] = searchCacheEntry{
		tools: []*database.ToolCache{{Name: "foo", Provider: "brew"}},
		at:    time.Now(),
	}

	newModel, _ := m.Update(debouncedSearchMsg{query: "foo", gen: 1})
	m2 := newModel.(Model)

	if !m2.searching {
		t.Fatal("searching should be true when only a different provider's cache entry exists")
	}
	if len(m2.searchTools) != 0 {
		t.Fatalf("searchTools = %d, want no cached brew results for pip tab", len(m2.searchTools))
	}
}

func TestSearchResultsMsg_DropsProviderMismatch(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "existing", Provider: "brew"}})
	m.mode = viewSearch
	m.searchGen = 2
	m.searching = true
	m.searchCache = make(map[string]searchCacheEntry)
	m.providerNames = []string{"brew", "pip"}
	m.providerTabIdx = 2

	got := drive(m, searchResultsMsg{
		query:          "foo",
		providerFilter: "brew",
		gen:            2,
		tools:          []*database.ToolCache{{Name: "foo", Provider: "brew"}},
	})

	if len(got.searchTools) != 0 {
		t.Fatalf("searchTools = %d, want stale provider-filter results dropped", len(got.searchTools))
	}
	if _, ok := got.searchCache[searchCacheKey("foo", "brew")]; ok {
		t.Fatal("searchCache should not store stale provider-filter results")
	}
	if !got.searching {
		t.Fatal("searching should remain owned by the current generation/provider search")
	}
}
