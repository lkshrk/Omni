package tui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// doCreateConfig creates an empty settings.json and reloads.
func (m *Model) doCreateConfig() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.CreateEmptyConfig(); err != nil {
			return toolsLoadedMsg{err: err}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return toolsLoadedMsg{err: err}
		}
		settings, _ := a.LoadSettings() // non-fatal: setup proceeds with zero-value settings if this fails
		taps, _ := a.LoadTaps()         // non-fatal: setup proceeds with no taps if this fails
		// Build provider picker rows for setup step 1. These require probing
		// available managers; must be done here because loadTools returns early
		// with noConfig=true when HasConfig() is false (step 0 doesn't need
		// the provider list, step 1 does).
		allPyBins, allNodeBins := a.AllAvailableManagers()
		ecosystemMap := a.ResolvedEcosystemProviders(ctx)
		spRows := buildSetupProvidersFromManagers(ecosystemMap, allPyBins, allNodeBins, settings)
		return toolsLoadedMsg{
			tools:              tools,
			settings:           settings,
			taps:               taps,
			setupProviders:     spRows,
			ecosystemProviders: a.EcosystemProviderNames(),
		}
	}
}

// doSetupImport runs omni import during the setup wizard and returns setupImportDoneMsg.
// enabled is the list of ecosystem provider names the user chose to keep active.
// disabled is the list to persist as host_settings.DisabledProviders.
func (m *Model) doSetupImport(disabled []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.Import(ctx, app.ImportOptions{SkipEcosystemProviders: disabled})
		if err != nil {
			return setupImportDoneMsg{err: err}
		}
		if len(disabled) > 0 {
			if err := a.SaveDisabledProviders(ctx, disabled); err != nil {
				return setupImportDoneMsg{err: err}
			}
		}
		tools, _ := a.ListTools(ctx, "")
		groupNames, toolGroups, _ := m.reloadToolGroups()
		hostInfo, _ := a.HostStatus()
		return setupImportDoneMsg{
			added:      len(result.Added),
			tools:      tools,
			toolGroups: toolGroups,
			groupNames: groupNames,
			hostInfo:   hostInfo,
		}
	}
}

// shortHostname returns the first label of the machine's hostname (e.g.
// "macbook" from "macbook.local"). Respects OMNI_HOSTNAME override.
func shortHostname() string {
	h := strings.TrimSpace(os.Getenv("OMNI_HOSTNAME"))
	if h == "" {
		host, err := os.Hostname()
		if err != nil {
			return "localhost"
		}
		h = strings.TrimSpace(host)
		if h == "" {
			return "localhost"
		}
	}
	if idx := strings.IndexByte(h, '.'); idx >= 0 {
		return h[:idx]
	}
	return h
}

// doSetupHost creates a host entry for the current machine.
func (m *Model) doSetupHost(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		hostname := shortHostname()
		if strings.TrimSpace(name) == "" {
			return setupHostDoneMsg{err: fmt.Errorf("hostname is required")}
		}
		if err := a.EnsureHost(hostname); err != nil {
			return setupHostDoneMsg{err: err}
		}
		info, err := a.HostStatus()
		if err != nil {
			return setupHostDoneMsg{err: err}
		}
		return setupHostDoneMsg{hostName: hostname, info: info}
	}
}

func (m *Model) doCopyHostGroupsFrom(host string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		info, err := a.HostStatus()
		if err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		source, ok := info.Hosts[host]
		if !ok {
			return hostCopiedMsg{err: fmt.Errorf("host %q not found", host), host: host}
		}
		if err := a.SetHostGroups(shortHostname(), source.Groups); err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		info, err = a.HostStatus()
		if err != nil {
			return hostCopiedMsg{err: err, host: host}
		}
		return hostCopiedMsg{host: host, info: info}
	}
}

func (m *Model) doSetupCopyHostConfigFrom(source string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		target := shortHostname()
		if err := a.CopyHostConfig(source, target); err != nil {
			return setupHostCopyDoneMsg{err: err, source: source, target: target}
		}
		info, err := a.HostStatus()
		if err != nil {
			return setupHostCopyDoneMsg{err: err, source: source, target: target}
		}
		return setupHostCopyDoneMsg{source: source, target: target, info: info}
	}
}

func (m *Model) doSetupHostGroups(groups []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		target := shortHostname()
		if err := a.SetHostGroups(target, groups); err != nil {
			return setupHostGroupsDoneMsg{err: err, groups: groups}
		}
		info, err := a.HostStatus()
		if err != nil {
			return setupHostGroupsDoneMsg{err: err, groups: groups}
		}
		return setupHostGroupsDoneMsg{groups: append([]string(nil), groups...), info: info}
	}
}

func (m *Model) doSetupBootstrapTools() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.Sync(ctx, gosync.SyncOptions{SkipPrivileged: true})
		if err != nil {
			return setupBootstrapDoneMsg{action: "sync-tools", err: err}
		}
		installed := 0
		if result != nil {
			installed = len(result.Installed())
		}
		message := "host tools applied"
		if installed > 0 {
			message = fmt.Sprintf("host tools applied, %d installed", installed)
		}
		return setupBootstrapDoneMsg{action: "sync-tools", message: message}
	}
}

func (m *Model) doSetupBootstrapDots() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ops, err := a.DotsSyncContext(ctx, dots.SyncOptions{})
		if err != nil {
			return setupBootstrapDoneMsg{action: "sync-dots", err: err}
		}
		message := "dotfiles applied"
		if len(ops) > 0 {
			message = fmt.Sprintf("dotfiles applied, %d operation(s)", len(ops))
		}
		return setupBootstrapDoneMsg{action: "sync-dots", message: message}
	}
}

// doSetupDotsRepo saves the dots repo path to settings. Dotfile sync itself is
// run from the Dots tab so onboarding stays limited to configuration.
func (m *Model) doSetupDotsRepo(path string) tea.Cmd {
	a, ctx := m.app, m.ctx
	settings := m.settings
	settings.DotsRepo = path
	settings.DotsDisabled = config.BoolPtr(false)
	return func() tea.Msg {
		if err := a.SaveSettings(ctx, settings); err != nil {
			return dangerOpDoneMsg{action: "setup-dots", err: fmt.Errorf("save dots repo: %w", err)}
		}
		if _, err := a.BootstrapDotsEntries(); err != nil {
			return dangerOpDoneMsg{action: "setup-dots", err: fmt.Errorf("bootstrap dots entries: %w", err)}
		}
		return dangerOpDoneMsg{action: "setup-dots", detail: "dots configured"}
	}
}

// doSaveDisabledProviders persists the list of disabled ecosystem providers to
// host_settings for this machine. Used by setup wizard step 2.
func (m *Model) doSaveDisabledProviders(disabled []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SaveDisabledProviders(ctx, disabled)
		return setupProvidersDoneMsg{err: err}
	}
}
