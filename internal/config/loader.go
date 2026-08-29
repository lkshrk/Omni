package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/testguard"
)

// Each call gets its own temp name so concurrent callers for the same path do not collide.
func atomicWrite(path string, data []byte) (retErr error) {
	if err := testguard.RequireTempPath("config write", path); err != nil {
		return err
	}
	// Every launch re-normalizes the config; renaming an identical result would churn mtime and downstream dotfile sync.
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	dir := filepath.Dir(path)
	// 0o700: settings.json holds host mappings and install pins other local users need not read.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	lock, err := AcquireWriteLock(path)
	if err != nil {
		return fmt.Errorf("creating temp file: acquire config lock: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	return atomicWriteUnlocked(path, data)
}

func atomicWriteUnlocked(path string, data []byte) (retErr error) {
	if err := testguard.RequireTempPath("config write", path); err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	writePath, err := resolveConfigWritePath(path)
	if err != nil {
		return err
	}
	if err := testguard.RequireTempPath("config write target", writePath); err != nil {
		return err
	}
	dir := filepath.Dir(writePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "settings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("removing config temp file: %w", err))
		}
	}()
	_, werr := f.Write(data)
	cerr := f.Close()
	closed = true
	if werr != nil {
		return errors.Join(fmt.Errorf("writing temp file: %w", werr), cerr)
	}
	if cerr != nil {
		return fmt.Errorf("closing temp file: %w", cerr)
	}
	if err := os.Rename(tmp, writePath); err != nil {
		return fmt.Errorf("renaming config file: %w", err)
	}
	return nil
}

func resolveConfigWritePath(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return path, nil
		}
		return "", fmt.Errorf("checking config file: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("reading config symlink: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(target), nil
	}
	return "", fmt.Errorf("resolving config symlink target: %w", err)
}

const schemaBaseURL = "https://raw.githubusercontent.com/lkshrk/omni/main/spec"

var SchemaURL = SchemaURLForVersion(CurrentVersion)

func SchemaURLForVersion(version int) string {
	return fmt.Sprintf("%s/omni.settings.v%d.schema.json", schemaBaseURL, version)
}

