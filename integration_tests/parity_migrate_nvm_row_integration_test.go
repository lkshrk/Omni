//go:build integration

package integration_test

import (
	"context"
	"database/sql"
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

func TestTUIToolMigrateNvmRowMovesPackageToPnpm(t *testing.T) {
	s := newParitySandbox(t, t.TempDir())
	seedBatch24Nvm(t, s)
	runBatch24NvmTUI(t, buildOmniBinary(t), s)
	if !batch24NvmConverged(s) {
		t.Fatal("TUI nvm migration did not converge")
	}
}

func TestCLIAndTUIToolMigrateNvmRowProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	cli, tui := newParityTwins(t)
	seedBatch24Nvm(t, cli)
	seedBatch24Nvm(t, tui)
	runOmniCommand(t, bin, cli.root, cli.env, "--yes", "--config", cli.configPath, "--cache-dir", cli.cache, "tools", "migrate-nvm", "pnpm")
	runBatch24NvmTUI(t, bin, tui)
	runOmniCommand(t, bin, tui.root, tui.env, "--config", tui.configPath, "--cache-dir", tui.cache, "tools", "list", "pnpm", "--format", "json")
	cliState, tuiState := observeBatch24Nvm(t, cli), observeBatch24Nvm(t, tui)
	if !reflect.DeepEqual(cliState, tuiState) {
		t.Fatalf("migrate-nvm state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

func seedBatch24Nvm(t *testing.T, s *paritySandbox) {
	t.Helper()
	brewState, pnpmState := filepath.Join(s.root, "brew-state"), filepath.Join(s.root, "pnpm-state")
	for _, dir := range []string{brewState, pnpmState} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeIntegrationFile(t, filepath.Join(brewState, "pnpm"), "9.0.0\n")
	nvmBin, binDir := filepath.Join(s.home, ".nvm", "versions", "node", "v22.1.0", "bin"), filepath.Join(s.root, "bin")
	writeExecutable(t, filepath.Join(nvmBin, "node"), "#!/bin/sh\n[ \"${1:-}\" = \"--version\" ] && echo v22.1.0\n")
	writeBatch24Pnpm(t, filepath.Join(nvmBin, "pnpm"))
	writeBatch24Brew(t, filepath.Join(binDir, "brew"))
	writeExecutable(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nexit 1\n")
	s.env = replaceIntegrationEnv(s.env, "PATH", nvmBin+string(os.PathListSeparator)+binDir+string(os.PathListSeparator)+integrationEnvValue(s.env, "PATH"))
	s.env = append(s.env, "NVM_BIN="+nvmBin, "OMNI_TEST_BREW_STATE="+brewState, "OMNI_TEST_PNPM_STATE="+pnpmState, "OMNI_TEST_PROVIDER_LOG="+filepath.Join(s.root, "provider.log"))
	if err := config.Save(s.configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"brew", "pnpm"}, DisabledProviders: []string{"apt", "apk", "dnf", "pacman", "zypper", "bun", "npm", "python", "uv", "pip"}},
		Tools:    map[string]config.ToolSpec{"pnpm": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "pnpm"}, {Provider: "cargo", Package: "pnpm-alt"}}}},
		Hosts:    map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "pnpm"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	seedTUIToolCache(t, s.cache,
		&database.ToolCache{Name: "pnpm", Provider: "brew", Package: "pnpm", Installed: true, InstalledWith: "brew", Tracked: true, Version: sql.NullString{String: "9.0.0", Valid: true}},
		&database.ToolCache{Name: "pnpm", Provider: "pnpm", Package: "pnpm", Installed: true, InstalledWith: "pnpm", Tracked: false, Version: sql.NullString{String: "9.0.0", Valid: true}},
	)
}

func runBatch24NvmTUI(t *testing.T, bin string, s *paritySandbox) {
	t.Helper()
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 10*time.Second, screenHas("Out of Sync", "pnpm"), "TUI did not render pnpm")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("nvm-managed:", "r migrate nvm-managed"), "TUI did not select pnpm")
		writeTUIKeys(t, term, "r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("r move off brew to pnpm", "esc cancel"), "TUI did not arm migration")
		writeTUIKeys(t, term, "r")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool { return batch24NvmConverged(s) }, "TUI did not migrate pnpm")
	})
}

