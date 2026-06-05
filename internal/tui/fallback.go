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

type fallbackEditorFieldID string

const (
	fallbackFieldRepo           fallbackEditorFieldID = "repo"
	fallbackFieldBinary         fallbackEditorFieldID = "binary"
	fallbackFieldBinDir         fallbackEditorFieldID = "bin_dir"
	fallbackFieldAssetPattern   fallbackEditorFieldID = "asset_pattern"
	fallbackFieldInstallCommand fallbackEditorFieldID = "install"
	fallbackFieldCheckCommand   fallbackEditorFieldID = "check"
	fallbackFieldUninstall      fallbackEditorFieldID = "uninstall"
	fallbackFieldUpgrade        fallbackEditorFieldID = "upgrade"
	fallbackFieldVersion        fallbackEditorFieldID = "version"
	fallbackFieldReleaseChannel fallbackEditorFieldID = "release_channel"
)

type fallbackEditorField struct {
	id          fallbackEditorFieldID
	label       string
	placeholder string
}

type fallbackEditorState struct {
	fields map[fallbackEditorFieldID]string
	cursor int
}

var fallbackEditorFields = []fallbackEditorField{
	{id: fallbackFieldRepo, label: "repo", placeholder: "owner/repo"},
	{id: fallbackFieldBinary, label: "binary", placeholder: "binary name"},
	{id: fallbackFieldBinDir, label: "bin dir", placeholder: "{{fallback_bin_dir}}"},
	{id: fallbackFieldAssetPattern, label: "asset", placeholder: "{repo}-{version}-{os}-{arch}.tar.gz"},
	{id: fallbackFieldInstallCommand, label: "install", placeholder: "install command"},
	{id: fallbackFieldCheckCommand, label: "check", placeholder: "check command"},
	{id: fallbackFieldUninstall, label: "uninstall", placeholder: "optional uninstall command"},
	{id: fallbackFieldUpgrade, label: "upgrade", placeholder: "optional upgrade command"},
	{id: fallbackFieldVersion, label: "version", placeholder: "optional version command"},
	{id: fallbackFieldReleaseChannel, label: "channel", placeholder: "stable"},
}

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
	m.fallbackEditor = fallbackEditorStateForTool(t, m.toolFallbacks)
	m.focusFallbackEditorField()
	return textinput.Blink
}

func (m *Model) closeFallbackEditor() {
	m.fallbackTarget = database.ToolCache{}
	m.fallbackTargetSet = false
	m.fallbackEditor = fallbackEditorState{}
	m.settingsInput.SetValue("")
	m.settingsInput.Blur()
	m.mode = viewList
}

func (m *Model) handleFallbackEditorKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if key.Matches(msg, m.keys.Back) {
		m.closeFallbackEditor()
		return nil
	}
	if msg.Code == tea.KeyTab && (msg.Mod.Contains(tea.ModShift) || msg.String() == "shift+tab") {
		m.moveFallbackEditorCursor(-1)
		return nil
	}
	if msg.Code == tea.KeyTab {
		m.moveFallbackEditorCursor(1)
		return nil
	}
	if key.Matches(msg, m.keys.Up) {
		m.moveFallbackEditorCursor(-1)
		return nil
	}
	if key.Matches(msg, m.keys.Down) {
		m.moveFallbackEditorCursor(1)
		return nil
	}
	if key.Matches(msg, m.keys.Confirm) {
		m.saveFallbackEditorInput()
		if strings.TrimSpace(m.fallbackEditor.fields[fallbackFieldRepo]) == "" {
			return nil
		}
		name := m.fallbackTarget.Name
		cmd := m.doSaveFallbackEditor(name)
		m.closeFallbackEditor()
		m.loading = true
		startOp(m, "Saving fallback for "+name+"…")
		return []tea.Cmd{m.spinner.Tick, cmd}
	}
	var cmd tea.Cmd
	m.settingsInput, cmd = m.settingsInput.Update(msg)
	m.saveFallbackEditorInput()
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

