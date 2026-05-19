package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

func (m *Model) blockPrivilegedToolAction(t *database.ToolCache, action provider.PrivilegeAction) bool {
	if t == nil || m.app == nil {
		return false
	}
	if m.blockProtectedProviderToolDelete(t, action) {
		return true
	}
	plan, err := m.app.ToolPrivilegePlan(m.ctx, t, action)
	if err != nil || !plan.RequiresPrivilege() {
		return false
	}
	reason := plan.Reason
	if reason == "" {
		reason = "package manager needs sudo/root access"
	}
	plan.Reason = reason
	if err := m.app.MarkToolPrivilegeRequired(m.ctx, t.Name, t.Provider, t.Package, string(plan.Requirement), reason); err != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: mark privilege required: %v\n", err)
	}
	if m.openAdminTerminalPrompt(t, action, plan) {
		return true
	}
	key := toolKey(t.Name, t.Provider)
	m.setToolActionError(key, "requires admin terminal; unsupported provider command")
	return true
}

func (m *Model) blockProtectedProviderToolDelete(t *database.ToolCache, action provider.PrivilegeAction) bool {
	if t == nil || m.app == nil || action != provider.PrivilegeActionUninstall {
		return false
	}
	if err := m.app.ValidateToolDelete(t.Name); err != nil {
		m.setToolActionError(toolKey(t.Name, t.Provider), err.Error())
		return true
	}
	return false
}

func (m *Model) queuePrivilegedInstallPrompts(rowErrors map[string]string) bool {
	return m.queuePrivilegedToolPrompts(rowErrors, privilegedActionMapForRows(rowErrors, provider.PrivilegeActionInstall))
}

func (m *Model) queuePrivilegedToolPrompts(rowErrors map[string]string, actions map[string]provider.PrivilegeAction) bool {
	if len(rowErrors) == 0 || m.app == nil {
		return false
	}
	m.adminTerminalQueue = nil
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for _, t := range m.privilegedToolQueueCandidates(rowErrors) {
		key := toolKey(t.Name, t.Provider)
		action, ok := actions[key]
		if !ok || action == "" || skipPrivilegedToolQueueCandidate(t, action) {
			continue
		}
		if m.blockProtectedProviderToolDelete(t, action) {
			continue
		}
		message := rowErrors[key]
		rowPlan, classified := privilegePlanFromRowError(message)
		if !classified && !cachedToolRequiresPrivilege(t) && !isPrivilegedInstallFailure(message) {
			continue
		}
		plan, err := m.app.ToolPrivilegePlan(ctx, t, action)
		if err != nil || !plan.RequiresPrivilege() {
			plan = rowPlan
		} else if shouldPreferRowPrivilegePlan(plan, rowPlan) {
			plan = rowPlan
		}
		if !plan.RequiresPrivilege() {
			continue
		}
		if plan.Reason == "" {
			plan.Reason = "package manager needs sudo/root access"
		}
		if err := m.app.MarkToolPrivilegeRequired(ctx, t.Name, t.Provider, t.Package, string(plan.Requirement), plan.Reason); err != nil {
			fmt.Fprintf(os.Stderr, "warning: omni: mark privilege required: %v\n", err)
		}
		state, ok := m.adminTerminalStateForTool(t, action, plan)
		if !ok {
			m.setToolActionError(key, "requires admin terminal; unsupported provider command")
			continue
		}
		state.preserveOtherRowErrors = true
		m.setToolActionError(key, privilegedApprovalRowMessage(action))
		m.adminTerminalQueue = append(m.adminTerminalQueue, state)
	}
	total := len(m.adminTerminalQueue)
	if total > 1 {
		for i := range m.adminTerminalQueue {
			m.adminTerminalQueue[i].queueIndex = i + 1
			m.adminTerminalQueue[i].queueTotal = total
		}
	}
	return m.openNextQueuedAdminTerminalAction()
}

func privilegedActionMapForRows(rowErrors map[string]string, action provider.PrivilegeAction) map[string]provider.PrivilegeAction {
	if len(rowErrors) == 0 || action == "" {
		return nil
	}
	actions := make(map[string]provider.PrivilegeAction, len(rowErrors))
	for key := range rowErrors {
		actions[key] = action
	}
	return actions
}