func DefaultConfigPath() (string, error) {
	if p := os.Getenv("OMNI_CONFIG"); p != "" {
		if err := testguard.RequireTempPath("OMNI_CONFIG", p); err != nil {
			return "", err
		}
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	path := filepath.Join(base, "omni", "settings.json")
	if err := testguard.RequireTempPath("default config path", path); err != nil {
		return "", err
	}
	return path, nil
}

// DefaultCacheDir — The SQLite database and other derived/transient state live here.
func DefaultCacheDir() (string, error) {
	if dir := os.Getenv("OMNI_CACHE_DIR"); dir != "" {
		if err := testguard.RequireTempPath("OMNI_CACHE_DIR", dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	path := filepath.Join(base, "omni")
	if err := testguard.RequireTempPath("default cache dir", path); err != nil {
		return "", err
	}
	return path, nil
}

// DefaultStateDir resolves durable Omni state independently from the cache and
// never falls back to the working directory.
func DefaultStateDir() (string, error) {
	if dir := os.Getenv("OMNI_STATE_DIR"); dir != "" {
		path, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		if err := testguard.RequireTempPath("OMNI_STATE_DIR", path); err != nil {
			return "", err
		}
		return path, nil
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	path, err := filepath.Abs(filepath.Join(base, "omni"))
	if err != nil {
		return "", err
	}
	if err := testguard.RequireTempPath("default state dir", path); err != nil {
		return "", err
	}
	return path, nil
}

// Load — Returns an empty RootConfig and no error when the file does not exist.
func Load(path string) (*RootConfig, error) {
	cfg, _, err := load(path, true)
	return cfg, err
}

func load(path string, normalize bool) (*RootConfig, bool, error) {
	if err := testguard.RequireTempPath("config read", path); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &RootConfig{Version: CurrentVersion}, false, nil
		}
		return nil, false, fmt.Errorf("reading config file: %w", err)
	}

	var cfg RootConfig
	if err := unmarshalJSONObject(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parsing config file %q: %w", path, err)
	}
	if err := loadIncludes(path, &cfg); err != nil {
		return nil, false, err
	}
	migrated, err := Migrate(&cfg)
	if err != nil {
		return nil, false, err
	}
	if normalize {
		Normalize(&cfg)
	}
	return &cfg, migrated, nil
}

// Migrate — Future versions are rejected so older binaries never rewrite configs they cannot understand.
func Migrate(cfg *RootConfig) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	if cfg.Version > CurrentVersion || cfg.Version < 0 {
		return false, unsupportedVersionError(cfg.Version)
	}
	migrated := false
	for cfg.Version < CurrentVersion {
		step, ok := configMigrationFrom(cfg.Version)
		if !ok {
			return false, missingMigrationError(cfg.Version)
		}
		from := cfg.Version
		if step.apply == nil {
			return false, fmt.Errorf("config migration from version %d to %d has no typed migration", step.from, step.to)
		}
		if err := step.apply(cfg); err != nil {
			return false, fmt.Errorf("migrating config from version %d to %d: %w", step.from, step.to, err)
		}
		if cfg.Version != step.to {
			return false, fmt.Errorf("config migration from version %d to %d set version %d", step.from, step.to, cfg.Version)
		}
		if cfg.Version <= from {
			return false, fmt.Errorf("config migration from version %d to %d did not advance version", step.from, step.to)
		}
		migrated = true
	}
	return migrated, nil
}

func unsupportedVersionError(version int) error {
	if version > CurrentVersion {
		return fmt.Errorf("config version %d is newer than supported version %d", version, CurrentVersion)
	}
	return fmt.Errorf("config version %d is not supported; supported version is %d", version, CurrentVersion)
}

func missingMigrationError(version int) error {
	return fmt.Errorf("missing config migration from version %d to %d", version, version+1)
}

func removedAgentConfigFieldError(path string) error {
	return fmt.Errorf("config field %q was removed in v24; declare agent packages and runtime state in ~/.apm/apm.yml", path)
}

type configMigration struct {
	from     int
	to       int
	apply    func(*RootConfig) error
	applyRaw func(map[string]json.RawMessage) error
}

var configMigrations = []configMigration{
	{from: 0, to: 1, apply: migrateConfigV0ToV1, applyRaw: migrateRawConfigV0ToV1},
	{from: 1, to: 2, apply: migrateConfigV1ToV2, applyRaw: migrateRawConfigV1ToV2},
	{from: 2, to: 3, apply: migrateConfigV2ToV3, applyRaw: migrateRawConfigV2ToV3},
	{from: 3, to: 4, apply: migrateConfigV3ToV4, applyRaw: migrateRawConfigV3ToV4},
	{from: 4, to: 5, apply: migrateConfigV4ToV5, applyRaw: migrateRawConfigV4ToV5},
	{from: 5, to: 6, apply: migrateConfigV5ToV6, applyRaw: migrateRawConfigV5ToV6},
	{from: 6, to: 7, apply: migrateConfigV6ToV7, applyRaw: migrateRawConfigV6ToV7},
	{from: 7, to: 8, apply: migrateConfigV7ToV8, applyRaw: migrateRawConfigV7ToV8},
	{from: 8, to: 9, apply: migrateConfigV8ToV9, applyRaw: migrateRawConfigV8ToV9},
	{from: 9, to: 10, apply: migrateConfigV9ToV10, applyRaw: migrateRawConfigV9ToV10},
	{from: 10, to: 11, apply: migrateConfigV10ToV11, applyRaw: migrateRawConfigV10ToV11},
	{from: 11, to: 12, apply: migrateConfigV11ToV12, applyRaw: migrateRawConfigV11ToV12},
	{from: 12, to: 13, apply: migrateConfigV12ToV13, applyRaw: migrateRawConfigV12ToV13},
	{from: 13, to: 14, apply: migrateConfigV13ToV14, applyRaw: migrateRawConfigV13ToV14},
	{from: 14, to: 15, apply: migrateConfigV14ToV15, applyRaw: migrateRawConfigV14ToV15},
	{from: 15, to: 16, apply: migrateConfigV15ToV16, applyRaw: migrateRawConfigV15ToV16},
	{from: 16, to: 17, apply: migrateConfigV16ToV17, applyRaw: migrateRawConfigV16ToV17},
	{from: 17, to: 18, apply: migrateConfigV17ToV18, applyRaw: migrateRawConfigV17ToV18},
	{from: 18, to: 19, apply: migrateConfigV18ToV19, applyRaw: migrateRawConfigV18ToV19},
	{from: 19, to: 20, apply: migrateConfigV19ToV20, applyRaw: migrateRawConfigV19ToV20},
	{from: 20, to: 21, apply: migrateConfigV20ToV21, applyRaw: migrateRawConfigV20ToV21},
	{from: 21, to: 22, apply: migrateConfigV21ToV22, applyRaw: migrateRawConfigV21ToV22},
	{from: 22, to: 23, apply: migrateConfigV22ToV23, applyRaw: migrateRawConfigV22ToV23},
	{from: 23, to: 24, apply: migrateConfigV23ToV24, applyRaw: migrateRawConfigV23ToV24},
}

func configMigrationFrom(version int) (configMigration, bool) {
	for _, step := range configMigrations {
		if step.from == version {
			return step, true
		}
	}
	return configMigration{}, false
}

func migrateConfigV0ToV1(cfg *RootConfig) error {
	cfg.Version = 1
	return nil
}

func migrateRawConfigV0ToV1(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`1`)
	return nil
}

