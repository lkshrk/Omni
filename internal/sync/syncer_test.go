package sync_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	syncer "github.com/lkshrk/omni/internal/sync"
)

// --- mock provider for sync tests ---

type mockProvider struct {
	name         string
	available    bool
	isInstalled  map[string]bool // pkg → installed
	isInstallErr error
	versions     map[string]string // pkg → version
	installed    []string          // record of Install calls
	uninstalled  []string          // record of Uninstall calls
	upgraded     []string          // record of Upgrade calls
}

func (m *mockProvider) Name() string        { return m.name }
func (m *mockProvider) Description() string { return "mock" }
func (m *mockProvider) Available(_ context.Context) (bool, error) {
	return m.available, nil
}
func (m *mockProvider) IsInstalled(_ context.Context, t provider.Tool) (bool, string, error) {
	if m.isInstallErr != nil {
		return false, "", m.isInstallErr
	}
	ok := m.isInstalled[t.EffectivePackage()]
	return ok, m.versions[t.EffectivePackage()], nil
}
func (m *mockProvider) Install(_ context.Context, t provider.Tool) error {
	m.installed = append(m.installed, t.EffectivePackage())
	m.isInstalled[t.EffectivePackage()] = true
	return nil
}
func (m *mockProvider) Uninstall(_ context.Context, t provider.Tool) error {
	m.uninstalled = append(m.uninstalled, t.EffectivePackage())
	m.isInstalled[t.EffectivePackage()] = false
	return nil
}
func (m *mockProvider) Upgrade(_ context.Context, t provider.Tool) error {
	m.upgraded = append(m.upgraded, t.EffectivePackage())
	return nil
}
func (m *mockProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

type managerUninstallProvider struct {
	mockProvider
	uninstallManagers []string
}

func (m *managerUninstallProvider) UninstallFrom(_ context.Context, t provider.Tool, manager string) error {
	m.uninstallManagers = append(m.uninstallManagers, manager)
	m.uninstalled = append(m.uninstalled, t.EffectivePackage())
	m.isInstalled[t.EffectivePackage()] = false
	return nil
}

type postInstallVerifyProvider struct {
	mockProvider
	verifyInstalled bool
	verifyErr       error
	installDone     bool
}

func (p *postInstallVerifyProvider) IsInstalled(_ context.Context, t provider.Tool) (bool, string, error) {
	if !p.installDone {
		return false, "", nil
	}
	if p.verifyErr != nil {
		return false, "", p.verifyErr
	}
	return p.verifyInstalled, p.versions[t.EffectivePackage()], nil
}

func (p *postInstallVerifyProvider) Install(_ context.Context, t provider.Tool) error {
	p.installed = append(p.installed, t.EffectivePackage())
	p.installDone = true
	return nil
}

type privilegedMockProvider struct {
	mockProvider
	plan provider.PrivilegePlan
}

func (p *privilegedMockProvider) PrivilegePlan(_ context.Context, _ provider.PrivilegeAction, _ provider.Tool) (provider.PrivilegePlan, error) {
	return p.plan, nil
}

// --- helpers ---

func newDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func cfg(tools ...config.ToolEntry) *config.Config {
	return &config.Config{Tools: tools}
}

func entry(name, prov string) config.ToolEntry {
	return config.ToolEntry{Name: name, Provider: prov, Package: name}
}

func entryPackage(name, prov, pkg string) config.ToolEntry {
	return config.ToolEntry{Name: name, Provider: prov, Package: pkg}
}

// --- tests ---

func TestSync_AllInstalled_NoInstallCalls(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"ripgrep": true, "node": true},
		versions:    map[string]string{"ripgrep": "14.1.1", "node": "21.5.0"},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew"), entry("node", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(mock.installed) != 0 {
		t.Errorf("expected no installs, got %v", mock.installed)
	}
	if skipped := result.Skipped(); len(skipped) != 2 {
		t.Errorf("expected 2 skipped, got %d", len(skipped))
	}
	// No OpInstall entries should appear in result.Ops.
	for _, op := range result.Ops {
		if op.Kind == syncer.OpInstall {
			t.Errorf("unexpected OpInstall for %q in result.Ops", op.Tool.Name)
		}
	}
}

