package tui

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestActionCatalogMatchesDefaultKeyMap(t *testing.T) {
	keys := DefaultKeyMap()
	value := reflect.ValueOf(keys)
	fieldRefs := map[string]int{}
	for _, action := range actions.All() {
		if action.TUI != nil {
			fieldRefs[action.TUI.KeyMapField]++
		}
	}

	for _, action := range actions.All() {
		if action.TUI == nil {
			continue
		}
		field := value.FieldByName(action.TUI.KeyMapField)
		if !field.IsValid() {
			t.Fatalf("%s references missing KeyMap field %q", action.ID, action.TUI.KeyMapField)
		}
		binding, ok := field.Interface().(key.Binding)
		if !ok {
			t.Fatalf("%s KeyMap field %q is not key-like", action.ID, action.TUI.KeyMapField)
		}
		help := binding.Help()
		if help.Key != action.TUI.DefaultKey {
			t.Fatalf("%s help key = %q, want %q", action.ID, help.Key, action.TUI.DefaultKey)
		}
		if fieldRefs[action.TUI.KeyMapField] == 1 && help.Desc != actions.MustTUILabel(action.ID) {
			t.Fatalf("%s help desc = %q, want %q", action.ID, help.Desc, actions.MustTUILabel(action.ID))
		}
	}
}

func TestActionCatalogTUIBindingsExist(t *testing.T) {
	keys := DefaultKeyMap()
	value := reflect.ValueOf(keys)

	for _, action := range actions.All() {
		if action.TUI == nil {
			continue
		}
		field := value.FieldByName(action.TUI.KeyMapField)
		if !field.IsValid() {
			t.Fatalf("%s references missing KeyMap field %q", action.ID, action.TUI.KeyMapField)
		}
		binding, ok := field.Interface().(key.Binding)
		if !ok {
			t.Fatalf("%s KeyMap field %q is not key-like", action.ID, action.TUI.KeyMapField)
		}
		if got := binding.Help().Key; got != action.TUI.DefaultKey {
			t.Fatalf("%s help key = %q, want %q", action.ID, got, action.TUI.DefaultKey)
		}
	}
}

func TestCanonicalActionKeys(t *testing.T) {
	for _, id := range []actions.ID{
		actions.ToolDelete,
		actions.ToolDeleteSpec,
		actions.DotsDelete,
		actions.GroupDelete,
		actions.HostDelete,
	} {
		action, ok := actions.Get(id)
		if !ok {
			t.Fatalf("missing action %s", id)
		}
		if action.TUI == nil || action.TUI.DefaultKey != "d" {
			t.Fatalf("%s delete-like TUI key = %+v, want d", id, action.TUI)
		}
		if !strings.Contains(action.Label, actions.LabelDelete) {
			t.Fatalf("%s label = %q, want delete wording", id, action.Label)
		}
	}
	for _, id := range []actions.ID{actions.ToolIgnore, actions.DotsIgnore} {
		action, ok := actions.Get(id)
		if !ok {
			t.Fatalf("missing action %s", id)
		}
		if action.TUI == nil || action.TUI.DefaultKey != "x" {
			t.Fatalf("%s ignore TUI key = %+v, want x", id, action.TUI)
		}
	}
	if refresh := actions.MustTUILabel(actions.DotsRefresh); refresh != "refresh" {
		t.Fatalf("dots refresh label = %q, want refresh", refresh)
	}
	if action, _ := actions.Get(actions.DotsRefresh); action.TUI == nil || action.TUI.DefaultKey == "d" {
		t.Fatalf("dots refresh must not use d because d is reserved for delete: %+v", action.TUI)
	}
}