func migrateConfigV1ToV2(cfg *RootConfig) error {
	cfg.Version = 2
	return nil
}

func migrateRawConfigV1ToV2(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`2`)
	return nil
}

func migrateConfigV2ToV3(cfg *RootConfig) error {
	cfg.Version = 3
	return nil
}

func migrateRawConfigV2ToV3(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`3`)
	return nil
}

func migrateConfigV3ToV4(cfg *RootConfig) error {
	cfg.Version = 4
	return nil
}

func migrateRawConfigV3ToV4(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`4`)
	return nil
}

func migrateConfigV4ToV5(cfg *RootConfig) error {
	cfg.Version = 5
	return nil
}

func migrateRawConfigV4ToV5(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`5`)
	return nil
}

func migrateConfigV5ToV6(cfg *RootConfig) error {
	for name, spec := range cfg.Tools {
		spec.Providers = migrateToolProviders(spec)
		spec.Provider = ""
		spec.Package = ""
		spec.InstallWith = ""
		spec.Options = nil
		spec.Variants = nil
		spec.Hosts = nil
		cfg.Tools[name] = spec
	}
	cfg.Settings.Ecosystems = nil
	for host, settings := range cfg.HostSettings {
		settings.Ecosystems = nil
		cfg.HostSettings[host] = settings
	}
	cfg.Version = 6
	return nil
}

func migrateRawConfigV5ToV6(raw map[string]json.RawMessage) error {
	unknown := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		unknown[key] = value
	}
	var cfg RootConfig
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if err := migrateConfigV5ToV6(&cfg); err != nil {
		return err
	}
	next, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var nextRaw map[string]json.RawMessage
	if err := json.Unmarshal(next, &nextRaw); err != nil {
		return err
	}
	for key := range raw {
		delete(raw, key)
	}
	for key, value := range unknown {
		raw[key] = value
	}
	for key, value := range nextRaw {
		raw[key] = value
	}
	return nil
}

func migrateConfigV6ToV7(cfg *RootConfig) error {
	cfg.Version = 7
	return nil
}

func migrateRawConfigV6ToV7(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`7`)
	return nil
}

func migrateConfigV7ToV8(cfg *RootConfig) error {
	cfg.Version = 8
	return nil
}

func migrateRawConfigV7ToV8(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`8`)
	return nil
}

func migrateToolProviders(spec ToolSpec) []ToolInstallSpec {
	out := make([]ToolInstallSpec, 0, 1+len(spec.Variants))
	add := func(candidate ToolInstallSpec) {
		candidate.Provider = concreteLegacyProvider(candidate.Provider, candidate.InstallWith)
		candidate.InstallWith = ""
		if candidate.Provider == "" {
			return
		}
		for _, existing := range out {
			if existing.Provider == candidate.Provider && existing.EffectivePackage("") == candidate.EffectivePackage("") {
				return
			}
		}
		out = append(out, candidate)
	}
	add(ToolInstallSpec{
		Provider:    spec.Provider,
		Package:     spec.Package,
		InstallWith: spec.InstallWith,
		Options:     spec.Options,
	})
	for _, variant := range spec.Variants {
		add(variant)
	}
	return out
}

