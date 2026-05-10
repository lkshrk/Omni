// Package app wires all dependencies and exposes high-level operations
// that CLI commands and the TUI delegate to.
package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/lkshrk/omni/internal/provider/brew"
	dnfpkg "github.com/lkshrk/omni/internal/provider/dnf"
	"github.com/lkshrk/omni/internal/provider/node"
	pacmanpkg "github.com/lkshrk/omni/internal/provider/pacman"
	"github.com/lkshrk/omni/internal/provider/pip"
	"github.com/lkshrk/omni/internal/provider/python"
	systempkg "github.com/lkshrk/omni/internal/provider/system"
	zypppkg "github.com/lkshrk/omni/internal/provider/zypper"
	isync "github.com/lkshrk/omni/internal/sync"
	"github.com/lkshrk/omni/internal/testguard"
)

type App struct {
	ConfigPath string // full path to settings.json
	CacheDir   string // where omni.db lives; derived from XDG_CACHE_HOME when empty
	DBPath     string

	db       *database.DB
	registry *provider.Registry
	testMode bool

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
	Progress       func(string)
	ToolProgress   func(isync.ProgressEvent)
	SkipPrivileged bool
}

type SyncAllResult struct {
	SyncResult                  *isync.SyncResult
	ClaimedNames                []string
	NormalizedProviderOverrides []NormalizedInstallOverride
	Failures                    []BulkToolError
}

type BulkToolError struct {
	Name        string
	Provider    string
	Message     string
	ActionError *provider.ActionError
}

type UpgradeAllResult struct {
	Upgraded []string
	Failures []BulkToolError
}

type UpgradeAllOptions struct {
	SkipPrivileged bool
}

// New creates an App targeting configPath (the full path to settings.json).
// Call Init or InitTestMode before any other method.
func New(configPath string) *App {
	return &App{ConfigPath: configPath}
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
	exec := executor.New()
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
	a.registry.RegisterWithMetadata(pip.New(exec), provider.BuiltinMetadata("pip"))

	// ecosystem providers — skipped when the user has disabled them on this machine.
	disabledSet := make(map[string]bool, len(settings.DisabledProviders))
	for _, p := range settings.DisabledProviders {
		disabledSet[p] = true
	}
	if !disabledSet[provider.EcosystemNode] {
		a.registry.RegisterWithMetadata(node.New(exec, settings.EcosystemManager(provider.EcosystemNode)), provider.BuiltinMetadata(provider.EcosystemNode))
	}
	if !disabledSet[provider.EcosystemPython] {
		a.registry.RegisterWithMetadata(python.New(exec, settings.EcosystemManager(provider.EcosystemPython)), provider.BuiltinMetadata(provider.EcosystemPython))
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

// loadConfig reads settings.json. Returns an empty RootConfig when the file
// does not exist — callers treat this as "nothing configured yet". Read-only
// callers do not need configMu; mutating callers must use withConfig.
func (a *App) loadConfig() (*config.RootConfig, error) {
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return nil, err
	}
	if a.registry != nil {
		if errs := config.ValidateRoot(cfg, a.providerValidation()); len(errs) > 0 {
			return nil, config.ValidationErrors(errs)
		}
	}
	return cfg, nil
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

var errSkipSave = errors.New("skip save")

func (a *App) withConfig(fn func(*config.RootConfig) error) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	before, err := topLevelKeys(cfg)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		if errors.Is(err, errSkipSave) {
			return nil
		}
		return err
	}
	if a.registry != nil {
		if errs := config.ValidateRoot(cfg, a.providerValidation()); len(errs) > 0 {
			return config.ValidationErrors(errs)
		}
	}
	after, err := topLevelKeys(cfg)
	if err != nil {
		return err
	}
	diff := make(map[string]json.RawMessage)
	for k, v := range after {
		if k == "$schema" {
			continue
		}
		if !bytes.Equal(before[k], v) {
			diff[k] = v
		}
	}
	for k := range before {
		if k == "$schema" {
			continue
		}
		if _, ok := after[k]; !ok {
			diff[k] = json.RawMessage(`null`)
		}
	}
	if len(diff) == 0 {
		return nil
	}
	if dir := a.configDir(); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}
	}
	return config.PatchRaw(a.ConfigPath, diff)
}

