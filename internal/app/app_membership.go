package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func (a *App) SetHostGroupsWithCreated(hostname string, groups, createdGroups []string) error {
	return a.setHostGroups(hostname, groups, createdGroups)
}

func (a *App) setHostGroups(hostname string, groups, createdGroups []string) error {
	hostname = strings.TrimSpace(machineGroupName(hostname))
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	var targets map[string]struct{}
	if len(createdGroups) > 0 {
		targets = membershipGroupSet(groups)
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := createSelectedGroupsInConfig(cfg, createdGroups, targets); err != nil {
			return err
		}
		if _, err := ensureHostGroupInConfig(cfg, hostname); err != nil {
			return err
		}
		if cfg.Hosts == nil {
			cfg.Hosts = make(map[string][]string)
		}
		filtered, err := reusableHostGroupsInConfig(cfg, hostname, groups)
		if err != nil {
			return err
		}
		cfg.Hosts[hostname] = filtered
		return nil
	})
}

func (a *App) SetHostGroupsWithState(ctx context.Context, hostname string, groups, createdGroups []string) (*ToolGroupState, error) {
	return a.toolGroupStateAfter(ctx, func() error {
		return a.SetHostGroupsWithCreated(hostname, groups, createdGroups)
	})
}

func (a *App) SetToolGroups(name string, groups, createdGroups []string, activeHost string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	targets := membershipGroupSet(groups)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if len(targets) > 0 {
			if _, ok := cfg.Tools[name]; !ok {
				return fmt.Errorf("logical tool %q not found; run 'omni tools set %s --provider <ecosystem-provider>' first", name, name)
			}
		}
		if err := createSelectedGroupsInConfig(cfg, createdGroups, targets); err != nil {
			return err
		}
		if err := ensureMembershipTargetGroups(cfg, targets); err != nil {
			return err
		}
		if err := ensureMembershipGroupsOnHostInConfig(cfg, activeHost, targets); err != nil {
			return err
		}
		setToolGroupsInConfig(cfg, name, targets)
		return nil
	})
}

func (a *App) SetToolGroupsWithState(ctx context.Context, name string, groups, createdGroups []string, activeHost string) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		return a.SetToolGroups(name, groups, createdGroups, activeHost)
	})
}

func (a *App) SetToolGroupMembershipWithState(ctx context.Context, name, group string, add bool) (*ToolGroupMutationState, error) {
	return a.toolGroupMutationStateAfter(ctx, func() error {
		if add {
			return a.MoveToolToGroup(name, group)
		}
		return a.RemoveToolFromGroup(name, group)
	})
}

type DotGroupsChange struct {
	DotMemberships map[string][]string
	DotsState      *DotsState
}

// ReusableGroupNames returns the names of all reusable (non-host) groups
// currently configured.
func (a *App) ReusableGroupNames() ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return reusableGroupNames(cfg.Groups), nil
}

func (a *App) DotGroups(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("dots entry name is required")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if err := a.requireDotsEnabled(cfg); err != nil {
		return nil, err
	}
	groups := dotMembershipMapFromConfig(cfg)[name]
	if len(groups) == 0 {
		return nil, fmt.Errorf("dots entry %q not found", name)
	}
	return slices.Clone(groups), nil
}

func (a *App) SetDotGroups(name string, groups, createdGroups []string, activeHost string) (DotGroupsChange, error) {
	return a.SetDotGroupsWithState(context.Background(), name, groups, createdGroups, activeHost)
}

func (a *App) SetDotGroupsWithState(ctx context.Context, name string, groups, createdGroups []string, activeHost string) (result DotGroupsChange, err error) {
	defer func() {
		result.DotsState = a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	name = strings.TrimSpace(name)
	if name == "" {
		return result, fmt.Errorf("dots entry name is required")
	}
	targets := membershipGroupSet(groups)
	err = a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		template, found := firstDotEntryInConfig(cfg, name)
		if !found && len(targets) > 0 {
			return fmt.Errorf("dots entry %q not found", name)
		}
		if err := createSelectedGroupsInConfig(cfg, createdGroups, targets); err != nil {
			return err
		}
		if err := ensureMembershipTargetGroups(cfg, targets); err != nil {
			return err
		}
		if err := ensureMembershipGroupsOnHostInConfig(cfg, activeHost, targets); err != nil {
			return err
		}
		if found {
			reduced := EnforceMembershipInvariant(sortedMembershipGroups(targets), ReusablePredicate(reusableGroupNames(cfg.Groups)))
			setDotGroupsInConfig(cfg, name, template, membershipGroupSet(reduced))
		}
		result.DotMemberships = dotMembershipMapFromConfig(cfg)
		return nil
	})
	return result, err
}

