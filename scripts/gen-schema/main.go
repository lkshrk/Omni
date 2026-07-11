// gen-schema writes the JSON Schema for omni's settings.json to the current
// versioned schema path and spec/omni.settings.schema.json (relative to the
// repository root).
//
// Usage:
//
//	go run ./scripts/gen-schema          # writes the canonical and latest schemas
//	go run ./scripts/gen-schema [path]   # writes to a custom path
//
// Run via make:
//
//	make gen-schema
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	schemaMetaURL  = "https://json-schema.org/draft/2020-12/schema"
	latestOutput   = "spec/omni.settings.schema.json"
	latestSchemaID = "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.schema.json"
)

// schema is a minimal JSON Schema 2020-12 node. Fields with zero values are omitted.
// Field order here is the serialisation order — matches the conventional layout:
// meta → annotations → type/validation → object → array → $defs last.
type schema struct {
	// Meta (root only)
	Schema string `json:"$schema,omitempty"`
	ID     string `json:"$id,omitempty"`

	// Core ref
	Ref string `json:"$ref,omitempty"`

	// Annotations
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	Examples    []any  `json:"examples,omitempty"`

	// Type / value constraints
	Type      any   `json:"type,omitempty"` // string or []string
	Const     any   `json:"const,omitempty"`
	Enum      []any `json:"enum,omitempty"`
	MinLength int   `json:"minLength,omitempty"`

	// Object constraints
	Properties           map[string]*schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"` // bool or *schema

	// Array constraints
	Items *schema `json:"items,omitempty"`

	// Definitions (conventionally last)
	Defs map[string]*schema `json:"$defs,omitempty"`
}

func build() *schema {
	return buildWithID(config.SchemaURL)
}

func currentOutput() string {
	return fmt.Sprintf("spec/omni.settings.v%d.schema.json", config.CurrentVersion)
}

