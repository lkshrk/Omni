package app

import (
	"fmt"
	"slices"
	"strings"
)

func (a *App) rejectProviderToolDelete(name string) error {
	if a.isProviderToolName(name) {
		return fmt.Errorf("tool %q is a package manager/provider and cannot be deleted or uninstalled by omni", name)
	}
	return nil
}

// ValidateToolDelete rejects tool names reserved for package managers/providers.
func (a *App) ValidateToolDelete(name string) error {
	return a.rejectProviderToolDelete(name)
}

func (a *App) isProviderToolName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	validation := a.providerValidation()
	if slices.Contains(validation.Known, name) || slices.Contains(validation.Ecosystems, name) {
		return true
	}
	_, ok := validation.ConcreteEcosystems[name]
	return ok
}
