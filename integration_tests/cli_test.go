//go:build integration

package integration_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/lkshrk/omni/internal/cli"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

//go:embed testdata/github_cli_latest_release.json
var githubCLILatestRelease []byte

// Registers "omni" as a testscript command so the real binary runs in a subprocess, with no separate build step.
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"omni":                                func() int { cli.Execute(); return 0 },
		"omni-assert-claude-mcp-header":       assertClaudeMcpHeaderMain,
		"omni-assert-codex-mcp-header":        assertCodexMcpHeaderMain,
		"omni-assert-grok-mcp-header":         assertGrokMcpHeaderMain,
		"omni-mark-outdated-refresh-fresh":    markOutdatedRefreshFreshMain,
		"omni-seed-cache":                     seedCacheMain,
		"omni-seed-package-availability":      seedPackageAvailabilityMain,
		"omni-seed-update-metadata":           seedUpdateMetadataMain,
		"omni-assert-tool-provider-list":      assertToolProviderListMain,
		"omni-with-npm-registry":              withNPMRegistryMain,
		"omni-tools-fallback-configured-git":  toolsFallbackConfiguredGitMain,
		"omni-tools-fallback-unsupported-git": toolsFallbackUnsupportedGitMain,
		"omni-native-fallback-install":        nativeFallbackInstallMain,
		"omni-corrupt-skill-metadata":         corruptSkillMetadataMain,
		"omni-wellknown-skills":               wellKnownSkillsMain,
		"omni-with-skills-catalog":            withSkillsCatalogMain,
	}))
}