func buildWithID(id string) *schema {
	ecosystemNames := stringsToAny(provider.BuiltinEcosystemNames())
	managerNames := stringsToAny(provider.BuiltinSettingsManagerNames(provider.EcosystemNode))
	managerNames = append(managerNames, stringsToAny(provider.BuiltinSettingsManagerNames(provider.EcosystemPython))...)
	providerNames := stringsToAny(provider.BuiltinConcreteEcosystemNames())
	providerNames = append(providerNames, "script")
	installWithNames := stringsToAny(provider.BuiltinConcreteEcosystemNames())
	installWithExamples := stringsToAny(exampleInstallWithNames())
	systemPriority := stringsToAny(provider.BuiltinSystemProviderPriorityNames())
	return &schema{
		Schema:      schemaMetaURL,
		ID:          id,
		Title:       "omni settings",
		Description: "omni settings.json — manages all dev tools, dotfiles, host assignments, and groups from a single file.",
		Type:        "object",
		Properties: map[string]*schema{
			"$schema": {
				Description: "JSON Schema reference URI (injected automatically by omni on every write).",
				Type:        "string",
			},
			"$include": {
				Description: "Additional settings fragments to merge into this file on load. Paths are relative to the directory containing this settings file. Stripped before save.",
				Type:        "array",
				Items:       &schema{Type: "string", MinLength: 1},
			},
			"version": {
				Description: "settings.json format version. Missing means legacy unversioned config; omni migrates it to the current version on write.",
				Type:        "integer",
				Const:       config.CurrentVersion,
				Default:     config.CurrentVersion,
				Examples:    []any{config.CurrentVersion},
			},
			"settings": ref("#/$defs/Settings"),
			"hosts": {
				Description: "Reusable group assignments keyed by short hostname. The matching special host group is active implicitly and must not be listed here.",
				Type:        "object",
				AdditionalProperties: &schema{
					Type:  "array",
					Items: &schema{Type: "string", MinLength: 1},
				},
				Examples: []any{
					map[string]any{"macbook-pro": []any{"work", "personal"}, "home-desktop": []any{"personal"}},
				},
			},
			"ignore": {
				Description:          "Tool and dotfile names skipped globally.",
				Type:                 "object",
				AdditionalProperties: false,
				Properties: map[string]*schema{
					"tools": {Type: "array", Items: &schema{Type: "string", MinLength: 1}},
					"dots":  {Type: "array", Items: &schema{Type: "string", MinLength: 1}},
				},
				Examples: []any{
					map[string]any{"tools": []any{"slack"}, "dots": []any{"work-secrets"}},
				},
			},
			"host_settings": {
				Description:          "Per-host setting overrides keyed by short hostname.",
				Type:                 "object",
				AdditionalProperties: ref("#/$defs/HostSettings"),
				Examples: []any{
					map[string]any{"macbook-pro": map[string]any{"dots_disabled": false}},
				},
			},
			"tools": {
				Description:          "Logical tool specs keyed by logical tool name. Groups reference these keys by name.",
				Type:                 "object",
				AdditionalProperties: ref("#/$defs/ToolSpec"),
				Examples: []any{
					map[string]any{
						"ripgrep": map[string]any{
							"providers": []any{
								map[string]any{"provider": "brew", "package": "ripgrep"},
								map[string]any{"provider": "apt", "package": "ripgrep"},
							},
						},
					},
				},
			},
			"groups": {
				Description: "Tool/dots groups. Host groups use special=\"host\" and are reserved for one hostname.",
				Type:        "array",
				Items:       ref("#/$defs/GroupConfig"),
			},
			"agents": ref("#/$defs/AgentsConfig"),
		},
		Required:             []string{"version"},
		AdditionalProperties: false,
		Defs: map[string]*schema{
			"Settings": {
				Description: "User-configurable options stored in settings.json.",
				Type:        "object",
				Properties: map[string]*schema{
					"auto_import": {
						Description: "Add newly discovered installed tools to the config on every sync.",
						Type:        "boolean",
						Default:     false,
					},
					"ecosystems": {
						Description: "Legacy provider-family settings for system, node, and python. New config should prefer provider_priority and tool providers[].",
						Type:        "object",
						Properties: map[string]*schema{
							provider.EcosystemSystem: ref("#/$defs/EcosystemSettings"),
							provider.EcosystemNode:   ref("#/$defs/EcosystemSettings"),
							provider.EcosystemPython: ref("#/$defs/EcosystemSettings"),
						},
						AdditionalProperties: ref("#/$defs/EcosystemSettings"),
						Examples: []any{map[string]any{
							provider.EcosystemSystem: map[string]any{"priority": systemPriority},
							provider.EcosystemNode:   map[string]any{"manager": firstString(provider.BuiltinManagerNames(provider.EcosystemNode))},
							provider.EcosystemPython: map[string]any{"manager": firstString(provider.BuiltinManagerNames(provider.EcosystemPython))},
						}},
					},
					"fallback_bin_dir": {
						Description: "Default directory for fallback-installed binaries. Omni warns when this directory is not on PATH.",
						Type:        "string",
						Examples:    []any{"~/.local/share/omni/fallback/bin"},
					},
					"dots_repo": {
						Description: "Per-machine path to the dotfiles git repository (~ is expanded).",
						Type:        "string",
						Examples:    []any{"~/Dev/dotfiles", "~/.dotfiles"},
					},
					"dots_disabled": {
						Description: "Disable dotfile sync for this settings scope.",
						Type:        "boolean",
					},
					"agents_disabled": {
						Description: "Master switch: disable the agent skills/mcp/plugins features for this settings scope.",
						Type:        "boolean",
					},
					"skills_disabled": {
						Description: "Disable the agent skills feature for this settings scope.",
						Type:        "boolean",
					},
					"mcp_disabled": {
						Description: "Disable the agent mcp feature for this settings scope.",
						Type:        "boolean",
					},
					"plugins_disabled": {
						Description: "Disable the agent plugins feature for this settings scope.",
						Type:        "boolean",
					},
					"dots_git": ref("#/$defs/DotsGitConfig"),
				},
				AdditionalProperties: false,
			},
			"HostSettings": {
				Description: "Per-host setting overrides.",
				Type:        "object",
				Properties: map[string]*schema{
					"ecosystems": {
						Description: "Legacy host-specific provider-family manager and priority overrides.",
						Type:        "object",
						Properties: map[string]*schema{
							provider.EcosystemSystem: ref("#/$defs/EcosystemSettings"),
							provider.EcosystemNode:   ref("#/$defs/EcosystemSettings"),
							provider.EcosystemPython: ref("#/$defs/EcosystemSettings"),
						},
						AdditionalProperties: ref("#/$defs/EcosystemSettings"),
					},
					"dots_repo": {
						Description: "Host-specific path to the dotfiles git repository.",
						Type:        "string",
						Examples:    []any{"~/Dev/dotfiles", "~/.dotfiles"},
					},
					"dots_disabled": {
						Description: "Whether dotfile sync is disabled on this host.",
						Type:        "boolean",
					},
					"agents_disabled": {
						Description: "Master switch: whether the agent skills/mcp/plugins features are disabled on this host.",
						Type:        "boolean",
					},
					"skills_disabled": {
						Description: "Whether the agent skills feature is disabled on this host.",
						Type:        "boolean",
					},
					"mcp_disabled": {
						Description: "Whether the agent mcp feature is disabled on this host.",
						Type:        "boolean",
					},
					"plugins_disabled": {
						Description: "Whether the agent plugins feature is disabled on this host.",
						Type:        "boolean",
					},
					"disabled_providers": {
						Description: "Provider family names disabled on this host.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{ecosystemNames},
					},
				},
				AdditionalProperties: false,
			},
			"EcosystemSettings": {
				Description: "Legacy settings for one provider family.",
				Type:        "object",
				Properties: map[string]*schema{
					"manager": {
						Description: "Concrete manager used by manager-backed provider families. Omit to auto-detect.",
						Type:        "string",
						Examples:    managerNames,
					},
					"priority": {
						Description: "Ordered concrete provider/manager priority for this provider family.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{systemPriority},
					},
				},
				AdditionalProperties: false,
			},
			"DotsGitConfig": {
				Description: "Controls git behaviour for the dots repository.",
				Type:        "object",
				Properties: map[string]*schema{
					"auto_commit": {
						Description: "Run 'git commit' automatically after add/remove operations.",
						Type:        "boolean",
						Default:     false,
					},
					"auto_push": {
						Description: "Run 'git push' after add/remove (implies auto_commit).",
						Type:        "boolean",
						Default:     false,
					},
				},
				AdditionalProperties: false,
			},
			"GroupConfig": {
				Description: "A named collection of logical tool memberships and dotfile entries.",
				Type:        "object",
				Properties: map[string]*schema{
					"name": {
						Description: "Group identifier.",
						Type:        "string",
						Examples:    []any{"work", "personal", "media"},
					},
					"special": {
						Description: "Reserved marker for protected host groups.",
						Type:        "string",
						Enum:        []any{"host"},
						Examples:    []any{"host"},
					},
					"description": {
						Description: "Human-readable description shown in 'omni groups'.",
						Type:        "string",
						Examples:    []any{"Work tools and configs"},
					},
					"taps": {
						Description: "Legacy Homebrew taps for this group. Prefer tool-level 'taps' on logical tool specs.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"homebrew/cask-fonts"}},
					},
					"tools": {
						Description: "Logical tool names installed by this group.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"ripgrep", "fd", "black"}},
					},
					"dots": {
						Description: "Dotfile entries managed by this group.",
						Type:        "array",
						Items:       ref("#/$defs/DotEntry"),
					},
					"skills": {
						Description: "Skill package sources active on this group's hosts.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
					},
					"mcp_servers": {
						Description: "MCP server names active on this group's hosts.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
					},
					"plugins": {
						Description: "Plugin names active on this group's hosts.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
					},
					"marketplaces": {
						Description: "Marketplace names assigned to this group.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
					},
				},
				AdditionalProperties: false,
			},
			"ToolSpec": {
				Description: "A logical tool spec. Providers define concrete install candidates; legacy provider, variants, and hosts fields are migrated on load.",
				Type:        "object",
				Properties: map[string]*schema{
					"providers": {
						Description: "Concrete provider candidates for this logical tool, in priority order.",
						Type:        "array",
						Items:       ref("#/$defs/ToolInstallSpec"),
						Examples: []any{
							[]any{
								map[string]any{"provider": "brew", "package": "ripgrep"},
								map[string]any{"provider": "apt", "package": "ripgrep"},
							},
						},
					},
					"provider": {
						Description: "Legacy provider field accepted for migration. Use providers[] for new config.",
						Type:        "string",
						Enum:        providerNames,
						Examples:    []any{"brew"},
					},
					"install_with": {
						Description: "Legacy concrete provider or manager pin accepted for migration. Use providers[].provider for new config.",
						Type:        "string",
						Enum:        installWithNames,
						Examples:    installWithExamples,
					},
					"package": {
						Description: "Package identifier passed to the provider. Defaults to the logical tool name.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"ripgrep", "typescript"},
					},
					"git": {
						Description: "Upstream git repository URL for this tool. Brew metadata refresh and install-from-search may populate GitHub URLs here for later fallback setup.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"https://github.com/BurntSushi/ripgrep"},
					},
					"options": {
						Description:          "Provider-specific install options (key-value pairs).",
						Type:                 "object",
						AdditionalProperties: &schema{Type: "string"},
					},
					"taps": {
						Description: "Homebrew taps required before installing this tool.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"homebrew/core"}},
					},
					"ignore": {
						Description: "Ignore this logical tool globally. Used for imported libraries/packages that should not be managed.",
						Type:        "boolean",
						Default:     false,
					},
					"variants": {
						Description: "Alternate install candidates tried in config order when the default provider is unavailable.",
						Type:        "array",
						Items:       ref("#/$defs/ToolInstallSpec"),
					},
					"hosts": {
						Description:          "Strict hostname-specific install overrides keyed by full or short hostname.",
						Type:                 "object",
						AdditionalProperties: ref("#/$defs/ToolInstallSpec"),
						Examples: []any{
							map[string]any{"linuxbox": map[string]any{"provider": "apt", "package": "ripgrep"}},
						},
					},
					"fallback": {
						Description: "Global fallback recipe used only when this system tool is unavailable from the target system package manager.",
						Ref:         "#/$defs/FallbackSpec",
					},
				},
				AdditionalProperties: false,
			},
			"ToolInstallSpec": {
				Description: "One install candidate for a logical tool.",
				Type:        "object",
				Required:    []string{"provider"},
				Properties: map[string]*schema{
					"provider": {
						Description: "Concrete provider or manager for this install candidate.",
						Type:        "string",
						Enum:        providerNames,
						Examples:    []any{"npm"},
					},
					"install_with": {
						Description: "Legacy concrete provider or manager pin accepted for migration. New providers[] entries should put the concrete value in provider.",
						Type:        "string",
						Enum:        installWithNames,
						Examples:    firstNAny(installWithExamples, 1),
					},
					"package": {
						Description: "Package identifier passed to the provider. Defaults to the logical tool name.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"ripgrep", "typescript"},
					},
					"options": {
						Description:          "Provider-specific install options (key-value pairs).",
						Type:                 "object",
						AdditionalProperties: &schema{Type: "string"},
					},
					"source": {
						Description: "Optional structured source metadata for recipe-backed install candidates.",
						Ref:         "#/$defs/FallbackSource",
					},
					"recipe": {
						Description: "Optional structured install recipe materialized into provider options at resolve time.",
						Ref:         "#/$defs/FallbackRecipe",
					},
					"bin_dir": {
						Description: "Optional binary directory override for recipe-backed script installs.",
						Type:        "string",
					},
				},
				AdditionalProperties: false,
			},
			"FallbackSpec": {
				Description: "A best-effort non-provider install recipe for a system tool.",
				Type:        "object",
				Required:    []string{"source"},
				Properties: map[string]*schema{
					"source": {
						Description: "Source metadata for the fallback recipe.",
						Ref:         "#/$defs/FallbackSource",
					},
					"status": {
						Description: "Resolver or install verification state for this fallback recipe.",
						Type:        "string",
						Enum: []any{
							config.FallbackStatusUnresolved,
							config.FallbackStatusUnsupported,
							config.FallbackStatusUnverified,
							config.FallbackStatusVerified,
							config.FallbackStatusFailed,
						},
						Default:  config.FallbackStatusUnverified,
						Examples: []any{config.FallbackStatusUnverified},
					},
					"binary": {
						Description: "Primary executable name installed or checked by this fallback.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"rg"},
					},
					"bin_dir": {
						Description: "Tool-specific binary directory override. Defaults to settings.fallback_bin_dir.",
						Type:        "string",
						Examples:    []any{"~/.local/share/omni/fallback/bin"},
					},
					"release_channel": {
						Description: "Release channel preference. Empty means stable releases only unless no stable release exists.",
						Type:        "string",
						Examples:    []any{"stable", "prerelease"},
					},
					"recipe": {
						Description: "Structured recipe metadata used to generate or edit fallback commands.",
						Ref:         "#/$defs/FallbackRecipe",
					},
					"platforms": {
						Description:          "Optional OS/architecture-specific release asset overrides keyed by os/arch.",
						Type:                 "object",
						AdditionalProperties: ref("#/$defs/FallbackPlatform"),
						Examples: []any{
							map[string]any{"linux/amd64": map[string]any{"asset_pattern": "ripgrep-{version}-x86_64-unknown-linux-musl.tar.gz"}},
						},
					},
					"commands": {
						Description: "User-editable fallback lifecycle shell commands.",
						Ref:         "#/$defs/FallbackCommands",
					},
				},
				AdditionalProperties: false,
			},
			"FallbackSource": {
				Description: "Where a fallback recipe came from.",
				Type:        "object",
				Required:    []string{"type"},
				Properties: map[string]*schema{
					"type": {
						Description: "Fallback source kind.",
						Type:        "string",
						Enum:        []any{config.FallbackSourceGitHub},
						Examples:    []any{config.FallbackSourceGitHub},
					},
					"owner": {
						Description: "GitHub repository owner for github sources.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"BurntSushi"},
					},
					"repo": {
						Description: "GitHub repository name for github sources.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"ripgrep"},
					},
					"url": {
						Description: "Source URL, when known.",
						Type:        "string",
						Examples:    []any{"https://github.com/BurntSushi/ripgrep"},
					},
				},
				AdditionalProperties: false,
			},
			"FallbackRecipe": {
				Description: "Structured fallback recipe metadata.",
				Type:        "object",
				Properties: map[string]*schema{
					"type": {
						Description: "Fallback recipe kind.",
						Type:        "string",
						Enum: []any{
							config.FallbackRecipeGitHubReleaseAsset,
							config.FallbackRecipeRawCommands,
							config.FallbackRecipeCurlInstallScript,
							config.FallbackRecipeAptRepo,
						},
						Examples: []any{config.FallbackRecipeGitHubReleaseAsset},
					},
					"asset_pattern": {
						Description: "Release asset match pattern using fallback template variables.",
						Type:        "string",
						Examples:    []any{"ripgrep-{version}-{os}-{arch}.tar.gz"},
					},
					"binary_path": {
						Description: "Path to the executable inside the downloaded asset, when extraction is needed.",
						Type:        "string",
						Examples:    []any{"rg"},
					},
					"checksum": {
						Description: "Checksum to verify the matched release asset when confidently known.",
						Type:        "string",
					},
					"tag_name": {
						Description: "Pinned GitHub release tag (e.g. v0.62.2). Omit to use latest.",
						Type:        "string",
						Examples:    []any{"v0.62.2"},
					},
				},
				AdditionalProperties: false,
			},
			"FallbackPlatform": {
				Description: "OS/architecture-specific release asset metadata.",
				Type:        "object",
				Properties: map[string]*schema{
					"asset_pattern": {
						Description: "Release asset match pattern for this platform.",
						Type:        "string",
					},
					"binary_path": {
						Description: "Path to the executable inside the downloaded asset for this platform.",
						Type:        "string",
					},
					"checksum": {
						Description: "Checksum for the matched release asset on this platform.",
						Type:        "string",
					},
				},
				AdditionalProperties: false,
			},
			"FallbackCommands": {
				Description: "User-editable fallback lifecycle commands.",
				Type:        "object",
				Properties: map[string]*schema{
					"install": {
						Description: "Install command run by fallback sync/install.",
						Type:        "string",
					},
					"check": {
						Description: "Command that verifies the fallback is installed and usable.",
						Type:        "string",
					},
					"uninstall": {
						Description: "Optional uninstall command. If absent, uninstall is reported as unavailable.",
						Type:        "string",
					},
					"upgrade": {
						Description: "Optional upgrade command. If absent, install may be reused for upgrades.",
						Type:        "string",
					},
					"version": {
						Description: "Optional version command used to display installed fallback version.",
						Type:        "string",
					},
				},
				AdditionalProperties: false,
			},
			"DotEntry": {
				Description: "A dotfile or directory managed by 'omni dots'.",
				Type:        "object",
				Required:    []string{"name", "path"},
				Properties: map[string]*schema{
					"name": {
						Description: "Human-readable identifier. Also used as the default source directory name.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"nvim", "zsh", "git"},
					},
					"package": {
						Description: "Default stow package directory. Defaults to the logical dotfile name.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"nvim", "zsh-work"},
					},
					"hosts": {
						Description: "Host-specific stow package variants keyed by short hostname.",
						Type:        "object",
						AdditionalProperties: &schema{
							Type:                 "object",
							AdditionalProperties: false,
							Properties: map[string]*schema{
								"package": {
									Description: "Stow package directory to use on this host.",
									Type:        "string",
									MinLength:   1,
									Examples:    []any{"nvim-work-laptop"},
								},
							},
						},
					},
					"path": {
						Description: "Original filesystem location managed by this entry (~ and environment variables are expanded).",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"~/.config/nvim", "~/.zshrc"},
					},
					"ignored": {
						Description: "Keep the entry visible but skip sync/discovery management.",
						Type:        "boolean",
					},
					"ignore": {
						Description: "gitignore-style patterns for files to skip within this entry.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"*.local", "secrets/"}},
					},
				},
				AdditionalProperties: false,
			},
			"AgentsConfig": {
				Description: "Omni-managed AI-agent resources: skill packages, MCP servers, plugin marketplaces, and plugins.",
				Type:        "object",
				Properties: map[string]*schema{
					"packages": {
						Description: "Skill package sources restored via the upstream skills CLI.",
						Type:        "array",
						Items:       ref("#/$defs/SkillPackage"),
					},
					"mcp_servers": {
						Description: "MCP server entries restored for the configured agents.",
						Type:        "array",
						Items:       ref("#/$defs/McpServer"),
					},
					"marketplaces": {
						Description: "Plugin marketplace sources for the configured agents.",
						Type:        "array",
						Items:       ref("#/$defs/Marketplace"),
					},
					"plugins": {
						Description: "Plugins installed from a declared marketplace.",
						Type:        "array",
						Items:       ref("#/$defs/Plugin"),
					},
					"ignore": ref("#/$defs/AgentsIgnore"),
				},
				AdditionalProperties: false,
			},
			"SkillPackage": {
				Description: "A source repo of agent skills, tracked as one unit.",
				Type:        "object",
				Required:    []string{"source"},
				Properties: map[string]*schema{
					"source": {
						Description: "Skill package repository source (owner/repo or URL).",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"vercel-labs/agent-skills"},
					},
					"ref": {
						Description: "Git ref (branch, tag, or commit) to install from. Defaults to the package's default branch.",
						Type:        "string",
					},
					"agents": {
						Description: "Target agent IDs for this package. Empty means all configured agents.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"claude-code", "codex"}},
					},
				},
				AdditionalProperties: false,
			},
			"McpServer": {
				Description: "One MCP server entry in the omni agent manifest.",
				Type:        "object",
				Required:    []string{"name", "transport"},
				Properties: map[string]*schema{
					"name": {
						Description: "MCP server identifier.",
						Type:        "string",
						MinLength:   1,
						Examples:    []any{"context7"},
					},
					"transport": {
						Description: "MCP transport kind. stdio requires command; http/sse require url.",
						Type:        "string",
						Enum:        []any{"stdio", "http", "sse"},
						Examples:    []any{"stdio"},
					},
					"command": {
						Description: "Command to launch the server. Required for stdio transport.",
						Type:        "string",
						Examples:    []any{"npx -y @upstash/context7-mcp"},
					},
					"url": {
						Description: "Server URL. Required for http/sse transport.",
						Type:        "string",
						Examples:    []any{"https://mcp.example.com"},
					},
					"env": {
						Description: "Environment variable names resolved from the calling environment at restore time.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"CONTEXT7_API_KEY"}},
					},
					"env_literal": {
						Description:          "Non-secret inline environment values forwarded verbatim.",
						Type:                 "object",
						AdditionalProperties: &schema{Type: "string"},
					},
					"agents": {
						Description: "Target agent IDs for this server. Empty means all configured agents.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"claude-code", "codex"}},
					},
				},
				AdditionalProperties: false,
			},
			"Marketplace": {
				Description: "A plugin marketplace source in the omni agent manifest.",
				Type:        "object",
				Required:    []string{"name", "source"},
				Properties: map[string]*schema{
					"name": {
						Description: "Marketplace identifier.",
						Type:        "string",
						MinLength:   1,
					},
					"source": {
						Description: "Marketplace source (owner/repo or URL) as accepted by the agent CLI's marketplace add command.",
						Type:        "string",
						MinLength:   1,
					},
					"agents": {
						Description: "Target agent IDs for this marketplace. Empty means all configured agents.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"claude-code", "codex"}},
					},
				},
				AdditionalProperties: false,
			},
			"Plugin": {
				Description: "A plugin installed from a declared marketplace.",
				Type:        "object",
				Required:    []string{"name", "marketplace"},
				Properties: map[string]*schema{
					"name": {
						Description: "Plugin identifier.",
						Type:        "string",
						MinLength:   1,
					},
					"marketplace": {
						Description: "Name of the declared agents.marketplaces entry this plugin comes from.",
						Type:        "string",
						MinLength:   1,
					},
					"agents": {
						Description: "Target agent IDs for this plugin. Empty means all configured agents.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"claude-code", "codex"}},
					},
				},
				AdditionalProperties: false,
			},
			"AgentsIgnore": {
				Description: "Agent resources skipped during restore/sync.",
				Type:        "object",
				Properties: map[string]*schema{
					"skills": {
						Description: "Skill package sources skipped during restore/sync.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"vercel-labs/agent-skills"}},
					},
					"mcp_servers": {
						Description: "MCP server names skipped during restore/sync.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"context7"}},
					},
					"plugins": {
						Description: "Plugin names skipped during restore/sync.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"my-plugin"}},
					},
					"marketplaces": {
						Description: "Marketplace names skipped during restore/sync.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{[]any{"my-marketplace"}},
					},
				},
				AdditionalProperties: false,
			},
		},
	}
}

