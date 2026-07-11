package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
)

// hintItem pairs a key label and its action description for rendered hints.
type hintItem struct {
	key        string
	desc       string
	danger     bool
	pressAgain bool
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
	hintCtxSettingsToggle hintContext = iota
	hintCtxSettingsEdit
	hintCtxSettingsDotsSync
	hintCtxSettingsAgents
	hintCtxSettingsDuration
	hintCtxSettingsDurationEdit
	hintCtxSettingsDanger
	hintCtxSettingsPriorityEdit
	hintCtxSettingsPriorityHold
	hintCtxSettingsStatus
	hintCtxHostGroupPicker
	hintCtxHostDefault
	hintCtxGroupDefault
	hintCtxDotsDeleteConfirm
	hintCtxDotsRepoConfirm
	hintCtxDotsLocalConfirm
	hintCtxDotsIgnoreConfirm
	hintCtxDotsVariantCreate
	hintCtxDotsVariantRemove
	hintCtxDotsRow
	hintCtxDotsConflict
	hintCtxFilePickerBrowse
	hintCtxHostGroupTools
	hintCtxHostGroupToolsSearch
	hintCtxHostGroupDots
	hintCtxHostGroupDotsSearch
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

func pressAgainHint(key, action string) hintItem {
	return hintItem{key: key, desc: action, danger: true, pressAgain: true}
}

// hintKey renders a key+description pair with the key styled like the legend.
func hintKey(pal palette, k, desc string) string {
	return pal.styleTitle.Render(k) + pal.styleHintDesc.Render(" "+desc)
}

func renderHintItem(pal palette, h hintItem) string {
	if h.pressAgain {
		return pal.styleDangerLabel.Bold(true).Render("press ") +
			pal.styleDangerSection.Bold(true).Render(h.key) +
			pal.styleDangerLabel.Bold(true).Render(" again to "+h.desc)
	}
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

func renderPopupActionHintText(pal palette, width int, hints []hintItem) string {
	if len(hints) == 0 {
		return ""
	}
	width = max(width, 1)
	left, middle, right := splitPopupActionHints(hints)
	if len(left) == 0 && len(right) == 0 {
		return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(renderActionHintText(pal, middle))
	}
	return renderPopupActionColumns(pal, width, left, middle, right)
}

func splitPopupActionHints(hints []hintItem) (left, middle, right []hintItem) {
	for _, h := range hints {
		switch {
		case isPopupAbortHint(h):
			left = append(left, h)
		case isPopupPrimaryHint(h):
			right = append(right, h)
		default:
			middle = append(middle, h)
		}
	}
	return left, middle, right
}

func isPopupAbortHint(h hintItem) bool {
	desc := strings.ToLower(strings.TrimSpace(h.desc))
	return desc == "cancel" || desc == "close" || desc == "skip" ||
		strings.Contains(desc, "cancel")
}

func isPopupPrimaryHint(h hintItem) bool {
	desc := strings.ToLower(strings.TrimSpace(h.desc))
	if desc == "save" || desc == "create" || desc == "confirm" || desc == "pick" || desc == "apply" || desc == "select" || desc == "run" {
		return true
	}
	return strings.Contains(desc, "continue") ||
		strings.HasPrefix(desc, "reconcile ") ||
		strings.HasPrefix(desc, "confirm ") ||
		strings.HasPrefix(desc, "create ") ||
		strings.HasPrefix(desc, "save ")
}

func renderPopupActionColumns(pal palette, width int, left, middle, right []hintItem) string {
	leftText := renderActionHintText(pal, append(append([]hintItem(nil), left...), middle...))
	rightText := renderActionHintText(pal, right)
	return renderPopupActionEdgeLine(width, leftText, rightText)
}

func renderPopupActionEdgeLine(width int, left, right string) string {
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	switch {
	case left == "":
		return strings.Repeat(" ", max(width-rightW, 0)) + right
	case right == "":
		return fitPopupLine(left, width)
	case leftW+rightW+2 <= width:
		return left + strings.Repeat(" ", width-leftW-rightW) + right
	default:
		return fitPopupLine(left, width) + "\n" + strings.Repeat(" ", max(width-rightW, 0)) + right
	}
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

func selectCancelActionItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Toggle, "select"),
		hintFromBindingDesc(m.keys.Back, "cancel"),
	}
}

