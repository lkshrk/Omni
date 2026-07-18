// Package app wires all dependencies and exposes high-level operations
// that CLI commands and the TUI delegate to.
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
	apkpkg "github.com/lkshrk/omni/internal/provider/apk"
	aptpkg "github.com/lkshrk/omni/internal/provider/apt"
	"github.com/lkshrk/omni/internal/provider/aptrepo"
	"github.com/lkshrk/omni/internal/provider/brew"
	dnfpkg "github.com/lkshrk/omni/internal/provider/dnf"
	"github.com/lkshrk/omni/internal/provider/node"
	pacmanpkg "github.com/lkshrk/omni/internal/provider/pacman"
	"github.com/lkshrk/omni/internal/provider/pip"
	"github.com/lkshrk/omni/internal/provider/python"
	"github.com/lkshrk/omni/internal/provider/script"
	systempkg "github.com/lkshrk/omni/internal/provider/system"
	zypppkg "github.com/lkshrk/omni/internal/provider/zypper"
	isync "github.com/lkshrk/omni/internal/sync"
	"github.com/lkshrk/omni/internal/testguard"
)

type App struct {
	ConfigPath string // full path to settings.json
	CacheDir   string // where omni.db lives; derived from XDG_CACHE_HOME when empty
	DBPath     string

	db                 *database.DB
	registry           *provider.Registry
	fallbackExec       executor.Executor
	githubAPI          string
	githubClient       *http.Client
	testMode           bool
	testMcpAdapters    []McpAdapter
	testPluginAdapters []PluginAdapter

	// configMu serialises all read-modify-write cycles on settings.json. Held
	// for the duration of withConfig; read-only loadConfig calls do not need it.
	configMu sync.Mutex

	// dbMu protects the a.db pointer itself (not the DB's internal operations,
	// which SQLite handles). Writers (ResetCache) hold an exclusive lock during
	// the Close → nil → Open → assign cycle. Readers call a.readDB() which holds
	// a shared lock long enough to copy the pointer; the copy is then used directly
	// because SQLite's own locking handles concurrent method calls.
	dbMu sync.RWMutex

	// historyMu serialises the read-modify-write cycle in prependDotsHistory
	// so concurrent tea.Cmd goroutines cannot lose history entries.
	historyMu sync.Mutex

	// githubReleaseMu coalesces concurrent provider refreshes for the same
	// repository without retaining results across refresh operations.
	githubReleaseMu       sync.Mutex
	githubReleaseInFlight map[githubReleaseRepo]*githubReleaseLookupFlight

	// dotSvc owns the App-layer dots orchestration (see dots_service.go). Built
	// once in New; holds a back to App only through the narrow dotsHost seam.
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
		if !testguard.PathInRoot(target, home) || !testguard.PathInTempRoot(target) {
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
	Discovered     []*database.ToolCache
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
	Tools  []*database.ToolCache
	State  *ToolGroupState
}

type SyncAllStateResult struct {
	Result *SyncAllResult
	Tools  []*database.ToolCache
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
	// Skipped lists tools that could not be upgraded but are not treated as
	// failures (e.g. a package manager that cannot self-upgrade in an externally
	// managed Python). The run still exits successfully.
	Skipped []BulkToolError
}

type UpgradeAllStateResult struct {
	Result *UpgradeAllResult
	Tools  []*database.ToolCache
	State  *ToolGroupState
}

type UpgradeAllOptions struct {
	SkipPrivileged bool
	Force          bool
}

// New creates an App targeting configPath (the full path to settings.json).
// Call Init or InitTestMode before any other method.
func New(configPath string, opts ...func(*App)) *App {
	a := &App{ConfigPath: configPath}
	for _, opt := range opts {
		opt(a)
	}
	a.dotSvc = newDotsService(a)
	return a
}

// readDB acquires a shared lock on dbMu, copies the db pointer, and releases
// the lock. Callers use the returned pointer directly; SQLite's own locking
// handles concurrent method calls on the same *database.DB handle.
// Returns nil if the database has not been initialised or is mid-reset.
func (a *App) readDB() *database.DB {
	a.dbMu.RLock()
	db := a.db
	a.dbMu.RUnlock()
	return db
}

// configDir returns the directory containing ConfigPath.
func (a *App) configDir() string {
	if a.ConfigPath == "" {
		return ""
	}
	return filepath.Dir(a.ConfigPath)
}

func (a *App) Init(ctx context.Context) error {
	if a.CacheDir == "" {
		cacheDir, err := config.DefaultCacheDir()
		if err != nil {
			return fmt.Errorf("resolving cache directory: %w", err)
		}
		a.CacheDir = cacheDir
	}
	a.DBPath = filepath.Join(a.CacheDir, "omni.db")

	if _, err := config.NormalizeFile(a.ConfigPath); err != nil {
		return fmt.Errorf("normalizing config file: %w", err)
	}
	a.backupConfigOnLaunch()
	if err := a.repairCurrentHostEntry(); err != nil {
		return fmt.Errorf("repairing current host entry: %w", err)
	}

	db, err := database.Open(a.DBPath)
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

// InitReadOnly prepares config/cache paths and provider metadata without
// normalizing settings.json, repairing host entries, or migrating the cache DB.
// It is intended for diagnostic commands that must report broken state without
// mutating it first.
func (a *App) InitReadOnly(_ context.Context) error {
	if a.CacheDir == "" {
		cacheDir, err := config.DefaultCacheDir()
		if err != nil {
			return fmt.Errorf("resolving cache directory: %w", err)
		}
		a.CacheDir = cacheDir
	}
	a.DBPath = filepath.Join(a.CacheDir, "omni.db")

	var settings config.Settings
	if cfg, err := config.Load(a.ConfigPath); err == nil {
		settings = a.effectiveSettings(cfg)
	}
	a.initProviderRegistry(settings)
	return nil
}

func (a *App) initProviderRegistry(settings config.Settings) {
	exec := a.newExecutor()
	a.fallbackExec = exec
	a.registry = provider.NewRegistry()

	// concrete providers — always registered regardless of disabled_providers.
	brewP := brew.New(exec)
	aptP := aptpkg.New(exec)
	apkP := apkpkg.New(exec)
	dnfP := dnfpkg.New(exec)
	pacmanP := pacmanpkg.New(exec)
	zypperP := zypppkg.New(exec)

	concreteProviders := map[string]provider.Provider{
		brewP.Name():   brewP,
		aptP.Name():    aptP,
		apkP.Name():    apkP,
		dnfP.Name():    dnfP,
		pacmanP.Name(): pacmanP,
		zypperP.Name(): zypperP,
	}
	for _, p := range concreteProviders {
		a.registry.RegisterWithMetadata(p, provider.BuiltinMetadata(p.Name()))
	}
	a.registry.RegisterWithMetadata(provider.Named("bun", node.New(exec, "bun")), provider.BuiltinMetadata("bun"))
	a.registry.RegisterWithMetadata(provider.Named("pnpm", node.New(exec, "pnpm")), provider.BuiltinMetadata("pnpm"))
	a.registry.RegisterWithMetadata(provider.Named("npm", node.New(exec, "npm")), provider.BuiltinMetadata("npm"))
	a.registry.RegisterWithMetadata(provider.Named("uv", python.New(exec, "uv")), provider.BuiltinMetadata("uv"))
	a.registry.RegisterWithMetadata(pip.New(exec), provider.BuiltinMetadata("pip"))
	a.registry.RegisterWithMetadata(script.New(exec), provider.BuiltinMetadata("script"))
	a.registry.RegisterWithMetadata(aptrepo.New(exec), provider.BuiltinMetadata("apt_repo"))

	// provider families — skipped when the user has disabled them on this machine.
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
	// system resolves to the first available concrete package manager on the host.
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

// ─── Internal config helpers ──────────────────────────────────────────────────

// LoadConfig exposes loadConfig to callers outside internal/app (TUI, CLI)
// that need the same host-key/ecosystem normalization and validation as the
// rest of App instead of reading settings.json directly.
func (a *App) LoadConfig() (*config.RootConfig, error) {
	return a.loadConfig()
}

// loadConfig reads settings.json. Returns an empty RootConfig when the file
// does not exist — callers treat this as "nothing configured yet". Read-only
// callers do not need configMu; mutating callers must use withConfig.
func (a *App) loadConfig() (*config.RootConfig, error) {
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return nil, err
	}
	// Present legacy mixed-case entries for this machine as the canonical
	// lower-cased hostname so every reader sees a consistent key; a later write
	// persists the normalised form to disk.
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

// fatalValidationErrors drops fallback-pathed and warn-level validation errors.
// A fallback only matters when actually used; warn-level errors are advisory.
// Neither should block loading or saving config. `omni doctor` reports the full set.
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

// forEachAvailable iterates registered providers, skipping any whose Available
// returns an error or false. fn is invoked on each survivor; the first non-nil
// fn error short-circuits and is returned.
func (a *App) forEachAvailable(ctx context.Context, fn func(provider.Provider) error) error {
	for _, p := range a.registry.All() {
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

// availableProviders runs Available concurrently across all registered
// providers and returns the subset that report available, in registry order.
// Errors from Available are treated as unavailable (matches forEachAvailable
// semantics). Use this when the caller needs both the count up front and the
// providers themselves (e.g. for x/y progress reporting in RefreshInstalled).
func (a *App) availableProviders(ctx context.Context) []provider.Provider {
	all := a.registry.All()
	if len(all) == 0 {
		return nil
	}
	avail := make([]bool, len(all))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range all {
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
		if avail[i] {
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

// errSkipSave aliases config.ErrSkipSave so a withConfig mutation can abort the
// write cleanly and config.WriteConfig recognizes the same sentinel.
var errSkipSave = config.ErrSkipSave

// withConfig acquires the config mutex, then delegates the actual load →
// mutate → validate → route → write to config.WriteConfig. The lock and the
// app-specific load (loadConfig, which applies host/legacy migrations) stay
// here; the include-safe write invariant lives behind the config seam.
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

// patchToolConfig edits a tool at the file that owns its effective definition.
// Root settings may include a tools fragment; writing the merged config back to
// the root would otherwise be overwritten on the next load.
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

// findGroupInConfig returns the first group whose BaseName matches name.
// Returns nil when not found.
func findGroupInConfig(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, g := range cfg.Groups {
		if g.BaseName() == name {
			return g
		}
	}
	return nil
}

// ensureGroupInConfig returns an existing group by name or appends and returns a new one.
func ensureGroupInConfig(cfg *config.RootConfig, name string) *config.GroupConfig {
	if g := findGroupInConfig(cfg, name); g != nil {
		return g
	}
	g := &config.GroupConfig{Name: name}
	cfg.Groups = append(cfg.Groups, g)
	return g
}

// collectTaps unions all taps across groups.
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

// findToolInGroups returns the GroupConfig and index of the first tool matching
// (name, providerName). Returns (nil, -1) if not found.
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

// filterGroups returns only groups whose BaseName matches groupName.
func filterGroups(groups []*config.GroupConfig, groupName string) []*config.GroupConfig {
	var out []*config.GroupConfig
	for _, g := range groups {
		if g.BaseName() == groupName {
			out = append(out, g)
		}
	}
	return out
}

// groupBaseNames extracts the BaseName of each group.
func groupBaseNames(groups []*config.GroupConfig) []string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.BaseName()
	}
	return names
}

// machineGroupName returns the config group name reserved for one machine's
// local inbox. It intentionally shares the short-hostname normalization used by
// host settings, but call sites use this helper when they mean "group".
func machineGroupName(hostname string) string {
	return shortHostname(hostname)
}

func currentMachineGroupName() string {
	return machineGroupName(currentHostname())
}

// CurrentMachineGroupName returns the current machine's config group name.
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

// HostGroups returns groups active for a host. When hostname is empty, all
// groups are returned.
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

// ─── Tap management ───────────────────────────────────────────────────────────

// brewTapManager is satisfied by the brew provider; declared here to avoid
// importing the brew package from the app layer.
type brewTapManager interface {
	ListTaps(ctx context.Context) ([]string, error)
	Tap(ctx context.Context, name string) error
	Trust(ctx context.Context, name string) error
}

// syncTaps ensures every tap declared across cfg taps is present and trusts
// every tap the machine is subscribed to. Homebrew 5.2+ hides untrusted-tap
// formulae from list/leaves/info, so an untrusted tap makes its installed tools
// look missing — omni would then try a bare `brew install` that is refused or
// silently resolves to homebrew/core. Trusting all currently-tapped repos
// before the scan breaks that chicken-and-egg: a tap the user already ran
// `brew tap` on is one they opted into, the same rationale used for config taps.
// Already-tapped repos are skipped; dry-run skips mutations.
func (a *App) syncTaps(ctx context.Context, taps []string, dryRun bool) error {
	brewProv, ok := a.registry.Get("brew")
	if !ok {
		return nil
	}
	bm, ok := brewProv.(brewTapManager)
	if !ok {
		return nil
	}
	// brew is always registered; skip on machines where it is not installed so
	// `brew tap` is never invoked there.
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
	// Trust the union of config-declared taps and every tap already present on
	// the machine. Order config taps first so a tap missing from the machine is
	// tapped before it is trusted.
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
	// Each `brew trust` spawns a subprocess; once a tap is trusted we record it in
	// the DB so subsequent syncs skip the call entirely (no brew invocation).
	trusted := map[string]bool{}
	if db := a.readDB(); db != nil {
		if t, err := db.TrustedTaps(ctx); err == nil {
			trusted = t
		}
	}
	// tapOpTimeout caps each individual brew tap/trust subprocess call so a
	// single slow network response cannot stall the full sync indefinitely.
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