func concreteLegacyProvider(provider, installWith string) string {
	if normalized := normalizeConcreteProvider(installWith); normalized != "" {
		return normalized
	}
	switch provider {
	case "system", "node", "python":
		return ""
	default:
		return normalizeConcreteProvider(provider)
	}
}

func normalizeConcreteProvider(provider string) string {
	switch provider {
	case "", "system", "node", "python":
		return ""
	case "pip3":
		return "pip"
	default:
		return provider
	}
}

// NormalizeConcreteProvider — Meta-provider families and empty strings canonicalize to ""; "pip3" collapses to "pip"; other names pass through.
func NormalizeConcreteProvider(provider string) string {
	return normalizeConcreteProvider(provider)
}

func Normalize(cfg *RootConfig) bool {
	if cfg == nil {
		return false
	}
	changed := false
	if !groupsSorted(cfg.Groups) {
		sort.SliceStable(cfg.Groups, func(i, j int) bool {
			return groupSortKey(cfg.Groups[i]) < groupSortKey(cfg.Groups[j])
		})
		changed = true
	}
	for host, groups := range cfg.Hosts {
		if !sort.StringsAreSorted(groups) {
			sort.Strings(groups)
			cfg.Hosts[host] = groups
			changed = true
		}
	}
	return changed
}

// NormalizeFile — An explicit repair action, never a load step: calling it on a read path makes reporting commands rewrite the config they report on.
func NormalizeFile(path string) (bool, error) {
	cfg, migrated, err := load(path, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	normalized := Normalize(cfg)
	if !migrated && !normalized {
		return false, nil
	}
	type orderPatch struct {
		Version int                 `json:"version"`
		Hosts   map[string][]string `json:"hosts,omitempty"`
		Groups  []*GroupConfig      `json:"groups,omitempty"`
	}
	if err := PatchRouted(path, orderPatch{Version: cfg.Version, Hosts: cfg.Hosts, Groups: cfg.Groups}); err != nil {
		return false, err
	}
	return true, nil
}

func groupsSorted(groups []*GroupConfig) bool {
	return sort.SliceIsSorted(groups, func(i, j int) bool {
		return groupSortKey(groups[i]) < groupSortKey(groups[j])
	})
}

func groupSortKey(g *GroupConfig) string {
	if g != nil && g.IsHost() {
		return "1:" + groupBaseName(g)
	}
	return "0:" + groupBaseName(g)
}

func groupBaseName(g *GroupConfig) string {
	if g == nil {
		return ""
	}
	return g.BaseName()
}

// Patch — Merges top-level keys only (a JSON null value deletes a key) and is include-blind, so keys owned by an $include fragment get duplicated into path — prefer WriteConfig or PatchRouted for real edits.
func Patch(path string, patch interface{}) (retErr error) {
	lock, err := AcquireWriteLock(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	return patchUnlocked(path, patch)
}

func patchUnlocked(path string, patch interface{}) error {
	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encoding patch: %w", err)
	}
	var patchMap map[string]json.RawMessage
	if err := unmarshalJSONObject(patchData, &patchMap); err != nil {
		return fmt.Errorf("parsing patch: %w", err)
	}
	return patchRawUnlocked(path, patchMap)
}

// PatchRouted — Routed form of Patch: each top-level key is written to the file that owns it across the $include chain, so fragments are not resurrected into the parent.
func PatchRouted(path string, patch interface{}) error {
	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encoding patch: %w", err)
	}
	var patchMap map[string]json.RawMessage
	if err := unmarshalJSONObject(patchData, &patchMap); err != nil {
		return fmt.Errorf("parsing patch: %w", err)
	}
	return PatchRawRouted(path, patchMap)
}