func renderPressAgainActionHint(pal palette, prefix, keyLabel, action string) string {
	return renderActionHints(pal, actionHints{
		prefix: prefix,
		items:  []hintItem{pressAgainHint(keyLabel, action)},
	})
}

func contextHintItems(m Model, ctx hintContext) []hintItem {
	switch ctx {
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
		if dotsViewDisabled(m) {
			desc = "enable"
		}
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.Confirm, desc),
		}
	case hintCtxSettingsAgents:
		enabled := m.agentsEnabled
		switch m.settingsCursor {
		case settingsRowSkillsEnabled:
			enabled = m.skillsEnabled
		case settingsRowMcpEnabled:
			enabled = m.mcpEnabled
		case settingsRowPluginsEnabled:
			enabled = m.pluginsEnabled
		}
		desc := "disable"
		if !enabled {
			desc = "enable"
		}
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, desc),
		}
	case hintCtxSettingsDuration:
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, "set"),
		}
	case hintCtxSettingsDurationEdit:
		return []hintItem{
			rawHint("h/l", "adjust"),
			hintFromBindingDesc(m.keys.Confirm, "apply"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxSettingsDanger:
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.Confirm, "confirm"),
		}
	case hintCtxSettingsPriorityEdit:
		return []hintItem{
			hintFromBindingDesc(m.keys.Toggle, "grab"),
			rawHint("x", "on/off"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxSettingsPriorityHold:
		return []hintItem{
			rawHint("↑/↓", "carry"),
			hintFromBindingDesc(m.keys.Toggle, "drop"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxSettingsStatus:
		return nil
	case hintCtxHostGroupPicker:
		return []hintItem{
			hintFromBindingDesc(m.keys.Toggle, "toggle"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxHostDefault:
		hints := []hintItem{}
		if m.hostCopyConfirm {
			hints = append(hints, pressAgainHint(m.keys.Toggle.Help().Key, "copy groups"))
		} else if m.canCopySelectedHostGroups() {
			hints = append(hints, hintFromBindingDesc(m.keys.Toggle, "copy groups"))
		}
		return append(hints,
			hintFromBinding(m.keys.Rename),
			hintFromBinding(m.keys.HostGroups),
			hintFromBinding(m.keys.Delete),
		)
	case hintCtxGroupDefault:
		hints := []hintItem{}
		if !isProtectedGroupName(m.selectedHostGroupName()) {
			hints = append(hints,
				hintFromBinding(m.keys.Rename),
			)
		}
		hints = append(hints,
			hintFromBinding(m.keys.GroupTools),
			hintFromBindingDesc(m.keys.GroupDots, "edit dotfiles"),
		)
		if !isProtectedGroupName(m.selectedHostGroupName()) {
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
			dangerHintFromBindingDesc(m.keys.DotUseRepo, actions.MustTUIConfirmDescription(actions.DotsResolveUseRepo)),
		}
	case hintCtxDotsLocalConfirm:
		return []hintItem{
			dangerHintFromBindingDesc(m.keys.DotUseLocal, actions.MustTUIConfirmDescription(actions.DotsResolveUseLocal)),
		}
	case hintCtxDotsIgnoreConfirm:
		return dotsIgnoreConfirmHintItems(m)
	case hintCtxDotsVariantCreate:
		return []hintItem{
			pressAgainHint(m.keys.DotVariant.Help().Key, "create variant"),
		}
	case hintCtxDotsVariantRemove:
		return []hintItem{
			pressAgainHint(m.keys.DotVariant.Help().Key, "remove variant"),
		}
	case hintCtxDotsRow:
		return dotsRowHintItems(m)
	case hintCtxDotsConflict:
		return dotsConflictHintItems(m)
	case hintCtxFilePickerBrowse:
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, "pick"),
			hintFromBindingDesc(m.keys.Back, "close"),
		}
	case hintCtxHostGroupTools:
		ignoreDesc := "ignore"
		if row, ok := m.selectedGroupToolRow(); ok && row.groupIgnore {
			ignoreDesc = "unignore"
		}
		return []hintItem{
			hintFromBindingDesc(m.keys.Back, "cancel"),
			hintFromBindingDesc(m.keys.Search, "search"),
			hintFromBindingDesc(footerFilterBinding(m.keys, false), "filter"),
			hintFromBindingDesc(m.keys.Toggle, "toggle"),
			hintFromBindingDesc(m.keys.Ignore, ignoreDesc),
			hintFromBindingDesc(m.keys.Confirm, "save"),
		}
	case hintCtxHostGroupToolsSearch:
		return []hintItem{
			hintFromBindingDesc(m.keys.Confirm, "apply"),
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}
	case hintCtxHostGroupDots:
		return []hintItem{
			hintFromBindingDesc(m.keys.Back, "cancel"),
			hintFromBindingDesc(m.keys.Search, "search"),
			hintFromBindingDesc(m.keys.Toggle, "toggle"),
			hintFromBindingDesc(m.keys.Confirm, "save"),
		}
	case hintCtxHostGroupDotsSearch:
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
	hints = append(hints, dotsPrimaryHintItem(m, row))
	if dotsViewDisabled(m) {
		return hints
	}
	if !row.isChild && app.DotStatusHasAction(entry, app.DotActionSync) {
		hints = append(hints, hintFromBindingDesc(m.keys.Sync, app.DotStatusSyncActionLabel(entry)))
	}
	if !row.isChild && len(m.dotMemberships[entry.Name]) > 0 {
		hints = append(hints, hintFromBindingDesc(m.keys.MoveGroup, actions.MustTUILabel(actions.DotsEditGroups)))
	}
	if dotsChildExtractable(row) {
		hints = append(hints, hintFromBindingDesc(m.keys.MoveGroup, actions.MustTUILabel(actions.DotsEditGroups)))
	}
	if dotsVariantEligible(row) {
		hints = append(hints, hintFromBinding(m.keys.DotVariant))
	}
	if row.isChild && app.DotStatusHasAction(entry, app.DotActionIgnore) {
		desc := "ignore"
		if row.child.Ignored {
			desc = "include"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.DotIgnore, desc))
	} else if !row.isChild && (app.DotStatusHasAction(entry, app.DotActionIgnore) || app.DotStatusHasAction(entry, app.DotActionUnignore)) {
		desc := "ignore"
		if app.DotStatusIgnored(entry) {
			desc = "include"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.DotIgnore, desc))
	}
	if !row.isChild && app.DotStatusHasAction(entry, app.DotActionRemove) {
		hints = append(hints, hintFromBinding(m.keys.DotDelete))
	}
	return hints
}

func dotsConflictHintItems(m Model) []hintItem {
	visible := dotsVisibleRows(m)
	if m.dotsCursor < 0 || m.dotsCursor >= len(visible) {
		return nil
	}
	row := visible[m.dotsCursor]
	entry := row.entry
	hints := make([]hintItem, 0, 4)
	hints = append(hints, dotsPrimaryHintItem(m, row))
	if dotsViewDisabled(m) {
		return hints
	}
	if app.DotStatusHasAction(entry, app.DotActionUseRepo) {
		hints = append(hints, hintFromBindingDesc(m.keys.DotUseRepo, actions.MustTUILabel(actions.DotsResolveUseRepo)))
	}
	if app.DotStatusHasAction(entry, app.DotActionUseLocal) {
		hints = append(hints, hintFromBindingDesc(m.keys.DotUseLocal, actions.MustTUILabel(actions.DotsResolveUseLocal)))
	}
	if dotsVariantEligible(row) {
		hints = append(hints, hintFromBinding(m.keys.DotVariant))
	}
	if app.DotStatusHasAction(entry, app.DotActionIgnore) || app.DotStatusHasAction(entry, app.DotActionUnignore) {
		desc := "ignore"
		if app.DotStatusIgnored(entry) {
			desc = "include"
		}
		hints = append(hints, hintFromBindingDesc(m.keys.DotIgnore, desc))
	}
	if app.DotStatusHasAction(entry, app.DotActionRemove) {
		hints = append(hints, hintFromBinding(m.keys.DotDelete))
	}
	if dotsConflictCount(m) > 1 {
		hints = append(hints,
			hintFromBindingDesc(m.keys.DotUseRepoAll, actions.MustTUILabel(actions.DotsResolveAllUseRepo)),
			hintFromBindingDesc(m.keys.DotUseLocalAll, actions.MustTUILabel(actions.DotsResolveAllUseLocal)),
		)
	}
	return hints
}

func dotsPrimaryHintItem(m Model, row dotsVisibleRow) hintItem {
	if dotsRowIsDir(row) {
		desc := "expand"
		if dotsRowExpanded(m, row) {
			desc = "collapse"
		}
		return hintFromBindingDesc(m.keys.Toggle, desc)
	}
	return hintFromBindingDesc(m.keys.Toggle, "peek")
}

func dotsIgnoreConfirmHintItems(m Model) []hintItem {
	visible := dotsVisibleRows(m)
	desc := "confirm ignore"
	if m.dotsCursor >= 0 && m.dotsCursor < len(visible) {
		row := visible[m.dotsCursor]
		if row.child.Ignored || app.DotStatusIgnored(row.entry) {
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

	if !isIgnored && t.Installed && t.Outdated && t.UpdateBlocked != app.UpdateBlockSelfUpdates {
		hints = append(hints, hintFromBinding(m.keys.Upgrade))
	}

	var showGroup, showIgnore, showDelete bool
	switch {
	case ss == syncOrphan && t.Installed:
		hints = append(hints, hintFromBinding(m.keys.Claim))
		showIgnore = true
		showDelete = true
	case providerPinForTool(t, m.toolProviderPins) != "":
		hints = append(hints, hintFromBindingDesc(m.keys.PinProvider, "remove override"))
		if isProviderRepairSync(ss) {
			hints = append(hints, hintFromBinding(m.keys.MigrateProvider))
		}
		showGroup = true
		showIgnore = true
		showDelete = true
	case ss == syncWrongProv, ss == syncNvmManaged:
		if ss == syncWrongProv {
			hints = append(hints, hintFromBinding(m.keys.PinProvider))
		}
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
	if m.toolFallbackActionEligible(t) {
		hints = append(hints, hintFromBinding(m.keys.Fallback))
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
		if dotsViewBlocked(*m) {
			return footerBindings(k, nil, []key.Binding{k.Search})
		}
		return footerBindings(k, []key.Binding{k.DotAdd, k.DotRefresh, k.SyncAll}, []key.Binding{k.Search})
	case viewSettings:
		return footerBindings(k, nil, nil)
	case viewStatus:
		return footerBindings(k, dashboardFooterActionBindings(m), nil)
	case viewGroups:
		actions := []key.Binding{k.NewGroup}
		return footerBindings(k, actions, nil)
	case viewSkills:
		listActions := []key.Binding{agentsUpdateAllBinding(), agentsSyncAllBinding(), agentsRefreshBinding()}
		return footerBindings(k, listActions, []key.Binding{agentsFilterBinding(k), agentsAgentFilterBinding(k)})
	default:
		if m.listConfirm.action == listConfirmSyncAll {
			return nil
		}
		filters := []key.Binding{k.Search, footerFilterBinding(k, true)}
		if m.toolFiltersOrSearchActive() {
			filters = append(filters, footerClearFiltersBinding(k))
		}
		return footerBindings(k, []key.Binding{k.UpgradeAll, k.SyncAll, k.Refresh}, filters)
	}
}

func rowConfirmationActive(m Model) bool {
	if m.listConfirm.action != "" && m.listConfirm.action != listConfirmSyncAll {
		return true
	}
	return m.dashboardReconcilePlanOpen ||
		m.hostDeleteConfirm ||
		m.groupDeleteConfirm ||
		m.dangerConfirmRow >= 0 ||
		m.agentsDeleteConfirm ||
		m.agentsIgnoreConfirm ||
		m.pluginMarketplaceOfferConfirm ||
		dotsConfirmationActive(m)
}

func dotsConfirmationActive(m Model) bool {
	return m.dotsConfirmIdx >= 0 ||
		m.dotsOverwriteIdx >= 0 ||
		m.dotsLocalIdx >= 0 ||
		m.dotsIgnoreIdx >= 0 ||
		m.dotsVariantIdx >= 0
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

// agentsInstallBinding, agentsUpdateBinding, agentsGroupBinding,
// agentsIgnoreBinding, and agentsDeleteBinding describe the agents-all row
// actions (i/u/g/x/d) for the full-help popup. They're constructed ad hoc
// rather than added to KeyMap since they're footer/popup-display-only —
// handleAgentsAllRowActionKeyMsg matches on the raw key string directly.
func agentsInstallBinding() key.Binding {
	return key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "install"))
}

func agentsUpdateBinding() key.Binding {
	return key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "update"))
}

func agentsGroupBinding() key.Binding {
	return key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "group"))
}

