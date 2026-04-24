package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// waitForProgress blocks on one receive from ch and emits a progressMsg.
// Channel close is reported as a stream event, not operation completion; the
// background operation returns progressDoneMsg with the final result.
func waitForProgress(ch chan progressUpdate, gen int) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return progressStreamClosedMsg{gen: gen}
		}
		return progressMsg(update)
	}
}

func (m *Model) beginProgressStream() (chan progressUpdate, int) {
	m.progressCh = nil
	m.progressGen++
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	return ch, m.progressGen
}

func sendProgress(ch chan progressUpdate, gen int, text string) {
	select {
	case ch <- progressUpdate{gen: gen, text: text}:
	default:
	}
}

func sendToolProgress(ch chan progressUpdate, gen int, event gosync.ProgressEvent) {
	key := toolKey(event.Tool.Name, event.Tool.Provider)
	update := progressUpdate{gen: gen, text: event.Message, rowKey: key}
	if event.Err != nil {
		update.rowErr = event.Err.Error()
		update.rowDone = true
	} else if event.Done {
		update.rowDone = true
	} else {
		update.rowStatus = event.Message
	}
	select {
	case ch <- update:
	default:
	}
}

const (
	statusDurationOK  = 1500 * time.Millisecond
	statusDurationErr = 8000 * time.Millisecond // errors need more time to read
)

// setStatus sets the status message with appropriate styling and schedules
// auto-clear. Error messages are shown in red for twice as long as successful
// ones. Incrementing statusGen ensures stale timers from prior messages are
// ignored — newer activities always win.
func setStatus(m *Model, text string, isErr bool) tea.Cmd {
	return setStatusFor(m, text, isErr, statusDurationOK)
}

func setStatusFor(m *Model, text string, isErr bool, okDuration time.Duration) tea.Cmd {
	m.statusGen++
	if isErr {
		// Normalize multi-line errors (e.g. stow stderr) to a single readable line.
		text = strings.ReplaceAll(text, "\n", "  ")
		text = strings.ReplaceAll(text, "\r", "")
		if runes := []rune(text); len(runes) > 160 {
			text = string(runes[:157]) + "…"
		}
	}
	m.statusMsg = text
	m.statusIsErr = isErr
	d := okDuration
	if isErr {
		d = statusDurationErr
	}
	gen := m.statusGen
	return func() tea.Msg {
		time.Sleep(d)
		return clearStatusMsg{gen: gen}
	}
}

func startOp(m *Model, message string) {
	m.statusGen++
	m.statusMsg = message
	m.statusIsErr = false
}

func finishOpOK(m *Model, message string) tea.Cmd {
	return setStatus(m, "✓ "+message, false)
}

func finishOpErr(m *Model, message string) tea.Cmd {
	return setStatus(m, message, true)
}

func clearStatus(m *Model) {
	m.statusGen++
	m.statusMsg = ""
	m.statusIsErr = false
}

// doSyncWithProgress triggers a background sync with progress streaming.
func (m *Model) doSyncWithProgress(ch chan progressUpdate, gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer close(ch)
		result, err := a.Sync(ctx, gosync.SyncOptions{
			Progress: func(s string) {
				sendProgress(ch, gen, s)
			},
			ToolProgress: func(event gosync.ProgressEvent) {
				sendToolProgress(ch, gen, event)
			},
		})
		if err != nil {
			return progressDoneMsg{gen: gen, err: err}
		}
		installed := result.Installed()
		msg := "install complete"
		if len(installed) > 0 {
			msg = fmt.Sprintf("install complete — %d installed", len(installed))
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: sync succeeded; stale list retained if refresh fails
		rowErrors := syncResultRowErrors(result)
		if len(rowErrors) > 0 {
			msg = fmt.Sprintf("%s, %d failed", msg, len(rowErrors))
		}
		return progressDoneMsg{gen: gen, message: msg, tools: tools, rowErrors: rowErrors}
	}
}

// doSyncAllWithProgress installs configured missing tools and adds currently
// discovered local tools to this machine's hostname group.
func (m *Model) doSyncAllWithProgress(ch chan progressUpdate, gen int, discovered []*database.ToolCache) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer close(ch)
		result, err := a.SyncAll(ctx, app.SyncAllOptions{
			Discovered: discovered,
			Progress: func(s string) {
				sendProgress(ch, gen, s)
			},
			ToolProgress: func(event gosync.ProgressEvent) {
				sendToolProgress(ch, gen, event)
			},
		})

		tools, _ := a.ListTools(ctx, "")
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		claimedNames := syncAllClaimedNames(result)
		msg := syncAllMessage(result, len(claimedNames))
		rowErrors := syncAllRowErrors(result)
		if len(rowErrors) > 0 {
			msg = fmt.Sprintf("%s, %d failed", msg, len(rowErrors))
			// SyncAll returns errors.Join(claimErr, syncErr); each joined error
			// is already represented in result.Failures (and therefore rowErrors).
			// Clear err so the status bar shows the summary message instead of
			// duplicating per-tool failures already attached to row entries.
			err = nil
		}
		return progressDoneMsg{
			gen:             gen,
			message:         msg,
			err:             err,
			tools:           tools,
			claimedNames:    claimedNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			groupNames:      groupNames,
			rowErrors:       rowErrors,
		}
	}
}

