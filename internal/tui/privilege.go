package tui

import (
	"context"
	"fmt"
	"os"

	"github.com/lkshrk/omni/internal/app"
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

func (m *Model) queuePrivilegedToolPrompts(rowErrors map[string]string, actions map[string]provider.PrivilegeAction) bool {
	if len(rowErrors) == 0 || m.app == nil {
		return false
	}
	m.adminTerminalQueue = nil
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	plan, err := m.app.PrivilegeQueuePlan(ctx, app.PrivilegeQueueRequest{
		RowErrors:    rowErrors,
		Actions:      actions,
		VisibleTools: m.visibleTools,
		Tools:        m.allTools,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: queue privileged actions: %v\n", err)
		return false
	}
	for key, message := range plan.RowErrors {
		m.setToolActionError(key, message)
	}
	for _, item := range plan.Items {
		if err := m.app.MarkToolPrivilegeRequired(ctx, item.Tool.Name, item.Tool.Provider, item.Tool.Package, string(item.Plan.Requirement), item.Plan.Reason); err != nil {
			fmt.Fprintf(os.Stderr, "warning: omni: mark privilege required: %v\n", err)
		}
		state := m.adminTerminalStateForCommand(item.Command)
		state.preserveOtherRowErrors = true
		m.setToolActionError(item.Key, item.ApprovalMessage)
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

func (m *Model) openNextQueuedAdminTerminalAction() bool {
	if len(m.adminTerminalQueue) == 0 {
		return false
	}
	state := m.adminTerminalQueue[0]
	m.adminTerminalQueue = m.adminTerminalQueue[1:]
	return m.openAdminTerminalState(state)
}
