package app

import "fmt"

// RestoreSkillsSummaryText renders a one-line restore summary.
func RestoreSkillsSummaryText(res RestoreSkillsResult) string {
	return fmt.Sprintf("%d installed, %d failed", len(res.Installed), len(res.Failed))
}

// ImportDiffSummaryText renders a one-line import summary.
func ImportDiffSummaryText(diff ImportDiff) string {
	return fmt.Sprintf("%d added, %d updated, %d unchanged", len(diff.Added), len(diff.Updated), len(diff.Unchanged))
}