func syncAllClaimedNames(result *app.SyncAllResult) []string {
	if result == nil {
		return nil
	}
	return result.ClaimedNames
}

func syncAllRowErrors(result *app.SyncAllResult) map[string]string {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	return bulkToolErrorsToRowErrors(result.Failures)
}

func (m *Model) reloadToolGroups() ([]string, map[string]string, map[string][]string) {
	groups, _ := m.app.Groups(m.ctx)
	memberships, _ := m.app.ToolMembershipMap(m.ctx)
	return buildGroupNames(groups), compactToolGroupMapForProfile(memberships, m.profileInfo), memberships
}

func syncResultRowErrors(result *gosync.SyncResult) map[string]string {
	if result == nil {
		return nil
	}
	failed := result.Failed()
	rowErrors := make(map[string]string, len(failed))
	for _, op := range failed {
		if op.Err == nil || op.Tool.Name == "" || op.Tool.Provider == "" {
			continue
		}
		rowErrors[toolKey(op.Tool.Name, op.Tool.Provider)] = op.Err.Error()
	}
	if len(rowErrors) == 0 {
		return nil
	}
	return rowErrors
}

func syncAllMessage(result *app.SyncAllResult, claimed int) string {
	installed := 0
	if result != nil && result.SyncResult != nil {
		installed = len(result.SyncResult.Installed())
	}
	return fmt.Sprintf("sync complete — %d installed, %d added to config", installed, claimed)
}

// doSaveNodeManager persists the chosen node manager to host_settings.
// Used by setup wizard step 3.
func (m *Model) doSaveNodeManager(manager string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		return setupNodeMgrDoneMsg{err: a.SaveNodeManager(ctx, manager)}
	}
}

// anyMissingDescription reports whether any tool in the list lacks a cached
// description. Used to skip the background description warm-up on launches
// where every tool is already populated.
func anyMissingDescription(tools []*database.ToolCache) bool {
	for _, t := range tools {
		if !t.Description.Valid || t.Description.String == "" {
			return true
		}
	}
	return false
}

// doScanProvider scans a single named provider: updates install status and
// checks for outdated tools. A 45-second timeout prevents slow network calls
// (e.g. brew outdated) from blocking the UI indefinitely. Does NOT call
// ListTools — all goroutines must finish their upserts before a consistent
// snapshot can be read. The final ListTools is done by doFetchFinalTools once
// every providerScannedMsg has arrived.
func (m *Model) doScanProvider(provName string, gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		installErr := a.RefreshProviderInstalled(ctx, provName)
		outdatedErr := a.RefreshProviderOutdated(ctx, provName)
		return providerScannedMsg{gen: gen, provider: provName, err: errors.Join(installErr, outdatedErr)}
	}
}

// doFetchFinalTools fetches a consistent tool list after all per-provider scan
// goroutines have completed their upserts. This single call avoids the race
// where concurrent ListTools calls produce snapshots that are missing each
// other's upserts.
func (m *Model) doFetchFinalTools(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return allProvidersDoneMsg{gen: gen, err: err}
		}
		ecosystemMap := a.ResolvedEcosystemProviders(ctx)
		return allProvidersDoneMsg{
			gen:                    gen,
			tools:                  tools,
			effectiveSystemManager: ecosystemMap[provider.EcosystemSystem],
		}
	}
}

// doRefreshDiscovered scans all providers for locally-installed tools that are
// not in the config (orphan scan). Runs as a separate background pass so it
// does not delay the installedRefreshedMsg signal.
func (m *Model) doRefreshDiscovered(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.RefreshDiscovered(ctx); err != nil {
			return discoveredRefreshedMsg{gen: gen, err: err}
		}
		discovered, err := a.ListDiscovered(ctx)
		if err != nil {
			return discoveredRefreshedMsg{gen: gen, err: err}
		}
		return discoveredRefreshedMsg{gen: gen, discovered: discovered}
	}
}

// doRefreshDescriptions pre-warms the description cache for configured and
// discovered tools that don't have one yet.
func (m *Model) startDescriptionRefresh() tea.Cmd {
	m.descRefreshGen++
	return m.doRefreshDescriptions(m.descRefreshGen)
}

func (m *Model) doRefreshDescriptions(gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.RefreshDescriptions(ctx, 0); err != nil {
			// Pre-warm failed; return empty so the TUI keeps its current list.
			return descRefreshDoneMsg{gen: gen}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: stale descriptions remain if list refresh fails
		return descRefreshDoneMsg{gen: gen, tools: tools}
	}
}

