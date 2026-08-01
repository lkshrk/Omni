// Package config defines the JSON schema and validation for omni's settings file.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

// CurrentVersion — Version 0 is the legacy unversioned format.
const CurrentVersion = 21

const (
	FallbackSourceGitHub = "github"

	// FallbackStatusUnresolved means a source is known but no usable recipe exists yet.
	FallbackStatusUnresolved = "unresolved"
	// FallbackStatusUnsupported means source metadata exists but no current-platform recipe is usable.
	FallbackStatusUnsupported = "unsupported"
	// FallbackStatusUnverified means a recipe exists but has not completed successfully.
	FallbackStatusUnverified = "unverified"
	// FallbackStatusVerified means a recipe completed and its check command passed.
	FallbackStatusVerified = "verified"
	// FallbackStatusFailed means a recipe was attempted and failed.
	FallbackStatusFailed = "failed"

	FallbackRecipeGitHubReleaseAsset = "github_release_asset"
	FallbackRecipeRawCommands        = "raw_commands"
	FallbackRecipeCurlInstallScript  = "curl_install_script"
	FallbackRecipeAptRepo            = "apt_repo"

	// AgentRefPackages expands to every agents.packages[].source value.
	AgentRefPackages = "@agents.packages"
	// AgentRefMcpServers expands to every agents.mcp_servers[].name value.
	AgentRefMcpServers = "@agents.mcp_servers"
	// AgentRefPlugins expands to every agents.plugins[].name value.
	AgentRefPlugins = "@agents.plugins"
	// AgentRefMarketplaces expands to every agents.marketplaces[].name value.
	AgentRefMarketplaces = "@agents.marketplaces"
)

// ToolEntry — Group memberships persist as a bare name string; the provider/package fields serve the app/sync flat view only.
type ToolEntry struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	Package     string            `json:"package,omitempty"`
	InstallWith string            `json:"install_with,omitempty"`
	Ignore      bool              `json:"ignore,omitempty"` // auto-ignored on import (e.g. pip library packages)
	Options     map[string]string `json:"options,omitempty"`
}

// UnmarshalJSON — Rejects the old group-owned object form so invalid settings fail at load time.
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

func (e ToolEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Name)
}