func rewriteStubServerURL(configPath, serverURL string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if !bytes.Contains(data, []byte("STUB_SERVER_URL")) {
		return fmt.Errorf("config is missing STUB_SERVER_URL")
	}
	data = bytes.ReplaceAll(data, []byte("STUB_SERVER_URL"), []byte(serverURL))
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func runMcpRestore(configPath string) error {
	root := cli.NewRootCmd()
	root.SetArgs([]string{"--config", configPath, "agents", "mcp", "restore"})
	return root.Execute()
}

// Each invocation stands up a fresh stub on a fresh port, so a moved URL is an identity change restore reports as drift; resolve is the verb that applies it.
func runMcpConverge(configPath, serverName string, resolve bool) error {
	if !resolve {
		return runMcpRestore(configPath)
	}
	root := cli.NewRootCmd()
	root.SetArgs([]string{"--yes", "--config", configPath, "agents", "mcp", "resolve", serverName, "--use-managed"})
	return root.Execute()
}

func mcpHeaderHelperArgs(usage string) (configPath, serverName, headerName, want string, resolve bool, ok bool) {
	args := os.Args[1:]
	if len(args) < 4 || len(args) > 5 {
		fmt.Fprintln(os.Stderr, usage)
		return "", "", "", "", false, false
	}
	if len(args) == 5 {
		if args[4] != "resolve" {
			fmt.Fprintln(os.Stderr, usage)
			return "", "", "", "", false, false
		}
		resolve = true
	}
	return args[0], args[1], args[2], args[3], resolve, true
}

func assertClaudeMcpHeaderMain() int {
	configPath, serverName, headerName, want, resolve, ok := mcpHeaderHelperArgs(
		"usage: omni-assert-claude-mcp-header <config> <server> <header> <value> [resolve]")
	if !ok {
		return 2
	}
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- r.Header.Get(headerName):
		default:
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	if err := rewriteStubServerURL(configPath, server.URL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := runMcpConverge(configPath, serverName, resolve); err != nil {
		fmt.Fprintf(os.Stderr, "converge MCP server: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "claude", "mcp", "get", serverName).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude mcp get: %v: %s\n", err, output)
		return 1
	}
	select {
	case got := <-received:
		if got != want {
			fmt.Fprintf(os.Stderr, "captured header %s = %q, want %q\n", headerName, got, want)
			return 1
		}
		fmt.Printf("captured header %s\n", headerName)
		return 0
	case <-time.After(3 * time.Second):
		fmt.Fprintln(os.Stderr, "claude did not contact the MCP server")
		return 1
	}
}

func assertGrokMcpHeaderMain() int {
	configPath, serverName, headerName, want, resolve, ok := mcpHeaderHelperArgs(
		"usage: omni-assert-grok-mcp-header <config> <server> <header> <value> [resolve]")
	if !ok {
		return 2
	}
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value := r.Header.Get(headerName); value != "" {
			select {
			case received <- value:
			default:
			}
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if err := rewriteStubServerURL(configPath, server.URL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := runMcpConverge(configPath, serverName, resolve); err != nil {
		fmt.Fprintf(os.Stderr, "converge MCP server: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "grok", "mcp", "doctor", serverName, "--json")
	_ = cmd.Run() // A 500 response makes doctor fail after emitting the header.
	if ctx.Err() != nil {
		fmt.Fprintf(os.Stderr, "grok mcp doctor: %v\n", ctx.Err())
		return 1
	}
	select {
	case got := <-received:
		if got != want {
			fmt.Fprintf(os.Stderr, "captured header %s = %q, want %q\n", headerName, got, want)
			return 1
		}
		fmt.Printf("captured header %s\n", headerName)
		return 0
	case <-time.After(3 * time.Second):
		fmt.Fprintln(os.Stderr, "grok did not contact the MCP server")
		return 1
	}
}

func assertCodexMcpHeaderMain() int {
	args := os.Args[1:]
	if len(args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: omni-assert-codex-mcp-header <config> <server> <header> <value>")
		return 2
	}
	configPath, _, headerName, want := args[0], args[1], args[2], args[3]
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			if value := r.Header.Get(headerName); value != "" {
				select {
				case received <- value:
				default:
				}
			}
			w.WriteHeader(http.StatusInternalServerError)
		case "/v1/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-probe\"}}\n\n")
			fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-probe\",\"usage\":{\"input_tokens\":0,\"input_tokens_details\":null,\"output_tokens\":0,\"output_tokens_details\":null,\"total_tokens\":0}}}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := rewriteStubServerURL(configPath, server.URL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := runMcpRestore(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "restore MCP server: %v\n", err)
		return 1
	}
	codexHome := os.Getenv("CODEX_HOME")
	configFile := filepath.Join(codexHome, "config.toml")
	data, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read Codex config: %v\n", err)
		return 1
	}
	prefix := fmt.Sprintf("model = \"gpt-5-codex\"\nmodel_provider = \"probe\"\n\n[model_providers.probe]\nname = \"Local probe\"\nbase_url = %q\nwire_api = \"responses\"\n\n", server.URL+"/v1")
	if err := os.WriteFile(configFile, append([]byte(prefix), data...), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write Codex config: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "exec", "--skip-git-repo-check", "say done")
	cmd.Stdin = strings.NewReader("")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "OPENAI_API_KEY=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex exec: %v: %s\n", err, output)
		return 1
	}
	select {
	case got := <-received:
		if got != want {
			fmt.Fprintf(os.Stderr, "captured header %s = %q, want %q\n", headerName, got, want)
			return 1
		}
		fmt.Printf("captured header %s\n", headerName)
		return 0
	case <-time.After(3 * time.Second):
		fmt.Fprintln(os.Stderr, "codex did not contact the MCP server")
		return 1
	}
}

func assertToolProviderListMain() int {
	args := os.Args[1:]
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: omni-assert-tool-provider-list <config> <tool> [<provider>[=<package>] ...]")
		return 2
	}
	cfg, err := config.Load(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return 1
	}
	spec, ok := cfg.Tools[args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "tool %q missing\n", args[1])
		return 1
	}
	if spec.Provider != "" || spec.Package != "" || spec.InstallWith != "" {
		fmt.Fprintf(os.Stderr, "legacy fields still populated: provider=%q package=%q install_with=%q\n", spec.Provider, spec.Package, spec.InstallWith)
		return 1
	}
	want := args[2:]
	if len(spec.Providers) != len(want) {
		fmt.Fprintf(os.Stderr, "providers = %+v, want %v\n", spec.Providers, want)
		return 1
	}
	for i, providerName := range want {
		wantProvider, wantPackage, hasPackage := strings.Cut(providerName, "=")
		if spec.Providers[i].Provider != wantProvider {
			fmt.Fprintf(os.Stderr, "providers = %+v, want %v\n", spec.Providers, want)
			return 1
		}
		if hasPackage && spec.Providers[i].Package != wantPackage {
			fmt.Fprintf(os.Stderr, "provider %q package = %q, want %q in %+v\n", wantProvider, spec.Providers[i].Package, wantPackage, spec.Providers)
			return 1
		}
	}
	return 0
}

