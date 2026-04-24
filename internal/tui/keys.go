package tui

import (
	"charm.land/bubbles/v2/key"

	"github.com/lkshrk/omni/internal/actions"
)

// KeyMap defines all keyboard shortcuts for the TUI.
type KeyMap struct {
	// List navigation
	Up           key.Binding
	Down         key.Binding
	Top          key.Binding // home — jump to first item
	Bottom       key.Binding // G   — jump to last item
	HalfPageUp   key.Binding // ctrl+u
	HalfPageDown key.Binding // ctrl+d
	PageUp       key.Binding // pgup, ctrl+b
	PageDown     key.Binding // pgdown, ctrl+f

	// Tool actions
	Install         key.Binding
	Delete          key.Binding // d — delete tool/config entry
	Upgrade         key.Binding
	UpgradeAll      key.Binding
	Sync            key.Binding
	SyncAll         key.Binding // S — install missing and add discovered tools to config
	Claim           key.Binding // c — add orphan tool to config
	Ignore          key.Binding // x — ignore / un-ignore
	MigrateProvider key.Binding // r — reinstall with default provider
	NewProfile      key.Binding // p — new profile
	NewGroup        key.Binding // n — new group
	ProfileGroups   key.Binding // g — edit profile groups
	Rename          key.Binding // r — rename selected profile/group
	EditHosts       key.Binding // h — edit selected profile hosts
	GroupTools      key.Binding // t — edit selected group tools
	GroupDots       key.Binding // f — edit selected group dotfiles

	// UI controls
	Search    key.Binding
	Confirm   key.Binding // enter — confirm / primary action
	Quit      key.Binding
	Back      key.Binding // esc
	Tab       key.Binding // tab / shift+tab — cycle main tabs
	PrevTab   key.Binding // [ — prev provider filter pill
	NextTab   key.Binding // ] — next provider filter pill
	Toggle    key.Binding // space — toggle boolean setting
	Palette   key.Binding // :
	Help      key.Binding // ?
	MoveGroup key.Binding // g — change selected tool's group

	// Tool list actions
	Refresh   key.Binding // R — re-scan all providers and update install status
	GroupPrev key.Binding // { — cycle group filter backward
	GroupNext key.Binding // } — cycle group filter forward

	// Dots tab actions
	DotDiscover key.Binding // D — discover untracked dotfile candidates
	DotPull     key.Binding // p — git pull + resync (command palette only)
	DotDelete   key.Binding // d — delete dots entry (confirm required)
	DotAdd      key.Binding // a — adopt a new path into the dots repo
	DotIgnore   key.Binding // x — add an ignore pattern for the selected entry
	DotUseRepo  key.Binding // u — resolve conflict with repo version
	DotUseLocal key.Binding // l — resolve conflict with local version

	// Out-of-sync actions
	PinProvider key.Binding // p — pin provider scope
}

// ShortHelp implements key.Map. Returns the most-used bindings for the
// compact single-line hint in the status bar.
func (k KeyMap) ShortHelp() []key.Binding {
	return footerBindings(k, []key.Binding{k.UpgradeAll, k.SyncAll, k.Refresh}, []key.Binding{k.Search, footerFilterBinding(k, true)})
}

// FullHelp implements key.Map. Returns all bindings grouped into columns
// for the ? overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Top, k.Bottom, k.HalfPageUp, k.HalfPageDown, k.PageUp, k.PageDown, k.Tab},
		{k.Install, k.Upgrade, k.UpgradeAll, k.SyncAll, k.Claim, k.MoveGroup, k.PinProvider, k.MigrateProvider, k.Ignore, k.Delete, k.Refresh},
		{k.Search, k.PrevTab, k.NextTab, k.GroupPrev, k.GroupNext, k.Palette, k.NewProfile, k.NewGroup, k.ProfileGroups, k.GroupTools, k.Help, k.Quit},
	}
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:   key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("", "")),
		Down: key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("", "")),
		Top: key.NewBinding(
			key.WithKeys("home"),
			key.WithHelp("home", "top"),
		),
		Bottom: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "bottom"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "half page up"),
		),
		HalfPageDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "half page down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "ctrl+b"),
			key.WithHelp("pgup,ctrl+b", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+f"),
			key.WithHelp("pgdown,ctrl+f", "page down"),
		),
		Install: key.NewBinding(
			key.WithKeys("i"),
			key.WithHelp("i", actions.MustLabel(actions.ToolInstall)),
		),
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", actions.MustLabel(actions.ToolDelete)),
		),
		Upgrade: key.NewBinding(
			key.WithKeys("u"),
			key.WithHelp("u", actions.MustLabel(actions.ToolUpdate)),
		),
		UpgradeAll: key.NewBinding(
			key.WithKeys("U"),
			key.WithHelp("U", actions.MustLabel(actions.ToolUpdateAll)),
		),
		Sync: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", actions.MustLabel(actions.DotsSync)),
		),
		SyncAll: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", actions.MustLabel(actions.ToolSyncAll)),
		),
		Claim: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", actions.MustLabel(actions.ToolClaim)),
		),
		Ignore: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("x", actions.MustLabel(actions.ToolIgnore)),
		),
		MigrateProvider: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", actions.MustLabel(actions.ToolReinstallDefault)),
		),
		NewProfile: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", actions.MustLabel(actions.ProfileCreate)),
		),
		NewGroup: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", actions.MustLabel(actions.GroupCreate)),
		),
		ProfileGroups: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", actions.MustLabel(actions.ProfileEditGroups)),
		),
		Rename: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", actions.LabelRename),
		),
		EditHosts: key.NewBinding(
			key.WithKeys("h"),
			key.WithHelp("h", actions.MustLabel(actions.ProfileEditHosts)),
		),
		GroupTools: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", actions.MustLabel(actions.GroupEditTools)),
		),
		GroupDots: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", actions.MustLabel(actions.GroupEditDots)),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab", "shift+tab"),
			key.WithHelp("tab", "switch tab"),
		),
		PrevTab: key.NewBinding(
			key.WithKeys("["),
			key.WithHelp("[,]", "filter"),
		),
		NextTab: key.NewBinding(
			key.WithKeys("]"),
			key.WithHelp("]", "next filter"),
		),
		Toggle: key.NewBinding(
			key.WithKeys("space"),
			key.WithHelp("space", "toggle"),
		),
		Palette: key.NewBinding(
			key.WithKeys(":"),
			key.WithHelp(":", "commands"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		MoveGroup: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", actions.MustLabel(actions.ToolChangeGroup)),
		),
		Refresh: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", actions.MustLabel(actions.ToolRefresh)),
		),
		DotDiscover: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", actions.MustLabel(actions.DotsDiscover)),
		),
		DotPull: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "pull"),
		),
		DotDelete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", actions.MustLabel(actions.DotsDelete)),
		),
		DotAdd: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", actions.MustLabel(actions.DotsAdd)),
		),
		DotIgnore: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("x", actions.MustLabel(actions.DotsIgnore)),
		),
		DotUseRepo: key.NewBinding(
			key.WithKeys("u"),
			key.WithHelp("u", "use repo"),
		),
		DotUseLocal: key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("l", "use local"),
		),
		PinProvider: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", actions.MustLabel(actions.ToolPinProvider)),
		),
		GroupPrev: key.NewBinding(
			key.WithKeys("{"),
			key.WithHelp("{,}", "group filter"),
		),
		GroupNext: key.NewBinding(
			key.WithKeys("}"),
			key.WithHelp("}", ""),
		),
	}
}
