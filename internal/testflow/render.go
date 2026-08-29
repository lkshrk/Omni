package testflow

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

const (
	FlowsStart   = "<!-- BEGIN GENERATED FLOW CATALOG -->"
	FlowsEnd     = "<!-- END GENERATED FLOW CATALOG -->"
	ActionsStart = "<!-- BEGIN GENERATED ACTION CATALOG -->"
	ActionsEnd   = "<!-- END GENERATED ACTION CATALOG -->"
)

// RenderMatrix replaces only the generated tables in the test matrix.
func RenderMatrix(doc []byte, catalog Catalog, actions []ActionSurface) ([]byte, error) {
	actionByID := make(map[string]ActionSurface, len(actions))
	for _, action := range actions {
		actionByID[action.ID] = action
	}

	flowByAction := make(map[string]string, len(actions))
	flows := append([]Flow(nil), catalog.Flows...)
	sort.Slice(flows, func(i, j int) bool { return flows[i].ID < flows[j].ID })
	for _, flow := range flows {
		for _, id := range flow.ActionIDs {
			flowByAction[id] = flow.ID
		}
	}

	out, err := replaceGenerated(doc, FlowsStart, FlowsEnd, renderFlows(flows, actionByID))
	if err != nil {
		return nil, err
	}
	return replaceGenerated(out, ActionsStart, ActionsEnd, renderActions(actions, flowByAction))
}

func replaceGenerated(doc []byte, start, end, body string) ([]byte, error) {
	startBytes, endBytes := []byte(start), []byte(end)
	if bytes.Count(doc, startBytes) != 1 || bytes.Count(doc, endBytes) != 1 {
		return nil, fmt.Errorf("expected exactly one %q and %q marker", start, end)
	}
	startAt, endAt := bytes.Index(doc, startBytes), bytes.Index(doc, endBytes)
	if startAt > endAt {
		return nil, fmt.Errorf("marker %q appears after %q", start, end)
	}
	replacement := []byte(start + "\n" + body + end)
	out := make([]byte, 0, len(doc)-endAt+startAt+len(replacement))
	out = append(out, doc[:startAt]...)
	out = append(out, replacement...)
	out = append(out, doc[endAt+len(endBytes):]...)
	return out, nil
}

func renderFlows(flows []Flow, actions map[string]ActionSurface) string {
	var b strings.Builder
	b.WriteString("| Flow ID | Criticality | Surfaces | Parity | Requirements | Gaps | Evidence |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, flow := range flows {
		cli, tui := flowSurfaces(flow, actions)
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s |\n",
			markdown(flow.ID), flow.Criticality, surfaces(cli, tui), parityMode(flow.Parity),
			requirementLevels(flow.Requirements), gaps(flow.Requirements), evidence(flow.Requirements))
	}
	return b.String()
}

func renderActions(actions []ActionSurface, flowByAction map[string]string) string {
	ordered := append([]ActionSurface(nil), actions...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var b strings.Builder
	b.WriteString("| Action ID | CLI | TUI | Flow |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, action := range ordered {
		fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` |\n", markdown(action.ID), yesNo(action.CLI), yesNo(action.TUI), markdown(flowByAction[action.ID]))
	}
	return b.String()
}

func flowSurfaces(flow Flow, actions map[string]ActionSurface) (bool, bool) {
	if len(flow.ActionIDs) == 0 {
		return flow.Surfaces.CLI != nil && *flow.Surfaces.CLI, flow.Surfaces.TUI != nil && *flow.Surfaces.TUI
	}
	var cli, tui bool
	for _, id := range flow.ActionIDs {
		action := actions[id]
		cli, tui = cli || action.CLI, tui || action.TUI
	}
	return cli, tui
}

func surfaces(cli, tui bool) string {
	var out []string
	if cli {
		out = append(out, "CLI")
	}
	if tui {
		out = append(out, "TUI")
	}
	if len(out) == 0 {
		return "—"
	}
	return strings.Join(out, "+")
}

func parityMode(parity *Parity) string {
	if parity == nil {
		return "—"
	}
	if parity.SemanticState != "" {
		return "state"
	}
	return "query"
}

func requirementLevels(requirements []Requirement) string {
	levels := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		levels = append(levels, string(requirement.Level))
	}
	sort.Strings(levels)
	return strings.Join(levels, ", ")
}

func gaps(requirements []Requirement) string {
	var out []string
	for _, requirement := range requirements {
		if requirement.Status == StatusGap {
			out = append(out, fmt.Sprintf("%s (%s)", requirement.Level, markdown(requirement.TargetStage)))
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "—"
	}
	return strings.Join(out, "; ")
}

func evidence(requirements []Requirement) string {
	var out []string
	for _, requirement := range requirements {
		for _, item := range requirement.Evidence {
			selector := item.Selector.Package + "." + item.Selector.Test
			out = append(out, fmt.Sprintf("%s: `%s`", requirement.Level, markdown(selector)))
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "—"
	}
	return strings.Join(out, "; ")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "—"
}

func markdown(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