func (a *App) RemoveDotGroups(name string, removeGroups []string) (DotGroupsChange, error) {
	return a.RemoveDotGroupsWithState(context.Background(), name, removeGroups)
}

func (a *App) RemoveDotGroupsWithState(ctx context.Context, name string, removeGroups []string) (DotGroupsChange, error) {
	current, err := a.DotGroups(name)
	if err != nil {
		return DotGroupsChange{}, err
	}
	target, removed := removeMembershipGroups(current, removeGroups)
	if !removed {
		return DotGroupsChange{}, fmt.Errorf("remove requires at least one group")
	}
	if len(target) == 0 {
		return DotGroupsChange{}, fmt.Errorf("dots entry %q needs at least one group; use dots delete to remove it from management", name)
	}
	return a.SetDotGroupsWithState(ctx, name, target, nil, "")
}

func (a *App) SetGroupTools(group string, membership, originalMembership, ignores, originalIgnores map[string]bool) (int, error) {
	group = compatibilityGroupName(group)
	changed := stagedBoolMapChangeCount(membership, originalMembership) + stagedBoolMapChangeCount(ignores, originalIgnores)
	err := a.withConfig(func(cfg *config.RootConfig) error {
		if findGroupInConfig(cfg, group) == nil {
			return fmt.Errorf("group %q not found", group)
		}
		if err := applyStagedBoolMapChanges(membership, originalMembership, func(name string, desired bool) error {
			if desired {
				return moveToolToGroupInConfig(cfg, name, group)
			}
			return removeToolFromGroupInConfig(cfg, name, group)
		}); err != nil {
			return err
		}
		return applyStagedBoolMapChanges(ignores, originalIgnores, func(name string, desired bool) error {
			return setGlobalToolIgnoreInConfig(cfg, name, desired)
		})
	})
	return changed, err
}

type GroupToolsChange struct {
	Changed      int
	Tools        []*database.ToolCache
	State        *ToolGroupState
	ScopeDisplay *ToolScopeDisplayState
}

func (a *App) SetGroupToolsWithState(ctx context.Context, group string, membership, originalMembership, ignores, originalIgnores map[string]bool) (*GroupToolsChange, error) {
	changed, err := a.SetGroupTools(group, membership, originalMembership, ignores, originalIgnores)
	if err != nil {
		return nil, err
	}
	toolState, err := a.toolGroupMutationState(ctx)
	if err != nil {
		return nil, err
	}
	scopeDisplay, err := a.ToolScopeDisplayState(ctx)
	if err != nil {
		return nil, err
	}
	return &GroupToolsChange{
		Changed:      changed,
		Tools:        toolState.Tools,
		State:        toolState.State,
		ScopeDisplay: scopeDisplay,
	}, nil
}

type GroupDotsChange struct {
	Changed        int
	DotMemberships map[string][]string
	DotsState      *DotsState
}

func (a *App) SetGroupDots(group string, membership, originalMembership map[string]bool) (GroupDotsChange, error) {
	return a.SetGroupDotsWithState(context.Background(), group, membership, originalMembership)
}

func (a *App) SetGroupDotsWithState(ctx context.Context, group string, membership, originalMembership map[string]bool) (result GroupDotsChange, err error) {
	defer func() {
		result.DotsState = a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	group = compatibilityGroupName(group)
	result.Changed = stagedBoolMapChangeCount(membership, originalMembership)
	err = a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		if findGroupInConfig(cfg, group) == nil {
			return fmt.Errorf("group %q not found", group)
		}
		if err := applyStagedBoolMapChanges(membership, originalMembership, func(name string, desired bool) error {
			if desired {
				return moveDotToGroupInConfig(cfg, name, group)
			}
			return removeDotFromGroupInConfig(cfg, name, group)
		}); err != nil {
			return err
		}
		result.DotMemberships = dotMembershipMapFromConfig(cfg)
		return nil
	})
	return result, err
}

func membershipGroupSet(groups []string) map[string]struct{} {
	set := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(compatibilityGroupName(group))
		if group == "" {
			continue
		}
		set[group] = struct{}{}
	}
	return set
}

func NormalizeGroupNames(groups []string) []string {
	set := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		set[group] = struct{}{}
	}
	return sortedMembershipGroups(set)
}

