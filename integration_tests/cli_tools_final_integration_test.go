//go:build integration

package integration_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryToolsFinalMaintenanceFlows(t *testing.T) {
	t.Run("tools.pin_provider", func(t *testing.T) {
		root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}})
		runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "set", "fixture", "--provider", "node", "--package", "fixture-cli", "--install-with", "pnpm")
		spec := loadFinalToolsConfig(t, configPath).Tools["fixture"]
		if len(spec.Providers) != 1 || spec.Providers[0].Provider != "pnpm" || spec.Providers[0].Package != "fixture-cli" {
			t.Fatalf("pinned provider spec = %#v", spec)
		}
	})

	t.Run("tools.normalize_provider_overrides", func(t *testing.T) {
		root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{
			Settings: config.Settings{Ecosystems: map[string]config.EcosystemSettings{"node": {Manager: "pnpm"}}},
			Tools:    map[string]config.ToolSpec{"fixture": {Providers: []config.ToolInstallSpec{{Provider: "node", Package: "fixture-cli", InstallWith: "pnpm"}}}},
			Hosts:    map[string][]string{"testhost": {}},
			Groups:   []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}}},
		})
		out := runOmniOutput(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "normalize", "--default-overrides")
		spec := loadFinalToolsConfig(t, configPath).Tools["fixture"]
		if !strings.Contains(out, "Normalized 1 provider override") || len(spec.Providers) != 1 || spec.Providers[0].Provider != "node" || spec.Providers[0].InstallWith != "" {
			t.Fatalf("normalized override = spec %#v\n%s", spec, out)
		}
	})

	t.Run("tools.consolidate", func(t *testing.T) {
		root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}})
		binDir := filepath.Join(root, "bin")
		writeExecutable(t, filepath.Join(binDir, "pnpm"), "#!/bin/sh\n[ \"${1:-}\" = \"--version\" ] && echo 9.0.0\n")
		env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
		runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "consolidate", "node", "pnpm")
		if got := loadFinalToolsConfig(t, configPath).HostSettings["testhost"].ProviderPriority; !strings.Contains(strings.Join(got, ","), "pnpm") {
			t.Fatalf("node manager priority = %#v", got)
		}
	})

	t.Run("tools.claim", func(t *testing.T) { runFinalToolsBrewMutation(t, false) })
	t.Run("tools.import", func(t *testing.T) { runFinalToolsBrewMutation(t, true) })
	t.Run("tools.reinstall_default", func(t *testing.T) { runFinalToolsPythonMutation(t, true) })
	t.Run("tools.switch_provider", func(t *testing.T) { runFinalToolsPythonMutation(t, false) })

	t.Run("tools.migrate_nvm", func(t *testing.T) {
		root, home, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{
			Settings: config.Settings{ProviderPriority: []string{"brew", "pnpm"}},
			Tools: map[string]config.ToolSpec{
				"pnpm": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "pnpm"}}},
				"node": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "node"}}},
			},
			Hosts:  map[string][]string{"testhost": {}},
			Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "pnpm"}, {Name: "node"}}}},
		})
		nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
		writeExecutable(t, filepath.Join(nvmBin, "node"), "#!/bin/sh\n[ \"${1:-}\" = \"--version\" ] && echo v22.1.0\n")
		writeExecutable(t, filepath.Join(nvmBin, "pnpm"), "#!/bin/sh\ncase \"${1:-}\" in --version) echo 9.0.0;; add) exit 0;; ls) echo '└── pnpm@9.0.0';; esac\n")
		binDir := filepath.Join(root, "bin")
		writeExecutable(t, filepath.Join(binDir, "brew"), "#!/bin/sh\ncase \"${1:-}\" in --version) echo 'Homebrew 4.0.0';; uninstall) exit 0;; esac\n")
		env = replaceIntegrationEnv(env, "PATH", nvmBin+string(os.PathListSeparator)+binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
		env = append(env, "NVM_BIN="+nvmBin)
		runOmniCommand(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "migrate-nvm", "--all")
		cfg := loadFinalToolsConfig(t, configPath)
		if _, ok := cfg.Tools["node"]; ok || len(cfg.Tools["pnpm"].Providers) < 1 || cfg.Tools["pnpm"].Providers[0].Provider != "pnpm" {
			t.Fatalf("migrated nvm config = %#v", cfg.Tools)
		}
	})

	t.Run("tools.baseline_system_inventory", func(t *testing.T) {
		root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Settings: config.Settings{DisabledProviders: []string{"apk", "dnf", "pacman", "zypper", "brew", "node", "bun", "pnpm", "npm", "python", "uv", "pip"}}, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}})
		binDir := filepath.Join(root, "bin")
		writeExecutable(t, filepath.Join(binDir, "apt-get"), "#!/bin/sh\n[ \"${1:-}\" = \"--version\" ] && echo 'apt 3.0'\n")
		writeExecutable(t, filepath.Join(binDir, "apt-mark"), "#!/bin/sh\n[ \"${1:-}\" = \"showmanual\" ] && printf 'libnss3\\nxvfb\\n'\n")
		writeExecutable(t, filepath.Join(binDir, "dpkg-query"), "#!/bin/sh\nprintf 'libnss3\\t1.0\\tii \\nxvfb\\t2.0\\tii \\n'\n")
		env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
		bin := buildOmniBinary(t)
		runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "baseline")
		out := runOmniOutput(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "baseline")
		if !strings.Contains(out, "No system packages to absorb") {
			t.Fatalf("baseline did not persist: %s", out)
		}
	})

	t.Run("tools.fallback", func(t *testing.T) {
		root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Tools: map[string]config.ToolSpec{"fixture": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}}}, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}}}})
		var server *httptest.Server
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/releases/latest") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":1,"tag_name":"v1.0.0","published_at":"2026-08-30T00:00:00Z","assets":[{"id":2,"name":"fixture_1.0_linux_x86_64.tar.gz","browser_download_url":%q},{"id":3,"name":"fixture_1.0_linux_aarch64.tar.gz","browser_download_url":%q}]}`, server.URL+"/fixture-amd64", server.URL+"/fixture-arm64")
				return
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(server.Close)
		certPath := filepath.Join(root, "github-test-ca.pem")
		cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		writeIntegrationFile(t, certPath, string(cert))
		env = append(env, "OMNI_GITHUB_API_BASE="+server.URL, "SSL_CERT_FILE="+certPath)
		runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "fallback", "fixture", "--from-github", "owner/repo")
		fallback := loadFinalToolsConfig(t, configPath).Tools["fixture"].Fallback
		if fallback == nil || fallback.Source.Owner != "owner" || fallback.Source.Repo != "repo" || fallback.Recipe.AssetDownloadURL == "" {
			t.Fatalf("persisted fallback = %#v", fallback)
		}
	})

	t.Run("tools.fallback_unreachable_api", func(t *testing.T) {
		root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Tools: map[string]config.ToolSpec{"fixture": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}}}, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}}}})
		env = append(env, "OMNI_GITHUB_API_BASE=http://127.0.0.1:1")
		out, err := runFinalToolsFailure(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "fallback", "fixture", "--from-github", "owner/repo")
		if err == nil || !strings.Contains(out, "/repos/owner/repo/releases/latest") || loadFinalToolsConfig(t, configPath).Tools["fixture"].Fallback != nil {
			t.Fatalf("fallback fail-closed result = err %v\n%s", err, out)
		}
	})
}

func finalToolsFixture(t *testing.T, cfg *config.RootConfig) (root, home, cache string, env []string, configPath string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	configPath = filepath.Join(root, "settings.json")
	cfg.Version = config.CurrentVersion
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return root, home, cache, env, configPath
}

func loadFinalToolsConfig(t *testing.T, path string) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func finalToolsGroupHas(cfg *config.RootConfig, groupName, toolName string) bool {
	for _, group := range cfg.Groups {
		if group == nil || group.BaseName() != groupName {
			continue
		}
		for _, tool := range group.Tools {
			if tool.Name == toolName {
				return true
			}
		}
	}
	return false
}

func runFinalToolsBrewMutation(t *testing.T, importOnly bool) {
	t.Helper()
	root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}})
	state := filepath.Join(root, "brew-state")
	env = finalToolsFakeBrew(t, root, env, state)
	bin := buildOmniBinary(t)
	name := "fixture"
	if importOnly {
		name = "orphan"
		writeIntegrationFile(t, filepath.Join(state, name), "")
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "import", "--provider", "brew", "--group", "testhost")
	} else {
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "add", name, "--provider", "brew", "--group", "testhost")
	}
	cfg := loadFinalToolsConfig(t, configPath)
	if _, ok := cfg.Tools[name]; !ok || !finalToolsGroupHas(cfg, "testhost", name) {
		t.Fatalf("brew mutation config state = tools %#v, groups %#v", cfg.Tools, cfg.Groups)
	}
}

func runFinalToolsPythonMutation(t *testing.T, reinstallDefault bool) {
	t.Helper()
	root, _, cache, env, configPath := finalToolsFixture(t, &config.RootConfig{Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}})
	env = finalToolsFakePythonManagers(t, root, env)
	bin := buildOmniBinary(t)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "set", "black", "--provider", "pip")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "groups", "move-tool", "testhost", "black")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "install", "black", "--provider", "pip")
	if reinstallDefault {
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "set", "black", "--provider", "uv")
		runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "reinstall", "black", "--reinstall-default")
	} else {
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "reinstall", "black", "--from", "pip", "--to", "uv")
	}
	if _, err := os.Stat(filepath.Join(cache, "fake-uv", "black")); err != nil {
		t.Fatalf("python mutation did not install uv state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "fake-pip", "black")); !os.IsNotExist(err) {
		t.Fatalf("python mutation retained pip state: %v", err)
	}
}

func finalToolsFakeBrew(t *testing.T, root string, env []string, state string) []string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(binDir, "brew"), `#!/bin/sh
set -eu
state="$OMNI_TEST_BREW_STATE"
mkdir -p "$state"
case "${1:-}" in
  --version) echo 'Homebrew 6.0.0' ;;
  install)
    name=""
    for arg in "$@"; do case "$arg" in -*) ;; install) ;; *) name="${arg##*/}" ;; esac; done
    [ -n "$name" ] && : > "$state/$name"
    ;;
  leaves) for pkg in "$state"/*; do [ -f "$pkg" ] && basename "$pkg"; done ;;
  list)
    shift
    if [ "${1:-}" = "--cask" ]; then exit 0; fi
    [ "${1:-}" = "--versions" ] || exit 1
    shift
    found=1
    for pkg in "$@"; do name="${pkg##*/}"; if [ -f "$state/$name" ]; then echo "$name 1.0.0"; found=0; fi; done
    exit "$found"
    ;;
  info) printf '{"formulae":[],"casks":[]}\n' ;;
  outdated) printf '{"formulae":[],"casks":[]}\n' ;;
  update) ;;
  *) exit 64 ;;
