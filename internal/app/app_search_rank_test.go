package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSortByProviderRank(t *testing.T) {
	t.Parallel()
	provs := []config.ToolInstallSpec{
		{Provider: "pip"},
		{Provider: "zebra"},
		{Provider: "brew"},
		{Provider: "apt"},
		{Provider: "node"},
	}
	sortByProviderRank(provs, map[string]int{"brew": 0, "node": 1, "pip": 1})

	got := make([]string, len(provs))
	for i, p := range provs {
		got[i] = p.Provider
	}
	want := []string{"brew", "node", "pip", "apt", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
