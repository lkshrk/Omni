package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// hintItem pairs a key label and its action description for rendered hints.
type hintItem struct {
	key    string
	desc   string
	danger bool
}

type helpGroup struct {
	title string
	items []hintItem
}

type actionHints struct {
	prefix string
	items  []hintItem
}

type hintContext int

const (
	hintCtxSetupNodeManager hintContext = iota
	hintCtxSettingsToggle
	hintCtxSettingsEdit
	hintCtxSettingsDotsSync
	hintCtxSettingsDanger
	hintCtxSettingsPriorityEdit
	hintCtxProfileGroupPicker
	hintCtxProfileDefault
	hintCtxGroupDefault
	hintCtxDotsDeleteConfirm
	hintCtxDotsRepoConfirm
	hintCtxDotsLocalConfirm
	hintCtxDotsIgnoreConfirm
	hintCtxDotsRow
	hintCtxDotsConflict
	hintCtxFilePickerBrowse
	hintCtxProfileGroupTools
	hintCtxProfileGroupToolsSearch
	hintCtxProfileGroupDots
	hintCtxProfileGroupDotsSearch
)

func rawHint(key, desc string) hintItem {
	return hintItem{key: key, desc: desc}
}

func hintFromBinding(b key.Binding) hintItem {
	h := b.Help()
	return hintItem{key: h.Key, desc: h.Desc}
}

func hintFromBindingDesc(b key.Binding, desc string) hintItem {
	h := b.Help()
	return hintItem{key: h.Key, desc: desc}
}

func dangerHintFromBindingDesc(b key.Binding, desc string) hintItem {
	h := b.Help()
	return hintItem{key: h.Key, desc: desc, danger: true}
}

func dangerRawHint(key, desc string) hintItem {
	return hintItem{key: key, desc: desc, danger: true}
}

// hintKey renders a key+description pair with the key styled like the legend.
func hintKey(pal palette, k, desc string) string {
	return pal.styleTitle.Render(k) + pal.styleHintDesc.Render(" "+desc)
}

func renderHintItem(pal palette, h hintItem) string {
	if h.danger {
		return pal.styleDangerSection.Render(h.key) + pal.styleDangerLabel.Bold(true).Render(" "+h.desc)
	}
	return hintKey(pal, h.key, h.desc)
}

// hintJoin joins pre-rendered hint strings with the same separator used by the
// footer help model.
func hintJoin(pal palette, parts ...string) string {
	sep := pal.styleSep.Render(" • ")
	return strings.Join(parts, sep)
}

func renderActionHints(pal palette, hints actionHints) string {
	if len(hints.items) == 0 {
		return ""
	}
	return hints.prefix + renderActionHintText(pal, hints.items)
}

func renderActionHintText(pal palette, hints []hintItem) string {
	if len(hints) == 0 {
		return ""
	}
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = renderHintItem(pal, h)
	}
	return hintJoin(pal, parts...)
}

// renderInlineHints renders a list of hintItems joined by the footer help
// separator and prefixed with prefix. Returns "" when hints is empty.
func renderInlineHints(pal palette, hints []hintItem, prefix string) string {
	return renderActionHints(pal, actionHints{prefix: prefix, items: hints})
}

func renderHintItems(pal palette, prefix string, hints []hintItem) string {
	return renderActionHints(pal, actionHints{prefix: prefix, items: hints})
}

func renderContextHints(m Model, ctx hintContext, prefix string) string {
	return renderHintItems(m.palette, prefix, contextHintItems(m, ctx))
}

func confirmActionItems(confirm key.Binding, confirmLabel string, cancel key.Binding) []hintItem {
	confirmItem := hintFromBindingDesc(confirm, confirmLabel)
	if isDangerConfirmLabel(confirmLabel) {
		confirmItem.danger = true
		return []hintItem{confirmItem}
	}
	return []hintItem{
		confirmItem,
		hintFromBindingDesc(cancel, "cancel"),
	}
}

func isDangerConfirmLabel(label string) bool {
	label = strings.TrimSpace(label)
	return label == "confirm" ||
		label == "delete" ||
		label == "execute" ||
		strings.HasPrefix(label, "confirm ") ||
		strings.HasPrefix(label, "press ")
}

func toggleSaveCancelActionItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Toggle, "toggle"),
		hintFromBindingDesc(m.keys.Confirm, "save"),
		hintFromBindingDesc(m.keys.Back, "cancel"),
	}
}

func renderConfirmActionHints(m Model, prefix string, confirm key.Binding, confirmLabel string) string {
	return renderActionHints(m.palette, actionHints{
		prefix: prefix,
		items:  confirmActionItems(confirm, confirmLabel, m.keys.Back),
	})
}

func confirmActionHintText(m Model, confirm key.Binding, confirmLabel string) string {
	return renderActionHintText(m.palette, confirmActionItems(confirm, confirmLabel, m.keys.Back))
}

func toggleSaveCancelHintText(m Model) string {
	return renderActionHintText(m.palette, toggleSaveCancelActionItems(m))
}

func renderPressAgainActionHint(pal palette, prefix, keyLabel, action string) string {
	return renderActionHints(pal, actionHints{
		prefix: prefix,
		items:  []hintItem{dangerRawHint(keyLabel, "press "+keyLabel+" again to "+action)},
	})
}

func pressAgainBinding(b key.Binding, action string) key.Binding {
	h := b.Help()
	return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(h.Key, "press "+h.Key+" again to "+action))
}

func contextHintItems(m Model, ctx hintContext) []hintItem {
	switch ctx {
	case hintCtxSetupNodeManager:
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.Confirm, "confirm"),
			hintFromBindingDesc(m.keys.Back, "skip"),
		}
	case hintCtxSettingsToggle:
		return []hintItem{
			hintFromBindingDesc(m.keys.Toggle, "change"),
		}
	case hintCtxSettingsEdit:
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, "edit"),
		}
	case hintCtxSettingsDotsSync:
		desc := "disable"
		if config.BoolVal(m.settings.DotsDisabled) {
			desc = "enable"
		}
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.Confirm, desc),
		}
	case hintCtxSettingsDanger:
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.Confirm, "confirm"),
		}
	case hintCtxSettingsPriorityEdit:
		return []hintItem{
			rawHint("K", "up"),
			rawHint("J", "down"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxProfileGroupPicker:
		return []hintItem{
			hintFromBindingDesc(m.keys.Toggle, "toggle"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxProfileDefault:
		return []hintItem{
			hintFromBindingDesc(m.keys.Toggle, "activate profile"),
			hintFromBinding(m.keys.Rename),
			hintFromBinding(m.keys.ProfileGroups),
			hintFromBinding(m.keys.EditHosts),
			hintFromBinding(m.keys.Delete),
		}
	case hintCtxGroupDefault:
		hints := []hintItem{}
		if m.selectedProfileGroupName() != "base" {
			hints = append(hints,
				hintFromBinding(m.keys.Rename),
			)
		}
		hints = append(hints,
			hintFromBinding(m.keys.GroupTools),
			hintFromBindingDesc(m.keys.GroupDots, "edit dotfiles"),
		)
		if m.selectedProfileGroupName() != "base" {
			hints = append(hints, hintFromBinding(m.keys.Delete))
		}
		return hints
	case hintCtxDotsDeleteConfirm:
		return []hintItem{
			dangerRawHint("y", "yes"),
			dangerRawHint("n", "no"),
		}
	case hintCtxDotsRepoConfirm:
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.DotUseRepo, "confirm use repo"),
		}
	case hintCtxDotsLocalConfirm:
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.DotUseLocal, "confirm use local"),
		}
	case hintCtxDotsIgnoreConfirm:
		return dotsIgnoreConfirmHintItems(m)
	case hintCtxDotsRow:
		return dotsRowHintItems(m)
	case hintCtxDotsConflict:
		return dotsConflictHintItems(m)
	case hintCtxFilePickerBrowse:
		return []hintItem{
			rawHint("tab", "complete"),
			rawHint("bs", "parent"),
			hintFromBindingDesc(m.keys.Confirm, "pick"),
			hintFromBindingDesc(m.keys.Back, "close"),
		}
	case hintCtxProfileGroupTools:
		return []hintItem{
			hintFromBindingDesc(m.keys.Search, "search"),
			hintFromBindingDesc(footerFilterBinding(m.keys, false), "filter"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxProfileGroupToolsSearch:
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, "apply"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxProfileGroupDots:
		return []hintItem{
			hintFromBindingDesc(m.keys.Search, "search"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxProfileGroupDotsSearch:
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, "apply"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	default:
		return nil
	}
}

func dotsRowHintItems(m Model) []hintItem {
	visible := dotsVisibleRows(m)
	if m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return nil
	}
	row := visible[m.dotsCursor]
	entry := row.entry
	hints := make([]hintItem, 0, 4)
	if len(entry.Children) > 0 && !row.isChild {
		desc := "expand"
		if m.dotsExpandedName == entry.Name {
			desc = "collapse"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.Toggle, desc))
	}
	if !row.isChild && dotHasAction(entry, app.DotActionSync) {
		hints = append(hints, hintFromBindingDesc(m.keys.Sync, dotsSyncHintDesc(dotStatusState(entry))))
	}
	if !row.isChild && len(m.dotMemberships[entry.Name]) > 0 {
		hints = append(hints, hintFromBindingDesc(m.keys.MoveGroup, "edit groups"))
	}
	if row.isChild && dotHasAction(entry, app.DotActionIgnore) {
		desc := "ignore"
		if row.child.Ignored {
			desc = "include"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.DotIgnore, desc))
	} else if !row.isChild && (dotHasAction(entry, app.DotActionIgnore) || dotHasAction(entry, app.DotActionUnignore)) {
		desc := "ignore"
		if dotStatusState(entry) == app.DotStateIgnored {
			desc = "include"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.DotIgnore, desc))
	}
	if !row.isChild && dotHasAction(entry, app.DotActionRemove) {
		hints = append(hints, hintFromBinding(m.keys.DotDelete))
	}
	return hints
}