// PatchRaw — Lower-level Patch taking an already-decoded key map; a JSON null value deletes that key.
func PatchRaw(path string, patch map[string]json.RawMessage) (retErr error) {
	lock, err := AcquireWriteLock(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	return patchRawUnlocked(path, patch)
}

func patchRawUnlocked(path string, patch map[string]json.RawMessage) error {
	if err := testguard.RequireTempPath("config patch", path); err != nil {
		return err
	}
	raw := make(map[string]json.RawMessage)
	exists := false
	if data, err := os.ReadFile(path); err == nil {
		exists = true
		if err := unmarshalJSONObject(data, &raw); err != nil {
			return fmt.Errorf("parsing existing config %q: %w", path, err)
		}
		if err := migrateRawVersion(raw); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading config file: %w", err)
	}

	for k, v := range patch {
		if isJSONNull(v) {
			delete(raw, k)
			continue
		}
		raw[k] = v
	}
	if !exists {
		raw["version"] = json.RawMessage(fmt.Sprintf("%d", CurrentVersion))
	}
	delete(raw, "$schema")
	if err := migrateRawVersion(raw); err != nil {
		return err
	}
	versionRaw := raw["version"]
	delete(raw, "version")

	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("{\n  \"$schema\": \"")
	buf.WriteString(SchemaURL)
	buf.WriteString("\"")
	buf.WriteString(",\n  \"version\": ")
	buf.Write(versionRaw)
	for _, k := range keys {
		buf.WriteString(",\n  ")
		kJSON, _ := json.Marshal(k)
		buf.Write(kJSON)
		buf.WriteString(": ")
		var indented bytes.Buffer
		if indentErr := json.Indent(&indented, raw[k], "  ", "  "); indentErr == nil {
			buf.Write(indented.Bytes())
		} else {
			buf.Write(raw[k])
		}
	}
	buf.WriteString("\n}\n")
	return atomicWriteUnlocked(path, buf.Bytes())
}

// ToolSource — Included files merge after their parent, so the last matching definition is the effective one.
func ToolSource(path, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("tool name is required")
	}
	var stack includePathStack
	source, found, err := toolSource(path, name, &stack)
	if err != nil {
		return "", err
	}
	if !found {
		return path, nil
	}
	return source, nil
}

func toolSource(path, name string, stack *includePathStack) (string, bool, error) {
	if err := testguard.RequireTempPath("config source read", path); err != nil {
		return "", false, err
	}
	if err := stack.push(path); err != nil {
		return "", false, err
	}
	defer stack.pop()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("reading config file: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := unmarshalJSONObject(data, &raw); err != nil {
		return "", false, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	var source string
	found := false
	var tools map[string]json.RawMessage
	if err := json.Unmarshal(raw["tools"], &tools); err == nil && tools[name] != nil {
		source = path
		found = true
	}
	var includes []string
	if includeRaw := raw["$include"]; len(includeRaw) > 0 {
		if err := json.Unmarshal(includeRaw, &includes); err != nil {
			return "", false, fmt.Errorf("parsing config includes: %w", err)
		}
	}
	for _, include := range includes {
		include = strings.TrimSpace(include)
		if include == "" {
			continue
		}
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(includeBaseDir(path), includePath)
		}
		includedSource, included, err := toolSource(includePath, name, stack)
		if err != nil {
			return "", false, err
		}
		if included {
			source = includedSource
			found = true
		}
	}
	return source, found, nil
}

// PatchTool — Writes into the file that owns the tool, preserving sibling definitions and include layout.
func PatchTool(path, name string, mutate func(*ToolSpec) error) (retErr error) {
	lock, err := AcquireWriteLock(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	source, err := ToolSource(path, name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := unmarshalJSONObject(data, &raw); err != nil {
		return fmt.Errorf("parsing config file %q: %w", source, err)
	}
	tools := make(map[string]ToolSpec)
	if toolsRaw := raw["tools"]; len(toolsRaw) > 0 {
		if err := json.Unmarshal(toolsRaw, &tools); err != nil {
			return fmt.Errorf("parsing tools: %w", err)
		}
	}
	spec := tools[name]
	if err := mutate(&spec); err != nil {
		return err
	}
	tools[name] = spec
	return patchUnlocked(source, struct {
		Tools map[string]ToolSpec `json:"tools"`
	}{Tools: tools})
}

func migrateRawVersion(raw map[string]json.RawMessage) error {
	if raw == nil {
		return nil
	}
	version, err := rawConfigVersion(raw)
	if err != nil {
		return err
	}
	if version > CurrentVersion || version < 0 {
		return unsupportedVersionError(version)
	}
	for version < CurrentVersion {
		step, ok := configMigrationFrom(version)
		if !ok {
			return missingMigrationError(version)
		}
		if step.applyRaw == nil {
			return fmt.Errorf("config migration from version %d to %d has no raw migration", step.from, step.to)
		}
		if err := step.applyRaw(raw); err != nil {
			return fmt.Errorf("migrating raw config from version %d to %d: %w", step.from, step.to, err)
		}
		nextVersion, err := rawConfigVersion(raw)
		if err != nil {
			return err
		}
		if nextVersion != step.to {
			return fmt.Errorf("raw config migration from version %d to %d set version %d", step.from, step.to, nextVersion)
		}
		if nextVersion <= version {
			return fmt.Errorf("raw config migration from version %d to %d did not advance version", step.from, step.to)
		}
		version = nextVersion
	}
	return nil
}

func rawConfigVersion(raw map[string]json.RawMessage) (int, error) {
	versionRaw, ok := raw["version"]
	if !ok || isJSONNull(versionRaw) {
		return 0, nil
	}
	var version int
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return 0, fmt.Errorf("parsing config version: %w", err)
	}
	return version, nil
}

func isJSONNull(v json.RawMessage) bool {
	trimmed := bytes.TrimSpace(v)
	return len(trimmed) == 4 && string(trimmed) == "null"
}

func unmarshalJSONObject(data []byte, dst any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("top-level value must be a JSON object")
	}
	return json.Unmarshal(data, dst)
}

