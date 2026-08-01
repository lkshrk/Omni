package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rogpeppe/go-internal/lockedfile"
	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

type hermesMcpAdapter struct {
	lookupEnv func(string) (string, bool)
}

var errHermesConfigUnchanged = errors.New("hermes config unchanged")

func NewHermesMcpAdapter(
	_ func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) McpAdapter {
	return &hermesMcpAdapter{lookupEnv: lookupEnv}
}

func (a *hermesMcpAdapter) ID() string { return "hermes-agent" }

func (a *hermesMcpAdapter) Available() bool {
	_, err := lookPath("hermes")
	return err == nil
}

func (a *hermesMcpAdapter) Add(_ context.Context, s config.McpServer) error {
	lookupEnv := a.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	env := make(map[string]string, len(s.Env)+len(s.EnvLiteral))
	for name, value := range s.EnvLiteral {
		env[name] = value
	}
	for _, name := range s.Env {
		if _, ok := lookupEnv(name); !ok {
			return fmt.Errorf("env var %q not set for mcp server %q", name, s.Name)
		}
		env[name] = "${env:" + name + "}"
	}

	var command []string
	switch s.Transport {
	case "stdio":
		command = strings.Fields(s.Command)
		if len(command) == 0 {
			return fmt.Errorf("hermes mcp add %s: empty command", s.Name)
		}
	case "http", "sse":
		if len(s.Env) > 0 || len(s.EnvLiteral) > 0 {
			return fmt.Errorf("hermes does not support env for remote servers (server %q)", s.Name)
		}
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("hermes mcp add %s: empty URL", s.Name)
		}
	default:
		return fmt.Errorf("unsupported transport %q for mcp server %q", s.Transport, s.Name)
	}

	return a.mutateConfig(true, func(servers *yaml.Node) error {
		entry, err := yamlMapEntry(servers, s.Name, true)
		if err != nil {
			return err
		}
		if s.Transport == "stdio" {
			yamlMapSet(entry, "command", yamlScalar(command[0]))
			yamlMapSetOrDelete(entry, "args", yamlStringSequence(command[1:]))
			yamlMapSetOrDelete(entry, "env", yamlStringMap(env))
			yamlMapDelete(entry, "url")
			yamlMapDelete(entry, "headers")
			yamlMapDelete(entry, "transport")
			return nil
		}
		yamlMapSet(entry, "url", yamlScalar(s.URL))
		yamlMapSetOrDelete(entry, "headers", yamlStringMap(s.Headers))
		yamlMapDelete(entry, "command")
		yamlMapDelete(entry, "args")
		yamlMapDelete(entry, "env")
		if s.Transport == "sse" {
			yamlMapSet(entry, "transport", yamlScalar("sse"))
		} else {
			yamlMapDelete(entry, "transport")
		}
		return nil
	})
}

func (a *hermesMcpAdapter) UpdateMcpServer(ctx context.Context, s config.McpServer) error {
	return a.Add(ctx, s)
}

func (a *hermesMcpAdapter) Remove(_ context.Context, name string) error {
	return a.mutateConfig(false, func(servers *yaml.Node) error {
		if yamlMapIndex(servers, name) < 0 {
			return errHermesConfigUnchanged
		}
		yamlMapDelete(servers, name)
		return nil
	})
}

