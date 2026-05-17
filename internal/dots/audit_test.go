package dots_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestAuditIgnoreList_Empty(t *testing.T) {
	findings := dots.AuditIgnoreList(nil)
	if len(findings) != 0 {
		t.Fatalf("expected no findings for nil input, got %d", len(findings))
	}
	findings = dots.AuditIgnoreList([]string{})
	if len(findings) != 0 {
		t.Fatalf("expected no findings for empty input, got %d", len(findings))
	}
}

func TestAuditIgnoreList_Clean(t *testing.T) {
	// A well-formed allowlist pattern: nothing to report.
	patterns := []string{
		"*",
		"!/settings.json",
		"!/CLAUDE.md",
		"!/mcp.json",
	}
	findings := dots.AuditIgnoreList(patterns)
	if len(findings) != 0 {
		t.Fatalf("expected no findings for clean config, got %v", findings)
	}
}

func TestAuditIgnoreList_Duplicate(t *testing.T) {
	patterns := []string{"*.log", "cache", "*.log"}
	findings := dots.AuditIgnoreList(patterns)
	found := findingByType(findings, "duplicate")
	if found == nil {
		t.Fatalf("expected duplicate finding, got %v", findings)
	}
	// Indices[0] is the duplicate to remove, Indices[1] is the canonical to keep.
	if found.Indices[0] != 2 || found.Indices[1] != 0 {
		t.Fatalf("duplicate indices = %v, want [2, 0] (removable first)", found.Indices)
	}
}

func TestAuditIgnoreList_Contradiction_UnignoreThenIgnore(t *testing.T) {
	// Real-world case: !/skills/ then skills
	patterns := []string{
		"*",
		"!/skills/",
		"!/agents/",
		"skills",
	}
	findings := dots.AuditIgnoreList(patterns)
	found := findingByType(findings, "contradiction")
	if found == nil {
		t.Fatalf("expected contradiction finding, got %v", findings)
	}
	if found.Patterns[0] != "!/skills/" {
		t.Fatalf("dead pattern = %q, want %q", found.Patterns[0], "!/skills/")
	}
}

func TestAuditIgnoreList_Contradiction_Multiple(t *testing.T) {
	patterns := []string{
		"*",
		"!/skills/",
		"!/hooks/",
		"skills",
		"hooks",
	}
	findings := dots.AuditIgnoreList(patterns)
	contradictions := findingsByType(findings, "contradiction")
	if len(contradictions) != 2 {
		t.Fatalf("expected 2 contradictions, got %d: %v", len(contradictions), findings)
	}
}

func TestAuditIgnoreList_Redundant(t *testing.T) {
	// "*" already ignores everything; "cache" after it is redundant.
	patterns := []string{"*", "cache"}
	findings := dots.AuditIgnoreList(patterns)
	found := findingByType(findings, "redundant")
	if found == nil {
		t.Fatalf("expected redundant finding, got %v", findings)
	}
	if found.Indices[0] != 1 {
		t.Fatalf("redundant index = %v, want [1]", found.Indices)
	}
}

func TestAuditIgnoreList_Redundant_NotIfUnignoreBetween(t *testing.T) {
	// "*" ignores all, "!cache" un-ignores cache, "cache" re-ignores it.
	// The re-ignore is NOT redundant — it's a contradiction with the un-ignore,
	// but it does have an effect (re-ignores after un-ignore).
	patterns := []string{"*", "!cache", "cache"}
	findings := dots.AuditIgnoreList(patterns)
	redundant := findingsByType(findings, "redundant")
	if len(redundant) != 0 {
		t.Fatalf("expected no redundant finding when un-ignore sits between, got %v", redundant)
	}
}

func TestAuditIgnoreList_ShadowedInclude_First(t *testing.T) {
	// Include as first pattern — nothing before it to reverse.
	patterns := []string{"!/foo", "*"}
	findings := dots.AuditIgnoreList(patterns)
	found := findingByType(findings, "shadowed")
	if found == nil {
		t.Fatalf("expected shadowed finding, got %v", findings)
	}
	if found.Indices[0] != 0 {
		t.Fatalf("shadowed index = %v, want [0]", found.Indices)
	}
}

func TestAuditIgnoreList_ShadowedInclude_NoPriorIgnore(t *testing.T) {
	// Two includes in a row — second has nothing ignoring its target.
	patterns := []string{"cache", "!settings.json"}
	findings := dots.AuditIgnoreList(patterns)
	found := findingByType(findings, "shadowed")
	if found == nil {
		t.Fatalf("expected shadowed finding for !settings.json, got %v", findings)
	}
}

func TestAuditIgnoreList_ShadowedInclude_NotIfPriorIgnore(t *testing.T) {
	// "*" ignores all, then "!settings.json" is meaningful.
	patterns := []string{"*", "!/settings.json"}
	findings := dots.AuditIgnoreList(patterns)
	shadowed := findingsByType(findings, "shadowed")
	if len(shadowed) != 0 {
		t.Fatalf("expected no shadowed finding when * precedes include, got %v", shadowed)
	}
}

func TestAuditIgnoreList_RealWorldClaude(t *testing.T) {
	// Simulates the user's actual broken Claude config.
	patterns := []string{
		"*",
		"!/settings.json",
		"!/CLAUDE.md",
		"!/mcp.json",
		"!/plugins",
		"!/plugins/installed_plugins.json",
		"!/skills/",
		"!/agents/",
		"!/hooks/",
		"!/scripts/",
		"!/.omc-config.json",
		"!/keybindings.json",
		"!/statusline-command.sh",
		"skills",
		"hooks",
	}
	findings := dots.AuditIgnoreList(patterns)
	contradictions := findingsByType(findings, "contradiction")
	if len(contradictions) != 2 {
		t.Fatalf("expected 2 contradictions (skills + hooks), got %d: %v", len(contradictions), findings)
	}
}

func TestAuditIgnoreList_RealWorldCodex(t *testing.T) {
	patterns := []string{
		"*",
		"!/config.toml",
		"!/mcp.json",
		"!/AGENTS.md",
		"!/RTK.md",
		"!/rules/",
		"!/skills/",
		"/skills/.system/",
		"skills",
	}
	findings := dots.AuditIgnoreList(patterns)
	contradictions := findingsByType(findings, "contradiction")
	// !/skills/ overridden by skills
	if len(contradictions) < 1 {
		t.Fatalf("expected at least 1 contradiction, got %d: %v", len(contradictions), findings)
	}
}

func findingByType(findings []dots.AuditFinding, typ string) *dots.AuditFinding {
	for i := range findings {
		if findings[i].Type == typ {
			return &findings[i]
		}
	}
	return nil
}

func findingsByType(findings []dots.AuditFinding, typ string) []dots.AuditFinding {
	var out []dots.AuditFinding
	for _, f := range findings {
		if f.Type == typ {
			out = append(out, f)
		}
	}
	return out
}
