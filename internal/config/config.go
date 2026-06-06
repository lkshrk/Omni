// Package config defines the JSON schema and validation for omni's settings file.
package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// CurrentVersion is the latest settings.json format version understood by omni.
// Version 0 is the legacy unversioned format.
const CurrentVersion = 7

const (
	// FallbackSourceGitHub identifies a fallback recipe sourced from a GitHub repository.
	FallbackSourceGitHub = "github"

	// FallbackStatusUnresolved means a source is known but no usable recipe exists yet.
	FallbackStatusUnresolved = "unresolved"
	// FallbackStatusUnverified means a recipe exists but has not completed successfully.
	FallbackStatusUnverified = "unverified"
	// FallbackStatusVerified means a recipe completed and its check command passed.
	FallbackStatusVerified = "verified"
	// FallbackStatusFailed means a recipe was attempted and failed.
	FallbackStatusFailed = "failed"

	// FallbackRecipeGitHubReleaseAsset installs from a matched GitHub release asset.
	FallbackRecipeGitHubReleaseAsset = "github_release_asset"
	// FallbackRecipeRawCommands runs user-editable shell commands.
	FallbackRecipeRawCommands = "raw_commands"
)

// ToolEntry is the resolved execution form for a logical tool.
//
// Persisted group memberships use only Name and marshal as a JSON string. The
// provider/package fields remain for the app/sync internal flat view.
type ToolEntry struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	Package     string            `json:"package,omitempty"`
	InstallWith string            `json:"install_with,omitempty"`
	Ignore      bool              `json:"ignore,omitempty"` // auto-ignored on import (e.g. pip library packages)
	Options     map[string]string `json:"options,omitempty"`
}

// UnmarshalJSON accepts the new logical membership form ("ripgrep") and rejects
// the old group-owned object form so invalid settings fail at load time.
func (e *ToolEntry) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		e.Name = name
		e.Provider = ""
		e.Package = ""
		e.InstallWith = ""
		e.Ignore = false
		e.Options = nil
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		return fmt.Errorf("old group tool object config is no longer supported; use a logical tool name string")
	}
	return fmt.Errorf("tool membership must be a string")
}

// MarshalJSON writes only the logical tool name for group memberships.
func (e ToolEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Name)
}

