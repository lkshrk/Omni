//go:build integration

package integration_test

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIToolsFallbackProduceEquivalentResolvedRecipe(t *testing.T) {
	server := batch23FallbackServer(t)
	cli := batch23FallbackFixture(t, server)
	tui := batch23FallbackFixture(t, server)
	runOmniCommand(t, cli.bin, cli.root, cli.env, "--config", cli.configPath, "--cache-dir", cli.cache, "tools", "fallback", "fixture")
	runTUI(t, tui.bin, tui.root, tui.env, []string{"--config", tui.configPath, "--cache-dir", tui.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return screenHas("fixture", "missing")(text) && !strings.Contains(text, "gh?")
		}, "TUI did not render unresolved fallback fixture")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas(">", "fixture", "f set fallback"), "TUI did not select fallback fixture")
		writeTUIKeys(t, term, "f")
		waitForRequiredScreen(t, term, 5*time.Second, screenHas("Set Fallback: fixture", "owner/repo", "enter save"), "TUI did not open prefilled fallback editor")
		writeTUIKeys(t, term, "\r")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			return batch23ResolvedFallback(tui.configPath) != nil
		}, "TUI did not persist resolved fallback recipe")
	})
	cliState := batch23ObserveFallback(t, cli.configPath)
	tuiState := batch23ObserveFallback(t, tui.configPath)
	if !reflect.DeepEqual(cliState, tuiState) {
		t.Fatalf("tools.fallback semantic state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

type batch23FallbackSandbox struct {
	bin, root, cache, configPath string
	env                          []string
}

type batch23FallbackObservation struct {
	Providers   []config.ToolInstallSpec
	Git         string
	Fallback    *config.FallbackSpec
	Memberships []string
}

func batch23FallbackFixture(t *testing.T, server *httptest.Server) batch23FallbackSandbox {
	t.Helper()
	bin, root := batch16OmniBinary(t), t.TempDir()
	home, cache := filepath.Join(root, "home"), filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	certPath := filepath.Join(root, "github-test-ca.pem")
	writeIntegrationFile(t, certPath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))
	env = append(env, "OMNI_GITHUB_API_BASE="+server.URL, "SSL_CERT_FILE="+certPath)
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion, Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "brew", "dnf", "node", "pacman", "pip", "python", "zypper"}},
		Tools: map[string]config.ToolSpec{"fixture": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}, Git: "https://github.com/owner/repo"}},
		Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	return batch23FallbackSandbox{bin: bin, root: root, cache: cache, configPath: configPath, env: env}
}

func batch23FallbackServer(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":1,"tag_name":"v1.0.0","published_at":"2026-08-30T00:00:00Z","assets":[{"id":2,"name":"fixture_1.0_linux_x86_64.tar.gz","browser_download_url":%q},{"id":3,"name":"fixture_1.0_linux_aarch64.tar.gz","browser_download_url":%q}]}`, server.URL+"/fixture-amd64", server.URL+"/fixture-arm64")
	}))
	t.Cleanup(server.Close)
	return server
}

func batch23ResolvedFallback(path string) *config.FallbackSpec {
	cfg, err := config.Load(path)
	if err != nil {
		return nil
	}
	fallback := cfg.Tools["fixture"].Fallback
	if fallback == nil || fallback.Recipe.AssetDownloadURL == "" || fallback.Recipe.TagName == "" {
		return nil
	}
	return fallback
}

func batch23ObserveFallback(t *testing.T, path string) batch23FallbackObservation {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var memberships []string
	for _, group := range cfg.Groups {
		for _, tool := range group.Tools {
			if tool.Name == "fixture" {
				memberships = append(memberships, group.BaseName())
			}
		}
	}
	spec := cfg.Tools["fixture"]
	return batch23FallbackObservation{Providers: spec.Providers, Git: spec.Git, Fallback: spec.Fallback, Memberships: memberships}
}
