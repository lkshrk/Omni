package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	adminTerminalEventBuffer   = 128
	adminTerminalMaxOutputSize = 64 * 1024
)

type adminTerminalState struct {
	action                 provider.PrivilegeAction
	name                   string
	providerName           string
	pkg                    string
	installedWith          string
	addToConfig            bool
	addGroup               string
	addHost                string
	preserveOtherRowErrors bool
	reason                 string
	command                string
	args                   []string
	display                string
	returnMode             viewMode
	rowKey                 string
	queueIndex             int
	queueTotal             int
	id                     int
	running                bool
	output                 string
	events                 chan tea.Msg
	session                *adminTerminalSession
}

type adminTerminalSession struct {
	ptmx *os.File
}

type adminTerminalOutputMsg struct {
	id    int
	chunk string
}

type adminTerminalDoneMsg struct {
	id    int
	state adminTerminalState
	err   error
}

func (m *Model) openAdminTerminalPrompt(t *database.ToolCache, action provider.PrivilegeAction, plan provider.PrivilegePlan) bool {
	state, ok := m.adminTerminalStateForTool(t, action, plan)
	if !ok {
		return false
	}
	return m.openAdminTerminalState(state)
}

func (m *Model) adminTerminalStateForTool(t *database.ToolCache, action provider.PrivilegeAction, plan provider.PrivilegePlan) (adminTerminalState, bool) {
	if t == nil {
		return adminTerminalState{}, false
	}
	command, args, ok := m.adminTerminalCommand(action, t, plan)
	if !ok {
		return adminTerminalState{}, false
	}
	return adminTerminalState{
		action:        action,
		name:          t.Name,
		providerName:  t.Provider,
		pkg:           effectiveToolPackage(t),
		installedWith: t.InstalledWith,
		reason:        plan.Reason,
		command:       command,
		args:          args,
		display:       shellJoin(append([]string{command}, args...)),
		returnMode:    m.mode,
		rowKey:        toolKey(t.Name, t.Provider),
	}, true
}

func (m *Model) openAdminTerminalState(state adminTerminalState) bool {
	if state.command == "" {
		return false
	}
	m.adminTerminal = &state
	m.mode = viewAdminTerminal
	m.clearListConfirmation()
	clearStatus(m)
	return true
}

func brewCaskAdminCommand(action provider.PrivilegeAction, t *database.ToolCache) (string, []string) {
	verb := "uninstall"
	switch action {
	case provider.PrivilegeActionInstall:
		verb = "install"
	case provider.PrivilegeActionUpgrade:
		verb = "upgrade"
	}
	return "brew", []string{verb, "--cask", effectiveToolPackage(t)}
}

func (m *Model) adminTerminalCommand(action provider.PrivilegeAction, t *database.ToolCache, plan provider.PrivilegePlan) (string, []string, bool) {
	concrete := m.adminTerminalConcreteProvider(t, action)
	if concrete == "" || concrete == provider.EcosystemSystem {
		concrete = adminTerminalProviderFromPlan(plan)
	}
	switch concrete {
	case "brew":
		if !brewCaskMayPromptForPassword(plan) {
			return "", nil, false
		}
		cmd, args := brewCaskAdminCommand(action, t)
		return cmd, args, true
	case "apt":
		return interactivePrivilegedCommand("apt-get", adminTerminalAPTArgs(action, effectiveToolPackage(t))...)
	case "apk":
		return interactivePrivilegedCommand("apk", adminTerminalAPKArgs(action, effectiveToolPackage(t))...)
	case "dnf":
		return interactivePrivilegedCommand("dnf", adminTerminalDNFArgs(action, effectiveToolPackage(t))...)
	case "pacman":
		return interactivePrivilegedCommand("pacman", adminTerminalPacmanArgs(action, effectiveToolPackage(t))...)
	case "zypper":
		return interactivePrivilegedCommand("zypper", adminTerminalZypperArgs(action, effectiveToolPackage(t))...)
	default:
		return "", nil, false
	}
}

func adminTerminalProviderFromPlan(plan provider.PrivilegePlan) string {
	fields := strings.Fields(plan.Reason)
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "apt", "apk", "dnf", "pacman", "zypper", "brew":
		return fields[0]
	default:
		return ""
	}
}

