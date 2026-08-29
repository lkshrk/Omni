package testguard

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const guardImportPath = "github.com/lkshrk/omni/internal/testguard"

type guardBuildContext struct {
	name string
	ctx  build.Context
}

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
		for _, context := range guardBuildContexts() {
			hasTests, hasGuard, err := testPackageGuardState(path, context.ctx)
			if err != nil {
				return err
			}
			if hasTests && !hasGuard {
				missing = append(missing, rel+" ["+context.name+"]")
			}
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
		`evidence_root="$$safe_root/test-evidence"`,
		"refusing unsafe repo .tmp",
		"repo .tmp escaped checkout",
		"refusing unsafe test-evidence directory",
		"test-evidence escaped repo .tmp",
		"refusing unsafe lane evidence directory",
		"lane evidence escaped test-evidence root",
	} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("integration evidence path safety is missing %q", want)
		}
	}
	for _, want := range []string{
		"TEST_SAFE   := bash scripts/run-test-safe.sh",
		"TEST_PACKAGES ?= ./...",
		"TEST_FLAGS  ?= -race -trimpath",
		"test: test-scripts test-unit",
		"$(TEST_SAFE) bash scripts/test-release.sh",
		"$(TEST_SAFE) go test $(TEST_FLAGS) $(TEST_PACKAGES)",
		"$(TEST_SAFE) go test -trimpath ./...",
	} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile test target is missing safe runner command %q", want)
		}
	}
	if strings.Contains(makefile, "go clean -testcache") {
		t.Fatal("Makefile test targets should preserve Go's test cache")
	}
	for _, forbidden := range []string{"TEST_UNIT_ROOT"} {
		if strings.Contains(makefile, forbidden) {
			t.Fatalf("Makefile safe test targets contain forbidden shared cleanup %q", forbidden)
		}
	}
	for _, target := range []string{
		"test", "test-unit", "test-unit-fast", "test-fast", "test-scripts", "test-canary",
		"test-package-managers", "test-all", "test-integration-build", "test-integration", "lint",
	} {
		block := makeTargetBlock(makefile, target)
		for _, forbidden := range []string{"prune-tmp", "clean-docker", "clean-cache"} {
			if strings.Contains(block, forbidden) {
				t.Fatalf("Makefile %s target contains forbidden global cleanup %q", target, forbidden)
			}
		}
	}
}

