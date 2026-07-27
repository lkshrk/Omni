package dots

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
)

func CheckInstalled(ctx context.Context, exec executor.Executor) bool {
	if checker, ok := exec.(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable("stow")
	}
	_, _, err := exec.Run(ctx, "stow", "--version")
	return err == nil
}

// Restow — --no-folding plus the global ignores; dryRun adds --simulate.
func Restow(ctx context.Context, exec executor.Executor, repoPath string, packages []string, dryRun bool) error {
	if len(packages) == 0 {
		return nil
	}
	args, err := stowArgs("-R", repoPath, packages, dryRun)
	if err != nil {
		return fmt.Errorf("stow -R: %w", err)
	}
	_, stderr, err := exec.Run(ctx, "stow", args...)
	if err != nil {
		return fmt.Errorf("stow -R: %w (stderr: %s)", err, stderr)
	}
	return nil
}

// Refuses an unresolvable HOME: stow reads an empty target as cwd and would plant symlinks there.
func stowArgs(mode, repoPath string, packages []string, dryRun bool) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory for stow target: %w", err)
	}
	if home == "" {
		return nil, fmt.Errorf("home directory is empty; refusing to invoke stow with an unset target")
	}
	args := []string{mode, "--no-folding"}
	args = append(args, stowDefaultIgnoreArgs()...)
	if dryRun {
		args = append(args, "--simulate")
	}
	args = append(args, "-d", repoPath, "-t", home)
	return append(args, packages...), nil
}

func stowDefaultIgnoreArgs() []string {
	patterns := DefaultIgnores()
	args := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		regex, ok := stowIgnoreRegex(pattern)
		if !ok {
			continue
		}
		args = append(args, "--ignore="+regex)
	}
	return args
}

func stowIgnoreRegex(pattern string) (string, bool) {
	pattern = strings.TrimSpace(filepathSlash(pattern))
	if pattern == "" || strings.HasPrefix(pattern, "!") {
		return "", false
	}
	pattern = strings.TrimPrefix(pattern, "/")
	dirPattern := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" {
		return "", false
	}
	regex := globToStowRegex(pattern)
	prefix := "(?:^|/)"
	if strings.Contains(pattern, "/") {
		prefix = ""
	}
	if dirPattern {
		return prefix + regex + "(?:/|$)", true
	}
	return prefix + regex + "$", true
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func globToStowRegex(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}