// BoolVal safely dereferences a *bool, returning false for nil.
func BoolVal(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(b bool) *bool { return &b }

// EffectivePackage returns the package identifier to pass to the provider.
// Falls back to the tool name when no explicit package is set.
func (e ToolEntry) EffectivePackage() string {
	if e.Package != "" {
		return e.Package
	}
	return e.Name
}

// ToolInstallSpec is a concrete install candidate for a logical tool.
type ToolInstallSpec struct {
	Provider    string            `json:"provider"`
	Package     string            `json:"package,omitempty"`
	Bin         string            `json:"bin,omitempty"`
	InstallWith string            `json:"install_with,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
}

// EffectivePackage returns the package identifier for logicalName.
func (s ToolInstallSpec) EffectivePackage(logicalName string) string {
	if s.Package != "" {
		return s.Package
	}
	return logicalName
}

// FallbackSource describes where a fallback recipe came from.
type FallbackSource struct {
	Type  string `json:"type"`
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
	URL   string `json:"url,omitempty"`
}

// FallbackRecipe describes structured metadata used to build install commands.
type FallbackRecipe struct {
	Type             string `json:"type,omitempty"`
	AssetPattern     string `json:"asset_pattern,omitempty"`
	BinaryPath       string `json:"binary_path,omitempty"`
	Checksum         string `json:"checksum,omitempty"`
	ReleaseID        string `json:"release_id,omitempty"`
	TagName          string `json:"tag_name,omitempty"`
	PublishedAt      string `json:"published_at,omitempty"`
	AssetID          string `json:"asset_id,omitempty"`
	AssetName        string `json:"asset_name,omitempty"`
	AssetDownloadURL string `json:"asset_download_url,omitempty"`
}

// FallbackPlatform overrides release-asset matching for one OS/architecture key.
type FallbackPlatform struct {
	AssetPattern string `json:"asset_pattern,omitempty"`
	BinaryPath   string `json:"binary_path,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
}

// FallbackCommands contains user-editable shell commands for fallback lifecycle actions.
type FallbackCommands struct {
	Install   string `json:"install,omitempty"`
	Check     string `json:"check,omitempty"`
	Uninstall string `json:"uninstall,omitempty"`
	Upgrade   string `json:"upgrade,omitempty"`
	Version   string `json:"version,omitempty"`
}

// FallbackSpec defines a best-effort non-provider install recipe for a system tool.
type FallbackSpec struct {
	Source         FallbackSource              `json:"source"`
	Status         string                      `json:"status,omitempty"`
	Binary         string                      `json:"binary,omitempty"`
	BinDir         string                      `json:"bin_dir,omitempty"`
	ReleaseChannel string                      `json:"release_channel,omitempty"`
	Recipe         FallbackRecipe              `json:"recipe,omitempty"`
	Platforms      map[string]FallbackPlatform `json:"platforms,omitempty"`
	Commands       FallbackCommands            `json:"commands,omitempty"`
}

// ToolSpec defines one logical tool and its default install spec.
type ToolSpec struct {
	Type        string                     `json:"type,omitempty"`
	Providers   []ToolInstallSpec          `json:"providers,omitempty"`
	Provider    string                     `json:"provider,omitempty"`
	Package     string                     `json:"package,omitempty"`
	InstallWith string                     `json:"install_with,omitempty"`
	Git         string                     `json:"git,omitempty"`
	Quarantine  string                     `json:"quarantine,omitempty"`
	Options     map[string]string          `json:"options,omitempty"`
	Taps        []string                   `json:"taps,omitempty"`
	Ignore      bool                       `json:"ignore,omitempty"`
	Variants    []ToolInstallSpec          `json:"variants,omitempty"`
	Hosts       map[string]ToolInstallSpec `json:"hosts,omitempty"`
	Fallback    *FallbackSpec              `json:"fallback,omitempty"`
}

// DefaultInstallSpec returns the default install candidate for this logical tool.
func (s ToolSpec) DefaultInstallSpec() ToolInstallSpec {
	if len(s.Providers) > 0 {
		return s.Providers[0]
	}
	return ToolInstallSpec{Provider: s.Provider, Package: s.Package, InstallWith: s.InstallWith, Options: s.Options}
}

// ToToolEntry resolves this spec into the syncer's flat tool shape.
func (s ToolSpec) ToToolEntry(logicalName string, install ToolInstallSpec) ToolEntry {
	packageName := install.EffectivePackage(logicalName)
	if install.Package == "" && s.Package != "" {
		packageName = s.Package
	}
	return ToolEntry{
		Name:        logicalName,
		Provider:    install.Provider,
		Package:     packageName,
		InstallWith: install.InstallWith,
		Ignore:      s.Ignore,
		Options:     install.Options,
	}
}

// ProviderEntry is a self-contained, ordered bootstrap provider. It mirrors
// ToolSpec (default spec + Variants + Hosts) but carries its own Name, since
// Settings.Providers is a list (not a name-keyed map like Tools). Unlike
// ToolEntry, ProviderEntry serializes as a full object.
type ProviderEntry struct {
	Name        string                     `json:"name"`
	Provider    string                     `json:"provider"`
	Package     string                     `json:"package,omitempty"`
	InstallWith string                     `json:"install_with,omitempty"`
	Options     map[string]string          `json:"options,omitempty"`
	Variants    []ToolInstallSpec          `json:"variants,omitempty"`
	Hosts       map[string]ToolInstallSpec `json:"hosts,omitempty"`
}

// ToToolSpec adapts a provider entry to the ToolSpec resolution shape so it can
// reuse the tool install-spec resolver (Hosts → Variants-by-availability → default).
func (p ProviderEntry) ToToolSpec() ToolSpec {
	return ToolSpec{
		Provider:    p.Provider,
		Package:     p.Package,
		InstallWith: p.InstallWith,
		Options:     p.Options,
		Variants:    p.Variants,
		Hosts:       p.Hosts,
	}
}

type EcosystemSettings struct {
	Manager  string   `json:"manager,omitempty"`
	Priority []string `json:"priority,omitempty"`
}

// Settings holds user-configurable options stored in settings.json.
type Settings struct {
	// AutoImport adds newly discovered installed tools to the config on every sync.
	AutoImport bool `json:"auto_import,omitempty"`
	// UpdateQuarantine defers updates until the package-manager reported
	// availability timestamp is older than this duration (for example "2d").
	UpdateQuarantine string `json:"update_quarantine,omitempty"`
	// ProviderUpdateQuarantine overrides UpdateQuarantine by logical provider,
	// concrete provider, or concrete manager. Concrete values win in app logic.
	ProviderUpdateQuarantine map[string]string `json:"provider_update_quarantine,omitempty"`
	// Ecosystems holds settings for portable ecosystem providers such as system,
	// node, and python.
	Ecosystems map[string]EcosystemSettings `json:"ecosystems,omitempty"`
	// FallbackBinDir is the default directory for fallback-installed binaries.
	FallbackBinDir string `json:"fallback_bin_dir,omitempty"`
	// DotsRepo is the per-machine path to the dotfiles git repository.
	DotsRepo string `json:"dots_repo,omitempty"`
	// DotsDisabled is true when the user has explicitly opted out of dotfile sync
	// on this machine. Stored in host_settings, not global settings.
	// Using *bool so that an absent host entry (nil) is distinguished from an
	// explicit false — a nil host value must not silently override a global true.
	DotsDisabled *bool `json:"dots_disabled,omitempty"`
	// DotsGit controls git behaviour for the dots repo.
	DotsGit DotsGitConfig `json:"dots_git"`
	// DisabledProviders lists ecosystem provider names ("system", "node", "python")
	// that are disabled on this machine. Stored in host_settings, not global settings.
	DisabledProviders []string `json:"disabled_providers,omitempty"`
	// ProviderPriority ranks concrete providers for this host.
	ProviderPriority []string `json:"provider_priority,omitempty"`
	// Providers is an ordered list of bootstrap providers installed before the
	// rest of a sync, in list order. Each entry is self-contained.
	Providers []ProviderEntry `json:"providers,omitempty"`
}

func (s Settings) Ecosystem(name string) EcosystemSettings {
	if s.Ecosystems == nil {
		return EcosystemSettings{}
	}
	return s.Ecosystems[name]
}

func (s Settings) EcosystemManager(name string) string {
	if eco := s.Ecosystem(name); eco.Manager != "" {
		return eco.Manager
	}
	return ""
}

func (s *Settings) SetEcosystemManager(name, manager string) {
	if s.Ecosystems == nil {
		s.Ecosystems = make(map[string]EcosystemSettings)
	}
	eco := s.Ecosystems[name]
	eco.Manager = manager
	s.Ecosystems[name] = eco
}

func (s Settings) EcosystemPriority(name string) []string {
	if eco := s.Ecosystem(name); len(eco.Priority) > 0 {
		return eco.Priority
	}
	return nil
}

func (s *Settings) SetEcosystemPriority(name string, priority []string) {
	if s.Ecosystems == nil {
		s.Ecosystems = make(map[string]EcosystemSettings)
	}
	eco := s.Ecosystems[name]
	eco.Priority = append([]string(nil), priority...)
	s.Ecosystems[name] = eco
}

// DotsGitConfig controls how the dots git wrapper behaves.
type DotsGitConfig struct {
	// AutoCommit runs "git commit" automatically after add/remove operations.
	AutoCommit bool `json:"auto_commit,omitempty"`
	// AutoPush runs "git push" (including an implicit commit) after add/remove.
	// Implies AutoCommit.
	AutoPush bool `json:"auto_push,omitempty"`
}

// DotVariant selects a host-specific stow package for a logical dot entry.
type DotVariant struct {
	// Package is the stow package directory for this host variant. When empty,
	// the dot entry's default package is used.
	Package string `json:"package,omitempty"`
}

// DotEntry is one [[dots]] entry declaring a config file/dir to be managed.
type DotEntry struct {
	// Name is the logical dotfile identity used for groups, ignore rules, and UI
	// rows. It is also the default stow package name when Package is empty.
	Name string `json:"name"`
	// Path is the original filesystem location being managed (e.g. "~/.config/nvim").
	// Used for display, health checks, and onboarding adoption.
	Path string `json:"path,omitempty"`
	// Package is the default stow package directory. Empty means Name.
	Package string `json:"package,omitempty"`
	// Hosts maps short hostnames to host-specific package variants.
	Hosts map[string]DotVariant `json:"hosts,omitempty"`
	// Ignored keeps the entry visible while preventing sync/discovery from managing it.
	Ignored bool `json:"ignored,omitempty"`
	// Ignore holds glob patterns for files to skip within this entry.
	Ignore []string `json:"ignore,omitempty"`
	// OnConflict sets an automatic conflict resolution for this entry during sync.
	// Empty means manual resolution (sync errors on conflict). "use_repo" relinks
	// the repo version over the local target; "use_local" adopts the local content
	// into the repo. Useful for files an external tool constantly rewrites.
	OnConflict string `json:"on_conflict,omitempty"`
}

// EffectivePackage returns the default stow package directory for this dot.
func (e DotEntry) EffectivePackage() string {
	if strings.TrimSpace(e.Package) != "" {
		return e.Package
	}
	return e.Name
}

// PackageForHost returns the stow package directory for host, falling back to
// the default package. Host names are matched exactly; callers own normalization.
func (e DotEntry) PackageForHost(host string) string {
	if strings.TrimSpace(host) != "" && e.Hosts != nil {
		if variant, ok := e.Hosts[host]; ok && strings.TrimSpace(variant.Package) != "" {
			return variant.Package
		}
	}
	return e.EffectivePackage()
}

// GlobalIgnore lists tools and dotfiles skipped across all hosts.
type GlobalIgnore struct {
	Tools []string `json:"tools,omitempty"`
	Dots  []string `json:"dots,omitempty"`
}

// HostAssignment is the in-memory UI/app view of one host's reusable group
// assignments plus the global tool ignore list exposed for the active host.
type HostAssignment struct {
	Groups []string `json:"groups,omitempty"`
	Ignore []string `json:"ignore,omitempty"`
}

// GroupConfig is a named collection of tools, dots, and taps.
type GroupConfig struct {
	// Name is the group identifier.
	Name        string      `json:"name,omitempty"`
	Special     string      `json:"special,omitempty"`
	Description string      `json:"description,omitempty"`
	Taps        []string    `json:"taps,omitempty"`
	Tools       []ToolEntry `json:"tools,omitempty"`
	Dots        []DotEntry  `json:"dots,omitempty"`
	Ignore      []string    `json:"-"`
}

// IsHost reports whether this is the physical special group for a hostname.
func (g *GroupConfig) IsHost() bool { return g != nil && g.Special == "host" }

// GroupName returns the display name.
func (g *GroupConfig) GroupName() string { return g.Name }

// BaseName returns the identifier used for filtering and display.
func (g *GroupConfig) BaseName() string {
	return g.Name
}

// RootConfig is the top-level settings.json struct.
// It holds all omni configuration: settings, host assignments, and groups.
type RootConfig struct {
	// Schema holds the "$schema" URI injected on every write for editor support.
	// It is read back on load but never acted on by the application.
	Schema string `json:"$schema,omitempty"`
	// Version identifies the settings.json format. Missing/zero is treated as the
	// legacy unversioned format and migrated to CurrentVersion on load.
	Version  int                 `json:"version"`
	Settings Settings            `json:"settings"`
	Tools    map[string]ToolSpec `json:"tools,omitempty"`
	Hosts    map[string][]string `json:"hosts,omitempty"`
	Ignore   GlobalIgnore        `json:"ignore,omitempty"`
	Groups   []*GroupConfig      `json:"groups,omitempty"`
	// HostSettings holds per-machine setting overrides, keyed by short hostname.
	// Fields set here override the global Settings block for that machine.
	// Host-specific fields: Ecosystems, DotsRepo, DotsDisabled,
	// DisabledProviders.
	// Global fields (not overridable here): AutoImport, DotsGit.
	HostSettings map[string]Settings `json:"host_settings,omitempty"`
}

// EffectiveSettings returns the merged settings for a specific machine.
// Global settings provide the base; host-specific settings override individual fields.
func (c *RootConfig) EffectiveSettings(shortHostname string) Settings {
	s := cloneSettings(c.Settings)
	if len(c.HostSettings) == 0 {
		return s
	}
	hs, ok := c.HostSettings[shortHostname]
	if !ok {
		return s
	}
	if len(hs.Ecosystems) > 0 {
		if s.Ecosystems == nil {
			s.Ecosystems = make(map[string]EcosystemSettings)
		}
		for name, hostEco := range hs.Ecosystems {
			eco := s.Ecosystems[name]
			if hostEco.Manager != "" {
				eco.Manager = hostEco.Manager
			}
			if len(hostEco.Priority) > 0 {
				eco.Priority = append([]string(nil), hostEco.Priority...)
			}
			s.Ecosystems[name] = eco
		}
	}
	if hs.DotsRepo != "" {
		s.DotsRepo = hs.DotsRepo
	}
	// DotsDisabled: only override when the host has explicitly set it (non-nil).
	// A nil host pointer means "not configured here — inherit global".
	if hs.DotsDisabled != nil {
		s.DotsDisabled = hs.DotsDisabled
	}
	// DisabledProviders: nil means "inherit global"; an explicit empty list
	// means "enable all ecosystem providers on this host".
	if hs.DisabledProviders != nil {
		s.DisabledProviders = cloneStringSlice(hs.DisabledProviders)
	}
	if hs.ProviderPriority != nil {
		s.ProviderPriority = cloneStringSlice(hs.ProviderPriority)
	}
	// Providers: nil host list means "inherit global"; a non-nil host list
	// (including empty) replaces the global list entirely.
	if hs.Providers != nil {
		s.Providers = cloneProviders(hs.Providers)
	}
	return s
}

// Config is a flat view of tools and settings used by the syncer.
// It is not persisted directly — it is built from RootConfig at sync time.
type Config struct {
	Settings Settings    `json:"settings"`
	Taps     []string    `json:"taps,omitempty"`
	Tools    []ToolEntry `json:"tools"`
}

// ValidateGroups is retained for old callers. Logical tools may intentionally
// belong to multiple groups, so group-level membership duplication is validated
// by ValidateRoot only for malformed same-group entries.
func ValidateGroups(groups []*GroupConfig) []error {
	return nil
}

// ValidationError is a semantic config validation error with a JSON path.
type ValidationError struct {
	Path    string
	Message string
}

func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

type ValidationErrors []ValidationError

func (errs ValidationErrors) Error() string {
	if len(errs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}

// ProviderValidation describes the provider identities accepted by semantic
// config validation without coupling config to the provider registry package.
type ProviderValidation struct {
	Known              []string
	Ecosystems         []string
	ConcreteEcosystems map[string]string
}

// ValidateRoot checks the normalized logical-tool settings model.
// Pass a zero ProviderValidation to skip provider validation.
func ValidateRoot(cfg *RootConfig, providers ProviderValidation) []ValidationError {
	if cfg == nil {
		return nil
	}
	providerSet := make(map[string]struct{}, len(providers.Known))
	for _, p := range providers.Known {
		providerSet[p] = struct{}{}
	}
	ecosystemSet := make(map[string]struct{}, len(providers.Ecosystems))
	for _, p := range providers.Ecosystems {
		ecosystemSet[p] = struct{}{}
	}
	validateInstall := func(path string, spec ToolInstallSpec, allowInstallWith bool) []ValidationError {
		var errs []ValidationError
		if spec.Provider == "script" {
			return validateScriptSpec(path, spec)
		}
		if spec.Provider == "" {
			errs = append(errs, ValidationError{Path: path + ".provider", Message: "provider is required"})
		} else if len(providerSet) > 0 {
			if _, ok := providerSet[spec.Provider]; !ok {
				errs = append(errs, ValidationError{Path: path + ".provider", Message: fmt.Sprintf("unknown provider %q", spec.Provider)})
			} else if _, ok := ecosystemSet[spec.Provider]; ok {
				errs = append(errs, ValidationError{Path: path + ".provider", Message: fmt.Sprintf("ecosystem provider %q is not supported in tools providers", spec.Provider)})
			}
		}
		if spec.InstallWith != "" && !allowInstallWith {
			errs = append(errs, ValidationError{Path: path + ".install_with", Message: "install_with is not supported on provider entries"})
		} else if spec.InstallWith != "" && len(providerSet) > 0 {
			if _, ok := providerSet[spec.InstallWith]; !ok {
				errs = append(errs, ValidationError{Path: path + ".install_with", Message: fmt.Sprintf("unknown concrete provider/manager %q", spec.InstallWith)})
			} else if len(ecosystemSet) > 0 {
				if _, ok := ecosystemSet[spec.InstallWith]; ok {
					errs = append(errs, ValidationError{Path: path + ".install_with", Message: fmt.Sprintf("install_with %q must be a concrete provider or manager", spec.InstallWith)})
				}
				if ecosystem := providers.ConcreteEcosystems[spec.InstallWith]; ecosystem != "" && spec.Provider != "" && ecosystem != spec.Provider {
					errs = append(errs, ValidationError{Path: path + ".install_with", Message: fmt.Sprintf("install_with %q belongs to ecosystem %q, not %q", spec.InstallWith, ecosystem, spec.Provider)})
				}
			}
		}
		return errs
	}

	var errs []ValidationError
	for name, spec := range cfg.Tools {
		path := fmt.Sprintf("$.tools.%q", name)
		if strings.TrimSpace(name) == "" {
			errs = append(errs, ValidationError{Path: "$.tools", Message: "tool name is required"})
		}
		if len(spec.Providers) > 0 {
			for i, provider := range spec.Providers {
				errs = append(errs, validateInstall(fmt.Sprintf("%s.providers[%d]", path, i), provider, false)...)
			}
		} else if spec.Provider != "" || spec.Package != "" || spec.InstallWith != "" || len(spec.Options) > 0 {
			errs = append(errs, validateInstall(path, spec.DefaultInstallSpec(), true)...)
		}
		errs = append(errs, validateFallback(path+".fallback", spec.Fallback)...)
		for i, variant := range spec.Variants {
			errs = append(errs, validateInstall(fmt.Sprintf("%s.variants[%d]", path, i), variant, true)...)
		}
		for host, override := range spec.Hosts {
			if strings.TrimSpace(host) == "" {
				errs = append(errs, ValidationError{Path: path + ".hosts", Message: "host name is required"})
			}
			errs = append(errs, validateInstall(fmt.Sprintf("%s.hosts.%q", path, host), override, true)...)
		}
	}

	groupNames := make(map[string]struct{}, len(cfg.Groups))
	groupByName := make(map[string]*GroupConfig, len(cfg.Groups))
	memberships := make(map[string]*GroupConfig)
	dotMemberships := make(map[string]*GroupConfig)
	dotPackages := make(map[string]string)
	for gi, g := range cfg.Groups {
		if g == nil {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.groups[%d]", gi), Message: "group must not be null"})
			continue
		}
		groupName := g.BaseName()
		if strings.TrimSpace(groupName) == "" {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.groups[%d].name", gi), Message: "group name is required"})
		}
		if _, dup := groupNames[groupName]; dup {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.groups[%d].name", gi), Message: fmt.Sprintf("duplicate group %q", groupName)})
		}
		groupNames[groupName] = struct{}{}
		groupByName[groupName] = g
		if g.Special != "" && g.Special != "host" {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.groups[%d].special", gi), Message: fmt.Sprintf("unknown special group kind %q", g.Special)})
		}
		seenInGroup := make(map[string]struct{}, len(g.Tools))
		for ti, tool := range g.Tools {
			path := fmt.Sprintf("$.groups[%d].tools[%d]", gi, ti)
			if strings.TrimSpace(tool.Name) == "" {
				errs = append(errs, ValidationError{Path: path, Message: "tool name is required"})
				continue
			}
			if tool.Provider != "" || tool.Package != "" || tool.InstallWith != "" || tool.Ignore || len(tool.Options) > 0 {
				errs = append(errs, ValidationError{Path: path, Message: "group tool entries must be logical tool names only"})
			}
			if _, ok := cfg.Tools[tool.Name]; !ok {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("missing logical tool %q", tool.Name)})
			}
			if _, dup := seenInGroup[tool.Name]; dup {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("duplicate tool membership %q in group %q", tool.Name, groupName)})
			}
			seenInGroup[tool.Name] = struct{}{}
			if first, ok := memberships[tool.Name]; ok {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("tool %q already belongs to group %q", tool.Name, first.BaseName())})
			}
			if _, ok := memberships[tool.Name]; !ok {
				memberships[tool.Name] = g
			}
		}
		seenDots := make(map[string]struct{}, len(g.Dots))
		for di, dot := range g.Dots {
			path := fmt.Sprintf("$.groups[%d].dots[%d]", gi, di)
			if strings.TrimSpace(dot.Name) == "" {
				errs = append(errs, ValidationError{Path: path, Message: "dotfile name is required"})
				continue
			}
			if err := validateDotPackageName(dot.Name); err != nil {
				errs = append(errs, ValidationError{Path: path + ".name", Message: err.Error()})
			}
			defaultPackage := dot.EffectivePackage()
			if strings.TrimSpace(dot.Package) != "" {
				if err := validateDotPackageName(dot.Package); err != nil {
					errs = append(errs, ValidationError{Path: path + ".package", Message: err.Error()})
				}
			}
			errs = recordDotPackageUsage(errs, dotPackages, path+".package", defaultPackage, dot.Name)
			switch dot.OnConflict {
			case "", "use_repo", "use_local":
			default:
				errs = append(errs, ValidationError{Path: path + ".on_conflict", Message: fmt.Sprintf("invalid on_conflict %q: want \"use_repo\" or \"use_local\"", dot.OnConflict)})
			}
			for host, variant := range dot.Hosts {
				hostPath := fmt.Sprintf("%s.hosts.%q", path, host)
				if strings.TrimSpace(host) == "" {
					errs = append(errs, ValidationError{Path: path + ".hosts", Message: "host name is required"})
				}
				if strings.TrimSpace(variant.Package) != "" {
					if err := validateDotPackageName(variant.Package); err != nil {
						errs = append(errs, ValidationError{Path: hostPath + ".package", Message: err.Error()})
					}
				}
				errs = recordDotPackageUsage(errs, dotPackages, hostPath+".package", dot.PackageForHost(host), dot.Name)
			}
			if _, dup := seenDots[dot.Name]; dup {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("duplicate dotfile membership %q in group %q", dot.Name, groupName)})
			}
			seenDots[dot.Name] = struct{}{}
			if first, ok := dotMemberships[dot.Name]; ok {
				errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("dotfile %q already belongs to group %q", dot.Name, first.BaseName())})
			}
			if _, ok := dotMemberships[dot.Name]; !ok {
				dotMemberships[dot.Name] = g
			}
		}
	}
	for host, groups := range cfg.Hosts {
		if strings.TrimSpace(host) == "" {
			errs = append(errs, ValidationError{Path: "$.hosts", Message: "host name is required"})
		}
		hostGroup, ok := groupByName[host]
		if !ok {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.hosts.%q", host), Message: fmt.Sprintf("missing host group %q", host)})
		} else if !hostGroup.IsHost() {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.hosts.%q", host), Message: fmt.Sprintf("group %q must be marked as special host group", host)})
		}
		for i, group := range groups {
			assigned, ok := groupByName[group]
			if !ok {
				errs = append(errs, ValidationError{Path: fmt.Sprintf("$.hosts.%q[%d]", host, i), Message: fmt.Sprintf("missing group %q", group)})
			}
			if group == host {
				errs = append(errs, ValidationError{Path: fmt.Sprintf("$.hosts.%q[%d]", host, i), Message: "host group is implicit and must not be listed"})
			}
			if assigned != nil && assigned.IsHost() {
				errs = append(errs, ValidationError{Path: fmt.Sprintf("$.hosts.%q[%d]", host, i), Message: fmt.Sprintf("host group %q cannot be assigned", group)})
			}
		}
	}
	for i, p := range cfg.Settings.Providers {
		base := fmt.Sprintf("$.settings.providers[%d]", i)
		if strings.TrimSpace(p.Name) == "" {
			errs = append(errs, ValidationError{Path: base + ".name", Message: "provider name is required"})
		}
		errs = append(errs, validateProviderSpec(base, p.ToToolSpec().DefaultInstallSpec(), providerSet)...)
		for j, variant := range p.Variants {
			errs = append(errs, validateProviderSpec(fmt.Sprintf("%s.variants[%d]", base, j), variant, providerSet)...)
		}
		for host, override := range p.Hosts {
			if strings.TrimSpace(host) == "" {
				errs = append(errs, ValidationError{Path: base + ".hosts", Message: "host name is required"})
			}
			errs = append(errs, validateProviderSpec(fmt.Sprintf("%s.hosts.%q", base, host), override, providerSet)...)
		}
	}

	for i, ignored := range cfg.Ignore.Tools {
		if strings.TrimSpace(ignored) == "" {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.ignore.tools[%d]", i), Message: "tool name is required"})
		}
		if _, ok := cfg.Tools[ignored]; !ok {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.ignore.tools[%d]", i), Message: fmt.Sprintf("missing logical tool %q", ignored)})
		}
	}
	for i, ignored := range cfg.Ignore.Dots {
		if strings.TrimSpace(ignored) == "" {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.ignore.dots[%d]", i), Message: "dotfile name is required"})
		}
	}
	return errs
}

