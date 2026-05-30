package testguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const guardImportPath = "github.com/lkshrk/omni/internal/testguard"

func TestEveryTestPackageImportsGuard(t *testing.T) {
	root := repoRoot(t)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if shouldSkipDir(rel, d.Name()) {
			return filepath.SkipDir
		}
		if rel == filepath.Join("internal", "testguard") {
			return filepath.SkipDir
		}
		hasTests, hasGuard, err := testPackageGuardState(path)
		if err != nil {
			return err
		}
		if hasTests && !hasGuard {
			missing = append(missing, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning repo: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("test packages missing guard import %q: %s", guardImportPath, strings.Join(missing, ", "))
	}
}

func TestMakeTestTargetsUseSafeRunner(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	makefile := string(data)
	for _, want := range []string{
		"TEST_SAFE   := bash scripts/run-test-safe.sh",
		"$(TEST_SAFE) bash scripts/test-release.sh",
		"$(TEST_SAFE) go clean -testcache",
		"$(TEST_SAFE) go test -race -trimpath ./...",
	} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile test target is missing safe runner command %q", want)
		}
	}
}

func TestSafeRunnerDisablesGoTelemetry(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "run-test-safe.sh"))
	if err != nil {
		t.Fatalf("reading safe runner: %v", err)
	}
	if !strings.Contains(string(data), "go telemetry off") {
		t.Fatal("safe runner should disable Go telemetry inside isolated HOME before running tests")
	}
}

func shouldSkipDir(rel, name string) bool {
	if rel == "." {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "dist", "build", "bin":
		return true
	default:
		return false
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("go.mod not found")
		}
		dir = next
	}
}

func testPackageGuardState(dir string) (bool, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false, err
	}
	hasTests := false
	hasGuard := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		hasTests = true
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false, false, err
		}
		if strings.Contains(string(data), guardImportPath) {
			hasGuard = true
		}
	}
	return hasTests, hasGuard, nil
}
