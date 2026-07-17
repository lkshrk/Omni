package app

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestLintSettings_SurfacesIncludeMergeNotices(t *testing.T) {
	cfg := &config.RootConfig{
		MergeNotices: []string{
			`group "core" dot entry "claude" is defined in both the parent config and include "settings.d/groups.json"; the include's definition wins — remove one copy`,
		},
	}
	issues := lintSettings(cfg)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want exactly the merge notice", issues)
	}
	if issues[0].Path != "$.$include" || !strings.Contains(issues[0].Message, `"claude"`) {
		t.Fatalf("issue = %+v, want include-path notice", issues[0])
	}
}

func TestLintSettings_NoNoticesNoIncludeIssues(t *testing.T) {
	issues := lintSettings(&config.RootConfig{})
	for _, issue := range issues {
		if issue.Path == "$.$include" {
			t.Fatalf("unexpected include issue: %+v", issue)
		}
	}
}
