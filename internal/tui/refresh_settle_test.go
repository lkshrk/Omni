package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// Descriptions are populated so the description leg only starts in the tests that ask for it.
func refreshTestModel(t *testing.T, tools ...*app.ToolView) Model {
	t.Helper()
	if len(tools) == 0 {
		tools = []*app.ToolView{{Name: "ripgrep", Provider: "brew", Installed: true, Tracked: true}}
	}
	for _, tool := range tools {
		tool.Description = "described"
	}
	m := baseModel(tools)
	m.app = newScanPlanTestApp(t, &scanPlanProvider{name: "brew"})
	m.upgradingKeys = make(map[string]bool)
	return m
}

// Start through the user entry point so the real owner raises the gate.
func startRefreshByKey(t *testing.T, m Model) Model {
	t.Helper()
	got := drive(m, pressRune('R'))
	if len(got.scanningProviders) == 0 {
		t.Fatal("R should have started the provider scans")
	}
	if !got.loading {
		t.Fatal("R should raise the gate")
	}
	return got
}

func toolsMissingDescription() []*app.ToolView {
	return []*app.ToolView{{Name: "jq", Provider: "brew"}}
}

// The final refresh leg must clear m.loading, including on error, or every key remains gated.
func TestToolRefresh_ClearsLoadingWhenTheLastLegFinishes(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.scanningProviders = map[string]bool{"brew": true}

	got := drive(m, providerScannedMsg{gen: m.scanGen, provider: "brew"})
	if !got.loading {
		t.Fatal("loading should stay set while the snapshot and orphan legs run")
	}
	got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen})
	if !got.loading {
		t.Fatal("loading should stay set while the provider snapshot is pending")
	}
	got = drive(got, allProvidersDoneMsg{gen: got.scanGen})
	if !got.loading {
		t.Fatal("loading should stay set while the update check runs")
	}
	got = drive(got,
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)

	if got.toolRefreshPending() {
		t.Fatalf("refresh still pending: %+v", got.spinnerActivityState())
	}
	if got.loading {
		t.Fatal("loading should clear once the last refresh leg finishes; it gates every key press")
	}
}

func TestToolRefresh_ClearsLoadingWhenTheLastLegFails(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.scanningProviders = map[string]bool{"brew": true}

	got := drive(m, providerScannedMsg{gen: m.scanGen, provider: "brew"})
	got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen})
	got = drive(got,
		allProvidersDoneMsg{gen: got.scanGen},
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen, err: errors.New("boom")},
	)

	if got.loading {
		t.Fatal("a failed refresh must not leave the TUI gated behind loading")
	}
}

func TestToolRefresh_KeepsLoadingWhileMigrating(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.migrating = true
	m.scanningProviders = map[string]bool{"brew": true}

	got := drive(m, providerScannedMsg{gen: m.scanGen, provider: "brew"})
	got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen})
	got = drive(got,
		allProvidersDoneMsg{gen: got.scanGen},
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)

	if !got.loading {
		t.Fatal("a migration owns loading; the refresh legs must not clear it")
	}
}

// The wedge was a missed hand-off: one leg cleared its own flag and no one cleared m.loading. Which
// leg finishes last is timing-dependent, so every ordering has to clear it, not just the common one.
func TestToolRefresh_ClearsLoadingWhicheverLegFinishesLast(t *testing.T) {
	legs := map[string]func(m Model) []tea.Msg{
		"provider snapshot last": func(m Model) []tea.Msg {
			return []tea.Msg{
				discoveredRefreshedMsg{gen: m.discoveryGen},
				providerOutdatedCheckedMsg{gen: m.scanGen, provider: "brew"},
				outdatedProvidersDoneMsg{gen: m.scanGen},
				allProvidersDoneMsg{gen: m.scanGen},
			}
		},
		"orphan scan last": func(m Model) []tea.Msg {
			return []tea.Msg{
				allProvidersDoneMsg{gen: m.scanGen},
				providerOutdatedCheckedMsg{gen: m.scanGen, provider: "brew"},
				outdatedProvidersDoneMsg{gen: m.scanGen},
				discoveredRefreshedMsg{gen: m.discoveryGen},
			}
		},
		"update check last": func(m Model) []tea.Msg {
			return []tea.Msg{
				allProvidersDoneMsg{gen: m.scanGen},
				discoveredRefreshedMsg{gen: m.discoveryGen},
				providerOutdatedCheckedMsg{gen: m.scanGen, provider: "brew"},
				outdatedProvidersDoneMsg{gen: m.scanGen},
			}
		},
	}
	for name, order := range legs {
		t.Run(name, func(t *testing.T) {
			m := refreshTestModel(t)
			got := startRefreshByKey(t, m)
			got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
			for _, msg := range order(got) {
				got = drive(got, msg)
			}
			if got.toolRefreshPending() {
				t.Fatalf("refresh still pending: %+v", got.spinnerActivityState())
			}
			if got.loading {
				t.Fatal("loading survived the last leg; every key press stays gated")
			}
		})
	}
}