func TestSync_SomeMissing_InstallsExactlyThose(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"ripgrep": true, "node": false, "jq": false},
		versions:    map[string]string{"ripgrep": "14.1.1"},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx,
		cfg(entry("ripgrep", "brew"), entry("node", "brew"), entry("jq", "brew")),
		syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(mock.installed) != 2 {
		t.Errorf("expected 2 installs, got %v", mock.installed)
	}
	installed := result.Installed()
	if len(installed) != 2 {
		t.Errorf("result.Installed() = %d, want 2", len(installed))
	}
	// Confirm the specific tool names appear in the installed ops.
	wantInstalled := map[string]bool{"node": false, "jq": false}
	for _, op := range installed {
		if _, ok := wantInstalled[op.Tool.Name]; ok {
			wantInstalled[op.Tool.Name] = true
		}
	}
	for name, found := range wantInstalled {
		if !found {
			t.Errorf("expected OpInstall for %q in result.Installed(), got %v", name, installed)
		}
	}
}

func TestSync_SkipPrivileged_SkipsInstallAndCachesRequirement(t *testing.T) {
	ctx := context.Background()
	mock := &privilegedMockProvider{
		mockProvider: mockProvider{
			name:        "apt",
			available:   true,
			isInstalled: map[string]bool{"vim": false},
			versions:    map[string]string{},
		},
		plan: provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "apt install vim"},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	var events []syncer.ProgressEvent
	result, err := s.Sync(ctx, cfg(entry("vim", "apt")), syncer.SyncOptions{
		SkipPrivileged: true,
		ToolProgress: func(event syncer.ProgressEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(mock.installed) != 0 {
		t.Fatalf("installed = %v, want none", mock.installed)
	}
	failed := result.Failed()
	if len(failed) != 1 {
		t.Fatalf("Failed() = %d, want 1", len(failed))
	}
	if failed[0].Err == nil || !strings.Contains(failed[0].Err.Error(), "requires sudo") {
		t.Fatalf("failed error = %v, want sudo requirement", failed[0].Err)
	}
	if len(events) == 0 || events[len(events)-1].Message != "Admin approval needed for vim" {
		t.Fatalf("last progress event = %+v, want admin approval message", events)
	}
	got, err := db.Get(ctx, "vim", "apt", "vim")
	if err != nil {
		t.Fatalf("Get cached row: %v", err)
	}
	if got.Privilege != string(provider.PrivilegeRequired) {
		t.Fatalf("Privilege = %q, want %q", got.Privilege, provider.PrivilegeRequired)
	}
	if !got.PrivilegeReason.Valid || got.PrivilegeReason.String != "apt install vim" {
		t.Fatalf("PrivilegeReason = %+v, want apt install vim", got.PrivilegeReason)
	}
}

func TestSync_InstallWithConcreteProviderKeepsLogicalCacheKey(t *testing.T) {
	ctx := context.Background()
	brew := &bulkMockProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": true},
			versions:    map[string]string{"ripgrep": "14.1.1"},
		},
		installedMap: map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(brew)

	db := newDB(t)
	s := syncer.New(reg, db)
	entry := config.ToolEntry{Name: "ripgrep", Provider: "system", Package: "ripgrep", InstallWith: "brew"}

	result, err := s.Sync(ctx, cfg(entry), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if installed := result.Installed(); len(installed) != 1 || installed[0].Tool.Provider != "system" {
		t.Fatalf("Installed = %+v, want one logical system install", installed)
	}
	if len(brew.installed) != 1 || brew.installed[0] != "ripgrep" {
		t.Fatalf("brew installed = %v, want [ripgrep]", brew.installed)
	}
	cached, err := db.Get(ctx, "ripgrep", "system", "ripgrep")
	if err != nil {
		t.Fatalf("db.Get logical cache key: %v", err)
	}
	if cached.InstalledWith != "brew" {
		t.Fatalf("InstalledWith = %q, want brew", cached.InstalledWith)
	}
}

func TestSync_IsInstalledErrorStopsBeforeInstall(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:         "brew",
		available:    true,
		isInstalled:  map[string]bool{},
		isInstallErr: errors.New("status probe failed"),
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	_, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
	if err == nil {
		t.Fatal("Sync returned nil, want status probe error")
	}
	if len(mock.installed) != 0 {
		t.Fatalf("Install called after status probe failed: %v", mock.installed)
	}
}

func TestSync_PostInstallVerificationFailureMarksFailed(t *testing.T) {
	tests := []struct {
		name      string
		provider  *postInstallVerifyProvider
		wantError string
	}{
		{
			name: "status error",
			provider: &postInstallVerifyProvider{
				mockProvider: mockProvider{name: "brew", available: true, versions: map[string]string{}},
				verifyErr:    errors.New("status failed"),
			},
			wantError: "status failed",
		},
		{
			name: "not installed",
			provider: &postInstallVerifyProvider{
				mockProvider:    mockProvider{name: "brew", available: true, versions: map[string]string{}},
				verifyInstalled: false,
			},
			wantError: "not installed after install",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			reg := provider.NewRegistry()
			reg.Register(tt.provider)
			db := newDB(t)
			s := syncer.New(reg, db)

			result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			failed := result.Failed()
			if len(failed) != 1 {
				t.Fatalf("Failed = %+v, want one failed op", failed)
			}
			if !strings.Contains(failed[0].Err.Error(), tt.wantError) {
				t.Fatalf("failed error = %q, want %q", failed[0].Err.Error(), tt.wantError)
			}
			cached, getErr := db.Get(ctx, "ripgrep", "brew", "ripgrep")
			if getErr != nil {
				t.Fatalf("db.Get: %v", getErr)
			}
			if cached.Installed {
				t.Fatalf("cached Installed = true, want false after verification failure")
			}
		})
	}
}

func TestSync_DryRun_NoInstallCalls(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"ripgrep": false},
		versions:    map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(mock.installed) != 0 {
		t.Errorf("dry-run: expected no installs, got %v", mock.installed)
	}
	if len(result.Ops) == 0 {
		t.Error("dry-run: expected planned ops")
	}
}

func TestSync_ProviderUnavailable(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   false,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Kind != syncer.OpProviderUnavailable {
		t.Errorf("expected OpProviderUnavailable, got %+v", result.Ops)
	}
}

func TestSync_UnknownProvider(t *testing.T) {
	ctx := context.Background()
	reg := provider.NewRegistry() // empty registry
	db := newDB(t)
	s := syncer.New(reg, db)

	// "cargo" is not registered
	result, err := s.Sync(ctx, cfg(entry("rustup", "cargo")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Kind != syncer.OpProviderUnavailable {
		t.Errorf("expected OpProviderUnavailable for unknown provider, got %+v", result.Ops)
	}
}

func TestSync_ProviderFilter(t *testing.T) {
	ctx := context.Background()
	brew := &mockProvider{name: "brew", available: true, isInstalled: map[string]bool{"ripgrep": false}, versions: map[string]string{}}
	pip := &mockProvider{name: "pip", available: true, isInstalled: map[string]bool{"black": false}, versions: map[string]string{}}

	reg := provider.NewRegistry()
	reg.Register(brew)
	reg.Register(pip)

	db := newDB(t)
	s := syncer.New(reg, db)

	_, err := s.Sync(ctx,
		cfg(entry("ripgrep", "brew"), entry("black", "pip")),
		syncer.SyncOptions{Provider: "brew"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(brew.installed) != 1 {
		t.Errorf("expected 1 brew install, got %v", brew.installed)
	}
	if len(pip.installed) != 0 {
		t.Errorf("expected 0 pip installs (filtered out), got %v", pip.installed)
	}
}

func TestSync_ProviderFilterIncludesInstallWithConcreteProvider(t *testing.T) {
	ctx := context.Background()
	brew := &mockProvider{name: "brew", available: true, isInstalled: map[string]bool{"ripgrep": false}, versions: map[string]string{}}
	pip := &mockProvider{name: "pip", available: true, isInstalled: map[string]bool{"black": false}, versions: map[string]string{}}

	reg := provider.NewRegistry()
	reg.Register(brew)
	reg.Register(pip)

	db := newDB(t)
	s := syncer.New(reg, db)

	logical := config.ToolEntry{Name: "ripgrep", Provider: "system", Package: "ripgrep", InstallWith: "brew"}
	_, err := s.Sync(ctx,
		cfg(logical, entry("black", "pip")),
		syncer.SyncOptions{Provider: "brew"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(brew.installed) != 1 || brew.installed[0] != "ripgrep" {
		t.Fatalf("brew installed = %v, want [ripgrep]", brew.installed)
	}
	if len(pip.installed) != 0 {
		t.Fatalf("pip installed = %v, want no installs", pip.installed)
	}
}

func TestSync_ProviderFilterIncludesResolvedConcreteProvider(t *testing.T) {
	ctx := context.Background()
	system := &concreteResolverProvider{
		mockProvider: mockProvider{
			name:        "system",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false},
			versions:    map[string]string{},
		},
		concreteName: "brew",
	}
	pip := &mockProvider{name: "pip", available: true, isInstalled: map[string]bool{"black": false}, versions: map[string]string{}}

	reg := provider.NewRegistry()
	reg.Register(system)
	reg.Register(pip)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx,
		cfg(entry("ripgrep", "system"), entry("black", "pip")),
		syncer.SyncOptions{Provider: "brew", DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Tool.Name != "ripgrep" || result.Ops[0].Tool.Provider != "system" {
		t.Fatalf("Ops = %+v, want only logical system ripgrep", result.Ops)
	}
}

func TestSync_Prune(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"jq": true},
		versions:    map[string]string{"jq": "1.7"},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	// Pre-populate cache with a tool that's no longer in config.
	_ = db.Upsert(ctx, &database.ToolCache{
		Name: "jq", Provider: "brew", Package: "jq", Installed: true,
	})

	s := syncer.New(reg, db)

	// Config has no tools (empty) → jq should be pruned.
	result, err := s.Sync(ctx, cfg(), syncer.SyncOptions{Prune: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(mock.uninstalled) != 1 || mock.uninstalled[0] != "jq" {
		t.Errorf("expected jq to be uninstalled, got %v", mock.uninstalled)
	}
	// result.Ops must contain an OpUninstall entry for "jq".
	found := false
	for _, op := range result.Ops {
		if op.Kind == syncer.OpUninstall && op.Tool.Name == "jq" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected OpUninstall for jq in result.Ops, got: %v", result.Ops)
	}
}

func TestSync_Prune_UsesRegisteredInstalledWithProvider(t *testing.T) {
	ctx := context.Background()
	system := &mockProvider{
		name:        "system",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	brew := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"rg": true},
		versions:    map[string]string{"rg": "14.1.1"},
	}
	reg := provider.NewRegistry()
	reg.Register(system)
	reg.Register(brew)

	db := newDB(t)
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	s := syncer.New(reg, db)
	if _, err := s.Sync(ctx, cfg(), syncer.SyncOptions{Prune: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(brew.uninstalled) != 1 || brew.uninstalled[0] != "rg" {
		t.Fatalf("brew uninstalled = %v, want [rg]", brew.uninstalled)
	}
	if len(system.uninstalled) != 0 {
		t.Fatalf("system uninstalled = %v, want no calls", system.uninstalled)
	}
}

func TestSync_Prune_UsesCachedManagerForEcosystemProvider(t *testing.T) {
	ctx := context.Background()
	python := &managerUninstallProvider{
		mockProvider: mockProvider{
			name:        "python",
			available:   true,
			isInstalled: map[string]bool{"black": true},
			versions:    map[string]string{"black": "24.4.0"},
		},
	}
	reg := provider.NewRegistry()
	reg.Register(python)

	db := newDB(t)
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:          "black",
		Provider:      "python",
		Package:       "black",
		Installed:     true,
		InstalledWith: "pip3",
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	s := syncer.New(reg, db)
	if _, err := s.Sync(ctx, cfg(), syncer.SyncOptions{Prune: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(python.uninstallManagers) != 1 || python.uninstallManagers[0] != "pip3" {
		t.Fatalf("uninstall managers = %v, want [pip3]", python.uninstallManagers)
	}
	if len(python.uninstalled) != 1 || python.uninstalled[0] != "black" {
		t.Fatalf("python uninstalled = %v, want [black]", python.uninstalled)
	}
}

func TestSync_Prune_RemovesUninstalledCacheRowWithoutProviderUninstall(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "python",
		available:   true,
		isInstalled: map[string]bool{"black": true},
		versions:    map[string]string{"black": "24.4.0"},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	if err := db.MarkFailed(ctx, "black", "python", "black", "status failed"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	s := syncer.New(reg, db)
	result, err := s.Sync(ctx, cfg(), syncer.SyncOptions{Prune: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(mock.uninstalled) != 0 {
		t.Fatalf("provider uninstall called for not-installed row: %v", mock.uninstalled)
	}
	if _, err := db.Get(ctx, "black", "python", "black"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache row after prune err = %v, want sql.ErrNoRows", err)
	}
	found := false
	for _, op := range result.Ops {
		if op.Kind == syncer.OpUninstall && op.Tool.Name == "black" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ops = %v, want stale cache cleanup op for black", result.Ops)
	}
}

func TestSyncResult_Errors(t *testing.T) {
	errBoom := errors.New("boom")
	result := &syncer.SyncResult{
		Ops: []syncer.SyncOp{
			{Kind: syncer.OpInstall, Err: nil},
			{Kind: syncer.OpFailed, Err: errBoom},
			{Kind: syncer.OpAlreadyInstalled, Err: nil},
		},
	}

	errs := result.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %d ops, want 1", len(errs))
	}
	if errs[0].Err != errBoom {
		t.Errorf("Errors()[0].Err = %v, want %v", errs[0].Err, errBoom)
	}
}

// ─── IgnoreList ───────────────────────────────────────────────────────────────

func TestSync_IgnoreList_SkipsTools(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	db := newDB(t)
	reg := provider.NewRegistry()
	reg.Register(mock)
	s := syncer.New(reg, db)

	c := cfg(entry("git", "brew"), entry("node", "brew"), entry("ripgrep", "brew"))
	result, err := s.Sync(ctx, c, syncer.SyncOptions{
		DryRun:     true,
		IgnoreList: []string{"node"},
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	ignored := result.Ignored()
	if len(ignored) != 1 {
		t.Fatalf("Ignored() = %d, want 1", len(ignored))
	}
	if ignored[0].Tool.Name != "node" {
		t.Errorf("ignored tool = %q, want 'node'", ignored[0].Tool.Name)
	}

	// git and ripgrep should be non-ignored ops (OpInstall in dry-run)
	nonIgnored := 0
	for _, op := range result.Ops {
		if op.Kind != syncer.OpIgnored {
			nonIgnored++
		}
	}
	if nonIgnored != 2 {
		t.Errorf("non-ignored ops = %d, want 2", nonIgnored)
	}
}

func TestSync_ToolEntryIgnore_SkipsTools(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	db := newDB(t)
	reg := provider.NewRegistry()
	reg.Register(mock)
	s := syncer.New(reg, db)

	ignored := entry("node", "brew")
	ignored.Ignore = true
	c := cfg(entry("git", "brew"), ignored)
	result, err := s.Sync(ctx, c, syncer.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	ignoredOps := result.Ignored()
	if len(ignoredOps) != 1 || ignoredOps[0].Tool.Name != "node" {
		t.Fatalf("Ignored() = %+v, want node", ignoredOps)
	}
	nonIgnored := 0
	for _, op := range result.Ops {
		if op.Kind != syncer.OpIgnored {
			nonIgnored++
		}
	}
	if nonIgnored != 1 {
		t.Fatalf("non-ignored ops = %d, want 1", nonIgnored)
	}
}

func TestSync_IgnoreList_Empty_NoEffect(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	db := newDB(t)
	reg := provider.NewRegistry()
	reg.Register(mock)
	s := syncer.New(reg, db)

	c := cfg(entry("git", "brew"), entry("node", "brew"))
	result, err := s.Sync(ctx, c, syncer.SyncOptions{DryRun: true, IgnoreList: nil})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Ignored()) != 0 {
		t.Errorf("expected no ignored ops, got %d", len(result.Ignored()))
	}
	if len(result.Ops) != 2 {
		t.Errorf("expected 2 ops, got %d", len(result.Ops))
	}
}

// ─── BulkChecker / OutdatedChecker mocks ─────────────────────────────────────

// bulkMockProvider additionally implements BulkChecker.
type bulkMockProvider struct {
	mockProvider
	installedMap map[string]string // lowercase name → version
}

func (b *bulkMockProvider) InstalledMap(_ context.Context) (map[string]string, error) {
	return b.installedMap, nil
}

type concreteResolverProvider struct {
	mockProvider
	concreteName string
}

func (c *concreteResolverProvider) ResolvedName(_ context.Context) (string, error) {
	return c.concreteName, nil
}

type concreteBulkProvider struct {
	bulkMockProvider
	concreteName string
}

func (c *concreteBulkProvider) ResolvedName(_ context.Context) (string, error) {
	return c.concreteName, nil
}

type multiManagerMockProvider struct {
	mockProvider
	installedByManager map[string]provider.InstalledEntry
	concreteName       string
}

func (m *multiManagerMockProvider) InstalledByManager(_ context.Context) (map[string]provider.InstalledEntry, error) {
	return m.installedByManager, nil
}

func (m *multiManagerMockProvider) ResolvedName(_ context.Context) (string, error) {
	return m.concreteName, nil
}

// outdatedMockProvider additionally implements OutdatedChecker.
type outdatedMockProvider struct {
	bulkMockProvider
	outdatedMap map[string]string // lowercase name → latest version
	calls       int
}

func (o *outdatedMockProvider) OutdatedMap(_ context.Context) (map[string]string, error) {
	o.calls++
	return o.outdatedMap, nil
}

// errAvailProvider returns an error from Available.
type errAvailProvider struct {
	mockProvider
}

func (e *errAvailProvider) Available(_ context.Context) (bool, error) {
	return false, errors.New("binary check failed")
}

// ─── Resilience helpers ───────────────────────────────────────────────────────

// failingProvider always returns installErr from Install.
type failingProvider struct {
	mockProvider
	installErr error
}

func (f *failingProvider) Install(_ context.Context, t provider.Tool) error {
	f.mockProvider.installed = append(f.mockProvider.installed, t.EffectivePackage())
	return f.installErr
}

// slowProvider blocks in Install until delay elapses or the context is cancelled.
type slowProvider struct {
	mockProvider
	delay time.Duration
}

func (s *slowProvider) Install(ctx context.Context, _ provider.Tool) error {
	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSync_IgnoreList_MultipleTools(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	db := newDB(t)
	reg := provider.NewRegistry()
	reg.Register(mock)
	s := syncer.New(reg, db)

	c := cfg(entry("git", "brew"), entry("node", "brew"), entry("python", "brew"), entry("ripgrep", "brew"))
	result, err := s.Sync(ctx, c, syncer.SyncOptions{
		DryRun:     true,
		IgnoreList: []string{"node", "python"},
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Ignored()) != 2 {
		t.Fatalf("Ignored() = %d, want 2", len(result.Ignored()))
	}
}

// ─── Resilience tests ─────────────────────────────────────────────────────────

func TestSync_InstallFails_ProducesOpFailed(t *testing.T) {
	ctx := context.Background()
	installErr := errors.New("brew: network error")
	mock := &failingProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false},
			versions:    map[string]string{},
		},
		installErr: installErr,
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Failed()) != 1 {
		t.Fatalf("Failed() = %d, want 1", len(result.Failed()))
	}
	op := result.Failed()[0]
	if op.Kind != syncer.OpFailed {
		t.Errorf("op.Kind = %v, want OpFailed", op.Kind)
	}
	if op.Err != installErr {
		t.Errorf("op.Err = %v, want %v", op.Err, installErr)
	}
}

func TestSync_InstallFails_WritesToDB(t *testing.T) {
	ctx := context.Background()
	mock := &failingProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false},
			versions:    map[string]string{},
		},
		installErr: errors.New("network error"),
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	_, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	failed, err := db.ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("ListFailed() = %d, want 1", len(failed))
	}
	if failed[0].Name != "ripgrep" || failed[0].FailureCount != 1 {
		t.Errorf("got %+v, want ripgrep/failure_count=1", failed[0])
	}
}

func TestSync_InstallCanceled_ClearsFailureMarker(t *testing.T) {
	ctx := context.Background()
	mock := &failingProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false},
			versions:    map[string]string{},
		},
		installErr: context.Canceled,
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	if err := db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "prior failure"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Failed()) != 1 {
		t.Fatalf("Failed() = %d, want 1 cancelled op", len(result.Failed()))
	}

	failed, err := db.ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("ListFailed() = %d, want 0 after cancellation", len(failed))
	}
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount != 0 || got.FailedAt != nil || got.LastError.Valid {
		t.Fatalf("cancelled install should clear failure marker, got %+v", got)
	}
}

func TestSync_InstallFails_ContinuesOtherTools(t *testing.T) {
	ctx := context.Background()
	mock := &failingProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false, "jq": true},
			versions:    map[string]string{"jq": "1.7"},
		},
		installErr: errors.New("always fails"),
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew"), entry("jq", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(result.Ops))
	}
	if len(result.Failed()) != 1 {
		t.Errorf("Failed() = %d, want 1", len(result.Failed()))
	}
	if len(result.Skipped()) != 1 {
		t.Errorf("Skipped() = %d, want 1", len(result.Skipped()))
	}
}

func TestSync_RetryFailed_OnlyRetriesFailedTools(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	// Pre-mark ripgrep as failed; jq has no failure.
	_ = db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "prior error")

	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"ripgrep": false, "jq": false},
		versions:    map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx,
		cfg(entry("ripgrep", "brew"), entry("jq", "brew")),
		syncer.SyncOptions{RetryFailed: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Only ripgrep should be attempted (jq filtered out — no prior failure).
	if len(result.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(result.Ops))
	}
	if result.Ops[0].Tool.Name != "ripgrep" {
		t.Errorf("op tool = %q, want 'ripgrep'", result.Ops[0].Tool.Name)
	}
}

func TestSync_RetryFailed_EmptyWhenNoFailures(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)

	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{"ripgrep": false},
		versions:    map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")),
		syncer.SyncOptions{RetryFailed: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Ops) != 0 {
		t.Errorf("expected 0 ops (no failures in DB), got %d", len(result.Ops))
	}
}

// ─── Coverage gap tests ───────────────────────────────────────────────────────

func TestSync_Progress_Callback(t *testing.T) {
	ctx := context.Background()
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	var messages []string
	opts := syncer.SyncOptions{
		DryRun:   true,
		Progress: func(msg string) { messages = append(messages, msg) },
	}
	_, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), opts)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(messages) == 0 {
		t.Error("expected Progress to be called at least once")
	}
}

