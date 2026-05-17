//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/lkshrk/omni/internal/cli"
	"github.com/lkshrk/omni/internal/database"
)

// TestMain registers "omni" as a testscript command so we test the real
// binary behaviour in a subprocess without needing a separate build step.
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"omni":            func() int { cli.Execute(); return 0 },
		"omni-seed-cache": seedCacheMain,
	}))
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