// doUpgradeAll upgrades every outdated tool with progress streaming.
func (m *Model) doUpgradeAll(ch chan progressUpdate, gen int) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		defer close(ch)
		result, err := a.UpgradeAllDetailed(ctx, func(s string) {
			sendProgress(ch, gen, s)
		}, func(event gosync.ProgressEvent) {
			sendToolProgress(ch, gen, event)
		})
		tools, _ := a.ListTools(ctx, "") // non-fatal: upgrade succeeded; stale list retained if refresh fails
		rowErrors := upgradeAllRowErrors(result)
		if len(rowErrors) > 0 {
			return progressDoneMsg{gen: gen, key: "*", message: fmt.Sprintf("upgrades complete — %d failed", len(rowErrors)), tools: tools, rowErrors: rowErrors}
		}
		if err != nil {
			return progressDoneMsg{gen: gen, key: "*", err: err, tools: tools}
		}
		return progressDoneMsg{gen: gen, key: "*", message: "upgrades complete", tools: tools}
	}
}

func upgradeAllRowErrors(result *app.UpgradeAllResult) map[string]string {
	if result == nil || len(result.Failures) == 0 {
		return nil
	}
	return bulkToolErrorsToRowErrors(result.Failures)
}

func bulkToolErrorsToRowErrors(failures []app.BulkToolError) map[string]string {
	rowErrors := make(map[string]string, len(failures))
	for _, failure := range failures {
		if failure.Name == "" || failure.Provider == "" || failure.Message == "" {
			continue
		}
		rowErrors[toolKey(failure.Name, failure.Provider)] = failure.Message
	}
	if len(rowErrors) == 0 {
		return nil
	}
	return rowErrors
}

// doInstall installs a single tool.
func (m *Model) doInstall(name, prov string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.Install(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: install succeeded; stale list retained if refresh fails
		return opCompleteMsg{message: "installed " + name, tools: tools}
	}
}

// doDelete deletes a single tool.
func (m *Model) doDelete(name, prov string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.Uninstall(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: delete succeeded; stale list retained if refresh fails
		return opCompleteMsg{message: "deleted " + name, tools: tools}
	}
}

// doDeleteFromConfig deletes a missing tool from settings.json without
// calling a package manager.
func (m *Model) doDeleteFromConfig(name, prov string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.RemoveToolFromConfig(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: config update succeeded
		return opCompleteMsg{message: "deleted " + name + " from config", tools: tools}
	}
}

// doUpgrade upgrades a single tool.
func (m *Model) doUpgrade(name, prov string) tea.Cmd {
	a, ctx := m.app, m.ctx
	uk := toolKey(name, prov)
	return func() tea.Msg {
		if err := a.Upgrade(ctx, name, prov); err != nil {
			return opCompleteMsg{key: uk, err: err}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: upgrade succeeded; stale list retained if refresh fails
		return opCompleteMsg{key: uk, message: "upgraded " + name, tools: tools}
	}
}

// cancelSearch cancels any in-flight provider search HTTP request.
// Callers are responsible for incrementing searchGen to invalidate pending
// debounced messages.
func (m *Model) cancelSearch() {
	if m.searchCancel != nil {
		m.searchCancel()
		m.searchCancel = nil
	}
}

// startSearch cancels any in-flight search, increments the generation counter,
// sets the searching flag, and returns the commands needed to run doSearch.
// It is the single entry point for triggering a provider search.
func (m *Model) startSearch(query string) []tea.Cmd {
	if m.searchCancel != nil {
		m.searchCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.searchCancel = cancel
	m.searching = true
	clearStatus(m) // clear stale text so activityLabel renders "Searching…"
	return []tea.Cmd{m.spinner.Tick, m.doSearch(ctx, query, m.searchGen)}
}

// debounceSearch returns a command that sleeps for the debounce delay and then
// emits debouncedSearchMsg. When the user types faster than the delay, the
// generation counter will have advanced and the stale message is dropped.
func debounceSearch(query string, gen int) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(300 * time.Millisecond)
		return debouncedSearchMsg{query: query, gen: gen}
	}
}

// doSearch runs a provider search within ctx and returns searchResultsMsg.
// If ctx is cancelled before the search completes, nil is returned so that
// Bubbletea dispatches no message and the spinner is cleared by the next
// startSearch call instead.
func (m *Model) doSearch(ctx context.Context, query string, gen int) tea.Cmd {
	a := m.app
	// Pass the active provider pill as a filter so the network call is scoped
	// to the selected provider rather than fanning out to all providers.
	providerFilter := m.currentSearchProviderFilter()
	return func() tea.Msg {
		results, err := a.Search(ctx, query, providerFilter)
		if ctx.Err() != nil {
			return nil // cancelled — next search (or Esc) owns the searching flag
		}
		if err != nil {
			return searchResultsMsg{gen: gen, query: query, providerFilter: providerFilter, err: err}
		}
		tools := make([]*database.ToolCache, 0, len(results))
		for _, r := range results {
			t := &database.ToolCache{
				Name:     r.Name,
				Provider: r.Provider,
			}
			if r.Version != "" {
				t.Version = sql.NullString{String: r.Version, Valid: true}
			}
			if r.Description != "" {
				t.Description = sql.NullString{String: r.Description, Valid: true}
			}
			tools = append(tools, t)
		}
		return searchResultsMsg{gen: gen, query: query, providerFilter: providerFilter, tools: tools}
	}
}

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
		return setupImportDoneMsg{
			added:      len(result.Added),
			tools:      tools,
			toolGroups: toolGroups,
			groupNames: groupNames,
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

// doSetupProfile creates a profile with the given name and maps the current
// hostname to it. Used by the setup wizard profile step.
func (m *Model) doSetupProfile(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.AddProfile(name, []string{}); err != nil {
			return setupProfileDoneMsg{err: err}
		}
		hostname := shortHostname()
		if err := a.SetHostname(hostname, name); err != nil {
			return setupProfileDoneMsg{err: err}
		}
		return setupProfileDoneMsg{profileName: name}
	}
}