func TestSync_BulkChecker_UsesInstalledMap(t *testing.T) {
	ctx := context.Background()
	mock := &bulkMockProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{},
			versions:    map[string]string{},
		},
		installedMap: map[string]string{
			"ripgrep": "14.1.1",
			"jq":      "1.7",
		},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew"), entry("jq", "brew")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	skipped := result.Skipped()
	if len(skipped) != 2 {
		t.Fatalf("Skipped() = %d, want 2", len(skipped))
	}
	// Versions should be populated from the bulk map.
	for _, op := range skipped {
		if op.Version == "" {
			t.Errorf("tool %s: expected version from BulkChecker, got empty", op.Tool.Name)
		}
	}
}

func TestSync_BulkChecker_UsesFullSlashPackage(t *testing.T) {
	ctx := context.Background()
	mock := &bulkMockProvider{
		mockProvider: mockProvider{
			name:        "node",
			available:   true,
			isInstalled: map[string]bool{},
			versions:    map[string]string{},
		},
		installedMap: map[string]string{
			"@playwright/test": "1.52.0",
			"test":             "0.0.1",
		},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entryPackage("playwright-test", "node", "@playwright/test")), syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	skipped := result.Skipped()
	if len(skipped) != 1 {
		t.Fatalf("Skipped() = %d, want 1", len(skipped))
	}
	if skipped[0].Version != "1.52.0" {
		t.Fatalf("skipped version = %q, want full scoped package version 1.52.0", skipped[0].Version)
	}
}

