package app

import "github.com/lkshrk/omni/internal/config"

// Re-exports of the internal/config symbols the TUI needs, so the view layer
// depends only on internal/app (layering finding model.go:202). Type aliases
// (identical types), so App's own config-typed signatures — and the config
// values carried in StartupSnapshot/messages — accept them unchanged. The TUI
// treats these as opaque view state; it never loads or saves config itself
// (that stays behind App methods).

// Leaf config value types the TUI reads out of app-provided state.
type (
	Settings         = config.Settings
	ToolInstallSpec  = config.ToolInstallSpec
	FallbackSpec     = config.FallbackSpec
	FallbackSource   = config.FallbackSource
	FallbackRecipe   = config.FallbackRecipe
	FallbackCommands = config.FallbackCommands
	McpServer        = config.McpServer
	Marketplace      = config.Marketplace
	Plugin           = config.Plugin
	AgentsIgnore     = config.AgentsIgnore
	OptimizeReport   = config.OptimizeReport
)

// Fallback source/recipe/status discriminators (untyped string constants) and
// the reserved system-inventory group name.
const (
	FallbackSourceGitHub             = config.FallbackSourceGitHub
	FallbackRecipeGitHubReleaseAsset = config.FallbackRecipeGitHubReleaseAsset
	FallbackStatusUnresolved         = config.FallbackStatusUnresolved
	FallbackStatusUnsupported        = config.FallbackStatusUnsupported
	FallbackStatusUnverified         = config.FallbackStatusUnverified
	FallbackStatusVerified           = config.FallbackStatusVerified
	FallbackStatusFailed             = config.FallbackStatusFailed
	SystemInventoryGroup             = config.SystemInventoryGroup
)
