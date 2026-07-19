package agent

import (
	"context"
	"fmt"
	"os/exec"
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

var lookPath = exec.LookPath

// McpAdapter manages MCP servers in one target agent.
type McpAdapter interface {
	ID() string
	Available() bool
	List(ctx context.Context) ([]InstalledMcpServer, error)
	Add(ctx context.Context, s config.McpServer) error
	Remove(ctx context.Context, name string) error
}

func headerFlags(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	args := make([]string, 0, 2*len(names))
	for _, name := range names {
		args = append(args, "--header", name+": "+headers[name])
	}
	return args
}

// InstalledMcpServer is one MCP server as reported by an agent's list output.
type InstalledMcpServer struct {
	Name       string
	Transport  string
	Command    string
	URL        string
	Version    string
	Headers    map[string]string
	EnvLiteral map[string]string
	// HeadersKnown distinguishes an agent that reported no headers from one
	// whose list output cannot report headers at all.
	HeadersKnown bool
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