func TestSync_BulkChecker_PersistsConcreteInstalledWith(t *testing.T) {
	ctx := context.Background()
	mock := &bulkMockProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{},
			versions:    map[string]string{},
		},
		installedMap: map[string]string{"ripgrep": "14.1.1"},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	if _, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	cached, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if cached.InstalledWith != "brew" {
		t.Fatalf("InstalledWith = %q, want brew", cached.InstalledWith)
	}
}

func TestSync_BulkChecker_PersistsResolvedConcreteInstalledWith(t *testing.T) {
	ctx := context.Background()
	mock := &concreteBulkProvider{
		bulkMockProvider: bulkMockProvider{
			mockProvider: mockProvider{
				name:        "node",
				available:   true,
				isInstalled: map[string]bool{},
				versions:    map[string]string{},
			},
			installedMap: map[string]string{"typescript": "5.3.3"},
		},
		concreteName: "pnpm",
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	if _, err := s.Sync(ctx, cfg(entry("typescript", "node")), syncer.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	cached, err := db.Get(ctx, "typescript", "node", "typescript")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if cached.InstalledWith != "pnpm" {
		t.Fatalf("InstalledWith = %q, want pnpm", cached.InstalledWith)
	}
}

func TestSync_MultiManagerBulkChecker_PersistsOwningManager(t *testing.T) {
	ctx := context.Background()
	mock := &multiManagerMockProvider{
		mockProvider: mockProvider{
			name:        "python",
			available:   true,
			isInstalled: map[string]bool{},
			versions:    map[string]string{},
		},
		installedByManager: map[string]provider.InstalledEntry{
			"black": {Version: "24.4.0", ConcreteManager: "pip3"},
		},
		concreteName: "uv",
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	if _, err := s.Sync(ctx, cfg(entry("black", "python")), syncer.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	cached, err := db.Get(ctx, "black", "python", "black")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if cached.InstalledWith != "pip3" {
		t.Fatalf("InstalledWith = %q, want pip3", cached.InstalledWith)
	}
	if cached.Version.String != "24.4.0" {
		t.Fatalf("Version = %q, want 24.4.0", cached.Version.String)
	}
}

func TestSync_Install_PersistsResolvedConcreteInstalledWith(t *testing.T) {
	ctx := context.Background()
	mock := &concreteResolverProvider{
		mockProvider: mockProvider{
			name:        "system",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false},
			versions:    map[string]string{},
		},
		concreteName: "brew",
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	if _, err := s.Sync(ctx, cfg(entry("ripgrep", "system")), syncer.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	cached, err := db.Get(ctx, "ripgrep", "system", "ripgrep")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if cached.InstalledWith != "brew" {
		t.Fatalf("InstalledWith = %q, want brew", cached.InstalledWith)
	}
}

func TestSync_PreservesOutdatedState(t *testing.T) {
	ctx := context.Background()
	mock := &outdatedMockProvider{
		bulkMockProvider: bulkMockProvider{
			mockProvider: mockProvider{
				name:        "brew",
				available:   true,
				isInstalled: map[string]bool{},
				versions:    map[string]string{},
			},
			installedMap: map[string]string{"ripgrep": "14.0.0"},
		},
		outdatedMap: map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
	}); err != nil {
		t.Fatalf("prepopulate db: %v", err)
	}
	if err := db.UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "14.1.1"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}
	s := syncer.New(reg, db)

	if _, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	cached, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if !cached.Outdated || cached.LatestVersion.String != "14.1.1" {
		t.Fatalf("outdated/latest = %v/%q, want preserved true/14.1.1", cached.Outdated, cached.LatestVersion.String)
	}
	if mock.calls != 0 {
		t.Fatalf("OutdatedMap calls = %d, want 0; refresh owns update availability", mock.calls)
	}
	if len(mock.upgraded) > 0 {
		t.Fatalf("sync called Upgrade %v, want none", mock.upgraded)
	}
}

func TestSync_AvailabilityCheckError_PropagatesErr(t *testing.T) {
	ctx := context.Background()
	mock := &errAvailProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   false,
			isInstalled: map[string]bool{},
			versions:    map[string]string{},
		},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	_, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{})
	if err == nil {
		t.Fatal("expected error from availability check, got nil")
	}
}

func TestSync_PackageNameWithSlash_LooksUpByBasename(t *testing.T) {
	ctx := context.Background()
	// Package "hashicorp/tap/terraform" — BulkChecker key must be "terraform".
	mock := &bulkMockProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{},
			versions:    map[string]string{},
		},
		installedMap: map[string]string{
			"terraform": "1.7.0",
		},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)
	db := newDB(t)
	s := syncer.New(reg, db)

	tapEntry := config.ToolEntry{Name: "terraform", Provider: "brew", Package: "hashicorp/tap/terraform"}
	result, err := s.Sync(ctx, &config.Config{Tools: []config.ToolEntry{tapEntry}}, syncer.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Skipped()) != 1 {
		t.Fatalf("expected terraform found via basename lookup, Skipped()=%d", len(result.Skipped()))
	}
}

