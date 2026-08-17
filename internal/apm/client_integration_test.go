//go:build integration

package apm_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	commandexec "github.com/lkshrk/omni/internal/executor"
)

func TestGlobalInstallReplaysManifestIntoIsolatedUserScope(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	apmHome := filepath.Join(root, "apm-home")
	codexHome := filepath.Join(root, "codex-home")
	work := filepath.Join(root, "work")
	pkg := filepath.Join(root, "fixture-skill")
	for _, dir := range []string{filepath.Join(home, ".apm"), apmHome, codexHome, work, pkg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("APM_HOME", apmHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	t.Chdir(work)

	writeFile(t, filepath.Join(pkg, "apm.yml"), `name: fixture-skill
version: 1.0.0
type: skill
dependencies:
  apm: []
  mcp: []
`)
	writeFile(t, filepath.Join(pkg, "SKILL.md"), `---
name: fixture-skill
description: Offline APM integration fixture
---

# Fixture skill
`)
	writeFile(t, filepath.Join(pkg, ".apm", "agents", "reviewer.agent.md"), "---\nname: reviewer\ndescription: Reviews integration fixtures.\n---\n\nReview the fixture.\n")
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: omni-integration\nversion: 1.0.0\ntargets:\n  - codex\n  - claude\ndependencies:\n  apm:\n    - "+pkg+"\n  mcp: []\n")

	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{DryRun: true}); err != nil {
		t.Fatalf("global dry-run: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.lock.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a lockfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "fixture-skill", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run deployed a skill: %v", err)
	}
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{}); err != nil {
		t.Fatalf("initial global install: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}

	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	if content, err := os.ReadFile(lock); err != nil || !strings.Contains(string(content), "fixture-skill") {
		t.Fatalf("lockfile missing installed package: error=%v content=%q", err, content)
	}
	deployed := filepath.Join(home, ".agents", "skills", "fixture-skill", "SKILL.md")
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("skill was not deployed: %v", err)
	}
	for _, path := range []string{
		filepath.Join(home, ".codex", "agents", "reviewer.toml"),
		filepath.Join(home, ".claude", "agents", "reviewer.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("target-specific agent was not deployed to %s: %v", path, err)
		}
	}

	if err := os.RemoveAll(filepath.Dir(deployed)); err != nil {
		t.Fatal(err)
	}
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Frozen: true}); err != nil {
		t.Fatalf("frozen global install: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("frozen manifest install did not restore skill: %v", err)
	}
}

