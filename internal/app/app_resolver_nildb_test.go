package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestInstallCandidateUsableCached_NilDBDoesNotPanic(t *testing.T) {
	a := &App{}
	candidate := config.ToolInstallSpec{Provider: "brew", Package: "jq"}
	usable, skip := a.installCandidateUsableCached(context.Background(), "jq", candidate, map[string]bool{"brew": true}, nil)
	if !usable {
		t.Fatalf("candidate must stay usable without a DB, got skip %+v", skip)
	}
}