func topLevelKeys(cfg *config.RootConfig) (map[string]json.RawMessage, error) {
	type rootConfigPatchDoc struct {
		Schema       string                       `json:"$schema,omitempty"`
		Version      int                          `json:"version"`
		Settings     config.Settings              `json:"settings"`
		Tools        map[string]config.ToolSpec   `json:"tools,omitempty"`
		Hosts        map[string][]string          `json:"hosts,omitempty"`
		Ignore       config.GlobalIgnore          `json:"ignore,omitempty"`
		Groups       []*config.GroupConfig        `json:"groups,omitempty"`
		HostSettings map[string]hostSettingsPatch `json:"host_settings,omitempty"`
	}
	data, err := json.Marshal(rootConfigPatchDoc{
		Schema:       cfg.Schema,
		Version:      cfg.Version,
		Settings:     cfg.Settings,
		Tools:        cfg.Tools,
		Hosts:        cfg.Hosts,
		Ignore:       cfg.Ignore,
		Groups:       cfg.Groups,
		HostSettings: hostSettingsPatchDoc(cfg.HostSettings),
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
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
}

// syncTaps ensures every tap declared across cfg taps is present.
// Already-tapped repos are skipped; dry-run skips mutations.
func (a *App) syncTaps(ctx context.Context, taps []string, dryRun bool) error {
	if len(taps) == 0 {
		return nil
	}
	brewProv, ok := a.registry.Get("brew")
	if !ok {
		return nil
	}
	bm, ok := brewProv.(brewTapManager)
	if !ok {
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
	for _, tap := range taps {
		if _, exists := tapped[tap]; exists {
			continue
		}
		if dryRun {
			continue
		}
		if err := bm.Tap(ctx, tap); err != nil {
			return fmt.Errorf("tapping %s: %w", tap, err)
		}
	}
	return nil
}

// ─── Sync ─────────────────────────────────────────────────────────────────────

// Sync syncs taps first, then tools. When AutoImport is enabled it also runs
// Import so newly installed tools are captured in the config.
// opts.Group restricts the sync to one named group.
// When opts.Group is empty, the active host's special group plus assigned
// reusable groups are synced.
func (a *App) Sync(ctx context.Context, opts isync.SyncOptions) (*isync.SyncResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	groups := cfg.Groups

	var activeGroups []*config.GroupConfig

	switch {
	case opts.Group != "":
		groups = filterGroups(groups, opts.Group)
		if len(groups) == 0 {
			return nil, fmt.Errorf("group %q not found", opts.Group)
		}
	default:
		hostname := currentMachineGroupName()
		effective, active, ok := effectiveHostGroups(cfg, groups, hostname)
		if !ok {
			return nil, fmt.Errorf("no host configuration for %q - run 'omni bootstrap' to set one up", hostname)
		}
		groups = effective
		activeGroups = active
	}

	// Resolve once and derive both the entry list (for the syncer) and the
	// detailed view (for tap collection) from the same pass.
	resolvedDetailed, warnings := a.resolveTools(ctx, cfg, groups)
	resolvedTools := make([]config.ToolEntry, 0, len(resolvedDetailed))
	for _, t := range resolvedDetailed {
		resolvedTools = append(resolvedTools, t.entry)
	}
	taps := collectResolvedTaps(resolvedDetailed)
	if err = a.syncTaps(ctx, taps, opts.DryRun); err != nil {
		return nil, err
	}

	// Build a flat config view for the syncer; logical tools are deduplicated
	// by the resolver across group memberships.
	flatCfg := &config.Config{Tools: resolvedTools, Settings: cfg.Settings}

	opts.IgnoreList = cfg.Ignore.Tools

	// Construct syncer per call so Sync always sees the current *database.DB,
	// avoiding stale references after ResetCache rotates the connection.
	result, err := isync.New(a.registry, a.readDB()).Sync(ctx, flatCfg, opts)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	if !opts.DryRun {
		if opts.Group == "" {
			// Collect installed tools not covered by the active host and
			// append them to the machine group so nothing is lost.
			if err := a.syncOrphansToMachineGroup(ctx, activeGroups); err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("syncing orphans to machine group: %v", err))
			}

			// Report non-active groups that are now fully installed so the CLI
			// can prompt the user to add them to the host.
			activeNames := groupBaseNames(activeGroups)
			if satisfied, e := a.CheckSatisfiedGroups(ctx, activeNames); e == nil {
				result.SatisfiedGroups = satisfied
			}
		} else if cfg.Settings.AutoImport {
			if _, err := a.Import(ctx, ImportOptions{Group: currentMachineGroupName()}); err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("auto-importing machine group: %v", err))
			}
		}
	}
	return result, nil
}