func agentsIgnoreBinding() key.Binding {
	return key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "ignore"))
}

func agentsDeleteBinding() key.Binding {
	return key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
}

// agentsFilterBinding and agentsAgentFilterBinding describe the agents tab's
// two independent filter chip bars ([,] type chip, {,} agent chip) for the
// footer defaults row, kept as separate hint entries (unlike tools' single
// combined footerFilterBinding) since the agent chip's label ("agent") means
// something different from tools' group filter.
func agentsFilterBinding(k KeyMap) key.Binding {
	help := k.PrevTab.Help()
	return key.NewBinding(key.WithKeys(k.PrevTab.Keys()...), key.WithHelp(compactFilterLabel(help.Key), "filter"))
}

func agentsAgentFilterBinding(k KeyMap) key.Binding {
	help := k.GroupPrev.Help()
	return key.NewBinding(key.WithKeys(k.GroupPrev.Keys()...), key.WithHelp(compactFilterLabel(help.Key), "agent"))
}

// agentsUpdateAllBinding, agentsSyncAllBinding, and agentsRefreshBinding
// describe the agents tab's three global bulk actions (U/S/R), mirroring
// tools' UpgradeAll/SyncAll/Refresh footer row exactly. Handled directly by
// key string in handleAgentsGlobalActionKeyMsg (see the "U"/"S"/"R" cases),
// so these exist for display only.
func agentsUpdateAllBinding() key.Binding {
	return key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "update all"))
}