func (m *Model) adminTerminalConcreteProvider(t *database.ToolCache, action provider.PrivilegeAction) string {
	if t == nil {
		return ""
	}
	if t.InstalledWith != "" && t.InstalledWith != t.Provider {
		return t.InstalledWith
	}
	if (action == provider.PrivilegeActionUninstall || action == provider.PrivilegeActionUpgrade) && t.InstalledWith != "" {
		return t.InstalledWith
	}
	if t.Provider == provider.EcosystemSystem {
		return m.effectiveSystemManager
	}
	return t.Provider
}

func interactivePrivilegedCommand(cmd string, args ...string) (string, []string, bool) {
	if os.Geteuid() == 0 {
		return cmd, args, true
	}
	return "sudo", append([]string{cmd}, args...), true
}

func adminTerminalAPTArgs(action provider.PrivilegeAction, pkg string) []string {
	switch action {
	case provider.PrivilegeActionInstall:
		return []string{"install", "-y", pkg}
	case provider.PrivilegeActionUpgrade:
		return []string{"install", "--only-upgrade", "-y", pkg}
	default:
		return []string{"remove", "-y", pkg}
	}
}

func adminTerminalAPKArgs(action provider.PrivilegeAction, pkg string) []string {
	switch action {
	case provider.PrivilegeActionInstall:
		return []string{"add", pkg}
	case provider.PrivilegeActionUpgrade:
		return []string{"upgrade", pkg}
	default:
		return []string{"del", pkg}
	}
}

func adminTerminalDNFArgs(action provider.PrivilegeAction, pkg string) []string {
	switch action {
	case provider.PrivilegeActionInstall:
		return []string{"install", "-y", pkg}
	case provider.PrivilegeActionUpgrade:
		return []string{"upgrade", "-y", pkg}
	default:
		return []string{"remove", "-y", pkg}
	}
}

func adminTerminalPacmanArgs(action provider.PrivilegeAction, pkg string) []string {
	switch action {
	case provider.PrivilegeActionUninstall:
		return []string{"-R", "--noconfirm", pkg}
	default:
		return []string{"-S", "--noconfirm", pkg}
	}
}

func adminTerminalZypperArgs(action provider.PrivilegeAction, pkg string) []string {
	switch action {
	case provider.PrivilegeActionInstall:
		return []string{"install", "-y", pkg}
	case provider.PrivilegeActionUpgrade:
		return []string{"update", "-y", pkg}
	default:
		return []string{"remove", "-y", pkg}
	}
}

func effectiveToolPackage(t *database.ToolCache) string {
	if t == nil {
		return ""
	}
	if t.Package != "" {
		return t.Package
	}
	return t.Name
}

func (m *Model) handleAdminTerminalKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if m.adminTerminal == nil {
		m.mode = viewList
		return nil
	}
	if m.adminTerminal.running {
		if data := adminTerminalKeyBytes(msg); len(data) > 0 {
			m.writeAdminTerminalInput(data)
		}
		return nil
	}
	switch {
	case key.Matches(msg, m.keys.Back):
		m.mode = m.adminTerminal.returnMode
		m.adminTerminal = nil
		m.adminTerminalQueue = nil
		return nil
	case key.Matches(msg, m.keys.Confirm):
		return []tea.Cmd{m.startAdminTerminalSession()}
	default:
		return nil
	}
}

func (m *Model) startAdminTerminalSession() tea.Cmd {
	if m.adminTerminal == nil {
		return nil
	}
	m.adminTerminalGen++
	state := m.adminTerminal
	state.id = m.adminTerminalGen
	state.running = true
	state.events = make(chan tea.Msg, adminTerminalEventBuffer)
	state.output = adminTerminalInitialOutput(*state)
	m.loading = true
	startOp(m, adminTerminalRunningStatus(*state))
	m.startRowOperation(state.name, state.providerName, "Admin terminal")
	if state.action == provider.PrivilegeActionUpgrade {
		if m.upgradingKeys == nil {
			m.upgradingKeys = make(map[string]bool)
		}
		m.upgradingKeys[state.rowKey] = true
	}
	session, err := startAdminTerminalProcess(m.ctx, *state, adminTerminalContentWidth(*m), adminTerminalPTYRows(*m), state.events)
	if err != nil {
		doneState := state.completionState()
		return func() tea.Msg {
			return adminTerminalDoneMsg{id: doneState.id, state: doneState, err: err}
		}
	}
	state.session = session
	return waitAdminTerminalEvent(state.id, state.events)
}