func (a *App) SyncAll(ctx context.Context, opts SyncAllOptions) (*SyncAllResult, error) {
	discovered := opts.Discovered
	if discovered == nil {
		if opts.DryRun {
			var err error
			discovered, err = a.previewDiscovered(ctx)
			if err != nil {
				return nil, fmt.Errorf("previewing discovered tools: %w", err)
			}
		} else {
			if err := a.RefreshDiscovered(ctx); err != nil {
				return nil, fmt.Errorf("refreshing discovered tools: %w", err)
			}
			var err error
			discovered, err = a.ListDiscovered(ctx)
			if err != nil {
				return nil, fmt.Errorf("listing discovered tools: %w", err)
			}
		}
	}

	if !opts.DryRun {
		if err := a.EnsureHost(currentMachineGroupName()); err != nil {
			return nil, err
		}
	}
	var normalized []NormalizedInstallOverride
	if !opts.DryRun {
		var err error
		normalized, err = a.NormalizeHostDefaultInstallOverrides(ctx)
		if err != nil {
			return nil, fmt.Errorf("normalizing default provider overrides: %w", err)
		}
		if len(normalized) > 0 && opts.Progress != nil {
			opts.Progress(fmt.Sprintf("normalized %d default provider overrides…", len(normalized)))
		}
	}
	claimedNames, claimFailures, claimErr := a.claimDiscoveredTools(ctx, discovered, currentMachineGroupName(), opts)
	syncResult, syncErr := a.Sync(ctx, isync.SyncOptions{
		DryRun:         opts.DryRun,
		Progress:       opts.Progress,
		ToolProgress:   opts.ToolProgress,
		SkipPrivileged: opts.SkipPrivileged,
	})
	failures := append([]BulkToolError(nil), claimFailures...)
	if syncResult != nil {
		for _, op := range syncResult.Failed() {
			failures = append(failures, bulkToolErrorFromError(op.Tool.Name, op.Tool.Provider, op.Err))
		}
	}
	return &SyncAllResult{SyncResult: syncResult, ClaimedNames: claimedNames, NormalizedProviderOverrides: normalized, Failures: failures}, errors.Join(claimErr, syncErr)
}

func bulkToolErrorFromError(name, providerName string, err error) BulkToolError {
	failure := BulkToolError{Name: name, Provider: providerName}
	if err != nil {
		failure.Message = err.Error()
		if actionErr, ok := provider.ActionErrorFrom(err); ok {
			failure.ActionError = actionErr
		}
	}
	return failure
}

