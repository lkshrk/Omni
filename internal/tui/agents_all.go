package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

const agentsBusyStatus = "⚠ APM busy — wait for the running command to finish"

type agentsOnboardPromptKind uint8

const (
	agentsPromptOwnership agentsOnboardPromptKind = iota + 1
	agentsPromptTargets
	agentsPromptSecret
	agentsPromptBlocked
	agentsPromptApply
)

type agentsOnboardPrompt struct {
	kind         agentsOnboardPromptKind
	item         int
	cursor       int
	secretFields []string
	secret       int
}

type apmCommandDoneMsg struct {
	command string
	stdout  string
	stderr  string
	err     error
}

type agentsOnboardPlanDoneMsg struct {
	result app.AgentsOnboardResult
	err    error
}
type agentsOnboardApplyDoneMsg struct {
	result app.AgentsOnboardResult
	err    error
}
type agentsOnboardStatusDoneMsg struct {
	result app.AgentsOnboardStatusResult
	err    error
}
type agentsOnboardCleanupDoneMsg struct {
	preview   app.AgentsOnboardCleanupPreview
	confirmed bool
	err       error
}

func (m *Model) agentsOpInFlight() bool { return m.apmRunning }

func (m *Model) runAPM(command string, args ...string) []tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.apmRunning = true
	m.apmCommand = command
	m.apmOutput = ""
	m.apmErr = nil
	a, ctx := m.app, m.ctx
	return []tea.Cmd{m.spinner.Tick, func() tea.Msg {
		result, err := a.RunAPM(ctx, args...)
		return apmCommandDoneMsg{command: command, stdout: result.Stdout, stderr: result.Stderr, err: err}
	}}
}

func (m *Model) doAgentsSyncAll() []tea.Cmd {
	return m.runAPM("apm install -g", "install", "-g")
}

func (m *Model) doAgentsUpdateAll() []tea.Cmd {
	return m.runAPM("apm update -g --yes", "update", "-g", "--yes")
}

func (m *Model) doAgentsRefresh() []tea.Cmd {
	return m.runAPM("apm deps list -g", "deps", "list", "-g")
}

func (m *Model) doAgentsOnboardPlan() []tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.apmRunning, m.apmCommand, m.apmOutput, m.apmErr = true, "omni agents onboard (preview)", "", nil
	a, ctx := m.app, m.ctx
	return []tea.Cmd{m.spinner.Tick, func() tea.Msg {
		result, err := a.AgentsOnboardPlan(ctx, app.AgentsOnboardOptions{})
		return agentsOnboardPlanDoneMsg{result: result, err: err}
	}}
}

func (m *Model) doAgentsOnboardApply() []tea.Cmd {
	if m.app == nil || m.agentsOnboardPlan == nil || m.agentsOnboardPlan.Envelope.Plan == nil {
		return nil
	}
	plan := *m.agentsOnboardPlan.Envelope.Plan
	m.clearAgentsOnboardPrompt()
	m.apmRunning, m.apmCommand = true, "omni agents onboard (apply)"
	a, ctx := m.app, m.ctx
	return []tea.Cmd{m.spinner.Tick, func() tea.Msg {
		result, err := a.AgentsOnboardApplyReviewed(ctx, plan)
		return agentsOnboardApplyDoneMsg{result: result, err: err}
	}}
}

func onboardPlanSummary(result app.AgentsOnboardResult) string {
	if result.Envelope.Plan == nil {
		return "No onboarding plan returned."
	}
	plan := result.Envelope.Plan
	text := fmt.Sprintf("Agent onboarding preview: %d item(s), %d blocker(s).", len(plan.Items), onboardBlockerCount(plan))
	blocked := []string{}
	for i := range plan.Items {
		if onboardItemBlockerCount(plan.Items[i]) > 0 {
			blocked = append(blocked, plan.Items[i].Name)
		}
	}
	if len(blocked) > 0 {
		text += " Unresolved: " + strings.Join(blocked, ",")
	}
	for _, blocker := range plan.Blockers {
		if !slices.ContainsFunc(plan.Items, func(item app.OnboardItem) bool { return slices.Contains(item.Blockers, blocker) }) {
			reason := blocker
			if _, suffix, ok := strings.Cut(blocker, ":"); ok {
				reason = suffix
			}
			text += " Plan blocker: " + reason
		}
	}
	return text
}

func (m *Model) beginAgentsOnboardReview() {
	m.agentsOnboardReviewed = map[string]bool{}
	m.advanceAgentsOnboardPrompt()
}

func (m *Model) clearAgentsOnboardPrompt() {
	m.agentsOnboardPrompt = nil
	m.agentsOnboardConfirm = false
	m.settingsInput.Blur()
}

