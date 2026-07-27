package tui

// Lets mouse click dispatch look up which zone was clicked without each view re-implementing the same scan loop.
type hitZoneProvider func(m Model) []toolFilterHitZone

func matchHitZone(provider hitZoneProvider, m Model, x, y int) (toolFilterHitZone, bool) {
	for _, zone := range provider(m) {
		if y == zone.y && x >= zone.start && x < zone.end {
			return zone, true
		}
	}
	return toolFilterHitZone{}, false
}
