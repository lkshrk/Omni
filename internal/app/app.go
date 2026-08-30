// Package app wires dependencies and exposes the operations the CLI and TUI delegate to.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	_ "github.com/lkshrk/omni/internal/provider/all"
	"github.com/lkshrk/omni/internal/provider/node"
	"github.com/lkshrk/omni/internal/provider/python"
	systempkg "github.com/lkshrk/omni/internal/provider/system"
	isync "github.com/lkshrk/omni/internal/sync"
	"github.com/lkshrk/omni/internal/testguard"
)

type App struct {
	ConfigPath string // full path to settings.json
	CacheDir   string // where omni.db lives; derived from XDG_CACHE_HOME when empty
	StateDir   string // durable private state; derived from XDG_STATE_HOME when empty
	DBPath     string

	db           *database.DB
	registry     *provider.Registry
	fallbackExec executor.Executor
	githubAPI    string
	// Shared by every outbound HTTP caller here; tests inject one client for all of them.
	httpClient *http.Client
	testMode   bool

	// InitReadOnly marker: incidental writes like command traces are suppressed.
	diagnosticMode bool

	// Serialises read-modify-write cycles on settings.json; read-only loadConfig does not need it.
	configMu sync.Mutex
	// Guards the a.db pointer only; SQLite's own locking handles concurrent calls on the handle.
	dbMu sync.RWMutex
	// Prevents a provider scan captured before a lifecycle mutation from restoring stale installed rows afterward.
	installedStateMu sync.RWMutex

	// Serialises prependDotsHistory so concurrent tea.Cmd goroutines cannot lose entries.
	historyMu sync.Mutex

	githubReleaseMu       sync.Mutex
	githubReleaseInFlight map[githubReleaseRepo]*githubReleaseLookupFlight

	// Memoizes requirePinnedAPM and the PATH lookup behind APMAvailable per process; doctor's own
	// version check stays uncached, and installing apm resets both.
	// Context errors are never cached, so a cancelled caller doesn't wedge later callers.
	pinnedAPMMu    sync.Mutex
	pinnedAPMDone  bool
	pinnedAPMErr   error
	apmPresentDone bool
	apmPresent     bool

	// Built once in New; holds a back to App only through the narrow dotsHost seam.
	dotSvc *dotsService
}

func (a *App) requireSafeTestHomeForDots() error {
	return a.requireSafeTestDotsMutation("", nil)
}

func (a *App) requireSafeTestDotsMutation(repoPath string, entries []config.DotEntry) error {
	if !a.testMode {
		return nil
	}
	if err := testguard.RequireHome("dots mutation"); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("unsafe dots test setup: resolve HOME: %w", err)
	}
	if strings.TrimSpace(repoPath) != "" {
		if err := testguard.RequireTempPath("dots_repo", repoPath); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		target, err := dotsTestTargetPath(entry.Path)
		if err != nil {
			return err
		}
		if err := testguard.RequireTempEntryPath("dotfiles target", target); err != nil {
			return err
		}
		if !testguard.EntryPathInRoot(target, home) {
			return fmt.Errorf("unsafe dots test setup: target path %q for entry %q is outside test HOME=%q; refusing dots filesystem mutation in InitTestMode", target, entry.Name, home)
		}
	}
	return nil
}