func (m *Model) cancelAgentsOnboardReview() []tea.Cmd {
	m.clearAgentsOnboardPrompt()
	m.agentsOnboardPlan = nil
	m.agentsOnboardReviewed = nil
	return []tea.Cmd{setStatus(m, "Agent onboarding cancelled.", false)}
}

func onboardReviewKey(item app.OnboardItem, index int) string {
	if item.ID != "" {
		return item.ID
	}
	return fmt.Sprintf("#%d", index)
}

func onboardHasBlocker(item app.OnboardItem, match func(string) bool) bool {
	return slices.ContainsFunc(item.Blockers, match)
}

func onboardItemNeedsPrompt(item app.OnboardItem, ownershipReviewed bool) bool {
	if item.Dots != nil && !ownershipReviewed {
		return true
	}
	if item.Resolution.Decision == "keep-unmanaged" || item.Resolution.Decision == "keep-in-dots" {
		return false
	}
	if onboardHasBlocker(item, func(blocker string) bool {
		return blocker == "target-resolution-required" || strings.HasPrefix(blocker, "unknown-target:")
	}) && !allOnboardTargetsAllowed(item) {
		return true
	}
	if onboardHasBlocker(item, func(blocker string) bool { return blocker == "secret-mapping-required" }) {
		fields := onboardSecretFields(item.Payload)
		if len(fields) == 0 {
			return true
		}
		for _, field := range fields {
			if item.Resolution.EnvBindings[field] == "" {
				return true
			}
		}
	}
	return onboardItemBlockerCount(item) > 0
}

func (m *Model) keepRemainingOnboardItemsUnmanaged() int {
	if m.agentsOnboardPlan == nil || m.agentsOnboardPlan.Envelope.Plan == nil {
		return 0
	}
	count := 0
	for i := range m.agentsOnboardPlan.Envelope.Plan.Items {
		item := &m.agentsOnboardPlan.Envelope.Plan.Items[i]
		key := onboardReviewKey(*item, i)
		if !onboardItemNeedsPrompt(*item, m.agentsOnboardReviewed[key]) {
			continue
		}
		if item.Resolution.Decision != "keep-unmanaged" {
			count++
		}
		item.Resolution.Decision = "keep-unmanaged"
		m.agentsOnboardReviewed[key] = true
	}
	m.advanceAgentsOnboardPrompt()
	return count
}

func (m *Model) advanceAgentsOnboardPrompt() {
	m.clearAgentsOnboardPrompt()
	if m.agentsOnboardPlan == nil || m.agentsOnboardPlan.Envelope.Plan == nil {
		return
	}
	m.apmOutput = onboardPlanSummary(*m.agentsOnboardPlan)
	plan := m.agentsOnboardPlan.Envelope.Plan
	for _, blocker := range plan.Blockers {
		if !slices.ContainsFunc(plan.Items, func(item app.OnboardItem) bool { return slices.Contains(item.Blockers, blocker) }) {
			m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: agentsPromptBlocked, item: -1}
			return
		}
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Dots != nil && !m.agentsOnboardReviewed[onboardReviewKey(*item, i)] {
			m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: agentsPromptOwnership, item: i}
			return
		}
		if item.Resolution.Decision == "keep-unmanaged" || item.Resolution.Decision == "keep-in-dots" {
			continue
		}
		needsTargets := onboardHasBlocker(*item, func(blocker string) bool {
			return blocker == "target-resolution-required" || strings.HasPrefix(blocker, "unknown-target:")
		}) && !allOnboardTargetsAllowed(*item)
		if needsTargets {
			item.Resolution.ApprovedTargets = slices.DeleteFunc(item.Resolution.ApprovedTargets, func(target string) bool {
				return !slices.Contains(item.TargetOptions, target)
			})
			kind := agentsPromptTargets
			if len(item.TargetOptions) == 0 {
				kind = agentsPromptBlocked
			}
			m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: kind, item: i}
			return
		}
		if onboardHasBlocker(*item, func(blocker string) bool { return blocker == "secret-mapping-required" }) {
			fields := onboardSecretFields(item.Payload)
			for fieldIndex, field := range fields {
				if item.Resolution.EnvBindings[field] == "" {
					m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: agentsPromptSecret, item: i, secretFields: fields, secret: fieldIndex}
					m.settingsInput.SetValue(onboardEnvName(item.Name + "_" + field))
					m.settingsInput.CursorEnd()
					m.settingsInput.Focus()
					return
				}
			}
			if len(fields) == 0 {
				m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: agentsPromptBlocked, item: i}
				return
			}
		}
		if onboardItemBlockerCount(*item) > 0 {
			m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: agentsPromptBlocked, item: i}
			return
		}
	}
	if onboardBlockerCount(plan) == 0 {
		m.agentsOnboardConfirm = true
		m.agentsOnboardPrompt = &agentsOnboardPrompt{kind: agentsPromptApply, item: -1}
	}
}

