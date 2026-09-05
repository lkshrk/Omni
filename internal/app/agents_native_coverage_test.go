package app

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestGatherNativeAgentsKeepsHealthyClientWhenAnotherFails(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
		executor.MatchRule{Pattern: "codex plugin list --json", Response: executor.MockCall{Err: errors.New("codex timed out")}},
	)

	inventory := a.gatherNativeAgents(t.Context())
	if len(inventory.Observations) == 0 {
		t.Fatalf("codex failure erased claude observations: %#v", inventory)
	}
	for _, observation := range inventory.Observations {
		if observation.Source != "claude" {
			t.Fatalf("observation from a failed client: %#v", observation)
		}
	}
	if !slices.ContainsFunc(inventory.Observations, func(o agentObservation) bool {
		return o.Kind == agentKindPlugin && o.Identity == "demo@official"
	}) {
		t.Fatalf("claude drift missing: %#v", inventory.Observations)
	}
	assertNativeCoverage(t, inventory, map[string]bool{"claude": true, "codex": false})
	if err := inventory.Err(); err == nil || !strings.Contains(err.Error(), "codex timed out") {
		t.Fatalf("coverage error = %v", err)
	}
}

func TestGatherNativeAgentsReportsEveryFailedClient(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		executor.MatchRule{Pattern: "claude plugins list --json", Response: executor.MockCall{Err: errors.New("claude exploded")}},
		executor.MatchRule{Pattern: "codex plugin list --json", Response: executor.MockCall{Err: errors.New("codex exploded")}},
	)

	inventory := a.gatherNativeAgents(t.Context())
	if len(inventory.Observations) != 0 {
		t.Fatalf("failed clients produced observations: %#v", inventory.Observations)
	}
	assertNativeCoverage(t, inventory, map[string]bool{"claude": false, "codex": false})
	err := inventory.Err()
	if err == nil {
		t.Fatal("total inventory failure reported clean coverage")
	}
	for _, want := range []string{"claude exploded", "codex exploded"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("coverage error %v is missing %q", err, want)
		}
	}
}

func TestGatherNativeAgentsCoversUnavailableClients(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{})
	inventory := a.gatherNativeAgents(t.Context())
	assertNativeCoverage(t, inventory, map[string]bool{"claude": true, "codex": true})
	if err := inventory.Err(); err != nil {
		t.Fatalf("absent clients reported a gap: %v", err)
	}
}

func assertNativeCoverage(t *testing.T, inventory nativeAgentInventory, want map[string]bool) {
	t.Helper()
	if len(inventory.Coverage) != len(want) {
		t.Fatalf("coverage = %#v, want %d clients", inventory.Coverage, len(want))
	}
	for _, coverage := range inventory.Coverage {
		complete, known := want[coverage.Client]
		if !known {
			t.Fatalf("unexpected client in coverage: %#v", coverage)
		}
		if coverage.Complete() != complete {
			t.Fatalf("coverage for %s = %#v, want complete=%v", coverage.Client, coverage, complete)
		}
	}
}
