package app

import "fmt"

func RestoreSkillsSummaryText(res RestoreSkillsResult) string {
	return fmt.Sprintf("%d installed, %d failed", len(res.Installed), len(res.Failed))
}

func ImportDiffSummaryText(diff ImportDiff) string {
	return fmt.Sprintf("%d added, %d updated, %d unchanged", len(diff.Added), len(diff.Updated), len(diff.Unchanged))
}
