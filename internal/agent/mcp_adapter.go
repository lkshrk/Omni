package agent

import (
	"context"
	"fmt"
	"os/exec"
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

var lookPath = exec.LookPath

type McpAdapter interface {
	ID() string
	Available() bool
	List(ctx context.Context) ([]InstalledMcpServer, error)
	Add(ctx context.Context, s config.McpServer) error
	Remove(ctx context.Context, name string) error
}

// McpInPlaceUpdater preserves adapter-specific configuration that is not represented in McpServer.
type McpInPlaceUpdater interface {
	UpdateMcpServer(ctx context.Context, s config.McpServer) error
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

type InstalledMcpServer struct {
	Name       string
	Transport  string
	Command    string
	URL        string
	Version    string
	Headers    map[string]string
	EnvLiteral map[string]string
	// Distinguishes an agent that reported no headers from one that cannot report them at all.
	HeadersKnown bool
	// Set when the report omitted the transport and the parser had to infer one from the entry's shape.
	// Polarity is inverted from HeadersKnown deliberately: the zero value keeps the strict comparison,
	// so a parser that forgets this flag over-reports drift rather than silently hiding a real one.
	TransportInferred bool
}

// Errors before any exec when a named variable is unset.
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