func TestDockerIntegrationRunsFreshContainerAndExportsEvidence(t *testing.T) {
	root := repoRoot(t)
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile.test"))
	if err != nil {
		t.Fatalf("reading Dockerfile.test: %v", err)
	}
	if strings.Contains(string(dockerfile), "go test -count=1") {
		t.Fatal("Dockerfile.test must build the environment without running tests during image build")
	}
	for _, want := range []string{"USER omni", "RUN go mod vendor", "ENV GOFLAGS=-mod=vendor"} {
		if !strings.Contains(string(dockerfile), want) {
			t.Fatalf("Dockerfile.test is missing runtime environment contract %q", want)
		}
	}

	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	makefile := string(data)
	build := makeTargetBlock(makefile, "test-integration-build")
	run := makeTargetBlock(makefile, "test-integration")
	if strings.Contains(makefile, "INTEGRATION_EVIDENCE_DIR") {
		t.Fatal("integration evidence root must not be caller-overridable")
	}
	validateAt := strings.Index(run, `if [ -e "$$tmp_root" ]`)
	createAt := strings.Index(run, `mkdir -p "$$tmp_root"`)
	if validateAt < 0 || createAt < 0 || validateAt > createAt {
		t.Fatal("integration evidence root is created before its non-symlink validation")
	}
	for _, pair := range [][2]string{
		{`if [ -e "$$evidence_root" ]`, `mkdir -p "$$evidence_root"`},
		{`if [ -e "$$evidence" ]`, `mkdir -p "$$evidence"`},
	} {
		if guard, create := strings.Index(run, pair[0]), strings.Index(run, pair[1]); guard < 0 || create < 0 || guard > create {
			t.Fatalf("integration evidence path %q is created before validation", pair[1])
		}
	}
	for _, want := range []string{"--load", "--tag \"$(INTEGRATION_IMAGE)\""} {
		if !strings.Contains(build, want) {
			t.Fatalf("integration image build is missing %q", want)
		}
	}
	for _, want := range []string{
		"$(DOCKER) create", "$(DOCKER) start -a", "$(DOCKER) cp", "$(DOCKER) rm -f",
		"scripts/run-test-safe.sh go test -count=1 -tags=integration -race -trimpath -json",
		"go-test.jsonl", "meta.json", "gate.json", "container_gate", "$(DOCKER) image inspect",
		"OMNI_TEST_APPROVED_TOOLS=apm,claude,codex,grok,cowsay",
		"--network none",
	} {
		if !strings.Contains(run, want) {
			t.Fatalf("integration container run is missing %q", want)
		}
	}
	for _, forbidden := range []string{"--mount", "--volume", "/var/run/docker.sock", "builder prune", "cacheonly"} {
		if strings.Contains(build+run, forbidden) {
			t.Fatalf("integration targets contain forbidden host coupling or cache-only execution %q", forbidden)
		}
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("reading CI workflow: %v", err)
	}
	for _, want := range []string{
		"INTEGRATION_LANE: ${{ matrix.name }}",
		"name: flow-evidence-docker-${{ matrix.name }}",
		"path: .tmp/test-evidence/docker-${{ matrix.name }}",
		"if-no-files-found: error",
	} {
		if !strings.Contains(string(workflow), want) {
			t.Fatalf("CI Docker evidence contract is missing %q", want)
		}
	}
}

func TestCIUnitLanesUploadFlowEvidence(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("reading CI workflow: %v", err)
	}
	workflow := string(data)
	for _, want := range []string{
		`TEST_FLAGS="-race -trimpath -count=1 -json"`,
		`| tee "$evidence_dir/go-test.jsonl"`,
		`{"schema_version":1,"lane":"unit-${{ matrix.name }}","goos":"linux","tags":[],"count":1}`,
		`if: always()`,
		`name: flow-evidence-unit-${{ matrix.name }}`,
		`if-no-files-found: error`,
		`flow-evidence:`,
		`needs: [unit-tests, integration]`,
		`pattern: flow-evidence-*`,
		`go run ./scripts/flow-evidence -catalog test/flows.json -evidence "$evidence"`,
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("CI unit evidence is missing %q", want)
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

func TestSafeRunnerAcceptsCanonicalUnixTemporaryRoots(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "run-test-safe.sh"))
	if err != nil {
		t.Fatalf("reading safe runner: %v", err)
	}
	script := string(data)
	for _, root := range []string{"/tmp/omni-test.*", "/private/tmp/omni-test.*"} {
		if !strings.Contains(script, root) {
			t.Fatalf("safe runner cleanup does not accept canonical root %q", root)
		}
	}
}

func TestSafeRunnerMarksEnvironmentIsolated(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `
		set -eu
		test "$OMNI_TEST_ISOLATED" = 1
		test -n "$OMNI_TEST_NONCE"
		test "$(stat -c %a "$OMNI_TEST_ROOT" 2>/dev/null || stat -f %Lp "$OMNI_TEST_ROOT")" = 700
		test -f "$OMNI_TEST_ROOT/.omni-test-sandbox"
		test ! -L "$OMNI_TEST_ROOT/.omni-test-sandbox"
		test "$(cat "$OMNI_TEST_ROOT/.omni-test-sandbox")" = "$OMNI_TEST_NONCE"
		for path in "$HOME" "$USERPROFILE" "$APPDATA" "$LOCALAPPDATA" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME" "$OMNI_CACHE_DIR" "$OMNI_STATE_DIR" "$NPM_CONFIG_PREFIX" "$TMPDIR"; do
			case "$path" in "$OMNI_TEST_ROOT"/*) ;; *) exit 1 ;; esac
		done
	`)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("safe runner should mark its environment isolated: %v\n%s", err, out)
	}
}

