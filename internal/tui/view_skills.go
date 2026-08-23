package tui

import (
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
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
		p.styleHelp.Render(pad + "O onboard   P project   T status   V resume   X cleanup   S sync   U update   R inspect   e logs"),
	}
	if item := m.currentOnboardItem(); item != nil {
		lines = append(lines, "", p.styleTitle.Render(fmt.Sprintf("%s%d/%d %s (%s)", pad, m.agentsOnboardItem+1, len(m.agentsOnboardPlan.Envelope.Plan.Items), item.Name, item.Classification)), p.styleHelp.Render(pad+"j/k inspect  E executables  m map secrets  x leave unmanaged"))
		if choices := onboardTargetChoiceHelp(*item); choices != "" {
			lines = append(lines, p.styleHelp.Render(pad+choices))
		}
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

func onboardTargetChoiceHelp(item apm.ImportItem) string {
	options := item.TargetOptions()
	choices := make([]string, 0, min(len(options), 9)+1)
	for i, target := range options {
		if i == 9 {
			break
		}
		choices = append(choices, fmt.Sprintf("%d %s", i+1, target))
	}
	if len(options) > 1 {
		choices = append(choices, "a all")
	}
	return strings.Join(choices, "  ")
}