func (m *Model) doActivateProfile(profile string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.SetHostname(shortHostname(), profile); err != nil {
			return profileActivatedMsg{err: err, profile: profile}
		}
		info, err := a.ProfileStatus()
		if err != nil {
			return profileActivatedMsg{err: err, profile: profile}
		}
		return profileActivatedMsg{profile: profile, info: info}
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
			return toolsLoadedMsg{err: fmt.Errorf("save dots repo: %w", err)}
		}
		if _, err := a.BootstrapDotsEntries(); err != nil {
			return toolsLoadedMsg{err: fmt.Errorf("bootstrap dots entries: %w", err)}
		}
		return loadTools(a, ctx)()
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

func (m *Model) doSetToolGroupMembership(name, group string, add bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		var err error
		if add {
			err = a.AddToolToGroup(name, group)
		} else {
			err = a.RemoveToolFromGroup(name, group)
		}
		if err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		tools, _ := a.ListTools(ctx, "")
		action := "added to"
		if !add {
			action = "removed from"
		}
		return groupChangedMsg{
			detail:          "✓ " + name + " " + action + " " + group,
			tools:           tools,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			groupNames:      groupNames,
		}
	}
}

func (m *Model) doSetToolGroupMemberships(name string, before, after, createdGroups []string, activeProfile string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		for _, group := range createdGroups {
			if activeProfile != "" {
				if err := a.AddGroupToProfile(activeProfile, group); err != nil {
					return groupChangedMsg{err: err}
				}
			} else if err := a.CreateGroup(group); err != nil {
				return groupChangedMsg{err: err}
			}
		}
		beforeSet := stringSet(before)
		afterSet := stringSet(after)
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveToolFromGroup(name, group); err != nil {
					return groupChangedMsg{err: err}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.AddToolToGroup(name, group); err != nil {
					return groupChangedMsg{err: err}
				}
			}
		}
		groups, _ := a.Groups(ctx)
		memberships, _ := a.ToolMembershipMap(ctx)
		info, _ := a.ProfileStatus()
		tools, _ := a.ListTools(ctx, "")
		return groupChangedMsg{
			detail:          "✓ updated groups for " + name,
			tools:           tools,
			toolGroups:      compactToolGroupMapForProfile(memberships, info),
			toolMemberships: memberships,
			groupNames:      buildGroupNames(groups),
			info:            info,
		}
	}
}

func (m *Model) doSetDotGroupMemberships(name string, before, after, createdGroups []string, activeProfile string) tea.Cmd {
	a, ctx := m.app, m.ctx
	_, gen := m.currentDotsOperation()
	return func() tea.Msg {
		for _, group := range createdGroups {
			if activeProfile != "" {
				if err := a.AddGroupToProfile(activeProfile, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			} else if err := a.CreateGroup(group); err != nil {
				return dotsLoadedMsg{gen: gen, err: err}
			}
		}
		beforeSet := stringSet(before)
		afterSet := stringSet(after)
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveDotFromGroup(name, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.AddDotToGroup(name, group); err != nil {
					return dotsLoadedMsg{gen: gen, err: err}
				}
			}
		}
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			return dotsLoadedMsg{gen: gen, err: err}
		}
		return dotsLoadedMsg{
			gen:            gen,
			entries:        result.Entries,
			gitStatus:      result.GitStatus,
			dotMemberships: loadDotMemberships(a, ctx),
			detail:         "✓ updated groups for " + name,
		}
	}
}

func (m *Model) doSetProfileGroupTools(group string, membership, originalMembership, ignores, originalIgnores map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changed := 0
		for name, desired := range membership {
			if originalMembership[name] == desired {
				continue
			}
			var err error
			if desired {
				err = a.AddToolToGroup(name, group)
			} else {
				err = a.RemoveToolFromGroup(name, group)
			}
			if err != nil {
				return groupToolsChangedMsg{err: err}
			}
			changed++
		}
		for name, desired := range ignores {
			if originalIgnores[name] == desired {
				continue
			}
			if err := a.SetGroupIgnore(group, name, desired); err != nil {
				return groupToolsChangedMsg{err: err}
			}
			changed++
		}
		groups, _ := a.Groups(ctx)
		memberships, _ := a.ToolMembershipMap(ctx)
		info, _ := a.ProfileStatus()
		tools, _ := a.ListTools(ctx, "")
		var profileIgnore []string
		if info != nil && info.Active != "" {
			if prof, ok := info.Profiles[info.Active]; ok {
				profileIgnore = prof.Ignore
			}
		}
		labels := buildIgnoreLabels(a.ConfigPath, groups, profileIgnore)
		toolIgnores, groupIgnores, _ := buildToolScopeState(a.ConfigPath, groups)
		return groupToolsChangedMsg{
			detail:          fmt.Sprintf("✓ updated %d tool settings for %s", changed, group),
			tools:           tools,
			toolGroups:      compactToolGroupMapForProfile(memberships, info),
			toolMemberships: memberships,
			groupNames:      buildGroupNames(groups),
			ignoreLabels:    labels,
			toolIgnoreSet:   toolIgnores,
			groupIgnoreSet:  groupIgnores,
		}
	}
}