func waitAdminTerminalEvent(id int, events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return adminTerminalDoneMsg{id: id, err: fmt.Errorf("admin terminal event stream closed")}
		}
		return msg
	}
}

func (m *Model) handleAdminTerminalOutputMsg(msg adminTerminalOutputMsg) []tea.Cmd {
	if m.adminTerminal == nil || m.adminTerminal.id != msg.id || !m.adminTerminal.running {
		return nil
	}
	m.adminTerminal.appendOutput(msg.chunk)
	return []tea.Cmd{waitAdminTerminalEvent(m.adminTerminal.id, m.adminTerminal.events)}
}

func (m *Model) handleAdminTerminalDoneMsg(msg adminTerminalDoneMsg) []tea.Cmd {
	if m.adminTerminal != nil && msg.id != 0 && m.adminTerminal.id != msg.id {
		return nil
	}
	if m.adminTerminal != nil {
		m.closeAdminTerminalSession()
		m.mode = m.adminTerminal.returnMode
		m.adminTerminal = nil
	} else if msg.state.returnMode != viewAdminTerminal {
		m.mode = msg.state.returnMode
	}
	if msg.err != nil {
		return m.handleOpCompleteMsg(opCompleteMsg{key: msg.state.rowKey, err: fmt.Errorf("admin terminal: %w", msg.err)})
	}
	m.startRowOperation(msg.state.name, msg.state.providerName, "Refreshing…")
	return []tea.Cmd{m.doCompleteAdminTerminalAction(msg.state)}
}

func (m *Model) doCompleteAdminTerminalAction(state adminTerminalState) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		if err := a.CompleteExternalToolAction(ctx, state.action, state.name, state.providerName, state.pkg, state.installedWith); err != nil {
			return opCompleteMsg{key: state.rowKey, err: err}
		}
		tools, _ := a.ListTools(ctx, "")
		msg := opCompleteMsg{
			key:                    state.rowKey,
			message:                adminTerminalSuccessMessage(state),
			tools:                  tools,
			preserveOtherRowErrors: state.preserveOtherRowErrors,
		}
		if state.addToConfig && state.action == provider.PrivilegeActionInstall {
			removeDiscovered := []string{toolKey(state.name, state.providerName)}
			if err := a.Add(ctx, state.providerName, state.pkg, state.name, state.addGroup, ""); err != nil {
				groupNames, toolGroups, memberships, info := m.reloadToolContext()
				return opCompleteMsg{
					key:                  state.rowKey,
					err:                  fmt.Errorf("installed %s but config save failed: %w", state.name, err),
					tools:                tools,
					removeDiscoveredKeys: removeDiscovered,
					groupNames:           groupNames,
					toolGroups:           toolGroups,
					toolMemberships:      memberships,
					hostInfo:             info,
				}
			}
			if state.addGroup != "" {
				if err := a.AddGroupToHost(shortHostname(), state.addGroup); err != nil {
					groupNames, toolGroups, memberships, info := m.reloadToolContext()
					return opCompleteMsg{
						key:                  state.rowKey,
						err:                  fmt.Errorf("installed %s and added to config but host update failed: %w", state.name, err),
						tools:                tools,
						removeDiscoveredKeys: removeDiscovered,
						groupNames:           groupNames,
						toolGroups:           toolGroups,
						toolMemberships:      memberships,
						hostInfo:             info,
					}
				}
				if state.addHost != "" {
					if err := a.AddGroupToHost(state.addHost, state.addGroup); err != nil {
						groupNames, toolGroups, memberships, info := m.reloadToolContext()
						return opCompleteMsg{
							key:                  state.rowKey,
							err:                  fmt.Errorf("installed %s and added to config but host update failed: %w", state.name, err),
							tools:                tools,
							removeDiscoveredKeys: removeDiscovered,
							groupNames:           groupNames,
							toolGroups:           toolGroups,
							toolMemberships:      memberships,
							hostInfo:             info,
						}
					}
				}
			}
			tools, _ = a.ListTools(ctx, "")
			groupNames, toolGroups, memberships, info := m.reloadToolContext()
			msg.message = "installed " + state.name + " and added to config"
			msg.tools = tools
			msg.removeDiscoveredKeys = removeDiscovered
			msg.groupNames = groupNames
			msg.toolGroups = toolGroups
			msg.toolMemberships = memberships
			msg.hostInfo = info
		} else if state.action == provider.PrivilegeActionUninstall {
			groupNames, toolGroups, memberships, info := m.reloadToolContext()
			msg.groupNames = groupNames
			msg.toolGroups = toolGroups
			msg.toolMemberships = memberships
			msg.hostInfo = info
		}
		return msg
	}
}

