package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddDiscoveredRejectsCaseInsensitiveDuplicates(t *testing.T) {
	t.Parallel()
	out := map[string]string{"Skill": "/first"}
	err := addDiscovered(out, "skill", "/second")
	if err == nil || !strings.Contains(err.Error(), "duplicate discovered skill") {
		t.Fatalf("error = %v, want duplicate discovered skill", err)
	}
	if !strings.Contains(err.Error(), `"Skill"`) || !strings.Contains(err.Error(), `"skill"`) {
		t.Fatalf("error = %v, want both spellings named", err)
	}
	if len(out) != 1 {
		t.Fatalf("out = %v, want the colliding name rejected", out)
	}
}

func TestAddDiscoveredKeepsSameDirectoryOfferedTwice(t *testing.T) {
	t.Parallel()
	out := map[string]string{"skill": "/first"}
	if err := addDiscovered(out, "skill", "/first"); err != nil {
		t.Fatalf("re-offering the same directory: %v", err)
	}
}

func TestDiscoverSkillsRejectsUppercaseManifestName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestSkill(t, filepath.Join(root, "skills", "lower"), "skill", "lower case")
	writeTestSkill(t, filepath.Join(root, "skills", "upper"), "Skill", "upper case")

	discovered, warnings, err := discoverSkills(root)
	if err != nil {
		t.Fatalf("discoverSkills: %v", err)
	}
	if len(discovered) != 1 || discovered["skill"] == "" {
		t.Fatalf("discovered = %v, want only the lower-case name", discovered)
	}
	if !containsSubstring(warnings, "skipping skill directory skills/upper") {
		t.Fatalf("warnings = %v, want the upper-case manifest skipped", warnings)
	}
}
