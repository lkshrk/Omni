// Package apm invokes the Agent Package Manager CLI without duplicating its state.
package apm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	commandexec "github.com/lkshrk/omni/internal/executor"
)

type Scope uint8

const (
	Project Scope = iota
	Global
)

var (
	ErrNotInstalled     = errors.New("apm executable not found")
	ErrUnsupportedScope = errors.New("APM operation does not support this scope")
)

const InstallHint = "run 'omni doctor --fix' and ensure apm is on PATH"

type Result struct {
	Stdout string
	Stderr string
}

type Client struct {
	exec  commandexec.Executor
	scope Scope
}

func New(exec commandexec.Executor, scope Scope) *Client {
	return &Client{exec: exec, scope: scope}
}

// Run invokes APM without translating its output or maintaining a second state model.
// Commands that operate on the global workspace run from ~/.apm so their behavior is
// independent of omni's caller working directory.
func (c *Client) Run(ctx context.Context, args ...string) (Result, error) {
	if c == nil || c.exec == nil {
		return Result{}, errors.New("APM client missing executor")
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return Result{}, errors.New("APM command is required")
	}
	return c.runEnv(ctx, nil, normalizeArgs(args)...)
}

// RunPrivate invokes APM with secret input on stdin. The bytes never enter
// argv, environment, command traces, stdout, or error text.
func (c *Client) RunPrivate(ctx context.Context, stdin []byte, args ...string) (Result, error) {
	if len(stdin) == 0 {
		return Result{}, errors.New("private stdin is required")
	}
	return c.runEnvStdin(ctx, nil, stdin, normalizeArgs(args)...)
}

// Manifest surfaces for --only. MCP never goes through the `apm mcp` alias group, which exits 0 on failure.
const (
	SurfacePackages = "apm"
	SurfaceMcp      = "mcp"
)

// InstallOptions — Frozen requires the manifest and lockfile to agree instead of re-resolving.
type InstallOptions struct {
	Frozen bool
	DryRun bool
	// Update refreshes each dependency to the latest ref its declaration matches. `apm update` does the same
	// but takes no --only, so it always redeploys the MCP surface into whichever targets the run selected.
	Update bool
	// ScrubEnv names variables removed from the install's environment. Most client adapters resolve a
	// ${VAR} reference at install time, so a variable APM can read is written to the agent config as its
	// value; unset, the placeholder deploys verbatim and the agent resolves it at runtime instead.
	ScrubEnv []string
}

// InstallOnly reconciles one surface into comma-joined targets: a repeated --target keeps only the last value, and an unscoped install reaches every surface APM detects.
func (c *Client) InstallOnly(ctx context.Context, surface string, targets []string, opts InstallOptions) (Result, error) {
	if strings.TrimSpace(surface) == "" {
		return Result{}, errors.New("install requires a manifest surface")
	}
	if c.scope != Global {
		return Result{}, fmt.Errorf("apm install --only %s: %w", surface, ErrUnsupportedScope)
	}
	args := []string{"install", "-g"}
	if opts.Frozen {
		args = append(args, "--frozen")
	}
	args = append(args, "--only", surface)
	if opts.Update {
		args = append(args, "--update")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	if len(targets) > 0 {
		args = append(args, "--target", strings.Join(targets, ","))
	}
	return c.runEnv(ctx, opts.ScrubEnv, args...)
}

func (c *Client) Uninstall(ctx context.Context, packages ...string) (Result, error) {
	if len(packages) == 0 {
		return Result{}, errors.New("uninstall requires at least one package")
	}
	return c.run(ctx, append(c.scoped("uninstall"), packages...)...)
}

func (c *Client) Search(ctx context.Context, query string) (Result, error) {
	if query == "" {
		return Result{}, errors.New("search requires a query")
	}
	return c.run(ctx, "search", query)
}

func (c *Client) Audit(ctx context.Context) (Result, error) {
	if err := c.projectOnly("audit"); err != nil {
		return Result{}, err
	}
	return c.run(ctx, "audit", "--ci", "--format", "json")
}

func (c *Client) Targets(ctx context.Context) (Result, error) {
	if err := c.projectOnly("targets"); err != nil {
		return Result{}, err
	}
	return c.run(ctx, "targets", "--json")
}

func (c *Client) scoped(command string) []string {
	if c.scope == Global {
		return []string{command, "--global"}
	}
	return []string{command}
}

func (c *Client) projectOnly(operation string) error {
	if c.scope == Project {
		return nil
	}
	return fmt.Errorf("apm %s: %w", operation, ErrUnsupportedScope)
}

func (c *Client) run(ctx context.Context, args ...string) (Result, error) {
	return c.Run(ctx, args...)
}

// scrub carries bare variable names, which the executor overlay reads as an unset rather than an assignment.
func (c *Client) runEnv(ctx context.Context, scrub []string, args ...string) (Result, error) {
	return c.runEnvStdin(ctx, scrub, nil, args...)
}

func (c *Client) runEnvStdin(ctx context.Context, scrub []string, stdin []byte, args ...string) (Result, error) {
	if c == nil || c.exec == nil {
		return Result{}, errors.New("APM client missing executor")
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return Result{}, errors.New("APM command is required")
	}
	run := func() (string, string, error) {
		if !usesGlobalWorkspace(args) {
			if stdin != nil {
				return commandexec.RunInDirWithEnvAndStdin(ctx, c.exec, ".", scrub, stdin, "apm", args...)
			}
			return commandexec.RunWithEnv(ctx, c.exec, scrub, "apm", args...)
		}
		dir, err := ensureGlobalWorkspaceDir()
		if err != nil {
			return "", "", err
		}
		if stdin != nil {
			return commandexec.RunInDirWithEnvAndStdin(ctx, c.exec, dir, scrub, stdin, "apm", args...)
		}
		return runInDir(ctx, c.exec, scrub, dir, "apm", args...)
	}
	stdout, stderr, err := run()
	result := Result{Stdout: stdout, Stderr: stderr}
	if err == nil {
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return result, fmt.Errorf("%w: %s", ErrNotInstalled, InstallHint)
	}

	command := "apm"
	if len(args) > 0 {
		command += " " + args[0]
	}
	return result, commandexec.WrapError(err, command+" failed", stdout, stderr)
}

func usesGlobalWorkspace(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "import":
		if len(args) > 1 && slices.Contains([]string{"status", "resume", "finalize", "rollback", "cleanup", "exclusions"}, args[1]) {
			return true
		}
		for _, arg := range args[1:] {
			if arg == "--apply-plan" {
				return true
			}
		}
		return false
	case "audit", "marketplace", "prune", "targets":
		return true
	case "deps", "install", "outdated", "uninstall", "update", "view":
		return hasGlobalFlag(args[1:])
	}
	return false
}

func hasGlobalFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-g" || arg == "--global" {
			return true
		}
	}
	return false
}

func normalizeArgs(args []string) []string {
	normalized := append([]string(nil), args...)
	if len(normalized) >= 3 && normalized[0] == "marketplace" && normalized[1] == "remove" {
		for _, arg := range normalized[3:] {
			if arg == "--yes" || arg == "-y" {
				return normalized
			}
		}
		normalized = append(normalized, "--yes")
	}
	return normalized
}

// GlobalWorkspaceDir resolves APM's authoritative global workspace without mutating it.
func GlobalWorkspaceDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve APM workspace: %w", err)
	}
	return filepath.Join(home, ".apm"), nil
}

func ensureGlobalWorkspaceDir() (string, error) {
	dir, err := GlobalWorkspaceDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create APM workspace: %w", err)
	}
	return dir, nil
}
