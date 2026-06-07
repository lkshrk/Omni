// Package app wires all dependencies and exposes high-level operations
// that CLI commands and the TUI delegate to.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

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

	db           *database.DB
	registry     *provider.Registry
	fallbackExec executor.Executor
	githubAPI    string
	githubClient *http.Client
	testMode     bool

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

	// provider families — skipped when the user has disabled them on this machine.
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
