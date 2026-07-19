package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type claudeCodeMcpAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewClaudeCodeMcpAdapter returns an McpAdapter that delegates to the claude CLI.
func NewClaudeCodeMcpAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) McpAdapter {
	return &claudeCodeMcpAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *claudeCodeMcpAdapter) ID() string { return "claude-code" }

func (a *claudeCodeMcpAdapter) Available() bool {
	_, err := lookPath("claude")
	return err == nil
}

func (a *claudeCodeMcpAdapter) Add(ctx context.Context, s config.McpServer) error {
	switch s.Transport {
	case "stdio":
		envFlags, err := resolveEnvFlags(s, a.lookupEnv, "-e")
		if err != nil {
			return err
		}
		args := append([]string{"mcp", "add", "-s", "user", s.Name}, envFlags...)
		args = append(args, "--")
		args = append(args, strings.Fields(s.Command)...)
		_, stderr, err := a.exec(ctx, "claude", args...)
		if err != nil {
			return fmt.Errorf("claude mcp add %s: %w: %s", s.Name, err, stderr)
		}
	case "http", "sse":
		envFlags, err := resolveEnvFlags(s, a.lookupEnv, "-e")
		if err != nil {
			return err
		}
		args := []string{"mcp", "add", "-s", "user", s.Name, "--transport", s.Transport, s.URL}
		args = append(args, envFlags...)
		args = append(args, headerFlags(s.Headers)...)
		_, stderr, err := a.exec(ctx, "claude", args...)
		if err != nil {
			return fmt.Errorf("claude mcp add %s: %w: %s", s.Name, err, stderr)
		}
	default:
		return fmt.Errorf("unsupported transport %q for mcp server %q", s.Transport, s.Name)
	}
	return nil
}

func (a *claudeCodeMcpAdapter) Remove(ctx context.Context, name string) error {
	_, stderr, err := a.exec(ctx, "claude", "mcp", "remove", "-s", "user", name)
	if err != nil {
		return fmt.Errorf("claude mcp remove %s: %w: %s", name, err, stderr)
	}
	return nil
}

// List reads ~/.claude.json's top-level "mcpServers" object directly instead
// of shelling out to `claude mcp list`, which performs a live health check
// (i.e. connects to / starts) every registered server before printing.
// Plugin-provided servers are not stored in this file, so they never leak
// into the result -- only user-scope servers added via `claude mcp add`.
func (a *claudeCodeMcpAdapter) List(ctx context.Context) ([]InstalledMcpServer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("claude mcp list: resolve home dir: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude mcp list: read ~/.claude.json: %w", err)
	}
	return parseClaudeConfigMcpServers(data)
}

// claudeConfigFile mirrors the fields of ~/.claude.json relevant to MCP
// servers. Each entry is either a stdio server (command/args/env) or a
// remote server (type "http"/"sse" plus url); type defaults to "http" when
// a url is present but type is omitted, matching `claude mcp add --transport
// http` output observed on a live install.
type claudeConfigFile struct {
	McpServers map[string]claudeConfigMcpServer `json:"mcpServers"`
}

type claudeConfigMcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	URL     string            `json:"url"`
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
}

// parseClaudeConfigMcpServers converts ~/.claude.json's mcpServers object
// into InstalledMcpServer entries, sorted by name for deterministic output.
func parseClaudeConfigMcpServers(data []byte) ([]InstalledMcpServer, error) {
	var file claudeConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse ~/.claude.json: %w", err)
	}
	names := make([]string, 0, len(file.McpServers))
	for name := range file.McpServers {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]InstalledMcpServer, 0, len(names))
	for _, name := range names {
		entry := file.McpServers[name]
		s := InstalledMcpServer{Name: name, EnvLiteral: entry.Env}
		if entry.URL != "" {
			s.HeadersKnown = true
			switch entry.Type {
			case "sse":
				s.Transport = "sse"
			default:
				s.Transport = "http"
			}
			s.URL = entry.URL
			s.Headers = entry.Headers
		} else {
			s.Transport = "stdio"
			s.Command = strings.TrimSpace(strings.Join(append([]string{entry.Command}, entry.Args...), " "))
			s.Version = ExtractMcpPinnedVersion(s.Command)
		}
		servers = append(servers, s)
	}
	return servers, nil
}

// mcpPinnedVersionRe matches an npx/bunx package spec pinned to a semver-ish
// version, e.g. "npx -y @scope/pkg@1.2.3" or "bunx some-pkg@2.0.0". The
// package name group excludes "@" so a scoped package's leading "@scope/"
// isn't mistaken for the version separator; the version group requires a
// leading "\d+\.\d+\.\d+" so tags like "@latest"/"@next" never match.
var mcpPinnedVersionRe = regexp.MustCompile(`(?:npx|bunx)\s+(?:-\S+\s+)*(?:@[^\s@]+/)?[^\s@]+@(\d+\.\d+\.\d+[^\s]*)`)

// ExtractMcpPinnedVersion statically parses a pinned semver-ish version out
// of an npx/bunx invocation in command, or "" if none is present (bare
// paths, http/sse transports with no command, or untagged/@latest/@next
// packages). No exec, no network — regex parsing only.
func ExtractMcpPinnedVersion(command string) string {
	m := mcpPinnedVersionRe.FindStringSubmatch(command)
	if m == nil {
		return ""
	}
	return m[1]
}