func withNPMRegistryMain() int {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/v1/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("text") != "prettier" {
			fmt.Fprint(w, `{"objects":[]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if os.Getenv("OMNI_TEST_NPM_WEAK_SEARCH") == "1" {
			fmt.Fprint(w, `{"objects":[{"package":{"name":"prettier-plugin-tailwindcss","version":"0.6.14","description":"Tailwind CSS class sorter for Prettier"}}]}`)
			return
		}
		fmt.Fprint(w, `{"objects":[{"package":{"name":"prettier","version":"3.5.0","description":"Prettier formatter","links":{"repository":"https://github.com/prettier/prettier"}}}]}`)
	}))
	defer server.Close()
	if err := os.Setenv("OMNI_TEST_NPM_REGISTRY_URL", server.URL); err != nil {
		fmt.Fprintf(os.Stderr, "set OMNI_TEST_NPM_REGISTRY_URL: %v\n", err)
		return 1
	}
	if err := os.Setenv("OMNI_TEST_ISOLATED", "1"); err != nil {
		fmt.Fprintf(os.Stderr, "set OMNI_TEST_ISOLATED: %v\n", err)
		return 1
	}
	cmd := cli.NewRootCmd()
	cmd.SetArgs(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// Stub catalog whose result set includes one row no client can use; each query is appended to $OMNI_TEST_CATALOG_LOG.
// Usage in txtar: exec omni-with-skills-catalog --config settings.json <cmd> [args...]
func withSkillsCatalogMain() int {
	logPath := os.Getenv("OMNI_TEST_CATALOG_LOG")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if logPath != "" {
			file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Fprintln(file, r.URL.RawQuery) //nolint:errcheck
			file.Close()                       //nolint:errcheck
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("owner") == "acme" {
			fmt.Fprint(w, `{"skills":[{"source":"acme/skills","skillId":"acme-tool","installs":5}]}`)
			return
		}
		fmt.Fprint(w, `{"skills":[
			{"source":"owner/repo","skillId":"review","installs":12},
			{"source":"owner/repo.git","skillId":"broken","installs":9},
			{"source":"owner/other","skillId":"other","installs":3}
		]}`)
	}))
	defer server.Close()
	if err := os.Setenv("OMNI_SKILLS_CATALOG_URL", server.URL); err != nil {
		fmt.Fprintf(os.Stderr, "set OMNI_SKILLS_CATALOG_URL: %v\n", err)
		return 1
	}
	cmd := cli.NewRootCmd()
	cmd.SetArgs(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// The CLI builds its own client with a nil Transport, so the stub CA is the only seam; the URL stays https and verified.
func trustStubCA(server *httptest.Server) error {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("http.DefaultTransport is not *http.Transport")
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return nil
}

func toolsFallbackConfiguredGitMain() int {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/cli/cli/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(githubCLILatestRelease); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	if err := trustStubCA(server); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Setenv("OMNI_GITHUB_API_BASE", server.URL); err != nil {
		fmt.Fprintf(os.Stderr, "set OMNI_GITHUB_API_BASE: %v\n", err)
		return 1
	}
	cmd := cli.NewRootCmd()
	cmd.SetArgs(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func toolsFallbackUnsupportedGitMain() int {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/cli/cli/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
  "id": 330388700,
  "tag_name": "v2.93.0",
  "published_at": "2026-05-27T17:47:41Z",
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "id": 431301999,
      "name": "gh_2.93.0_unsupportedos_unsupportedarch.tar.gz",
      "browser_download_url": "https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_unsupportedos_unsupportedarch.tar.gz"
    }
  ]
}`)
	}))
	defer server.Close()
	if err := trustStubCA(server); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Setenv("OMNI_GITHUB_API_BASE", server.URL); err != nil {
		fmt.Fprintf(os.Stderr, "set OMNI_GITHUB_API_BASE: %v\n", err)
		return 1
	}
	cmd := cli.NewRootCmd()
	cmd.SetArgs(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// Serves a valid 0.2.0 index, a digest mismatch, an unsupported schema, and a forbidden probe path that must send the caller on to Git.
// Usage in txtar: exec omni-wellknown-skills --config settings.json <cmd> [args...]
func wellKnownSkillsMain() int {
	const schemaV02 = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"
	skillMD := []byte("---\nname: hello\ndescription: Served by a well-known skills index.\n---\n\nhello\n")
	sum := sha256.Sum256(skillMD)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeIndex := func(schema, name, digest string) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"$schema": schema,
				"skills": []map[string]any{{
					"name":        name,
					"type":        "skill-md",
					"description": "Served by a well-known skills index.",
					"url":         "artifact/" + name + ".md",
					"digest":      digest,
				}},
			})
		}
		switch r.URL.Path {
		case "/good/index.json":
			writeIndex(schemaV02, "hello", digest)
		case "/bad-digest/index.json":
			writeIndex(schemaV02, "broken", "sha256:"+strings.Repeat("0", 64))
		case "/old-schema/index.json":
			writeIndex("https://schemas.agentskills.io/discovery/0.1.0/schema.json", "legacy", digest)
		case "/good/artifact/hello.md", "/bad-digest/artifact/broken.md", "/old-schema/artifact/legacy.md":
			w.Write(skillMD) //nolint:errcheck
		case "/.well-known/agent-skills/index.json":
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			if err := rewriteStubServerURL(args[i+1], server.URL); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			break
		}
	}
	cmd := cli.NewRootCmd()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func seedCacheMain() int {
	args := os.Args[1:]
	if len(args) < 5 || len(args) > 6 {
		fmt.Fprintln(os.Stderr, "usage: omni-seed-cache <name> <provider> <package> <version> <latest> [installed-with]")
		return 2
	}
	cacheDir := os.Getenv("OMNI_CACHE_DIR")
	if cacheDir == "" {
		fmt.Fprintln(os.Stderr, "OMNI_CACHE_DIR is required")
		return 2
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create cache dir: %v\n", err)
		return 1
	}
	db, err := database.Open(filepath.Join(cacheDir, "omni.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate db: %v\n", err)
		return 1
	}
	installedWith := ""
	if len(args) == 6 {
		installedWith = args[5]
	}
	entry := &database.ToolCache{
		Name:          args[0],
		Provider:      args[1],
		Package:       args[2],
		Installed:     true,
		InstalledWith: installedWith,
		Version:       sql.NullString{String: args[3], Valid: args[3] != ""},
		LastChecked:   time.Now(),
		Tracked:       true,
	}
	if err := db.Upsert(ctx, entry); err != nil {
		fmt.Fprintf(os.Stderr, "seed cache: %v\n", err)
		return 1
	}
	if err := db.UpdateOutdated(ctx, args[0], args[1], args[2], true, args[4]); err != nil {
		fmt.Fprintf(os.Stderr, "seed outdated: %v\n", err)
		return 1
	}
	return 0
}

