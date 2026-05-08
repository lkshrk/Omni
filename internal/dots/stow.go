package dots

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
)

// CheckInstalled reports whether the stow binary is reachable on PATH.
func CheckInstalled(ctx context.Context, exec executor.Executor) bool {
	_, _, err := exec.Run(ctx, "stow", "--version")
	return err == nil
}

// Restow re-links all packages in the repo without folding directories and
// with the built-in dotfile ignore patterns applied to stow itself:
//
//	stow -R --no-folding --ignore=<global-ignore>... [--simulate] <packages...> -d <repo> -t ~
//
// dryRun passes --simulate so no filesystem changes are made.
// No-op when packages is empty.
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

// Unstow removes managed links for packages without refolding directories and
// with the built-in dotfile ignore patterns applied to stow itself:
//
//	stow -D --no-folding --ignore=<global-ignore>... <packages...> -d <repo> -t ~
//
// No-op when packages is empty.
func Unstow(ctx context.Context, exec executor.Executor, repoPath string, packages []string) error {
	if len(packages) == 0 {
		return nil
	}
	args, err := stowArgs("-D", repoPath, packages, false)
	if err != nil {
		return fmt.Errorf("stow -D: %w", err)
	}
	_, stderr, err := exec.Run(ctx, "stow", args...)
	if err != nil {
		return fmt.Errorf("stow -D: %w (stderr: %s)", err, stderr)
	}
	return nil
}

// Adopt moves the existing files at their home locations into the repo package
// directory and links them back: equivalent to
//
//	stow --adopt --no-folding --ignore=<global-ignore>... [--simulate] <pkg> -d <repo> -t ~
//
// Callers should create a backup before calling Adopt to guard against data loss
// if stow exits mid-adoption.
func Adopt(ctx context.Context, exec executor.Executor, repoPath, pkg string, dryRun bool) error {
	args, err := stowArgs("--adopt", repoPath, []string{pkg}, dryRun)
	if err != nil {
		return fmt.Errorf("stow --adopt %q: %w", pkg, err)
	}
	_, stderr, err := exec.Run(ctx, "stow", args...)
	if err != nil {
		return fmt.Errorf("stow --adopt %q: %w (stderr: %s)", pkg, err, stderr)
	}
	return nil
}

// stowArgs builds the argument slice for a stow invocation. Target is always
// the current user's home directory; if HOME cannot be resolved we refuse
// rather than fall back to "" which stow would interpret as cwd, planting
// symlinks in whatever directory omni was launched from.
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