func TestSafeRunnerCreatesUniqueRootsAndCleansThem(t *testing.T) {
	root := repoRoot(t)
	runner := filepath.Join(root, "scripts", "run-test-safe.sh")
	var roots []string
	for range 2 {
		cmd := exec.Command("bash", runner, "bash", "-c", `printf '%s' "$OMNI_TEST_ROOT"; mkdir -p "$HOME/go/pkg/mod/example"; touch "$HOME/go/pkg/mod/example/go.mod"; chmod -R a-w "$HOME/go/pkg/mod"`)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("safe runner execution failed: %v\n%s", err, out)
		}
		sandbox := string(out)
		if !isSafeRunnerRoot(sandbox) {
			t.Fatalf("sandbox root = %q, want a canonical temporary root", sandbox)
		}
		if _, err := os.Stat(sandbox); !os.IsNotExist(err) {
			t.Fatalf("sandbox should be deleted after execution, stat error = %v", err)
		}
		roots = append(roots, sandbox)
	}
	if roots[0] == roots[1] {
		t.Fatalf("safe runner reused sandbox root %q", roots[0])
	}
}

func TestSafeRunnerSanitizesCredentialsAndGit(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `
		set -eu
		for key in GH_TOKEN GITHUB_TOKEN SSH_AUTH_SOCK AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SHARED_CREDENTIALS_FILE CODEX_HOME CLAUDE_CONFIG_DIR; do
			test -z "$(printenv "$key" || true)"
		done
		for key in HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy; do
			test "$(printenv "$key")" = "http://127.0.0.1:1"
		done
		test "$GIT_CONFIG_NOSYSTEM" = 1
		test "$GIT_TERMINAL_PROMPT" = 0
		case "$GIT_CONFIG_GLOBAL" in "$OMNI_TEST_ROOT"/*) ;; *) exit 1 ;; esac
		case "$KUBECONFIG" in "$OMNI_TEST_ROOT"/*) ;; *) exit 1 ;; esac
		case "$DOCKER_CONFIG" in "$OMNI_TEST_ROOT"/*) ;; *) exit 1 ;; esac
		test "$NO_PROXY" = "localhost,127.0.0.1,::1"
	`)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"OMNI_TEST_ROOT=/tmp/caller-owned",
		"GH_TOKEN=secret", "GITHUB_TOKEN=secret", "SSH_AUTH_SOCK=/tmp/agent.sock",
		"AWS_ACCESS_KEY_ID=secret", "AWS_SECRET_ACCESS_KEY=secret", "AWS_SHARED_CREDENTIALS_FILE=/real/aws", "KUBECONFIG=/real/kubeconfig",
		"CODEX_HOME=/real/codex", "CLAUDE_CONFIG_DIR=/real/claude",
		"HTTP_PROXY=http://proxy.invalid", "HTTPS_PROXY=http://proxy.invalid", "ALL_PROXY=socks5://proxy.invalid",
		"http_proxy=http://proxy.invalid", "https_proxy=http://proxy.invalid", "all_proxy=socks5://proxy.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("safe runner should sanitize inherited environment: %v\n%s", err, out)
	}
}

func TestSafeRunnerUsesOnlyApprovedTools(t *testing.T) {
	root := repoRoot(t)
	fakeBin := t.TempDir()
	for _, name := range []string{"brew", "apt", "dnf", "pacman", "zypper", "apm", "claude", "codex", "grok", "curl", "ssh"} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `
		set -eu
		test "$PATH" = "$OMNI_TEST_ROOT/bin"
		for tool in go git; do
			case "$(command -v "$tool")" in "$OMNI_TEST_ROOT/bin/"*) ;; *) exit 1 ;; esac
		done
		for tool in brew apt dnf pacman zypper apm claude codex grok curl ssh; do
			! command -v "$tool" >/dev/null 2>&1
		done
	`)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("safe runner tool allowlist failed: %v\n%s", err, out)
	}
}

