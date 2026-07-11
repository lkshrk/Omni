package app

import (
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// SettingsLintIssue is one advisory settings problem.
type SettingsLintIssue struct {
	Path    string
	Message string
}

func (i SettingsLintIssue) String() string {
	if i.Path == "" {
		return i.Message
	}
	return i.Path + ": " + i.Message
}

// LintSettings inspects cfg for common settings hygiene problems.
func (a *App) LintSettings(cfg *config.RootConfig) []SettingsLintIssue {
	return lintSettings(cfg)
}

func lintSettings(cfg *config.RootConfig) []SettingsLintIssue {
	if cfg == nil {
		return nil
	}
	var issues []SettingsLintIssue
	toolGroups := make(map[string][]string)
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		groupName := group.BaseName()
		for _, tool := range group.Tools {
			if tool.Name == "" {
				continue
			}
			toolGroups[tool.Name] = append(toolGroups[tool.Name], groupName)
		}
	}
	for name, groups := range toolGroups {
		if len(groups) > 1 {
			issues = append(issues, SettingsLintIssue{
				Path:    fmt.Sprintf("$.tools.%q", name),
				Message: fmt.Sprintf("tool belongs to multiple groups: %s", strings.Join(groups, ", ")),
			})
		}
	}
	for name, spec := range cfg.Tools {
		path := fmt.Sprintf("$.tools.%q", name)
		for host, override := range spec.Hosts {
			if strings.TrimSpace(override.Provider) != "" {
				issues = append(issues, SettingsLintIssue{
					Path:    fmt.Sprintf("%s.hosts.%q", path, host),
					Message: "tools.*.hosts provider overrides are deprecated; use providers[] instead",
				})
			}
		}
		candidates := append([]config.ToolInstallSpec(nil), spec.Providers...)
		if len(candidates) == 0 {
			candidates = append(candidates, spec.DefaultInstallSpec())
		}
		for i, candidate := range candidates {
			if candidate.Provider != "script" {
				continue
			}
			install := strings.TrimSpace(candidate.Options["install"])
			upgrade := strings.TrimSpace(candidate.Options["upgrade"])
			if upgrade != "" && install != "" && upgrade != install {
				issues = append(issues, SettingsLintIssue{
					Path:    fmt.Sprintf("%s.providers[%d].options.upgrade", path, i),
					Message: "script upgrade differs from install; prefer a single install command",
				})
			}
			if strings.Contains(install, "github.com") && strings.Contains(install, "/releases") {
				if candidate.Recipe == nil || candidate.Recipe.Type != config.FallbackRecipeGitHubReleaseAsset {
					issues = append(issues, SettingsLintIssue{
						Path:    fmt.Sprintf("%s.providers[%d]", path, i),
						Message: "script install looks like a GitHub release download; consider a github_release_asset recipe",
					})
				}
			}
		}
	}
	return issues
}
