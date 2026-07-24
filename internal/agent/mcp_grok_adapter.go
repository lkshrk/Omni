package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type grokMcpAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewGrokMcpAdapter returns an McpAdapter that delegates to the grok CLI.
func NewGrokMcpAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) McpAdapter {
	return &grokMcpAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *grokMcpAdapter) ID() string { return "grok" }

func (a *grokMcpAdapter) Available() bool {
	_, err := lookPath("grok")
	return err == nil
}

func (a *grokMcpAdapter) Add(ctx context.Context, s config.McpServer) error {
	switch s.Transport {
	case "stdio":
		envFlags, err := resolveEnvFlags(s, a.lookupEnv, "-e")
		if err != nil {
			return err
		}
		args := append([]string{"mcp", "add", "-s", "user", s.Name}, envFlags...)
		args = append(args, "--")
		args = append(args, strings.Fields(s.Command)...)
		_, stderr, err := a.exec(ctx, "grok", args...)
		if err != nil {
			return fmt.Errorf("grok mcp add %s: %w: %s", s.Name, err, stderr)
		}
	case "http", "sse":
		if len(s.Env) > 0 || len(s.EnvLiteral) > 0 {
			return fmt.Errorf("grok does not support env for http/sse servers (server %q)", s.Name)
		}
		args := []string{"mcp", "add", "-s", "user", "--transport", s.Transport, s.Name, s.URL}
		args = append(args, headerFlags(s.Headers)...)
		_, stderr, err := a.exec(ctx, "grok", args...)
		if err != nil {
			return fmt.Errorf("grok mcp add %s: %w: %s", s.Name, err, stderr)
		}
	default:
		return fmt.Errorf("unsupported transport %q for mcp server %q", s.Transport, s.Name)
	}
	return nil
}

func (a *grokMcpAdapter) Remove(ctx context.Context, name string) error {
	_, stderr, err := a.exec(ctx, "grok", "mcp", "remove", "-s", "user", name)
	if err != nil {
		return fmt.Errorf("grok mcp remove %s: %w: %s", name, err, stderr)
	}
	return nil
}

func (a *grokMcpAdapter) List(ctx context.Context) ([]InstalledMcpServer, error) {
	stdout, stderr, err := a.exec(ctx, "grok", "mcp", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("grok mcp list: %w: %s", err, stderr)
	}
	return parseGrokMcpList(stdout)
}

// grokMcpListEntry mirrors one element of `grok mcp list --json`.
type grokMcpListEntry struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Headers map[string]string `json:"headers"`
	Env     map[string]string `json:"env"`
}

func parseGrokMcpList(out string) ([]InstalledMcpServer, error) {
	var entries []grokMcpListEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("grok mcp list: parse json: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("grok mcp list: parse json: expected array, got null")
	}
	servers := make([]InstalledMcpServer, 0, len(entries))
	for _, e := range entries {
		s := InstalledMcpServer{Name: e.Name, EnvLiteral: e.Env}
		if e.URL != "" {
			s.Transport = "http"
			s.URL = e.URL
			s.Headers = e.Headers
			s.HeadersKnown = true
		} else {
			s.Transport = "stdio"
			s.Command = strings.TrimSpace(strings.Join(append([]string{e.Command}, e.Args...), " "))
			s.Version = ExtractMcpPinnedVersion(s.Command)
		}
		servers = append(servers, s)
	}
	return servers, nil
}