func TestSafeRunnerPreservesFixedOptionalTools(t *testing.T) {
	root := repoRoot(t)
	fakeBin := t.TempDir()
	for _, name := range []string{"apm", "brew"} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `
		set -eu
		test "$OMNI_TEST_APPROVED_TOOLS" = apm
		case "$(command -v apm)" in "$OMNI_TEST_ROOT/bin/"*) ;; *) exit 1 ;; esac
		! command -v brew >/dev/null 2>&1
	`)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"OMNI_TEST_APPROVED_TOOLS=apm",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("safe runner optional tool opt-in failed: %v\n%s", err, out)
	}
}

func TestSafeRunnerRejectsUnknownOptionalTools(t *testing.T) {
	root := repoRoot(t)
	for _, value := range []string{"brew", "apm,", "apm,apm"} {
		t.Run(value, func(t *testing.T) {
			cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "true")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "OMNI_TEST_APPROVED_TOOLS="+value)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("safe runner accepted %q: %s", value, out)
			}
		})
	}
}

func TestSafeRunnerBuildsDependenciesThenIsolatesTestChild(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command(
		"bash", filepath.Join(root, "scripts", "run-test-safe.sh"),
		"go", "test", "-count=1", "-run", "^TestDirectGoTestCreatesCompleteSandbox$",
		"./internal/config", "./internal/testguard",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"OMNI_TEST_BUILD_GOCACHE=/tmp/omni-go-build",
		"OMNI_TEST_BUILD_GOMODCACHE=/tmp/omni-go-mod",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("safe runner should build a dependency-bearing package before isolating test binaries: %v\n%s", err, out)
	}
}

func TestSafeRunnerRefusesCleanupAfterMarkerTampering(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `printf '%s' "$OMNI_TEST_ROOT"; printf 'wrong\nmarker\n' >"$OMNI_TEST_ROOT/.omni-test-sandbox"`)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("safe runner should fail closed when its cleanup marker is modified")
	}
	sandbox := strings.SplitN(string(out), "refusing unsafe test cleanup", 2)[0]
	sandbox = strings.TrimSpace(sandbox)
	if !isSafeRunnerRoot(sandbox) {
		t.Fatalf("could not recover refused sandbox path from %q", out)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sandbox) })
	if _, statErr := os.Stat(sandbox); statErr != nil {
		t.Fatalf("runner deleted sandbox despite invalid marker: %v", statErr)
	}
}

func TestSafeRunnerCleansReadOnlyGoModuleCache(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `mkdir -p "$HOME/go/pkg/mod/example"; touch "$HOME/go/pkg/mod/example/go.mod"; chmod -R a-w "$HOME/go/pkg/mod"`)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("safe runner should clean read-only Go module cache: %v\n%s", err, out)
	}
}

func TestSafeRunnerCleanupDoesNotFollowSymlinks(t *testing.T) {
	root := repoRoot(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o400); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "run-test-safe.sh"), "bash", "-c", `ln -s "$1" "$OMNI_TEST_ROOT/outside-link"`, "_", outside)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("safe runner should remove sandbox symlinks: %v\n%s", err, out)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatalf("outside symlink target was removed: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o400 {
		t.Fatalf("outside symlink target mode = %o, want 400", got)
	}
}

func TestMakePruneTmpBoundsRepoCaches(t *testing.T) {
	root := repoRoot(t)
	work := t.TempDir()
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "Makefile"), makefile, 0o644); err != nil {
		t.Fatalf("writing Makefile fixture: %v", err)
	}
	cacheDir := filepath.Join(work, ".tmp", "go-build")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache-entry"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing cache entry: %v", err)
	}

	cmd := exec.Command("make", "prune-tmp", "TMP_MAX_MB=0")
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-tmp should remove oversized repo caches: %v\n%s", err, out)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache dir should be pruned, stat error = %v", err)
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

