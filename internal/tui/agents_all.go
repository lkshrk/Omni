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
	m.agentsOnboardConfirm, m.apmRunning, m.apmCommand = false, true, "omni agents onboard (apply)"
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
	return text
}

func (m *Model) currentOnboardItem() *app.OnboardItem {
	if m.agentsOnboardPlan == nil || m.agentsOnboardPlan.Envelope.Plan == nil || len(m.agentsOnboardPlan.Envelope.Plan.Items) == 0 {
		return nil
	}
	if m.agentsOnboardItem >= len(m.agentsOnboardPlan.Envelope.Plan.Items) {
		m.agentsOnboardItem = 0
	}
	return &m.agentsOnboardPlan.Envelope.Plan.Items[m.agentsOnboardItem]
}
func resolveOnboardItem(item *app.OnboardItem, key string) bool {
	if item == nil {
		return false
	}
	options := item.TargetOptions
	if key == "a" && len(options) > 0 {
		setOnboardTargetDecision(item, true)
		item.Resolution.ApprovedTargets = append([]string(nil), options...)
		return true
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		index := int(key[0] - '1')
		if index >= len(options) {
			return false
		}
		target := options[index]
		if slices.Contains(item.Resolution.ApprovedTargets, target) {
			item.Resolution.ApprovedTargets = slices.DeleteFunc(item.Resolution.ApprovedTargets, func(value string) bool { return value == target })
		} else {
			item.Resolution.ApprovedTargets = append(item.Resolution.ApprovedTargets, target)
			sort.Strings(item.Resolution.ApprovedTargets)
		}
		if len(item.Resolution.ApprovedTargets) == 0 {
			setOnboardTargetDecision(item, false)
		} else {
			setOnboardTargetDecision(item, true)
		}
		return true
	}
	switch key {
	case "x":
		item.Resolution.Decision = "keep-unmanaged"
	case "d":
		if item.Dots == nil {
			return false
		}
		item.Resolution.Decision = "keep-in-dots"
	case "M":
		if item.Dots == nil {
			return false
		}
		item.Resolution.Decision = "move-to-apm"
	case "m":
		item.Resolution.Decision = "map-secret"
		if item.Resolution.EnvBindings == nil {
			item.Resolution.EnvBindings = map[string]string{}
		}
		for _, field := range onboardSecretFields(item.Payload) {
			item.Resolution.EnvBindings[field] = onboardEnvName(item.Name + "_" + field)
		}
	default:
		return false
	}
	return true
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
	if m.agentsOnboardConfirm {
		switch key {
		case "y", "Y":
			return true, m.doAgentsOnboardApply()
		case "n", "N", "esc":
			m.agentsOnboardConfirm = false
			m.agentsOnboardPlan = nil
			return true, []tea.Cmd{setStatus(m, "Agent onboarding cancelled.", false)}
		}
		return true, nil
	}
	if m.agentsOnboardPlan != nil {
		items := m.agentsOnboardPlan.Envelope.Plan.Items
		switch key {
		case "j":
			if len(items) > 0 {
				m.agentsOnboardItem = (m.agentsOnboardItem + 1) % len(items)
			}
		case "k":
			if len(items) > 0 {
				m.agentsOnboardItem = (m.agentsOnboardItem - 1 + len(items)) % len(items)
			}
		case "esc":
			m.agentsOnboardPlan = nil
		case "enter":
			if onboardBlockerCount(m.agentsOnboardPlan.Envelope.Plan) == 0 {
				m.agentsOnboardConfirm = true
				return true, []tea.Cmd{setStatus(m, "Apply this onboarding plan? y/N", false)}
			}
		default:
			resolveOnboardItem(m.currentOnboardItem(), key)
		}
		if m.agentsOnboardPlan != nil {
			m.apmOutput = onboardPlanSummary(*m.agentsOnboardPlan)
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
