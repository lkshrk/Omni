package app

import "github.com/lkshrk/omni/internal/provider"

// Re-exports of the small set of internal/provider symbols the TUI needs, so the
// view layer depends only on internal/app (layering finding model.go:202). These
// are type aliases (identical types, not new types), so App's own provider-typed
// signatures — ToolPrivilegePlan, PrivilegedToolCommand, etc. — accept them
// unchanged. Data types that genuinely traffic through provider round-trips
// (provider.Tool, SearchResult, InstalledTool) are intentionally NOT re-exported
// here; they stay in the app-owned DTOs that carry them.

// PrivilegeAction and its values classify a lifecycle action for privilege
// planning and the admin-terminal command path.
type PrivilegeAction = provider.PrivilegeAction

const (
	PrivilegeActionInstall   = provider.PrivilegeActionInstall
	PrivilegeActionUpgrade   = provider.PrivilegeActionUpgrade
	PrivilegeActionUninstall = provider.PrivilegeActionUninstall
)

// PrivilegePlan is the resolved privilege requirement + reason for an action.
type PrivilegePlan = provider.PrivilegePlan

// ErrorSolution and ActionError describe an actionable provider failure surfaced
// in the TUI's per-row error UI.
type (
	ErrorSolution = provider.ErrorSolution
	ActionError   = provider.ActionError
)

// ActionErrorFrom extracts a structured *ActionError from an error, if present.
var ActionErrorFrom = provider.ActionErrorFrom

// BuiltinIsEcosystem reports whether name is a built-in ecosystem alias
// (node/python/system) rather than a concrete manager.
var BuiltinIsEcosystem = provider.BuiltinIsEcosystem
