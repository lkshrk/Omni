package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// FindResult is one entry from `skills find`. Source is owner/repo (the package
// identity); Skill is the individual skill name within it; Installs is the raw
// install-count label for display.
type FindResult struct {
	Source   string
	Skill    string
	Installs string
}

var findLineRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([A-Za-z0-9_.-]+)\s+(.*)$`)

var ansiSkillsRe = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

func stripANSISkills(s string) string { return ansiSkillsRe.ReplaceAllString(s, "") }

func findSkillPackages(ctx context.Context, exec func(context.Context, string, ...string) (string, string, error), runner, query string) ([]FindResult, error) {
	args := append([]string{"skills", "find"}, strings.Fields(query)...)
	stdout, stderr, err := exec(ctx, runner, args...)
	if err != nil {
		return nil, fmt.Errorf("skills find: %w: %s", err, stderr)
	}
	return parseFindOutput(stripANSISkills(stdout)), nil
}

// FindSkillPackages runs `skills find <query>` via the node runner and returns
// parsed results.
func (a *App) FindSkillPackages(ctx context.Context, query string) ([]FindResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return nil, err
	}
	runner := skillRunner(nodeManager(cfg))
	return findSkillPackages(ctx, a.fallbackExecutor().Run, runner, query)
}

// parseFindOutput parses ANSI-stripped `skills find` stdout. Result lines look
// like `owner/repo@skill  N installs`; the following `└ <url>` lines and any
// non-matching lines are ignored.
func parseFindOutput(out string) []FindResult {
	var results []FindResult
	for _, line := range strings.Split(out, "\n") {
		m := findLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		results = append(results, FindResult{Source: m[1], Skill: m[2], Installs: strings.TrimSpace(m[3])})
	}
	return results
}