// The description leg is the one the channel-identity heuristic misread: the orphan leg releases the
// discovery stream, so startDescriptionRefresh opens a third stream that matched neither registered
// channel and the last leg concluded a foreign operation owned the gate. Every step here is a real
// message and the description leg must start on its own, so hand-setting descRefreshing would hide
// exactly the misclassification under test.
func TestToolRefresh_ClearsLoadingWhenDescriptionsFinishLast(t *testing.T) {
	got := startRefreshByKey(t, refreshTestModel(t))

	got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
	if !got.providerSnapshotRefreshing || !got.discoveryRefreshing {
		t.Fatal("the last provider scan should hand off to the snapshot and orphan legs")
	}
	got = drive(got, allProvidersDoneMsg{gen: got.scanGen})
	got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen, discovered: toolsMissingDescription()})

	if !got.descRefreshing {
		t.Fatal("a description-less tool should have started the description leg")
	}
	if got.progressCh == nil {
		t.Fatal("the description leg should have taken over the released stream")
	}

	got = drive(got,
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)
	if !got.loading {
		t.Fatal("loading should stay set while the description leg runs")
	}

	got = drive(got, descRefreshDoneMsg{gen: got.descRefreshGen})

	if got.toolRefreshPending() {
		t.Fatalf("refresh still pending: %+v", got.spinnerActivityState())
	}
	if got.loading {
		t.Fatal("loading survived the description leg; every key press stays gated")
	}
}

// A bulk upgrade sets m.loading too and clears it from its own progress stream. A refresh leg landing
// mid-upgrade must not release that gate, or a second operation could start on top of the first.
func TestToolRefresh_DoesNotReleaseAnotherOperationsGate(t *testing.T) {
	m := refreshTestModel(t, &app.ToolView{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true})
	// Automatic post-load scans leave the gate down so a keypress can start the concurrent upgrade.
	m.startCurrentProviderScans()
	if m.loading {
		t.Fatal("the automatic refresh should not raise the gate")
	}

	got := drive(m, pressRune('U'))
	if !got.loading || len(got.upgradingKeys) == 0 {
		t.Fatalf("U should have started the bulk upgrade: loading=%v keys=%v", got.loading, got.upgradingKeys)
	}

	got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
	got = drive(got,
		discoveredRefreshedMsg{gen: got.discoveryGen},
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)
	got = drive(got, allProvidersDoneMsg{gen: got.scanGen})

	if got.toolRefreshPending() {
		t.Fatalf("refresh still pending: %+v", got.spinnerActivityState())
	}
	if !got.loading {
		t.Fatal("the refresh released the upgrade's gate; input is live during a bulk upgrade")
	}
}

// Shared progress must not release the refresh-owned gate or input can mutate the list mid-refresh.
func TestToolRefresh_ProgressDoneDoesNotReleaseTheRefreshGate(t *testing.T) {
	got := startRefreshByKey(t, refreshTestModel(t))
	got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
	if !got.providerSnapshotRefreshing || !got.discoveryRefreshing {
		t.Fatal("both refresh legs should be in flight")
	}

	got = drive(got, progressDoneMsg{gen: got.progressGen})

	if !got.loading {
		t.Fatal("another operation's progressDoneMsg released the refresh's gate mid-refresh")
	}

	got = drive(got,
		allProvidersDoneMsg{gen: got.scanGen},
		discoveredRefreshedMsg{gen: got.discoveryGen},
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)
	if got.loading {
		t.Fatal("the refresh must still clear its own gate once its last leg finishes")
	}
}

// A local operation raises the same gate without opening a stream at all, which the channel-identity
// heuristic could not see: a refresh leg landing afterwards released it and let input go live while
// the delete was still running.
func TestToolRefresh_DoesNotReleaseALocalOperationsGate(t *testing.T) {
	m := refreshTestModel(t)
	m.startCurrentProviderScans()

	got := drive(m, pressRune('d'), pressRune('d'))
	if !got.loading {
		t.Fatal("a confirmed delete should raise the gate")
	}

	got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
	got = drive(got,
		allProvidersDoneMsg{gen: got.scanGen},
		discoveredRefreshedMsg{gen: got.discoveryGen},
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)

	if got.toolRefreshPending() {
		t.Fatalf("refresh still pending: %+v", got.spinnerActivityState())
	}
	if !got.loading {
		t.Fatal("the refresh released the delete's gate; input is live while the delete runs")
	}
}

// The orphan leg settles before it starts the description leg, so the gate is briefly down with a leg
// still in flight; a second refresh from there duplicates the work over the same DB.
func TestToolRefresh_SecondRefreshBlockedWhileDescriptionsRun(t *testing.T) {
	got := startRefreshByKey(t, refreshTestModel(t))
	got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
	got = drive(got,
		allProvidersDoneMsg{gen: got.scanGen},
		providerOutdatedCheckedMsg{gen: got.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: got.scanGen},
	)
	got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen, discovered: toolsMissingDescription()})

	if !got.descRefreshing {
		t.Fatal("the description leg should be in flight")
	}
	if got.loading {
		t.Fatal("the gate is expected to be down here; that is what makes the second refresh reachable")
	}

	descGen := got.descRefreshGen
	scanGen := got.scanGen
	got = drive(got, pressRune('R'))

	if got.descRefreshGen != descGen || got.scanGen != scanGen {
		t.Fatal("R started a second refresh while the description leg was still running")
	}
}
