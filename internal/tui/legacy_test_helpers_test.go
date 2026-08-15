package tui

import (
	"context"
	"path/filepath"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func agentsAllModel(skills []app.SkillPackageRow, mcpRows []app.McpServerRow, pluginRows []app.PluginRow) Model {
	m := baseModel(nil)
	m.pluginFormName, m.pluginFormMarketplace, m.pluginFormSource, m.pluginFormAgents = textinput.New(), textinput.New(), textinput.New(), textinput.New()
	m.mode, m.agentsEnabled, m.skillTypeIdx, m.width = viewSkills, true, agentsChipAll, 120
	m.skillsLoaded, m.mcpLoaded, m.pluginLoaded = true, true, true
	m.skillsRowsKnown, m.mcpRowsKnown, m.pluginRowsKnown, m.marketplaceRowsKnown = true, true, true, true
	m.skillsRows, m.mcpRows, m.pluginRows, m.enabledAgents = skills, mcpRows, pluginRows, []string{"claude"}
	return m
}

func setupImportModel(t *testing.T, adapters ...app.McpAdapter) Model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, app.WithMcpAdapters(adapters))
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := baseModel(nil)
	m.app, m.ctx, m.mode, m.setupStep = a, context.Background(), viewSetup, 4
	return m
}

func runBatchCmd(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, sub := range batch {
			if sub != nil {
				out = append(out, sub())
			}
		}
		return out
	}
	return []tea.Msg{msg}
}