func agentsSyncAllBinding() key.Binding {
	return key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sync all"))
}

func agentsRefreshBinding() key.Binding {
	return key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))
}

func footerClearFiltersBinding(k KeyMap) key.Binding {
	help := k.Back.Help()
	return key.NewBinding(key.WithKeys(k.Back.Keys()...), key.WithHelp(help.Key, "clear"))
}

func dashboardRefreshBinding(k KeyMap) key.Binding {
	help := k.Refresh.Help()
	return key.NewBinding(key.WithKeys(k.Refresh.Keys()...), key.WithHelp(help.Key, "refresh dashboard"))
}

func dashboardReconcileBinding(k KeyMap) key.Binding {
	help := k.Reconcile.Help()
	return key.NewBinding(key.WithKeys(k.Reconcile.Keys()...), key.WithHelp(help.Key, "reconcile all"))
}

func dashboardFooterActionBindings(m *Model) []key.Binding {
	k := m.keys
	actions := make([]key.Binding, 0, 2)
	if statusDashboardReconcileActionable(*m) {
		actions = append(actions, dashboardReconcileBinding(k))
	}
	actions = append(actions, dashboardRefreshBinding(k))
	return actions
}

func compactFilterLabel(label string) string {
	return strings.ReplaceAll(label, ",", "")
}

