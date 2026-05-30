package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/provider"
)

func TestAdminTerminalProcess_FakeBrewCaskGetsTTYAndInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY admin terminal is unsupported on Windows")
	}

	home := t.TempDir()
	brewDir := filepath.Join(home, ".volta", "bin")
	if err := os.MkdirAll(brewDir, 0o755); err != nil {
		t.Fatalf("create fake node-manager bin dir: %v", err)
	}
	brewPath := filepath.Join(brewDir, "brew")
	script := `#!/bin/sh
set -eu
if [ "$1" != "uninstall" ] || [ "$2" != "--cask" ] || [ "$3" != "parsec" ]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
if [ ! -t 0 ]; then echo "stdin is not a tty" >&2; exit 10; fi
if [ ! -t 1 ]; then echo "stdout is not a tty" >&2; exit 11; fi
if [ ! -t 2 ]; then echo "stderr is not a tty" >&2; exit 12; fi
printf "Password:"
IFS= read -r answer
if [ "$answer" != "secret" ]; then
  echo "bad password input: $answer" >&2
  exit 13
fi
echo "PTY_OK"
`
	if err := os.WriteFile(brewPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")

	events := make(chan tea.Msg, adminTerminalEventBuffer)
	state := adminTerminalState{
		id:      1,
		command: "brew",
		args:    []string{"uninstall", "--cask", "parsec"},
		display: "brew uninstall --cask parsec",
	}
	session, err := startAdminTerminalProcess(context.Background(), state, 80, 24, events)
	if err != nil {
		t.Fatalf("start admin terminal process: %v", err)
	}
	defer session.ptmx.Close()

	if _, err := session.ptmx.Write([]byte("secret\r")); err != nil {
		t.Fatalf("write PTY input: %v", err)
	}

	output, err := collectAdminTerminalTestOutput(events, 2*time.Second)
	if err != nil {
		t.Fatalf("admin PTY command failed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "PTY_OK") {
		t.Fatalf("output = %q, want PTY_OK marker", output)
	}
	for _, forbidden := range []string{"stdin is not a tty", "stdout is not a tty", "stderr is not a tty", "bad password input"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("found %q in output\n%s", forbidden, output)
		}
	}
}

func collectAdminTerminalTestOutput(events <-chan tea.Msg, timeout time.Duration) (string, error) {
	deadline := time.After(timeout)
	var output strings.Builder
	for {
		select {
		case msg := <-events:
			switch msg := msg.(type) {
			case adminTerminalOutputMsg:
				output.WriteString(msg.chunk)
			case adminTerminalDoneMsg:
				return output.String(), msg.err
			}
		case <-deadline:
			return output.String(), context.DeadlineExceeded
		}
	}
}

func TestSendAdminTerminalOutputPreservesRecentOutputWhenBufferFull(t *testing.T) {
	events := make(chan tea.Msg, adminTerminalEventBuffer)
	for i := 0; i < adminTerminalEventBuffer+8; i++ {
		sendAdminTerminalOutput(events, adminTerminalOutputMsg{id: 7, chunk: "old output line\n"})
	}
	sendAdminTerminalOutput(events, adminTerminalOutputMsg{id: 7, chunk: "FINAL_MARKER\n"})
	sendAdminTerminalOutput(events, adminTerminalOutputMsg{id: 7, chunk: "ERROR: final failure detail\n"})

	var output strings.Builder
	for len(events) > 0 {
		msg := <-events
		if msg, ok := msg.(adminTerminalOutputMsg); ok {
			output.WriteString(msg.chunk)
		}
	}
	got := output.String()
	if !strings.Contains(got, "FINAL_MARKER") {
		t.Fatalf("buffered output lost final marker:\n%s", got)
	}
	if !strings.Contains(got, "ERROR: final failure detail") {
		t.Fatalf("buffered output lost final error line:\n%s", got)
	}
}

func TestSendAdminTerminalOutputPreservesDoneMsg(t *testing.T) {
	events := make(chan tea.Msg, adminTerminalEventBuffer)
	// Fill channel completely with output messages.
	for i := range adminTerminalEventBuffer {
		events <- adminTerminalOutputMsg{id: 1, chunk: fmt.Sprintf("line %d\n", i)}
	}
	// Inject a doneMsg at the front by draining one and re-inserting.
	<-events
	events <- adminTerminalDoneMsg{id: 1, state: adminTerminalState{id: 1}}

	// Now the channel is full with a mix of output + one doneMsg at the end.
	// Send more output — this should drain output messages but never the doneMsg.
	for range 10 {
		sendAdminTerminalOutput(events, adminTerminalOutputMsg{id: 1, chunk: "new\n"})
	}

	// Drain and verify the doneMsg survived.
	foundDone := false
	for len(events) > 0 {
		msg := <-events
		if _, ok := msg.(adminTerminalDoneMsg); ok {
			foundDone = true
		}
	}
	if !foundDone {
		t.Fatal("sendAdminTerminalOutput dropped adminTerminalDoneMsg; it must never be discarded")
	}
}

