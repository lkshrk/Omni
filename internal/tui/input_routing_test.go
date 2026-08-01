package tui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type focusedInputCase struct {
	name  string
	setup func(*testing.T) Model
	value func(Model) string
}

func TestFocusedToolSearchAcceptsQuitRune(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSearch
	m.filter.Focus()

	got := drive(m, pressRune('j'), pressRune('q'))

	if got.filter.Value() != "jq" {
		t.Fatalf("search query = %q, want jq", got.filter.Value())
	}
	if got.confirmQuit {
		t.Fatal("typing q in focused search must not arm quit")
	}
}

func TestDotsSearchConfirmReturnsKeysToRows(t *testing.T) {
	t.Parallel()
	m := drive(dotsModel(), pressRune('/'), pressRune('z'), pressEnter())

	if !m.dotsSearchActive || m.filter.Focused() || m.filter.Value() != "z" {
		t.Fatalf("confirmed search = active:%v focused:%v query:%q", m.dotsSearchActive, m.filter.Focused(), m.filter.Value())
	}

	got := drive(m, pressRune('s'))
	if !got.dotsLoading || got.progressText != "Syncing zshrc…" {
		t.Fatalf("row sync after search = loading:%v progress:%q", got.dotsLoading, got.progressText)
	}
}

func TestStartupErrorDoesNotMaskRepairSettingsTab(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewStatus
	m.loading = true
	m = drive(m, toolsLoadedMsg{
		err: errors.New("loading startup state: dots_repo path is not a directory"),
	})
	if out := stripANSIEscapeSequences(m.viewString()); !strings.Contains(out, "Press q to quit") {
		t.Fatalf("dashboard should retain startup diagnosis:\n%s", out)
	}
	got := drive(m, pressTab(), pressTab(), pressTab(), pressTab(), pressTab())
	if got.mode != viewSettings {
		t.Fatalf("mode = %v, want settings after tabbing", got.mode)
	}
	if got.err == nil {
		t.Fatal("startup error should remain available on the dashboard")
	}
	if out := stripANSIEscapeSequences(got.viewString()); strings.Contains(out, "Press q to quit") {
		t.Fatalf("startup error still masked repair settings:\n%s", out)
	}

	reloaded := drive(got, toolsLoadedMsg{})
	if reloaded.err != nil {
		t.Fatalf("successful startup reload retained error: %v", reloaded.err)
	}
}

func TestFocusedTextInputsOwnPrintableGlobalBindings(t *testing.T) {
	for _, tc := range focusedInputCases() {
		for _, r := range []rune{'q', '?', ':', '/', 'U', 'S', 'R', 'C', 'A', 'L'} {
			t.Run(tc.name+"/"+string(r), func(t *testing.T) {
				m := tc.setup(t)
				beforeMode, beforeValue := m.mode, tc.value(m)

				got := drive(m, pressRune(r))

				if want := beforeValue + string(r); tc.value(got) != want {
					t.Fatalf("input value = %q, want %q", tc.value(got), want)
				}
				if got.mode != beforeMode {
					t.Fatalf("mode = %v, want %v", got.mode, beforeMode)
				}
				if got.confirmQuit {
					t.Fatal("printable input armed quit")
				}
				if got.help.ShowAll {
					t.Fatal("printable input opened help")
				}
			})
		}
	}
}

func TestFocusedTextInputsOwnMainTabNavigation(t *testing.T) {
	for _, tc := range focusedInputCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(t)
			beforeMode := m.mode

			got := drive(m, tea.KeyPressMsg{Code: tea.KeyTab})

			if got.mode != beforeMode {
				t.Fatalf("tab changed mode from %v to %v while input was focused", beforeMode, got.mode)
			}
		})
	}
}

