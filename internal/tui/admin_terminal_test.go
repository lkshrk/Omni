package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type adminTerminalTraceSinkStub struct {
	records []executor.TraceRecord
}

func (s *adminTerminalTraceSinkStub) RecordCommandTrace(_ context.Context, trace executor.TraceRecord) error {
	s.records = append(s.records, trace)
	return nil
}

func TestRecordAdminTerminalTraceSanitizesBeforeSink(t *testing.T) {
	sink := &adminTerminalTraceSinkStub{}
	state := adminTerminalState{
		reason:  "admin\x00 AUTH_TOKEN=reasonsecret\rnext\t界",
		command: "to\x08ol",
		args:    []string{"--pass\x08word", "argsecret", "\x1b[31mplain\x1b[0m"},
	}
	err := errors.New("TOKEN=errorsecret\rretry\x08\x7f\u0085\x1b]0;title\x07" + string([]byte{0xff}) + "界")
	started := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	recordAdminTerminalTrace(context.Background(), sink, state, started, started.Add(time.Second), err)

	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
	trace := sink.records[0]
	if trace.Reason != "admin AUTH_TOKEN=[redacted]\nnext\t界" {
		t.Errorf("reason = %q", trace.Reason)
	}
	if trace.Command != "tool --password '[redacted]' plain" {
		t.Errorf("command = %q", trace.Command)
	}
	if trace.Error != "TOKEN=[redacted]\nretry界" {
		t.Errorf("error = %q", trace.Error)
	}
}

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
	session, err := startAdminTerminalProcess(context.Background(), state, 80, 24, events, nil)
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

func TestStartAdminTerminalSessionDoesNotStartProcessBeforeCommandRuns(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("PTY admin terminal is unsupported on Windows")
	}
	m := baseModel(nil)
	m.ctx = context.Background()
	m.width = 80
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		name:    "deferred-test",
		command: filepath.Join(t.TempDir(), "missing-admin-command"),
		display: "missing-admin-command",
	}

	cmd := m.startAdminTerminalSession()
	if m.adminTerminal.session != nil {
		_ = m.adminTerminal.session.ptmx.Close()
		t.Fatal("admin terminal process started while constructing its command, before the running frame could render")
	}
	if cmd == nil {
		t.Fatal("admin terminal start returned no command")
	}
	if _, startedTooEarly := cmd().(adminTerminalDoneMsg); startedTooEarly {
		t.Fatal("admin terminal process was attempted while constructing its command, before the running frame could render")
	}
}

func TestSendAdminTerminalOutputPreservesRecentOutputWhenBufferFull(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	events := make(chan tea.Msg, adminTerminalEventBuffer)
	for i := range adminTerminalEventBuffer {
		events <- adminTerminalOutputMsg{id: 1, chunk: fmt.Sprintf("line %d\n", i)}
	}
	// Inject a doneMsg at the front by draining one and re-inserting.
	<-events
	events <- adminTerminalDoneMsg{id: 1, state: adminTerminalState{id: 1}}

	// Send more output — this should drain output messages but never the doneMsg.
	for range 10 {
		sendAdminTerminalOutput(events, adminTerminalOutputMsg{id: 1, chunk: "new\n"})
	}

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
	t.Parallel()
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
	t.Parallel()
	m := baseModel(nil)
	m.width = 90
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		name:    "karabiner-elements",
		command: "brew",
		args:    []string{"install", "--cask", "--no-ask", "karabiner-elements"},
		display: "brew install --cask --no-ask karabiner-elements",
		reason:  "brew cask karabiner-elements uses a pkg installer",
	}

	out := renderAdminTerminalPopup(m)
	assertLinesFitWidth(t, out, adminTerminalContentWidth(m))
	for _, want := range []string{
		"karabiner-elements",
		"brew cask",
		"The karabiner-elements cask uses a macOS package installer.",
		"command",
		"brew install --cask --no-ask karabiner-elements",
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
	t.Parallel()
	m := baseModel(nil)
	m.adminTerminal = &adminTerminalState{name: "vim", action: provider.PrivilegeActionInstall}
	if got := adminTerminalPopupFrame(m).Title; got != "Admin Approval Required" {
		t.Fatalf("pre-run title = %q, want Admin Approval Required", got)
	}
	m.adminTerminal.running = true
	if got := adminTerminalPopupFrame(m).Title; got != "Installing vim" {
		t.Fatalf("running title = %q, want Installing vim", got)
	}
	m.adminTerminal.output = "[sudo] password for alex: "
	if got := adminTerminalPopupFrame(m).Title; got != "Password Required · vim" {
		t.Fatalf("password title = %q, want Password Required · vim", got)
	}
}