func makeTargetBlock(makefile, target string) string {
	lines := strings.Split(makefile, "\n")
	var block []string
	started := false
	for _, line := range lines {
		if !started {
			if strings.HasPrefix(line, target+":") {
				started = true
				block = append(block, line)
			}
			continue
		}
		if line != "" && !strings.HasPrefix(line, "\t") {
			break
		}
		block = append(block, line)
	}
	return strings.Join(block, "\n")
}

func isSafeRunnerRoot(path string) bool {
	return strings.HasPrefix(path, "/tmp/omni-test.") || strings.HasPrefix(path, "/private/tmp/omni-test.")
}

func guardBuildContexts() []guardBuildContext {
	contexts := []guardBuildContext{{name: "default", ctx: build.Default}}
	for _, tag := range []string{"integration", "canary", "pmcontainer"} {
		ctx := build.Default
		ctx.BuildTags = append(append([]string(nil), ctx.BuildTags...), tag)
		contexts = append(contexts, guardBuildContext{name: tag, ctx: ctx})
	}
	return contexts
}

func testPackageGuardState(dir string, ctx build.Context) (bool, bool, error) {
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
		matches, err := ctx.MatchFile(dir, entry.Name())
		if err != nil {
			return false, false, err
		}
		if !matches {
			continue
		}
		hasTests = true
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			return false, false, err
		}
		for _, spec := range file.Imports {
			if spec.Path.Value == `"`+guardImportPath+`"` {
				hasGuard = true
			}
		}
	}
	return hasTests, hasGuard, nil
}

func TestGuardDetectionUsesImportsNotComments(t *testing.T) {
	dir := t.TempDir()
	source := "package fixture\n// " + guardImportPath + "\nfunc TestExample(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "example_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	hasTests, hasGuard, err := testPackageGuardState(dir, build.Default)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTests || hasGuard {
		t.Fatalf("guard state = (%v, %v), want (true, false)", hasTests, hasGuard)
	}
}

func TestGuardDetectionIgnoresBuildExcludedImport(t *testing.T) {
	dir := t.TempDir()
	plain := "package fixture\nfunc TestExample(t *testing.T) {}\n"
	excluded := "//go:build omni_guard_never\n\npackage fixture\nimport _ \"" + guardImportPath + "\"\n"
	for name, source := range map[string]string{
		"example_test.go":        plain,
		"excluded_guard_test.go": excluded,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hasTests, hasGuard, err := testPackageGuardState(dir, build.Default)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTests || hasGuard {
		t.Fatalf("guard state = (%v, %v), want active tests without a guard", hasTests, hasGuard)
	}
}

func TestGuardDetectionCoversIntegrationBuildContext(t *testing.T) {
	dir := t.TempDir()
	integration := build.Default
	integration.BuildTags = append(append([]string(nil), integration.BuildTags...), "integration")
	withoutGuard := "//go:build integration\n\npackage fixture\nfunc TestIntegration(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "integration_test.go"), []byte(withoutGuard), 0o600); err != nil {
		t.Fatal(err)
	}
	hasTests, hasGuard, err := testPackageGuardState(dir, integration)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTests || hasGuard {
		t.Fatalf("integration guard state = (%v, %v), want active tests without a guard", hasTests, hasGuard)
	}

	withGuard := "//go:build integration\n\npackage fixture\nimport _ \"" + guardImportPath + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "integration_guard_test.go"), []byte(withGuard), 0o600); err != nil {
		t.Fatal(err)
	}
	hasTests, hasGuard, err = testPackageGuardState(dir, integration)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTests || !hasGuard {
		t.Fatalf("integration guard state = (%v, %v), want active tests with a guard", hasTests, hasGuard)
	}
}