// Save — Whole-file, include-blind replace: use it only to materialize a fresh file (init) or overwrite one wholesale (import), never to edit settings in place.
func Save(path string, cfg *RootConfig) error {
	// Stamp schema URL without mutating the caller's struct.
	stamped := normalizedCopy(cfg)
	if stamped.Version == 0 && rootUsesCurrentProviderLists(stamped) {
		stamped.Version = CurrentVersion
	}
	if _, err := Migrate(&stamped); err != nil {
		return err
	}
	stamped.Schema = SchemaURL
	data, err := json.MarshalIndent(stamped, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(path, data)
}

func rootUsesCurrentProviderLists(cfg RootConfig) bool {
	for _, spec := range cfg.Tools {
		if len(spec.Providers) > 0 {
			return true
		}
	}
	if len(cfg.Settings.ProviderPriority) > 0 {
		return true
	}
	for _, settings := range cfg.HostSettings {
		if len(settings.ProviderPriority) > 0 {
			return true
		}
	}
	return false
}

func normalizedCopy(cfg *RootConfig) RootConfig {
	if cfg == nil {
		return RootConfig{Version: CurrentVersion}
	}
	out := *cfg
	out.Include = nil
	out.Settings = cloneSettings(cfg.Settings)
	out.Groups = make([]*GroupConfig, 0, len(cfg.Groups))
	for _, g := range cfg.Groups {
		if g == nil {
			out.Groups = append(out.Groups, nil)
			continue
		}
		gc := *g
		gc.Taps = append([]string(nil), g.Taps...)
		gc.Tools = append([]ToolEntry(nil), g.Tools...)
		gc.Dots = append([]DotEntry(nil), g.Dots...)
		for i := range gc.Dots {
			if gc.Dots[i].Hosts != nil {
				hosts := make(map[string]DotVariant, len(gc.Dots[i].Hosts))
				for host, variant := range gc.Dots[i].Hosts {
					hosts[host] = variant
				}
				gc.Dots[i].Hosts = hosts
			}
			gc.Dots[i].Ignore = append([]string(nil), gc.Dots[i].Ignore...)
		}
		out.Groups = append(out.Groups, &gc)
	}
	out.Tools = make(map[string]ToolSpec, len(cfg.Tools))
	for name, spec := range cfg.Tools {
		spec.Options = cloneStringMap(spec.Options)
		spec.Taps = append([]string(nil), spec.Taps...)
		spec.Providers = append([]ToolInstallSpec(nil), spec.Providers...)
		for i := range spec.Providers {
			spec.Providers[i].Options = cloneStringMap(spec.Providers[i].Options)
			spec.Providers[i].Source = cloneFallbackSource(spec.Providers[i].Source)
			spec.Providers[i].Recipe = cloneFallbackRecipe(spec.Providers[i].Recipe)
		}
		spec.Variants = append([]ToolInstallSpec(nil), spec.Variants...)
		for i := range spec.Variants {
			spec.Variants[i].Options = cloneStringMap(spec.Variants[i].Options)
		}
		if spec.Hosts != nil {
			hosts := make(map[string]ToolInstallSpec, len(spec.Hosts))
			for host, override := range spec.Hosts {
				override.Options = cloneStringMap(override.Options)
				hosts[host] = override
			}
			spec.Hosts = hosts
		}
		out.Tools[name] = spec
	}
	out.Hosts = make(map[string][]string, len(cfg.Hosts))
	for host, groups := range cfg.Hosts {
		out.Hosts[host] = append([]string(nil), groups...)
	}
	out.Ignore.Tools = append([]string(nil), cfg.Ignore.Tools...)
	out.Ignore.Dots = append([]string(nil), cfg.Ignore.Dots...)
	out.HostSettings = make(map[string]Settings, len(cfg.HostSettings))
	for host, settings := range cfg.HostSettings {
		out.HostSettings[host] = cloneSettings(settings)
	}
	Normalize(&out)
	return out
}

func cloneSettings(settings Settings) Settings {
	settings.DisabledProviders = cloneStringSlice(settings.DisabledProviders)
	settings.ProviderPriority = cloneStringSlice(settings.ProviderPriority)
	settings.ProviderUpdateQuarantine = cloneStringMap(settings.ProviderUpdateQuarantine)
	if settings.Ecosystems != nil {
		ecosystems := make(map[string]EcosystemSettings, len(settings.Ecosystems))
		for name, eco := range settings.Ecosystems {
			eco.Priority = append([]string(nil), eco.Priority...)
			ecosystems[name] = eco
		}
		settings.Ecosystems = ecosystems
	}
	settings.Providers = cloneProviders(settings.Providers)
	return settings
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneProviders(in []ProviderEntry) []ProviderEntry {
	if in == nil {
		return nil
	}
	out := make([]ProviderEntry, len(in))
	for i, p := range in {
		p.Options = cloneStringMap(p.Options)
		p.Variants = cloneInstallSpecs(p.Variants)
		p.Hosts = cloneInstallSpecMap(p.Hosts)
		out[i] = p
	}
	return out
}

func cloneInstallSpecs(in []ToolInstallSpec) []ToolInstallSpec {
	if in == nil {
		return nil
	}
	out := make([]ToolInstallSpec, len(in))
	for i, s := range in {
		s.Options = cloneStringMap(s.Options)
		out[i] = s
	}
	return out
}

func cloneInstallSpecMap(in map[string]ToolInstallSpec) map[string]ToolInstallSpec {
	if in == nil {
		return nil
	}
	out := make(map[string]ToolInstallSpec, len(in))
	for k, s := range in {
		s.Options = cloneStringMap(s.Options)
		out[k] = s
	}
	return out
}

func migrateConfigV9ToV10(cfg *RootConfig) error {
	cfg.Version = 10
	return nil
}

func migrateRawConfigV9ToV10(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`10`)
	return nil
}

func migrateConfigV10ToV11(cfg *RootConfig) error {
	cfg.Version = 11
	return nil
}

func migrateRawConfigV10ToV11(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`11`)
	return nil
}