func validateFallback(path string, fallback *FallbackSpec) []ValidationError {
	if fallback == nil {
		return nil
	}
	var errs []ValidationError
	switch fallback.Source.Type {
	case "":
		errs = append(errs, ValidationError{Path: path + ".source.type", Message: "fallback source type is required"})
	case FallbackSourceGitHub:
		if strings.TrimSpace(fallback.Source.Owner) == "" || strings.TrimSpace(fallback.Source.Repo) == "" {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "github fallback source requires owner and repo"})
		}
	default:
		errs = append(errs, ValidationError{Path: path + ".source.type", Message: fmt.Sprintf("unknown fallback source type %q", fallback.Source.Type)})
	}

	status := fallback.Status
	if status == "" {
		status = FallbackStatusUnverified
	}
	switch status {
	case FallbackStatusUnresolved, FallbackStatusUnverified, FallbackStatusVerified, FallbackStatusFailed:
	default:
		errs = append(errs, ValidationError{Path: path + ".status", Message: fmt.Sprintf("unknown fallback status %q", fallback.Status)})
	}
	if status != FallbackStatusUnresolved && strings.TrimSpace(fallback.Commands.Check) == "" {
		errs = append(errs, ValidationError{Path: path + ".commands.check", Message: "fallback check command is required unless status is unresolved"})
	}
	return errs
}

