package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// bulkCheckingStub extends stubProvider with the BulkChecker interface.
type bulkCheckingStub struct {
	stubProvider
	bulk map[string]string // lowercase name → version
}

func (b *bulkCheckingStub) InstalledMap(_ context.Context) (map[string]string, error) {
	return b.bulk, nil
}

// bulkConcreteStub extends bulkCheckingStub with ConcreteResolver — models a
// ecosystem provider like "node" that delegates to a concrete backend (e.g. "bun").
type bulkConcreteStub struct {
	bulkCheckingStub
	concreteName string // the resolved backend binary
}

func (b *bulkConcreteStub) ResolvedName(_ context.Context) (string, error) {
	return b.concreteName, nil
}

// isInstalledStub extends stubProvider with a configurable IsInstalled response.
type isInstalledStub struct {
	stubProvider
	installedName string
	installedVer  string
}

func (s *isInstalledStub) IsInstalled(_ context.Context, t provider.Tool) (bool, string, error) {
	if t.Name == s.installedName {
		return true, s.installedVer, nil
	}
	return false, "", nil
}

func TestRefreshInstalled_BulkPath_MarksInstalled(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if !tools[0].Installed {
		t.Errorf("ripgrep.Installed = false, want true")
	}
	if tools[0].Version.String != "14.1.0" {
		t.Errorf("ripgrep.Version = %q, want 14.1.0", tools[0].Version.String)
	}
}

func TestRefreshInstalled_BulkPath_MarksNotInstalled(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{}, // ripgrep absent
	}
	a, cfgPath := newImportApp(t, prov)

	// Pre-seed DB as installed.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if tools[0].Installed {
		t.Errorf("ripgrep.Installed = true, want false (bulk map is empty)")
	}
}

func TestRefreshInstalled_MissingToolPreservesFailureState(t *testing.T) {
	ctx := context.Background()
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "prior install error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	failed, err := a.DB().ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("ListFailed() = %d, want retry-failed marker preserved", len(failed))
	}
	if failed[0].Name != "ripgrep" || failed[0].FailureCount != 1 {
		t.Fatalf("failed row = %+v, want ripgrep with one prior failure", failed[0])
	}
}

func TestRefreshInstalled_SlowPath_MarksInstalled(t *testing.T) {
	// Provider has no BulkChecker — falls back to per-tool IsInstalled.
	prov := &isInstalledStub{
		stubProvider:  stubProvider{name: "pip", available: true},
		installedName: "black",
		installedVer:  "24.3.0",
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "pip")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if !tools[0].Installed {
		t.Errorf("black.Installed = false, want true")
	}
}

func TestRefreshInstalled_UnavailableProvider_SkipsTool(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: false},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// RefreshInstalled should succeed (skipping unavailable provider).
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	// Tool should not appear in DB (never upserted).
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("got %d tools in DB, want 0 (provider unavailable → skipped)", len(tools))
	}
}

func TestRefreshInstalled_EmptyConfig_Noop(t *testing.T) {
	prov := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, prov)

	// No config file → RefreshInstalled is a noop, returns nil.
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled on empty config: %v", err)
	}
}

// ── TestRefreshInstalled_Progress_BulkProvider ────────────────────────────────

// TestRefreshInstalled_Progress_BulkProvider verifies that the progress callback
// is called with "Scanning brew…" before the bulk-check pass for a BulkChecker
// provider. It should be called exactly once per provider.
func TestRefreshInstalled_Progress_BulkProvider(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	var msgs []string
	prog := func(s string) { msgs = append(msgs, s) }

	if err := a.RefreshInstalled(context.Background(), prog); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if len(msgs) == 0 {
		t.Fatal("progress callback never called")
	}
	found := false
	for _, m := range msgs {
		if m == "Scanning brew…" {
			found = true
		}
	}
	if !found {
		t.Errorf("progress msgs = %v, want entry %q", msgs, "Scanning brew…")
	}
}

// ── TestRefreshInstalled_Progress_SlowPath ────────────────────────────────────

// TestRefreshInstalled_Progress_SlowPath verifies that the progress callback is
// called for a non-BulkChecker provider (slow-path IsInstalled per tool).
func TestRefreshInstalled_Progress_SlowPath(t *testing.T) {
	prov := &isInstalledStub{
		stubProvider:  stubProvider{name: "pip", available: true},
		installedName: "black",
		installedVer:  "24.3.0",
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "pip")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	var msgs []string
	prog := func(s string) { msgs = append(msgs, s) }

	if err := a.RefreshInstalled(context.Background(), prog); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if len(msgs) == 0 {
		t.Fatal("progress callback never called for slow-path provider")
	}
	found := false
	for _, m := range msgs {
		if m == "Scanning pip…" {
			found = true
		}
	}
	if !found {
		t.Errorf("progress msgs = %v, want entry %q", msgs, "Scanning pip…")
	}
}

// ── TestRefreshInstalled_Progress_NilCallback ─────────────────────────────────

