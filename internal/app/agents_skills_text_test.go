package app

import (
	"strings"
	"testing"
)

func TestRestoreSkillsSummaryText(t *testing.T) {
	res := RestoreSkillsResult{Installed: []string{"a", "b"}, Failed: []SkillFailure{{Name: "c", Message: "boom"}}}
	out := RestoreSkillsSummaryText(res)
	if !strings.Contains(out, "2 installed") || !strings.Contains(out, "1 failed") {
		t.Fatalf("summary = %q", out)
	}
}

func TestImportDiffSummaryText(t *testing.T) {
	out := ImportDiffSummaryText(ImportDiff{Added: []string{"x"}, Updated: []string{"y"}, Unchanged: []string{"z"}})
	if !strings.Contains(out, "1 added") || !strings.Contains(out, "1 updated") {
		t.Fatalf("summary = %q", out)
	}
}