func ref(s string) *schema { return &schema{Ref: s} }

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstNAny(values []any, n int) []any {
	if len(values) < n {
		n = len(values)
	}
	return append([]any(nil), values[:n]...)
}

func exampleInstallWithNames() []string {
	var examples []string
	for _, name := range provider.BuiltinConcreteEcosystemNames() {
		if provider.BuiltinMetadata(name).SupportsTaps {
			examples = append(examples, name)
			break
		}
	}
	for _, ecosystem := range provider.BuiltinEcosystemNames() {
		if manager := firstString(provider.BuiltinSettingsManagerNames(ecosystem)); manager != "" {
			examples = append(examples, manager)
		}
	}
	return examples
}

func main() {
	outputs := []schemaOutput{
		{path: currentOutput(), id: config.SchemaURL},
		{path: latestOutput, id: latestSchemaID},
	}
	if len(os.Args) > 1 {
		outputs = outputs[:0]
		for _, out := range os.Args[1:] {
			outputs = append(outputs, schemaOutput{path: out, id: config.SchemaURL})
		}
	}
	for _, out := range outputs {
		if err := writeGeneratedSchema(out); err != nil {
			fmt.Fprintf(os.Stderr, "gen-schema: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", out.path)
	}
}

type schemaOutput struct {
	path string
	id   string
}

func writeGeneratedSchema(out schemaOutput) error {
	doc := buildWithID(out.id)
	if out.path == currentOutput() {
		return writeVersionedSchema(out.path, doc)
	}
	return writeSchema(out.path, doc)
}

func writeVersionedSchema(out string, doc *schema) error {
	data, err := encodeSchema(doc)
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(out)
	switch {
	case err == nil:
		if !bytes.Equal(existing, data) {
			equal, cmpErr := equalIgnoringSchemaDescriptions(existing, data)
			if cmpErr != nil {
				return fmt.Errorf("compare %s: %w", out, cmpErr)
			}
			if equal {
				return nil
			}
			return fmt.Errorf("%s already exists with different content; bump config.CurrentVersion to create a new schema version instead of rewriting an old one", out)
		}
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read %s: %w", out, err)
	default:
		return writeBytes(out, data)
	}
}

func equalIgnoringSchemaDescriptions(left, right []byte) (bool, error) {
	var leftJSON any
	if err := json.Unmarshal(left, &leftJSON); err != nil {
		return false, err
	}
	var rightJSON any
	if err := json.Unmarshal(right, &rightJSON); err != nil {
		return false, err
	}
	stripSchemaDescriptions(leftJSON)
	stripSchemaDescriptions(rightJSON)
	return jsonDeepEqual(leftJSON, rightJSON), nil
}

func stripSchemaDescriptions(v any) {
	switch value := v.(type) {
	case map[string]any:
		delete(value, "description")
		for _, child := range value {
			stripSchemaDescriptions(child)
		}
	case []any:
		for _, child := range value {
			stripSchemaDescriptions(child)
		}
	}
}

func jsonDeepEqual(left, right any) bool {
	leftData, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightData, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(leftData, rightData)
}

func writeSchema(out string, doc *schema) error {
	data, err := encodeSchema(doc)
	if err != nil {
		return err
	}
	return writeBytes(out, data)
}

func encodeSchema(doc *schema) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep < > & readable in descriptions
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return buf.Bytes(), nil
}

func writeBytes(out string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		if cerr := f.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", cerr))
		}
		_ = os.Remove(out) // remove partially-written file
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
