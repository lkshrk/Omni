package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestFatalValidationErrorsDropsFallback(t *testing.T) {
	t.Parallel()
	errs := []config.ValidationError{
		{Path: `$.tools."pi".fallback.source`, Message: "github fallback source requires owner and repo"},
		{Path: `$.tools."glow".fallback.commands.check`, Message: "fallback check command is required"},
		{Path: `$.tools."y"`, Message: "tool name is required"},
		{Path: `$.agents.skills[0].name`, Message: "skill name is required"},
	}
	fatal := fatalValidationErrors(errs)
	if len(fatal) != 2 {
		t.Fatalf("want 2 fatal (non-fallback) errors, got %d: %v", len(fatal), fatal)
	}
	for _, e := range fatal {
		if strings.Contains(e.Path, ".fallback") {
			t.Errorf("fallback error leaked into fatal set: %v", e)
		}
	}
}

func TestFatalValidationErrorsAllFallbackYieldsNone(t *testing.T) {
	t.Parallel()
	errs := []config.ValidationError{
		{Path: `$.tools."pi".fallback.source`, Message: "github fallback source requires owner and repo"},
	}
	if fatal := fatalValidationErrors(errs); len(fatal) != 0 {
		t.Fatalf("all-fallback errors must not be fatal, got %v", fatal)
	}
}

func TestLoadConfigIgnoresFallbackValidationErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(ctx, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	badFallback := `{"version":9,"tools":{"rg":{"providers":[{"provider":"brew"}],"fallback":{"source":{"type":"github","owner":"o","repo":"r"},"status":"bogus","recipe":{"type":"github_release_asset"}}}}}`
	if err := os.WriteFile(path, []byte(badFallback), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := a.loadConfig(); err != nil {
		t.Fatalf("load must ignore fallback validation errors, got: %v", err)
	}

	badTool := `{"version":9,"tools":{"   ":{"providers":[{"provider":"brew"}]}}}`
	if err := os.WriteFile(path, []byte(badTool), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := a.loadConfig(); err == nil {
		t.Fatal("load must still fail on non-fallback validation errors")
	}
}