func migrateConfigV11ToV12(cfg *RootConfig) error {
	cfg.Version = 12
	return nil
}

func migrateRawConfigV11ToV12(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`12`)
	return nil
}

func migrateConfigV12ToV13(cfg *RootConfig) error {
	cfg.Version = 13
	return nil
}

func migrateRawConfigV12ToV13(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`13`)
	return nil
}

// No-op: retained so old configs can still advance through the version chain.
func migrateConfigV14ToV15(cfg *RootConfig) error {
	cfg.Version = 15
	return nil
}

func migrateRawConfigV14ToV15(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`15`)
	return nil
}

// No-op: retained so old configs can still advance through the version chain.
func migrateConfigV15ToV16(cfg *RootConfig) error {
	cfg.Version = 16
	return nil
}

func migrateRawConfigV15ToV16(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`16`)
	return nil
}

// No-op: v17 only adds optional ToolInstallSpec source/recipe/bin_dir fields and $include support.
func migrateConfigV16ToV17(cfg *RootConfig) error {
	cfg.Version = 17
	return nil
}

func migrateRawConfigV16ToV17(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`17`)
	return nil
}

// No-op: v18 adds cargo to the provider enums without changing persisted config shape.
func migrateConfigV17ToV18(cfg *RootConfig) error {
	cfg.Version = 18
	return nil
}

func migrateRawConfigV17ToV18(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`18`)
	return nil
}

// No-op: retained so old configs can still advance through the version chain.
func migrateConfigV18ToV19(cfg *RootConfig) error {
	cfg.Version = 19
	return nil
}

