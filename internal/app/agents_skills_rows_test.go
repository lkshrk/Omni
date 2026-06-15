package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSkillUpdatedDate(t *testing.T) {
	if got := skillUpdatedDate("2026-06-01T12:34:56Z"); got != "2026-06-01" {
		t.Errorf("got %q, want 2026-06-01", got)
	}
	if got := skillUpdatedDate(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestAgentsWithSkill(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "frontend-design"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// codex dir intentionally absent → codex must not match.

	got := agentsWithSkill(home, "frontend-design")
	if !reflect.DeepEqual(got, []string{"claude-code"}) {
		t.Fatalf("agents = %v, want [claude-code]", got)
	}
	if got := agentsWithSkill(home, "missing"); len(got) != 0 {
		t.Fatalf("agents for missing skill = %v, want none", got)
	}
}