func moveAgentsOnboardCursor(prompt *agentsOnboardPrompt, delta, count int) {
	if prompt != nil && count > 0 {
		prompt.cursor = (prompt.cursor + delta + count) % count
	}
}

func validOnboardEnvName(value string) bool {
	for i, r := range value {
		if !(r == '_' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return value != ""
}

func (m *Model) handleAgentsOnboardPromptKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	prompt := m.agentsOnboardPrompt
	if prompt == nil {
		return false, nil
	}
	key := msg.String()
	if key == "esc" || key == "n" && prompt.kind == agentsPromptApply {
		return true, m.cancelAgentsOnboardReview()
	}
	if key == "ctrl+x" && prompt.item >= 0 && prompt.kind != agentsPromptApply {
		count := m.keepRemainingOnboardItemsUnmanaged()
		return true, []tea.Cmd{setStatus(m, fmt.Sprintf("Kept %d remaining finding(s) unmanaged.", count), false)}
	}
	if prompt.kind == agentsPromptSecret {
		if key != "enter" {
			var cmd tea.Cmd
			m.settingsInput, cmd = m.settingsInput.Update(msg)
			return true, []tea.Cmd{cmd}
		}
		value := strings.TrimSpace(m.settingsInput.Value())
		if !validOnboardEnvName(value) {
			return true, []tea.Cmd{setStatus(m, "Use an environment variable name containing only A-Z, 0-9, and _.", true)}
		}
		item := &m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item]
		if item.Resolution.EnvBindings == nil {
			item.Resolution.EnvBindings = map[string]string{}
		}
		item.Resolution.EnvBindings[prompt.secretFields[prompt.secret]] = value
		m.advanceAgentsOnboardPrompt()
		return true, nil
	}
	count := 2
	switch prompt.kind {
	case agentsPromptTargets:
		count = len(m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item].TargetOptions)
	case agentsPromptBlocked:
		count = 1
	}
	switch key {
	case "up", "k", "left", "h":
		moveAgentsOnboardCursor(prompt, -1, count)
		return true, nil
	case "down", "j", "right", "l":
		moveAgentsOnboardCursor(prompt, 1, count)
		return true, nil
	}
	item := func() *app.OnboardItem {
		if prompt.item < 0 {
			return nil
		}
		return &m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item]
	}()
	switch prompt.kind {
	case agentsPromptTargets:
		if key == "a" {
			item.Resolution.ApprovedTargets = append([]string(nil), item.TargetOptions...)
			setOnboardTargetDecision(item, true)
			m.advanceAgentsOnboardPrompt()
			return true, nil
		}
		if key == " " || key == "space" {
			target := item.TargetOptions[prompt.cursor]
			if slices.Contains(item.Resolution.ApprovedTargets, target) {
				item.Resolution.ApprovedTargets = slices.DeleteFunc(item.Resolution.ApprovedTargets, func(value string) bool { return value == target })
			} else {
				item.Resolution.ApprovedTargets = append(item.Resolution.ApprovedTargets, target)
				sort.Strings(item.Resolution.ApprovedTargets)
			}
			return true, nil
		}
		if key == "enter" {
			if !allOnboardTargetsAllowed(*item) {
				return true, []tea.Cmd{setStatus(m, "Select at least one target.", true)}
			}
			setOnboardTargetDecision(item, true)
			m.advanceAgentsOnboardPrompt()
		}
	case agentsPromptOwnership:
		if key == "enter" {
			if prompt.cursor == 0 {
				item.Resolution.Decision = "move-to-apm"
			} else if item.Dots.Native {
				item.Resolution.Decision = "keep-unmanaged"
			} else {
				item.Resolution.Decision = "keep-in-dots"
			}
			m.agentsOnboardReviewed[onboardReviewKey(*item, prompt.item)] = true
			m.advanceAgentsOnboardPrompt()
		}
	case agentsPromptBlocked:
		if prompt.item >= 0 && key == "enter" {
			item.Resolution.Decision = "keep-unmanaged"
			m.advanceAgentsOnboardPrompt()
		}
	case agentsPromptApply:
		if key == "y" || key == "Y" || key == "enter" && prompt.cursor == 0 {
			return true, m.doAgentsOnboardApply()
		}
		if key == "enter" {
			return true, m.cancelAgentsOnboardReview()
		}
	}
	return true, nil
}

func setOnboardTargetDecision(item *app.OnboardItem, selected bool) {
	if item.Dots != nil {
		return
	}
	if selected {
		item.Resolution.Decision = "migrate"
	} else {
		item.Resolution.Decision = ""
	}
}

func onboardEnvName(value string) string {
	var out strings.Builder
	out.WriteString("OMNI_")
	for _, r := range strings.ToUpper(value) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}