func adminTerminalRunningStatus(state adminTerminalState) string {
	return externalAdminActionGerund(state.action) + " " + state.name + "…"
}

func adminTerminalSuccessMessage(state adminTerminalState) string {
	switch state.action {
	case provider.PrivilegeActionInstall:
		return "installed " + state.name
	case provider.PrivilegeActionUninstall:
		return "deleted " + state.name
	case provider.PrivilegeActionUpgrade:
		return "upgraded " + state.name
	default:
		return externalAdminActionVerb(state.action) + " " + state.name
	}
}

func externalAdminActionVerb(action provider.PrivilegeAction) string {
	switch action {
	case provider.PrivilegeActionInstall:
		return "install"
	case provider.PrivilegeActionUninstall:
		return "delete"
	case provider.PrivilegeActionUpgrade:
		return "upgrade"
	default:
		return string(action)
	}
}

func externalAdminActionGerund(action provider.PrivilegeAction) string {
	switch action {
	case provider.PrivilegeActionInstall:
		return "Installing"
	case provider.PrivilegeActionUninstall:
		return "Deleting"
	case provider.PrivilegeActionUpgrade:
		return "Upgrading"
	default:
		return "Running"
	}
}

func renderAdminTerminalPopup(m Model) string {
	state := m.adminTerminal
	if state == nil {
		return ""
	}
	width := adminTerminalContentWidth(m)
	var content string
	if state.running {
		content = renderAdminTerminalRunningPopup(m, state, width)
	} else {
		content = renderAdminTerminalApprovalPopup(m, state, width)
	}
	return lipgloss.NewStyle().Width(width).Render(content)
}