func dotsTestTargetPath(path string) (string, error) {
	expanded, err := dots.ExpandPath(path)
	if err != nil {
		return "", fmt.Errorf("unsafe dots test setup: expand path %q: %w", path, err)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("unsafe dots test setup: target path %q: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

type SyncAllOptions struct {
	Discovered     []*ToolView
	DryRun         bool
	Group          string // target group for discovered tools (default: machine hostname)
	Progress       func(string)
	ToolProgress   func(isync.ProgressEvent)
	SkipPrivileged bool
	AllowWeak      bool
}

type SyncAllResult struct {
	SyncResult                  *isync.SyncResult
	ClaimedNames                []string
	NormalizedProviderOverrides []NormalizedInstallOverride
	Failures                    []BulkToolError
}

type SyncStateResult struct {
	Result *isync.SyncResult
	Tools  []*ToolView
	State  *ToolGroupState
}

type SyncAllStateResult struct {
	Result *SyncAllResult
	Tools  []*ToolView
	State  *ToolGroupState
}

type BulkToolError struct {
	Name        string
	Provider    string
	Message     string
	ActionError *provider.ActionError
}

type UpgradeAllResult struct {
	Upgraded    []string
	Quarantined []QuarantinedUpdate
	Failures    []BulkToolError
	// Not failures (e.g. a manager that cannot self-upgrade); the run still exits successfully.
	Skipped []BulkToolError
}

type UpgradeAllStateResult struct {
	Result *UpgradeAllResult
	Tools  []*ToolView
	State  *ToolGroupState
}

type UpgradeAllOptions struct {
	SkipPrivileged bool
	Force          bool
}

// New — Call Init or InitTestMode before any other method.
func New(configPath string, opts ...func(*App)) *App {
	a := &App{ConfigPath: configPath}
	for _, opt := range opts {
		opt(a)
	}
	a.dotSvc = newDotsService(a)
	return a
}

// Callers use the returned pointer directly; nil while uninitialised or mid-reset.
func (a *App) readDB() *database.DB {
	a.dbMu.RLock()
	db := a.db
	a.dbMu.RUnlock()
	return db
}

func (a *App) configDir() string {
	if a.ConfigPath == "" {
		return ""
	}
	return filepath.Dir(a.ConfigPath)
}

func (a *App) Init(ctx context.Context) error {
	if err := a.resolveStateDir(); err != nil {
		return err
	}
	if a.CacheDir == "" {
		cacheDir, err := config.DefaultCacheDir()
		if err != nil {
			return fmt.Errorf("resolving cache directory: %w", err)
		}
		a.CacheDir = cacheDir
	}
	a.DBPath = filepath.Join(a.CacheDir, "omni.db")

	a.backupConfigOnLaunch()
	if err := a.repairCurrentHostEntry(); err != nil {
		return fmt.Errorf("repairing current host entry: %w", err)
	}

	db, err := database.OpenContext(ctx, a.DBPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}
	a.db = db

	// Best-effort settings load — missing settings.json is fine (defaults apply).
	var settings config.Settings
	if cfg, err := config.Load(a.ConfigPath); err == nil {
		settings = a.effectiveSettings(cfg)
	}

	a.initProviderRegistry(settings)
	return nil
}

// InitReadOnly — For diagnostic commands that must report broken state without mutating it first.
func (a *App) InitReadOnly(ctx context.Context) error {
	if err := a.resolveStateDir(); err != nil {
		return err
	}
	if a.CacheDir == "" {
		cacheDir, err := config.DefaultCacheDir()
		if err != nil {
			return fmt.Errorf("resolving cache directory: %w", err)
		}
		a.CacheDir = cacheDir
	}
	a.DBPath = filepath.Join(a.CacheDir, "omni.db")
	a.diagnosticMode = true
	// A missing DB is left uncreated and an unopenable one degrades rather than blocking diagnostics.
	if _, err := os.Stat(a.DBPath); err == nil {
		if db, err := database.OpenContext(ctx, a.DBPath); err == nil {
			a.db = db
		}
	}

	var settings config.Settings
	if cfg, err := config.Load(a.ConfigPath); err == nil {
		settings = a.effectiveSettings(cfg)
	}
	a.initProviderRegistry(settings)
	return nil
}

func (a *App) resolveStateDir() error {
	if a.StateDir != "" {
		abs, err := filepath.Abs(a.StateDir)
		if err != nil {
			return fmt.Errorf("resolving state directory: %w", err)
		}
		if err := testguard.RequireTempPath("app state directory", abs); err != nil {
			return err
		}
		a.StateDir = abs
		return nil
	}
	stateDir, err := config.DefaultStateDir()
	if err != nil {
		return fmt.Errorf("resolving state directory: %w", err)
	}
	if !filepath.IsAbs(stateDir) {
		return errors.New("resolved state directory is not absolute")
	}
	a.StateDir = stateDir
	return nil
}

func (a *App) initProviderRegistry(settings config.Settings) {
	exec := a.newExecutor()
	a.fallbackExec = exec
	a.registry = provider.NewRegistry()

	// Concrete providers self-register via provider.RegisterConcrete; adding one needs no change here.
	concreteProviders := provider.BuildConcreteProviders(exec)
	for name, p := range concreteProviders {
		a.registry.RegisterWithMetadata(p, provider.BuiltinMetadata(name))
	}
	a.registry.Register(&githubReleaseAssetProvider{app: a, next: concreteProviders["script"]})

	a.registry.RegisterWithMetadata(provider.Named("bun", node.New(exec, "bun")), provider.BuiltinMetadata("bun"))
	a.registry.RegisterWithMetadata(provider.Named("pnpm", node.New(exec, "pnpm")), provider.BuiltinMetadata("pnpm"))
	a.registry.RegisterWithMetadata(provider.Named("npm", node.New(exec, "npm")), provider.BuiltinMetadata("npm"))
	a.registry.RegisterWithMetadata(provider.Named("uv", python.New(exec, "uv")), provider.BuiltinMetadata("uv"))

	disabledSet := make(map[string]bool, len(settings.DisabledProviders))
	for _, p := range settings.DisabledProviders {
		disabledSet[p] = true
	}
	if !disabledSet[provider.EcosystemNode] {
		a.registry.RegisterWithMetadata(node.New(exec, EffectiveEcosystemManager(settings, provider.EcosystemNode)), provider.BuiltinMetadata(provider.EcosystemNode))
	}
	if !disabledSet[provider.EcosystemPython] {
		a.registry.RegisterWithMetadata(python.New(exec, EffectiveEcosystemManager(settings, provider.EcosystemPython)), provider.BuiltinMetadata(provider.EcosystemPython))
	}
	// Native Linux PMs ordered before brew so distro-native packages win on Linux.
	if !disabledSet[provider.EcosystemSystem] {
		var delegates []provider.Provider
		for _, name := range provider.BuiltinSystemProviderPriorityNames() {
			if p, ok := concreteProviders[name]; ok {
				delegates = append(delegates, p)
			}
		}
		a.registry.RegisterWithMetadata(
			systempkg.New(delegates...),
			provider.BuiltinMetadata(provider.EcosystemSystem),
		)
	}
}

func (a *App) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *App) LoadConfig() (*config.RootConfig, error) {
	return a.loadConfig()
}

// Returns an empty RootConfig when the file does not exist; mutating callers must use withConfig.
func (a *App) loadConfig() (*config.RootConfig, error) {
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return nil, err
	}
	// Legacy mixed-case entries read as the canonical lower-cased hostname; a later write persists it.
	migrateCurrentHostCase(cfg, currentMachineGroupName())
	migrateLegacyEcosystemManager(&cfg.Settings)
	for host, hs := range cfg.HostSettings {
		migrateLegacyEcosystemManager(&hs)
		cfg.HostSettings[host] = hs
	}
	if a.registry != nil {
		if errs := fatalValidationErrors(config.ValidateRoot(cfg, a.providerValidation())); len(errs) > 0 {
			return nil, config.ValidationErrors(errs)
		}
	}
	return cfg, nil
}

// A fallback only matters when used and warn-level errors are advisory, so neither blocks load or save.
func fatalValidationErrors(errs []config.ValidationError) []config.ValidationError {
	fatal := make([]config.ValidationError, 0, len(errs))
	for _, e := range errs {
		if strings.Contains(e.Path, ".fallback") || e.Warn {
			continue
		}
		fatal = append(fatal, e)
	}
	return fatal
}

func (a *App) effectiveSettings(cfg *config.RootConfig) config.Settings {
	return cfg.EffectiveSettings(shortHostname(currentHostname()))
}

func (a *App) forEachAvailable(ctx context.Context, fn func(provider.Provider) error) error {
	for _, p := range a.registry.All() {
		if p.Name() == config.ProviderGitHubReleaseAsset {
			continue
		}
		avail, err := p.Available(ctx)
		if err != nil || !avail {
			continue
		}
		if err := fn(p); err != nil {
			return err
		}
	}
	return nil
}

// Errors from Available count as unavailable; use this when the caller needs the count up front too.
func (a *App) availableProviders(ctx context.Context) []provider.Provider {
	all := a.registry.All()
	if len(all) == 0 {
		return nil
	}
	avail := make([]bool, len(all))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range all {
		if p.Name() == config.ProviderGitHubReleaseAsset {
			continue
		}
		i, p := i, p
		g.Go(func() error {
			ok, err := p.Available(gctx)
			avail[i] = err == nil && ok
			return nil // never propagate; treat error as unavailable
		})
	}
	// Goroutines return nil unconditionally; Wait only fails on panic recovery.
	if err := g.Wait(); err != nil {
		return nil
	}
	out := make([]provider.Provider, 0, len(all))
	for i, p := range all {
		if p.Name() != config.ProviderGitHubReleaseAsset && avail[i] {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) providerValidation() config.ProviderValidation {
	registered := []string(nil)
	if a != nil && a.registry != nil {
		registered = a.registry.Names()
	}
	known := provider.MergeKnownNames(registered)
	ecosystems := provider.BuiltinEcosystemNames()
	concrete := provider.BuiltinConcreteEcosystems()
	if a != nil && a.registry != nil {
		for _, name := range a.registry.Names() {
			meta, ok := a.registry.Metadata(name)
			if !ok {
				continue
			}
			switch meta.Kind {
			case provider.ProviderKindEcosystem:
				if !slices.Contains(ecosystems, name) {
					ecosystems = append(ecosystems, name)
				}
			case provider.ProviderKindConcrete:
				if meta.Ecosystem != "" {
					concrete[name] = meta.Ecosystem
				}
			}
		}
	}
	return config.ProviderValidation{
		Known:              known,
		Ecosystems:         ecosystems,
		ConcreteEcosystems: concrete,
	}
}

const settingsBackupSuffix = ".bak"

func (a *App) backupConfigOnLaunch() {
	if a.ConfigPath == "" {
		return
	}
	data, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: omni: read %s for backup: %v\n", a.ConfigPath, err)
		}
		return
	}
	dst := a.ConfigPath + settingsBackupSuffix
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "settings-*.json.bak.tmp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: create backup temp: %v\n", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "warning: omni: write backup temp: %v\n", err)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "warning: omni: close backup temp: %v\n", err)
		return
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "warning: omni: rename backup: %v\n", err)
	}
}

