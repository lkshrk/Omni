// Package apm invokes the Agent Package Manager CLI without duplicating its state.
package apm

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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

const InstallHint = "install APM with 'uv tool install apm-cli' (or 'pip install apm-cli'), or run 'omni doctor --fix', and ensure apm is on PATH"

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
	return c.runEnv(ctx, nil, args...)
}

// scrub carries bare variable names, which the executor overlay reads as an unset rather than an assignment.
func (c *Client) runEnv(ctx context.Context, scrub []string, args ...string) (Result, error) {
	run := func() (string, string, error) { return c.exec.Run(ctx, "apm", args...) }
	if len(scrub) > 0 {
		run = func() (string, string, error) {
			return commandexec.RunWithEnv(ctx, c.exec, scrub, "apm", args...)
		}
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