func tabFullHelpBindings(m *Model) [][]key.Binding {
	k := m.keys
	common := []key.Binding{k.Top, k.Bottom, k.HalfPageUp, k.HalfPageDown, k.PageUp, k.PageDown, k.Tab, k.Search, k.Palette, k.Help, k.Quit}
	switch m.mode {
	case viewDots:
		if dotsViewBlocked(*m) {
			return [][]key.Binding{
				common,
				{k.Confirm, k.Back},
			}
		}
		return [][]key.Binding{
			common,
			{k.SyncAll, k.DotRefresh, k.DotAdd, k.DotVariant, k.Sync, k.DotUseRepo, k.DotUseLocal, k.DotDelete, k.DotIgnore, k.Back},
		}
	case viewSettings:
		return [][]key.Binding{
			common,
			{k.Toggle, k.Confirm, k.Back, k.GroupPrev, k.GroupNext},
		}
	case viewStatus:
		statusActions := make([]key.Binding, 0, 4)
		if binding, ok := selectedStatusActionHintBinding(*m); ok {
			statusActions = append(statusActions, binding)
		}
		statusActions = append(statusActions, dashboardFooterActionBindings(m)...)
		statusActions = append(statusActions, k.Back)
		return [][]key.Binding{
			common,
			statusActions,
		}
	case viewGroups:
		return [][]key.Binding{
			common,
			{k.NewGroup, k.HostGroups, k.GroupTools, k.GroupDots, k.Toggle, k.Back},
		}
	case viewSkills:
		return [][]key.Binding{
			common,
			{agentsInstallBinding(), agentsUpdateBinding(), agentsGroupBinding(), agentsIgnoreBinding(), agentsDeleteBinding()},
			{footerFilterBinding(k, true)},
		}
	default:
		return [][]key.Binding{
			common,
			{k.Install, k.Upgrade, k.UpgradeAll, k.SyncAll, k.Delete, k.Claim, k.Ignore, k.MoveGroup, k.PinProvider, k.MigrateProvider, k.Refresh},
			{k.PrevTab, k.NextTab, k.GroupPrev, k.GroupNext},
		}
	}
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
		return []hintItem{pressAgainHint(keyLabel, "quit")}
	case m.listConfirm.action == listConfirmSyncAll:
		keyLabel := m.keys.SyncAll.Help().Key
		desc := "sync all"
		if m.mode == viewStatus {
			desc = "reconcile all"
		}
		return []hintItem{pressAgainHint(keyLabel, desc)}
	case m.listConfirm.action == listConfirmDelete:
		return confirmActionItems(m.keys.Delete, actions.MustTUIConfirmDescription(actions.ToolDelete), m.keys.Back)
	case m.listConfirm.action == listConfirmReinstallDefault:
		return confirmActionItems(m.keys.MigrateProvider, actions.MustTUIConfirmDescription(actions.ToolReinstallDefault), m.keys.Back)
	case m.listConfirm.action == listConfirmClearProviderOverride:
		return confirmActionItems(m.keys.PinProvider, "remove provider override and reinstall with default", m.keys.Back)
	case m.dotsConfirmIdx >= 0:
		return contextHintItems(m, hintCtxDotsDeleteConfirm)
	case m.dotsOverwriteIdx >= 0:
		return contextHintItems(m, hintCtxDotsRepoConfirm)
	case m.dotsLocalIdx >= 0:
		return contextHintItems(m, hintCtxDotsLocalConfirm)
	case m.dotsIgnoreIdx >= 0:
		return contextHintItems(m, hintCtxDotsIgnoreConfirm)
	case m.dotsVariantIdx >= 0 && m.dotsVariantMode == dotsVariantCreate:
		return contextHintItems(m, hintCtxDotsVariantCreate)
	case m.dotsVariantIdx >= 0 && m.dotsVariantMode == dotsVariantRemove:
		return contextHintItems(m, hintCtxDotsVariantRemove)
	case m.dangerConfirmRow == settingsRowDotsSync:
		return contextHintItems(m, hintCtxDotsDeleteConfirm)
	case m.dangerConfirmRow >= 0:
		return contextHintItems(m, hintCtxSettingsDanger)
	case m.hostDeleteConfirm:
		return []hintItem{pressAgainHint("d", "delete")}
	case m.hostCopyConfirm:
		return []hintItem{pressAgainHint(m.keys.Toggle.Help().Key, "copy groups")}
	case m.groupDeleteConfirm:
		return confirmActionItems(m.keys.Confirm, actions.MustTUIConfirmDescription(actions.GroupDelete), m.keys.Back)
	case m.agentsDeleteConfirm:
		label := "delete"
		if m.agentsDeleteUninstall {
			label = "uninstall"
		}
		return []hintItem{pressAgainHint(m.keys.Delete.Help().Key, label)}
	case m.agentsIgnoreConfirm:
		label := "confirm ignore"
		if m.agentsIgnoreName != "" {
			if entry, ok := agentsAllEntryAt(m, m.agentsAllCursor); ok && entry.feature == m.agentsIgnoreFeature && entry.status == agentsStatusIgnored {
				label = "confirm include"
			}
		}
		return []hintItem{pressAgainHint(m.keys.Ignore.Help().Key, label)}
	case m.pluginMarketplaceOfferConfirm:
		return confirmActionItems(m.keys.Confirm, "claim marketplace "+m.pluginMarketplaceOfferMarket+" too", m.keys.Back)
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
		if h.pressAgain {
			sb.WriteString("  ")
			sb.WriteString(renderHintItem(p, h))
			sb.WriteByte('\n')
			continue
		}
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
		if dotsViewBlocked(m) {
			desc := "enable dots"
			if dotsViewUnconfigured(m) {
				desc = "set up dots"
			}
			return []helpGroup{{items: []hintItem{
				hintFromBindingDesc(k.Confirm, desc),
			}}}
		}
		return []helpGroup{{items: []hintItem{
			hintFromBindingDesc(k.DotAdd, actions.MustTUILabel(actions.DotsAdd)),
			hintFromBindingDesc(k.DotRefresh, actions.MustTUILabel(actions.DotsRefresh)),
			hintFromBindingDesc(k.SyncAll, actions.MustTUILabel(actions.ToolSyncAll)),
			hintFromBindingDesc(k.Sync, actions.MustTUILabel(actions.DotsSync)),
			hintFromBindingDesc(k.DotVariant, actions.MustTUILabel(actions.DotsVariant)),
			hintFromBindingDesc(k.DotUseRepo, actions.MustTUILabel(actions.DotsResolveUseRepo)),
			hintFromBindingDesc(k.DotUseLocal, actions.MustTUILabel(actions.DotsResolveUseLocal)),
			hintFromBindingDesc(k.MoveGroup, actions.MustTUILabel(actions.DotsEditGroups)),
			hintFromBindingDesc(k.DotIgnore, actions.MustTUILabel(actions.DotsIgnore)),
			hintFromBindingDesc(k.DotDelete, actions.MustTUILabel(actions.DotsDelete)),
		}}}
	case viewSettings:
		return []helpGroup{{items: []hintItem{
			hintFromBindingDesc(k.Toggle, "change toggle or option"),
			hintFromBindingDesc(k.Confirm, "edit or save expanded setting"),
			rawHint("K/J", "move system priority"),
		}}}
	case viewStatus:
		rowItems := []hintItem(nil)
		if hint, ok := selectedStatusActionHintItem(m); ok {
			rowItems = []hintItem{hint}
		}
		groups := make([]helpGroup, 0, 2)
		if len(rowItems) > 0 {
			groups = append(groups, helpGroup{title: "Row", items: rowItems})
		}
		bulkItems := make([]hintItem, 0, 2)
		for _, binding := range dashboardFooterActionBindings(&m) {
			bulkItems = append(bulkItems, hintFromBinding(binding))
		}
		groups = append(groups, helpGroup{title: "Bulk", items: bulkItems})
		return groups
	case viewGroups:
		items := []hintItem{
			hintFromBindingDesc(k.NewGroup, actions.MustTUILabel(actions.GroupCreate)),
			hintFromBindingDesc(k.Rename, actions.LabelRename),
			hintFromBindingDesc(k.HostGroups, actions.MustTUILabel(actions.HostEditGroups)),
			hintFromBindingDesc(k.GroupTools, actions.MustTUILabel(actions.GroupEditTools)),
			hintFromBindingDesc(k.GroupDots, "edit dotfiles"),
			hintFromBindingDesc(k.Delete, actions.LabelDelete),
		}
		if m.canCopySelectedHostGroups() {
			items = append([]hintItem{items[0], hintFromBindingDesc(k.Toggle, "copy groups")}, items[1:]...)
		}
		return []helpGroup{{items: items}}
	default:
		return []helpGroup{
			{title: "Row", items: []hintItem{
				hintFromBindingDesc(k.Install, actions.MustTUILabel(actions.ToolInstall)),
				hintFromBindingDesc(k.Upgrade, actions.MustTUILabel(actions.ToolUpdate)),
				hintFromBindingDesc(k.Claim, actions.MustTUILabel(actions.ToolClaim)),
				hintFromBindingDesc(k.PinProvider, actions.MustTUILabel(actions.ToolPinProvider)),
				hintFromBindingDesc(k.MigrateProvider, actions.MustTUILabel(actions.ToolReinstallDefault)),
				hintFromBindingDesc(k.MoveGroup, actions.MustTUILabel(actions.ToolChangeGroup)),
				hintFromBindingDesc(k.Ignore, actions.MustTUILabel(actions.ToolIgnore)),
				hintFromBindingDesc(k.Delete, actions.MustTUILabel(actions.ToolDelete)),
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
	return mode == viewList || mode == viewSearch || mode == viewCommand || mode == viewGroupPicker || mode == viewGroupMembership || mode == viewGroupTools || mode == viewGroupDots || mode == viewIgnoreScope || mode == viewProviderScope || mode == viewSetup
}

func renderHelpLegend(m Model, width int) string {
	p := m.palette
	items := helpLegendItems(m)
	divider := popupDividerWithStyle(p.styleHelp, width)
	if len(items) == 0 {
		return divider + "\n" + lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(p.styleHelp.Render("none"))
	}
	lines := wrapHelpLegendItems(items, p.styleHelp.Render("  "), width)
	for i, line := range lines {
		lines[i] = lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(line)
	}
	return divider + "\n" + strings.Join(lines, "\n")
}

func helpLegendItems(m Model) []string {
	p := m.palette
	switch {
	case m.mode == viewDots:
		if dotsViewBlocked(m) {
			return nil
		}
		return []string{
			p.styleInstalled.Render("✓") + p.styleHelp.Render(" synced"),
			p.styleMissing.Render("✗") + p.styleHelp.Render(" missing"),
			p.styleOutdated.Render("!") + p.styleHelp.Render(" conflict"),
			p.styleHelp.Render("· no source"),
			p.styleProvider.Render(dotVariantIcon) + p.styleHelp.Render(" host variant"),
			p.styleHelp.Render("↳ child"),
		}
	case m.mode == viewSettings:
		return []string{
			p.styleInstalled.Render("[ON]") + p.styleHelp.Render(" enabled"),
			p.styleMissing.Render("[OFF]") + p.styleHelp.Render(" disabled"),
			p.styleMissing.Render("⚠") + p.styleHelp.Render(" confirm"),
		}
	case m.mode == viewStatus:
		return []string{
			p.styleInstalled.Render(iconInstalled) + p.styleHelp.Render(" healthy"),
			p.styleStatus.Render(iconPending) + p.styleHelp.Render(" working"),
			p.styleOutdated.Render(iconFailed) + p.styleHelp.Render(" warning"),
			p.styleMissing.Render(iconMissing) + p.styleHelp.Render(" failure"),
			p.styleHelp.Render(iconIgnored) + p.styleHelp.Render(" quiet"),
			p.styleProvider.Render("[ON]") + p.styleHelp.Render(" service on"),
			p.styleHelp.Render("[OFF] service off"),
		}
	case isGroupsHelpMode(m.mode):
		return []string{
			p.styleProvider.Render("(local)") + p.styleHelp.Render(" machine group"),
			p.styleHelp.Render("[x] included"),
			p.styleHelp.Render("[ ] excluded"),
			p.styleIgnored.Render(iconIgnored) + p.styleHelp.Render(" ignored"),
			p.styleHelp.Render(iconPrivileged) + p.styleHelp.Render(" may need sudo"),
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
			p.styleHelp.Render(iconPrivileged) + p.styleHelp.Render(" may need sudo"),
			p.styleErr.Render(iconFailed) + p.styleHelp.Render(" failed"),
			p.styleHelp.Render("mgr!") + p.styleHelp.Render(" override"),
		}
	}
}

func wrapHelpLegendItems(items []string, sep string, width int) []string {
	if len(items) == 0 {
		return nil
	}
	width = max(width, 1)
	var lines []string
	line := ""
	for _, item := range items {
		candidate := item
		if line != "" {
			candidate = line + sep + item
		}
		if line != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, line)
			line = item
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func isGroupsHelpMode(mode viewMode) bool {
	switch mode {
	case viewGroups, viewGroupMembership, viewGroupTools, viewGroupDots:
		return true
	default:
		return false
	}
}

func helpPopupTitle(m Model) string {
	switch m.mode {
	case viewDots:
		return "Dots Help"
	case viewSettings:
		return "Settings Help"
	case viewStatus:
		return "Dashboard Help"
	case viewGroups:
		return "Groups Help"
	default:
		return "Tools Help"
	}
}
