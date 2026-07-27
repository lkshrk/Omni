package app

import (
	"sort"

	"github.com/lkshrk/omni/internal/dots"
)

func DotStatusState(status DotStatus) dots.State {
	if status.State != "" {
		return status.State
	}
	switch status.Health {
	case HealthOK:
		return dots.StateSynced
	case HealthMissing:
		return dots.StateMissing
	case HealthConflict:
		return dots.StateConflict
	case HealthNoSource:
		return dots.StateNoSource
	default:
		return dots.State(status.Health)
	}
}

type DotStatusSection struct {
	Title    string
	Statuses []DotStatus
}

func DotStatusSections(statuses []DotStatus) []DotStatusSection {
	sections := []DotStatusSection{
		{Title: "Conflict"},
		{Title: "Out Of Sync"},
		{Title: "Synced"},
		{Title: "Ignored"},
	}
	for _, status := range statuses {
		switch dotStatusSectionIndex(status) {
		case 0:
			sections[0].Statuses = append(sections[0].Statuses, status)
		case 2:
			sections[2].Statuses = append(sections[2].Statuses, status)
		case 3:
			sections[3].Statuses = append(sections[3].Statuses, status)
		default:
			sections[1].Statuses = append(sections[1].Statuses, status)
		}
	}
	return sections
}

func SortDotStatuses(statuses []DotStatus) {
	sort.SliceStable(statuses, func(i, j int) bool {
		ki, kj := dotStatusSectionIndex(statuses[i]), dotStatusSectionIndex(statuses[j])
		if ki != kj {
			return ki < kj
		}
		return statuses[i].Name < statuses[j].Name
	})
}

func dotStatusSectionIndex(status DotStatus) int {
	if DotStatusTransientCandidate(status) {
		return 1
	}
	switch DotStatusState(status) {
	case dots.StateConflict, dots.StateUntrackedConflict, dots.StateAmbiguous:
		return 0
	case dots.StateSynced:
		return 2
	case dots.StateIgnored, dots.StateInactive, dots.StateDisabled:
		return 3
	default:
		return 1
	}
}

func DotStatusTransientCandidate(status DotStatus) bool {
	if status.Group != "" {
		return false
	}
	switch DotStatusState(status) {
	case dots.StateLocalOnly, dots.StateRepoOnly, dots.StateUntrackedConflict, dots.StateUntrackedLinked, dots.StateNoSource:
		return true
	default:
		return false
	}
}

func DotSyncAllPendingNames(statuses []DotStatus) map[string]bool {
	pending := make(map[string]bool)
	for _, status := range statuses {
		if status.Name == "" || DotStatusTransientCandidate(status) {
			continue
		}
		switch DotStatusState(status) {
		case dots.StateIgnored, dots.StateInactive, dots.StateDisabled:
			continue
		}
		pending[status.Name] = true
	}
	return pending
}

func DotSyncAllEntryOrder(statuses []DotStatus) []string {
	ordered := append([]DotStatus(nil), statuses...)
	SortDotStatuses(ordered)
	order := make([]string, 0, len(ordered))
	for _, section := range DotStatusSections(ordered) {
		for _, status := range section.Statuses {
			if status.Name != "" {
				order = append(order, status.Name)
			}
		}
	}
	return order
}

func DotStatusVariantEligible(status DotStatus) bool {
	if status.Name == "" {
		return false
	}
	state := DotStatusState(status)
	if DotStatusTransientCandidate(status) && state != dots.StateLocalOnly {
		return false
	}
	switch state {
	case dots.StateInactive, dots.StateDisabled:
		// Inactive or disabled entries belong to another host; ignored entries are still configured and can carry a variant.
		return false
	default:
		return true
	}
}

func DotStatusIgnored(status DotStatus) bool {
	return DotStatusState(status) == dots.StateIgnored
}

func DotStatusNeedsAttention(status DotStatus) bool {
	state := DotStatusState(status)
	return state != dots.StateSynced && state != dots.StateIgnored
}

func DotStatusSyncActionLabel(status DotStatus) string {
	switch DotStatusState(status) {
	case dots.StateMissing:
		return "use repo"
	case dots.StateBroken:
		return "repair"
	case dots.StateLocalOnly:
		return "use local"
	case dots.StateRepoOnly:
		return "use repo"
	default:
		return "sync"
	}
}

func DotStatusFileCounts(status DotStatus) DotFileCounts {
	if status.Counts.Total() > 0 || status.FileCount <= 0 {
		return status.Counts
	}
	if DotStatusState(status) == dots.StateSynced {
		return DotFileCounts{Synced: status.FileCount}
	}
	return DotFileCounts{OutOfSync: status.FileCount}
}

func DotStatusesFileCounts(statuses []DotStatus) DotFileCounts {
	var total DotFileCounts
	for _, status := range statuses {
		if DotStatusState(status) == dots.StateIgnored {
			continue
		}
		counts := DotStatusFileCounts(status)
		total.Synced += counts.Synced
		total.OutOfSync += counts.OutOfSync
		total.Ignored += counts.Ignored
	}
	return total
}

func DotChildFileCounts(child DotChild, parentState dots.State) DotFileCounts {
	if child.Counts.Total() > 0 || child.FileCount <= 0 {
		return child.Counts
	}
	state := parentState
	if child.State != "" {
		state = child.State
	}
	if state == dots.StateSynced {
		return DotFileCounts{Synced: child.FileCount}
	}
	return DotFileCounts{OutOfSync: child.FileCount}
}

func DotStateIcon(state dots.State) string {
	switch state {
	case dots.StateSynced:
		return "✓"
	case dots.StateConflict, dots.StateUntrackedConflict, dots.StateAmbiguous:
		return "✗"
	case dots.StateNoSource:
		return "?"
	case dots.StateIgnored, dots.StateInactive, dots.StateDisabled:
		return "·"
	default:
		return "!"
	}
}

func DotStatusHasAction(status DotStatus, action dots.Action) bool {
	for _, candidate := range status.Actions {
		if candidate == action {
			return true
		}
	}
	if len(status.Actions) > 0 {
		return false
	}
	switch DotStatusState(status) {
	case dots.StateMissing, dots.StateBroken, dots.StateModified, dots.StateLocalOnly, dots.StateRepoOnly, dots.StateUntrackedConflict:
		if DotStatusState(status) == dots.StateUntrackedConflict {
			return action == dots.ActionUseRepo || action == dots.ActionUseLocal || action == dots.ActionIgnore
		}
		return action == dots.ActionSync || action == dots.ActionRemove || action == dots.ActionIgnore
	case dots.StateSynced:
		return action == dots.ActionRemove || action == dots.ActionIgnore
	case dots.StateConflict:
		return action == dots.ActionUseRepo || action == dots.ActionUseLocal || action == dots.ActionRemove || action == dots.ActionIgnore
	case dots.StateNoSource:
		return action == dots.ActionRemove || action == dots.ActionIgnore
	case dots.StateIgnored:
		return action == dots.ActionUnignore || action == dots.ActionRemove
	}
	return false
}

func healthForDotState(state dots.State) DotHealth {
	switch state {
	case dots.StateSynced:
		return HealthOK
	case dots.StateConflict, dots.StateUntrackedConflict:
		return HealthConflict
	case dots.StateNoSource:
		return HealthNoSource
	case dots.StateMissing, dots.StateBroken, dots.StateModified, dots.StateLocalOnly, dots.StateRepoOnly, dots.StateUntrackedLinked:
		return HealthMissing
	default:
		return DotHealth(state)
	}
}