func seedUpdateMetadataMain() int {
	args := os.Args[1:]
	if len(args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: omni-seed-update-metadata <provider> <package> <version> <age>")
		return 2
	}
	cacheDir := os.Getenv("OMNI_CACHE_DIR")
	if cacheDir == "" {
		fmt.Fprintln(os.Stderr, "OMNI_CACHE_DIR is required")
		return 2
	}
	age, err := time.ParseDuration(args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse age: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create cache dir: %v\n", err)
		return 1
	}
	db, err := database.Open(filepath.Join(cacheDir, "omni.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate db: %v\n", err)
		return 1
	}
	if err := db.UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    args[0],
		Package:     args[1],
		Version:     args[2],
		AvailableAt: time.Now().Add(-age),
		DateSource:  "testscript",
		CheckedAt:   time.Now(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "seed update metadata: %v\n", err)
		return 1
	}
	return 0
}

func seedPackageAvailabilityMain() int {
	args := os.Args[1:]
	if len(args) < 4 || len(args) > 5 {
		fmt.Fprintln(os.Stderr, "usage: omni-seed-package-availability <name> <provider> <package> <available> [reason]")
		return 2
	}
	cacheDir := os.Getenv("OMNI_CACHE_DIR")
	if cacheDir == "" {
		fmt.Fprintln(os.Stderr, "OMNI_CACHE_DIR is required")
		return 2
	}
	available, err := strconv.ParseBool(args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse available: %v\n", err)
		return 2
	}
	reason := ""
	if len(args) == 5 {
		reason = args[4]
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create cache dir: %v\n", err)
		return 1
	}
	db, err := database.Open(filepath.Join(cacheDir, "omni.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate db: %v\n", err)
		return 1
	}
	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      args[0],
		Provider:  args[1],
		Package:   args[2],
		Available: available,
		Reason:    reason,
		CheckedAt: time.Now(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "seed package availability: %v\n", err)
		return 1
	}
	return 0
}