func TestGlobalPackageLifecycleAndLocalMarketplaceSearch(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	market := filepath.Join(root, "marketplace")
	pkg := filepath.Join(market, "packages", "searchable-skill")
	for _, dir := range []string{filepath.Join(home, ".apm"), pkg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: lifecycle\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm:\n    - "+pkg+"\n  mcp: []\n")
	writeFile(t, filepath.Join(pkg, "apm.yml"), "name: searchable-skill\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeFile(t, filepath.Join(pkg, "SKILL.md"), "---\nname: searchable-skill\ndescription: Searchable offline fixture\n---\n")
	writeFile(t, filepath.Join(market, "apm.yml"), "name: local-marketplace\nversion: 0.1.0\nmarketplace:\n  owner:\n    name: omni\n    url: https://example.invalid/omni\n  outputs:\n    claude: {}\n  packages:\n    - name: searchable-skill\n      description: Searchable offline fixture\n      source: ./packages/searchable-skill\n      version: 1.0.0\n")

	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{}); err != nil {
		t.Fatalf("install: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	deployed := filepath.Join(home, ".agents", "skills", "searchable-skill", "SKILL.md")
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("install did not deploy skill: %v", err)
	}
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Update: true}); err != nil {
		t.Fatalf("update: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}

	pack := exec.CommandContext(ctx, "apm", "pack", "--marketplace=claude")
	pack.Dir = market
	pack.Env = os.Environ()
	if output, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("pack marketplace: %v\n%s", err, output)
	}
	register := exec.CommandContext(ctx, "apm", "marketplace", "add", market, "--name", "local")
	register.Env = os.Environ()
	if output, err := register.CombinedOutput(); err != nil {
		t.Fatalf("register marketplace: %v\n%s", err, output)
	}
	if result, err := apm.New(commandexec.New(), apm.Project).Search(ctx, "searchable@local"); err != nil || !strings.Contains(result.Stdout, "searchable-skill") {
		t.Fatalf("search: err=%v stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	if result, err := client.Uninstall(ctx, pkg); err != nil {
		t.Fatalf("remove: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(deployed); !os.IsNotExist(err) {
		t.Fatalf("remove left deployed skill: %v", err)
	}
}

// APM reports this failure on stdout only; the wrapper must surface it as error detail.
func TestGlobalInstallFailureSurfacesStdoutDetail(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))

	_, err := apm.New(commandexec.New(), apm.Global).InstallOnly(ctx, apm.SurfacePackages, []string{"claude"}, apm.InstallOptions{Frozen: true})
	if err == nil {
		t.Fatal("expected the replay to fail without a global manifest")
	}
	if !strings.Contains(err.Error(), "apm.yml found") {
		t.Fatalf("error lacks apm stdout detail: %v", err)
	}
}

func TestGlobalUpdateRefreshesRemoteGitPackage(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("integration tests require git on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	remote := filepath.Join(root, "owner", "fixture.git")
	work := filepath.Join(root, "work")
	for _, dir := range []string{filepath.Join(home, ".apm"), work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	runGit(t, root, "config", "--global", "http.sslVerify", "false")
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "--bare", remote)
	runGit(t, work, "init", "-b", "main")
	runGit(t, work, "config", "user.email", "integration@example.test")
	runGit(t, work, "config", "user.name", "APM Integration")
	writeFile(t, filepath.Join(work, "apm.yml"), "name: update-fixture\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeFile(t, filepath.Join(work, "SKILL.md"), "---\nname: update-fixture\ndescription: Version one\n---\n\nversion one\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "v1")
	runGit(t, work, "remote", "add", "origin", remote)
	runGit(t, work, "push", "-u", "origin", "main")

	server := httptest.NewTLSServer(gitHTTPHandler(root))
	t.Cleanup(server.Close)
	gitURL := server.URL + "/owner/fixture"
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: update-integration\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm:\n    - git: "+gitURL+"\n      ref: main\n  mcp: []\n")
	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{}); err != nil {
		t.Fatalf("install v1: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	deployed := filepath.Join(home, ".agents", "skills", "fixture", "SKILL.md")
	if content, err := os.ReadFile(deployed); err != nil || !strings.Contains(string(content), "version one") {
		t.Fatalf("v1 deployment: err=%v content=%q", err, content)
	}
	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	lockV1, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(work, "SKILL.md"), "---\nname: update-fixture\ndescription: Version two\n---\n\nversion two\n")
	runGit(t, work, "add", "SKILL.md")
	runGit(t, work, "commit", "-m", "v2")
	runGit(t, work, "push", "origin", "main")
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Update: true}); err != nil {
		t.Fatalf("update v2: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	if content, err := os.ReadFile(deployed); err != nil || !strings.Contains(string(content), "version two") {
		t.Fatalf("v2 deployment: err=%v content=%q", err, content)
	}
	if lockV2, err := os.ReadFile(lock); err != nil || bytes.Equal(lockV1, lockV2) {
		t.Fatalf("lockfile did not change: err=%v", err)
	}
}

func blockExternalNetwork(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitHTTPHandler(root string) http.Handler {
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
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			http.Error(w, stderr.String(), http.StatusInternalServerError)
			return
		}
		parts := bytes.SplitN(output, []byte("\r\n\r\n"), 2)
		if len(parts) != 2 {
			http.Error(w, "invalid git http-backend response", http.StatusInternalServerError)
			return
		}
		status := http.StatusOK
		for _, line := range strings.Split(string(parts[0]), "\r\n") {
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			if name == "Status" {
				_, _ = fmt.Sscanf(value, "%d", &status)
			} else {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(parts[1])
	})
}