// TestRefreshInstalled_Progress_NilCallback verifies that passing nil as the
// progress callback works without panicking.
func TestRefreshInstalled_Progress_NilCallback(t *testing.T) {
	prov := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Explicit nil callback — must not panic.
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled with nil callback: %v", err)
	}
}

// ── TestRefreshInstalled_BulkPath_ConcreteResolver ────────────────────────────

// TestRefreshInstalled_BulkPath_ConcreteResolver verifies that ecosystem providers
// which implement both BulkChecker and ConcreteResolver (e.g. "node" backed by
// "bun") store the resolved concrete name in InstalledWith — not the ecosystem name.
// Regression: previously the bulk fast-path always wrote t.Provider ("node")
// as InstalledWith, causing syncWrongProv in the TUI for every node tool when
// the effectiveNodeManager was "bun".
func TestRefreshInstalled_BulkPath_ConcreteResolver(t *testing.T) {
	prov := &bulkConcreteStub{
		bulkCheckingStub: bulkCheckingStub{
			stubProvider: stubProvider{name: "node", available: true},
			bulk:         map[string]string{"typescript": "5.3.3"},
		},
		concreteName: "bun",
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("typescript", "node")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("typescript"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if !tools[0].Installed {
		t.Errorf("typescript.Installed = false, want true")
	}
	if tools[0].InstalledWith != "bun" {
		t.Errorf("typescript.InstalledWith = %q, want %q (concrete backend)", tools[0].InstalledWith, "bun")
	}
}

// ── TestRefreshInstalled_Progress_XofY ───────────────────────────────────────

// TestRefreshInstalled_Progress_XofY verifies that when multiple providers are
// present, progress messages include the "(x/y)" suffix.
func TestRefreshInstalled_Progress_XofY(t *testing.T) {
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.0"},
	}
	pip := &isInstalledStub{
		stubProvider:  stubProvider{name: "pip", available: true},
		installedName: "black",
		installedVer:  "24.3.0",
	}
	a, cfgPath := newImportApp(t, brew, pip)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("black", "pip"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "black"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	var msgs []string
	if err := a.RefreshInstalled(context.Background(), func(s string) { msgs = append(msgs, s) }); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("got %d progress messages, want 2: %v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if !strings.Contains(m, "/2") {
			t.Errorf("progress message %q missing x/2 suffix", m)
		}
	}
}

// ── TestRefreshInstalled_MultiManagerPath ─────────────────────────────────────

// multiManagerStub implements provider.MultiManagerBulkChecker, returning
// per-tool InstalledEntry values with distinct ConcreteManager attribution.
type multiManagerStub struct {
	stubProvider
	entries map[string]provider.InstalledEntry
}

func (s *multiManagerStub) InstalledByManager(_ context.Context) (map[string]provider.InstalledEntry, error) {
	return s.entries, nil
}

// TestRefreshInstalled_MultiManagerPath_PerToolInstalledWith verifies that when
// a provider implements MultiManagerBulkChecker, RefreshInstalled stores the
// per-tool ConcreteManager as InstalledWith — not the provider name. This enables
// WrongProv detection for tools installed by a non-effective backend (e.g. ruff
// installed via pip3 when uv is the configured manager).
func TestRefreshInstalled_MultiManagerPath_PerToolInstalledWith(t *testing.T) {
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "python", available: true},
		entries: map[string]provider.InstalledEntry{
			"black": {Version: "24.3.0", ConcreteManager: "uv"},
			"ruff":  {Version: "0.4.0", ConcreteManager: "pip3"}, // installed by wrong manager
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("black", "python"),
			logicalTool("ruff", "python"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black", "ruff"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}

	byName := make(map[string]*database.ToolCache, len(tools))
	for _, tc := range tools {
		byName[tc.Name] = tc
	}

	if byName["black"].InstalledWith != "uv" {
		t.Errorf("black.InstalledWith = %q, want uv", byName["black"].InstalledWith)
	}
	if byName["ruff"].InstalledWith != "pip3" {
		t.Errorf("ruff.InstalledWith = %q, want pip3 (enables WrongProv when effective=uv)", byName["ruff"].InstalledWith)
	}
	// Both tools are installed (ConcreteManager != "").
	if !byName["black"].Installed {
		t.Errorf("black.Installed = false, want true")
	}
	if !byName["ruff"].Installed {
		t.Errorf("ruff.Installed = false, want true")
	}
}

func TestRefreshInstalled_MultiManagerPath_UsesFullSlashPackage(t *testing.T) {
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "node", available: true},
		entries: map[string]provider.InstalledEntry{
			"@playwright/test": {Version: "1.52.0", ConcreteManager: "npm"},
			"test":             {Version: "0.0.1", ConcreteManager: "pnpm"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalToolPackage("playwright-test", "node", "@playwright/test"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("playwright-test"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(context.Background(), "playwright-test", "node", "@playwright/test")
	if err != nil {
		t.Fatalf("Get playwright-test: %v", err)
	}
	if !got.Installed || got.InstalledWith != "npm" || got.Version.String != "1.52.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/npm/1.52.0", got.Installed, got.InstalledWith, got.Version.String)
	}
}
