package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/actions"
)

// palCmd is one entry in the command palette.
type palCmd struct {
	name string // what the user types
	desc string // shown in the suggestion list
	run  func(m *Model) tea.Cmd
}

// buildPalette returns the full set of available commands for the current model
// state. Called on every keystroke in palette mode — must be free of IO.
func buildPalette(m Model) []palCmd {
	syncAction := actions.MustPalette(actions.ToolSync)
	cmds := []palCmd{
		{
			name: paletteCommandName(syncAction),
			desc: syncAction.Description,
			run: func(m *Model) tea.Cmd {
				m.loading = true
				m.progressText = ""
				ch, gen := m.beginProgressStream()
				m.markBulkPendingSync()
				return tea.Batch(m.spinner.Tick, m.doSyncWithProgress(ch, gen), waitForProgress(ch, gen))
			},
		},
	}

	if m.settings.DotsRepo != "" {
		dotsPull := actions.MustPalette(actions.DotsPull)
		dotsPush := actions.MustPalette(actions.DotsPush)
		dotsSync := actions.MustPalette(actions.DotsSync)
		cmds = append(cmds,
			palCmd{
				name: paletteCommandName(dotsPull),
				desc: dotsPull.Description,
				run: func(m *Model) tea.Cmd {
					m.mode = viewDots
					if !m.dotsLoaded && !m.dotsLoading {
						m.dotsLoaded = true
					}
					m.beginDotsOperation("Pulling…")
					return tea.Batch(m.spinner.Tick, m.doDotsPull())
				},
			},
			palCmd{
				name: paletteCommandName(dotsPush),
				desc: dotsPush.Description,
				run: func(m *Model) tea.Cmd {
					m.mode = viewDots
					if !m.dotsLoaded && !m.dotsLoading {
						m.dotsLoaded = true
					}
					m.beginDotsOperation("Pushing…")
					return tea.Batch(m.spinner.Tick, m.doDotsPush())
				},
			},
			palCmd{
				name: paletteCommandName(dotsSync),
				desc: dotsSync.Description,
				run: func(m *Model) tea.Cmd {
					m.mode = viewDots
					if !m.dotsLoaded && !m.dotsLoading {
						m.dotsLoaded = true
					}
					m.beginDotsOperation("Syncing dots…")
					return tea.Batch(m.spinner.Tick, m.doDotsSyncOnly())
				},
			},
		)
	}

	consolidateAction := actions.MustPalette(actions.ToolConsolidate)
	for _, opt := range m.consolidateOptions {
		opt := opt
		cmds = append(cmds, palCmd{
			name: paletteCommandName(consolidateAction) + " " + opt.Ecosystem + " " + opt.Manager,
			desc: paletteDescription(consolidateAction, opt.Manager, opt.Ecosystem),
			run: func(m *Model) tea.Cmd {
				m.loading = true
				startOp(m, "consolidating "+opt.Ecosystem+" → "+opt.Manager+"…")
				return tea.Batch(m.spinner.Tick, m.doConsolidate(opt.Ecosystem, opt.Manager))
			},
		})
	}

	return cmds
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

// filterPalette returns the subset of cmds whose name has q as a prefix.
// Returns all cmds when q is empty.
func filterPalette(cmds []palCmd, q string) []palCmd {
	if q == "" {
		return cmds
	}
	q = strings.ToLower(q)
	var out []palCmd
	for _, c := range cmds {
		if strings.Contains(strings.ToLower(c.name), q) {
			out = append(out, c)
		}
	}
	return out
}