func (a *App) claimDiscoveredTools(ctx context.Context, discovered []*database.ToolCache, groupName string, opts SyncAllOptions) ([]string, []BulkToolError, error) {
	var claimed []string
	var failures []BulkToolError
	var errs []error
	resolvedEcosystems := a.ResolvedEcosystemProviders(ctx)
	for _, t := range discovered {
		if t == nil || !t.Installed || t.Name == "" || t.Provider == "" {
			continue
		}
		if opts.Progress != nil {
			if opts.DryRun {
				opts.Progress("would claim " + t.Name + "…")
			} else {
				opts.Progress("claiming " + t.Name + "…")
			}
		}
		configProvider := a.searchResultConfigProvider(t.Provider)
		installWith := configInstallWithForConcreteProvider(configProvider, t.InstalledWith, resolvedEcosystems)
		if installWith == "" && t.InstalledWith == "" {
			installWith = configInstallWithForConcreteProvider(configProvider, t.Provider, resolvedEcosystems)
		}
		tool := provider.Tool{Name: t.Name, Provider: configProvider, Package: t.Package}
		if tool.Package == "" {
			tool.Package = t.Name
		}
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Adding " + t.Name + " to config…"})
		}
		if opts.DryRun {
			claimed = append(claimed, t.Name)
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Would add " + t.Name + " to config", Done: true})
			}
			continue
		}
		if err := a.Add(ctx, configProvider, tool.Package, t.Name, groupName, installWith); err != nil {
			if opts.ToolProgress != nil {
				opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed adding " + t.Name + " to config", Err: err, Done: true})
			}
			failures = append(failures, bulkToolErrorFromError(t.Name, configProvider, err))
			errs = append(errs, fmt.Errorf("claim %s: %w", t.Name, err))
			continue
		}
		if opts.ToolProgress != nil {
			opts.ToolProgress(isync.ProgressEvent{Tool: tool, Message: "Added " + t.Name + " to config", Done: true})
		}
		claimed = append(claimed, t.Name)
	}
	return claimed, failures, errors.Join(errs...)
}

// ─── Install / Uninstall / Upgrade ───────────────────────────────────────────

// resolveProvider returns the first available provider from priority.
// Falls back to the registry/catalog install priority when priority is empty.
func (a *App) resolveProvider(ctx context.Context, priority []string) (string, error) {
	if len(priority) == 0 {
		if a.registry != nil {
			priority = a.registry.DefaultInstallProviderNames()
		}
		if len(priority) == 0 {
			priority = provider.BuiltinDefaultInstallProviderNames()
		}
	}
	for _, name := range priority {
		p, ok := a.registry.Get(name)
		if !ok {
			continue
		}
		avail, err := p.Available(ctx)
		if err != nil || !avail {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("no available provider found in priority list %v", priority)
}

// ResolveProvider returns the first available provider from priority,
// falling back to the built-in default order when priority is empty.
func (a *App) ResolveProvider(ctx context.Context, priority []string) (string, error) {
	return a.resolveProvider(ctx, priority)
}

func (a *App) Install(ctx context.Context, name, providerName string) error {
	if t, opProvider, ok, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
		return err
	} else if ok {
		prov, ok := a.registry.Get(opProvider)
		if !ok {
			return fmt.Errorf("unknown provider %q", opProvider)
		}
		avail, err := prov.Available(ctx)
		if err != nil {
			return err
		}
		if !avail {
			return fmt.Errorf("provider %q is not available on this system", opProvider)
		}
		tool := a.operationTool(t, opProvider)
		if err := installWithProvider(ctx, prov, tool, t.InstallWith); err != nil {
			a.recordPrivilegeError(ctx, t.Name, t.Provider, t.EffectivePackage(), err)
			return err
		}
		ver, err := verifyInstalledAfterInstall(ctx, prov, tool, t.InstallWith, opProvider)
		if err != nil {
			return err
		}
		installedWith := installedWithForOperation(ctx, prov, opProvider, t.InstallWith)
		return a.readDB().Upsert(ctx, &database.ToolCache{
			Name:          t.Name,
			Provider:      t.Provider,
			Package:       t.EffectivePackage(),
			Installed:     true,
			InstalledWith: installedWith,
			Version:       sql.NullString{String: ver, Valid: ver != ""},
			LastChecked:   time.Now(),
		})
	}

	if providerName == "" {
		settings, _ := a.LoadSettings()
		resolved, err := a.resolveProvider(ctx, settings.EcosystemPriority("system"))
		if err != nil {
			return err
		}
		providerName = resolved
	}
	prov, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	avail, err := prov.Available(ctx)
	if err != nil {
		return err
	}
	if !avail {
		return fmt.Errorf("provider %q is not available on this system", providerName)
	}
	t := provider.Tool{Name: name, Provider: providerName, Package: name}
	if err := prov.Install(ctx, t); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, name, err)
		return err
	}
	ver, err := verifyInstalledAfterInstall(ctx, prov, t, "", providerName)
	if err != nil {
		return err
	}
	installedWith := installedWithForOperation(ctx, prov, providerName, "")
	return a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       name,
		Installed:     true,
		InstalledWith: installedWith,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	})
}