func (m *Model) doSetProfileGroupDots(group string, membership, originalMembership map[string]bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changed := 0
		for name, desired := range membership {
			if originalMembership[name] == desired {
				continue
			}
			var err error
			if desired {
				err = a.AddDotToGroup(name, group)
			} else {
				err = a.RemoveDotFromGroup(name, group)
			}
			if err != nil {
				return groupDotsChangedMsg{err: err}
			}
			changed++
		}
		memberships := loadDotMemberships(a, ctx)
		msg := groupDotsChangedMsg{
			detail:         fmt.Sprintf("✓ updated %d dotfiles for %s", changed, group),
			dotMemberships: memberships,
		}
		settings, _ := a.LoadSettings()
		if settings.DotsRepo == "" {
			return msg
		}
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			return groupDotsChangedMsg{err: err, dotMemberships: memberships}
		}
		msg.entries = result.Entries
		msg.gitStatus = result.GitStatus
		return msg
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// doCreateGroup creates a new empty named group in config.
func (m *Model) doCreateGroup(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.CreateGroup(name); err != nil {
			return createGroupDoneMsg{err: err, name: name}
		}
		groups, _ := a.Groups(ctx) // non-fatal: group created; stale list retained if refresh fails
		return createGroupDoneMsg{name: name, groupNames: buildGroupNames(groups)}
	}
}

// doSaveSettings persists the current settings to the config file.
func (m *Model) doSaveSettings() tea.Cmd {
	m.settingsSaveGen++
	gen := m.settingsSaveGen
	snapshot := cloneSettingsSnapshot(m.settings)
	if m.settingsSaveRunning {
		m.settingsSaveQueued = true
		m.settingsSaveQueuedSnapshot = snapshot
		m.settingsSaveQueuedGen = gen
		return nil
	}
	return m.startSettingsSave(snapshot, gen)
}

func (m *Model) appendSaveSettingsCmd(cmds *[]tea.Cmd) {
	if cmd := m.doSaveSettings(); cmd != nil {
		*cmds = append(*cmds, cmd)
	}
}

func (m *Model) startSettingsSave(settings config.Settings, gen int) tea.Cmd {
	m.settingsSaveRunning = true
	m.settingsSaveInFlightGen = gen
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		return settingsSavedMsg{gen: gen, err: a.SaveSettings(ctx, settings)}
	}
}

func cloneSettingsSnapshot(in config.Settings) config.Settings {
	out := in
	if in.Ecosystems != nil {
		out.Ecosystems = make(map[string]config.EcosystemSettings, len(in.Ecosystems))
		for name, eco := range in.Ecosystems {
			eco.Priority = append([]string(nil), eco.Priority...)
			out.Ecosystems[name] = eco
		}
	}
	if in.DotsDisabled != nil {
		dotsDisabled := *in.DotsDisabled
		out.DotsDisabled = &dotsDisabled
	}
	if in.DisabledProviders != nil {
		out.DisabledProviders = append([]string{}, in.DisabledProviders...)
	}
	return out
}

func (m *Model) doSaveSettingsAndDotsSync(settings config.Settings) tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	snapshot := cloneSettingsSnapshot(settings)
	return func() tea.Msg {
		if err := a.SaveSettings(ctx, snapshot); err != nil {
			return dotsSyncedMsg{gen: gen, err: fmt.Errorf("save dots repo: %w", err)}
		}
		_, syncErr := a.DotsSyncContext(ctx, dots.SyncOptions{})
		result, err := a.DiscoverDotsStatus(ctx)
		if err != nil {
			if result != nil {
				return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: combineDotsErrors(syncErr, err)}
			}
			return dotsSyncedMsg{gen: gen, err: combineDotsErrors(syncErr, err)}
		}
		return dotsSyncedMsg{gen: gen, entries: result.Entries, gitStatus: result.GitStatus, dotMemberships: loadDotMemberships(a, ctx), err: syncErr}
	}
}

// selectedProfileName returns the profile name at the current profileCursor, or "".
func (m *Model) selectedProfileName() string {
	if m.profileInfo == nil || len(m.profileInfo.Profiles) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.profileInfo.Profiles))
	for n := range m.profileInfo.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	if m.profileCursor >= 0 && m.profileCursor < len(names) {
		return names[m.profileCursor]
	}
	return ""
}

// doAddGroupToProfile appends a group to a profile and reloads profile info.
func (m *Model) doAddGroupToProfile(profile, group string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.AddGroupToProfile(profile, group); err != nil {
			return profileGroupChangedMsg{err: err, profile: profile, group: group, added: true}
		}
		info, _ := a.ProfileStatus()
		return profileGroupChangedMsg{profile: profile, group: group, added: true, info: info}
	}
}

