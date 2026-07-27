package app

import "github.com/lkshrk/omni/internal/config"

// Type aliases so App's own config-typed signatures accept them unchanged; the TUI never loads or saves config itself.

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