esac
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	return append(env, "OMNI_TEST_BREW_STATE="+state)
}

func finalToolsFakePythonManagers(t *testing.T, root string, env []string) []string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(binDir, "pip3"), `#!/bin/sh
set -eu
state="$OMNI_CACHE_DIR/fake-pip"
mkdir -p "$state"
case "${1:-}" in
  --version) echo 'pip 25.0' ;;
  install) [ "${2:-}" = "--upgrade" ] && shift; : > "$state/$2" ;;
  uninstall) rm -f "$state/${3:-}" ;;
  show) [ -f "$state/${2:-}" ] && printf 'Name: %s\nVersion: 1.0.0\n' "$2" ;;
  list) echo '[]' ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "uv"), `#!/bin/sh
set -eu
state="$OMNI_CACHE_DIR/fake-uv"
mkdir -p "$state"
case "${1:-}" in
  --version) echo 'uv 0.9.0' ;;
  tool)
    case "${2:-}" in
      install) : > "$state/$3" ;;
      uninstall) rm -f "$state/$3" ;;
      list) [ -f "$state/black" ] && echo 'black v1.0.0' ;;
    esac
    ;;
  pip) [ "${2:-}" = "show" ] && [ -f "$state/${3:-}" ] && printf 'Name: %s\nVersion: 1.0.0\n' "$3" ;;
esac
`)
	return replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
}

func runFinalToolsFailure(t *testing.T, bin, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}
