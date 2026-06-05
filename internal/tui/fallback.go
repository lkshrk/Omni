package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

func toolFallbackEligible(t *database.ToolCache) bool {
	if t == nil || t.Provider != provider.EcosystemSystem {
		return false
	}
	return !t.Installed || t.InstalledWith == "gh"
}

func (m *Model) openFallbackEditor(t *database.ToolCache) tea.Cmd {
	if !toolFallbackEligible(t) {
		return nil
	}
	m.fallbackTarget = *t
	m.fallbackTargetSet = true
	m.mode = viewFallbackEditor
	repo := ""
	if fallback, ok := m.toolFallbacks[t.Name]; ok && fallback.Source.Type == config.FallbackSourceGitHub {
		repo = fallback.Source.Owner + "/" + fallback.Source.Repo
	}
	m.settingsInput.SetValue(repo)
	m.settingsInput.Placeholder = "owner/repo"
	m.settingsInput.CursorEnd()
	m.settingsInput.Focus()
	return textinput.Blink
}

func (m *Model) closeFallbackEditor() {
	m.fallbackTarget = database.ToolCache{}
	m.fallbackTargetSet = false
	m.settingsInput.SetValue("")
	m.settingsInput.Blur()
	m.mode = viewList
}

func (m *Model) handleFallbackEditorKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if key.Matches(msg, m.keys.Back) {
		m.closeFallbackEditor()
		return nil
	}
	if key.Matches(msg, m.keys.Confirm) {
		repo := strings.TrimSpace(m.settingsInput.Value())
		if repo == "" {
			return nil
		}
		name := m.fallbackTarget.Name
		m.closeFallbackEditor()
		m.loading = true
		startOp(m, "Saving fallback for "+name+"…")
		return []tea.Cmd{m.spinner.Tick, m.doSaveFallback(name, repo)}
	}
	var cmd tea.Cmd
	m.settingsInput, cmd = m.settingsInput.Update(msg)
	return []tea.Cmd{cmd}
}

func (m *Model) doSaveFallback(name, repo string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	return func() tea.Msg {
		err := a.SaveToolFallbackFromGitHub(ctx, name, repo)
		var fallbacks map[string]config.FallbackSpec
		if err == nil {
			scope, scopeErr := a.ToolScopeDisplayState(ctx)
			if scopeErr != nil {
				err = scopeErr
			} else {
				fallbacks = scope.ToolFallbacks
			}
		}
		return fallbackSavedMsg{err: err, name: name, repo: repo, toolFallbacks: fallbacks}
	}
}

func (m *Model) handleFallbackSavedMsg(msg fallbackSavedMsg) []tea.Cmd {
	m.finishCancellableAction()
	m.loading = false
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	m.toolFallbacks = msg.toolFallbacks
	return []tea.Cmd{setStatus(m, fmt.Sprintf("✓ fallback saved for %s from gh %s", msg.name, msg.repo), false)}
}

func renderFallbackEditorPopup(m Model) string {
	p := m.palette
	contentW := fallbackEditorContentWidth(m)
	input := m.settingsInput
	input.Prompt = ""
	inputW := max(contentW-lipgloss.Width("gh ")-4, 1)
	input.SetWidth(inputW)

	var sb strings.Builder
	sb.WriteString(renderCreateNameField(p, "gh", renderEmptyAwareTextInputView(p, input, "owner/repo", inputW), contentW))
	sb.WriteString("\n\n")
	sb.WriteString(renderPickerHintItems(m, contentW, confirmActionItems(m.keys.Confirm, "save", m.keys.Back)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func fallbackEditorPopupFrame(m Model) popupFrame {
	paddingX := 2
	return popupFrame{
		Title:          popupTitleForFallbackTool(m),
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(fallbackEditorContentWidth(m), paddingX),
		NoTitleDivider: true,
	}
}

func fallbackEditorContentWidth(m Model) int {
	return popupContentWidth(m, 46, 30, 56)
}

func popupTitleForFallbackTool(m Model) string {
	if m.fallbackTargetSet && m.fallbackTarget.Name != "" {
		return "Set Fallback: " + m.fallbackTarget.Name
	}
	return "Set Fallback"
}

func fallbackConcreteLabel(fallback config.FallbackSpec) string {
	if fallback.Source.Type != config.FallbackSourceGitHub {
		return "fallback"
	}
	switch fallback.Status {
	case config.FallbackStatusVerified:
		return "gh"
	case config.FallbackStatusUnresolved, config.FallbackStatusFailed:
		return "gh!"
	default:
		return "gh?"
	}
}

func fallbackConcreteForTool(t *database.ToolCache, fallbacks map[string]config.FallbackSpec) string {
	if !toolFallbackEligible(t) || fallbacks == nil {
		return ""
	}
	fallback, ok := fallbacks[t.Name]
	if !ok {
		return ""
	}
	return fallbackConcreteLabel(fallback)
}
