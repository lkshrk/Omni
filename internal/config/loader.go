package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lkshrk/omni/internal/testguard"
)

// atomicWrite writes data to path using a unique temp file + rename so partial
// writes never corrupt an existing config. Each call gets its own temp name,
// so concurrent callers for the same path do not collide on the temp file.
func atomicWrite(path string, data []byte) error {
	if err := testguard.RequireTempPath("config write", path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	// 0o700: settings.json holds host mappings and per-tool install pins;
	// no need for other local users to read this on shared machines.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	writePath, err := resolveConfigWritePath(path)
	if err != nil {
		return err
	}
	if err := testguard.RequireTempPath("config write target", writePath); err != nil {
		return err
	}
	dir = filepath.Dir(writePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "settings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing temp file: %w", werr)
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("closing temp file: %w", cerr)
	}
	if err := os.Rename(tmp, writePath); err != nil {
		_ = os.Remove(tmp)
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

// SchemaURL is the canonical URL to the published JSON Schema for the current
// settings.json format. It is injected as "$schema" on every write so editors
// can provide validation and auto-complete without any additional setup.
var SchemaURL = SchemaURLForVersion(CurrentVersion)

// SchemaURLForVersion returns the canonical JSON Schema URL for a settings.json
// format version.
func SchemaURLForVersion(version int) string {
	return fmt.Sprintf("%s/omni.settings.v%d.schema.json", schemaBaseURL, version)
}

// DefaultConfigPath returns the path to settings.json using this priority:
//  1. $OMNI_CONFIG — explicit override (full file path)
//  2. $XDG_CONFIG_HOME/omni/settings.json — XDG base dir spec
//  3. $HOME/.config/omni/settings.json — fallback
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

// DefaultCacheDir returns the omni cache directory using this priority:
//  1. $OMNI_CACHE_DIR — explicit override
//  2. $XDG_CACHE_HOME/omni — XDG base dir spec
//  3. $HOME/.cache/omni — fallback
//
// The SQLite database and other derived/transient state live here.
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

// Load reads, parses, and normalizes settings.json at path.
// Returns an empty RootConfig (no error) when the file does not exist.
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
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parsing config file: %w", err)
	}
	migrated, err := Migrate(&cfg)
	if err != nil {
		return nil, false, err
	}
	MigrateSkillPackages(&cfg)
	if normalize {
		Normalize(&cfg)
	}
	return &cfg, migrated, nil
}

// Migrate updates cfg in place from older settings.json formats to the current
// in-memory representation. Future versions are rejected so older binaries do
// not accidentally rewrite configs they cannot understand.
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

// NormalizeConcreteProvider canonicalizes a concrete provider/manager name for
// persisted config: meta-provider families ("system"/"node"/"python") and empty
// strings return "", and "pip3" collapses to "pip". Other names pass through.
func NormalizeConcreteProvider(provider string) string {
	return normalizeConcreteProvider(provider)
}

// Normalize sorts order-insensitive config collections in place.
// Returns true when it changed cfg.
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

// NormalizeFile normalizes the persisted config when it exists.
// Unknown top-level keys are preserved.
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
	if err := Patch(path, orderPatch{Version: cfg.Version, Hosts: cfg.Hosts, Groups: cfg.Groups}); err != nil {
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

// Patch reads the JSON at path, merges the top-level keys from patch
// into it, and writes the result back atomically.
//
// patch must be a value that marshals to a JSON object (struct or map).
// Only the top-level keys present in the marshaled patch are updated;
// every other key already in the file is preserved unchanged.
//
// This is useful when you want to update a single section (e.g. "settings")
// without touching unrelated sections (e.g. "groups").
//
// If the file does not exist, Patch creates it with only the patch keys.
// A patch entry whose value is JSON null deletes that top-level key.
func Patch(path string, patch interface{}) error {
	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encoding patch: %w", err)
	}
	var patchMap map[string]json.RawMessage
	if err := json.Unmarshal(patchData, &patchMap); err != nil {
		return fmt.Errorf("parsing patch: %w", err)
	}
	return PatchRaw(path, patchMap)
}

// PatchRaw is the lower-level form of Patch that accepts an already-decoded
// top-level key map. A value of JSON null deletes that key from the file.
func PatchRaw(path string, patch map[string]json.RawMessage) error {
	if err := testguard.RequireTempPath("config patch", path); err != nil {
		return err
	}
	raw := make(map[string]json.RawMessage)
	exists := false
	if data, err := os.ReadFile(path); err == nil {
		exists = true
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing existing config: %w", err)
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
	return atomicWrite(path, buf.Bytes())
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

// Save marshals cfg to JSON and writes it atomically to path.
// Creates parent directories if they do not exist.
// Output is indented for human readability.
// The "$schema" field is always stamped with SchemaURL on write.
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
	data = append(data, '\n') // trailing newline
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
	out.Settings = cloneSettings(cfg.Settings)
	out.Agents.Packages = append([]SkillPackage(nil), cfg.Agents.Packages...)
	for i := range out.Agents.Packages {
		out.Agents.Packages[i].Agents = append([]string(nil), out.Agents.Packages[i].Agents...)
	}
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
		gc.Skills = append([]string(nil), g.Skills...)
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
	settings.AgentsUse = cloneStringSlice(settings.AgentsUse)
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

// migrateConfigV14ToV15 is a no-op data migration: v15 only adds
// GroupConfig.Marketplaces (a new omitempty field with no prior form to
// convert), matching how v11->v12 handled the field addition in the same
// commit that also raised CurrentVersion.
func migrateConfigV14ToV15(cfg *RootConfig) error {
	cfg.Version = 15
	return nil
}

func migrateRawConfigV14ToV15(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`15`)
	return nil
}

// migrateConfigV15ToV16 is a no-op data migration: v16 only adds
// AgentsIgnore.Marketplaces (a new omitempty field with no prior form to
// convert), matching how v14->v15 handled the field addition in the same
// commit that also raised CurrentVersion.
func migrateConfigV15ToV16(cfg *RootConfig) error {
	cfg.Version = 16
	return nil
}

func migrateRawConfigV15ToV16(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`16`)
	return nil
}