func skipPrivilegedToolQueueCandidate(t *database.ToolCache, action provider.PrivilegeAction) bool {
	if t == nil {
		return true
	}
	switch action {
	case provider.PrivilegeActionInstall:
		return t.Installed
	case provider.PrivilegeActionUninstall, provider.PrivilegeActionUpgrade:
		return false
	default:
		return true
	}
}

func privilegedApprovalRowMessage(action provider.PrivilegeAction) string {
	switch action {
	case provider.PrivilegeActionUpgrade:
		return "admin approval required to upgrade"
	case provider.PrivilegeActionUninstall:
		return "admin approval required to uninstall"
	default:
		return "admin approval required to install"
	}
}

func (m *Model) privilegedToolQueueCandidates(rowErrors map[string]string) []*database.ToolCache {
	candidates := make([]*database.ToolCache, 0, len(rowErrors))
	seen := make(map[string]bool, len(rowErrors))
	add := func(tools []*database.ToolCache) {
		for _, t := range tools {
			if t == nil {
				continue
			}
			key := toolKey(t.Name, t.Provider)
			if seen[key] {
				continue
			}
			if _, ok := rowErrors[key]; !ok {
				continue
			}
			seen[key] = true
			candidates = append(candidates, t)
		}
	}
	add(m.visibleTools)
	add(m.allTools)
	for key := range rowErrors {
		if seen[key] {
			continue
		}
		name, providerName, ok := strings.Cut(key, "\x00")
		if !ok || name == "" || providerName == "" {
			continue
		}
		seen[key] = true
		candidates = append(candidates, &database.ToolCache{Name: name, Provider: providerName, Package: name})
	}
	return candidates
}

func isPrivilegedInstallFailure(message string) bool {
	if plan, ok := privilegePlanFromRowError(message); ok && plan.RequiresPrivilege() {
		return true
	}
	message = strings.ToLower(message)
	return strings.Contains(message, "requires sudo") ||
		strings.Contains(message, "requires root") ||
		strings.Contains(message, "requires admin") ||
		strings.Contains(message, "administrator")
}

func privilegePlanFromRowError(message string) (provider.PrivilegePlan, bool) {
	message = strings.TrimSpace(message)
	reason := privilegeReasonFromRowError(message)
	if reason != "" {
		return provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: reason}, true
	}
	return provider.ClassifyPrivilegeError(errors.New(message))
}

func shouldPreferRowPrivilegePlan(plan, rowPlan provider.PrivilegePlan) bool {
	if !rowPlan.RequiresPrivilege() || rowPlan.Reason == "" {
		return false
	}
	if !plan.RequiresPrivilege() || plan.Reason == "" {
		return true
	}
	return isGenericPrivilegeReason(plan.Reason) && !isGenericPrivilegeReason(rowPlan.Reason)
}

func isGenericPrivilegeReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return reason == "" ||
		reason == "package manager needs sudo/root access" ||
		reason == "package manager needs privileged access" ||
		reason == "package manager needs administrator access"
}

func privilegeReasonFromRowError(message string) string {
	lower := strings.ToLower(message)
	for _, prefix := range []string{
		"requires sudo:",
		"requires root:",
		"requires admin:",
		"requires administrator:",
	} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(message[len(prefix):])
		}
	}
	for _, marker := range []string{
		"brew cask ",
		"apt install ",
		"apk add ",
		"dnf install ",
		"pacman install ",
		"zypper install ",
	} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return strings.TrimSpace(message[idx:])
		}
	}
	return ""
}

func cachedToolRequiresPrivilege(t *database.ToolCache) bool {
	if t == nil || t.Privilege == "" {
		return false
	}
	return provider.PrivilegePlan{Requirement: provider.PrivilegeRequirement(t.Privilege)}.RequiresPrivilege()
}

func (m *Model) openNextQueuedAdminTerminalAction() bool {
	if len(m.adminTerminalQueue) == 0 {
		return false
	}
	state := m.adminTerminalQueue[0]
	m.adminTerminalQueue = m.adminTerminalQueue[1:]
	return m.openAdminTerminalState(state)
}

func brewCaskMayPromptForPassword(plan provider.PrivilegePlan) bool {
	reason := strings.ToLower(plan.Reason)
	return strings.Contains(reason, "brew cask") &&
		(strings.Contains(reason, "pkgutil") || strings.Contains(reason, "pkg installer"))
}