func dotsConflictHintItems(m Model) []hintItem {
	visible := dotsVisibleRows(m)
	if m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return nil
	}
	entry := visible[m.dotsCursor].entry
	hints := make([]hintItem, 0, 4)
	if dotHasAction(entry, app.DotActionUseRepo) {
		hints = append(hints, hintFromBindingDesc(m.keys.DotUseRepo, "use repo"))
	}
	if dotHasAction(entry, app.DotActionUseLocal) {
		hints = append(hints, hintFromBindingDesc(m.keys.DotUseLocal, "use local"))
	}
	if dotHasAction(entry, app.DotActionIgnore) || dotHasAction(entry, app.DotActionUnignore) {
		desc := "ignore"
		if dotStatusState(entry) == app.DotStateIgnored {
			desc = "include"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.DotIgnore, desc))
	}
	if dotHasAction(entry, app.DotActionRemove) {
		hints = append(hints, hintFromBinding(m.keys.DotDelete))
	}
	return hints
}

func dotsSyncHintDesc(state app.DotState) string {
	switch state {
	case app.DotStateMissing:
		return "use repo"
	case app.DotStateBroken:
		return "repair"
	case app.DotStateLocalOnly:
		return "use local"
	case app.DotStateRepoOnly:
		return "use repo"
	default:
		return "sync"
	}
}

func dotsIgnoreConfirmHintItems(m Model) []hintItem {
	visible := dotsVisibleRows(m)
	desc := "confirm ignore"
	if m.dotsCursor >= 0 && m.dotsCursor < len(visible) {
		row := visible[m.dotsCursor]
		if row.child.Ignored || dotStatusState(row.entry) == app.DotStateIgnored {
			desc = "confirm include"
		}
	}
	return confirmActionItems(m.keys.DotIgnore, desc, m.keys.Back)
}

