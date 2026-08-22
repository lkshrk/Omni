package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewSkillsBody() string {
	p := m.palette
	pad := screenEdgeInset()
	lines := []string{
		"",
		p.styleNormal.Render(pad + "Agent packages are managed by Microsoft APM."),
		p.styleHelp.Render(pad + "Manifest: ~/.apm/apm.yml"),
		p.styleHelp.Render(pad + "Lock:     ~/.apm/apm.lock.yaml"),
		"",
		p.styleHelp.Render(pad + "O onboard   T status   V resume   X cleanup   S sync   U update   R inspect   e logs"),
	}
	if item := m.currentOnboardItem(); item != nil {
		lines = append(lines, "", p.styleTitle.Render(fmt.Sprintf("%s%d/%d %s (%s)", pad, m.agentsOnboardItem+1, len(m.agentsOnboardPlan.Envelope.Plan.Items), item.Name, item.Classification)), p.styleHelp.Render(pad+"j/k inspect  c codex  l claude  b both  E executables  m map secrets  x exclude"))
		if len(item.ReasonCodes) > 0 {
			lines = append(lines, p.styleHelp.Render(pad+strings.Join(item.ReasonCodes, ", ")))
		}
		lines = append(lines, p.styleHelp.Render(fmt.Sprintf("%sdecision=%s remaining=%d", pad, item.Resolution.Decision, onboardBlockerCount(m.agentsOnboardPlan.Envelope.Plan))))
	}
	if m.apmRunning {
		lines = append(lines, "", p.styleStatus.Render(pad+"running "+m.apmCommand+"…"))
	}
	if m.apmCommand != "" && !m.apmRunning {
		lines = append(lines, "", p.styleTitle.Render(pad+m.apmCommand))
	}
	if output := strings.TrimSpace(m.apmOutput); output != "" {
		for _, line := range strings.Split(output, "\n") {
			lines = append(lines, p.styleNormal.Render(pad+line))
		}
	}
	if m.apmErr != nil {
		lines = append(lines, "", p.styleErr.Render(pad+m.apmErr.Error()))
	}
	return strings.Join(lines, "\n") + "\n"
}
