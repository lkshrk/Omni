package app

import (
	"context"
	"encoding/json"
	"fmt"
	osExec "os/exec"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type codexMcpAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewCodexMcpAdapter returns an McpAdapter that delegates to the codex CLI.
func NewCodexMcpAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) McpAdapter {
	return &codexMcpAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *codexMcpAdapter) ID() string { return "codex" }

func (a *codexMcpAdapter) Available() bool {
	_, err := osExec.LookPath("codex")
	return err == nil
}

func (a *codexMcpAdapter) Add(ctx context.Context, s config.McpServer) error {
	switch s.Transport {
	case "stdio":
		envFlags, err := resolveEnvFlags(s, a.lookupEnv, "--env")
		if err != nil {
			return err
		}
		args := []string{"mcp", "add"}
		args = append(args, envFlags...)
		args = append(args, s.Name, "--")
		args = append(args, strings.Fields(s.Command)...)
		_, stderr, err := a.exec(ctx, "codex", args...)
		if err != nil {
			return fmt.Errorf("codex mcp add %s: %w: %s", s.Name, err, stderr)
		}
	case "http", "sse":
		if len(s.Env) > 0 || len(s.EnvLiteral) > 0 {
			return fmt.Errorf("codex does not support env for http/sse servers (server %q)", s.Name)
		}
		args := []string{"mcp", "add", s.Name, "--url", s.URL}
		_, stderr, err := a.exec(ctx, "codex", args...)
		if err != nil {
			return fmt.Errorf("codex mcp add %s: %w: %s", s.Name, err, stderr)
		}
	default:
		return fmt.Errorf("unsupported transport %q for mcp server %q", s.Transport, s.Name)
	}
	return nil
}

func (a *codexMcpAdapter) Remove(ctx context.Context, name string) error {
	_, stderr, err := a.exec(ctx, "codex", "mcp", "remove", name)
	if err != nil {
		return fmt.Errorf("codex mcp remove %s: %w: %s", name, err, stderr)
	}
	return nil
}

func (a *codexMcpAdapter) List(ctx context.Context) ([]InstalledMcpServer, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "mcp", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex mcp list: %w: %s", err, stderr)
	}
	return parseCodexMcpList(stdout)
}

// codexMcpListEntry mirrors the shape of one element of `codex mcp list --json`.
type codexMcpListEntry struct {
	Name      string `json:"name"`
	Transport struct {
		Type    string   `json:"type"`
		URL     string   `json:"url"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"transport"`
}

// parseCodexMcpList parses `codex mcp list --json` output.
func parseCodexMcpList(out string) ([]InstalledMcpServer, error) {
	var entries []codexMcpListEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("codex mcp list: parse json: %w", err)
	}
	servers := make([]InstalledMcpServer, 0, len(entries))
	for _, e := range entries {
		s := InstalledMcpServer{Name: e.Name}
		switch e.Transport.Type {
		case "streamable_http":
			s.Transport = "http"
			s.URL = e.Transport.URL
		case "stdio":
			s.Transport = "stdio"
			s.Command = strings.TrimSpace(strings.Join(append([]string{e.Transport.Command}, e.Transport.Args...), " "))
		default:
			s.Transport = e.Transport.Type
			s.URL = e.Transport.URL
		}
		servers = append(servers, s)
	}
	return servers, nil
}