func validateDotPackageName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("package name is required")
	}
	if filepath.IsAbs(name) || name == "." || name == ".." || filepath.Clean(name) != name {
		return fmt.Errorf("invalid package name %q", name)
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid package name %q: path separators are not allowed", name)
	}
	return nil
}

// validateProviderSpec validates one bootstrap-provider install spec. Unlike the
// tool validateInstall closure, it accepts concrete providers (brew, uv, bun):
// providers install through concrete managers, so the ecosystem-only rule does
// not apply.
func validateProviderSpec(path string, spec ToolInstallSpec, providerSet map[string]struct{}) []ValidationError {
	if spec.Provider == "script" {
		return validateScriptSpec(path, spec)
	}
	var errs []ValidationError
	if spec.Provider == "" {
		errs = append(errs, ValidationError{Path: path + ".provider", Message: "provider is required"})
	} else if len(providerSet) > 0 {
		if _, ok := providerSet[spec.Provider]; !ok {
			errs = append(errs, ValidationError{Path: path + ".provider", Message: fmt.Sprintf("unknown provider %q", spec.Provider)})
		}
	}
	if spec.InstallWith != "" && len(providerSet) > 0 {
		if _, ok := providerSet[spec.InstallWith]; !ok {
			errs = append(errs, ValidationError{Path: path + ".install_with", Message: fmt.Sprintf("unknown concrete provider/manager %q", spec.InstallWith)})
		}
	}
	return errs
}