type batch24CacheRow struct {
	Name, Provider, Package, InstalledWith, Version string
	Installed, Tracked, Outdated                    bool
}
type batch24NvmState struct {
	Config                          any
	Cache                           []batch24CacheRow
	BrewFiles, PnpmFiles, Mutations []string
}

func observeBatch24Nvm(t *testing.T, s *paritySandbox) batch24NvmState {
	t.Helper()
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cache := make([]batch24CacheRow, 0, len(rows))
	for _, row := range rows {
		cache = append(cache, batch24CacheRow{Name: row.Name, Provider: row.Provider, Package: row.Package, InstalledWith: row.InstalledWith, Version: row.Version.String, Installed: row.Installed, Tracked: row.Tracked, Outdated: row.Outdated})
	}
	sort.Slice(cache, func(i, j int) bool {
		return cache[i].Provider+"\x00"+cache[i].Name+"\x00"+cache[i].Package < cache[j].Provider+"\x00"+cache[j].Name+"\x00"+cache[j].Package
	})
	return batch24NvmState{Config: normalizedParityConfig(t, s), Cache: cache, BrewFiles: batch24Files(t, filepath.Join(s.root, "brew-state")), PnpmFiles: batch24Files(t, filepath.Join(s.root, "pnpm-state")), Mutations: batch24Mutations(t, filepath.Join(s.root, "provider.log"))}
}

func batch24NvmConverged(s *paritySandbox) bool {
	_, brewErr := os.Stat(filepath.Join(s.root, "brew-state", "pnpm"))
	_, pnpmErr := os.Stat(filepath.Join(s.root, "pnpm-state", "pnpm"))
	if !os.IsNotExist(brewErr) || pnpmErr != nil {
		return false
	}
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	rows, err := db.List(context.Background())
	if err != nil {
		return false
	}
	count := 0
	for _, row := range rows {
		if row.Name == "pnpm" {
			count++
			if row.Provider != "pnpm" || !row.Installed || !row.Tracked || row.InstalledWith != "pnpm" {
				return false
			}
		}
	}
	return count == 1
}

func batch24Files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
func batch24Mutations(t *testing.T, path string) []string {
	t.Helper()
	raw, _ := os.ReadFile(path)
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "brew uninstall ") || strings.HasPrefix(line, "pnpm add -g ") || strings.HasPrefix(line, "pnpm remove -g ") {
			out = append(out, line)
		}
	}
	return out
}

func writeBatch24Pnpm(t *testing.T, path string) {
	t.Helper()
	writeExecutable(t, path, `#!/bin/sh
set -eu
printf 'pnpm %s\n' "$*" >> "${OMNI_TEST_PROVIDER_LOG:?}"
state="${OMNI_TEST_PNPM_STATE:?}"
case "${1:-}" in --version) echo 9.0.0;; add) : > "$state/${3:?}";; remove) rm -f "$state/${3:?}";; ls|list) for f in "$state"/*; do [ -f "$f" ] && printf '└── %s@9.0.0\n' "$(basename "$f")"; done;; outdated) echo '{}';; *) exit 64;; esac
`)
}
func writeBatch24Brew(t *testing.T, path string) {
	t.Helper()
	writeExecutable(t, path, `#!/bin/sh
set -eu
printf 'brew %s\n' "$*" >> "${OMNI_TEST_PROVIDER_LOG:?}"
state="${OMNI_TEST_BREW_STATE:?}"
case "${1:-}" in --version) echo 'Homebrew 6.0.0';; leaves) for f in "$state"/*; do [ -f "$f" ] && basename "$f"; done;; list) shift; case "${1:-}" in --cask) exit 0;; --versions) shift;; esac; case "${1:-}" in --formula) shift;; esac; found=1; for pkg in "$@"; do name="${pkg##*/}"; if [ -f "$state/$name" ]; then printf '%s 9.0.0\n' "$name"; found=0; fi; done; exit "$found";; info) echo '{"formulae":[],"casks":[]}' ;; outdated) echo '{"formulae":[],"casks":[]}' ;; update|tap) ;; uninstall) shift; case "${1:-}" in --formula|--cask) shift;; esac; rm -f "$state/${1:?}";; *) exit 64;; esac
`)
}
