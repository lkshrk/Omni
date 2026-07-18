package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rogpeppe/go-internal/lockedfile"

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
	_, err := lookPath("codex")
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
		path, unlock, err := a.lockConfig()
		if err != nil {
			return err
		}
		defer unlock()
		args := []string{"mcp", "add", s.Name, "--url", s.URL}
		_, stderr, err := a.exec(ctx, "codex", args...)
		if err != nil {
			return fmt.Errorf("codex mcp add %s: %w: %s", s.Name, err, stderr)
		}
		if len(s.Headers) > 0 {
			if err := writeCodexHeaders(path, s.Name, s.Headers); err != nil {
				_, removeStderr, removeErr := a.exec(ctx, "codex", "mcp", "remove", s.Name)
				if removeErr != nil {
					return fmt.Errorf("write codex headers for %s: %w; rollback failed: %v: %s", s.Name, err, removeErr, removeStderr)
				}
				return fmt.Errorf("write codex headers for %s: %w", s.Name, err)
			}
		}
	default:
		return fmt.Errorf("unsupported transport %q for mcp server %q", s.Transport, s.Name)
	}
	return nil
}

func (a *codexMcpAdapter) writeHeaders(serverName string, headers map[string]string) error {
	path, unlock, err := a.lockConfig()
	if err != nil {
		return err
	}
	defer unlock()
	return writeCodexHeaders(path, serverName, headers)
}

func (a *codexMcpAdapter) lockConfig() (string, func(), error) {
	lookupEnv := a.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	codexHome, ok := lookupEnv("CODEX_HOME")
	if !ok || strings.TrimSpace(codexHome) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, fmt.Errorf("find home directory: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return "", nil, fmt.Errorf("create codex home: %w", err)
	}
	path := filepath.Join(codexHome, "config.toml")
	unlock, err := lockedfile.MutexAt(path + ".omni.lock").Lock()
	if err != nil {
		return "", nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return path, unlock, nil
}

func writeCodexHeaders(path, serverName string, headers map[string]string) error {
	for range 3 {
		current, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		updated := renderCodexHeaders(current, serverName, headers)
		written, err := replaceFileAtomically(path, current, updated)
		if err != nil {
			return err
		}
		if written {
			return nil
		}
	}
	return fmt.Errorf("config changed concurrently")
}

func renderCodexHeaders(data []byte, serverName string, headers map[string]string) []byte {
	data = removeCodexHeaderTable(data, serverName, "http_headers")
	data = removeCodexHeaderTable(data, serverName, "env_http_headers")
	literal, fromEnv := splitCodexHeaders(headers)
	var block strings.Builder
	writeCodexHeaderTable(&block, serverName, "http_headers", literal)
	writeCodexHeaderTable(&block, serverName, "env_http_headers", fromEnv)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, '\n')
	return append(data, block.String()...)
}

func removeCodexHeaderTable(data []byte, serverName, tableName string) []byte {
	targets := map[string]struct{}{
		"[mcp_servers." + tomlString(serverName) + "." + tableName + "]": {},
	}
	if isBareTomlKey(serverName) {
		targets["[mcp_servers."+serverName+"."+tableName+"]"] = struct{}{}
	}
	lines := strings.SplitAfter(string(data), "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if _, target := targets[trimmed]; target {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(trimmed, "[") {
			skipping = false
		}
		if !skipping {
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, ""))
}

func isBareTomlKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func splitCodexHeaders(headers map[string]string) (literal, fromEnv map[string]string) {
	literal = make(map[string]string)
	fromEnv = make(map[string]string)
	for name, value := range headers {
		if envName, ok := exactEnvReference(value); ok {
			fromEnv[name] = envName
		} else {
			literal[name] = value
		}
	}
	return literal, fromEnv
}

func exactEnvReference(value string) (string, bool) {
	if len(value) < 4 || !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", false
	}
	name := value[2 : len(value)-1]
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return "", false
	}
	return name, name != ""
}

func writeCodexHeaderTable(dst *strings.Builder, serverName, tableName string, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	fmt.Fprintf(dst, "[mcp_servers.%s.%s]\n", tomlString(serverName), tableName)
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(dst, "%s = %s\n", tomlString(name), tomlString(headers[name]))
	}
}

func tomlString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func replaceFileAtomically(path string, previous, data []byte) (bool, error) {
	writePath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", path, err)
	}
	info, err := os.Stat(writePath)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", writePath, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(writePath), ".config-*.toml.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("set temp config mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temp config: %w", err)
	}
	latest, err := os.ReadFile(writePath)
	if err != nil {
		return false, fmt.Errorf("re-read %s: %w", writePath, err)
	}
	if !bytes.Equal(latest, previous) {
		return false, nil
	}
	if err := os.Rename(tmpPath, writePath); err != nil {
		return false, fmt.Errorf("replace config: %w", err)
	}
	return true, nil
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
		Type           string            `json:"type"`
		URL            string            `json:"url"`
		Command        string            `json:"command"`
		Args           []string          `json:"args"`
		HTTPHeaders    map[string]string `json:"http_headers"`
		EnvHTTPHeaders map[string]string `json:"env_http_headers"`
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
			s.HeadersKnown = true
			if len(e.Transport.HTTPHeaders)+len(e.Transport.EnvHTTPHeaders) > 0 {
				s.Headers = make(map[string]string, len(e.Transport.HTTPHeaders)+len(e.Transport.EnvHTTPHeaders))
				for name, value := range e.Transport.HTTPHeaders {
					s.Headers[name] = value
				}
				for name, envName := range e.Transport.EnvHTTPHeaders {
					s.Headers[name] = "${" + envName + "}"
				}
			}
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