func GroupMembershipsChanged(current, original []string) bool {
	if len(current) != len(original) {
		return true
	}
	currentCopy := append([]string(nil), current...)
	originalCopy := append([]string(nil), original...)
	slices.Sort(currentCopy)
	slices.Sort(originalCopy)
	return !slices.Equal(currentCopy, originalCopy)
}

func CreatedMembershipGroups(created, memberships []string) []string {
	membershipSet := make(map[string]bool, len(memberships))
	for _, group := range memberships {
		membershipSet[group] = true
	}
	var out []string
	for _, group := range created {
		if membershipSet[group] {
			out = append(out, group)
		}
	}
	slices.Sort(out)
	return out
}

func sortedMembershipGroups(groups map[string]struct{}) []string {
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}

func removeMembershipGroups(current, removeGroups []string) ([]string, bool) {
	removeSet := membershipGroupSet(removeGroups)
	if len(removeSet) == 0 {
		return nil, false
	}
	targets := membershipGroupSet(current)
	for group := range removeSet {
		delete(targets, group)
	}
	return sortedMembershipGroups(targets), true
}

func ToolGroupLabelsForHost(memberships map[string][]string, info *HostInfo, machineGroup string) map[string]string {
	return toolGroupLabelsWithFilter(memberships, ActiveHostGroupSet(info, machineGroup))
}

func ToolGroupLabels(memberships map[string][]string, info *HostInfo) map[string]string {
	return ToolGroupLabelsForHost(memberships, info, currentMachineGroupName())
}

func toolGroupLabelsWithFilter(memberships map[string][]string, allowed map[string]bool) map[string]string {
	out := make(map[string]string, len(memberships))
	for key, groups := range memberships {
		out[key] = CompactGroupLabel(FilterGroupsForHost(groups, allowed))
	}
	return out
}

// GroupLabelForHost returns the compact memberships visible on one host.
func GroupLabelForHost(groups []string, info *HostInfo, machineGroup string) string {
	return CompactGroupLabel(FilterGroupsForHost(groups, ActiveHostGroupSet(info, machineGroup)))
}

// GroupLabel returns the compact memberships visible on the current machine.
func GroupLabel(groups []string, info *HostInfo) string {
	return GroupLabelForHost(groups, info, currentMachineGroupName())
}

// HostFirstGroups returns groups visible on the current machine, ordered
// with the host group first followed by reusable groups sorted
// alphabetically. It applies the same active-host filter as GroupLabel.
func HostFirstGroups(groups []string, info *HostInfo) []string {
	filtered := FilterGroupsForHost(groups, ActiveHostGroupSet(info, currentMachineGroupName()))
	host := ""
	if info != nil {
		host = machineGroupName(info.Active)
	}
	hostGroups := make([]string, 0, 1)
	reusable := make([]string, 0, len(filtered))
	for _, group := range filtered {
		if host != "" && group == host {
			hostGroups = append(hostGroups, group)
			continue
		}
		reusable = append(reusable, group)
	}
	sort.Strings(reusable)
	return append(hostGroups, reusable...)
}

// IsHostGroup reports whether name is the current host's own host group.
func IsHostGroup(name string, info *HostInfo) bool {
	if info == nil || name == "" {
		return false
	}
	return name == machineGroupName(info.Active)
}

func ActiveHostGroupSet(info *HostInfo, machineGroup string) map[string]bool {
	if info == nil || info.Active == "" {
		return nil
	}
	host, ok := info.Hosts[info.Active]
	if !ok {
		return nil
	}
	allowed := make(map[string]bool, len(host.Groups)+1)
	for _, group := range host.Groups {
		allowed[group] = true
	}
	if machineGroup = strings.TrimSpace(machineGroupName(machineGroup)); machineGroup != "" {
		allowed[machineGroup] = true
	}
	return allowed
}

func VisibleGroupNamesForHost(groupNames []string, info *HostInfo, machineGroup string) []string {
	allowed := ActiveHostGroupSet(info, machineGroup)
	if allowed == nil {
		return groupNames
	}
	names := make([]string, 0, len(groupNames))
	for _, name := range groupNames {
		if allowed[name] {
			names = append(names, name)
		}
	}
	return names
}

func VisibleGroupNamesForCurrentMachine(groupNames []string, info *HostInfo) []string {
	return VisibleGroupNamesForHost(groupNames, info, currentMachineGroupName())
}