// doRemoveGroupFromProfile removes a group from a profile and reloads profile info.
func (m *Model) doRemoveGroupFromProfile(profile, group string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RemoveGroupFromProfile(profile, group); err != nil {
			return profileGroupChangedMsg{err: err, profile: profile, group: group, added: false}
		}
		info, _ := a.ProfileStatus()
		return profileGroupChangedMsg{profile: profile, group: group, added: false, info: info}
	}
}

// doSetProfileGroups persists a staged profile group-membership edit.
func (m *Model) doSetProfileGroups(profile string, before, after, createdGroups []string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		beforeSet := make(map[string]bool, len(before))
		afterSet := make(map[string]bool, len(after))
		for _, group := range before {
			beforeSet[group] = true
		}
		for _, group := range after {
			afterSet[group] = true
		}
		for _, group := range createdGroups {
			if afterSet[group] {
				if err := a.CreateGroup(group); err != nil {
					return profileGroupChangedMsg{err: err, profile: profile}
				}
			}
		}
		for group := range beforeSet {
			if !afterSet[group] {
				if err := a.RemoveGroupFromProfile(profile, group); err != nil {
					return profileGroupChangedMsg{err: err, profile: profile}
				}
			}
		}
		for group := range afterSet {
			if !beforeSet[group] {
				if err := a.AddGroupToProfile(profile, group); err != nil {
					return profileGroupChangedMsg{err: err, profile: profile}
				}
			}
		}
		info, _ := a.ProfileStatus()
		return profileGroupChangedMsg{profile: profile, detail: "✓ groups updated for " + profile, info: info}
	}
}

// doSetProfileHosts persists staged host mappings for one profile.
func (m *Model) doSetProfileHosts(profile string, before, after map[string]string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		seen := make(map[string]bool, len(before)+len(after))
		for host := range before {
			seen[host] = true
		}
		for host := range after {
			seen[host] = true
		}
		for host := range seen {
			oldProfile := before[host]
			newProfile := after[host]
			if oldProfile == newProfile {
				continue
			}
			if newProfile == "" {
				if err := a.RemoveHostname(host); err != nil {
					return profileGroupChangedMsg{err: err, profile: profile}
				}
				continue
			}
			if err := a.SetHostname(host, newProfile); err != nil {
				return profileGroupChangedMsg{err: err, profile: profile}
			}
		}
		info, _ := a.ProfileStatus()
		return profileGroupChangedMsg{profile: profile, detail: "✓ hosts updated for " + profile, info: info}
	}
}

// doDeleteProfileFromTab removes a profile from settings.json and reloads profile info.
func (m *Model) doDeleteProfileFromTab(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.DeleteProfile(name); err != nil {
			return profileGroupChangedMsg{err: err, profile: name}
		}
		info, _ := a.ProfileStatus()
		return profileGroupChangedMsg{profile: name, info: info}
	}
}

// doRenameProfile renames a profile and reloads profile info.
func (m *Model) doRenameProfile(oldName, newName string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RenameProfile(oldName, newName); err != nil {
			return profileGroupChangedMsg{err: err, profile: oldName}
		}
		info, _ := a.ProfileStatus()
		return profileGroupChangedMsg{profile: newName, detail: "✓ " + oldName + " renamed to " + newName, info: info}
	}
}

// doCreateProfileFromTab creates a new profile with no groups and reloads profile info.
func (m *Model) doCreateProfileFromTab(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.AddProfile(name, []string{}); err != nil {
			return profileCreatedMsg{err: err, profile: name}
		}
		info, _ := a.ProfileStatus()
		return profileCreatedMsg{profile: name, info: info}
	}
}

// doRenameGroup renames a group in config and reloads group/profile data.
func (m *Model) doRenameGroup(oldName, newName string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.RenameGroup(oldName, newName); err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		info, _ := a.ProfileStatus()
		return groupChangedMsg{
			detail:          "✓ renamed " + oldName + " → " + newName,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			info:            info,
		}
	}
}

// doDeleteGroup removes a group, then reloads group/profile data.
func (m *Model) doDeleteGroup(name string, deleteTools bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		opts := app.DeleteGroupOptions{MoveTo: "base"}
		detail := "✓ deleted group " + name + " (tools moved to base)"
		if deleteTools {
			opts = app.DeleteGroupOptions{DeleteTools: true}
			detail = "✓ deleted group " + name + " (last-membership tools deleted)"
		}
		if err := a.DeleteGroup(ctx, name, opts); err != nil {
			return groupChangedMsg{err: err}
		}
		groupNames, toolGroups, memberships := m.reloadToolGroups()
		tools, _ := a.ListTools(ctx, "")
		info, _ := a.ProfileStatus()
		return groupChangedMsg{
			detail:          detail,
			tools:           tools,
			groupNames:      groupNames,
			toolGroups:      toolGroups,
			toolMemberships: memberships,
			info:            info,
		}
	}
}

// doDeleteProfile removes the named profile from settings.json then reloads tools.
func (m *Model) doDeleteProfile(name string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if err := a.DeleteProfile(name); err != nil {
			return dangerOpDoneMsg{action: "delete-profile", err: err}
		}
		return dangerOpDoneMsg{action: "delete-profile", detail: name + " deleted", reload: true}
	}
}

