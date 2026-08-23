package app_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func lintTestApp(t *testing.T) *app.App {
	t.Helper()
	return app.New(t.TempDir() + "/settings.json")
}

func TestLintSettings(t *testing.T) {
	t.Parallel()
	a := lintTestApp(t)

	if got := a.LintSettings(nil); got != nil {
		t.Fatalf("nil config must lint clean, got %v", got)
	}

	cfg := &config.RootConfig{
		MergeNotices: []string{"include cycle detected"},
		Groups: []*config.GroupConfig{
			{Name: "dev", Tools: []config.ToolEntry{{Name: "jq"}}},
			{Name: "ops", Tools: []config.ToolEntry{{Name: "jq"}}},
		},
		Tools: map[string]config.ToolSpec{
			"legacy": {Hosts: map[string]config.ToolInstallSpec{"mac": {Provider: "brew"}}},
			"scripty": {Providers: []config.ToolInstallSpec{{
				Provider: "script",
				Options: map[string]string{
					"install": "curl -L https://github.com/acme/x/releases/latest/download/x -o /usr/local/bin/x",
					"upgrade": "brew upgrade x",
				},
			}}},
		},
	}
	issues := a.LintSettings(cfg)
	wantSubstrings := map[string]string{
		"$.$include":             "include cycle detected",
		`$.tools."jq"`:           "multiple groups",
		`"legacy".hosts`:         "deprecated",
		`options.upgrade`:        "differs from install",
		`"scripty".providers[0]`: "github_release_asset",
	}
	for pathFrag, msgFrag := range wantSubstrings {
		found := false
		for _, issue := range issues {
			if strings.Contains(issue.Path, pathFrag) && strings.Contains(issue.Message, msgFrag) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing lint issue with path~%q message~%q in %v", pathFrag, msgFrag, issues)
		}
	}
}

func TestSettingsLintIssueString(t *testing.T) {
	t.Parallel()
	if got := (app.SettingsLintIssue{Message: "m"}).String(); got != "m" {
		t.Fatalf("String() without path = %q", got)
	}
	if got := (app.SettingsLintIssue{Path: "$.x", Message: "m"}).String(); got != "$.x: m" {
		t.Fatalf("String() with path = %q", got)
	}
}
