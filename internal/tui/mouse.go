package tui

// hitZoneProvider returns the clickable pill-bar zones for the model's
// current view, so mouse click dispatch can look up "which zone was clicked"
// without each view re-implementing the same scan loop.
type hitZoneProvider func(m Model) []toolFilterHitZone

// matchHitZone returns the first zone (from provider(m)) containing (x, y),
// so callers only need to handle what happens on a hit, not how to find one.
func matchHitZone(provider hitZoneProvider, m Model, x, y int) (toolFilterHitZone, bool) {
	for _, zone := range provider(m) {
		if y == zone.y && x >= zone.start && x < zone.end {
			return zone, true
		}
	}
	return toolFilterHitZone{}, false
}
