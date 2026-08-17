package agent

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"

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

// The name group excludes "@" so a scoped package's "@scope/" is not read as the version separator, and the version group requires digits so "@latest" never matches.
var mcpPinnedVersionRe = regexp.MustCompile(`(?:npx|bunx)\s+(?:-\S+\s+)*(?:@[^\s@]+/)?[^\s@]+@(\d+\.\d+\.\d+[^\s]*)`)

// ExtractMcpPinnedVersion — Regex parsing only: no exec and no network.
func ExtractMcpPinnedVersion(command string) string {
	m := mcpPinnedVersionRe.FindStringSubmatch(command)
	if m == nil {
		return ""
	}
	return m[1]
}