func (a *App) configuredOperationTool(ctx context.Context, name, providerName string) (config.ToolEntry, string, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.ToolEntry{}, "", false, fmt.Errorf("loading config: %w", err)
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
	var matches []config.ToolEntry
	for _, t := range tools {
		if t.Name != name {
			continue
		}
		if providerName != "" && !a.installSpecMatchesProvider(ctx, config.ToolInstallSpec{Provider: t.Provider, InstallWith: t.InstallWith}, providerName) {
			continue
		}
		matches = append(matches, t)
	}
	if len(matches) == 0 {
		return config.ToolEntry{}, "", false, nil
	}
	if len(matches) > 1 && providerName == "" {
		return config.ToolEntry{}, "", false, fmt.Errorf("tool %q has multiple resolved providers; pass --provider", name)
	}
	t := matches[0]
	opProvider := a.operationProviderName(t)
	return t, opProvider, true, nil
}

func installedWithForOperation(ctx context.Context, prov provider.Provider, opProvider, installWith string) string {
	if installWith != "" {
		return installWith
	}
	if cr, ok := prov.(provider.ConcreteResolver); ok {
		if concrete, err := cr.ResolvedName(ctx); err == nil {
			return concrete
		}
		return ""
	}
	return opProvider
}

func verifyInstalledAfterInstall(ctx context.Context, prov provider.Provider, tool provider.Tool, installWith, opProvider string) (string, error) {
	installed, ver, err := installedWithProvider(ctx, prov, tool, installWith)
	if err != nil {
		return "", fmt.Errorf("checking installed status after install for %s/%s: %w", opProvider, tool.Name, err)
	}
	if !installed {
		return "", fmt.Errorf("install verification failed for %s/%s: not installed after install", opProvider, tool.Name)
	}
	return ver, nil
}

func (a *App) lifecycleProvider(providerName, installedWith string) (provider.Provider, string, string, bool) {
	if installedWith != "" {
		if owner, ok := a.registry.Get(installedWith); ok {
			return owner, installedWith, "", true
		}
	}
	prov, ok := a.registry.Get(providerName)
	return prov, providerName, installedWith, ok
}

func (a *App) Uninstall(ctx context.Context, name, providerName string) error {
	_, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	var pkg string
	installedWith := ""
	if configured, _, found, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
		return err
	} else if found {
		if providerName != "" && providerName != configured.Provider && configured.InstallWith == "" {
			installedWith = providerName
		}
		providerName = configured.Provider
		pkg = configured.EffectivePackage()
		if configured.InstallWith != "" {
			installedWith = configured.InstallWith
		}
	} else {
		var err error
		pkg, err = a.configuredPackageForTool(ctx, name, providerName)
		if err != nil {
			return err
		}
	}
	if cached, err := a.readDB().Get(ctx, name, providerName, pkg); err == nil {
		pkg = cached.Package
		if cached.InstalledWith != "" {
			installedWith = cached.InstalledWith
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load cached install owner for %s/%s: %w", providerName, name, err)
	}
	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	t := provider.Tool{Name: name, Provider: opProvider, Package: pkg}
	if err := uninstallWithProvider(ctx, prov, t, manager); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, pkg, err)
		return err
	}
	if err := a.removeToolFromConfig(name, providerName); err != nil {
		return err
	}
	return a.readDB().Delete(ctx, name, providerName, pkg)
}