func SetupHostGroupDraft(groupNames []string, info *HostInfo) map[string]bool {
	draft := make(map[string]bool, len(groupNames))
	for _, group := range groupNames {
		draft[group] = false
	}
	if info == nil {
		return draft
	}
	host := info.Active
	if host == "" {
		host = currentMachineGroupName()
	}
	assignment, ok := info.Hosts[host]
	if !ok {
		return draft
	}
	for _, group := range assignment.Groups {
		if _, exists := draft[group]; exists {
			draft[group] = true
		}
	}
	return draft
}

func SetupSelectedHostGroups(groupNames []string, draft map[string]bool) []string {
	if len(groupNames) == 0 || len(draft) == 0 {
		return nil
	}
	groups := make([]string, 0, len(groupNames))
	for _, group := range groupNames {
		if draft[group] {
			groups = append(groups, group)
		}
	}
	return groups
}

func DotMembershipNames(memberships map[string][]string) []string {
	if len(memberships) == 0 {
		return nil
	}
	names := make([]string, 0, len(memberships))
	for name := range memberships {
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func AllGroupNamesForMachine(groupNames []string, machineGroup string) []string {
	host := strings.TrimSpace(machineGroupName(machineGroup))
	if host == "" {
		host = "localhost"
	}
	names := make([]string, 0, len(groupNames)+1)
	names = append(names, host)
	seen := map[string]bool{host: true}
	reusable := append([]string(nil), groupNames...)
	sort.Strings(reusable)
	for _, name := range reusable {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func AllGroupNamesForCurrentMachine(groupNames []string) []string {
	return AllGroupNamesForMachine(groupNames, currentMachineGroupName())
}

func HostAssignmentPickerGroups(host string, groupNames []string) []string {
	names := make([]string, 0, len(groupNames)+1)
	if host != "" {
		names = append(names, host)
	}
	reusable := append([]string(nil), groupNames...)
	sort.Strings(reusable)
	seen := map[string]bool{host: true}
	for _, name := range reusable {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func HostAssignmentDraftGroups(host string, groups []string) []string {
	out := []string{}
	if host != "" {
		out = append(out, host)
	}
	for _, group := range groups {
		if group == "" || group == host || slices.Contains(out, group) {
			continue
		}
		out = append(out, group)
	}
	sort.Strings(out)
	if host != "" {
		out = moveGroupToFront(out, host)
	}
	return out
}

func EditableHostAssignmentGroups(host string, groups []string) []string {
	out := []string{}
	for _, group := range groups {
		if group == "" || group == host || slices.Contains(out, group) {
			continue
		}
		out = append(out, group)
	}
	sort.Strings(out)
	return out
}

func IsLocalHostGroup(group, machineGroup string) bool {
	return group == "" || group == machineGroupName(machineGroup)
}

func IsCurrentMachineGroup(group string) bool {
	return IsLocalHostGroup(group, currentMachineGroupName())
}

func moveGroupToFront(groups []string, first string) []string {
	idx := slices.Index(groups, first)
	if idx <= 0 {
		return groups
	}
	out := append([]string{groups[idx]}, groups[:idx]...)
	out = append(out, groups[idx+1:]...)
	return out
}

func GroupPickerNamesForHost(groupNames []string, info *HostInfo, machineGroup string, createdGroups []string) []string {
	return PrioritizeGroupNamesForHost(AllGroupNamesForMachine(groupNames, machineGroup), info, machineGroup, createdGroups)
}

func GroupPickerNamesForCurrentMachine(groupNames []string, info *HostInfo, createdGroups []string) []string {
	return GroupPickerNamesForHost(groupNames, info, currentMachineGroupName(), createdGroups)
}

func PrioritizeGroupNamesForHost(groups []string, info *HostInfo, machineGroup string, createdGroups []string) []string {
	allowed := activeHostGroupSetForPicker(info, machineGroup, createdGroups)
	if allowed == nil {
		return groups
	}
	active := make([]string, 0, len(groups))
	inactive := make([]string, 0, len(groups))
	for _, group := range groups {
		if allowed[group] {
			active = append(active, group)
		} else {
			inactive = append(inactive, group)
		}
	}
	return append(active, inactive...)
}

func PrioritizeGroupNamesForCurrentMachine(groups []string, info *HostInfo, createdGroups []string) []string {
	return PrioritizeGroupNamesForHost(groups, info, currentMachineGroupName(), createdGroups)
}

func GroupInActiveHostForPicker(group string, info *HostInfo, machineGroup string, createdGroups []string) bool {
	allowed := activeHostGroupSetForPicker(info, machineGroup, createdGroups)
	return allowed != nil && allowed[group]
}

func GroupInActiveHostForCurrentMachinePicker(group string, info *HostInfo, createdGroups []string) bool {
	return GroupInActiveHostForPicker(group, info, currentMachineGroupName(), createdGroups)
}

func HasActiveHostGroupContext(info *HostInfo, machineGroup string) bool {
	return ActiveHostGroupSet(info, machineGroup) != nil
}

func HasActiveHostGroupContextForCurrentMachine(info *HostInfo) bool {
	return HasActiveHostGroupContext(info, currentMachineGroupName())
}

func activeHostGroupSetForPicker(info *HostInfo, machineGroup string, createdGroups []string) map[string]bool {
	allowed := ActiveHostGroupSet(info, machineGroup)
	if len(createdGroups) == 0 || allowed == nil {
		return allowed
	}
	cp := make(map[string]bool, len(allowed)+len(createdGroups))
	for group := range allowed {
		cp[group] = true
	}
	for _, group := range createdGroups {
		cp[group] = true
	}
	return cp
}

func FilterGroupsForHost(groups []string, allowed map[string]bool) []string {
	if allowed == nil {
		return groups
	}
	filtered := make([]string, 0, len(groups))
	for _, group := range groups {
		if allowed[group] {
			filtered = append(filtered, group)
		}
	}
	return filtered
}

func CompactGroupLabel(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	cp := append([]string(nil), groups...)
	sort.Strings(cp)
	if len(cp) <= 2 {
		return strings.Join(cp, ",")
	}
	return cp[0] + "," + cp[1] + fmt.Sprintf("+%d", len(cp)-2)
}

func reusableGroupNames(groups []*config.GroupConfig) []string {
	var names []string
	seen := make(map[string]bool)
	for _, group := range groups {
		if group.IsHost() {
			continue
		}
		name := group.BaseName()
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func createSelectedGroupsInConfig(cfg *config.RootConfig, createdGroups []string, selected map[string]struct{}) error {
	for _, group := range createdGroups {
		group = strings.TrimSpace(compatibilityGroupName(group))
		if group == "" {
			continue
		}
		if _, ok := selected[group]; !ok {
			continue
		}
		if findGroupInConfig(cfg, group) != nil {
			return fmt.Errorf("group %q already exists", group)
		}
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: group})
	}
	return nil
}

func ensureMembershipTargetGroups(cfg *config.RootConfig, groups map[string]struct{}) error {
	for _, group := range sortedMembershipGroups(groups) {
		if group == config.SystemInventoryGroup {
			if _, err := ensureSystemInventoryGroupInConfig(cfg); err != nil {
				return err
			}
			continue
		}
		ensureGroupInConfig(cfg, group)
	}
	return nil
}

func ensureSystemInventoryGroupInConfig(cfg *config.RootConfig) (*config.GroupConfig, error) {
	group := findGroupInConfig(cfg, config.SystemInventoryGroup)
	if group == nil {
		group = &config.GroupConfig{Name: config.SystemInventoryGroup, Special: config.SystemInventoryGroup}
		cfg.Groups = append(cfg.Groups, group)
		return group, nil
	}
	if group.Special == "" {
		group.Special = config.SystemInventoryGroup
	}
	if !group.IsSystemInventory() {
		return nil, fmt.Errorf("reserved group %q has special marker %q", config.SystemInventoryGroup, group.Special)
	}
	return group, nil
}

func ensureMembershipGroupsOnHostInConfig(cfg *config.RootConfig, activeHost string, groups map[string]struct{}) error {
	activeHost = strings.TrimSpace(activeHost)
	if activeHost == "" {
		return nil
	}
	activeHost = machineGroupName(activeHost)
	if _, err := ensureHostGroupInConfig(cfg, activeHost); err != nil {
		return err
	}
	if cfg.Hosts == nil {
		cfg.Hosts = make(map[string][]string)
	}
	for _, group := range sortedMembershipGroups(groups) {
		if group == activeHost {
			continue
		}
		g := findGroupInConfig(cfg, group)
		if g == nil {
			return fmt.Errorf("group %q not found", group)
		}
		if g.IsHost() {
			// Item memberships may span hosts. Host groups do not belong in
			// another host's reusable-group assignment list.
			continue
		}
		if !slices.Contains(cfg.Hosts[activeHost], group) {
			cfg.Hosts[activeHost] = append(cfg.Hosts[activeHost], group)
		}
	}
	return nil
}

func setToolGroupsInConfig(cfg *config.RootConfig, name string, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			filterToolMemberships(group, name)
			continue
		}
		if !containsToolMembership(group.Tools, name) {
			group.Tools = append(group.Tools, config.ToolEntry{Name: name})
		}
	}
}

func moveToolToGroupInConfig(cfg *config.RootConfig, name, groupName string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if _, ok := cfg.Tools[name]; !ok {
		return fmt.Errorf("logical tool %q not found; run 'omni tools set %s --provider <ecosystem-provider>' first", name, name)
	}
	// MoveToolToGroup is a relocate: the tool ends up in exactly one group. The
	// multi-group model (unlimited host groups + one reusable group) is reached
	// through the membership picker, which sets the full group set directly.
	for _, existing := range cfg.Groups {
		if existing == nil || existing.BaseName() == groupName {
			continue
		}
		filterToolMemberships(existing, name)
	}
	group := ensureGroupInConfig(cfg, groupName)
	if !containsToolMembership(group.Tools, name) {
		group.Tools = append(group.Tools, config.ToolEntry{Name: name})
	}
	return nil
}

func removeToolFromGroupInConfig(cfg *config.RootConfig, name, groupName string) error {
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	group := findGroupInConfig(cfg, groupName)
	if group == nil {
		return fmt.Errorf("group %q not found", groupName)
	}
	filterToolMemberships(group, name)
	return nil
}

func firstDotEntryInConfig(cfg *config.RootConfig, name string) (config.DotEntry, bool) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, entry := range group.Dots {
			if entry.Name == name {
				return entry, true
			}
		}
	}
	return config.DotEntry{}, false
}

func setDotGroupsInConfig(cfg *config.RootConfig, name string, template config.DotEntry, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			filterDotMemberships(group, name)
			continue
		}
		if !containsDotMembership(group.Dots, name) {
			group.Dots = append(group.Dots, template)
		}
	}
}

