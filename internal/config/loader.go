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
	if data, err := os.ReadFile(path); err == nil {
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

func normalizedCopy(cfg *RootConfig) RootConfig {
	if cfg == nil {
		return RootConfig{Version: CurrentVersion}
	}
	out := *cfg
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
	settings.ProviderUpdateQuarantine = cloneStringMap(settings.ProviderUpdateQuarantine)
	if settings.Ecosystems != nil {
		ecosystems := make(map[string]EcosystemSettings, len(settings.Ecosystems))
		for name, eco := range settings.Ecosystems {
			eco.Priority = append([]string(nil), eco.Priority...)
			ecosystems[name] = eco
		}
		settings.Ecosystems = ecosystems
	}
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
