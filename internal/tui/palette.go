package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/actions"
)

type palCmd struct {
	name string // what the user types
	desc string // shown in the suggestion list
	run  func(m *Model) tea.Cmd
}

// Called on every keystroke in palette mode — must be free of IO.
func buildPalette(m Model) []palCmd {
	reconcileAction := actions.MustPalette(actions.Reconcile)
	syncAction := actions.MustPalette(actions.ToolSync)
	cmds := []palCmd{
		{
			name: paletteCommandName(reconcileAction),
			desc: reconcileAction.Description,
			run: func(m *Model) tea.Cmd {
				m.mode = viewStatus
				var cmds []tea.Cmd
				m.startDashboardReconcile(&cmds)
				return tea.Batch(cmds...)
			},
		},
		{
			name: paletteCommandName(syncAction),
			desc: syncAction.Description,
			run: func(m *Model) tea.Cmd {
				m.beginLoading(loadingOwnerProgressOp)
				m.progressText = ""
				ch, gen := m.beginProgressStream()
				m.markBulkPendingSync()
				return tea.Batch(m.spinner.Tick, m.doSyncWithProgress(ch, gen), waitForProgress(ch, gen))
			},
		},
	}

	if m.dotsConfiguredCached {
		dotsPull := actions.MustPalette(actions.DotsPull)
		dotsCommit := actions.MustPalette(actions.DotsCommit)
		dotsPush := actions.MustPalette(actions.DotsPush)
		dotsSync := actions.MustPalette(actions.DotsSync)
		cmds = append(cmds,
			palCmd{
				name: paletteCommandName(dotsPull),
				desc: dotsPull.Description,
				run: func(m *Model) tea.Cmd {
					return m.runDotsPaletteCommand("Pulling…", m.doDotsPull)
				},
			},
			palCmd{
				name: paletteCommandName(dotsCommit),
				desc: dotsCommit.Description,
				run: func(m *Model) tea.Cmd {
					return m.runDotsPaletteCommand("Committing dots…", m.doDotsCommit)
				},
			},
			palCmd{
				name: paletteCommandName(dotsPush),
				desc: dotsPush.Description,
				run: func(m *Model) tea.Cmd {
					return m.runDotsPaletteCommand("Pushing…", m.doDotsPush)
				},
			},
			palCmd{
				name: paletteCommandName(dotsSync),
				desc: dotsSync.Description,
				run: func(m *Model) tea.Cmd {
					return m.runDotsPaletteCommand("Syncing dots…", m.doDotsSyncOnly)
				},
			},
		)
	}

	migrateNvmAction := actions.MustPalette(actions.ToolMigrateNvm)
	cmds = append(cmds, palCmd{
		name: paletteCommandName(migrateNvmAction),
		desc: migrateNvmAction.Description,
		run: func(m *Model) tea.Cmd {
			m.mode = viewList
			if !statusDashboardNvmManagedActionable(*m) {
				return setStatus(m, "no nvm-managed system-provider tools", false)
			}
			var batch []tea.Cmd
			m.startDashboardFixNvmManaged(&batch)
			return tea.Batch(batch...)
		},
	})

	cmds = appendAgentsPaletteCommands(m, cmds)

	consolidateAction := actions.MustPalette(actions.ToolConsolidate)
	for _, opt := range m.consolidateOptions {
		opt := opt
		cmds = append(cmds, palCmd{
			name: paletteCommandName(consolidateAction) + " " + opt.Ecosystem + " " + opt.Manager,
			desc: paletteDescription(consolidateAction, opt.Manager, opt.Ecosystem),
			run: func(m *Model) tea.Cmd {
				m.beginLoading(loadingOwnerLocalOp)
				startOp(m, "consolidating "+opt.Ecosystem+" → "+opt.Manager+"…")
				return tea.Batch(m.spinner.Tick, m.doConsolidate(opt.Ecosystem, opt.Manager))
			},
		})
	}

	return cmds
}

func (m *Model) runDotsPaletteCommand(status string, run func() tea.Cmd) tea.Cmd {
	if m.dotsPushRunning {
		return setStatus(m, "dots push in progress — wait for it to finish", false)
	}
	m.mode = viewDots
	if !m.dotsLoaded && !m.dotsLoading {
		m.dotsLoaded = true
	}
	m.beginDotsOperation(status)
	return tea.Batch(m.spinner.Tick, run())
}

func appendAgentsPaletteCommands(m Model, cmds []palCmd) []palCmd {
	restore := actions.MustPalette(actions.AgentsSync)
	return append(cmds,
		palCmd{
			name: paletteCommandName(restore),
			desc: restore.Description,
			run: func(m *Model) tea.Cmd {
				return m.runAgentsPaletteCommand(func(m *Model) []tea.Cmd {
					return m.doAgentsSyncAll()
				})
			},
		},
	)
}

// Refuses while an agents operation is in flight for the same reason the tab's own global actions do: a second bulk run would race the first.
func (m *Model) runAgentsPaletteCommand(run func(*Model) []tea.Cmd) tea.Cmd {
	if m.apmRunning {
		return setStatus(m, "⚠ agents busy — wait for the running operation to finish", true)
	}
	var cmds []tea.Cmd
	m.switchMainTab(viewSkills, &cmds)
	return tea.Batch(append(cmds, run(m)...)...)
}

func paletteCommandName(binding actions.PaletteBinding) string {
	return strings.Join(binding.Command, " ")
}

func paletteDescription(binding actions.PaletteBinding, args ...any) string {
	if binding.DescriptionFormat != "" {
		return fmt.Sprintf(binding.DescriptionFormat, args...)
	}
	return binding.Description
}

// A fully typed name wins outright, so a command whose name is another's prefix ("agents sync" inside "agents sync-all") stays reachable.
func filterPalette(cmds []palCmd, q string) []palCmd {
	if q == "" {
		return cmds
	}
	q = strings.ToLower(q)
	var out []palCmd
	for _, c := range cmds {
		name := strings.ToLower(c.name)
		if name == q {
			return []palCmd{c}
		}
		if strings.Contains(name, q) {
			out = append(out, c)
		}
	}
	return out
}