// RemoveToolFromConfig removes a configured tool without calling a package
// manager. Use for tools that are configured but not installed locally.
func (a *App) RemoveToolFromConfig(ctx context.Context, name, providerName string) error {
	pkg, err := a.configuredPackageForTool(ctx, name, providerName)
	if err != nil {
		return err
	}
	if err := a.removeToolFromConfig(name, providerName); err != nil {
		return err
	}
	return a.readDB().Delete(ctx, name, providerName, pkg)
}

func (a *App) configuredPackageForTool(ctx context.Context, name, providerName string) (string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}
	tools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
	if tool, ok := resolvedToolByName(tools, name, providerName); ok {
		return tool.EffectivePackage(), nil
	}
	if _, idx := findToolInGroups(cfg.Groups, name, providerName); idx == -1 {
		return name, nil
	}
	if spec, ok := cfg.Tools[name]; ok {
		install := a.resolveInstallSpec(ctx, name, spec)
		return install.EffectivePackage(name), nil
	}
	return name, nil
}

func (a *App) removeToolFromConfig(name, providerName string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		changed := false
		for _, g := range cfg.Groups {
			filtered := g.Tools[:0]
			for _, tool := range g.Tools {
				if tool.Name == name && (tool.Provider == "" || providerName == "" || tool.Provider == providerName) {
					changed = true
					continue
				}
				filtered = append(filtered, tool)
			}
			g.Tools = filtered
		}
		if _, ok := cfg.Tools[name]; ok {
			delete(cfg.Tools, name)
			changed = true
		}
		if !changed {
			return errSkipSave
		}
		return nil
	})
}

// Upgrade upgrades a single tool in-place.
func (a *App) Upgrade(ctx context.Context, name, providerName string) error {
	_, ok := a.registry.Get(providerName)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}

	var pkg string
	installedWith := ""
	if configured, _, found, err := a.configuredOperationTool(ctx, name, providerName); err != nil {
		return err
	} else if found {
		if providerName != "" && providerName != configured.Provider && configured.InstallWith == "" {
			installedWith = providerName
		}
		providerName = configured.Provider
		pkg = configured.EffectivePackage()
		if configured.InstallWith != "" {
			installedWith = configured.InstallWith
		}
	} else {
		var err error
		pkg, err = a.configuredPackageForTool(ctx, name, providerName)
		if err != nil {
			return err
		}
	}
	cached, err := a.readDB().Get(ctx, name, providerName, pkg)
	if err == nil {
		pkg = cached.Package
		if cached.InstalledWith != "" {
			installedWith = cached.InstalledWith
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load cached install owner for %s/%s: %w", providerName, name, err)
	}

	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	t := provider.Tool{Name: name, Provider: opProvider, Package: pkg}
	if err := upgradeTool(ctx, prov, t, manager); err != nil {
		a.recordPrivilegeError(ctx, name, providerName, pkg, err)
		return err
	}
	installed, ver, err := isInstalledTool(ctx, prov, t, manager)
	if err != nil {
		return fmt.Errorf("verify %s after upgrade: %w", name, err)
	}
	if !installed {
		return fmt.Errorf("verify %s after upgrade: not installed", name)
	}
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedWithForLifecycle(opProvider, manager),
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return fmt.Errorf("update cache after upgrade: %w", err)
	}
	if err := a.readDB().UpdateOutdated(ctx, name, providerName, pkg, false, ""); err != nil {
		return fmt.Errorf("clear outdated after upgrade: %w", err)
	}
	return nil
}

func installedWithForLifecycle(opProvider, manager string) string {
	if manager != "" {
		return manager
	}
	return opProvider
}

func upgradeTool(ctx context.Context, prov provider.Provider, tool provider.Tool, installedWith string) error {
	if installedWith != "" && installedWith != tool.Provider {
		if up, ok := prov.(provider.ManagerUpgrader); ok {
			return up.UpgradeWithManager(ctx, tool, installedWith)
		}
	}
	return prov.Upgrade(ctx, tool)
}