func focusedInputCases() []focusedInputCase {
	cases := []focusedInputCase{
		{
			name: "setup path picker",
			setup: func(t *testing.T) Model {
				m := baseModel(nil)
				m.mode = viewSetup
				m.openFilePicker("Dots repo path", t.TempDir(), false)
				return m
			},
			value: func(m Model) string { return m.dotsFilePicker.input.Value() },
		},
		{
			name: "tools search",
			setup: func(*testing.T) Model {
				m := baseModel(nil)
				m.mode = viewSearch
				m.filter.Focus()
				return m
			},
			value: func(m Model) string { return m.filter.Value() },
		},
		{
			name: "dots search",
			setup: func(*testing.T) Model {
				m := baseModel(nil)
				m.mode = viewDots
				m.dotsSearchActive = true
				m.filter.Focus()
				return m
			},
			value: func(m Model) string { return m.filter.Value() },
		},
		{
			name: "agents search",
			setup: func(*testing.T) Model {
				m := baseModel(nil)
				m.mode = viewSkills
				m.skillsSearchActive = true
				m.filter.Focus()
				return m
			},
			value: func(m Model) string { return m.filter.Value() },
		},
		{
			name: "command palette",
			setup: func(*testing.T) Model {
				m := baseModel(nil)
				m.mode = viewCommand
				m.commandInput.Focus()
				return m
			},
			value: func(m Model) string { return m.commandInput.Value() },
		},
		settingsInputCase("fallback editor", viewFallbackEditor, func(*Model) {}),
		settingsInputCase("group create", viewGroups, func(m *Model) { m.groupCreating = true }),
		settingsInputCase("host rename", viewGroups, func(m *Model) { m.hostRenameMode = true }),
		settingsInputCase("group rename", viewGroups, func(m *Model) { m.groupRenameMode = true }),
		settingsInputCase("host picker new group", viewGroups, func(m *Model) {
			m.hostEditMode = 1
			m.pickerCreatingGroup = true
		}),
		settingsInputCase("group picker new group", viewGroupPicker, func(m *Model) { m.pickerCreatingGroup = true }),
		settingsInputCase("membership picker new group", viewGroupMembership, func(m *Model) { m.pickerCreatingGroup = true }),
		settingsInputCase("group tools search", viewGroupTools, func(m *Model) { m.groupToolsEditor.searchActive = true }),
		settingsInputCase("group dots search", viewGroupDots, func(m *Model) { m.groupDotsEditor.searchActive = true }),
	}
	for field, name := range []string{"name", "marketplace", "source", "agents"} {
		field := field
		cases = append(cases, focusedInputCase{
			name: "plugin " + name,
			setup: func(*testing.T) Model {
				m := baseModel(nil)
				m.mode = viewSkills
				m.pluginFormOpen = true
				m.pluginFormName = textinput.New()
				m.pluginFormMarketplace = textinput.New()
				m.pluginFormSource = textinput.New()
				m.pluginFormAgents = textinput.New()
				m.pluginFormField = field
				m.focusPluginFormField()
				return m
			},
			value: func(m Model) string {
				switch field {
				case 0:
					return m.pluginFormName.Value()
				case 1:
					return m.pluginFormMarketplace.Value()
				case 2:
					return m.pluginFormSource.Value()
				default:
					return m.pluginFormAgents.Value()
				}
			},
		})
	}
	for _, field := range []struct {
		name      string
		index     int
		transport int
		value     func(Model) string
	}{
		{name: "name", index: 0, value: func(m Model) string { return m.mcpFormName.Value() }},
		{name: "command", index: 2, value: func(m Model) string { return m.mcpFormCommand.Value() }},
		{name: "url", index: 2, transport: 1, value: func(m Model) string { return m.mcpFormURL.Value() }},
		{name: "env", index: 3, value: func(m Model) string { return m.mcpFormEnv.Value() }},
		{name: "env literal", index: 4, value: func(m Model) string { return m.mcpFormEnvLit.Value() }},
	} {
		field := field
		cases = append(cases, focusedInputCase{
			name: "mcp " + field.name,
			setup: func(*testing.T) Model {
				m := baseModel(nil)
				m.mode = viewSkills
				m.mcpFormOpen = true
				m.mcpFormField = field.index
				m.mcpFormTransport = field.transport
				m.focusMcpFormField()
				return m
			},
			value: field.value,
		})
	}
	return cases
}

func settingsInputCase(name string, mode viewMode, configure func(*Model)) focusedInputCase {
	return focusedInputCase{
		name: name,
		setup: func(*testing.T) Model {
			m := baseModel(nil)
			m.mode = mode
			configure(&m)
			m.settingsInput.Focus()
			return m
		},
		value: func(m Model) string { return m.settingsInput.Value() },
	}
}
