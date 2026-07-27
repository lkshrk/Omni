package tui

import (
	"strings"
	"testing"
)

func TestScanStatus_NamesProviderAndToolWhileScanning(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.progressGen = 3
	m.scanningProviders = map[string]bool{"node": true, "system": true}
	m.providerScanToolCounts = map[string]int{"node": 2, "system": 1}
	m.providerScanToolDone = map[string]int{}
	m.providerScanLabels = map[string]string{"node": "node/bun", "system": "system/brew"}
	m.refreshToolTotal = 3

	got := drive(m, progressMsg{gen: 3, refreshProvider: "node", refreshToolName: "typescript"})
	if got.progressText != "Refreshing tools… 1/3: node/bun/typescript" {
		t.Fatalf("progressText = %q, want the scanning provider and tool named", got.progressText)
	}

	got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "node"})
	if got.progressText != "Refreshing tools… 2/3: system/brew" {
		t.Fatalf("progressText = %q, want the remaining provider named after node completed", got.progressText)
	}
}

func TestScanStatus_AggregatesConcurrentProvidersWithoutJitter(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.scanningProviders = map[string]bool{"node": true, "system": true, "cargo": true}
	m.providerScanToolCounts = map[string]int{"node": 2, "system": 9, "cargo": 1}
	m.providerScanLabels = map[string]string{"node": "node/bun", "system": "system/brew"}
	m.refreshToolTotal = 12

	want := "Refreshing tools… 0/12: system/brew +2"
	for range 50 {
		if got := activityLabel(m); got != want {
			t.Fatalf("activityLabel = %q, want %q", got, want)
		}
	}
}

func TestScanStatus_NamesProviderWhileCheckingUpdates(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.providerScanLabels = map[string]string{"node": "node/bun", "system": "system/brew"}
	m.providerScanToolCounts = map[string]int{"node": 2, "system": 9}

	cmds := m.startProviderOutdatedChecks([]string{"node", "system"}, m.scanGen)
	if len(cmds) != 2 {
		t.Fatalf("started %d outdated checks, want 2", len(cmds))
	}
	if got := activityLabel(m); got != "Checking updates… 0/2: system/brew +1" {
		t.Fatalf("activityLabel = %q, want the update check to name its provider", got)
	}

	got := drive(m, providerOutdatedCheckedMsg{gen: m.scanGen, provider: "system"})
	if label := activityLabel(got); label != "Checking updates… 1/2: node/bun" {
		t.Fatalf("activityLabel = %q, want the remaining provider named", label)
	}
}

func TestDiscoveredRefreshed_ReleasesStreamSoDescriptionsReportProgress(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	ch, progressGen := m.beginProgressStream()
	m.discoveryProgressCh = ch
	m.discoveryRefreshing = true
	m.allTools = baseModel(nil).allTools

	got := drive(m, discoveredRefreshedMsg{gen: m.discoveryGen})

	if got.progressCh == ch {
		t.Fatal("discovery stream still owned after discoveredRefreshedMsg")
	}
	if got.progressGen == progressGen {
		t.Fatal("progressGen not advanced when the discovery stream was released")
	}
}

func TestStartDescriptionRefresh_OwnsStreamAfterDiscoveryReleasesIt(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	ch, _ := m.beginProgressStream()
	m.discoveryProgressCh = ch
	m.releaseDiscoveryProgressStream()

	m.startDescriptionRefresh()

	if m.progressCh == nil {
		t.Fatal("description refresh got no progress stream, so its phase would render silently")
	}
	if !strings.Contains(m.progressText, "descriptions") {
		t.Fatalf("progressText = %q, want the description phase named", m.progressText)
	}
}
