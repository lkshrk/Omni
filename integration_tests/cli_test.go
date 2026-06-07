//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// TestMain registers "omni" as a testscript command so we test the real
// binary behaviour in a subprocess without needing a separate build step.
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"omni":                                func() int { cli.Execute(); return 0 },
		"omni-mark-outdated-refresh-fresh":    markOutdatedRefreshFreshMain,
		"omni-seed-cache":                     seedCacheMain,
		"omni-seed-package-availability":      seedPackageAvailabilityMain,
		"omni-seed-update-metadata":           seedUpdateMetadataMain,
		"omni-assert-tool-provider-list":      assertToolProviderListMain,
		"omni-with-npm-registry":              withNPMRegistryMain,
		"omni-tools-fallback-configured-git":  toolsFallbackConfiguredGitMain,
		"omni-tools-fallback-unsupported-git": toolsFallbackUnsupportedGitMain,
	}))
}

func assertToolProviderListMain() int {
	args := os.Args[1:]
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: omni-assert-tool-provider-list <config> <tool> <provider> [provider...]")
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
		if spec.Providers[i].Provider != providerName {
			fmt.Fprintf(os.Stderr, "providers = %+v, want %v\n", spec.Providers, want)
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
		fmt.Fprint(w, `{"objects":[{"package":{"name":"prettier","version":"3.5.0","description":"Prettier formatter"}}]}`)
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

func toolsFallbackConfiguredGitMain() int {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestCLI runs .txtar scripts in testdata/scripts/, skipping dependency/OS-gated
// fixtures when the local environment does not satisfy them.
func TestCLI(t *testing.T) {
	scripts, err := stowAwareScriptFiles()
	if err != nil {
		t.Fatal(err)
	}

	testscript.Run(t, testscript.Params{
		Files:               scripts,
		RequireExplicitExec: true,
		Setup: func(env *testscript.Env) error {
			// Point the app's cache dir to the per-test work directory so the
			// SQLite database is writable even when HOME=/no-home (testscript default).
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
