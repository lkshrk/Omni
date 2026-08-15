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

func (c *Client) Install(ctx context.Context, frozen, dryRun bool) (Result, error) {
	args := c.scoped("install")
	if frozen {
		args = append(args, "--frozen")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return c.run(ctx, args...)
}

// Add uses APM's positional install form, which updates apm.yml before installing.
func (c *Client) Add(ctx context.Context, packages ...string) (Result, error) {
	if len(packages) == 0 {
		return Result{}, errors.New("add requires at least one package")
	}
	return c.run(ctx, append(c.scoped("install"), packages...)...)
}

func (c *Client) Uninstall(ctx context.Context, packages ...string) (Result, error) {
	if len(packages) == 0 {
		return Result{}, errors.New("uninstall requires at least one package")
	}
	return c.run(ctx, append(c.scoped("uninstall"), packages...)...)
}

func (c *Client) Update(ctx context.Context, dryRun bool, packages ...string) (Result, error) {
	args := append([]string{"update"}, packages...)
	if dryRun {
		args = append(args, "--dry-run")
	} else {
		args = append(args, "--yes")
	}
	if c.scope == Global {
		args = append(args, "--global")
	}
	return c.run(ctx, args...)
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
	stdout, stderr, err := c.exec.Run(ctx, "apm", args...)
	result := Result{Stdout: stdout, Stderr: stderr}
	if err == nil {
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return result, fmt.Errorf("%w: install APM and ensure apm is on PATH", ErrNotInstalled)
	}

	command := "apm"
	if len(args) > 0 {
		command += " " + args[0]
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		return result, fmt.Errorf("%s failed: %w", command, err)
	}
	return result, fmt.Errorf("%s failed: %w: %s", command, err, detail)
}