func (a *hermesMcpAdapter) List(_ context.Context) ([]InstalledMcpServer, error) {
	path, err := a.configPath()
	if err != nil {
		return nil, fmt.Errorf("hermes mcp list: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("hermes mcp list: read %s: %w", path, err)
	}
	servers, err := parseHermesMcpConfig(data)
	if err != nil {
		return nil, fmt.Errorf("hermes mcp list: parse %s: %w", path, err)
	}
	return servers, nil
}

func (a *hermesMcpAdapter) configPath() (string, error) {
	lookupEnv := a.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if home, ok := lookupEnv("HERMES_HOME"); ok && strings.TrimSpace(home) != "" {
		return filepath.Join(home, "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".hermes", "config.yaml"), nil
}

func (a *hermesMcpAdapter) mutateConfig(create bool, mutate func(*yaml.Node) error) error {
	path, err := a.configPath()
	if err != nil {
		return err
	}
	if !create {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create hermes home: %w", err)
	}
	unlock, err := lockedfile.MutexAt(path + ".omni.lock").Lock()
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock()

	for range 3 {
		current, readErr := os.ReadFile(path)
		missing := os.IsNotExist(readErr)
		if readErr != nil && !missing {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		if missing && !create {
			return nil
		}
		doc, servers, err := parseHermesConfigNode(current)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if err := mutate(servers); err != nil {
			if errors.Is(err, errHermesConfigUnchanged) {
				return nil
			}
			return err
		}
		updated, err := yaml.Marshal(doc)
		if err != nil {
			return fmt.Errorf("render %s: %w", path, err)
		}
		if missing {
			written, err := createHermesConfigAtomically(path, updated)
			if err != nil {
				return err
			}
			if written {
				return nil
			}
			continue
		}
		written, err := replaceFileAtomically(path, current, updated)
		if err != nil {
			return err
		}
		if written {
			return nil
		}
	}
	return fmt.Errorf("hermes config changed concurrently")
}

func createHermesConfigAtomically(path string, data []byte) (bool, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
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
	if err := os.Link(tmpPath, path); err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("create %s: %w", path, err)
	}
	return true, nil
}

func parseHermesConfigNode(data []byte) (*yaml.Node, *yaml.Node, error) {
	doc := &yaml.Node{}
	if len(strings.TrimSpace(string(data))) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
	} else if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("expected mapping document")
	}
	servers, err := yamlMapEntry(doc.Content[0], "mcp_servers", true)
	if err != nil {
		return nil, nil, err
	}
	return doc, servers, nil
}

func yamlMapEntry(node *yaml.Node, key string, create bool) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", key)
	}
	if i := yamlMapIndex(node, key); i >= 0 {
		value := node.Content[i+1]
		if value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s must be a mapping", key)
		}
		return value, nil
	}
	if !create {
		return nil, nil
	}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content, yamlScalar(key), value)
	return value, nil
}

func yamlMapIndex(node *yaml.Node, key string) int {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func yamlMapSet(node *yaml.Node, key string, value *yaml.Node) {
	if i := yamlMapIndex(node, key); i >= 0 {
		old := node.Content[i+1]
		value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
		node.Content[i+1] = value
		return
	}
	node.Content = append(node.Content, yamlScalar(key), value)
}

func yamlMapSetOrDelete(node *yaml.Node, key string, value *yaml.Node) {
	if value == nil {
		yamlMapDelete(node, key)
		return
	}
	yamlMapSet(node, key, value)
}

func yamlMapDelete(node *yaml.Node, key string) {
	if i := yamlMapIndex(node, key); i >= 0 {
		node.Content = append(node.Content[:i], node.Content[i+2:]...)
	}
}

func yamlScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlStringSequence(values []string) *yaml.Node {
	if len(values) == 0 {
		return nil
	}
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node.Content = append(node.Content, yamlScalar(value))
	}
	return node
}

func yamlStringMap(values map[string]string) *yaml.Node {
	if len(values) == 0 {
		return nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		node.Content = append(node.Content, yamlScalar(key), yamlScalar(values[key]))
	}
	return node
}

type hermesMcpConfig struct {
	McpServers map[string]hermesMcpServer `yaml:"mcp_servers"`
}

type hermesMcpServer struct {
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	Transport string            `yaml:"transport"`
}

func parseHermesMcpConfig(data []byte) ([]InstalledMcpServer, error) {
	var file hermesMcpConfig
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
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
		if entry.URL == "" {
			s.Transport = "stdio"
			s.Command = strings.TrimSpace(strings.Join(append([]string{entry.Command}, entry.Args...), " "))
			s.Version = ExtractMcpPinnedVersion(s.Command)
		} else {
			s.Transport = "http"
			if entry.Transport == "sse" {
				s.Transport = "sse"
			}
			s.URL = entry.URL
			s.Headers = entry.Headers
			s.HeadersKnown = true
		}
		servers = append(servers, s)
	}
	return servers, nil
}
