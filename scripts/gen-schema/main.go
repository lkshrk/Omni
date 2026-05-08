// gen-schema writes the JSON Schema for omni's settings.json to
// spec/omni.settings.schema.json (relative to the repository root).
//
// Usage:
//
//	go run ./scripts/gen-schema          # writes spec/omni.settings.schema.json
//	go run ./scripts/gen-schema [path]   # writes to a custom path
//
// Run via make:
//
//	make gen-schema
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	schemaMetaURL = "https://json-schema.org/draft/2020-12/schema"
	defaultOutput = "spec/omni.settings.schema.json"
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
	ecosystemNames := stringsToAny(provider.BuiltinEcosystemNames())
	managerNames := stringsToAny(provider.BuiltinSettingsManagerNames(provider.EcosystemNode))
	managerNames = append(managerNames, stringsToAny(provider.BuiltinSettingsManagerNames(provider.EcosystemPython))...)
	installWithNames := stringsToAny(provider.BuiltinConcreteEcosystemNames())
	installWithExamples := stringsToAny(exampleInstallWithNames())
	systemPriority := stringsToAny(provider.BuiltinSystemProviderPriorityNames())
	return &schema{
		Schema:      schemaMetaURL,
		ID:          config.SchemaURL,
		Title:       "omni settings",
		Description: "omni settings.json — manages all dev tools, dotfiles, host assignments, and groups from a single file.",
		Type:        "object",
		Properties: map[string]*schema{
			"$schema": {
				Description: "JSON Schema reference URI (injected automatically by omni on every write).",
				Type:        "string",
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
							"provider": provider.EcosystemSystem,
							"package":  "ripgrep",
							"variants": []any{
								map[string]any{"provider": provider.EcosystemNode, "package": "ripgrep"},
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
		},
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
						Description: "Settings for portable ecosystem providers such as system, node, and python.",
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
					"dots_repo": {
						Description: "Per-machine path to the dotfiles git repository (~ is expanded).",
						Type:        "string",
						Examples:    []any{"~/Dev/dotfiles", "~/.dotfiles"},
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
						Description: "Host-specific ecosystem manager and priority overrides.",
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
					"disabled_providers": {
						Description: "Ecosystem provider names disabled on this host.",
						Type:        "array",
						Items:       &schema{Type: "string", MinLength: 1},
						Examples:    []any{ecosystemNames},
					},
				},
				AdditionalProperties: false,
			},
			"EcosystemSettings": {
				Description: "Settings for one ecosystem provider.",
				Type:        "object",
				Properties: map[string]*schema{
					"manager": {
						Description: "Concrete manager used by manager-backed ecosystems. Omit to auto-detect.",
						Type:        "string",
						Examples:    managerNames,
					},
					"priority": {
						Description: "Ordered concrete provider/manager priority for this ecosystem.",
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
				},
				AdditionalProperties: false,
			},
			"ToolSpec": {
				Description: "A logical tool spec. Provider identifies the portable ecosystem; variants and hosts define alternate install candidates.",
				Type:        "object",
				Required:    []string{"provider"},
				Properties: map[string]*schema{
					"provider": {
						Description: "Portable ecosystem provider for this logical tool.",
						Type:        "string",
						Enum:        ecosystemNames,
						Examples:    []any{provider.EcosystemSystem},
					},
					"install_with": {
						Description: "Concrete provider or manager used for this logical tool on matching hosts. Omit to use ecosystem settings/resolution.",
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
							map[string]any{"linuxbox": map[string]any{"provider": provider.EcosystemNode, "package": "ripgrep"}},
						},
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
						Description: "Portable ecosystem provider for this install candidate.",
						Type:        "string",
						Enum:        ecosystemNames,
						Examples:    []any{provider.EcosystemNode},
					},
					"install_with": {
						Description: "Concrete provider or manager used for this install candidate. Omit to use ecosystem settings/resolution.",
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
	out := defaultOutput
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "gen-schema: mkdir: %v\n", err)
		os.Exit(1)
	}

	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-schema: open: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep < > & readable in descriptions
	if err := enc.Encode(build()); err != nil {
		if cerr := f.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "gen-schema: close: %v\n", cerr)
		}
		_ = os.Remove(out) // remove partially-written file
		fmt.Fprintf(os.Stderr, "gen-schema: encode: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "gen-schema: write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", out)
}