func (m *Model) doSaveFallbackEditor(name string) tea.Cmd {
	a, ctx := m.app, m.beginCancellableAction()
	fallback := m.fallbackSpecFromEditor()
	repo := strings.TrimSpace(m.fallbackEditor.fields[fallbackFieldRepo])
	return func() tea.Msg {
		err := a.SaveToolFallbackFromGitHubSpec(ctx, name, repo, fallback)
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
	inputW := max(contentW-14, 1)

	var sb strings.Builder
	status := fallbackConcreteLabel(m.fallbackSpecFromEditor())
	sb.WriteString(renderCreateNameField(p, "status", status, contentW))
	for i, field := range fallbackEditorFields {
		sb.WriteString("\n")
		value := m.fallbackEditor.fields[field.id]
		if i == m.fallbackEditor.cursor {
			input := m.settingsInput
			input.Prompt = ""
			input.SetWidth(inputW)
			if input.Value() == "" && value != "" {
				input.SetValue(value)
				input.CursorEnd()
			}
			value = renderEmptyAwareTextInputView(p, input, field.placeholder, inputW)
		}
		sb.WriteString(renderCreateNameField(p, field.label, value, contentW))
	}
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
	return popupContentWidth(m, 72, 44, 86)
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

func fallbackEditorStateForTool(t *database.ToolCache, fallbacks map[string]config.FallbackSpec) fallbackEditorState {
	state := fallbackEditorState{fields: map[fallbackEditorFieldID]string{}}
	for _, field := range fallbackEditorFields {
		state.fields[field.id] = ""
	}
	name := ""
	if t != nil {
		name = t.Name
	}
	state.fields[fallbackFieldBinary] = name
	state.fields[fallbackFieldCheckCommand] = "command -v " + name
	state.fields[fallbackFieldReleaseChannel] = "stable"
	if fallbacks == nil || t == nil {
		return state
	}
	fallback, ok := fallbacks[t.Name]
	if !ok {
		return state
	}
	if fallback.Source.Type == config.FallbackSourceGitHub {
		state.fields[fallbackFieldRepo] = fallback.Source.Owner + "/" + fallback.Source.Repo
	}
	state.fields[fallbackFieldBinary] = firstNonEmpty(fallback.Binary, name)
	state.fields[fallbackFieldBinDir] = fallback.BinDir
	state.fields[fallbackFieldAssetPattern] = fallback.Recipe.AssetPattern
	state.fields[fallbackFieldInstallCommand] = fallback.Commands.Install
	state.fields[fallbackFieldCheckCommand] = fallback.Commands.Check
	state.fields[fallbackFieldUninstall] = fallback.Commands.Uninstall
	state.fields[fallbackFieldUpgrade] = fallback.Commands.Upgrade
	state.fields[fallbackFieldVersion] = fallback.Commands.Version
	state.fields[fallbackFieldReleaseChannel] = fallback.ReleaseChannel
	return state
}

func (m *Model) focusFallbackEditorField() {
	if m.fallbackEditor.fields == nil {
		m.fallbackEditor = fallbackEditorStateForTool(&m.fallbackTarget, m.toolFallbacks)
	}
	field := fallbackEditorFields[m.fallbackEditor.cursor]
	m.settingsInput.SetValue(m.fallbackEditor.fields[field.id])
	m.settingsInput.Placeholder = field.placeholder
	m.settingsInput.CursorEnd()
	m.settingsInput.Focus()
}

func (m *Model) moveFallbackEditorCursor(delta int) {
	m.saveFallbackEditorInput()
	count := len(fallbackEditorFields)
	m.fallbackEditor.cursor = (m.fallbackEditor.cursor + delta + count) % count
	m.focusFallbackEditorField()
}

func (m *Model) saveFallbackEditorInput() {
	if m.fallbackEditor.fields == nil || len(fallbackEditorFields) == 0 {
		return
	}
	field := fallbackEditorFields[m.fallbackEditor.cursor]
	m.fallbackEditor.fields[field.id] = strings.TrimSpace(m.settingsInput.Value())
}

func (m Model) fallbackSpecFromEditor() config.FallbackSpec {
	fields := m.fallbackEditor.fields
	status := config.FallbackStatusUnresolved
	if strings.TrimSpace(fields[fallbackFieldInstallCommand]) != "" && strings.TrimSpace(fields[fallbackFieldCheckCommand]) != "" {
		status = config.FallbackStatusUnverified
	}
	recipeType := ""
	if strings.TrimSpace(fields[fallbackFieldAssetPattern]) != "" {
		recipeType = config.FallbackRecipeGitHubReleaseAsset
	}
	return config.FallbackSpec{
		Source: config.FallbackSource{
			Type: config.FallbackSourceGitHub,
		},
		Status:         status,
		Binary:         strings.TrimSpace(fields[fallbackFieldBinary]),
		BinDir:         strings.TrimSpace(fields[fallbackFieldBinDir]),
		ReleaseChannel: strings.TrimSpace(fields[fallbackFieldReleaseChannel]),
		Recipe: config.FallbackRecipe{
			Type:         recipeType,
			AssetPattern: strings.TrimSpace(fields[fallbackFieldAssetPattern]),
		},
		Commands: config.FallbackCommands{
			Install:   strings.TrimSpace(fields[fallbackFieldInstallCommand]),
			Check:     strings.TrimSpace(fields[fallbackFieldCheckCommand]),
			Uninstall: strings.TrimSpace(fields[fallbackFieldUninstall]),
			Upgrade:   strings.TrimSpace(fields[fallbackFieldUpgrade]),
			Version:   strings.TrimSpace(fields[fallbackFieldVersion]),
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
