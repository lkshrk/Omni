//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsMigrationRealAPMLifecycle(t *testing.T) {
	requireMigrationPinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	root, home, state := isolateAgentsAPMIntegration(t)
	snapshot := filepath.Join(root, "snapshot")
	owner := filepath.Join(snapshot, "owner")
	original := filepath.Join(root, "legacy", "bundle-a")
	skillOriginal := filepath.Join(original, "skills", "review")

	mustWriteBundleFile(t, filepath.Join(owner, ".codex-plugin", "plugin.json"), `{"name":"bundle-a","version":"1.0.0"}`)
	mustWriteBundleFile(t, filepath.Join(owner, "mcp.json"), `{"mcpServers":{"owned":{"type":"stdio","command":"${PLUGIN_ROOT}/bin/server.sh","cwd":"${PLUGIN_ROOT}"}}}`)
	mustWriteBundleFile(t, filepath.Join(owner, "bin", "server.sh"), "#!/bin/sh\nread request\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"serverInfo\":{\"name\":\"owned\",\"version\":\"1\"}}}'\n")
	if err := os.Chmod(filepath.Join(owner, "bin", "server.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteBundleFile(t, filepath.Join(owner, "skills", "review", "SKILL.md"), "---\nname: review\ndescription: migration fixture\n---\n")
	settings := `{
  "agents": {
    "plugins": [{"name":"bundle-a","path":` + quoteJSON(original) + `,"agents":["codex"]}],
    "packages": [{"source":` + quoteJSON(skillOriginal) + `,"agents":["codex"]}]
  },
  "groups": [{"name":"g","plugins":["bundle-a"],"skills":[` + quoteJSON(skillOriginal) + `]}],
  "hosts": {"h":["g"]}
}`
	mustWriteBundleFile(t, filepath.Join(snapshot, "omni-config-000.json"), settings)
	paths, err := json.Marshal(map[string]string{
		"omni-config-000.json": filepath.Join(root, "legacy", "settings.json"),
		"owner":                original,
		"owner/skills/review":  skillOriginal,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteBundleFile(t, filepath.Join(snapshot, "paths.json"), string(paths))

	a := &App{StateDir: state}
	a.SetFallbackExecutor(executor.New())
	preview, err := a.AgentsMigrate(t.Context(), "h", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "agents-migration")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote migration wrappers: %v", err)
	}
	written, err := a.AgentsMigrateWrite(t.Context(), "h", snapshot)
	if err != nil || written != preview {
		t.Fatalf("migration write differs from preview: err=%v", err)
	}
	wrapperManifests, err := filepath.Glob(filepath.Join(state, "agents-migration", "bundles", "*", "apm.yml"))
	if err != nil || len(wrapperManifests) != 1 || len(filepath.Base(filepath.Dir(wrapperManifests[0]))) != 64 {
		t.Fatalf("content-addressed wrappers = %v, err=%v", wrapperManifests, err)
	}
	writeIntegrationFile(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.unrelated]\ncommand = \"true\"\n")
	if _, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{ForceTemplate: true}); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	deployedSkill := filepath.Join(home, ".agents", "skills", "review", "SKILL.md")
	assertIntegrationFileContains(t, deployedSkill, "migration fixture")
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	assertIntegrationFileContains(t, codexConfig, "mcp_servers.owned", "mcp_servers.unrelated")
	wrapper := filepath.Dir(wrapperManifests[0])
	handshake := exec.CommandContext(ctx, filepath.Join(wrapper, "runtime", "bin", "server.sh"))
	handshake.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	response, err := handshake.Output()
	if err != nil || !bytes.Contains(response, []byte(`"serverInfo":{"name":"owned"`)) {
		t.Fatalf("MCP handshake: err=%v response=%s", err, response)
	}

	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	before, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{ForceTemplate: true}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	after, err := os.ReadFile(lock)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("second sync changed lockfile: err=%v", err)
	}

	client := apm.New(executor.New(), apm.Global)
	if result, err := client.Uninstall(ctx, wrapper); err != nil {
		t.Fatalf("uninstall wrapper: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(deployedSkill); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained deployed skill: %v", err)
	}
	assertIntegrationFileContains(t, codexConfig, "mcp_servers.unrelated")
	if content, err := os.ReadFile(codexConfig); err != nil || strings.Contains(string(content), "mcp_servers.owned") {
		t.Fatalf("uninstall retained owned MCP: err=%v content=%q", err, content)
	}
}

