package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func runBatchCmd(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, sub := range batch {
			if sub != nil {
				out = append(out, sub())
			}
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestAPMCommandOutputPreservesStdoutAndStderr(t *testing.T) {
	got := apmCommandOutput("installed\n", "warning\n")
	if got != "installed\nwarning" {
		t.Fatalf("output = %q", got)
	}
}

func TestAgentsViewShowsLastAPMResult(t *testing.T) {
	m := Model{width: 100, palette: defaultPalette(), apmCommand: "apm deps list -g", apmOutput: "pkg@1.0.0", apmErr: errors.New("audit failed")}
	view := m.viewSkillsBody()
	for _, want := range []string{"~/.apm/apm.yml", "~/.apm/apm.lock.yaml", "pkg@1.0.0", "audit failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestAPMCommandFailureKeepsOutputAndError(t *testing.T) {
	m := baseModel(nil)
	m.apmRunning = true
	got := drive(m, apmCommandDoneMsg{
		command: "apm audit --ci",
		stdout:  "audit report",
		stderr:  "invalid dependency",
		err:     errors.New("exit status 2"),
	})
	if got.apmRunning || got.apmCommand != "apm audit --ci" {
		t.Fatalf("command state = running %v, command %q", got.apmRunning, got.apmCommand)
	}
	if got.apmOutput != "audit report\ninvalid dependency" || got.apmErr == nil {
		t.Fatalf("result = output %q, err %v", got.apmOutput, got.apmErr)
	}
	if !got.statusIsErr {
		t.Fatal("failed APM command reported success")
	}
}
