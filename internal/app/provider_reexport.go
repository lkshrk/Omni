package app

import "github.com/lkshrk/omni/internal/provider"

// Type aliases so App's provider-typed signatures accept them unchanged; provider round-trip data types stay in app-owned DTOs.

type PrivilegeAction = provider.PrivilegeAction

const (
	PrivilegeActionInstall   = provider.PrivilegeActionInstall
	PrivilegeActionUpgrade   = provider.PrivilegeActionUpgrade
	PrivilegeActionUninstall = provider.PrivilegeActionUninstall
)

type PrivilegePlan = provider.PrivilegePlan

type (
	ErrorSolution = provider.ErrorSolution
	ActionError   = provider.ActionError
)

var ActionErrorFrom = provider.ActionErrorFrom

// BuiltinIsEcosystem — node, python or system rather than a concrete manager.
var BuiltinIsEcosystem = provider.BuiltinIsEcosystem