func TestDurableTUIActionsAreCataloged(t *testing.T) {
	for _, id := range []actions.ID{
		actions.ToolInstall,
		actions.ToolDelete,
		actions.ToolUpdate,
		actions.ToolUpdateAll,
		actions.ToolSyncAll,
		actions.ToolClaim,
		actions.ToolIgnore,
		actions.ToolChangeGroup,
		actions.ToolPinProvider,
		actions.ToolReinstallDefault,
		actions.ToolRefresh,
		actions.DotsSync,
		actions.DotsRefresh,
		actions.DotsAdd,
		actions.DotsEditGroups,
		actions.DotsVariant,
		actions.DotsDelete,
		actions.DotsResolveUseRepo,
		actions.DotsResolveUseLocal,
		actions.DotsIgnore,
		actions.DotsEnable,
		actions.DotsDisable,
		actions.DotsReminder,
		actions.DotsWatch,
		actions.GroupCreate,
		actions.GroupRename,
		actions.GroupDelete,
		actions.GroupEditTools,
		actions.GroupEditDots,
		actions.HostDelete,
		actions.HostEditGroups,
		actions.SettingsSet,
		actions.SettingsProvider,
		actions.SettingsReset,
		actions.SettingsResetCache,
		actions.SetupInit,
	} {
		action, ok := actions.Get(id)
		if !ok {
			t.Fatalf("missing TUI durable action %s", id)
		}
		if action.TUI == nil {
			t.Fatalf("TUI durable action %s has no TUI binding", id)
		}
	}
}

func TestPaletteActionCatalogCommandsExist(t *testing.T) {
	m, _ := newDotsModelForCmds(t)
	m.consolidateOptions = []app.EcosystemMigration{{Ecosystem: "node", Manager: "bun"}}
	got := map[string]string{}
	for _, cmd := range buildPalette(m) {
		got[cmd.name] = cmd.desc
	}

	for _, id := range []actions.ID{actions.ToolSync, actions.ToolMigrateNvm, actions.DotsPull, actions.DotsPush, actions.DotsSync} {
		pal := actions.MustPalette(id)
		name := paletteCommandName(pal)
		if got[name] != pal.Description {
			t.Fatalf("%s palette entry = %q, want %q for %q", id, got[name], pal.Description, name)
		}
	}
	for _, action := range actions.All() {
		if !action.PaletteEligible || action.Palette == nil || action.Palette.DescriptionFormat != "" {
			continue
		}
		name := paletteCommandName(*action.Palette)
		if got[name] != action.Palette.Description {
			t.Fatalf("%s palette entry = %q, want %q for %q", action.ID, got[name], action.Palette.Description, name)
		}
	}
	consolidate := paletteCommandName(actions.MustPalette(actions.ToolConsolidate)) + " node bun"
	if got[consolidate] != paletteDescription(actions.MustPalette(actions.ToolConsolidate), "bun", "node") {
		t.Fatalf("missing dynamic consolidate palette entry %q", consolidate)
	}
}

func TestBuildPalette_UsesAppBackedDotsConfiguredState(t *testing.T) {
	t.Run("includes dots commands when app is configured despite stale local settings", func(t *testing.T) {
		m, _ := newDotsModelForCmds(t)
		m.settings = config.Settings{}

		got := paletteNames(buildPalette(m))

		for _, id := range []actions.ID{actions.DotsPull, actions.DotsCommit, actions.DotsPush, actions.DotsSync} {
			name := paletteCommandName(actions.MustPalette(id))
			if !got[name] {
				t.Fatalf("missing palette command %q", name)
			}
		}
	})

	t.Run("omits dots commands when app is unconfigured despite stale local settings", func(t *testing.T) {
		m := modelForCmds(newCmdAppNoConfig(t, &okProvider{name: "brew"}))
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}

		got := paletteNames(buildPalette(m))

		for _, id := range []actions.ID{actions.DotsPull, actions.DotsCommit, actions.DotsPush, actions.DotsSync} {
			name := paletteCommandName(actions.MustPalette(id))
			if got[name] {
				t.Fatalf("unexpected palette command %q", name)
			}
		}
	})
}

func paletteNames(cmds []palCmd) map[string]bool {
	out := make(map[string]bool, len(cmds))
	for _, cmd := range cmds {
		out[cmd.name] = true
	}
	return out
}

func toolActionTUIKey(t *testing.T, id actions.ID) string {
	t.Helper()
	action, ok := actions.Get(id)
	if !ok {
		t.Fatalf("unknown action %s", id)
	}
	if action.TUI == nil {
		t.Fatalf("%s has no TUI binding", id)
	}
	return action.TUI.DefaultKey
}

func toolActionConfirmDesc(t *testing.T, id actions.ID) string {
	t.Helper()
	action, ok := actions.Get(id)
	if !ok {
		t.Fatalf("unknown action %s", id)
	}
	if action.ConfirmDescription == "" {
		t.Fatalf("%s has no confirmation description", id)
	}
	return actions.MustTUIConfirmDescription(id)
}