// Aliased so a withConfig mutation can abort the write with the sentinel config.WriteConfig recognizes.
var errSkipSave = config.ErrSkipSave

// The lock and the app-specific load stay here; the include-safe write invariant lives behind the config seam.
func (a *App) withConfig(fn func(*config.RootConfig) error) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	var providers *config.ProviderValidation
	if a.registry != nil {
		pv := a.providerValidation()
		providers = &pv
	}
	return config.WriteConfig(a.ConfigPath, a.loadConfig, providers, fn)
}

// Root settings may include a tools fragment, so writing the merged config back would be overwritten on next load.
func (a *App) patchToolConfig(name string, mutate func(*config.ToolSpec) error) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	preview, ok := cfg.Tools[name]
	if !ok {
		return fmt.Errorf("logical tool %q not found", name)
	}
	if err := mutate(&preview); err != nil {
		return err
	}
	cfg.Tools[name] = preview
	if a.registry != nil {
		if errs := fatalValidationErrors(config.ValidateRoot(cfg, a.providerValidation())); len(errs) > 0 {
			return config.ValidationErrors(errs)
		}
	}
	return config.PatchTool(a.ConfigPath, name, mutate)
}

func findGroupInConfig(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, g := range cfg.Groups {
		if g.BaseName() == name {
			return g
		}
	}
	return nil
}