func TestSync_Prune_UnknownProviderSkipped(t *testing.T) {
	ctx := context.Background()
	// Registry only has "brew"; DB has a "cargo" tool that is no longer registered.
	mock := &mockProvider{
		name:        "brew",
		available:   true,
		isInstalled: map[string]bool{},
		versions:    map[string]string{},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	db := newDB(t)
	_ = db.Upsert(ctx, &database.ToolCache{
		Name: "rustup", Provider: "cargo", Package: "rustup", Installed: true,
	})

	s := syncer.New(reg, db)
	result, err := s.Sync(ctx, cfg(), syncer.SyncOptions{Prune: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// The "cargo" tool should be silently skipped (not produce an Uninstall op).
	for _, op := range result.Ops {
		if op.Kind == syncer.OpUninstall {
			t.Errorf("unexpected OpUninstall for unknown-provider tool: %+v", op)
		}
	}
}

func TestSync_InstallTimeout_ProducesOpFailed(t *testing.T) {
	ctx := context.Background()
	slow := &slowProvider{
		mockProvider: mockProvider{
			name:        "brew",
			available:   true,
			isInstalled: map[string]bool{"ripgrep": false},
			versions:    map[string]string{},
		},
		delay: 500 * time.Millisecond,
	}
	reg := provider.NewRegistry()
	reg.Register(slow)

	db := newDB(t)
	s := syncer.New(reg, db)

	result, err := s.Sync(ctx, cfg(entry("ripgrep", "brew")), syncer.SyncOptions{
		InstallTimeout: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Failed()) != 1 {
		t.Fatalf("Failed() = %d, want 1", len(result.Failed()))
	}
	if result.Failed()[0].Err == nil {
		t.Error("expected non-nil Err for timed-out install")
	}
}
