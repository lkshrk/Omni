//go:build integration

package integration_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/lkshrk/omni/internal/cli"
)

// TestMain registers "omni" as a testscript command so we test the real
// binary behaviour in a subprocess without needing a separate build step.
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"omni": func() int { cli.Execute(); return 0 },
	}))
}

// TestCLI runs .txtar scripts in testdata/scripts/, skipping dependency-gated
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
			// Use a fixed hostname so txtar scripts can map profiles deterministically.
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
	var stowMissing []string
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".txtar" {
			continue
		}
		requiresStow, err := scriptRequiresStow(filepath.Join("testdata/scripts", name))
		if err != nil {
			return nil, err
		}
		if stowErr != nil && requiresStow {
			stowMissing = append(stowMissing, name)
			continue
		}
		files = append(files, filepath.Join("testdata/scripts", name))
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

func scriptRequiresStow(path string) (bool, error) {
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
			if strings.Contains(line, "@requires:") && strings.Contains(line, "stow") {
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
