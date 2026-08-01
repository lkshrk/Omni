package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestHermesAdapter_IDAndAvailable(t *testing.T) {
	originalLookPath := lookPath
	t.Cleanup(func() { lookPath = originalLookPath })
	lookPath = func(name string) (string, error) {
		if name != "hermes" {
			t.Fatalf("lookPath(%q), want hermes", name)
		}
		return "/bin/hermes", nil
	}

	a := NewHermesMcpAdapter(nil, nil)
	if a.ID() != "hermes-agent" {
		t.Fatalf("ID() = %q, want hermes-agent", a.ID())
	}
	if !a.Available() {
		t.Fatal("Available() = false, want true")
	}
}

func TestHermesAdapter_ListUsesHermesHome(t *testing.T) {
	hermesHome := t.TempDir()
	data := []byte(`
unknown_root: true
mcp_servers:
  remote:
    url: https://mcp.example.com
    transport: sse
    headers:
      Authorization: ${TOKEN}
    unknown: ignored
  local:
    command: npx
    args: [-y, example@1.2.3]
    env:
      API_KEY: ${env:API_KEY}
`)
	if err := os.WriteFile(filepath.Join(hermesHome, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	a := hermesTestAdapter(hermesHome, nil)

	got, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List() returned %d servers, want 2: %+v", len(got), got)
	}
	if got[0].Name != "local" || got[0].Transport != "stdio" || got[0].Command != "npx -y example@1.2.3" ||
		got[0].Version != "1.2.3" || got[0].EnvLiteral["API_KEY"] != "${env:API_KEY}" {
		t.Fatalf("local server = %+v", got[0])
	}
	if got[1].Name != "remote" || got[1].Transport != "sse" || got[1].URL != "https://mcp.example.com" ||
		got[1].Headers["Authorization"] != "${TOKEN}" || !got[1].HeadersKnown {
		t.Fatalf("remote server = %+v", got[1])
	}
}

func TestHermesAdapter_ListDefaultPathAndMissingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := NewHermesMcpAdapter(nil, func(string) (string, bool) { return "", false })

	path, err := a.(*hermesMcpAdapter).configPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".hermes", "config.yaml")
	if path != want {
		t.Fatalf("configPath() = %q, want %q", path, want)
	}
	got, err := a.List(context.Background())
	if err != nil || got != nil {
		t.Fatalf("List() with missing config = %+v, %v, want nil, nil", got, err)
	}
}

