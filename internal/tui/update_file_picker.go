package tui

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func (m *Model) updateActiveFilePicker(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd
	if !m.showFilePicker {
		return cmds
	}
	if _, isKey := msg.(tea.KeyMsg); isKey {
		return cmds
	}
	var cmd tea.Cmd
	m.dotsFilePicker, cmd = m.dotsFilePicker.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	return cmds
}

func (m *Model) handleFilePickerKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	var cmds []tea.Cmd
	if !m.showFilePicker {
		return false, cmds
	}
	if key.Matches(msg, m.keys.Back) {
		m.showFilePicker = false
		m.filePickerForDotAdd = false
		if m.mode == viewSetup && m.setupStep == 6 {
			m.startSetupGroupSelection(&cmds)
		}
		return true, cmds
	}
	if key.Matches(msg, m.keys.Confirm) {
		if path := m.dotsFilePicker.SelectedPath(); path != "" {
			cmds = append(cmds, m.acceptFilePickerPath(path)...)
		}
		return true, cmds
	}
	var cmd tea.Cmd
	m.dotsFilePicker, cmd = m.dotsFilePicker.Update(msg)
	cmds = append(cmds, cmd)
	return true, cmds
}

func (m *Model) acceptFilePickerPath(path string) []tea.Cmd {
	var cmds []tea.Cmd
	m.showFilePicker = false
	if m.filePickerForDotAdd {
		m.filePickerForDotAdd = false
		m.acceptDotAddFilePickerPath(path, &cmds)
	} else if m.mode == viewSetup {
		if m.promptForStowInstall(stowInstallSetupDotsRepo) {
			m.stowInstallPath = path
			return cmds
		}
		m.loading = true
		startOp(m, "Saving…")
		cmds = append(cmds, m.spinner.Tick, m.doSetupDotsRepo(path))
	} else {
		settings := m.settings
		settings.DotsRepo = tildePath(path)
		settings.DotsDisabled = config.BoolPtr(false)
		if m.promptForStowInstall(stowInstallSaveSettingsSync) {
			m.stowInstallSettings = settings
			return cmds
		}
		m.settings = settings
		m.dotsLoaded = false
		m.beginDotsOperation("Syncing dots…")
		cmds = append(cmds, m.spinner.Tick, m.doSaveSettingsAndDotsSync(settings))
	}
	return cmds
}

func (m *Model) acceptDotAddFilePickerPath(path string, _ *[]tea.Cmd) {
	rawPath := tildePath(path)
	m.openGroupPickerForDotAdd(path, rawPath)
}

func (m *Model) openGroupPickerForDotAdd(absPath, rawPath string) {
	m.mode = viewGroupPicker
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerPurposeDotAdd = true
	m.pickerDotAddPath = absPath
	m.pickerDotAddRawPath = rawPath
	m.pickerCreatedGroups = nil
	m.pickerActionTool = database.ToolCache{}
	m.pickerActionToolSet = false
}

func tildePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, rerr := filepath.Rel(home, path); rerr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "~"
			}
			return "~/" + rel
		}
	}
	return path
}