// doResetSettings resets settings to defaults then reloads tools.
func (m *Model) doResetSettings() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.ResetSettings(ctx); err != nil {
			return dangerOpDoneMsg{action: "reset-settings", err: err}
		}
		return dangerOpDoneMsg{action: "reset-settings", detail: "settings cleared", reload: true}
	}
}

// doResetCache deletes and reinitialises the tool cache DB, then reloads tools.
func (m *Model) doResetCache() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.ResetCache(ctx); err != nil {
			return dangerOpDoneMsg{action: "reset-cache", err: err}
		}
		return dangerOpDoneMsg{action: "reset-cache", detail: "cache cleared", reload: true}
	}
}

// doDisableDots removes managed symlinks when dots is configured, optionally
// keeping local materialized copies, then persists dots_disabled=true for this
// machine and triggers a full settings reload. Safe to call when dots is not
// yet configured — the physical unlink step is skipped in that case.
func (m *Model) doDisableDots(keepLocal bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		ops, err := a.DisableDotsForHost(ctx, app.DisableDotsOptions{
			KeepExistingLocal: keepLocal,
			RemoveLocal:       !keepLocal,
		})
		detail := dotsDisableDetail(ops)
		if err != nil {
			return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: true, err: err}
		}
		return dangerOpDoneMsg{action: "disable-dots", detail: detail, reload: true}
	}
}

func dotsDisableDetail(ops []dots.Op) string {
	if len(ops) == 0 {
		return "dots disabled"
	}
	var unlinked, conflicts int
	for _, op := range ops {
		switch op.Kind {
		case dots.OpUnlink:
			unlinked++
		case dots.OpUnlinkConflict:
			conflicts++
		}
	}
	detail := fmt.Sprintf("%d unlinked", unlinked)
	if conflicts > 0 {
		detail += fmt.Sprintf(", %d conflicts", conflicts)
	}
	return detail
}

// doEnableDots re-enables dotfile sync: clears the disabled flag and runs a
// sync to restore managed symlinks.
func (m *Model) doEnableDots() tea.Cmd {
	a := m.app
	ctx, gen := m.currentDotsOperation()
	return func() tea.Msg {
		if _, err := a.EnableDotsForHost(ctx); err != nil {
			return dangerOpDoneMsg{action: "enable-dots", dotsGen: gen, err: err}
		}
		return dangerOpDoneMsg{action: "enable-dots", dotsGen: gen, detail: "dots enabled", reload: true, mode: viewDots}
	}
}

// doConsolidate switches all tools in ecosystem to manager.
func (m *Model) doConsolidate(ecosystem, manager string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		result, err := a.Consolidate(ctx, ecosystem, manager, nil)
		tools, _ := a.ListTools(ctx, "") // non-fatal: consolidate succeeded; stale list retained if refresh fails
		if err != nil {
			return opCompleteMsg{err: err}
		}
		parts := []string{fmt.Sprintf("%d migrated to %s", len(result.Migrated), manager)}
		if len(result.Failed) > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", len(result.Failed)))
		}
		if len(result.UninstallWarnings) > 0 {
			parts = append(parts, fmt.Sprintf("%d uninstall warning(s)", len(result.UninstallWarnings)))
		}
		return opCompleteMsg{message: strings.Join(parts, ", "), tools: tools}
	}
}

// doClaim adds an orphan tool (installed but not in config) to the given group.
// Pass groupName="" to add to the base group.
func (m *Model) doClaim(name, prov, groupName string, activeProfile ...string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.Add(ctx, prov, name, name, groupName, ""); err != nil {
			return claimDoneMsg{err: err, name: name, groupName: groupName}
		}
		if len(activeProfile) > 0 && activeProfile[0] != "" && groupName != "" {
			if err := a.AddGroupToProfile(activeProfile[0], groupName); err != nil {
				return claimDoneMsg{err: err, name: name, groupName: groupName}
			}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: claim succeeded; stale list retained if refresh fails
		return claimDoneMsg{name: name, groupName: groupName, tools: tools}
	}
}

func (m *Model) doSetIgnoreScope(name string, opt scopeOption) tea.Cmd {
	opt.initialChecked = opt.checked
	opt.checked = !opt.checked
	return m.doSaveIgnoreScopes(name, []scopeOption{opt})
}