func BoolVal(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

func BoolPtr(b bool) *bool { return &b }

func (e ToolEntry) EffectivePackage() string {
	if e.Package != "" {
		return e.Package
	}
	return e.Name
}

type ToolInstallSpec struct {
	Provider    string            `json:"provider"`
	Package     string            `json:"package,omitempty"`
	Bin         string            `json:"bin,omitempty"`
	InstallWith string            `json:"install_with,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
	Source      *FallbackSource   `json:"source,omitempty"`
	Recipe      *FallbackRecipe   `json:"recipe,omitempty"`
	BinDir      string            `json:"bin_dir,omitempty"`
}

func (s ToolInstallSpec) EffectivePackage(logicalName string) string {
	if s.Package != "" {
		return s.Package
	}
	return logicalName
}

type FallbackSource struct {
	Type  string `json:"type"`
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
	URL   string `json:"url,omitempty"`
}

type FallbackRecipe struct {
	Type         string `json:"type,omitempty"`
	AssetPattern string `json:"asset_pattern,omitempty"`
	BinaryPath   string `json:"binary_path,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
	// Checksum is trusted only while this matches the asset being installed; a rotated asset re-verifies from the release checksums file.
	ChecksumAssetID  string `json:"checksum_asset_id,omitempty"`
	ReleaseID        string `json:"release_id,omitempty"`
	TagName          string `json:"tag_name,omitempty"`
	PublishedAt      string `json:"published_at,omitempty"`
	AssetID          string `json:"asset_id,omitempty"`
	AssetName        string `json:"asset_name,omitempty"`
	AssetDownloadURL string `json:"asset_download_url,omitempty"`
	// Normalized version recorded at install time; upgrade detection uses it rather than published_at alone.
	InstalledVersion string `json:"installed_version,omitempty"`
}

type FallbackPlatform struct {
	AssetPattern string `json:"asset_pattern,omitempty"`
	BinaryPath   string `json:"binary_path,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
}

type FallbackCommands struct {
	Install   string `json:"install,omitempty"`
	Check     string `json:"check,omitempty"`
	Uninstall string `json:"uninstall,omitempty"`
	Upgrade   string `json:"upgrade,omitempty"`
	Version   string `json:"version,omitempty"`
}

// FallbackSpec is a best-effort install recipe used when no provider can install a system tool.
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

type ToolSpec struct {
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

func (s ToolSpec) DefaultInstallSpec() ToolInstallSpec {
	if len(s.Providers) > 0 {
		return s.Providers[0]
	}
	return ToolInstallSpec{Provider: s.Provider, Package: s.Package, InstallWith: s.InstallWith, Options: s.Options}
}

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

// ProviderEntry — Carries its own Name and serializes as a full object because Settings.Providers is a list, not a name-keyed map like Tools.
type ProviderEntry struct {
	Name        string                     `json:"name"`
	Provider    string                     `json:"provider"`
	Package     string                     `json:"package,omitempty"`
	InstallWith string                     `json:"install_with,omitempty"`
	Options     map[string]string          `json:"options,omitempty"`
	Variants    []ToolInstallSpec          `json:"variants,omitempty"`
	Hosts       map[string]ToolInstallSpec `json:"hosts,omitempty"`
}

// ToToolSpec — Adapts to ToolSpec to reuse the install-spec resolver (Hosts → Variants-by-availability → default).
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
	AutoImport bool `json:"auto_import,omitempty"`
	// Defers updates until the reported availability timestamp is older than this duration (for example "2d").
	UpdateQuarantine string `json:"update_quarantine,omitempty"`
	// Overrides UpdateQuarantine per provider or manager; concrete values win over logical ones.
	ProviderUpdateQuarantine map[string]string `json:"provider_update_quarantine,omitempty"`
	// Legacy provider-family settings for system, node, and python.
	Ecosystems     map[string]EcosystemSettings `json:"ecosystems,omitempty"`
	FallbackBinDir string                       `json:"fallback_bin_dir,omitempty"`
	// Per-machine path to the dotfiles git repository.
	DotsRepo string `json:"dots_repo,omitempty"`
	// Host-scoped; *bool so an absent host entry (nil) never overrides a global true.
	DotsDisabled *bool `json:"dots_disabled,omitempty"`
	// *bool so nil (absent) means enabled-by-default, distinct from an explicit false.
	AgentsDisabled *bool `json:"agents_disabled,omitempty"`
	// Per-feature switches; agents_disabled remains the master switch.
	SkillsDisabled  *bool `json:"skills_disabled,omitempty"`
	McpDisabled     *bool `json:"mcp_disabled,omitempty"`
	PluginsDisabled *bool `json:"plugins_disabled,omitempty"`
	// Host-scoped agent identifiers ("claude-code", "codex"): nil inherits global, an explicit empty list means no agents.
	AgentsUse []string      `json:"agents_use,omitempty"`
	DotsGit   DotsGitConfig `json:"dots_git"`
	// Host-scoped provider-family names ("system", "node", "python") disabled on this machine.
	DisabledProviders []string `json:"disabled_providers,omitempty"`
	ProviderPriority  []string `json:"provider_priority,omitempty"`
	// Bootstrap providers installed before the rest of a sync, in list order.
	Providers []ProviderEntry `json:"providers,omitempty"`
}

type DotsGitConfig struct {
	AutoCommit bool `json:"auto_commit,omitempty"`
	// AutoPush implies AutoCommit.
	AutoPush bool `json:"auto_push,omitempty"`
}

type DotVariant struct {
	// Empty means the dot entry's default package.
	Package string `json:"package,omitempty"`
}

type DotEntry struct {
	// Also the default stow package name when Package is empty.
	Name string `json:"name"`
	// The original filesystem location being managed (e.g. "~/.config/nvim"), not the repo copy.
	Path string `json:"path,omitempty"`
	// Empty means Name.
	Package string                `json:"package,omitempty"`
	Hosts   map[string]DotVariant `json:"hosts,omitempty"`
	// Ignored keeps the entry visible while preventing sync/discovery from managing it.
	Ignored bool `json:"ignored,omitempty"`
	// Ignore holds glob patterns for files to skip within this entry.
	Ignore []string `json:"ignore,omitempty"`
	// Empty means manual resolution (sync errors on conflict); "use_repo" relinks the repo version, "use_local" adopts local content.
	OnConflict string `json:"on_conflict,omitempty"`
}

func (e DotEntry) EffectivePackage() string {
	if strings.TrimSpace(e.Package) != "" {
		return e.Package
	}
	return e.Name
}

// PackageForHost — Host names are matched exactly; callers own normalization.
func (e DotEntry) PackageForHost(host string) string {
	if strings.TrimSpace(host) != "" && e.Hosts != nil {
		if variant, ok := e.Hosts[host]; ok && strings.TrimSpace(variant.Package) != "" {
			return variant.Package
		}
	}
	return e.EffectivePackage()
}

type GlobalIgnore struct {
	Tools []string `json:"tools,omitempty"`
	Dots  []string `json:"dots,omitempty"`
}

// HostAssignment is an in-memory app view; it is never persisted in this shape.
type HostAssignment struct {
	Groups []string `json:"groups,omitempty"`
	Ignore []string `json:"ignore,omitempty"`
}

type GroupConfig struct {
	Name         string      `json:"name,omitempty"`
	Special      string      `json:"special,omitempty"`
	Description  string      `json:"description,omitempty"`
	Taps         []string    `json:"taps,omitempty"`
	Tools        []ToolEntry `json:"tools,omitempty"`
	Dots         []DotEntry  `json:"dots,omitempty"`
	Skills       []string    `json:"skills,omitempty"`
	McpServers   []string    `json:"mcp_servers,omitempty"`
	Plugins      []string    `json:"plugins,omitempty"`
	Marketplaces []string    `json:"marketplaces,omitempty"`
	Ignore       []string    `json:"-"`
}

const SystemInventoryGroup = "provider-inventory"

func (g *GroupConfig) IsHost() bool { return g != nil && g.Special == "host" }

// IsSystemInventory — System inventory is excluded from user-facing tool views.
func (g *GroupConfig) IsSystemInventory() bool {
	return g != nil && g.Special == SystemInventoryGroup
}

func (g *GroupConfig) GroupName() string { return g.Name }

func (g *GroupConfig) BaseName() string {
	return g.Name
}

type RootConfig struct {
	// Injected on every write for editor support; read back on load but never acted on.
	Schema string `json:"$schema,omitempty"`
	// Fragment paths relative to the main settings file's directory; stripped before save.
	Include []string `json:"$include,omitempty"`
	// Advisory messages from $include merging; populated on load, surfaced by settings lint, never persisted.
	MergeNotices []string `json:"-"`
	// Missing/zero means the legacy unversioned format and is migrated to CurrentVersion on load.
	Version  int                 `json:"version"`
	Settings Settings            `json:"settings"`
	Tools    map[string]ToolSpec `json:"tools,omitempty"`
	Hosts    map[string][]string `json:"hosts,omitempty"`
	Ignore   GlobalIgnore        `json:"ignore,omitempty"`
	Groups   []*GroupConfig      `json:"groups,omitempty"`
	// Per-machine overrides keyed by short hostname; AutoImport and DotsGit are global-only.
	HostSettings map[string]Settings `json:"host_settings,omitempty"`
	Agents       AgentsConfig        `json:"agents,omitempty"`
}

type AgentsConfig struct {
	Packages     []SkillPackage `json:"packages,omitempty"`
	McpServers   []McpServer    `json:"mcp_servers,omitempty"`
	Marketplaces []Marketplace  `json:"marketplaces,omitempty"`
	Plugins      []Plugin       `json:"plugins,omitempty"`
	// Legacy per-skill manifest kept only for one-time migration into Packages; never written back.
	Skills []ManifestSkill `json:"skills,omitempty"`
	Ignore AgentsIgnore    `json:"ignore,omitempty"`
}

type AgentsIgnore struct {
	Skills       []string `json:"skills,omitempty"`
	McpServers   []string `json:"mcp_servers,omitempty"`
	Plugins      []string `json:"plugins,omitempty"`
	Marketplaces []string `json:"marketplaces,omitempty"`
}

// SkillPackage — Missing Skills means every skill discovered from the source.
type SkillPackage struct {
	Source string   `json:"source"`
	Ref    string   `json:"ref,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Agents []string `json:"agents,omitempty"`
}

// McpServer — Transport "stdio" requires Command, "http"/"sse" require URL; Env names resolve from the environment at restore time and empty Agents means all.
type McpServer struct {
	Name       string            `json:"name"`
	Transport  string            `json:"transport"`
	Command    string            `json:"command,omitempty"`
	URL        string            `json:"url,omitempty"`
	Env        []string          `json:"env,omitempty"`
	EnvLiteral map[string]string `json:"env_literal,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Agents     []string          `json:"agents,omitempty"`
}

// Marketplace — Source is whatever form the agent CLIs accept for marketplace add (owner/repo or URL).
type Marketplace struct {
	Name   string   `json:"name"`
	Source string   `json:"source"`
	Agents []string `json:"agents,omitempty"`
}

// Plugin — Exactly one of Marketplace or Source identifies where the agent installs the plugin from.
type Plugin struct {
	Name        string   `json:"name"`
	Marketplace string   `json:"marketplace,omitempty"`
	Source      string   `json:"source,omitempty"`
	Agents      []string `json:"agents,omitempty"`
}

// ManifestSkill — Legacy per-skill entry, kept for migration only.
type ManifestSkill struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Ref       string   `json:"ref,omitempty"`
	SkillPath string   `json:"skill_path,omitempty"`
	Agents    []string `json:"agents,omitempty"`
}

func MigrateSkillPackages(cfg *RootConfig) {
	if len(cfg.Agents.Skills) == 0 {
		return
	}
	index := make(map[string]int, len(cfg.Agents.Packages))
	existing := make(map[string]bool, len(cfg.Agents.Packages))
	for i, p := range cfg.Agents.Packages {
		index[p.Source] = i
		existing[p.Source] = true
	}
	for _, s := range cfg.Agents.Skills {
		if s.Source == "" {
			continue
		}
		i, ok := index[s.Source]
		if !ok {
			skills := []string(nil)
			if s.Name != "" {
				skills = []string{s.Name}
			}
			cfg.Agents.Packages = append(cfg.Agents.Packages, SkillPackage{
				Source: s.Source,
				Ref:    s.Ref,
				Skills: skills,
				Agents: s.Agents,
			})
			index[s.Source] = len(cfg.Agents.Packages) - 1
			continue
		}
		p := &cfg.Agents.Packages[i]
		if p.Ref == "" {
			p.Ref = s.Ref
		}
		for _, agent := range s.Agents {
			if !slices.Contains(p.Agents, agent) {
				p.Agents = append(p.Agents, agent)
			}
		}
		if s.Name == "" || (existing[s.Source] && len(p.Skills) == 0) {
			continue
		}
		found := false
		for _, name := range p.Skills {
			if name == s.Name {
				found = true
				break
			}
		}
		if !found {
			p.Skills = append(p.Skills, s.Name)
		}
	}
	cfg.Agents.Skills = nil
}

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
			if len(hostEco.Priority) > 0 {
				eco.Priority = append([]string(nil), hostEco.Priority...)
			}
			s.Ecosystems[name] = eco
		}
	}
	if hs.DotsRepo != "" {
		s.DotsRepo = hs.DotsRepo
	}
	if hs.DotsDisabled != nil {
		s.DotsDisabled = hs.DotsDisabled
	}
	if hs.AgentsDisabled != nil {
		s.AgentsDisabled = hs.AgentsDisabled
	}
	if hs.SkillsDisabled != nil {
		s.SkillsDisabled = hs.SkillsDisabled
	}
	if hs.McpDisabled != nil {
		s.McpDisabled = hs.McpDisabled
	}
	if hs.PluginsDisabled != nil {
		s.PluginsDisabled = hs.PluginsDisabled
	}
	if hs.DisabledProviders != nil {
		s.DisabledProviders = cloneStringSlice(hs.DisabledProviders)
	}
	if hs.ProviderPriority != nil {
		s.ProviderPriority = cloneStringSlice(hs.ProviderPriority)
	}
	// A non-nil host list, including empty, replaces the global list entirely.
	if hs.AgentsUse != nil {
		s.AgentsUse = cloneStringSlice(hs.AgentsUse)
	}
	if hs.Providers != nil {
		s.Providers = cloneProviders(hs.Providers)
	}
	return s
}

// Config is a flat syncer view built from RootConfig at sync time; it is never persisted.
type Config struct {
	Settings Settings    `json:"settings"`
	Taps     []string    `json:"taps,omitempty"`
	Tools    []ToolEntry `json:"tools"`
}

// ValidationError — Warn marks soft advisory problems that do not block config loading.
type ValidationError struct {
	Path    string
	Message string
	Warn    bool
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

// ProviderValidation carries provider identities so config need not import the provider registry.
type ProviderValidation struct {
	Known              []string
	Ecosystems         []string
	ConcreteEcosystems map[string]string
}

// ValidateRoot — Pass a zero ProviderValidation to skip provider validation.
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
			if spec.Recipe != nil && strings.TrimSpace(spec.Recipe.Type) != "" {
				return validateRecipeInstallSpec(path, spec)
			}
			return validateScriptSpec(path, spec)
		}
		if spec.Provider == "apt_repo" {
			// A recipe supplies setup and packages only once materialized, so validate the recipe instead.
			if spec.Recipe != nil && strings.TrimSpace(spec.Recipe.Type) != "" {
				return validateRecipeInstallSpec(path, spec)
			}
			return validateAptRepoSpec(path, spec)
		}
		if spec.Provider == "" {
			errs = append(errs, ValidationError{Path: path + ".provider", Message: "provider is required"})
		} else if len(providerSet) > 0 {
			if _, ok := providerSet[spec.Provider]; !ok {
				errs = append(errs, ValidationError{Path: path + ".provider", Message: fmt.Sprintf("unknown provider %q", spec.Provider)})
			} else if _, ok := ecosystemSet[spec.Provider]; ok {
				errs = append(errs, ValidationError{Path: path + ".provider", Message: fmt.Sprintf("provider family %q is not supported in tools providers", spec.Provider)})
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

	pkgSources := make(map[string]struct{}, len(cfg.Agents.Packages))
	for i, pkg := range cfg.Agents.Packages {
		path := fmt.Sprintf("$.agents.packages[%d]", i)
		if strings.TrimSpace(pkg.Source) == "" {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "skill package source is required"})
			continue
		}
		errs = append(errs, validateSkillSelector(path, pkg.Skills)...)
		pkgSources[pkg.Source] = struct{}{}
	}
	errs = append(errs, validateMcpServers(cfg.Agents.McpServers, "$.agents.mcp_servers")...)
	marketplaceNames := make(map[string]struct{}, len(cfg.Agents.Marketplaces))
	errs = append(errs, validateMarketplaces(cfg.Agents.Marketplaces, marketplaceNames, "$.agents.marketplaces")...)
	errs = append(errs, validatePlugins(cfg.Agents.Plugins, marketplaceNames, "$.agents.plugins")...)

	mcpServerNames := make(map[string]struct{}, len(cfg.Agents.McpServers))
	for _, s := range cfg.Agents.McpServers {
		if strings.TrimSpace(s.Name) != "" {
			mcpServerNames[s.Name] = struct{}{}
		}
	}

	pluginNames := make(map[string]struct{}, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		if strings.TrimSpace(p.Name) != "" {
			pluginNames[p.Name] = struct{}{}
		}
	}

	groupNames := make(map[string]struct{}, len(cfg.Groups))
	groupByName := make(map[string]*GroupConfig, len(cfg.Groups))
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
		if g.Special != "" && g.Special != "host" && g.Special != SystemInventoryGroup {
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
			// Same invariant as tools: unlimited host groups, one reusable group.
			if !g.IsHost() {
				if first, ok := dotMemberships[dot.Name]; ok {
					errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("dotfile %q already belongs to reusable group %q; an item may belong to at most one reusable group", dot.Name, first.BaseName())})
				} else {
					dotMemberships[dot.Name] = g
				}
			}
		}
		for si, src := range g.Skills {
			if IsAgentRef(src) {
				continue
			}
			if _, ok := pkgSources[src]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("$.groups[%d].skills[%d]", gi, si),
					Message: fmt.Sprintf("group skill ref %q has no matching package", src),
				})
			}
		}
		for mi, name := range g.McpServers {
			if IsAgentRef(name) {
				continue
			}
			if _, ok := mcpServerNames[name]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("$.groups[%d].mcp_servers[%d]", gi, mi),
					Message: fmt.Sprintf("group mcp_server ref %q has no matching mcp_server in agents.mcp_servers", name),
					Warn:    true,
				})
			}
		}
		for pi, name := range g.Plugins {
			if IsAgentRef(name) {
				continue
			}
			if _, ok := pluginNames[name]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("$.groups[%d].plugins[%d]", gi, pi),
					Message: fmt.Sprintf("group plugin ref %q has no matching plugin in agents.plugins", name),
					Warn:    true,
				})
			}
		}
		for mi, name := range g.Marketplaces {
			if IsAgentRef(name) {
				continue
			}
			if _, ok := marketplaceNames[name]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("$.groups[%d].marketplaces[%d]", gi, mi),
					Message: fmt.Sprintf("group marketplace ref %q has no matching marketplace in agents.marketplaces", name),
					Warn:    true,
				})
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
	}
	for i, ignored := range cfg.Ignore.Dots {
		if strings.TrimSpace(ignored) == "" {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("$.ignore.dots[%d]", i), Message: "dotfile name is required"})
		}
	}
	return errs
}

// An empty selector means "every skill in the package", so a blank entry is never the narrowing the author wrote it to express.
func validateSkillSelector(path string, skills []string) []ValidationError {
	var errs []ValidationError
	seen := make(map[string]struct{}, len(skills))
	for i, skill := range skills {
		name := strings.TrimSpace(skill)
		if name == "" {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("%s.skills[%d]", path, i), Message: "skill name is required"})
			continue
		}
		if _, dup := seen[name]; dup {
			errs = append(errs, ValidationError{Path: fmt.Sprintf("%s.skills[%d]", path, i), Message: fmt.Sprintf("duplicate skill %q", name), Warn: true})
			continue
		}
		seen[name] = struct{}{}
	}
	return errs
}

func validateMcpServers(servers []McpServer, path string) []ValidationError {
	var errs []ValidationError
	seen := make(map[string]struct{}, len(servers))
	for i, s := range servers {
		p := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(s.Name) == "" {
			errs = append(errs, ValidationError{Path: p + ".name", Message: "mcp_server name must not be empty"})
		} else if _, dup := seen[s.Name]; dup {
			errs = append(errs, ValidationError{Path: p + ".name", Message: fmt.Sprintf("duplicate mcp_server name %q", s.Name)})
		}
		seen[s.Name] = struct{}{}
		for j, envName := range s.Env {
			if strings.TrimSpace(envName) == "" {
				errs = append(errs, ValidationError{Path: fmt.Sprintf("%s.env[%d]", p, j), Message: "env var name must not be empty"})
			}
		}
		for name := range s.Headers {
			if !validHTTPHeaderName(name) {
				errs = append(errs, ValidationError{Path: p + ".headers", Message: fmt.Sprintf("invalid HTTP header name %q", name)})
			}
		}
		switch s.Transport {
		case "stdio":
			if strings.TrimSpace(s.Command) == "" {
				errs = append(errs, ValidationError{Path: p + ".command", Message: "stdio transport requires command"})
			}
			if s.URL != "" {
				errs = append(errs, ValidationError{Path: p + ".url", Message: "stdio transport must not set url"})
			}
			if len(s.Headers) > 0 {
				errs = append(errs, ValidationError{Path: p + ".headers", Message: "stdio transport must not set headers"})
			}
		case "http", "sse":
			if strings.TrimSpace(s.URL) == "" {
				errs = append(errs, ValidationError{Path: p + ".url", Message: fmt.Sprintf("%s transport requires url", s.Transport)})
			}
			if s.Command != "" {
				errs = append(errs, ValidationError{Path: p + ".command", Message: fmt.Sprintf("%s transport must not set command", s.Transport)})
			}
		default:
			errs = append(errs, ValidationError{Path: p + ".transport", Message: fmt.Sprintf("unknown transport %q; must be stdio, http, or sse", s.Transport)})
		}
	}
	return errs
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		}
		return false
	}
	return true
}

func validateMarketplaces(marketplaces []Marketplace, names map[string]struct{}, path string) []ValidationError {
	var errs []ValidationError
	for i, mkt := range marketplaces {
		p := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(mkt.Name) == "" {
			errs = append(errs, ValidationError{Path: p + ".name", Message: "marketplace name must not be empty"})
			continue
		}
		if _, dup := names[mkt.Name]; dup {
			errs = append(errs, ValidationError{Path: p + ".name", Message: fmt.Sprintf("duplicate marketplace name %q", mkt.Name)})
		}
		names[mkt.Name] = struct{}{}
		if strings.TrimSpace(mkt.Source) == "" {
			errs = append(errs, ValidationError{Path: p + ".source", Message: "marketplace source must not be empty"})
		}
	}
	return errs
}

// validatePlugins rejects ambiguous sources and dangling marketplace references because they make restore impossible.
func validatePlugins(plugins []Plugin, marketplaceNames map[string]struct{}, path string) []ValidationError {
	var errs []ValidationError
	seen := make(map[string]struct{}, len(plugins))
	for i, p := range plugins {
		pp := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(p.Name) == "" {
			errs = append(errs, ValidationError{Path: pp + ".name", Message: "plugin name must not be empty"})
		} else if _, dup := seen[p.Name]; dup {
			errs = append(errs, ValidationError{Path: pp + ".name", Message: fmt.Sprintf("duplicate plugin name %q", p.Name)})
		}
		seen[p.Name] = struct{}{}
		marketplace := strings.TrimSpace(p.Marketplace)
		source := strings.TrimSpace(p.Source)
		if (marketplace == "") == (source == "") {
			errs = append(errs, ValidationError{Path: pp, Message: "plugin requires exactly one of marketplace or source"})
			continue
		}
		if marketplace != "" {
			if _, ok := marketplaceNames[marketplace]; !ok {
				errs = append(errs, ValidationError{Path: pp + ".marketplace", Message: fmt.Sprintf("plugin marketplace %q has no matching agents.marketplaces entry", p.Marketplace)})
			}
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
		// raw_commands uses its own installer; other recipes need owner/repo to locate the release.
		if fallback.Recipe.Type != FallbackRecipeRawCommands &&
			(strings.TrimSpace(fallback.Source.Owner) == "" || strings.TrimSpace(fallback.Source.Repo) == "") {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "github fallback source requires owner and repo"})
		}
	default:
		errs = append(errs, ValidationError{Path: path + ".source.type", Message: fmt.Sprintf("unknown fallback source type %q", fallback.Source.Type)})
	}

	// FallbackSpec.Recipe is a distinct type, so validateRecipeInstallSpec never reaches this URL.
	if downloadURL := strings.TrimSpace(fallback.Recipe.AssetDownloadURL); downloadURL != "" && !IsHTTPSURL(downloadURL) {
		errs = append(errs, ValidationError{Path: path + ".recipe.asset_download_url", Message: errAssetDownloadURLScheme})
	}

	status := fallback.Status
	if status == "" {
		status = FallbackStatusUnverified
	}
	switch status {
	case FallbackStatusUnresolved, FallbackStatusUnsupported, FallbackStatusUnverified, FallbackStatusVerified, FallbackStatusFailed:
	default:
		errs = append(errs, ValidationError{Path: path + ".status", Message: fmt.Sprintf("unknown fallback status %q", fallback.Status)})
	}
	// Native release-asset recipes verify through the binary; shell recipes need an explicit check.
	if fallback.Recipe.Type != FallbackRecipeGitHubReleaseAsset &&
		status != FallbackStatusUnresolved && status != FallbackStatusUnsupported &&
		strings.TrimSpace(fallback.Commands.Check) == "" {
		errs = append(errs, ValidationError{Path: path + ".commands.check", Message: "fallback check command is required unless status is unresolved or unsupported"})
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

// validateProviderSpec accepts concrete bootstrap providers because they install through concrete managers.
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

const (
	errAptRepoKeyURLScheme        = "options.key_url must use https; the key is fetched and trusted by the root apt keyring, so plain http lets an on-path attacker choose it"
	errCurlInstallScriptURLScheme = "options.url must use https; the response is piped straight into a shell, so plain http lets an on-path attacker run arbitrary code"
	errAssetDownloadURLScheme     = "recipe.asset_download_url must use https; the asset is downloaded and made executable, so plain http lets an on-path attacker choose the binary"
)

// IsHTTPSURL gates executed or trusted content with no loopback or file exception.
func IsHTTPSURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "https") && parsed.Host != ""
}

func validateRecipeInstallSpec(path string, spec ToolInstallSpec) []ValidationError {
	var errs []ValidationError
	if spec.Recipe == nil || strings.TrimSpace(spec.Recipe.Type) == "" {
		errs = append(errs, ValidationError{Path: path + ".recipe.type", Message: "recipe type is required"})
		return errs
	}
	switch spec.Recipe.Type {
	case FallbackRecipeCurlInstallScript:
		switch scriptURL := curlInstallScriptURL(spec); {
		case scriptURL == "":
			errs = append(errs, ValidationError{Path: path + ".options.url", Message: "curl_install_script requires options.url or source.url"})
		case !IsHTTPSURL(scriptURL):
			errs = append(errs, ValidationError{Path: path + ".options.url", Message: errCurlInstallScriptURLScheme})
		}
	case FallbackRecipeGitHubReleaseAsset:
		if downloadURL := strings.TrimSpace(spec.Recipe.AssetDownloadURL); downloadURL != "" && !IsHTTPSURL(downloadURL) {
			errs = append(errs, ValidationError{Path: path + ".recipe.asset_download_url", Message: errAssetDownloadURLScheme})
		}
		if spec.Source == nil || spec.Source.Type != FallbackSourceGitHub {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "github_release_asset requires source.type github"})
		} else if strings.TrimSpace(spec.Source.Owner) == "" || strings.TrimSpace(spec.Source.Repo) == "" {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "github_release_asset requires source.owner and source.repo"})
		}
		if strings.TrimSpace(spec.Recipe.AssetPattern) == "" {
			errs = append(errs, ValidationError{Path: path + ".recipe.asset_pattern", Message: "github_release_asset requires recipe.asset_pattern"})
		}
	case FallbackRecipeAptRepo:
		keyURL := optionValue(spec.Options, "key_url")
		switch {
		case keyURL == "":
			errs = append(errs, ValidationError{Path: path + ".options.key_url", Message: "apt_repo requires options.key_url"})
		case !IsHTTPSURL(keyURL):
			errs = append(errs, ValidationError{Path: path + ".options.key_url", Message: errAptRepoKeyURLScheme})
		}
		if optionValue(spec.Options, "signed_by") == "" {
			errs = append(errs, ValidationError{Path: path + ".options.signed_by", Message: "apt_repo requires options.signed_by"})
		}
		if optionValue(spec.Options, "sources_format") == "" {
			errs = append(errs, ValidationError{Path: path + ".options.sources_format", Message: "apt_repo requires options.sources_format"})
		}
		// Without this the recipe path falls back to the logical tool name and installs the wrong package.
		if optionValue(spec.Options, "packages") == "" && strings.TrimSpace(spec.Package) == "" {
			errs = append(errs, ValidationError{Path: path + ".options.packages", Message: "apt_repo requires options.packages"})
		}
	default:
		errs = append(errs, ValidationError{Path: path + ".recipe.type", Message: fmt.Sprintf("unknown install recipe type %q", spec.Recipe.Type)})
	}
	return errs
}

func validateAptRepoSpec(path string, spec ToolInstallSpec) []ValidationError {
	var errs []ValidationError
	if strings.TrimSpace(spec.Options["setup"]) == "" {
		errs = append(errs, ValidationError{Path: path + ".options.setup", Message: "apt_repo requires options.setup or a materialized setup command"})
	}
	if strings.TrimSpace(spec.Options["packages"]) == "" && strings.TrimSpace(spec.Package) == "" {
		errs = append(errs, ValidationError{Path: path + ".options.packages", Message: "apt_repo requires options.packages"})
	}
	return errs
}

// validateScriptSpec leaves options.latest unconstrained because its version sources do not exist yet at validation time.
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