// Overwrites every canonical skill package's install metadata with unparsable JSON, so fixtures need no hand-written SQLite.
// Usage in txtar: exec omni-corrupt-skill-metadata <packages-root>
func corruptSkillMetadataMain() int {
	args := os.Args[1:]
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: omni-corrupt-skill-metadata <packages-root>")
		return 2
	}
	cacheDir := os.Getenv("OMNI_CACHE_DIR")
	if cacheDir == "" {
		fmt.Fprintln(os.Stderr, "OMNI_CACHE_DIR is required")
		return 2
	}
	entries, err := os.ReadDir(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read packages root: %v\n", err)
		return 1
	}
	db, err := database.Open(filepath.Join(cacheDir, "omni.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate db: %v\n", err)
		return 1
	}
	corrupted := 0
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 64 {
			continue
		}
		if err := db.SetState(ctx, "agent.skills."+entry.Name(), "{"); err != nil {
			fmt.Fprintf(os.Stderr, "corrupt install metadata: %v\n", err)
			return 1
		}
		corrupted++
	}
	if corrupted == 0 {
		fmt.Fprintf(os.Stderr, "no canonical skill packages under %s\n", args[0])
		return 1
	}
	return 0
}

func markOutdatedRefreshFreshMain() int {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: omni-mark-outdated-refresh-fresh")
		return 2
	}
	cacheDir := os.Getenv("OMNI_CACHE_DIR")
	if cacheDir == "" {
		fmt.Fprintln(os.Stderr, "OMNI_CACHE_DIR is required")
		return 2
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create cache dir: %v\n", err)
		return 1
	}
	db, err := database.Open(filepath.Join(cacheDir, "omni.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate db: %v\n", err)
		return 1
	}
	if err := db.SetState(ctx, "last_refresh_outdated", time.Now().UTC().Format(time.RFC3339)); err != nil {
		fmt.Fprintf(os.Stderr, "mark outdated refresh fresh: %v\n", err)
		return 1
	}
	return 0
}

// Runs testdata/scripts/*.txtar, skipping dependency- and OS-gated fixtures the local environment cannot satisfy.
func TestCLI(t *testing.T) {
	scripts, err := stowAwareScriptFiles()
	if err != nil {
		t.Fatal(err)
	}

	testscript.Run(t, testscript.Params{
		Files:               scripts,
		RequireExplicitExec: true,
		Setup: func(env *testscript.Env) error {
			// Cache dir points at the per-test work dir so SQLite stays writable under testscript's HOME=/no-home.
			env.Vars = append(env.Vars, "OMNI_CACHE_DIR="+filepath.Join(env.WorkDir, ".omni-cache"))
			// Use a fixed hostname so txtar scripts can configure hosts deterministically.
			env.Vars = append(env.Vars, "OMNI_HOSTNAME=testhost")
			return nil
		},
	})
}

func stowAwareScriptFiles() ([]string, error) {
	entries, err := os.ReadDir("testdata/scripts")
	if err != nil {
		return nil, err
	}

	_, stowErr := exec.LookPath("stow")
	_, brewErr := exec.LookPath("brew")
	_, claudeErr := exec.LookPath("claude")
	_, codexErr := exec.LookPath("codex")
	_, grokErr := exec.LookPath("grok")
	var stowMissing []string
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".txtar" {
			continue
		}
		path := filepath.Join("testdata/scripts", name)
		requiresLinux, err := scriptRequires(path, "linux")
		if err != nil {
			return nil, err
		}
		if requiresLinux && runtime.GOOS != "linux" {
			continue
		}
		requiresStow, err := scriptRequires(path, "stow")
		if err != nil {
			return nil, err
		}
		if stowErr != nil && requiresStow {
			stowMissing = append(stowMissing, name)
			continue
		}
		requiresBrew, err := scriptRequires(path, "brew")
		if err != nil {
			return nil, err
		}
		if requiresBrew && brewErr != nil {
			continue
		}
		requiresCodex, err := scriptRequires(path, "codex")
		if err != nil {
			return nil, err
		}
		if requiresCodex && codexErr != nil {
			continue
		}
		requiresClaude, err := scriptRequires(path, "claude")
		if err != nil {
			return nil, err
		}
		if requiresClaude && claudeErr != nil {
			continue
		}
		requiresGrok, err := scriptRequires(path, "grok")
		if err != nil {
			return nil, err
		}
		if requiresGrok && grokErr != nil {
			continue
		}
		files = append(files, path)
	}

	if len(stowMissing) > 0 {
		return nil, fmt.Errorf(
			"GNU Stow is required for %d integration fixture(s): %s\n"+
				"Install it locally (brew install stow | apt-get install -y stow | pacman -S stow | dnf install stow)",
			len(stowMissing), strings.Join(stowMissing, ", "),
		)
	}
	return files, nil
}