func ensureGroupInConfig(cfg *config.RootConfig, name string) *config.GroupConfig {
	if g := findGroupInConfig(cfg, name); g != nil {
		return g
	}
	g := &config.GroupConfig{Name: name}
	cfg.Groups = append(cfg.Groups, g)
	return g
}

func collectTaps(groups []*config.GroupConfig) []string {
	seen := make(map[string]struct{})
	var taps []string
	for _, g := range groups {
		for _, t := range g.Taps {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				taps = append(taps, t)
			}
		}
	}
	return taps
}

func findToolInGroups(groups []*config.GroupConfig, name, providerName string) (*config.GroupConfig, int) {
	for _, g := range groups {
		for i, e := range g.Tools {
			if e.Name != name {
				continue
			}
			if e.Provider == "" || providerName == "" || e.Provider == providerName {
				return g, i
			}
		}
	}
	return nil, -1
}

func filterGroups(groups []*config.GroupConfig, groupName string) []*config.GroupConfig {
	var out []*config.GroupConfig
	for _, g := range groups {
		if g.BaseName() == groupName {
			out = append(out, g)
		}
	}
	return out
}

func groupBaseNames(groups []*config.GroupConfig) []string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.BaseName()
	}
	return names
}

// Shares host settings' short-hostname normalization, but call sites use this when they mean group.
func machineGroupName(hostname string) string {
	return shortHostname(hostname)
}