func migrateRawConfigV18ToV19(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`19`)
	return nil
}

// No-op: retained so old configs can still advance through the version chain.
func migrateConfigV19ToV20(cfg *RootConfig) error {
	cfg.Version = 20
	return nil
}

func migrateRawConfigV19ToV20(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`20`)
	return nil
}

// No-op: retained so old configs can still advance through the version chain.
func migrateConfigV20ToV21(cfg *RootConfig) error {
	cfg.Version = 21
	return nil
}

func migrateRawConfigV20ToV21(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`21`)
	return nil
}

// No-op: v22 adds optional checksum manifests to GitHub release asset recipes.
func migrateConfigV21ToV22(cfg *RootConfig) error {
	cfg.Version = 22
	return nil
}

func migrateRawConfigV21ToV22(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`22`)
	return nil
}

// No-op: v24 rejects the removed agents section instead of rewriting it.
func migrateConfigV22ToV23(cfg *RootConfig) error {
	cfg.Version = 23
	return nil
}

func migrateRawConfigV22ToV23(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`23`)
	return nil
}

func migrateConfigV23ToV24(cfg *RootConfig) error {
	cfg.Version = 24
	return nil
}

func migrateRawConfigV23ToV24(raw map[string]json.RawMessage) error {
	if err := validateRemovedAgentConfigFields(raw); err != nil {
		return err
	}
	raw["version"] = json.RawMessage(`24`)
	return nil
}

func includeBaseDir(configPath string) string {
	if resolved, err := resolveConfigWritePath(configPath); err == nil {
		return filepath.Dir(resolved)
	}
	return filepath.Dir(configPath)
}

func loadIncludes(path string, cfg *RootConfig) error {
	var stack includePathStack
	return loadIncludesFrom(path, cfg, &stack)
}

type includePathEntry struct {
	canonical string
	display   string
}

type includePathStack []includePathEntry

func (s *includePathStack) push(path string) error {
	display, err := filepath.Abs(path)
	if err != nil {
		display = filepath.Clean(path)
	}
	canonical := display
	if resolved, err := filepath.EvalSymlinks(display); err == nil {
		canonical = resolved
	}
	for idx, entry := range *s {
		if entry.canonical != canonical {
			continue
		}
		cycle := make([]string, 0, len(*s)-idx+1)
		for _, active := range (*s)[idx:] {
			cycle = append(cycle, active.display)
		}
		cycle = append(cycle, display)
		return fmt.Errorf("config include cycle: %s", strings.Join(cycle, " -> "))
	}
	*s = append(*s, includePathEntry{canonical: canonical, display: display})
	return nil
}

func (s *includePathStack) pop() {
	*s = (*s)[:len(*s)-1]
}

func loadIncludesFrom(path string, cfg *RootConfig, stack *includePathStack) error {
	if err := stack.push(path); err != nil {
		return err
	}
	defer stack.pop()

	if cfg == nil || len(cfg.Include) == 0 {
		return nil
	}
	baseDir := includeBaseDir(path)
	includes := append([]string(nil), cfg.Include...)
	cfg.Include = nil
	for _, include := range includes {
		include = strings.TrimSpace(include)
		if include == "" {
			continue
		}
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(baseDir, include)
		}
		if err := testguard.RequireTempPath("config include read", includePath); err != nil {
			return err
		}
		data, err := os.ReadFile(includePath)
		if err != nil {
			return fmt.Errorf("reading included config %q: %w", include, err)
		}
		var fragment RootConfig
		if err := unmarshalJSONObject(data, &fragment); err != nil {
			return fmt.Errorf("parsing included config %q: %w", include, err)
		}
		if err := loadIncludesFrom(includePath, &fragment, stack); err != nil {
			return err
		}
		cfg.MergeNotices = append(cfg.MergeNotices, fragment.MergeNotices...)
		cfg.MergeNotices = append(cfg.MergeNotices, includeMergeNotices(cfg, &fragment, include)...)
		MergeRootConfig(cfg, &fragment)
	}
	return nil
}

func cloneFallbackSource(src *FallbackSource) *FallbackSource {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

func cloneFallbackRecipe(recipe *FallbackRecipe) *FallbackRecipe {
	if recipe == nil {
		return nil
	}
	out := *recipe
	return &out
}
