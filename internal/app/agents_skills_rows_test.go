package app

import (
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
