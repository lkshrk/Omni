package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
)

// McpAdapter manages MCP servers in one target agent by delegating to that
// agent's own CLI. omni never edits agent config files directly.
type McpAdapter interface {
	ID() string
	Available() bool
	List(ctx context.Context) ([]InstalledMcpServer, error)
	Add(ctx context.Context, s config.McpServer) error
	Remove(ctx context.Context, name string) error
}

// InstalledMcpServer is one MCP server as reported by an agent's list output.
type InstalledMcpServer struct {
	Name      string
	Transport string
	Command   string
	URL       string
	Version   string
}

// resolveEnvFlags builds flag pairs for env var names (resolved) and env_literal (inline).
// flagName is "-e" for claude-code or "--env" for codex.
// Returns an error (without calling exec) if any named var is unset.
func resolveEnvFlags(s config.McpServer, lookupEnv func(string) (string, bool), flagName string) ([]string, error) {
	var args []string
	for _, name := range s.Env {
		val, ok := lookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("env var %q not set for mcp server %q", name, s.Name)
		}
		args = append(args, flagName, name+"="+val)
	}
	for k, v := range s.EnvLiteral {
		args = append(args, flagName, k+"="+v)
	}
	return args, nil
}