func TestAgentsManifestlessRealAPMLifecycle(t *testing.T) {
	requireMigrationPinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	root, home, state := isolateAgentsAPMIntegration(t)
	deepwiki := filepath.Join(root, "repos", "local", "deepwiki-rs")
	shiplight := filepath.Join(root, "repos", "ShiplightAI", "agent-skills-v2")
	writeIntegrationFile(t, filepath.Join(deepwiki, "skills", "smart-docs", "SKILL.md"), "---\nname: smart-docs\ndescription: local DeepWiki fixture\n---\n")
	writeIntegrationFile(t, filepath.Join(shiplight, "shiplight", "SKILL.md"), "---\nname: shiplight\ndescription: local Shiplight fixture\n---\n")
	initIntegrationGitRepo(t, deepwiki)
	initIntegrationGitRepo(t, shiplight)
	bare := exec.Command("git", "clone", "--bare", deepwiki, deepwiki+".git")
	if output, err := bare.CombinedOutput(); err != nil {
		t.Fatalf("create DeepWiki HTTP fixture: %v\n%s", err, output)
	}
	server := httptest.NewTLSServer(integrationGitHTTPHandler(filepath.Join(root, "repos")))
	defer server.Close()
	t.Setenv("GIT_SSL_NO_VERIFY", "true")

	manifest := fmt.Sprintf(`name: manifestless-integration
version: 1.0.0
targets: [codex, claude]
dependencies:
  apm:
    - git: %s/local/deepwiki-rs
      ref: master
      path: skills/smart-docs
    - git: %s/ShiplightAI/agent-skills-v2
      path: shiplight
  mcp:
    - name: shiplight
      registry: false
      transport: stdio
      command: /bin/sh
      args: [-c, "exit 0"]
`, server.URL, server.URL)
	workspace := filepath.Join(home, ".apm")
	writeIntegrationFile(t, filepath.Join(workspace, "apm.yml"), manifest)
	client := apm.New(executor.New(), apm.Global)
	if result, err := client.Run(ctx, "install", "-g"); err != nil {
		t.Fatalf("install manifestless fixtures: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}

	statusApp := &App{StateDir: state}
	statusApp.SetFallbackExecutor(executor.New())
	status, err := statusApp.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"smart-docs", "shiplight"} {
		row := ownedPackageRow(t, status.Packages, name)
		if row.Status != AgentsPackageInstalled || len(row.Provides) != 0 || strings.Contains(strings.Join(row.Issues, "\n"), "ownership evidence unavailable") {
			t.Fatalf("manifestless package %s = %#v", name, row)
		}
	}
	if !hasAgentsService(status.MCP, "shiplight") {
		t.Fatalf("standalone Shiplight MCP missing: %#v", status.MCP)
	}
	result := &DoctorResult{}
	statusApp.doctorAgentsOwnedChildren(result, workspace)
	if len(result.Checks) != 1 || result.Checks[0].Status != DoctorStatusOK {
		t.Fatalf("ownership doctor = %#v", result.Checks)
	}

	template, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	writeIntegrationFile(t, template, manifest)
	if _, err := statusApp.AgentsSyncAll(ctx, AgentsSyncAllOptions{ForceTemplate: true}); err != nil {
		t.Fatalf("sync manifestless fixtures: %v", err)
	}
	status, err = statusApp.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !hasAgentsService(status.MCP, "shiplight") {
		t.Fatalf("sync absorbed standalone Shiplight MCP: %#v", status.MCP)
	}
	assertIntegrationFileContains(t, filepath.Join(home, ".codex", "config.toml"), "mcp_servers.shiplight")
	assertIntegrationFileContains(t, filepath.Join(home, ".agents", "skills", "smart-docs", "SKILL.md"), "DeepWiki")
	assertIntegrationFileContains(t, filepath.Join(home, ".agents", "skills", "shiplight", "SKILL.md"), "Shiplight")
}

func isolateAgentsAPMIntegration(t *testing.T) (root, home, state string) {
	t.Helper()
	root = t.TempDir()
	home = filepath.Join(root, "home")
	state = filepath.Join(root, "state")
	for _, dir := range []string{filepath.Join(home, ".apm"), state} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg-state"))
	t.Setenv("APM_E2E_TESTS", "1")
	gitConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(gitConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(name, "localhost,127.0.0.1,::1")
	}
	return root, home, state
}

func requireMigrationPinnedAPM(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("apm")
	if err != nil {
		t.Fatalf("integration tests require apm %s on PATH: %v", apmVersionPin, err)
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil || !slices.Contains(strings.Fields(string(output)), apmVersionPin) {
		t.Fatalf("integration tests require exactly apm %s, got %q: %v", apmVersionPin, strings.TrimSpace(string(output)), err)
	}
}

func quoteJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func writeIntegrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertIntegrationFileContains(t *testing.T, path string, values ...string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !bytes.Contains(content, []byte(value)) {
			t.Fatalf("%s does not contain %q:\n%s", path, value, content)
		}
	}
}

func initIntegrationGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-b", "master"}, {"config", "user.email", "integration@example.test"}, {"config", "user.name", "Omni Integration"}, {"add", "."}, {"commit", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

func integrationGitHTTPHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := exec.Command("git", "http-backend")
		cmd.Env = append(os.Environ(),
			"GIT_PROJECT_ROOT="+root,
			"GIT_HTTP_EXPORT_ALL=1",
			"PATH_INFO="+r.URL.Path,
			"REQUEST_METHOD="+r.Method,
			"QUERY_STRING="+r.URL.RawQuery,
			"CONTENT_TYPE="+r.Header.Get("Content-Type"),
			fmt.Sprintf("CONTENT_LENGTH=%d", r.ContentLength),
		)
		cmd.Stdin = r.Body
		output, err := cmd.Output()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		head, body, ok := bytes.Cut(output, []byte("\r\n\r\n"))
		if !ok {
			http.Error(w, "invalid git response", http.StatusInternalServerError)
			return
		}
		for _, line := range bytes.Split(head, []byte("\r\n")) {
			key, value, found := bytes.Cut(line, []byte(":"))
			if found {
				w.Header().Add(string(key), strings.TrimSpace(string(value)))
			}
		}
		_, _ = w.Write(body)
	})
}