func toolInlineHints(m Model, t *database.ToolCache) []hintItem {
	if t == nil {
		return nil
	}
	ss := m.syncStatusOf(t)
	isIgnored := m.displaySection(t) == sectionIgnored
	var hints []hintItem

	if !isIgnored && t.Installed && t.Outdated {
		hints = append(hints, hintFromBinding(m.keys.Upgrade))
	}

	var showGroup, showIgnore, showDelete bool
	switch {
	case ss == syncOrphan && t.Installed:
		hints = append(hints, hintFromBinding(m.keys.Claim))
		showDelete = true
	case ss == syncWrongProv:
		hints = append(hints, hintFromBinding(m.keys.PinProvider))
		hints = append(hints, hintFromBinding(m.keys.MigrateProvider))
		showGroup = true
		showIgnore = true
		showDelete = true
	case isIgnored:
		hints = append(hints, hintFromBindingDesc(m.keys.Ignore, "edit ignore"))
	case !t.Installed:
		hints = append(hints, hintFromBinding(m.keys.Install))
		if t.Tracked {
			showDelete = true
		}
		showGroup = ss != syncOrphan
		showIgnore = true
	default:
		showGroup = true
		showIgnore = true
		showDelete = true
	}

	if showGroup {
		hints = append(hints, hintFromBinding(m.keys.MoveGroup))
	}
	if showIgnore {
		hints = append(hints, hintFromBinding(m.keys.Ignore))
	}
	if showDelete {
		hints = append(hints, hintFromBinding(m.keys.Delete))
	}
	return hints
}

func tabShortHelpBindings(m *Model) []key.Binding {
	if m.suppressFooterHints || rowConfirmationActive(*m) {
		return nil
	}
	k := m.keys
	switch m.mode {
	case viewDots:
		return footerBindings(k, []key.Binding{k.DotAdd, k.DotDiscover, k.SyncAll}, []key.Binding{k.Search})
	case viewSettings:
		return footerBindings(k, nil, nil)
	case viewProfiles:
		actions := []key.Binding{k.NewProfile, k.NewGroup}
		return footerBindings(k, actions, nil)
	default:
		if m.listConfirm.action == listConfirmSyncAll {
			return footerBindings(k, []key.Binding{pressAgainBinding(k.SyncAll, "sync all")}, nil)
		}
		filters := []key.Binding{k.Search, footerFilterBinding(k, true)}
		return footerBindings(k, []key.Binding{k.UpgradeAll, k.SyncAll, k.Refresh}, filters)
	}
}

func rowConfirmationActive(m Model) bool {
	if m.listConfirm.action != "" && m.listConfirm.action != listConfirmSyncAll {
		return true
	}
	return m.profileDeleteConfirm ||
		m.groupDeleteConfirm ||
		m.dangerConfirmRow >= 0 ||
		dotsConfirmationActive(m)
}

func dotsConfirmationActive(m Model) bool {
	return m.dotsConfirmIdx >= 0 ||
		m.dotsOverwriteIdx >= 0 ||
		m.dotsLocalIdx >= 0 ||
		m.dotsIgnoreIdx >= 0
}

func footerBindings(k KeyMap, actions, filters []key.Binding) []key.Binding {
	bindings := make([]key.Binding, 0, len(actions)+len(filters)+3)
	bindings = append(bindings, actions...)
	bindings = append(bindings, filters...)
	bindings = append(bindings, k.Tab, k.Help, k.Quit)
	return bindings
}

func footerFilterBinding(k KeyMap, includeGroup bool) key.Binding {
	providerHelp := k.PrevTab.Help()
	labels := []string{compactFilterLabel(providerHelp.Key)}
	keys := append([]string{}, k.PrevTab.Keys()...)
	if includeGroup {
		labels = append(labels, compactFilterLabel(k.GroupPrev.Help().Key))
		keys = append(keys, k.GroupPrev.Keys()...)
	}
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(strings.Join(labels, ","), providerHelp.Desc))
}

func compactFilterLabel(label string) string {
	return strings.ReplaceAll(label, ",", "")
}