func TestHermesAdapter_AddCreatesConfigWithSecretReferences(t *testing.T) {
	hermesHome := filepath.Join(t.TempDir(), "new-hermes-home")
	a := hermesTestAdapter(hermesHome, func(name string) (string, bool) {
		return "PLAINTEXT_SECRET", name == "API_KEY"
	})
	err := a.Add(context.Background(), config.McpServer{
		Name:       "local",
		Transport:  "stdio",
		Command:    "npx -y example",
		Env:        []string{"API_KEY"},
		EnvLiteral: map[string]string{"LOG_LEVEL": "info"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hermesHome, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "PLAINTEXT_SECRET") || !strings.Contains(text, "${env:API_KEY}") || !strings.Contains(text, "LOG_LEVEL: info") {
		t.Fatalf("config leaked or omitted env values:\n%s", text)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, err = %v, want 0600", info, err)
	}
	got, err := a.List(context.Background())
	if err != nil || len(got) != 1 || got[0].Command != "npx -y example" {
		t.Fatalf("List() = %+v, %v", got, err)
	}
}

func TestHermesAdapter_AddRemoteRoundTrip(t *testing.T) {
	hermesHome := t.TempDir()
	a := hermesTestAdapter(hermesHome, nil)
	for _, server := range []config.McpServer{
		{Name: "http", Transport: "http", URL: "https://http.example.com", Headers: map[string]string{"X-Key": "value"}},
		{Name: "events", Transport: "sse", URL: "https://sse.example.com", Headers: map[string]string{"Authorization": "${TOKEN}"}},
	} {
		if err := a.Add(context.Background(), server); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "events" || got[0].Transport != "sse" || got[0].Headers["Authorization"] != "${TOKEN}" ||
		got[1].Name != "http" || got[1].Transport != "http" || got[1].Headers["X-Key"] != "value" {
		t.Fatalf("List() = %+v", got)
	}
}

func TestHermesAdapter_AddRemovePreservesUnrelatedYAML(t *testing.T) {
	hermesHome := t.TempDir()
	path := filepath.Join(hermesHome, "config.yaml")
	original := []byte(`# root comment
model: test # model comment
mcp_servers:
  keep:
    command: old
    enabled: false # keep server comment
    custom: value
`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	a := hermesTestAdapter(hermesHome, nil)
	if err := a.Add(context.Background(), config.McpServer{Name: "keep", Transport: "stdio", Command: "new --flag"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Add(context.Background(), config.McpServer{Name: "temporary", Transport: "http", URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(context.Background(), "temporary"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# root comment", "# model comment", "# keep server comment", "custom: value", "enabled: false", "command: new"} {
		if !strings.Contains(text, want) {
			t.Errorf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "temporary") {
		t.Fatalf("removed server remains:\n%s", text)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode = %v, err = %v, want 0640", info, err)
	}
}

func TestHermesAdapter_MalformedYAMLIsNotMutated(t *testing.T) {
	hermesHome := t.TempDir()
	path := filepath.Join(hermesHome, "config.yaml")
	original := []byte("mcp_servers: [}")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	a := hermesTestAdapter(hermesHome, nil)
	for _, action := range []func() error{
		func() error {
			return a.Add(context.Background(), config.McpServer{Name: "new", Transport: "http", URL: "https://example.com"})
		},
		func() error { return a.Remove(context.Background(), "old") },
	} {
		if err := action(); err == nil || !strings.Contains(err.Error(), "parse") {
			t.Fatalf("action error = %v, want parse error", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(original) {
			t.Fatalf("config mutated: %q, %v", got, err)
		}
	}
}

func TestHermesAdapter_RemoveMissingServerDoesNotRewrite(t *testing.T) {
	hermesHome := t.TempDir()
	path := filepath.Join(hermesHome, "config.yaml")
	original := []byte("# formatting stays exact\nmcp_servers: {keep: {command: server}}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hermesTestAdapter(hermesHome, nil).Remove(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("config rewritten: %q, %v", got, err)
	}
}

func TestHermesAdapter_AddValidatesNamedEnvWithoutCreatingConfig(t *testing.T) {
	hermesHome := filepath.Join(t.TempDir(), "new-hermes-home")
	a := hermesTestAdapter(hermesHome, func(string) (string, bool) { return "", false })
	err := a.Add(context.Background(), config.McpServer{
		Name: "local", Transport: "stdio", Command: "server", Env: []string{"MISSING"},
	})
	if err == nil || !strings.Contains(err.Error(), `env var "MISSING" not set`) {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(hermesHome, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config created after validation failure: %v", err)
	}
}

func TestCreateHermesConfigAtomicallyDoesNotClobberConcurrentCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	type result struct {
		written bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, data := range [][]byte{[]byte("winner: one\n"), []byte("winner: two\n")} {
		go func() {
			ready.Done()
			<-start
			written, err := createHermesConfigAtomically(path, data)
			results <- result{written: written, err: err}
		}()
	}
	ready.Wait()
	close(start)
	winners := 0
	for range 2 {
		res := <-results
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.written {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("successful creates = %d, want 1", winners)
	}
	data, err := os.ReadFile(path)
	if err != nil || (string(data) != "winner: one\n" && string(data) != "winner: two\n") {
		t.Fatalf("config = %q, %v", data, err)
	}
	if leftovers, err := filepath.Glob(filepath.Join(dir, ".config-*.yaml.tmp")); err != nil || len(leftovers) != 0 {
		t.Fatalf("temp files = %v, %v", leftovers, err)
	}
}

func hermesTestAdapter(home string, lookup func(string) (string, bool)) McpAdapter {
	return NewHermesMcpAdapter(nil, func(name string) (string, bool) {
		if name == "HERMES_HOME" {
			return home, true
		}
		if lookup == nil {
			return "", false
		}
		return lookup(name)
	})
}
