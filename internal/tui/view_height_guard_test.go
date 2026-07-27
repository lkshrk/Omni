package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func assertFrameFitsHeight(t *testing.T, out string, height int) {
	t.Helper()
	if n := lipgloss.Height(out); n > height {
		t.Fatalf("frame is %d lines, terminal is %d", n, height)
	}
}

// A frame taller than the terminal scrolls the screen, so every following frame lands one row lower and the old footer stays behind.
func TestViewNeverEmitsMoreLinesThanTerminalHeight(t *testing.T) {
	t.Parallel()
	for _, height := range []int{1, 2, 3, 4, 6, 10, 24, 40} {
		for _, mode := range []viewMode{viewStatus, viewList, viewDots, viewSettings, viewSkills, viewGroups} {
			m := worstCaseDashboardModel(100)
			m.mode = mode
			m.height = height
			m.statusMsg = strings.Repeat("still refreshing every configured provider ", 4)
			assertFrameFitsHeight(t, m.View().Content, height)
		}
	}
}

// The post-setup overlay composes its own layer over a full tab, reaching the frame through a different path than the plain tabs do.
func TestPostSetupOverlayNeverExceedsTerminalHeight(t *testing.T) {
	t.Parallel()
	tools := make([]*app.ToolView, 0, 60)
	dotEntries := make([]app.DotStatus, 0, 60)
	for i := range 60 {
		name := "tool-" + strconv.Itoa(i)
		tools = append(tools, &app.ToolView{Name: name, Provider: "brew", Installed: true, Tracked: true})
		dotEntries = append(dotEntries, app.DotStatus{Name: name, State: dots.StateSynced, Health: app.HealthOK})
	}

	for _, height := range []int{1, 2, 3, 4, 6, 10, 24, 40} {
		for _, background := range []viewMode{viewStatus, viewList, viewDots, viewSkills} {
			m := worstCaseDashboardModel(100)
			m.allTools = tools
			m.applyFilter()
			m.dotsEntries = dotEntries
			m.dotsLoaded = true
			m.mode = background
			m.setupBackgroundMode = background
			m.setupComplete = true
			m.setupReloading = true
			m.loading = true
			m.progressText = "Loading tools…"
			m.scanningProviders = map[string]bool{"brew": true, "npm": true}
			m.height = height
			m.statusMsg = strings.Repeat("still refreshing every configured provider ", 4)
			assertFrameFitsHeight(t, m.View().Content, height)
		}
	}
}

func TestClipFrameKeepsShorterFramesIntact(t *testing.T) {
	t.Parallel()
	frame := "one\ntwo\nthree"
	if got := clipFrame(frame, 5); got != frame {
		t.Fatalf("clipFrame = %q, want the frame untouched", got)
	}
	if got := clipFrame(frame, 0); got != frame {
		t.Fatalf("clipFrame with unknown height = %q, want the frame untouched", got)
	}
	if got := clipFrame(frame, 2); got != "one\ntwo" {
		t.Fatalf("clipFrame = %q, want the frame clipped to 2 lines", got)
	}
}