func tabFullHelpBindings(m *Model) [][]key.Binding {
	k := m.keys
	common := []key.Binding{k.Top, k.Bottom, k.HalfPageUp, k.HalfPageDown, k.PageUp, k.PageDown, k.Tab, k.Search, k.Palette, k.Help, k.Quit}
	switch m.mode {
	case viewDots:
		return [][]key.Binding{
			common,
			{k.SyncAll, k.DotDiscover, k.DotAdd, k.Sync, k.DotUseRepo, k.DotUseLocal, k.DotDelete, k.DotIgnore, k.Back},
		}
	case viewSettings:
		return [][]key.Binding{
			common,
			{k.Toggle, k.Confirm, k.Back, k.GroupPrev, k.GroupNext},
		}
	case viewProfiles:
		return [][]key.Binding{
			common,
			{k.NewProfile, k.NewGroup, k.ProfileGroups, k.GroupTools, k.GroupDots, k.Toggle, k.Back},
		}
	default:
		return [][]key.Binding{
			common,
			{k.Install, k.Upgrade, k.UpgradeAll, k.SyncAll, k.Delete, k.Claim, k.Ignore, k.MoveGroup, k.PinProvider, k.MigrateProvider, k.Refresh},
			{k.PrevTab, k.NextTab, k.GroupPrev, k.GroupNext},
		}
	}
}

func renderHelpPopup(m Model) string {
	return renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
}

func renderHelpPopupWithWidth(m Model, width int) string {
	p := m.palette
	width = max(width, 1)
	if items := activeConfirmationHelpItems(m); len(items) > 0 {
		return renderHelpGroups(p, "Current Confirmation", []helpGroup{{items: items}}, width)
	}
	return strings.Join([]string{
		renderHelpGroups(p, "Current Tab Actions", helpActionGroups(m), width),
		renderHelpSection(p, "Navigation", helpGlobalItems(m), width),
		renderHelpLegend(m, width),
	}, "\n\n")
}

func helpPopupContentWidth(m Model) int {
	return popupContentWidth(m, min(max(m.width-28, 52), 74), 52, 74)
}

func activeConfirmationHelpItems(m Model) []hintItem {
	switch {
	case m.confirmQuit:
		keyLabel := m.quitConfirmKey
		if keyLabel == "" {
			keyLabel = "q"
		}
		return []hintItem{dangerRawHint(keyLabel, "press "+keyLabel+" again to quit")}
	case m.listConfirm.action == listConfirmSyncAll:
		keyLabel := m.keys.SyncAll.Help().Key
		return []hintItem{dangerRawHint(keyLabel, "press "+keyLabel+" again to sync all")}
	case m.listConfirm.action == listConfirmDelete:
		return confirmActionItems(m.keys.Delete, actions.MustConfirmDescription(actions.ToolDelete), m.keys.Back)
	case m.listConfirm.action == listConfirmReinstallDefault:
		return confirmActionItems(m.keys.MigrateProvider, actions.MustConfirmDescription(actions.ToolReinstallDefault), m.keys.Back)
	case m.dotsConfirmIdx >= 0:
		return contextHintItems(m, hintCtxDotsDeleteConfirm)
	case m.dotsOverwriteIdx >= 0:
		return contextHintItems(m, hintCtxDotsRepoConfirm)
	case m.dotsLocalIdx >= 0:
		return contextHintItems(m, hintCtxDotsLocalConfirm)
	case m.dotsIgnoreIdx >= 0:
		return contextHintItems(m, hintCtxDotsIgnoreConfirm)
	case m.dangerConfirmRow == settingsRowDotsSync:
		return contextHintItems(m, hintCtxDotsDeleteConfirm)
	case m.dangerConfirmRow >= 0:
		return contextHintItems(m, hintCtxSettingsDanger)
	case m.profileDeleteConfirm:
		return []hintItem{dangerRawHint("d", "press d again to confirm delete")}
	case m.groupDeleteConfirm:
		return confirmActionItems(m.keys.Confirm, actions.MustConfirmDescription(actions.GroupDelete), m.keys.Back)
	default:
		return nil
	}
}