func isInstalledTool(ctx context.Context, prov provider.Provider, tool provider.Tool, installedWith string) (bool, string, error) {
	if installedWith != "" && installedWith != tool.Provider {
		if checker, ok := prov.(provider.ManagerInstalledChecker); ok {
			return checker.IsInstalledWithManager(ctx, tool, installedWith)
		}
	}
	return prov.IsInstalled(ctx, tool)
}

// UpgradeAll upgrades every outdated tool in the DB.
// progress is called with a status string before each upgrade (may be nil).
// All upgrades are attempted; per-tool errors are joined and returned together.
func (a *App) UpgradeAll(ctx context.Context, progress func(string)) error {
	_, err := a.UpgradeAllDetailed(ctx, progress, nil)
	return err
}

func (a *App) UpgradeAllDetailed(ctx context.Context, progress func(string), toolProgress func(isync.ProgressEvent)) (*UpgradeAllResult, error) {
	return a.UpgradeAllDetailedWithOptions(ctx, progress, toolProgress, UpgradeAllOptions{})
}

func (a *App) UpgradeAllDetailedWithOptions(ctx context.Context, progress func(string), toolProgress func(isync.ProgressEvent), opts UpgradeAllOptions) (*UpgradeAllResult, error) {
	tools, err := a.readDB().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}
	result := &UpgradeAllResult{}
	var errs []error
	for _, t := range tools {
		if !t.Installed || !t.Outdated {
			continue
		}
		if progress != nil {
			progress("upgrading " + t.Name + "…")
		}
		tool := provider.Tool{Name: t.Name, Provider: t.Provider, Package: t.Package}
		if tool.Package == "" {
			tool.Package = t.Name
		}
		if toolProgress != nil {
			toolProgress(isync.ProgressEvent{Tool: tool, Message: "Upgrading " + t.Name + "…"})
		}
		if opts.SkipPrivileged {
			if plan, planErr := a.ToolPrivilegePlan(ctx, t, provider.PrivilegeActionUpgrade); planErr == nil && plan.RequiresPrivilege() {
				err := fmt.Errorf("requires sudo: %s", privilegeReason(plan))
				if toolProgress != nil {
					toolProgress(isync.ProgressEvent{Tool: tool, Message: "Admin approval needed for " + t.Name, Err: err, Done: true})
				}
				result.Failures = append(result.Failures, BulkToolError{
					Name:     t.Name,
					Provider: t.Provider,
					Message:  err.Error(),
				})
				if markErr := a.readDB().MarkPrivilegeRequired(ctx, t.Name, t.Provider, t.Package, string(plan.Requirement), plan.Reason); markErr != nil {
					err = fmt.Errorf("%w (failed to record privilege requirement: %v)", err, markErr)
				}
				errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
				continue
			}
		}
		if err := a.Upgrade(ctx, t.Name, t.Provider); err != nil {
			if toolProgress != nil {
				toolProgress(isync.ProgressEvent{Tool: tool, Message: "Failed upgrading " + t.Name, Err: err, Done: true})
			}
			result.Failures = append(result.Failures, bulkToolErrorFromError(t.Name, t.Provider, err))
			errs = append(errs, fmt.Errorf("%s: %w", t.Name, err))
			continue
		}
		if toolProgress != nil {
			toolProgress(isync.ProgressEvent{Tool: tool, Message: "Upgraded " + t.Name, Done: true})
		}
		result.Upgraded = append(result.Upgraded, t.Name)
	}
	return result, errors.Join(errs...)
}

// ─── Config helpers ───────────────────────────────────────────────────────────

// HasConfig reports whether settings.json exists.
func (a *App) HasConfig() bool {
	_, err := os.Stat(a.ConfigPath)
	return err == nil
}

