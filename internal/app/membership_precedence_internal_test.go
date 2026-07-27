package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCollectDots_HostGroupDefinitionWinsOverReusable(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Package: "nvim-work"}}},
			{Name: "host", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Package: "nvim-host"}}},
		},
		Hosts: map[string][]string{"host": {"work"}},
	}

	effective, _, ok := effectiveHostGroups(cfg, cfg.Groups, "host")
	if !ok {
		t.Fatal("effectiveHostGroups should resolve for host")
	}
	if len(effective) == 0 || !effective[0].IsHost() {
		t.Fatalf("effective groups must start with the host group, got %+v", effective)
	}

	entries := collectDots(cfg, effective)
	if len(entries) != 1 {
		t.Fatalf("nvim should dedup to a single entry, got %+v", entries)
	}
	if entries[0].Package != "nvim-host" {
		t.Fatalf("resolved nvim package = %q, want nvim-host (host group wins)", entries[0].Package)
	}
}
