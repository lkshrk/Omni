package testflow

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderMatrixIsDeterministicAndPreservesOutside(t *testing.T) {
	doc := []byte("before\r\n" + FlowsStart + "\nold flows\n" + FlowsEnd + "\r\nmiddle\n" + ActionsStart + "\nold actions\n" + ActionsEnd + "\nafter\r\n")
	cli, tui := true, true
	catalog := Catalog{Flows: []Flow{
		{ID: "z.flow", Criticality: CriticalityLow, Surfaces: Surfaces{CLI: &cli, TUI: &tui}, Parity: &Parity{SemanticQuery: "items"}, Requirements: []Requirement{{Level: LevelParity, Status: StatusGap, TargetStage: "Stage 6"}}},
		{ID: "a.flow", Criticality: CriticalityHigh, ActionIDs: []string{"z.action"}, Requirements: []Requirement{{Level: LevelIntegration, Status: StatusRequired, Evidence: []Evidence{{Type: LevelIntegration, Selector: Selector{Package: "example.test/pkg", Test: "TestFlow"}}}}}},
	}}
	actions := []ActionSurface{{ID: "z.action", CLI: true}, {ID: "a.action", TUI: true}}

	first, err := RenderMatrix(doc, catalog, actions)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderMatrix(first, catalog, actions)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("second render changed output")
	}
	if !bytes.HasPrefix(first, []byte("before\r\n")) || !bytes.HasSuffix(first, []byte("after\r\n")) || !bytes.Contains(first, []byte(FlowsEnd+"\r\nmiddle\n"+ActionsStart)) {
		t.Fatalf("content outside markers changed:\n%s", first)
	}
	if strings.Index(string(first), "`a.flow`") > strings.Index(string(first), "`z.flow`") || strings.Index(string(first), "`a.action`") > strings.Index(string(first), "`z.action`") {
		t.Fatal("generated rows are not sorted")
	}
	for _, want := range []string{"| `a.flow` | high | CLI | — | integration | — | integration: `example.test/pkg.TestFlow` |", "| `z.flow` | low | CLI+TUI | query | parity | parity (Stage 6) | — |"} {
		if !strings.Contains(string(first), want) {
			t.Fatalf("rendered matrix missing %q:\n%s", want, first)
		}
	}
}

func TestRenderMatrixRejectsInvalidMarkers(t *testing.T) {
	validActions := ActionsStart + "\nold\n" + ActionsEnd
	tests := map[string]string{
		"missing":   FlowsStart + "\nold\n" + validActions,
		"duplicate": FlowsStart + "\n" + FlowsEnd + "\n" + FlowsStart + "\n" + FlowsEnd + "\n" + validActions,
		"reversed":  FlowsEnd + "\n" + FlowsStart + "\n" + validActions,
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := RenderMatrix([]byte(doc), Catalog{}, nil); err == nil {
				t.Fatal("RenderMatrix() succeeded")
			}
		})
	}
}
