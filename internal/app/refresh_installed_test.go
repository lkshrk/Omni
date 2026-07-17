package app_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/script"
)

func TestRefreshInstalled_ScriptVersionFailurePreservesCachedState(t *testing.T) {
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun-check", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun --version", Response: executor.MockCall{Err: context.DeadlineExceeded}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"bun": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Options: map[string]string{"install": "bun-install", "check": "bun-check", "version": "bun --version"},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("bun")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "bun", Provider: "script", Package: "bun", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "1.2.3", Valid: true}, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "bun", "script", "bun")
	if err != nil {
		t.Fatalf("Get bun: %v", err)
	}
	if !got.Installed || !got.Version.Valid || got.Version.String != "1.2.3" {
		t.Fatalf("cached state installed=%v version=%q; want true, %q", got.Installed, got.Version.String, "1.2.3")
	}
}

// bulkCheckingStub extends stubProvider with the BulkChecker interface.
type bulkCheckingStub struct {
	stubProvider
	bulk map[string]string // lowercase name → version
}

func (b *bulkCheckingStub) InstalledMap(_ context.Context) (map[string]string, error) {
	return b.bulk, nil
}

type metadataCheckingStub struct {
	stubProvider
	metadata map[string]provider.InstalledMetadata
}

func (b *metadataCheckingStub) InstalledMetadataMap(_ context.Context) (map[string]provider.InstalledMetadata, error) {
	return b.metadata, nil
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

func TestRefreshInstalled_CapturesConcreteProviderForEmptyProviderTool(t *testing.T) {
	prov := &metadataCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		metadata: map[string]provider.InstalledMetadata{
			"ripgrep": {
				Version: "14.1.0",
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
					URL:   "https://github.com/BurntSushi/ripgrep",
				},
			},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {}},
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["ripgrep"].Providers
	if len(providers) != 1 || providers[0].Provider != "brew" || providers[0].EffectivePackage("ripgrep") != "ripgrep" {
		t.Fatalf("providers = %+v, want [brew/ripgrep] captured from installed state", providers)
	}
	if got := cfg.Tools["ripgrep"].Git; got != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("git = %q, want backfilled from installed source", got)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || !tools[0].Installed || tools[0].Provider != "brew" {
		t.Fatalf("tools = %+v, want installed brew row", tools)
	}
}

