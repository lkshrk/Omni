package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestAPMMcpLockHasState(t *testing.T) {
	tests := []struct {
		name string
		lock string
		want bool
	}{
		{"no lockfile", "", false},
		{"package state only", "dependencies:\n  - lkshrk/useful-skills\n", false},
		{"empty mcp keys", "mcp_servers: []\nmcp_configs: {}\nmcp_target_servers: {}\n", false},
		{"deployed servers", "mcp_servers:\n  - linear\n", true},
		{"configs only", "mcp_configs:\n  linear:\n    url: https://linear.example/mcp\n", true},
		{"target map only", "mcp_target_servers:\n  claude:\n    - linear\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			manifestPath := filepath.Join(dir, "apm.yml")
			if tt.lock != "" {
				if err := os.WriteFile(filepath.Join(dir, "apm.lock.yaml"), []byte(tt.lock), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := apmMcpLockHasState(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("apmMcpLockHasState = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPMMcpLockHasStateReportsAMalformedLockfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "apm.lock.yaml"), []byte("mcp_servers: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := apmMcpLockHasState(filepath.Join(dir, "apm.yml")); err == nil {
		t.Fatal("expected an error for a malformed lockfile")
	}
}

func normalizeSyncConfig() *config.RootConfig {
	return &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
		}},
	}
}

const staleClaudeState = `{
  "numStartups": 7,
  "mcpServers": {
    "linear": {"type": "http", "url": "https://stale.example/mcp"},
    "unrelated": {"type": "stdio", "command": "npx", "args": ["-y", "other"]}
  }
}
`

func writeNormalizeClaudeState(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeStateNames(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Startups   int                        `json:"numStartups"`
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("claude state %q: %v", raw, err)
	}
	if file.Startups != 7 {
		t.Fatalf("claude state lost unrelated content: %q", raw)
	}
	names := make([]string, 0, len(file.McpServers))
	for name := range file.McpServers {
		names = append(names, name)
	}
	return names
}

// The claude adapter is gone, so this runs against the production registry on purpose: an override that
// supplies an adapter for claude would hide the fact that no registered adapter can clear these entries.
// APM's first-contact idempotence check is name-based, so a stale entry would be adopted and locked in.
func TestAgentsSyncAllClearsClaudeRegistrationsBeforeTheFirstAPMMcpInstall(t *testing.T) {
	a, _, _ := newSyncApp(t, normalizeSyncConfig(), "")
	statePath := writeNormalizeClaudeState(t, a.lookupEnv("HOME"), staleClaudeState)

	var clearedBeforeAPM bool
	hooked := &hookedExecutor{before: func(name string, _ []string) {
		if name == "apm" && !slices.Contains(claudeStateNames(t, statePath), "linear") {
			clearedBeforeAPM = true
		}
	}}
	a.SetFallbackExecutor(hooked)

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := claudeStateNames(t, statePath)
	if slices.Contains(names, "linear") || !slices.Contains(names, "unrelated") {
		t.Fatalf("claude servers = %v, want only the managed entry cleared", names)
	}
	if !clearedBeforeAPM {
		t.Fatal("the clear must precede every apm call")
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(&hooked.MockExecutor); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "so APM writes it fresh") {
		t.Fatalf("warnings = %v, want the normalization surfaced", result.Warnings)
	}
}

func TestAgentsSyncAllClearsClaudeRegistrationsUnderCLAUDECONFIGDIR(t *testing.T) {
	dir := t.TempDir()
	a, _, _ := newSyncAppEnv(t, normalizeSyncConfig(), "", map[string]string{"CLAUDE_CONFIG_DIR": dir})
	statePath := writeNormalizeClaudeState(t, dir, staleClaudeState)

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	if names := claudeStateNames(t, statePath); slices.Contains(names, "linear") {
		t.Fatalf("claude servers = %v, want the entry cleared from the configured directory", names)
	}
}

func TestAgentsSyncAllSkipsNormalizationOnceAPMOwnsTheMcpState(t *testing.T) {
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  mcp:\n  - name: linear\n    registry: false\n    transport: http\n    url: https://linear.example/mcp\n"
	a, mock, _ := newSyncApp(t, normalizeSyncConfig(), manifest)
	statePath := writeNormalizeClaudeState(t, a.lookupEnv("HOME"), staleClaudeState)
	home := a.lookupEnv("HOME")
	lock := "mcp_servers:\n  - linear\nmcp_target_servers:\n  claude:\n    - linear\n"
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.lock.yaml"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	if names := claudeStateNames(t, statePath); !slices.Contains(names, "linear") {
		t.Fatalf("claude servers = %v, want none cleared: APM already owns the deployed entries", names)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
}

func TestAgentsSyncAllLeavesUndeployedServersAloneDuringNormalization(t *testing.T) {
	a, _, _ := newSyncApp(t, normalizeSyncConfig(), "")
	statePath := writeNormalizeClaudeState(t, a.lookupEnv("HOME"), `{"numStartups": 7, "mcpServers": {"unrelated": {"type": "stdio", "command": "npx"}}}`)

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if names := claudeStateNames(t, statePath); !reflect.DeepEqual(names, []string{"unrelated"}) {
		t.Fatalf("claude servers = %v, want the unrelated entry untouched", names)
	}
	if strings.Contains(strings.Join(result.Warnings, " "), "so APM writes it fresh") {
		t.Fatalf("warnings = %v, want no normalization: nothing was registered natively", result.Warnings)
	}
}

func TestAgentsSyncAllPreviewsNormalizationWithoutRemovingOnDryRun(t *testing.T) {
	manifest := "name: test\nversion: 1.0.0\n"
	a, _, _ := newSyncApp(t, normalizeSyncConfig(), manifest)
	statePath := writeNormalizeClaudeState(t, a.lookupEnv("HOME"), staleClaudeState)

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := claudeStateNames(t, statePath); !slices.Contains(names, "linear") {
		t.Fatalf("claude servers = %v, want none removed on a dry run", names)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "so APM writes it fresh") {
		t.Fatalf("warnings = %v, want the normalization previewed", result.Warnings)
	}
}