// validateScriptSpec enforces the script provider's option contract: a
// non-empty install command, and at least one detection mechanism.
func validateScriptSpec(path string, spec ToolInstallSpec) []ValidationError {
	var errs []ValidationError
	if strings.TrimSpace(spec.Options["install"]) == "" {
		errs = append(errs, ValidationError{
			Path:    path + ".options.install",
			Message: "script provider requires a non-empty install command",
		})
	}
	if strings.TrimSpace(spec.Options["detect"]) == "" && strings.TrimSpace(spec.Options["check"]) == "" {
		errs = append(errs, ValidationError{
			Path:    path + ".options",
			Message: "script provider requires options.detect or options.check",
		})
	}
	return errs
}

func recordDotPackageUsage(errs []ValidationError, packages map[string]string, path, pkg, logicalName string) []ValidationError {
	if strings.TrimSpace(pkg) == "" {
		return append(errs, ValidationError{Path: path, Message: "package name is required"})
	}
	key := strings.ToLower(pkg)
	if owner, ok := packages[key]; ok && owner != logicalName {
		return append(errs, ValidationError{
			Path:    path,
			Message: fmt.Sprintf("package %q is already used by dotfile %q", pkg, owner),
		})
	}
	packages[key] = logicalName
	return errs
}

// Validate checks the config for semantic errors.
// Returns a slice of errors; an empty slice means the config is valid.
// Pass nil for knownProviders to skip provider validation.
func Validate(cfg *Config, knownProviders []string) []error {
	var errs []error

	providerSet := make(map[string]struct{}, len(knownProviders))
	for _, p := range knownProviders {
		providerSet[p] = struct{}{}
	}

	seen := make(map[string]struct{})
	for i, t := range cfg.Tools {
		if t.Name == "" {
			errs = append(errs, fmt.Errorf("tool[%d]: name is required", i))
		}
		if t.Provider == "" {
			errs = append(errs, fmt.Errorf("tool %q: provider is required", t.Name))
		}
		if len(knownProviders) > 0 {
			if _, ok := providerSet[t.Provider]; !ok {
				errs = append(errs, fmt.Errorf("tool %q: unknown provider %q", t.Name, t.Provider))
			}
		}
		key := t.Name + "\x00" + t.Provider
		if _, dup := seen[key]; dup {
			errs = append(errs, fmt.Errorf("duplicate tool: name=%q provider=%q", t.Name, t.Provider))
		}
		seen[key] = struct{}{}
	}

	return errs
}