// CreateEmptyConfig writes an empty settings.json (noop if already exists).
func (a *App) CreateEmptyConfig() error {
	if a.HasConfig() {
		return nil
	}
	return config.Save(a.ConfigPath, &config.RootConfig{})
}

// LoadTaps returns the union of all taps declared across all groups.
func (a *App) LoadTaps() ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return collectTaps(cfg.Groups), nil
}

// Groups returns all GroupConfigs from settings.json in display order.
func (a *App) Groups(_ context.Context) ([]*config.GroupConfig, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return append([]*config.GroupConfig(nil), cfg.Groups...), nil
}

// ─── Add ──────────────────────────────────────────────────────────────────────

// Add appends a tool to the named group (empty = current host group).
// For brew tap packages like "hashicorp/tap/terraform", the tap is auto-added.
func (a *App) Add(ctx context.Context, providerName, pkg, name, groupName, installWith string) error {
	if providerName == "" {
		return fmt.Errorf("provider is required")
	}
	if !a.knownProvider(providerName) {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	if !a.knownEcosystemProvider(providerName) {
		return fmt.Errorf("provider %q is not an ecosystem provider", providerName)
	}
	if err := a.validateInstallWith(providerName, installWith); err != nil {
		return err
	}

	if name == "" {
		name = pkg
	}
	if groupName == "" {
		groupName = currentMachineGroupName()
	}

	if err := a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, currentMachineGroupName()); err != nil {
			return err
		}
		for _, existing := range cfg.Groups {
			if existing.BaseName() != groupName {
				filterToolMemberships(existing, name)
			}
		}
		gc := ensureGroupInConfig(cfg, groupName)
		if cfg.Tools == nil {
			cfg.Tools = make(map[string]config.ToolSpec)
		}
		spec := cfg.Tools[name]
		spec.Provider = providerName
		spec.Package = pkg
		spec.InstallWith = installWith
		cfg.Tools[name] = spec
		if !containsToolMembership(gc.Tools, name) {
			gc.Tools = append(gc.Tools, config.ToolEntry{Name: name})
		}
		if a.providerSupportsTaps(providerName, installWith) {
			if tap := tapFromPackage(pkg); tap != "" && !slices.Contains(gc.Taps, tap) {
				spec := cfg.Tools[name]
				if !slices.Contains(spec.Taps, tap) {
					spec.Taps = append(spec.Taps, tap)
					cfg.Tools[name] = spec
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Promote any existing orphan DB row to config-tracked so the UI reflects
	// the claim immediately without waiting for the next full loadTools cycle.
	// No-op if the row doesn't exist yet.
	return a.readDB().MarkTracked(ctx, name, providerName, pkg)
}

func (a *App) providerSupportsTaps(providerName, installWith string) bool {
	if installWith != "" {
		if a.registry != nil {
			if meta, ok := a.registry.Metadata(installWith); ok && meta.SupportsTaps {
				return true
			}
		}
		return provider.BuiltinMetadata(installWith).SupportsTaps
	}
	if a.registry != nil {
		if meta, ok := a.registry.Metadata(providerName); ok && meta.SupportsTaps {
			return true
		}
	}
	if provider.BuiltinMetadata(providerName).SupportsTaps {
		return true
	}
	return false
}

// currentHostname returns the machine's hostname for host matching.
// OMNI_HOSTNAME overrides os.Hostname() — useful for tests and containers.
func currentHostname() string {
	if h := os.Getenv("OMNI_HOSTNAME"); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// shortHostname returns the first label of hostname (strips domain suffix).
// "macbook.corp.local" → "macbook", "macbook" → "macbook".
func shortHostname(hostname string) string {
	if hostname == "" {
		return "localhost"
	}
	if idx := strings.IndexByte(hostname, '.'); idx != -1 {
		return hostname[:idx]
	}
	return hostname
}

// tapFromPackage extracts "owner/repo" from a tap-qualified package path.
// "hashicorp/tap/terraform" → "hashicorp/tap", "git" → ""
func tapFromPackage(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) == 3 {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

func containsToolMembership(tools []config.ToolEntry, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