func renderAdminTerminalApprovalPopup(m Model, state *adminTerminalState, width int) string {
	var sb strings.Builder
	sb.WriteString(renderAdminTerminalApprovalSummary(m, state, width))
	sb.WriteString("\n\n")
	for _, line := range wrapText(adminTerminalApprovalMessage(state), width) {
		sb.WriteString(m.palette.styleNormal.Render(line))
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(m.palette.styleHelp.Render("command"))
	sb.WriteByte('\n')
	sb.WriteString(renderAdminTerminalCommandLine(m.palette, state.display, width))
	sb.WriteString("\n\n")
	sb.WriteString(m.palette.styleHelp.Render("A terminal will open here after you continue."))
	sb.WriteString("\n\n")
	sb.WriteString(renderPickerHintItems(m, width, []hintItem{
		hintFromBindingDesc(m.keys.Back, "cancel"),
		hintFromBindingDesc(m.keys.Confirm, "continue"),
	}))
	return strings.TrimRight(sb.String(), "\n")
}

func renderAdminTerminalRunningPopup(m Model, state *adminTerminalState, width int) string {
	var sb strings.Builder
	sb.WriteString(renderAdminTerminalCommandHeader(m, state, width))
	sb.WriteByte('\n')
	sb.WriteString(renderAdminTerminalOutput(m, state, width))
	return strings.TrimRight(sb.String(), "\n")
}

func renderAdminTerminalApprovalSummary(m Model, state *adminTerminalState, width int) string {
	left := state.name
	if left == "" {
		left = "Admin action"
	}
	right := adminTerminalContextLabel(state)
	return alignLR(
		m.palette.styleTitle.PaddingLeft(0).Render(left),
		m.palette.styleHelp.Render(right),
		width,
		2,
	)
}

func adminTerminalPopupFrame(m Model) popupFrame {
	paddingX := 2
	frame := popupFrame{
		Title:          adminTerminalPopupTitle(m),
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(adminTerminalContentWidth(m), paddingX),
		NoTitleDivider: true,
	}
	if m.adminTerminal != nil && m.adminTerminal.running {
		frame.ContentHeight = adminTerminalRunningContentHeight(m)
	}
	return frame
}

func adminTerminalPopupTitle(m Model) string {
	if m.adminTerminal == nil {
		return "Admin Terminal"
	}
	if m.adminTerminal.running {
		name := m.adminTerminal.name
		if name == "" {
			return "Admin Terminal"
		}
		return externalAdminActionGerund(m.adminTerminal.action) + " " + name
	}
	return "Admin Approval Required"
}

func adminTerminalContextLabel(state *adminTerminalState) string {
	if state == nil {
		return ""
	}
	if state.queueTotal > 1 && state.queueIndex > 0 {
		return fmt.Sprintf("%d/%d", state.queueIndex, state.queueTotal)
	}
	return adminTerminalProviderLabel(state)
}

func adminTerminalProviderLabel(state *adminTerminalState) string {
	if state == nil {
		return ""
	}
	if state.command == "brew" && hasAdminTerminalArg(state.args, "--cask") {
		return "brew cask"
	}
	if state.installedWith != "" && state.installedWith != state.providerName {
		return state.installedWith
	}
	if state.providerName != "" {
		return state.providerName
	}
	return state.command
}

func hasAdminTerminalArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func adminTerminalApprovalMessage(state *adminTerminalState) string {
	message := adminTerminalFriendlyReason(state)
	if message != "" {
		return message
	}
	name := "this tool"
	if state != nil && state.name != "" {
		name = state.name
	}
	return "The package manager needs administrator access for " + name + "."
}

func adminTerminalFriendlyReason(state *adminTerminalState) string {
	if state == nil {
		return ""
	}
	reason := strings.TrimSpace(state.reason)
	if reason == "" {
		return ""
	}
	lower := strings.ToLower(reason)
	if strings.HasPrefix(lower, "brew cask ") {
		pkg := adminTerminalBrewCaskReasonPackage(reason)
		if pkg == "" {
			pkg = state.name
		}
		switch {
		case strings.Contains(lower, "pkg installer"):
			return "The " + pkg + " cask uses a macOS package installer."
		case strings.Contains(lower, "pkgutil"):
			return "The " + pkg + " cask needs macOS package metadata for removal."
		}
	}
	if strings.Contains(lower, "sudo") || strings.Contains(lower, "root") || strings.Contains(lower, "privileged") {
		return "The package manager needs administrator access."
	}
	fields := strings.Fields(reason)
	if len(fields) >= 2 {
		manager := fields[0]
		action := fields[1]
		pkg := state.name
		if len(fields) > 2 {
			pkg = strings.Join(fields[2:], " ")
		}
		if verb := adminTerminalFriendlyAction(action); verb != "" {
			return manager + " needs administrator access to " + verb + " " + pkg + "."
		}
	}
	return reason
}

func adminTerminalBrewCaskReasonPackage(reason string) string {
	rest := strings.TrimSpace(reason[len("brew cask "):])
	for _, suffix := range []string{
		" uses a pkg installer",
		" uses pkgutil uninstall",
	} {
		if strings.HasSuffix(rest, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(rest, suffix))
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func adminTerminalFriendlyAction(action string) string {
	switch strings.ToLower(action) {
	case "install":
		return "install"
	case "upgrade":
		return "upgrade"
	case "uninstall", "delete", "remove":
		return "remove"
	default:
		return ""
	}
}

func adminTerminalContentWidth(m Model) int {
	return popupContentWidth(m, 64, 42, 72)
}

func adminTerminalRunningContentHeight(m Model) int {
	return 5 + adminTerminalOutputHeight(m)
}

func adminTerminalOutputHeight(m Model) int {
	if m.height <= 0 {
		return 10
	}
	return clampPopupDimension(14, 6, max(m.height-12, 1))
}

func renderAdminTerminalOutput(m Model, state *adminTerminalState, width int) string {
	lines := visibleAdminTerminalOutputLines(state.output, width, adminTerminalOutputHeight(m))
	var sb strings.Builder
	sb.WriteString(alignLR(
		m.palette.styleHelp.Render("terminal"),
		m.palette.styleStatus.Render("live"),
		width,
		2,
	))
	sb.WriteByte('\n')
	sb.WriteString(renderAdminTerminalViewport(m.palette, lines, width))
	return strings.TrimRight(sb.String(), "\n")
}

func renderAdminTerminalCommandHeader(m Model, state *adminTerminalState, width int) string {
	status := "ready"
	style := m.palette.styleHelp
	if state.running {
		status = "attached"
		style = m.palette.styleStatus
	}
	header := alignLR(
		m.palette.styleHelp.Render("command"),
		style.Render(status),
		width,
		2,
	)
	command := renderAdminTerminalCommandLine(m.palette, state.display, width)
	return header + "\n" + command
}

func renderAdminTerminalCommandLine(p palette, command string, width int) string {
	prefix := p.styleHelp.Render("  ")
	avail := max(width-lipgloss.Width(prefix), 1)
	text := fitCellText(command, avail)
	line := prefix + p.styleTitle.PaddingLeft(0).Render(text)
	return lipgloss.NewStyle().Width(width).Render(line)
}

func renderAdminTerminalViewport(p palette, lines []string, width int) string {
	if width < 4 {
		return strings.Join(lines, "\n")
	}
	innerWidth := max(width-4, 1)
	border := p.styleSep
	var sb strings.Builder
	sb.WriteString(border.Render("┌" + strings.Repeat("─", width-2) + "┐"))
	for _, line := range lines {
		sb.WriteByte('\n')
		text := fitCellText(line, innerWidth)
		sb.WriteString(border.Render("│ "))
		sb.WriteString(p.styleNormal.Render(text))
		sb.WriteString(strings.Repeat(" ", max(innerWidth-lipgloss.Width(text), 0)))
		sb.WriteString(border.Render(" │"))
	}
	sb.WriteByte('\n')
	sb.WriteString(border.Render("└" + strings.Repeat("─", width-2) + "┘"))
	return sb.String()
}

func visibleAdminTerminalOutputLines(output string, width, height int) []string {
	if height <= 0 {
		return nil
	}
	output = strings.TrimRight(output, "\n")
	if output == "" {
		lines := []string{""}
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}
	raw := strings.Split(output, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\t ")
		if width > 0 && lipgloss.Width(line) > width {
			line = lipgloss.NewStyle().Inline(true).MaxWidth(width).Render(line)
		}
		lines = append(lines, line)
	}
	truncated := len(lines) > height
	if len(lines) > height {
		lines = lines[len(lines)-height:]
		if height > 1 {
			lines[0] = "..."
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if truncated && height == 1 {
		lines[0] = "..."
	}
	return lines
}

func startAdminTerminalProcess(ctx context.Context, state adminTerminalState, cols, rows int, events chan tea.Msg) (*adminTerminalSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	commandPath, env := executor.ResolveCommand(state.command)
	cmd := exec.CommandContext(ctx, commandPath, state.args...)
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, adminTerminalWinsize(cols, rows))
	if err != nil {
		return nil, err
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				sendAdminTerminalOutput(events, adminTerminalOutputMsg{id: state.id, chunk: string(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()

	doneState := state.completionState()
	go func() {
		err := cmd.Wait()
		select {
		case <-readDone:
		case <-time.After(200 * time.Millisecond):
			_ = ptmx.Close()
			select {
			case <-readDone:
			case <-time.After(50 * time.Millisecond):
			}
		}
		_ = ptmx.Close()
		events <- adminTerminalDoneMsg{id: doneState.id, state: doneState, err: err}
	}()
	return &adminTerminalSession{ptmx: ptmx}, nil
}

func sendAdminTerminalOutput(events chan tea.Msg, msg adminTerminalOutputMsg) {
	select {
	case events <- msg:
		return
	default:
	}
	select {
	case <-events:
	default:
	}
	events <- msg
}

func adminTerminalWinsize(cols, rows int) *pty.Winsize {
	if rows <= 0 {
		rows = 12
	}
	if cols <= 0 {
		cols = 80
	}
	return &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
}

func adminTerminalPTYRows(m Model) int {
	return max(adminTerminalOutputHeight(m), 8)
}

func (m *Model) resizeAdminTerminalSession() {
	if m.adminTerminal == nil || m.adminTerminal.session == nil || m.adminTerminal.session.ptmx == nil {
		return
	}
	_ = pty.Setsize(m.adminTerminal.session.ptmx, adminTerminalWinsize(adminTerminalContentWidth(*m), adminTerminalPTYRows(*m)))
}

func (m *Model) closeAdminTerminalSession() {
	if m.adminTerminal == nil || m.adminTerminal.session == nil || m.adminTerminal.session.ptmx == nil {
		return
	}
	_ = m.adminTerminal.session.ptmx.Close()
	m.adminTerminal.session = nil
}

func (m *Model) writeAdminTerminalInput(data []byte) {
	if len(data) == 0 || m.adminTerminal == nil || m.adminTerminal.session == nil || m.adminTerminal.session.ptmx == nil {
		return
	}
	if _, err := m.adminTerminal.session.ptmx.Write(data); err != nil {
		m.adminTerminal.appendOutput("\n" + err.Error() + "\n")
	}
}

func adminTerminalKeyBytes(msg tea.KeyPressMsg) []byte {
	k := msg.Key()
	if k.Text != "" {
		data := []byte(k.Text)
		if k.Mod.Contains(tea.ModAlt) {
			return append([]byte{0x1b}, data...)
		}
		return data
	}
	if k.Mod.Contains(tea.ModCtrl) {
		if b, ok := adminTerminalCtrlByte(k.Code); ok {
			return []byte{b}
		}
	}
	switch k.Code {
	case tea.KeyEnter, tea.KeyKpEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp, tea.KeyKpUp:
		return []byte("\x1b[A")
	case tea.KeyDown, tea.KeyKpDown:
		return []byte("\x1b[B")
	case tea.KeyRight, tea.KeyKpRight:
		return []byte("\x1b[C")
	case tea.KeyLeft, tea.KeyKpLeft:
		return []byte("\x1b[D")
	case tea.KeyHome, tea.KeyKpHome:
		return []byte("\x1b[H")
	case tea.KeyEnd, tea.KeyKpEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete, tea.KeyKpDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert, tea.KeyKpInsert:
		return []byte("\x1b[2~")
	case tea.KeyPgUp, tea.KeyKpPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown, tea.KeyKpPgDown:
		return []byte("\x1b[6~")
	default:
		return nil
	}
}

func adminTerminalCtrlByte(code rune) (byte, bool) {
	code = unicode.ToLower(code)
	if code >= 'a' && code <= 'z' {
		return byte(code-'a') + 1, true
	}
	switch code {
	case '[':
		return 0x1b, true
	case '\\':
		return 0x1c, true
	case ']':
		return 0x1d, true
	case '^':
		return 0x1e, true
	case '_':
		return 0x1f, true
	default:
		return 0, false
	}
}

func adminTerminalInitialOutput(state adminTerminalState) string {
	return "omni admin terminal: " + state.display + "\n\n"
}

func (s adminTerminalState) completionState() adminTerminalState {
	s.output = ""
	s.events = nil
	s.session = nil
	s.running = false
	return s
}

func (s *adminTerminalState) appendOutput(chunk string) {
	if chunk == "" {
		return
	}
	clean := sanitizeAdminTerminalOutput(chunk)
	s.output += clean
	if len(s.output) <= adminTerminalMaxOutputSize {
		return
	}
	s.output = s.output[len(s.output)-adminTerminalMaxOutputSize:]
	if idx := strings.IndexByte(s.output, '\n'); idx >= 0 && idx+1 < len(s.output) {
		s.output = s.output[idx+1:]
	}
	s.output = strings.ToValidUTF8(s.output, "")
}

func sanitizeAdminTerminalOutput(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

func shellJoin(parts []string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = shellQuote(part)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '+' || r == '@' || r == '=' || r == ',' || r == '%' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
