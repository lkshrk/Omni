package config_test

import (
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestParseSkillLock(t *testing.T) {
	data := `{"version":3,"skills":{"frontend-design":{"source":"vercel-labs/agent-skills","sourceType":"github","sourceUrl":"https://github.com/vercel-labs/agent-skills","ref":"main","skillPath":"skills/frontend-design","skillFolderHash":"abc123","installedAt":"2026-06-01T00:00:00Z","updatedAt":"2026-06-01T00:00:00Z"}}}`
	lock, err := config.ParseSkillLock([]byte(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e, ok := lock.Skills["frontend-design"]
	if !ok {
		t.Fatal("missing frontend-design entry")
	}
	if e.Source != "vercel-labs/agent-skills" || e.Ref != "main" || e.SkillFolderHash != "abc123" {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestSkillLockPathPrefersXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")
	if got := config.SkillLockPath("/home/u"); got != filepath.Join("/tmp/xdgstate", "skills", ".skill-lock.json") {
		t.Fatalf("xdg path wrong: %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	if got := config.SkillLockPath("/home/u"); got != filepath.Join("/home/u", ".agents", ".skill-lock.json") {
		t.Fatalf("home path wrong: %s", got)
	}
}