func onboardSecretFields(payload json.RawMessage) []string {
	var object map[string]any
	if json.Unmarshal(payload, &object) != nil {
		return nil
	}
	var fields []string
	for _, key := range []string{"env", "headers"} {
		values, _ := object[key].(map[string]any)
		for field, raw := range values {
			blocked, _ := raw.(map[string]any)
			if blocked["blocked"] != nil {
				fields = append(fields, field)
			}
		}
	}
	sort.Strings(fields)
	return fields
}

func (m *Model) doAgentsOnboardStatus(resume bool) []tea.Cmd {
	if m.app == nil || m.agentsOnboardOperation == "" {
		return []tea.Cmd{setStatus(m, "No recoverable onboarding operation.", true)}
	}
	m.apmRunning = true
	a, ctx, op := m.app, m.ctx, m.agentsOnboardOperation
	return []tea.Cmd{m.spinner.Tick, func() tea.Msg {
		var result app.AgentsOnboardStatusResult
		var err error
		if resume {
			result, err = a.AgentsOnboardResume(ctx, op)
		} else {
			result, err = a.AgentsOnboardStatus(ctx, op)
		}
		return agentsOnboardStatusDoneMsg{result: result, err: err}
	}}
}
func (m *Model) doAgentsOnboardCleanup(confirm bool) []tea.Cmd {
	if m.app == nil || m.agentsOnboardOperation == "" {
		return []tea.Cmd{setStatus(m, "No onboarding operation to clean.", true)}
	}
	m.apmRunning = true
	a, ctx, op := m.app, m.ctx, m.agentsOnboardOperation
	return []tea.Cmd{m.spinner.Tick, func() tea.Msg {
		preview, err := a.AgentsOnboardCleanup(ctx, op, confirm)
		return agentsOnboardCleanupDoneMsg{preview: preview, confirmed: confirm, err: err}
	}}
}

func onboardBlockerCount(plan *app.OnboardPlan) int {
	if plan == nil {
		return 0
	}
	count := 0
	itemBlockers := map[string]bool{}
	for _, item := range plan.Items {
		for _, blocker := range item.Blockers {
			itemBlockers[blocker] = true
		}
		count += onboardItemBlockerCount(item)
	}
	for _, blocker := range plan.Blockers {
		if !itemBlockers[blocker] {
			count++
		}
	}
	return count
}

func onboardItemBlockerCount(item app.OnboardItem) int {
	count := 0
	for _, blocker := range item.Blockers {
		if item.Resolution.Decision == "keep-unmanaged" || item.Resolution.Decision == "keep-in-dots" {
			continue
		}
		if blocker == "target-resolution-required" && len(item.Resolution.ApprovedTargets) > 0 {
			continue
		}
		if strings.HasPrefix(blocker, "unknown-target:") && allOnboardTargetsAllowed(item) {
			continue
		}
		if blocker == "secret-mapping-required" && len(item.Resolution.EnvBindings) > 0 {
			continue
		}
		count++
	}
	return count
}

func allOnboardTargetsAllowed(item app.OnboardItem) bool {
	if len(item.Resolution.ApprovedTargets) == 0 {
		return false
	}
	for _, target := range item.Resolution.ApprovedTargets {
		if !slices.Contains(item.TargetOptions, target) {
			return false
		}
	}
	return true
}

func (m *Model) handleAgentsGlobalActionKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	key := msg.String()
	if m.agentsOnboardCleanupConfirm {
		if key == "y" || key == "Y" {
			m.agentsOnboardCleanupConfirm = false
			return true, m.doAgentsOnboardCleanup(true)
		}
		if key == "n" || key == "N" || key == "esc" {
			m.agentsOnboardCleanupConfirm = false
			return true, []tea.Cmd{setStatus(m, "Cleanup cancelled.", false)}
		}
		return true, nil
	}
	if key != "U" && key != "S" && key != "R" && key != "O" && key != "T" && key != "V" && key != "X" && key != "e" {
		return false, nil
	}
	if key == "e" {
		return true, []tea.Cmd{m.openTraceLog()}
	}
	if m.agentsOpInFlight() {
		return true, []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	switch key {
	case "O":
		return true, m.doAgentsOnboardPlan()
	case "T":
		return true, m.doAgentsOnboardStatus(false)
	case "V":
		return true, m.doAgentsOnboardStatus(true)
	case "X":
		return true, m.doAgentsOnboardCleanup(false)
	case "U":
		return true, m.doAgentsUpdateAll()
	case "S":
		return true, m.doAgentsSyncAll()
	default:
		return true, m.doAgentsRefresh()
	}
}

func apmCommandOutput(stdout, stderr string) string {
	parts := make([]string, 0, 2)
	if stdout = strings.TrimSpace(stdout); stdout != "" {
		parts = append(parts, stdout)
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		parts = append(parts, stderr)
	}
	return strings.Join(parts, "\n")
}