func (m *Model) doSaveIgnoreScopes(name string, options []scopeOption) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		changedProfile := false
		ignored := false
		for _, opt := range options {
			if opt.checked == opt.initialChecked {
				continue
			}
			desired := opt.checked
			ignored = ignored || desired
			var err error
			switch opt.kind {
			case "tool":
				err = a.SetToolIgnore(name, desired)
			case "group":
				err = a.SetGroupIgnore(opt.group, name, desired)
			case "profile":
				changedProfile = true
				profile := ""
				if m.profileInfo != nil {
					profile = m.profileInfo.Active
				}
				if desired {
					err = a.AddIgnoreToProfile(profile, name)
				} else {
					err = a.RemoveIgnoreFromProfile(profile, name)
				}
			default:
				err = fmt.Errorf("unknown ignore scope %q", opt.kind)
			}
			if err != nil {
				return ignoreDoneMsg{err: err, name: name, ignored: desired}
			}
		}
		groups, _ := a.Groups(ctx)
		tools, _ := a.ListTools(ctx, "")
		var profileIgnore []string
		if info, _ := a.ProfileStatus(); info != nil && info.Active != "" {
			if prof, ok := info.Profiles[info.Active]; ok {
				profileIgnore = prof.Ignore
			}
		}
		labels := buildIgnoreLabels(a.ConfigPath, groups, profileIgnore)
		toolIgnores, groupIgnores, _ := buildToolScopeState(a.ConfigPath, groups)
		if labels[name] != "" {
			ignored = true
		}
		return ignoreDoneMsg{name: name, ignored: ignored, tools: tools, profileScope: changedProfile, ignoreLabels: labels, toolIgnoreSet: toolIgnores, groupIgnoreSet: groupIgnores}
	}
}

// doInstallAndAdd installs a tool and adds it to the selected config group.
// Used for search results and orphan tools that are not yet in config.
func (m *Model) doInstallAndAdd(name, prov string, groupAndProfile ...string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		group := ""
		if len(groupAndProfile) > 0 {
			group = groupAndProfile[0]
		}
		if err := a.Install(ctx, name, prov); err != nil {
			return opCompleteMsg{err: err}
		}
		if err := a.Add(ctx, prov, name, name, group, ""); err != nil {
			// Install succeeded but config save failed. Surface as error so the
			// status bar renders red — config is the recoverable part the user
			// must address; pretending green ✓ would hide the failure.
			return opCompleteMsg{err: fmt.Errorf("installed %s but config save failed: %w", name, err)}
		}
		if len(groupAndProfile) > 1 && groupAndProfile[1] != "" && group != "" {
			if err := a.AddGroupToProfile(groupAndProfile[1], group); err != nil {
				return opCompleteMsg{err: fmt.Errorf("installed %s and added to config but profile update failed: %w", name, err)}
			}
		}
		tools, _ := a.ListTools(ctx, "") // non-fatal: install+add succeeded; stale list retained if refresh fails
		return opCompleteMsg{message: "installed " + name + " and added to config", tools: tools}
	}
}

func (m *Model) doSetProviderScope(name string, opt scopeOption, t *database.ToolCache) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if t == nil || t.InstalledWith == "" {
			return opCompleteMsg{err: fmt.Errorf("provider pin: installed provider is unknown")}
		}
		var err error
		switch opt.kind {
		case "provider-host":
			err = a.SetToolHostInstallSpec(name, t.Provider, t.Package, t.InstalledWith)
		case "provider-tool":
			err = a.SetToolDefaultInstallSpec(name, t.Provider, t.Package, t.InstalledWith)
		case "provider-ecosystem":
			err = a.PinEcosystemForHost(ctx, t.Provider, t.InstalledWith)
		default:
			err = fmt.Errorf("unknown provider scope %q", opt.kind)
		}
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: %w", err)}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return opCompleteMsg{err: fmt.Errorf("pin provider: list tools: %w", err)}
		}
		groups, _ := a.Groups(ctx)
		_, _, pins := buildToolScopeState(a.ConfigPath, groups)
		if pins == nil {
			pins = make(map[string]string)
		}
		pins[name] = t.InstalledWith
		return opCompleteMsg{message: "pinned " + name + " via " + opt.label, tools: tools, toolProviderPins: pins}
	}
}

// doMigrateProvider installs the tool via the config-intended provider, removes
// it from the currently-installed (wrong) provider, and updates the config.
// syncWrongProv means: tool is installed via installedWith but config says configProv.
// Migrate = install via configProv, uninstall from installedWith, rewrite config.
func (m *Model) doMigrateProvider(name, configProv, installedWith string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if _, err := a.MigrateInstallation(ctx, name, installedWith, configProv); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		// Re-probe all providers so InstalledWith is updated in the DB before we
		// return the fresh tool list. Runs silently (no progress channel needed here).
		if err := a.RefreshInstalled(ctx, nil); err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: refresh installed: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		tools, err := a.ListTools(ctx, "")
		if err != nil {
			return migrateProviderDoneMsg{err: fmt.Errorf("reinstall with default: list tools: %w", err), name: name, fromProvider: installedWith, toProvider: configProv}
		}
		return migrateProviderDoneMsg{name: name, fromProvider: installedWith, toProvider: configProv, tools: tools}
	}
}

// openFilePicker configures and opens the file picker popup.
// title is shown as the popup header.
// currentPath sets the starting directory: opens at the path itself if it
// already exists as a directory, otherwise falls back to its parent (or home).
// allowFiles controls whether plain files are selectable in addition to directories.
// Returns a tea.Cmd that starts the async directory read.
func (m *Model) openFilePicker(title, currentPath string, allowFiles bool) tea.Cmd {
	fp, cmd := newPathPicker(currentPath, allowFiles, filePickerContentWidth(*m), filePickerListHeight(*m))

	m.dotsFilePicker = fp
	m.filePickerTitle = title
	m.filePickerAllowFiles = allowFiles
	m.showFilePicker = true
	return cmd
}