func TestAdminTerminalApprovalSummary_ShowsQueuePosition(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRenderAdminTerminalPopup_EmphasizesPasswordPrompt(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.width = 90
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		display: "sudo apt-get install -y vim",
		running: true,
		output:  "omni admin terminal: sudo apt-get install -y vim\n[sudo] password for alex: ",
	}

	out := renderAdminTerminalPopup(m)
	assertLinesFitWidth(t, out, adminTerminalContentWidth(m))
	for _, want := range []string{
		"PASSWORD REQUIRED",
		"Type your sudo password and press Enter.",
		"Nothing will appear while you type.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderAdminTerminalPopup missing emphasized input guidance %q:\n%s", want, out)
		}
	}
	prompt := "[sudo] password for alex:"
	if !strings.Contains(out, m.palette.styleOutdated.Render(prompt)) {
		t.Fatalf("renderAdminTerminalPopup did not emphasize password prompt %q:\n%s", prompt, out)
	}
}

func TestRenderAdminTerminalOutput_LeftAlignsInputRequiredStatus(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.height = 24
	state := &adminTerminalState{
		running: true,
		output:  "Omni needs your sudo password: ",
	}

	out := stripANSIEscapeSequences(renderAdminTerminalOutput(m, state, 64))
	firstLine := strings.Split(out, "\n")[0]
	if !strings.HasPrefix(firstLine, "terminal  input required") {
		t.Fatalf("terminal status = %q, want left-aligned input-required label", firstLine)
	}
}

func TestAdminTerminalPasswordPromptFitsConstrainedWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "narrow", width: 34, height: 24},
		{name: "short", width: 70, height: 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := baseModel(nil)
			m.width = tt.width
			m.height = tt.height
			m.adminTerminal = &adminTerminalState{
				name:    "vim",
				display: "sudo apt-get install -y vim",
				running: true,
				output:  "[sudo] password for alex: ",
			}

			bg := strings.Repeat("\n", m.height-1)
			out := placePopup(bg, m, renderAdminTerminalPopup(m), adminTerminalPopupFrame(m))
			if got := lipgloss.Width(out); got > m.width {
				t.Fatalf("placed popup width = %d, window width = %d:\n%s", got, m.width, out)
			}
			if got := lipgloss.Height(out); got > m.height {
				t.Fatalf("placed popup height = %d, window height = %d:\n%s", got, m.height, out)
			}
			plain := stripANSIEscapeSequences(out)
			for _, want := range []string{"PASSWORD REQUIRED", "sudo", "password", "input", "Nothing will appear", "[sudo]"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("placed popup missing %q in %s window:\n%s", want, tt.name, out)
				}
			}
		})
	}
}

func TestAdminTerminalAwaitingPassword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "omni sudo prompt", output: "Omni needs your sudo password: ", want: true},
		{name: "standard sudo prompt", output: "[sudo] password for alex: ", want: true},
		{name: "generic password prompt", output: "Password: ", want: true},
		{name: "passphrase prompt", output: "Enter passphrase for key '/tmp/id_ed25519': ", want: true},
		{name: "prompt split across chunks", output: "installing\n[sudo] pass" + "word for alex: ", want: true},
		{name: "completed prompt", output: "Password:\nInstalling vim\n", want: false},
		{name: "password in log", output: "password authentication failed\n", want: false},
		{name: "password policy log", output: "Password policy:\n", want: false},
		{name: "password store log", output: "Password store:\n", want: false},
		{name: "ordinary output", output: "Downloading packages...\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminTerminalAwaitingPassword(tt.output); got != tt.want {
				t.Fatalf("adminTerminalAwaitingPassword(%q) = %t, want %t", tt.output, got, tt.want)
			}
		})
	}
}

func TestAdminTerminalProcessEnvSetsDistinctSudoPrompt(t *testing.T) {
	t.Parallel()
	original := []string{"PATH=/usr/bin", "SUDO_PROMPT=old prompt", "TERM=xterm-256color"}
	got := adminTerminalProcessEnv(original)

	want := "SUDO_PROMPT=Omni needs your sudo password: "
	count := 0
	for _, entry := range got {
		if strings.HasPrefix(entry, "SUDO_PROMPT=") {
			count++
			if entry != want {
				t.Fatalf("SUDO_PROMPT = %q, want %q", entry, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("SUDO_PROMPT entries = %d, want 1: %#v", count, got)
	}
	if original[1] != "SUDO_PROMPT=old prompt" {
		t.Fatalf("adminTerminalProcessEnv mutated input: %#v", original)
	}
}

func TestVisibleAdminTerminalOutputLines_TruncatesWithIndicator(t *testing.T) {
	t.Parallel()
	got := visibleAdminTerminalOutputLines("one\ntwo\nthree", 40, 2)
	if len(got) != 2 {
		t.Fatalf("line count = %d, want 2", len(got))
	}
	if got[0] != "..." || got[1] != "three" {
		t.Fatalf("lines = %#v, want truncation indicator and newest line", got)
	}
}