func TestRefreshInstalled_CapturesViaIsInstalledFallbackWhenBulkMisses(t *testing.T) {
	// A configured empty-provider tool installed as a brew dependency/cask is not
	// in the bulk "leaves" map, but a per-tool IsInstalled probe finds it.
	prov := &isInstalledStub{
		stubProvider:  stubProvider{name: "brew", available: true},
		installedName: "cmake",
		installedVer:  "3.30.0",
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"cmake": {}},
		Groups: []*config.GroupConfig{{
			Tools: groupTools("cmake"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["cmake"].Providers
	if len(providers) != 1 || providers[0].Provider != "brew" {
		t.Fatalf("providers = %+v, want [brew] captured via IsInstalled fallback", providers)
	}
}

func TestRefreshInstalled_CapturesConcreteManagerNotFamilyForEcosystemTool(t *testing.T) {
	// A python-ecosystem tool installed via uv must be captured as the concrete
	// "uv" provider, never the "python" family (which fails config validation).
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "python", available: true},
		entries: map[string]provider.InstalledEntry{
			"flux-local": {Version: "1.2.3", ConcreteManager: "uv"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"flux-local": {}},
		Groups: []*config.GroupConfig{{
			Tools: groupTools("flux-local"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["flux-local"].Providers
	if len(providers) != 1 || providers[0].Provider != "uv" {
		t.Fatalf("providers = %+v, want concrete [uv], never python family", providers)
	}
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) != 0 {
		t.Fatalf("config validation errors after capture: %v", errs)
	}
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

func TestRefreshInstalled_MetadataBulkPath_PersistsPrivilege(t *testing.T) {
	prov := &metadataCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		metadata: map[string]provider.InstalledMetadata{
			"parsec": {
				Version: "150-103a",
				Privilege: provider.PrivilegePlan{
					Requirement: provider.PrivilegeMaybe,
					Reason:      "brew cask parsec uses pkgutil uninstall",
				},
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "parsec-cloud",
					Repo:  "parsec-app",
					URL:   "https://github.com/parsec-cloud/parsec-app",
				},
			},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("parsec", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("parsec"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	cached, err := a.DB().Get(context.Background(), "parsec", "brew", "parsec")
	if err != nil {
		t.Fatalf("Get parsec: %v", err)
	}
	if cached.Privilege != string(provider.PrivilegeMaybe) {
		t.Fatalf("Privilege = %q, want maybe", cached.Privilege)
	}
	if !cached.PrivilegeReason.Valid || !strings.Contains(cached.PrivilegeReason.String, "pkgutil") {
		t.Fatalf("PrivilegeReason = %+v, want pkgutil reason", cached.PrivilegeReason)
	}
	if cached.PrivilegeAt == nil {
		t.Fatal("PrivilegeAt should be set")
	}
	meta, err := a.DB().GetMetadata(context.Background(), "parsec", "brew", "parsec")
	if err != nil {
		t.Fatalf("GetMetadata parsec: %v", err)
	}
	if meta.SourceType != provider.SourceTypeGitHub || meta.SourceOwner != "parsec-cloud" || meta.SourceRepo != "parsec-app" {
		t.Fatalf("metadata source = %s/%s/%s, want github/parsec-cloud/parsec-app", meta.SourceType, meta.SourceOwner, meta.SourceRepo)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["parsec"].Git; got != "https://github.com/parsec-cloud/parsec-app" {
		t.Fatalf("tool git = %q, want GitHub source URL", got)
	}
}

func TestRefreshInstalled_MetadataBulkPath_DoesNotOverwriteDifferentGit(t *testing.T) {
	prov := &metadataCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		metadata: map[string]provider.InstalledMetadata{
			"rg": {
				Version: "14.1.1",
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
					URL:   "https://github.com/BurntSushi/ripgrep",
				},
			},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {Provider: "system", InstallWith: "brew", Git: "https://example.com/user-edited/rg"},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("rg")}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["rg"].Git; got != "https://example.com/user-edited/rg" {
		t.Fatalf("tool git = %q, want user-provided value preserved", got)
	}
}

func TestRefreshInstalled_CachedOwnerMetadataPopulatesGit(t *testing.T) {
	system := &stubProvider{name: "system", available: true}
	brew := &metadataCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		metadata: map[string]provider.InstalledMetadata{
			"ripgrep": {
				Version: "14.1.1",
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
					URL:   "https://github.com/BurntSushi/ripgrep",
				},
			},
		},
	}
	a, cfgPath := newImportApp(t, system, brew)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
	}); err != nil {
		t.Fatalf("seed cache owner: %v", err)
	}

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Tools["ripgrep"].Git; got != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("tool git = %q, want cached Brew owner GitHub URL", got)
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

	// Provider unavailable: the config-led row still shows (not vanished), but
	// it must be not-installed since nothing was upserted.
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if anyToolInstalled(tools) {
		t.Errorf("tools = %+v, want none installed (provider unavailable → skipped)", tools)
	}
}

func TestRefreshInstalled_EmptyConfig_Noop(t *testing.T) {
	prov := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, prov)

	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled on empty config: %v", err)
	}
}

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
	want := "Scanning brew… (1/1)"
	if msgs[len(msgs)-1] != want {
		t.Errorf("progress msgs = %v, want final entry %q", msgs, want)
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
	want := "Scanning pip… (1/1)"
	if msgs[len(msgs)-1] != want {
		t.Errorf("progress msgs = %v, want final entry %q", msgs, want)
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
	seen := map[string]bool{}
	seenIndexes := map[string]bool{}
	for _, msg := range msgs {
		switch {
		case strings.Contains(msg, "Scanning brew…"):
			seen["brew"] = true
		case strings.Contains(msg, "Scanning pip…"):
			seen["pip"] = true
		default:
			t.Errorf("unexpected progress message %q", msg)
		}
		switch {
		case strings.Contains(msg, "(1/2)"):
			seenIndexes["1"] = true
		case strings.Contains(msg, "(2/2)"):
			seenIndexes["2"] = true
		default:
			t.Errorf("progress message %q missing 1/2 or 2/2 index", msg)
		}
	}
	if !seen["brew"] || !seen["pip"] {
		t.Errorf("progress messages = %v, want brew and pip scans", msgs)
	}
	if !seenIndexes["1"] || !seenIndexes["2"] {
		t.Errorf("progress messages = %v, want 1/2 and 2/2 indexes", msgs)
	}
}

// multiManagerStub implements provider.MultiManagerBulkChecker, returning
// per-tool InstalledEntry values with distinct ConcreteManager attribution.
type multiManagerStub struct {
	stubProvider
	entries map[string]provider.InstalledEntry
}

func (s *multiManagerStub) InstalledByManager(_ context.Context) (map[string]provider.InstalledEntry, error) {
	return s.entries, nil
}

func TestRefreshInstalled_MultiManagerPath_UsesFullSlashPackage(t *testing.T) {
	prov := &multiManagerStub{
		stubProvider: stubProvider{name: "npm", available: true},
		entries: map[string]provider.InstalledEntry{
			"@playwright/test": {Version: "1.52.0", ConcreteManager: "npm"},
			"test":             {Version: "0.0.1", ConcreteManager: "npm"},
		},
	}
	a, cfgPath := newImportApp(t, prov)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalToolPackage("playwright-test", "npm", "@playwright/test"),
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

	got, err := a.DB().Get(context.Background(), "playwright-test", "npm", "@playwright/test")
	if err != nil {
		t.Fatalf("Get playwright-test: %v", err)
	}
	if !got.Installed || got.InstalledWith != "npm" || got.Version.String != "1.52.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/npm/1.52.0", got.Installed, got.InstalledWith, got.Version.String)
	}
}

func TestRefreshInstalled_UsesCachedConcreteOwnerWhenDifferentFromConfiguredProvider(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	apt := &bulkCheckingStub{
		stubProvider: stubProvider{name: "apt", available: true},
		bulk:         map[string]string{"ripgrep": "14.1.1"},
	}
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	seedRipgrepBrewWithCachedAptOwner(t, a, cfgPath, ctx)

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get ripgrep: %v", err)
	}
	if !got.Installed || got.InstalledWith != "apt" || got.Version.String != "14.1.1" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/apt/14.1.1", got.Installed, got.InstalledWith, got.Version.String)
	}
}

func TestRefreshInstalled_UsesCachedConcreteOwnerSlowPath(t *testing.T) {
	brew, apt := cachedOwnerSlowPathProviders()
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	seedRipgrepBrewWithCachedAptOwner(t, a, cfgPath, ctx)

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	assertRipgrepBrewCache(t, a, ctx, "apt", "14.1.2")
}

func TestRefreshInstalled_FallsBackWhenCachedOwnerUnavailable(t *testing.T) {
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{"ripgrep": "14.2.0"},
	}
	apt := &stubProvider{name: "apt", available: false}
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "apt",
	}); err != nil {
		t.Fatalf("seed cached owner: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	assertRipgrepBrewCache(t, a, ctx, "brew", "14.2.0")
}

func TestRefreshInstalled_UsesCachedConcreteOwnerMetadata(t *testing.T) {
	brew, apt := cachedOwnerMetadataProviders()
	a, cfgPath := newImportApp(t, brew, apt)
	ctx := context.Background()

	seedRipgrepBrewWithCachedAptOwner(t, a, cfgPath, ctx)

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	assertRipgrepBrewOwnedByAptMetadata(t, a, cfgPath, ctx)
}