func currentMachineGroupName() string {
	return machineGroupName(currentHostname())
}

func CurrentMachineGroupName() string {
	return currentMachineGroupName()
}

func compatibilityGroupName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return currentMachineGroupName()
	}
	return name
}

func ensureHostGroupInConfig(cfg *config.RootConfig, hostname string) (*config.GroupConfig, error) {
	groupName := machineGroupName(hostname)
	if groupName == "" {
		return nil, fmt.Errorf("hostname is required")
	}
	if group := findGroupInConfig(cfg, groupName); group != nil {
		if !group.IsHost() {
			return nil, fmt.Errorf("group %q already exists and is not a host group", groupName)
		}
		return group, nil
	}
	group := &config.GroupConfig{Name: groupName, Special: "host"}
	cfg.Groups = append(cfg.Groups, group)
	return group, nil
}

func ensureDestinationGroupInConfig(cfg *config.RootConfig, groupName string) (*config.GroupConfig, error) {
	groupName = compatibilityGroupName(groupName)
	if machineGroupName(groupName) == currentMachineGroupName() {
		group, err := ensureHostGroupInConfig(cfg, groupName)
		if err != nil {
			return nil, err
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		if _, ok := cfg.Hosts[group.BaseName()]; !ok {
			cfg.Hosts[group.BaseName()] = []string{}
		}
		return group, nil
	}
	return ensureGroupInConfig(cfg, groupName), nil
}

func activeHostGroupNames(cfg *config.RootConfig, hostname string) ([]string, bool) {
	hostname = machineGroupName(hostname)
	groups, ok := cfg.Hosts[hostname]
	out := make([]string, 0, len(groups)+1)
	out = append(out, hostname)
	out = append(out, groups...)
	return out, ok
}

func groupsByNames(groups []*config.GroupConfig, names []string) []*config.GroupConfig {
	byName := make(map[string]*config.GroupConfig, len(groups))
	for _, group := range groups {
		byName[group.BaseName()] = group
	}
	out := make([]*config.GroupConfig, 0, len(names))
	for _, name := range names {
		if group := byName[name]; group != nil {
			out = append(out, group)
		}
	}
	return out
}

func effectiveHostGroups(cfg *config.RootConfig, groups []*config.GroupConfig, hostname string) ([]*config.GroupConfig, []*config.GroupConfig, bool) {
	if hostname != "" {
		hostname = shortHostname(hostname)
	}
	names, ok := activeHostGroupNames(cfg, hostname)
	effective := groupsByNames(groups, names)
	return effective, effective, ok
}

// HostGroups — An empty hostname returns all groups.
func (a *App) HostGroups(ctx context.Context, hostname string) ([]*config.GroupConfig, error) {
	groups, err := a.Groups(ctx)
	if err != nil {
		return nil, err
	}
	if hostname == "" {
		return groups, nil
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	effective, _, ok := effectiveHostGroups(cfg, groups, hostname)
	if !ok {
		return nil, fmt.Errorf("host %q is not configured", hostname)
	}
	return effective, nil
}

// Declared here to avoid importing the brew package from the app layer.
type brewTapManager interface {
	ListTaps(ctx context.Context) ([]string, error)
	Tap(ctx context.Context, name string) error
	Trust(ctx context.Context, name string) error
}

// Homebrew 5.2+ hides untrusted-tap formulae, so every already-tapped repo is trusted before the scan.
func (a *App) syncTaps(ctx context.Context, taps []string, dryRun bool) error {
	brewProv, ok := a.registry.Get("brew")
	if !ok {
		return nil
	}
	bm, ok := brewProv.(brewTapManager)
	if !ok {
		return nil
	}
	// brew is always registered; skip where it is not installed so brew tap is never invoked there.
	if available, err := brewProv.Available(ctx); err != nil || !available {
		return nil
	}
	current, err := bm.ListTaps(ctx)
	if err != nil {
		return fmt.Errorf("listing brew taps: %w", err)
	}
	tapped := make(map[string]struct{}, len(current))
	for _, t := range current {
		tapped[t] = struct{}{}
	}
	if dryRun {
		return nil
	}
	// Config taps first so a tap missing from the machine is tapped before it is trusted.
	trustOrder := make([]string, 0, len(taps)+len(current))
	seen := make(map[string]struct{}, len(taps)+len(current))
	for _, tap := range append(append([]string(nil), taps...), current...) {
		if tap == "" {
			continue
		}
		if _, dup := seen[tap]; dup {
			continue
		}
		seen[tap] = struct{}{}
		trustOrder = append(trustOrder, tap)
	}
	if len(trustOrder) == 0 {
		return nil
	}
	// Each brew trust spawns a subprocess, so a trusted tap is recorded to skip the call next sync.
	trusted := map[string]bool{}
	if db := a.readDB(); db != nil {
		if t, err := db.TrustedTaps(ctx); err == nil {
			trusted = t
		}
	}
	// Caps each brew subprocess so one slow network response cannot stall the full sync.
	const tapOpTimeout = 60 * time.Second

	for _, tap := range trustOrder {
		if _, exists := tapped[tap]; !exists {
			tapCtx, tapCancel := context.WithTimeout(ctx, tapOpTimeout)
			tapErr := bm.Tap(tapCtx, tap)
			tapCancel()
			if tapErr != nil {
				return fmt.Errorf("tapping %s: %w", tap, tapErr)
			}
		}
		if trusted[tap] {
			continue
		}
		trustCtx, trustCancel := context.WithTimeout(ctx, tapOpTimeout)
		trustErr := bm.Trust(trustCtx, tap)
		trustCancel()
		if trustErr != nil {
			return fmt.Errorf("trusting tap %s: %w", tap, trustErr)
		}
		if db := a.readDB(); db != nil {
			if err := db.MarkTapTrusted(ctx, tap, time.Now()); err != nil {
				return fmt.Errorf("recording trusted tap %s: %w", tap, err)
			}
		}
	}
	return nil
}
