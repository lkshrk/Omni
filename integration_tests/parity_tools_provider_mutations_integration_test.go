//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolsReinstallDefaultProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	cli, tui := newParityTwins(t)
	seedProviderMutationParity(t, bin, cli)
	seedProviderMutationParity(t, bin, tui)
	runOmniCommand(t, bin, cli.root, cli.env, "--yes", "--config", cli.configPath, "--cache-dir", cli.cache, "tools", "reinstall", "black", "--reinstall-default")
	runProviderMutationTUI(t, bin, tui, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "i")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool { return strings.Contains(strings.ToLower(text), "confirm reinstall") }, "TUI did not arm reinstall-default")
		writeTUIKeys(t, term, "i")
	}, func(s *paritySandbox) bool { return providerMutationSettled(s, "uv") })
	settleProviderMutationDiscovery(t, bin, cli)
	settleProviderMutationDiscovery(t, bin, tui)
	assertProviderMutationParity(t, cli, tui)
}

func TestCLIAndTUIToolsPinProviderProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	cli, tui := newParityTwins(t)
	seedProviderMutationParity(t, bin, cli)
	seedProviderMutationParity(t, bin, tui)
	runOmniCommand(t, bin, cli.root, cli.env, "--config", cli.configPath, "--cache-dir", cli.cache, "tools", "set", "black", "--provider", "pip", "--package", "black", "--install-with", "pip", "--global")
	runProviderMutationTUI(t, bin, tui, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "p")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("this tool on this host", "this tool everywhere", "pip"), "TUI did not open provider scope picker")
		writeTUIKeys(t, term, "j", " ")
	}, func(s *paritySandbox) bool {
		cfg, err := config.Load(s.configPath)
		if err != nil {
			return false
		}
		spec, ok := cfg.Tools["black"]
		return ok && len(spec.Providers) > 0 && spec.Providers[0].Provider == "pip" && spec.Provider == "" && spec.Package == "" && spec.InstallWith == ""
	})
	settleProviderMutationDiscovery(t, bin, cli)
	settleProviderMutationDiscovery(t, bin, tui)
	assertProviderMutationParity(t, cli, tui)
}

func seedProviderMutationParity(t *testing.T, bin string, s *paritySandbox) {
	s.env = providerMutationFakePython(t, s.root, s.env)
	if err := config.Save(s.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Hosts:   map[string][]string{"testhost": {"dev"}},
		Groups:  []*config.GroupConfig{{Name: "testhost", Special: "host"}, {Name: "dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "set", "black", "--provider", "pip")
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "groups", "move-tool", "dev", "black")
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "install", "black", "--provider", "pip")
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "set", "black", "--provider", "uv")
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "refresh")
	seedTUIToolCache(t, s.cache, &database.ToolCache{
		Name: "black", Provider: "uv", Package: "black", Installed: true, InstalledWith: "pip", Tracked: true,
		Version: sql.NullString{String: "1.0.0", Valid: true},
	})
	cfg, err := config.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := cfg.Tools["black"]
	spec.Options = map[string]string{"legacy": "keep", "shared": "legacy"}
	for i := range spec.Providers {
		switch spec.Providers[i].Provider {
		case "pip":
			spec.Providers[i].Options = map[string]string{"selected": "keep", "shared": "candidate"}
		case "uv":
			spec.Providers[i].Options = map[string]string{"uv": "keep"}
		}
	}
	cfg.Tools["black"] = spec
	if err := config.Save(s.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	preflightProviderMutationRow(t, bin, s)
}

func providerMutationFakePython(t *testing.T, root string, env []string) []string {
	binDir := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(binDir, "pip3"), `#!/bin/sh
set -eu
state="${OMNI_CACHE_DIR:?}/fake-pip"
mkdir -p "$state"
case "${1:-}" in
  --version) echo 'pip 25.0' ;;
  install) [ "${2:-}" = "--upgrade" ] && shift; : > "$state/$2" ;;
  uninstall) rm -f "$state/${3:-}" ;;
  show) [ -f "$state/${2:-}" ] && printf 'Name: %s\nVersion: 1.0.0\n' "$2" ;;
  list)
    if [ -f "$state/black" ]; then printf '[{"name":"black","version":"1.0.0"}]\n'; else echo '[]'; fi
    ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "uv"), `#!/bin/sh
set -eu
state="${OMNI_CACHE_DIR:?}/fake-uv"
mkdir -p "$state"
case "${1:-}" in
  --version) echo 'uv 0.9.0' ;;
  tool)
    case "${2:-}" in
      install) : > "$state/$3" ;;
      uninstall) rm -f "$state/$3" ;;
      list) if [ -f "$state/black" ]; then echo 'black v1.0.0'; fi ;;
    esac
    ;;
  pip) [ "${2:-}" = "show" ] && [ -f "$state/${3:-}" ] && printf 'Name: %s\nVersion: 1.0.0\n' "$3" ;;
