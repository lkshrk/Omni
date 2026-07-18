package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
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

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

const (
	adminTerminalEventBuffer   = 128
	adminTerminalMaxOutputSize = 64 * 1024
	adminTerminalSudoPrompt    = "Omni needs your sudo password: "
)

type adminTerminalState struct {
	action                 app.PrivilegeAction
	name                   string
	providerName           string
	pkg                    string
	installedWith          string
	options                map[string]string
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
	finished               bool
	finishErr              error
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

type adminTerminalStartedMsg struct {
	id      int
	session *adminTerminalSession
	state   adminTerminalState
	err     error
}

type adminTerminalDoneMsg struct {
	id    int
	state adminTerminalState
	err   error
}

func (m *Model) openAdminTerminalPrompt(t *app.ToolView, action app.PrivilegeAction, plan app.PrivilegePlan) bool {
	state, ok := m.adminTerminalStateForTool(t, action, plan)
	if !ok {
		return false
	}
	return m.openAdminTerminalState(state)
}

func (m *Model) adminTerminalStateForTool(t *app.ToolView, action app.PrivilegeAction, plan app.PrivilegePlan) (adminTerminalState, bool) {
	if t == nil || m.app == nil {
		return adminTerminalState{}, false
	}
	commandPlan, ok := m.app.PrivilegedToolCommand(m.ctx, t, action, plan)
	if !ok {
		return adminTerminalState{}, false
	}
	return m.adminTerminalStateForCommand(commandPlan), true
}

func (m *Model) adminTerminalStateForCommand(commandPlan app.PrivilegedToolCommand) adminTerminalState {
	return adminTerminalState{
		action:        commandPlan.Action,
		name:          commandPlan.Name,
		providerName:  commandPlan.ProviderName,
		pkg:           commandPlan.Package,
		installedWith: commandPlan.InstalledWith,
		options:       maps.Clone(commandPlan.Options),
		reason:        commandPlan.Reason,
		command:       commandPlan.Command,
		args:          commandPlan.Args,
		display:       commandPlan.Display,
		returnMode:    m.mode,
		rowKey:        toolKey(commandPlan.Name, commandPlan.ProviderName),
	}
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

func (m *Model) handleAdminTerminalKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if m.adminTerminal == nil {
		m.mode = viewList
		return nil
	}
	if m.adminTerminal.finished {
		state := m.adminTerminal.completionState()
		err := m.adminTerminal.finishErr
		m.mode = m.adminTerminal.returnMode
		m.adminTerminal = nil
		return m.finalizeAdminTerminal(state, err)
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
	if state.action == app.PrivilegeActionUpgrade {
		if m.upgradingKeys == nil {
			m.upgradingKeys = make(map[string]bool)
		}
		m.upgradingKeys[state.rowKey] = true
	}
	ctx := m.ctx
	processState := *state
	cols := adminTerminalContentWidth(*m)
	rows := adminTerminalPTYRows(*m)
	traceSink := m.app
	return func() tea.Msg {
		session, err := startAdminTerminalProcess(ctx, processState, cols, rows, processState.events, traceSink)
		return adminTerminalStartedMsg{
			id:      processState.id,
			session: session,
			state:   processState.completionState(),
			err:     err,
		}
	}
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

func (m *Model) handleAdminTerminalStartedMsg(msg adminTerminalStartedMsg) []tea.Cmd {
	if m.adminTerminal == nil || m.adminTerminal.id != msg.id || !m.adminTerminal.running {
		if msg.session != nil {
			_ = msg.session.ptmx.Close()
		}
		return nil
	}
	if msg.err != nil {
		return m.handleAdminTerminalDoneMsg(adminTerminalDoneMsg{id: msg.id, state: msg.state, err: msg.err})
	}
	m.adminTerminal.session = msg.session
	return []tea.Cmd{waitAdminTerminalEvent(msg.id, m.adminTerminal.events)}
}

func (m *Model) handleAdminTerminalDoneMsg(msg adminTerminalDoneMsg) []tea.Cmd {
	if m.adminTerminal != nil && msg.id != 0 && m.adminTerminal.id != msg.id {
		return nil
	}
	// Keep the terminal open after the process exits so its output and any
	// error stay visible; the user dismisses it with a keypress, which then
	// runs the post-action refresh/error handling.
	if m.adminTerminal != nil {
		m.closeAdminTerminalSession()
		m.adminTerminal.running = false
		m.adminTerminal.finished = true
		m.adminTerminal.finishErr = msg.err
		m.loading = false
		return nil
	}
	if msg.state.returnMode != viewAdminTerminal {
		m.mode = msg.state.returnMode
	}
	return m.finalizeAdminTerminal(msg.state, msg.err)
}

// finalizeAdminTerminal runs the post-completion handling once the user has
// dismissed the finished terminal (or when no view was open to keep).
func (m *Model) finalizeAdminTerminal(state adminTerminalState, err error) []tea.Cmd {
	if err != nil {
		return m.handleOpCompleteMsg(opCompleteMsg{key: state.rowKey, err: fmt.Errorf("admin terminal: %w", err)})
	}
	m.startRowOperation(state.name, state.providerName, "Refreshing…")
	return []tea.Cmd{m.doCompleteAdminTerminalAction(state)}
}

func (m *Model) doCompleteAdminTerminalAction(state adminTerminalState) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		assignHosts := []string(nil)
		if state.addHost != "" {
			assignHosts = []string{state.addHost}
		}
		result, err := a.CompleteExternalToolActionWithState(ctx, app.CompleteExternalToolActionOptions{
			Action:        state.action,
			Name:          state.name,
			ProviderName:  state.providerName,
			Package:       state.pkg,
			InstalledWith: state.installedWith,
			Options:       maps.Clone(state.options),
			AddToConfig:   state.addToConfig,
			GroupName:     state.addGroup,
			AssignHosts:   assignHosts,
		})
		msg := opCompleteMsg{
			key:                    state.rowKey,
			preserveOtherRowErrors: state.preserveOtherRowErrors,
		}
		if err == nil {
			msg.message = adminTerminalSuccessMessage(state)
		}
		if result != nil {
			msg.tools = result.Tools
			if result.GroupState != nil {
				msg.groupNames = result.GroupState.GroupNames
				msg.toolGroups = result.GroupState.ToolGroups
				msg.toolMemberships = result.GroupState.ToolMemberships
				msg.hostInfo = result.GroupState.HostInfo
			}
		}
		if state.addToConfig && state.action == app.PrivilegeActionInstall {
			msg.removeDiscoveredKeys = []string{toolKey(state.name, state.providerName)}
			if err == nil {
				msg.message = "installed " + state.name + " and added to config"
			}
		}
		if err != nil {
			msg.err = err
		}
		return msg
	}
}

func adminTerminalRunningStatus(state adminTerminalState) string {
	return externalAdminActionGerund(state.action) + " " + state.name + "…"
}

func adminTerminalSuccessMessage(state adminTerminalState) string {
	switch state.action {
	case app.PrivilegeActionInstall:
		return "installed " + state.name
	case app.PrivilegeActionUninstall:
		return "deleted " + state.name
	case app.PrivilegeActionUpgrade:
		return "upgraded " + state.name
	default:
		return externalAdminActionVerb(state.action) + " " + state.name
	}
}

func externalAdminActionVerb(action app.PrivilegeAction) string {
	switch action {
	case app.PrivilegeActionInstall:
		return "install"
	case app.PrivilegeActionUninstall:
		return "delete"
	case app.PrivilegeActionUpgrade:
		return "upgrade"
	default:
		return string(action)
	}
}

func externalAdminActionGerund(action app.PrivilegeAction) string {
	switch action {
	case app.PrivilegeActionInstall:
		return "Installing"
	case app.PrivilegeActionUninstall:
		return "Deleting"
	case app.PrivilegeActionUpgrade:
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
	switch {
	case state.finished:
		content = renderAdminTerminalFinishedPopup(m, state, width)
	case state.running:
		content = renderAdminTerminalRunningPopup(m, state, width)
	default:
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
	if adminTerminalAwaitingPassword(state.output) {
		sb.WriteString(renderAdminTerminalPasswordNotice(m, width))
		sb.WriteString("\n\n")
	}
	sb.WriteString(renderAdminTerminalOutput(m, state, width))
	return strings.TrimRight(sb.String(), "\n")
}

func renderAdminTerminalPasswordNotice(m Model, width int) string {
	lines := []string{m.palette.styleOutdated.Render(fitCellText("⚠ PASSWORD REQUIRED", width))}
	for _, item := range []struct {
		text  string
		style lipgloss.Style
	}{
		{text: "Type your sudo password and press Enter.", style: m.palette.styleActiveText},
		{text: "Nothing will appear while you type.", style: m.palette.styleHelp},
	} {
		for _, line := range wrapText(item.text, max(width, 1)) {
			lines = append(lines, item.style.Render(line))
		}
	}
	return strings.Join(lines, "\n")
}

func renderAdminTerminalFinishedPopup(m Model, state *adminTerminalState, width int) string {
	var sb strings.Builder
	sb.WriteString(renderAdminTerminalCommandHeader(m, state, width))
	sb.WriteByte('\n')
	sb.WriteString(renderAdminTerminalOutput(m, state, width))
	sb.WriteString("\n\n")
	if state.finishErr != nil {
		sb.WriteString(m.palette.styleErr.Render("failed: " + state.finishErr.Error()))
	} else {
		sb.WriteString(m.palette.styleInstalled.Render("done"))
	}
	sb.WriteString("\n\n")
	sb.WriteString(m.palette.styleHelp.Render("press any key to close"))
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
		if adminTerminalAwaitingPassword(m.adminTerminal.output) {
			if name == "" {
				return "Password Required"
			}
			return "Password Required · " + name
		}
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
	state := m.adminTerminal
	width := adminTerminalContentWidth(m)
	height := 5 + adminTerminalVisibleOutputHeight(m, state, width)
	if state != nil && adminTerminalAwaitingPassword(state.output) {
		height += adminTerminalPasswordNoticeRows(m, width)
	}
	return height
}

func adminTerminalOutputHeight(m Model) int {
	if m.height <= 0 {
		return 10
	}
	return clampPopupDimension(14, 6, max(m.height-12, 1))
}

func renderAdminTerminalOutput(m Model, state *adminTerminalState, width int) string {
	awaitingPassword := adminTerminalAwaitingPassword(state.output)
	height := adminTerminalVisibleOutputHeight(m, state, width)
	lines := visibleAdminTerminalOutputLines(state.output, width, height)
	header := alignLR(
		m.palette.styleHelp.Render("terminal"),
		m.palette.styleStatus.Render("live"),
		width,
		2,
	)
	if awaitingPassword {
		statusWidth := max(width-lipgloss.Width("terminal")-2, 1)
		header = m.palette.styleHelp.Render("terminal") + "  " +
			m.palette.styleOutdated.Render(fitCellText("input required", statusWidth))
		header = lipgloss.NewStyle().Width(width).Render(header)
	}
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteByte('\n')
	sb.WriteString(renderAdminTerminalViewport(m.palette, lines, width))
	return strings.TrimRight(sb.String(), "\n")
}

func adminTerminalVisibleOutputHeight(m Model, state *adminTerminalState, width int) int {
	height := adminTerminalOutputHeight(m)
	if state != nil && adminTerminalAwaitingPassword(state.output) {
		height = max(height-adminTerminalPasswordNoticeRows(m, width), 1)
	}
	return height
}

func adminTerminalPasswordNoticeRows(m Model, width int) int {
	return lipgloss.Height(renderAdminTerminalPasswordNotice(m, width)) + 1
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
		textStyle := p.styleNormal
		if adminTerminalPasswordPrompt(text) {
			textStyle = p.styleOutdated
		}
		sb.WriteString(border.Render("│ "))
		sb.WriteString(textStyle.Render(text))
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

func adminTerminalAwaitingPassword(output string) bool {
	output = strings.TrimRight(output, "\t\r\n ")
	if output == "" {
		return false
	}
	if idx := strings.LastIndexByte(output, '\n'); idx >= 0 {
		output = output[idx+1:]
	}
	return adminTerminalPasswordPrompt(output)
}

func adminTerminalPasswordPrompt(line string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	if !strings.HasSuffix(line, ":") {
		return false
	}
	return line == strings.ToLower(strings.TrimSpace(adminTerminalSudoPrompt)) ||
		strings.HasPrefix(line, "[sudo] password") ||
		line == "password:" ||
		strings.HasPrefix(line, "password for ") ||
		line == "enter password:" ||
		strings.HasPrefix(line, "enter password for ") ||
		line == "passphrase:" ||
		strings.HasPrefix(line, "passphrase for ") ||
		strings.HasPrefix(line, "enter passphrase for ")
}

func startAdminTerminalProcess(ctx context.Context, state adminTerminalState, cols, rows int, events chan tea.Msg, traceSink executor.TraceSink) (*adminTerminalSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now().UTC()
	commandPath, env := executor.ResolveCommand(state.command)
	cmd := exec.CommandContext(ctx, commandPath, state.args...)
	cmd.Env = adminTerminalProcessEnv(env)

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
		recordAdminTerminalTrace(ctx, traceSink, state, started, time.Now().UTC(), err)
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
		select {
		case events <- adminTerminalDoneMsg{id: doneState.id, state: doneState, err: err}:
		case <-ctx.Done():
		}
	}()
	return &adminTerminalSession{ptmx: ptmx}, nil
}

func adminTerminalProcessEnv(env []string) []string {
	const key = "SUDO_PROMPT="
	prompt := key + adminTerminalSudoPrompt
	result := append([]string(nil), env...)
	for i, entry := range result {
		if strings.HasPrefix(entry, key) {
			result[i] = prompt
			return result
		}
	}
	return append(result, prompt)
}

func recordAdminTerminalTrace(ctx context.Context, sink executor.TraceSink, state adminTerminalState, started, finished time.Time, err error) {
	if sink == nil {
		return
	}
	reason := strings.TrimSpace(state.reason)
	if reason == "" {
		reason = "admin terminal command"
	}
	status := "success"
	if err != nil {
		status = "failed"
	}
	if ctx != nil && ctx.Err() != nil {
		status = "canceled"
	}
	exitCode := sql.NullInt64{}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = sql.NullInt64{Int64: int64(exitErr.ExitCode()), Valid: true}
	}
	_ = sink.RecordCommandTrace(context.WithoutCancel(ctx), executor.TraceRecord{
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: finished.Sub(started).Milliseconds(),
		Reason:     reason,
		Command:    executor.RenderCommand(state.command, state.args...),
		Status:     status,
		ExitCode:   exitCode,
		Error:      adminTerminalTraceError(err),
	})
}

func adminTerminalTraceError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// sendAdminTerminalOutput sends an output message to the event channel.
// If the channel is full it drains up to 4 old output messages to make room,
// skipping any non-output messages (e.g. doneMsg) so they are never lost.
func sendAdminTerminalOutput(events chan tea.Msg, msg adminTerminalOutputMsg) {
	select {
	case events <- msg:
		return
	default:
	}
drain:
	for range 4 {
		select {
		case old := <-events:
			if _, ok := old.(adminTerminalOutputMsg); !ok {
				// Non-output message (e.g. doneMsg) — put it back and stop draining.
				events <- old
				break drain
			}
		default:
			break drain
		}
	}
	select {
	case events <- msg:
	default:
		// Channel still full after draining; drop this chunk to avoid blocking.
	}
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