func moveDotToGroupInConfig(cfg *config.RootConfig, name, groupName string) error {
	template, ok := firstDotEntryInConfig(cfg, name)
	if !ok {
		return fmt.Errorf("dots entry %q not found", name)
	}
	// MoveDotToGroup is a relocate to a single group; see moveToolToGroupInConfig.
	for _, existing := range cfg.Groups {
		if existing == nil || existing.BaseName() == groupName {
			continue
		}
		filterDotMemberships(existing, name)
	}
	group := ensureGroupInConfig(cfg, groupName)
	if !containsDotMembership(group.Dots, name) {
		group.Dots = append(group.Dots, template)
	}
	return nil
}

func removeDotFromGroupInConfig(cfg *config.RootConfig, name, groupName string) error {
	group := findGroupInConfig(cfg, groupName)
	if group == nil {
		return fmt.Errorf("group %q not found", groupName)
	}
	filterDotMemberships(group, name)
	return nil
}

func setGlobalToolIgnoreInConfig(cfg *config.RootConfig, name string, ignored bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if ignored {
		if !slices.Contains(cfg.Ignore.Tools, name) {
			cfg.Ignore.Tools = append(cfg.Ignore.Tools, name)
		}
		return nil
	}
	removeGlobalToolIgnore(cfg, name)
	return nil
}

func stagedBoolMapChangeCount(current, original map[string]bool) int {
	changed := 0
	for name, value := range current {
		if original[name] != value {
			changed++
		}
	}
	return changed
}

func GroupAssignmentChanged(current, original map[string]bool) bool {
	for name, value := range current {
		if original[name] != value {
			return true
		}
	}
	for name, value := range original {
		if current[name] != value {
			return true
		}
	}
	return false
}

func GroupToolsEditorChanged(membership, originalMembership, ignores, originalIgnores map[string]bool) bool {
	return GroupAssignmentChanged(membership, originalMembership) ||
		GroupAssignmentChanged(ignores, originalIgnores)
}

func applyStagedBoolMapChanges(current, original map[string]bool, apply func(string, bool) error) error {
	for _, name := range sortedBoolMapKeys(current) {
		desired := current[name]
		if original[name] == desired {
			continue
		}
		if err := apply(name, desired); err != nil {
			return err
		}
	}
	return nil
}

func sortedBoolMapKeys(values map[string]bool) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