esac
`)
	return replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
}

func preflightProviderMutationRow(t *testing.T, bin string, s *paritySandbox) {
	out := runOmniOutput(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "list", "black", "--format", "json")
	var rows []struct {
		Name          string `json:"name"`
		Provider      string `json:"provider"`
		InstalledWith string `json:"installed_with"`
		Installed     bool   `json:"installed"`
		Tracked       bool   `json:"tracked"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode provider preflight: %v\n%s", err, out)
	}
	canonical, discovered := 0, 0
	for _, row := range rows {
		switch {
		case row.Name == "black" && row.Provider == "uv" && row.InstalledWith == "pip" && row.Installed && row.Tracked:
			canonical++
		case row.Name == "black" && row.Provider == "pip" && row.InstalledWith == "pip" && row.Installed && !row.Tracked:
			discovered++
		default:
			t.Fatalf("unexpected provider preflight row: %#v (all rows: %#v)", row, rows)
		}
	}
	if canonical != 1 || discovered > 1 {
		t.Fatalf("provider preflight rows = %#v, want one canonical uv/pip row and at most one honest pip discovery", rows)
	}
}

func runProviderMutationTUI(t *testing.T, bin string, s *paritySandbox, act func(*vttest.Terminal), done func(*paritySandbox) bool) {
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 10*time.Second, screenHas("black", "uv", "pip"), "TUI did not preserve uv/pip ownership")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool { return strings.Contains(text, ">") && strings.Contains(text, "black") }, "TUI did not reveal black rows")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool {
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, ">") && strings.Contains(line, "black") && strings.Contains(line, "[dev]") {
					return true
				}
			}
			return false
		}, "TUI did not select canonical tracked black row")
		act(term)
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool { return done(s) }, "TUI provider mutation did not settle")
	})
}

type providerMutationCacheRow struct {
	Provider, Package, InstalledWith, Version, Latest string
	Installed, Tracked, Outdated                      bool
}

type providerMutationState struct {
	Config         any
	PipMarker      bool
	UVMarker       bool
	Rows           []providerMutationCacheRow
	ProviderTraces []string
}

func observeProviderMutationParity(t *testing.T, s *paritySandbox) providerMutationState {
	state := providerMutationState{Config: normalizedParityConfig(t, s)}
	_, pipErr := os.Stat(filepath.Join(s.cache, "fake-pip", "black"))
	_, uvErr := os.Stat(filepath.Join(s.cache, "fake-uv", "black"))
	state.PipMarker, state.UVMarker = pipErr == nil, uvErr == nil
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tools, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name == "black" {
			state.Rows = append(state.Rows, providerMutationCacheRow{Provider: tool.Provider, Package: tool.Package, InstalledWith: tool.InstalledWith, Version: tool.Version.String, Latest: tool.LatestVersion.String, Installed: tool.Installed, Tracked: tool.Tracked, Outdated: tool.Outdated})
		}
	}
	traces, err := db.ListCommandTraces(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, trace := range traces {
		if strings.Contains(trace.Command, "pip3 uninstall") || strings.Contains(trace.Command, "uv tool install") {
			state.ProviderTraces = append(state.ProviderTraces, strings.ReplaceAll(trace.Command, s.root, "$ROOT"))
		}
	}
	sort.Strings(state.ProviderTraces)
	return state
}

func settleProviderMutationDiscovery(t *testing.T, bin string, s *paritySandbox) {
	t.Helper()
	var previous providerMutationState
	for attempt := 0; attempt < 8; attempt++ {
		runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "refresh")
		runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "list", "black", "--format", "json")
		current := observeProviderMutationParity(t, s)
		if attempt > 0 && reflect.DeepEqual(current, previous) {
			return
		}
		previous = current
	}
	t.Fatalf("provider discovery did not reach a fixed point after 8 authoritative reads; last state: %#v", previous)
}

func assertProviderMutationParity(t *testing.T, cli, tui *paritySandbox) {
	t.Helper()
	want, got := observeProviderMutationParity(t, cli), observeProviderMutationParity(t, tui)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider mutation semantic state differs\nCLI: %#v\nTUI: %#v", want, got)
	}
}

func providerMutationSettled(s *paritySandbox, provider string) bool {
	if _, err := os.Stat(filepath.Join(s.cache, "fake-"+provider, "black")); err != nil {
		return false
	}
	if provider == "uv" {
		if _, err := os.Stat(filepath.Join(s.cache, "fake-pip", "black")); !os.IsNotExist(err) {
			return false
		}
	}
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "black", provider, "black")
	return err == nil && tool.Installed && tool.InstalledWith == provider && !tool.Outdated
}
