package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	textutil "github.com/lkshrk/omni/internal/text"
)

// The offline first-run state: onboarding is done, the overlay is up, and provider scans that never answer keep setupReloadPending() true forever.
func stuckPostSetupModel() Model {
	m := baseModel(threeTools())
	m.mode = viewStatus
	m.setupBackgroundMode = viewStatus
	m.setupComplete = true
	m.loading = true
	m.progressText = "Loading tools…"
	m.scanningProviders = map[string]bool{"brew": true, "npm": true}
	m.setupReloading = true
	return m
}

func TestSetupReloadOverlayTimesOutWhenScansHang(t *testing.T) {
	t.Parallel()
	m := stuckPostSetupModel()

	got := drive(m, setupReloadTimeoutMsg{gen: m.setupReloadGen})
	if got.setupReloading {
		t.Fatal("overlay should clear once the timeout fires with scans still pending")
	}
	if !got.setupReloadPending() {
		t.Fatal("the hung scans should still be pending; only the overlay goes away")
	}
	if !strings.Contains(got.statusMsg, "background scans still running") {
		t.Fatalf("statusMsg = %q, want the still-running explanation", got.statusMsg)
	}
	for _, name := range []string{"brew", "npm"} {
		if !strings.Contains(got.statusMsg, name) {
			t.Fatalf("statusMsg = %q, want it to name the pending provider %q", got.statusMsg, name)
		}
	}
	footer := stripANSIEscapeSequences(renderStatusBar(got))
	if !strings.Contains(footer, "background scans still running") {
		t.Fatalf("footer = %q, want the dismissal status", footer)
	}
}

func TestSetupReloadTimeoutIgnoresStaleGeneration(t *testing.T) {
	t.Parallel()
	m := stuckPostSetupModel()
	m.setupReloadGen = 4

	got := drive(m, setupReloadTimeoutMsg{gen: 3})
	if !got.setupReloading {
		t.Fatal("a timeout armed for an earlier overlay must not dismiss the current one")
	}
	if got.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want no status from a stale timeout", got.statusMsg)
	}
}

func TestSetupReloadTimeoutIsArmedWhenOnboardingFinishes(t *testing.T) {
	t.Parallel()
	m := drive(setupStep5Model(), pressRune('n'))
	got := drive(m, dangerOpDoneMsg{action: "disable-dots", detail: "dots disabled"})
	if !got.setupReloading {
		t.Fatal("finishing onboarding should raise the reload overlay")
	}
	if got.setupReloadGen == m.setupReloadGen {
		t.Fatal("raising the overlay should arm a timeout generation")
	}
}

func TestSetupReloadFinishDisarmsTimeout(t *testing.T) {
	t.Parallel()
	m := stuckPostSetupModel()
	armedGen := m.setupReloadGen

	m.finishSetupReload()
	if m.setupReloadGen == armedGen {
		t.Fatal("finishing the reload should invalidate the armed timeout")
	}

	got := drive(m, setupReloadTimeoutMsg{gen: armedGen})
	if got.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want a disarmed timeout to stay silent", got.statusMsg)
	}
}

func TestEscapeDismissesSetupReloadOverlay(t *testing.T) {
	t.Parallel()
	got := drive(stuckPostSetupModel(), pressEsc())
	if got.setupReloading {
		t.Fatal("esc should dismiss the post-setup overlay immediately")
	}
	if !strings.Contains(got.statusMsg, "background scans still running") {
		t.Fatalf("statusMsg = %q, want the still-running explanation", got.statusMsg)
	}
}

func TestQuitWhileSetupReloadOverlayIsUp(t *testing.T) {
	t.Parallel()
	for _, quitKey := range []tea.Msg{pressRune('q'), tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}} {
		model, _ := stuckPostSetupModel().Update(quitKey)
		armed := model.(Model)
		if !armed.confirmQuit {
			t.Fatalf("%v: first press should arm the quit confirmation behind the overlay", quitKey)
		}
		footer := stripANSIEscapeSequences(renderStatusBar(armed))
		if !strings.Contains(footer, "again to quit") {
			t.Fatalf("%v: armed footer = %q, want the press-again hint", quitKey, footer)
		}

		_, cmd := armed.Update(quitKey)
		if !cmdQuits(cmd) {
			t.Fatalf("%v: second press should quit while the overlay is up", quitKey)
		}
	}
}

// A silently disarmed quit confirmation is what made the stuck overlay look unquittable: the second press re-armed instead of exiting.
func TestExpiredQuitConfirmationSaysSo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		press tea.Msg
		want  string
	}{
		{pressRune('q'), "quit confirmation expired — press q again"},
		{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "quit confirmation expired — press ctrl+c again"},
	} {
		armed := drive(stuckPostSetupModel(), tc.press)
		got := drive(armed, confirmTimeoutMsg{gen: armed.confirmGen})
		if got.confirmQuit {
			t.Fatal("the quit confirmation should expire")
		}
		if got.statusMsg != tc.want {
			t.Fatalf("statusMsg = %q, want %q", got.statusMsg, tc.want)
		}
	}
}

// The overlay composes over a live tab, so the frame must carry that tab's footer once — not the setup footer, and not a second stacked copy.
func TestSetupReloadOverlayKeepsBackgroundFooter(t *testing.T) {
	t.Parallel()
	for _, background := range []viewMode{viewStatus, viewList, viewDots, viewSkills, viewGroups, viewSettings} {
		m := stuckPostSetupModel()
		m.mode = background
		m.setupBackgroundMode = background
		m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
		m.dotsEntries = []app.DotStatus{{Name: "zsh"}}

		bg := m
		bg.setupReloading = false
		bg.loading = true
		bg.visibleTools = nil
		footer := textutil.SymbolsFromEnv().Apply(renderStatusBar(bg))
		want := strings.TrimRight(stripANSIEscapeSequences(footer), " ")

		lines := strings.Split(stripANSIEscapeSequences(m.View().Content), "\n")
		last := strings.TrimRight(lines[len(lines)-1], " ")
		if last != want {
			t.Fatalf("background %v: overlay footer = %q, want the background footer %q", background, last, want)
		}
		if n := strings.Count(strings.Join(lines, "\n"), "switch tab"); n != 1 {
			t.Fatalf("background %v: footer legend appears %d times in the frame, want exactly 1", background, n)
		}
	}
}
