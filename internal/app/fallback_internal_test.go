package app

import (
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestAutomaticFallbackUsableStatusRules(t *testing.T) {
	base := config.FallbackSpec{
		Status: config.FallbackStatusUnverified,
		Commands: config.FallbackCommands{
			Install: "install rg",
			Check:   "command -v rg",
		},
	}
	tests := []struct {
		name        string
		fallback    *config.FallbackSpec
		allowFailed bool
		want        bool
	}{
		{name: "nil", want: false},
		{name: "unverified usable", fallback: &base, want: true},
		{name: "failed blocked", fallback: fallbackWithStatus(base, config.FallbackStatusFailed), want: false},
		{name: "failed allowed for retry", fallback: fallbackWithStatus(base, config.FallbackStatusFailed), allowFailed: true, want: true},
		{name: "unresolved blocked", fallback: fallbackWithStatus(base, config.FallbackStatusUnresolved), want: false},
		{name: "unsupported blocked", fallback: fallbackWithStatus(base, config.FallbackStatusUnsupported), want: false},
		{name: "missing install blocked", fallback: fallbackWithCommands(base, "", "command -v rg"), want: false},
		{name: "missing check blocked", fallback: fallbackWithCommands(base, "install rg", ""), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := automaticFallbackUsable(tt.fallback, tt.allowFailed); got != tt.want {
				t.Fatalf("automaticFallbackUsable = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutomaticFallbackUsableForToolHandlesConfiguredMissingFallback(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	a := New(cfgPath)
	if err := config.Save(cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	usable, err := a.automaticFallbackUsableForTool("rg", false)
	if err != nil {
		t.Fatalf("automaticFallbackUsableForTool configured no fallback: %v", err)
	}
	if usable {
		t.Fatal("automaticFallbackUsableForTool = true, want false without fallback")
	}

	if _, err := a.automaticFallbackUsableForTool("missing", false); err == nil {
		t.Fatal("automaticFallbackUsableForTool missing err = nil, want missing tool error")
	}
}

func fallbackWithStatus(base config.FallbackSpec, status string) *config.FallbackSpec {
	base.Status = status
	return &base
}

func fallbackWithCommands(base config.FallbackSpec, install, check string) *config.FallbackSpec {
	base.Commands.Install = install
	base.Commands.Check = check
	return &base
}