func renderHelpGroups(p palette, title string, groups []helpGroup, width int) string {
	var sb strings.Builder
	sb.WriteString(p.styleTitle.Render(title))
	for _, group := range groups {
		if group.title != "" {
			sb.WriteString("\n")
			sb.WriteString(p.styleHelp.Render("  " + group.title))
		}
		body := renderHelpRows(p, group.items, width)
		if body != "" {
			sb.WriteByte('\n')
			sb.WriteString(body)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderHelpSection(p palette, title string, hints []hintItem, width int) string {
	body := renderHelpRows(p, hints, width)
	if body == "" {
		body = p.styleHelp.Render("  none")
	}
	return p.styleTitle.Render(title) + "\n" + body
}

func renderHelpRows(p palette, hints []hintItem, width int) string {
	if len(hints) == 0 {
		return ""
	}
	keyW := 0
	for _, h := range hints {
		keyW = max(keyW, lipgloss.Width(h.key))
	}
	keyW = min(max(keyW, 4), 12)
	descW := max(width-keyW-4, 24)

	var sb strings.Builder
	for _, h := range hints {
		keyStyle := p.styleTitle
		descStyle := p.styleHelp
		if h.danger {
			keyStyle = p.styleDangerSection
			descStyle = p.styleDangerLabel.Bold(true)
		}
		keyText := keyStyle.Render(fmt.Sprintf("  %-*s", keyW, h.key))
		desc := h.desc
		if lipgloss.Width(desc) > descW {
			desc = truncatePath(desc, descW)
		}
		sb.WriteString(keyText)
		sb.WriteString("  ")
		sb.WriteString(descStyle.Render(desc))
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

func helpActionGroups(m Model) []helpGroup {
	k := m.keys
	switch m.mode {
	case viewDots:
		return []helpGroup{{items: []hintItem{
			hintFromBindingDesc(k.DotAdd, actions.MustLabel(actions.DotsAdd)),
			hintFromBindingDesc(k.DotDiscover, actions.MustLabel(actions.DotsDiscover)),
			hintFromBindingDesc(k.SyncAll, actions.MustLabel(actions.ToolSyncAll)),
			hintFromBindingDesc(k.Sync, actions.MustLabel(actions.DotsSync)),
			hintFromBindingDesc(k.DotUseRepo, "use repo"),
			hintFromBindingDesc(k.DotUseLocal, "use local"),
			hintFromBindingDesc(k.MoveGroup, actions.MustLabel(actions.DotsEditGroups)),
			hintFromBindingDesc(k.DotIgnore, actions.MustLabel(actions.DotsIgnore)),
			hintFromBindingDesc(k.DotDelete, actions.MustLabel(actions.DotsDelete)),
		}}}
	case viewSettings:
		return []helpGroup{{items: []hintItem{
			hintFromBindingDesc(k.Toggle, "change toggle or option"),
			hintFromBindingDesc(k.Confirm, "edit or save expanded setting"),
			rawHint("K/J", "move system priority"),
		}}}
	case viewProfiles:
		return []helpGroup{{items: []hintItem{
			hintFromBindingDesc(k.NewProfile, actions.MustLabel(actions.ProfileCreate)),
			hintFromBindingDesc(k.NewGroup, actions.MustLabel(actions.GroupCreate)),
			hintFromBindingDesc(k.Toggle, "activate profile"),
			hintFromBindingDesc(k.Rename, actions.LabelRename),
			hintFromBindingDesc(k.ProfileGroups, actions.MustLabel(actions.ProfileEditGroups)),
			hintFromBindingDesc(k.EditHosts, actions.MustLabel(actions.ProfileEditHosts)),
			hintFromBindingDesc(k.GroupTools, actions.MustLabel(actions.GroupEditTools)),
			hintFromBindingDesc(k.GroupDots, "edit dotfiles"),
			hintFromBindingDesc(k.Delete, actions.LabelDelete),
		}}}
	default:
		return []helpGroup{
			{title: "Row", items: []hintItem{
				hintFromBindingDesc(k.Install, actions.MustLabel(actions.ToolInstall)),
				hintFromBindingDesc(k.Upgrade, actions.MustLabel(actions.ToolUpdate)),
				hintFromBindingDesc(k.Claim, actions.MustLabel(actions.ToolClaim)),
				hintFromBindingDesc(k.PinProvider, actions.MustLabel(actions.ToolPinProvider)),
				hintFromBindingDesc(k.MigrateProvider, actions.MustLabel(actions.ToolReinstallDefault)),
				hintFromBindingDesc(k.MoveGroup, actions.MustLabel(actions.ToolChangeGroup)),
				hintFromBindingDesc(k.Ignore, actions.MustLabel(actions.ToolIgnore)),
				hintFromBindingDesc(k.Delete, actions.MustLabel(actions.ToolDelete)),
			}},
			{title: "Bulk", items: []hintItem{
				hintFromBindingDesc(k.UpgradeAll, "upgrade all outdated tools"),
				hintFromBindingDesc(k.SyncAll, "add discovered and install missing"),
				hintFromBindingDesc(k.Refresh, "rescan installed and outdated state"),
			}},
		}
	}
}

func helpGlobalItems(m Model) []hintItem {
	k := m.keys
	items := []hintItem{
		hintFromBindingDesc(k.Search, "search current tab"),
		hintFromBindingDesc(footerFilterBinding(k, true), "cycle provider/group filters"),
		hintFromBindingDesc(k.Palette, "open command palette"),
		hintFromBindingDesc(k.Tab, "switch tab"),
		hintFromBindingDesc(k.Back, "close"),
		hintFromBindingDesc(k.Help, "toggle help"),
		hintFromBindingDesc(k.Quit, "quit omni"),
	}
	if !isToolsHelpMode(m.mode) {
		items = append(items[:1], items[2:]...)
	}
	return items
}

func isToolsHelpMode(mode viewMode) bool {
	return mode == viewList || mode == viewSearch || mode == viewCommand || mode == viewGroupPicker || mode == viewGroupMembership || mode == viewProfileGroupTools || mode == viewProfileGroupDots || mode == viewIgnoreScope || mode == viewProviderScope || mode == viewSetup
}

func renderHelpLegend(m Model, width int) string {
	p := m.palette
	items := helpLegendItems(m)
	divider := p.styleHelp.Render(strings.Repeat("─", width))
	if len(items) == 0 {
		return divider + "\n" + lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(p.styleHelp.Render("none"))
	}
	legend := strings.Join(items, p.styleHelp.Render("  "))
	return divider + "\n" + lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(legend)
}

func helpLegendItems(m Model) []string {
	p := m.palette
	switch m.mode {
	case viewDots:
		return []string{
			p.styleInstalled.Render("✓") + p.styleHelp.Render(" ok"),
			p.styleMissing.Render("✗") + p.styleHelp.Render(" missing"),
			p.styleOutdated.Render("!") + p.styleHelp.Render(" conflict"),
			p.styleHelp.Render("· no source"),
		}
	case viewSettings:
		return []string{
			p.styleInstalled.Render("[ON]") + p.styleHelp.Render(" enabled"),
			p.styleMissing.Render("[OFF]") + p.styleHelp.Render(" disabled"),
			p.styleMissing.Render("⚠") + p.styleHelp.Render(" confirm"),
		}
	case viewProfiles:
		return []string{
			p.styleInstalled.Render("*") + p.styleHelp.Render(" active profile"),
			p.styleMissing.Render("⚠") + p.styleHelp.Render(" confirm"),
		}
	default:
		return []string{
			p.styleInstalled.Render(iconInstalled) + p.styleHelp.Render(" installed"),
			p.styleMissing.Render(iconMissing) + p.styleHelp.Render(" missing"),
			p.styleOutdated.Render(iconOutdated) + p.styleHelp.Render(" update"),
			p.styleOrphan.Render(iconOrphan) + p.styleHelp.Render(" orphan"),
			p.styleWrongProv.Render(iconWrongProv) + p.styleHelp.Render(" wrong provider"),
			p.styleIgnored.Render(iconIgnored) + p.styleHelp.Render(" ignored"),
		}
	}
}

func helpPopupTitle(m Model) string {
	switch m.mode {
	case viewDots:
		return "Dots Help"
	case viewSettings:
		return "Settings Help"
	case viewProfiles:
		return "Profiles Help"
	default:
		return "Tools Help"
	}
}
