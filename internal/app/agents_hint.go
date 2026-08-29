package app

import "fmt"

// AgentsTemplateHintLines explains that APM only touches the live workspace: a package is lost on the
// next template sync unless the host template declares it, and survives an uninstall until it drops it.
func AgentsTemplateHintLines(spec string, removal bool) []string {
	template, err := AgentsTemplatePath()
	if err != nil {
		return nil
	}
	if removal {
		return []string{
			"",
			fmt.Sprintf("hint: removed from the live workspace — also remove it from the host template (%s)", template),
			"  or the next 'omni agents sync' reinstalls it",
		}
	}
	repo, ref := APMPackageSpec(spec)
	lines := []string{
		"",
		"hint: declare it in the host template so 'omni agents sync' keeps it:",
		"  " + template,
		"    - git: " + repo,
	}
	if ref != "" {
		lines = append(lines, "      ref: "+ref)
	}
	return append(lines, "  add 'path:' for a subdirectory of the repo and 'targets:' to limit deploy targets")
}