func scriptRequires(path, requirement string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if strings.Contains(line, "@requires:") && strings.Contains(line, requirement) {
				return true, nil
			}
			continue
		}
		break
	}
	if err := scan.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// Stub server for the GitHub releases API, asset download, and checksums; rewrites STUB_SERVER_URL in the --config file at runtime.
// Usage in txtar: exec omni-native-fallback-install --config settings.json <cmd> [args...]
func nativeFallbackInstallMain() int {
	configuredManifest := os.Getenv("OMNI_TEST_NATIVE_FALLBACK_CONFIGURED_MANIFEST") == "1"
	failManifest := os.Getenv("OMNI_TEST_NATIVE_FALLBACK_MANIFEST_FAILURE") == "1"
	binaryVersion := "native-v1"
	if failManifest {
		binaryVersion = "native-v2"
	}
	binaryContent := []byte("#!/bin/sh\nprintf '%s\\n' " + binaryVersion + "\n")
	assetName := fmt.Sprintf("mytool_v1.0.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	var assetBuf bytes.Buffer
	gw := gzip.NewWriter(&assetBuf)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "mytool_v1.0.0/mytool", Mode: 0o755, Size: int64(len(binaryContent))}) //nolint:errcheck
	tw.Write(binaryContent)                                                                                 //nolint:errcheck
	tw.Close()                                                                                              //nolint:errcheck
	gw.Close()                                                                                              //nolint:errcheck
	assetBytes := assetBuf.Bytes()

	sum := sha256.Sum256(assetBytes)
	digest := hex.EncodeToString(sum[:])
	checksumContent := digest + "  " + assetName + "\n"

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releaseAssets := func(host string) []map[string]any {
			return []map[string]any{
				{"id": 1, "name": assetName, "browser_download_url": "https://" + host + "/asset/" + assetName},
				{"id": 2, "name": "checksums.txt", "browser_download_url": "https://" + host + "/checksums"},
				{"id": 3, "name": "configured.manifest", "browser_download_url": "https://" + host + "/configured-manifest"},
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"),
			strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.0"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"id": 1, "tag_name": "v1.0.0", "published_at": "2026-06-01T00:00:00Z",
				"assets": releaseAssets(r.Host),
			})
		case strings.HasPrefix(r.URL.Path, "/asset/"):
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(assetBytes) //nolint:errcheck
		case r.URL.Path == "/checksums":
			w.Header().Set("Content-Type", "text/plain")
			if configuredManifest {
				w.Write([]byte(strings.Repeat("0", 64) + "  " + assetName + "\n")) //nolint:errcheck
			} else {
				w.Write([]byte(checksumContent)) //nolint:errcheck
			}
		case r.URL.Path == "/configured-manifest":
			if failManifest {
				http.Error(w, "manifest unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(checksumContent)) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Trust the stub CA through DefaultTransport to retain verified HTTPS without a production bypass.
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		fmt.Fprintln(os.Stderr, "http.DefaultTransport is not *http.Transport")
		return 1
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}

	// Rewrite every placeholder and any previously written 127.0.0.1 asset URL, so re-invocations never hit a dead port.
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			cfgPath := args[i+1]
			if b, err := os.ReadFile(cfgPath); err == nil {
				s := string(b)
				s = strings.ReplaceAll(s, "STUB_SERVER_URL", server.URL)
				s = strings.ReplaceAll(s, "ASSET_NAME_PLACEHOLDER", assetName)
				s = rewriteLocalhostAssetURL(s, server.URL+"/asset/")
				if s != string(b) {
					_ = os.WriteFile(cfgPath, []byte(s), 0o644)
				}
			}
			break
		}
	}

	if err := os.Setenv("OMNI_GITHUB_API_BASE", server.URL); err != nil {
		fmt.Fprintf(os.Stderr, "set OMNI_GITHUB_API_BASE: %v\n", err)
		return 1
	}
	cmd := cli.NewRootCmd()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// Repoints stale "https://127.0.0.1:<port>/asset/" occurrences at the live server.
var localhostAssetRe = regexp.MustCompile(`https://127\.0\.0\.1:\d+/asset/`)

func rewriteLocalhostAssetURL(s, newAssetBase string) string {
	return localhostAssetRe.ReplaceAllString(s, newAssetBase)
}