func TestAdminTerminalKeyBytes(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{name: "text", msg: tea.KeyPressMsg{Text: "s", Code: 's'}, want: "s"},
		{name: "enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter}, want: "\r"},
		{name: "backspace", msg: tea.KeyPressMsg{Code: tea.KeyBackspace}, want: string([]byte{0x7f})},
		{name: "ctrl-c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, want: string([]byte{0x03})},
		{name: "up", msg: tea.KeyPressMsg{Code: tea.KeyUp}, want: "\x1b[A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(adminTerminalKeyBytes(tt.msg)); got != tt.want {
				t.Fatalf("adminTerminalKeyBytes() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderAdminTerminalPopup_PreRunStyling(t *testing.T) {
	m := baseModel(nil)
	m.width = 90
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		name:    "karabiner-elements",
		command: "brew",
		args:    []string{"install", "--cask", "karabiner-elements"},
		display: "brew install --cask karabiner-elements",
		reason:  "brew cask karabiner-elements uses a pkg installer",
	}

	out := renderAdminTerminalPopup(m)
	assertLinesFitWidth(t, out, adminTerminalContentWidth(m))
	for _, want := range []string{
		"karabiner-elements",
		"brew cask",
		"The karabiner-elements cask uses a macOS package installer.",
		"command",
		"brew install --cask karabiner-elements",
		"A terminal will open here after you continue.",
		"continue",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderAdminTerminalPopup missing %q:\n%s", want, out)
		}
	}
	for _, dontWant := range []string{
		"$ brew",
		"ready",
		"reason",
		"brew cask karabiner-elements uses a pkg installer",
		"Ready for password prompts inside Omni.",
	} {
		if strings.Contains(out, dontWant) {
			t.Fatalf("renderAdminTerminalPopup should not contain %q:\n%s", dontWant, out)
		}
	}
}

func TestAdminTerminalPopupFrame_TitleReflectsState(t *testing.T) {
	m := baseModel(nil)
	m.adminTerminal = &adminTerminalState{name: "vim", action: provider.PrivilegeActionInstall}
	if got := adminTerminalPopupFrame(m).Title; got != "Admin Approval Required" {
		t.Fatalf("pre-run title = %q, want Admin Approval Required", got)
	}
	m.adminTerminal.running = true
	if got := adminTerminalPopupFrame(m).Title; got != "Installing vim" {
		t.Fatalf("running title = %q, want Installing vim", got)
	}
}

func TestAdminTerminalApprovalSummary_ShowsQueuePosition(t *testing.T) {
	m := baseModel(nil)
	state := &adminTerminalState{
		name:       "vim",
		command:    "apt-get",
		args:       []string{"install", "-y", "vim"},
		queueIndex: 2,
		queueTotal: 3,
	}
	out := renderAdminTerminalApprovalSummary(m, state, 48)
	if !strings.Contains(out, "vim") || !strings.Contains(out, "2/3") {
		t.Fatalf("approval summary = %q, want tool name and queue position", out)
	}
}

func TestRenderAdminTerminalPopup_RunningViewportStyling(t *testing.T) {
	m := baseModel(nil)
	m.width = 90
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		display: "sudo apt-get install -y vim",
		running: true,
		output:  "omni admin terminal: sudo apt-get install -y vim\nPassword:\nInstalling vim\n",
	}

	out := renderAdminTerminalPopup(m)
	assertLinesFitWidth(t, out, adminTerminalContentWidth(m))
	for _, want := range []string{
		"attached",
		"terminal",
		"live",
		"┌",
		"└",
		"Password:",
		"Installing vim",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderAdminTerminalPopup missing %q:\n%s", want, out)
		}
	}
}

func TestVisibleAdminTerminalOutputLines_TruncatesWithIndicator(t *testing.T) {
	got := visibleAdminTerminalOutputLines("one\ntwo\nthree", 40, 2)
	if len(got) != 2 {
		t.Fatalf("line count = %d, want 2", len(got))
	}
	if got[0] != "..." || got[1] != "three" {
		t.Fatalf("lines = %#v, want truncation indicator and newest line", got)
	}
}
