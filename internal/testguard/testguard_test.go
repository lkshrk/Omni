package testguard

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestDirectGoTestCreatesCompleteSandbox(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("direct test build info unavailable")
	}
	if !testBinaryBuildPath(info.Path) {
		t.Fatalf("direct test build path=%q", info.Path)
	}
	if !Isolated() {
		t.Fatal("direct go test did not activate isolation")
	}
	root := os.Getenv(rootEnv)
	if root == "" || os.Getenv(nonceEnv) == "" {
		t.Fatal("direct go test did not assign root and nonce")
	}
	for _, key := range []string{
		"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME",
		"XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "OMNI_CONFIG",
		"OMNI_CACHE_DIR", "OMNI_STATE_DIR", "TMPDIR", "TMP", "TEMP", "GOCACHE", "GOMODCACHE", "PATH",
	} {
		if got := os.Getenv(key); got == "" || !PathInRoot(got, root) || got == root {
			t.Fatalf("%s=%q is not a sandbox descendant", key, got)
		}
	}
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK", "AWS_PROFILE", "AZURE_CONFIG_DIR"} {
		if value := os.Getenv(key); value != "" {
			t.Fatalf("direct go test retained credential environment %s", key)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if got := os.Getenv(key); got != "http://127.0.0.1:1" {
			t.Fatalf("%s=%q, want blocked loopback proxy", key, got)
		}
	}
}

func TestDirectGoTestIgnoresHomeTMPDIR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows OS temp may legitimately be below the user profile")
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	homeTmp := filepath.Join(current.HomeDir, ".tmp")
	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardInitHelper$") //nolint:gosec
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + homeTmp,
		"OMNI_TEST_HELPER_OUTSIDE_HOME=" + current.HomeDir,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("direct test used unsafe HOME TMPDIR: %v\n%s", err, out)
	}
	unsafeRoot := filepath.Join(homeTmp, "omni-test-explicit-unsafe")
	if err := validateRootCandidate(unsafeRoot); err == nil {
		t.Fatalf("explicit sandbox root below real HOME accepted: %q", unsafeRoot)
	}
}

func TestTestBinaryDetectionUsesBuildPathNotExecutableName(t *testing.T) {
	if !testBinaryBuildPath("github.com/lkshrk/omni/internal/testguard.test") {
		t.Fatal("standard Go test build path was not detected")
	}
	if testBinaryBuildPath("github.com/lkshrk/omni/cmd/omni") {
		t.Fatal("production build path was detected as a test")
	}
}

func TestSandboxPATHContainsOnlyApprovedTools(t *testing.T) {
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Cleanup() })
	values := envValues(sandbox.SanitizedEnv("PATH=/usr/bin", "SHELL=/bin/bash"))
	wantShell, ok := approvedToolPath(sandbox.Bin, "sh", values["PATHEXT"], runtime.GOOS == "windows")
	if !ok {
		t.Fatal("approved sh tool is missing")
	}
	if values["PATH"] != sandbox.Bin || values["SHELL"] != wantShell {
		t.Fatalf("sandbox tool roots overridden: PATH=%q SHELL=%q", values["PATH"], values["SHELL"])
	}
	t.Setenv("PATH", sandbox.Bin)
	for _, name := range []string{"go", "sh", "git", "echo", "printf"} {
		path, err := exec.LookPath(name)
		if err != nil || !EntryPathInRoot(path, sandbox.Bin) {
			t.Fatalf("approved available tool %q missing: %v", name, err)
		}
	}
	for _, name := range []string{
		"apm", "brew", "apt", "dnf", "pacman", "zypper", "claude", "codex", "grok", "curl", "wget",
		"ssh", "docker", "kubectl",
	} {
		if _, err := os.Lstat(filepath.Join(sandbox.Bin, name)); !os.IsNotExist(err) {
			t.Fatalf("dangerous tool %q exposed in sandbox bin: %v", name, err)
		}
	}
}

func TestNPMGlobalPrefixStaysInSandbox(t *testing.T) {
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Cleanup() })
	prefix := envValues(sandbox.SanitizedEnv("npm_config_prefix=/escape"))["NPM_CONFIG_PREFIX"]
	if prefix == "" || !PathInRoot(prefix, sandbox.Root) {
		t.Fatalf("NPM_CONFIG_PREFIX escaped sandbox: %q", prefix)
	}
	if info, err := os.Stat(prefix); err != nil || !info.IsDir() {
		t.Fatalf("NPM_CONFIG_PREFIX is not an existing directory: %v", err)
	}
	if !isProtectedEnvKey("npm_config_prefix", true) {
		t.Fatal("Windows case-insensitive npm prefix override was not protected")
	}
}

func TestApprovedToolPathIsPATHEXTAware(t *testing.T) {
	bin := t.TempDir()
	goExe := filepath.Join(bin, "go.EXE")
	if err := os.WriteFile(goExe, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, ok := approvedToolPath(bin, "go", ".COM;.EXE;.CMD", true); !ok || got != goExe {
		t.Fatalf("Windows approved tool path=(%q,%t), want %q", got, ok, goExe)
	}
	unix := filepath.Join(bin, "sh")
	if err := os.WriteFile(unix, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, ok := approvedToolPath(bin, "sh", "", false); !ok || got != unix {
		t.Fatalf("Unix approved tool path=(%q,%t), want %q", got, ok, unix)
	}
}

func TestCopyApprovedToolFallback(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("approved-tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyApprovedTool(source, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "approved-tool" {
		t.Fatalf("copied approved tool=%q, err=%v", data, err)
	}
}

func TestWindowsEnvKeyNormalizationBlocksCaseVariants(t *testing.T) {
	for _, key := range []string{"home", "OmNi_Config", "PaTh", "gOcAcHe"} {
		if !isProtectedEnvKey(key, true) {
			t.Fatalf("Windows protected key variant accepted: %q", key)
		}
	}
	if isProtectedEnvKey("home", false) {
		t.Fatal("Unix environment keys should remain case-sensitive")
	}
	env := map[string]string{"Home": "first"}
	putEnv(env, "HOME", "second", true)
	if len(env) != 1 || env["HOME"] != "second" {
		t.Fatalf("Windows environment dedup failed: %#v", env)
	}
}

func TestEffectiveEnvRejectsHostPATH(t *testing.T) {
	t.Setenv("PATH", string(filepath.Separator)+"usr"+string(filepath.Separator)+"bin")
	if _, err := sandboxFromEnv(); err == nil {
		t.Fatal("host PATH accepted by full child validation")
	}
}

func TestRenamedTestBinaryStillActivatesSandbox(t *testing.T) {
	renamed := filepath.Join(t.TempDir(), "omni-production-looking")
	if err := copyExecutable(os.Args[0], renamed); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(renamed, "-test.run=^TestGuardInitHelper$") //nolint:gosec
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renamed test binary did not activate sandbox: %v\n%s", err, out)
	}
}

func TestProductionBinaryNameDoesNotActivateWithoutTestFlags(t *testing.T) {
	fixture := t.TempDir()
	repo := checkoutRoot()
	goMod := fmt.Sprintf("module github.com/lkshrk/omni/testguardprod\n\ngo 1.26.2\n\nrequire github.com/lkshrk/omni v0.0.0\nreplace github.com/lkshrk/omni => %s\n", filepath.ToSlash(repo))
	mainGo := `package main
import (
	"fmt"
	"github.com/lkshrk/omni/internal/testguard"
)
func main() { fmt.Printf("ACTIVE=%t ISOLATED=%t", testguard.Active(), testguard.Isolated()) }
`
	if err := os.WriteFile(filepath.Join(fixture, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "main.go"), []byte(mainGo), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(fixture, "omni.test-looking-production")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = fixture
	build.Env = replaceEnv(replaceEnv(os.Environ(), "GOWORK", "off"), "CGO_ENABLED", "0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building production-like helper: %v\n%s", err, out)
	}
	cmd := exec.Command(binary) //nolint:gosec
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running production-like helper: %v\n%s", err, out)
	}
	if got := string(out); got != "ACTIVE=false ISOLATED=false" {
		t.Fatalf("production-like helper = %q", got)
	}
}

func TestRegisteredTestFlagsDoNotDependOnExecutableName(t *testing.T) {
	if !registeredTestFlags(flag.Lookup) {
		t.Fatal("registered Go test flags were not detected")
	}
	if registeredTestFlags(func(string) *flag.Flag { return nil }) {
		t.Fatal("process without registered Go test flags detected as a test")
	}
}

func TestPMContainerDoesNotBypassIsolation(t *testing.T) {
	t.Setenv(isolatedEnv, "")
	t.Setenv("OMNI_PMCONTAINER", "1")
	if Isolated() {
		t.Fatal("OMNI_PMCONTAINER was treated as test isolation")
	}
}

func TestRequireTempPathUsesOnlyAssignedRoot(t *testing.T) {
	if err := RequireTempPath("config", filepath.Join(os.Getenv(rootEnv), "config", "settings.json")); err != nil {
		t.Fatalf("assigned descendant rejected: %v", err)
	}
	for name, path := range map[string]string{
		"root itself": os.Getenv(rootEnv),
		"outside":     filepath.Join(filepath.Dir(os.Getenv(rootEnv)), "outside"),
		"root":        string(filepath.Separator),
	} {
		if err := RequireTempPath(name, path); err == nil {
			t.Fatalf("%s accepted: %q", name, path)
		}
	}
}

func TestPathContainmentRejectsEscapes(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "file")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "intermediate")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "final")); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "regular")
	if err := os.WriteFile(blocker, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"traversal":            filepath.Join(root, "..", "outside", "file"),
		"intermediate symlink": filepath.Join(root, "intermediate", "file"),
		"final symlink":        filepath.Join(root, "final"),
		"below regular file":   filepath.Join(blocker, "child"),
	} {
		if PathInRoot(path, root) {
			t.Fatalf("%s escape accepted: %q", name, path)
		}
	}
}

func TestValidateRootRejectsHostAndCheckout(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		ordinary, err := os.MkdirTemp("/tmp", "ordinary-sandbox-*")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(ordinary) })
		if err := validateRootCandidate(ordinary); err == nil {
			t.Fatalf("temporary directory without omni-test ancestry accepted: %q", ordinary)
		}
	}
	for name, path := range map[string]string{
		"filesystem root": string(filepath.Separator),
		"real home":       current.HomeDir,
		"checkout":        checkoutRoot(),
	} {
		if path != "" {
			if err := validateRootCandidate(path); err == nil {
				t.Fatalf("%s accepted as sandbox root: %q", name, path)
			}
		}
	}
}

func TestSandboxValidationFailsClosed(t *testing.T) {
	scenarios := []string{"missing-root", "missing-marker", "marker-symlink", "wrong-nonce"}
	if runtime.GOOS != "windows" {
		scenarios = append(scenarios, "root-mode", "marker-mode")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			sandbox, err := CreateSandbox()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(sandbox.Root) })
			env := sandbox.SanitizedEnv()
			switch scenario {
			case "missing-root":
				env = withoutEnv(env, rootEnv)
			case "missing-marker":
				if err := os.Remove(filepath.Join(sandbox.Root, markerName)); err != nil {
					t.Fatal(err)
				}
			case "marker-symlink":
				marker := filepath.Join(sandbox.Root, markerName)
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(filepath.Dir(sandbox.Root), "outside-marker")
				if err := os.WriteFile(outside, []byte(sandbox.nonce), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(outside) })
				if err := os.Symlink(outside, marker); err != nil {
					t.Fatal(err)
				}
			case "wrong-nonce":
				env = replaceEnv(env, nonceEnv, "wrong")
			case "root-mode":
				if err := os.Chmod(sandbox.Root, 0o755); err != nil {
					t.Fatal(err)
				}
			case "marker-mode":
				if err := os.Chmod(filepath.Join(sandbox.Root, markerName), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestGuardInitHelper$") //nolint:gosec
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("invalid sandbox scenario %q succeeded\n%s", scenario, out)
			}
			if !strings.Contains(string(out), "unsafe test sandbox") {
				t.Fatalf("scenario %q did not fail through guard: %v\n%s", scenario, err, out)
			}
		})
	}
}

func TestGuardInitHelper(t *testing.T) {
	root := os.Getenv(rootEnv)
	for _, key := range []string{
		"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "TMPDIR",
		"GOCACHE", "GOMODCACHE",
	} {
		if !PathInRoot(os.Getenv(key), root) {
			t.Fatalf("child %s escaped sandbox: %q", key, os.Getenv(key))
		}
	}
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK", "AWS_PROFILE"} {
		if os.Getenv(key) != "" {
			t.Fatalf("child retained credential environment %s", key)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if os.Getenv(key) != "http://127.0.0.1:1" {
			t.Fatalf("child %s is not blocked: %q", key, os.Getenv(key))
		}
	}
	if want := os.Getenv("OMNI_TEST_HELPER_EXPECT"); want != "" && os.Getenv("OMNI_TEST_HELPER_INPUT") != want {
		t.Fatalf("nested helper input=%q, want %q", os.Getenv("OMNI_TEST_HELPER_INPUT"), want)
	}
	if home := os.Getenv("OMNI_TEST_HELPER_OUTSIDE_HOME"); home != "" && PathInRoot(root, home) {
		t.Fatalf("direct test sandbox %q is inside real HOME %q", root, home)
	}
	fmt.Printf("CHILD_ROOT=%s\n", root)
}

func TestSanitizedEnvLaunchesValidatedChild(t *testing.T) {
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Cleanup() })
	roots := make(map[string]bool)
	for range 2 {
		cmd := exec.Command(os.Args[0], "-test.v", "-test.run=^TestGuardInitHelper$") //nolint:gosec
		cmd.Env = sandbox.SanitizedEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("validated child failed: %v\n%s", err, out)
		}
		childRoot := outputValue(string(out), "CHILD_ROOT=")
		if childRoot == "" || childRoot == sandbox.Root || !PathInRoot(childRoot, sandbox.Root) {
			t.Fatalf("child root %q is not a unique descendant of %q\n%s", childRoot, sandbox.Root, out)
		}
		roots[childRoot] = true
	}
	if len(roots) != 2 {
		t.Fatalf("test processes reused roots: %#v", roots)
	}
}

func TestDirectChildClearsCredentialsAndBlocksNetwork(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardInitHelper$") //nolint:gosec
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GITHUB_TOKEN=host-secret",
		"GH_TOKEN=host-secret",
		"SSH_AUTH_SOCK=/host/agent.sock",
		"AWS_PROFILE=production",
		"HTTP_PROXY=http://host-proxy",
		"https_proxy=http://host-proxy",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("direct child isolation failed: %v\n%s", err, out)
	}
}

func TestRunnerParentExternalBuildCachesAreReplaced(t *testing.T) {
	parent, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Cleanup() })
	env := parent.SanitizedEnv()
	env = replaceEnv(env, "GOCACHE", t.TempDir())
	env = replaceEnv(env, "GOMODCACHE", t.TempDir())
	env = replaceEnv(env, "HTTP_PROXY", "http://public-build-proxy.example")
	env = replaceEnv(env, "HTTPS_PROXY", "http://public-build-proxy.example")
	env = replaceEnv(env, "NO_PROXY", "proxy.golang.org,sum.golang.org")
	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardInitHelper$") //nolint:gosec
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("runner-parent child failed to replace external build caches: %v\n%s", err, out)
	}
}

func TestNestedTestHelperInputsSurviveChildFork(t *testing.T) {
	t.Setenv("OMNI_TEST_HELPER_INPUT", "fixture-value")
	t.Setenv("OMNI_TEST_HELPER_EXPECT", "fixture-value")
	t.Setenv("OMNI_UNRELATED_VALUE", "must-not-survive")
	parent, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Cleanup() })
	env := parent.SanitizedEnv()
	if envValues(env)["OMNI_UNRELATED_VALUE"] != "" {
		t.Fatal("unrelated OMNI_ value survived sanitization")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardInitHelper$") //nolint:gosec
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nested helper input did not survive child fork: %v\n%s", err, out)
	}
}

func TestSanitizedEnvAndSandboxUniqueness(t *testing.T) {
	first, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Cleanup() })
	second, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Cleanup() })
	if first.Root == second.Root {
		t.Fatal("parallel-capable sandboxes share a root")
	}

	t.Setenv("GITHUB_TOKEN", "host-secret")
	t.Setenv("SSH_AUTH_SOCK", "/host/agent.sock")
	env := first.SanitizedEnv("FLOW_ID=tools.list", "HOME=/escape")
	values := envValues(env)
	if values["HOME"] != first.Home || values[rootEnv] != first.Root || values[nonceEnv] == "" {
		t.Fatalf("sandbox assignments missing from sanitized env: %#v", values)
	}
	for _, secret := range []string{"GITHUB_TOKEN", "SSH_AUTH_SOCK"} {
		if _, ok := values[secret]; ok {
			t.Fatalf("sanitized env inherited %s", secret)
		}
	}
	if values["FLOW_ID"] != "tools.list" {
		t.Fatal("explicit non-path environment value was not preserved")
	}
}

func TestSanitizedEnvDropsCallerGoNetworkPolicy(t *testing.T) {
	for _, key := range []string{"GOPROXY", "GONOPROXY", "GOPRIVATE", "GONOSUMDB"} {
		t.Setenv(key, "caller-value")
	}
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Cleanup() })
	values := envValues(sandbox.SanitizedEnv())
	for _, key := range []string{"GOPROXY", "GONOPROXY", "GOPRIVATE", "GONOSUMDB"} {
		if _, ok := values[key]; ok {
			t.Fatalf("sanitized child retained caller %s", key)
		}
	}
}

func TestDefaultHTTPClientAllowsOnlyLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	resp, err := http.DefaultClient.Get(server.URL)
	if err != nil {
		t.Fatalf("loopback request rejected: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("loopback status=%d", resp.StatusCode)
	}
	blockedResp, err := http.DefaultClient.Get("http://192.0.2.1/")
	if blockedResp != nil {
		_ = blockedResp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "testguard blocked non-loopback") {
		t.Fatalf("external default-client request not blocked: %v", err)
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || transport != http.DefaultClient.Transport {
		t.Fatal("default transport and client do not share the loopback guard")
	}
}

func TestEffectiveEnvAllowsSafeDescendantOverrides(t *testing.T) {
	root := os.Getenv(rootEnv)
	home := filepath.Join(root, "work", "alternate-home")
	configDir := filepath.Join(root, "work", "alternate-config")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OMNI_CONFIG", filepath.Join(configDir, "settings.json"))
	if _, err := sandboxFromEnv(); err != nil {
		t.Fatalf("safe descendant override rejected: %v", err)
	}
	if err := RequireTempPath("alternate config", os.Getenv("OMNI_CONFIG")); err != nil {
		t.Fatalf("safe override rejected by mutation guard: %v", err)
	}
}

func TestEffectiveEnvAllowsSafeFallbacksAndFutureDirectories(t *testing.T) {
	for _, key := range []string{
		"OMNI_CONFIG", "OMNI_CACHE_DIR", "OMNI_STATE_DIR", "XDG_CONFIG_HOME",
		"XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
	} {
		t.Setenv(key, "")
	}
	if _, err := sandboxFromEnv(); err != nil {
		t.Fatalf("safe empty fallback inputs rejected: %v", err)
	}

	t.Setenv("XDG_STATE_HOME", filepath.Join(os.Getenv(rootEnv), "work", "future", "state"))
	if _, err := sandboxFromEnv(); err != nil {
		t.Fatalf("future in-sandbox directory rejected: %v", err)
	}
	blocker := filepath.Join(os.Getenv(rootEnv), "work", "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blocker)
	if _, err := sandboxFromEnv(); err == nil {
		t.Fatal("existing non-directory environment root accepted")
	}
}

func TestEffectiveEnvRejectsMissingAndEscapedRequiredPaths(t *testing.T) {
	root := os.Getenv(rootEnv)
	t.Setenv("HOME", "")
	if _, err := sandboxFromEnv(); err == nil {
		t.Fatal("missing HOME accepted by full child validation")
	}
	t.Setenv("HOME", filepath.Dir(root))
	if _, err := sandboxFromEnv(); err == nil {
		t.Fatal("outside HOME accepted by full child validation")
	}
	outside := filepath.Join(filepath.Dir(root), "effective-outside-home")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	link := filepath.Join(root, "work", "effective-linked-home")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	if _, err := sandboxFromEnv(); err == nil {
		t.Fatal("symlink-escaped HOME accepted by full child validation")
	}
}

func TestRequireTempEntryPathDoesNotFollowFinalSymlink(t *testing.T) {
	root := os.Getenv(rootEnv)
	inside := filepath.Join(root, "work", "inside-link")
	if err := os.Symlink(filepath.Join(root, "work", "missing-target"), inside); err != nil {
		t.Fatal(err)
	}
	if err := RequireTempEntryPath("unlink", inside); err != nil {
		t.Fatalf("broken in-sandbox entry rejected: %v", err)
	}
	outsideTarget := filepath.Join(filepath.Dir(root), "outside-target")
	outsideTargetLink := filepath.Join(root, "work", "outside-target-link")
	if err := os.Symlink(outsideTarget, outsideTargetLink); err != nil {
		t.Fatal(err)
	}
	if err := RequireTempEntryPath("unlink", outsideTargetLink); err != nil {
		t.Fatalf("in-sandbox link entry rejected because of its final target: %v", err)
	}

	outsideEntry := filepath.Join(filepath.Dir(root), "outside-entry")
	if err := os.Symlink(inside, outsideEntry); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outsideEntry) })
	if err := RequireTempEntryPath("unlink", outsideEntry); err == nil {
		t.Fatal("entry outside sandbox accepted")
	}
	outsideDir := filepath.Join(filepath.Dir(root), "outside-parent")
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	intermediate := filepath.Join(root, "work", "outside-parent-link")
	if err := os.Symlink(outsideDir, intermediate); err != nil {
		t.Fatal(err)
	}
	if err := RequireTempEntryPath("unlink", filepath.Join(intermediate, "entry")); err == nil {
		t.Fatal("entry through outside intermediate symlink accepted")
	}
}

func TestEntryPathInRootUsesLexicalEntryAndCanonicalParent(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "missing-target"), broken); err != nil {
		t.Fatal(err)
	}
	escapingParent := filepath.Join(root, "escaping-parent")
	if err := os.Symlink(outside, escapingParent); err != nil {
		t.Fatal(err)
	}
	outsideAlias := filepath.Join(tmp, "outside-alias")
	if err := os.Symlink(root, outsideAlias); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"broken final symlink": broken,
		"missing parents":      filepath.Join(root, "future", "nested", "entry"),
	} {
		if !EntryPathInRoot(path, root) {
			t.Fatalf("%s rejected: %q", name, path)
		}
	}
	for name, path := range map[string]string{
		"root itself":           root,
		"outside location":      filepath.Join(outside, "entry"),
		"intermediate escape":   filepath.Join(escapingParent, "entry"),
		"outside lexical alias": filepath.Join(outsideAlias, "entry"),
	} {
		if EntryPathInRoot(path, root) {
			t.Fatalf("%s accepted: %q", name, path)
		}
	}
}

func TestRequireTempPathDefersSafeInRootOperationErrors(t *testing.T) {
	work := filepath.Join(os.Getenv(rootEnv), "work")
	blocker := filepath.Join(work, "regular-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireTempPath("mkdir target", filepath.Join(blocker, "child")); err != nil {
		t.Fatalf("in-root ENOTDIR path rejected before operation: %v", err)
	}

	denied := filepath.Join(work, "permission-denied")
	if err := os.Mkdir(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(denied, 0o700) })
	target := filepath.Join(denied, "child")
	if _, err := os.Lstat(target); err != nil && os.IsPermission(err) {
		if err := RequireTempPath("create temp target", target); err != nil {
			t.Fatalf("in-root EACCES path rejected before operation: %v", err)
		}
	}
}

func TestRequireTempPathStillRejectsSymlinkEscapes(t *testing.T) {
	root := os.Getenv(rootEnv)
	outside := filepath.Join(filepath.Dir(root), "guard-outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	final := filepath.Join(root, "work", "guard-final-link")
	if err := os.Symlink(filepath.Join(outside, "file"), final); err != nil {
		t.Fatal(err)
	}
	intermediate := filepath.Join(root, "work", "guard-parent-link")
	if err := os.Symlink(outside, intermediate); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"final":        final,
		"intermediate": filepath.Join(intermediate, "file"),
	} {
		if err := RequireTempPath(name+" symlink", path); err == nil {
			t.Fatalf("%s symlink escape accepted", name)
		}
	}
}

func TestPMContainerCapabilityIsPreservedWithoutBypass(t *testing.T) {
	t.Setenv("OMNI_PMCONTAINER", "1")
	t.Setenv("OMNI_PMCONTAINER_PROVIDER", "apt")
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Cleanup() })
	values := envValues(sandbox.SanitizedEnv())
	if values["OMNI_PMCONTAINER"] != "1" || values["OMNI_PMCONTAINER_PROVIDER"] != "apt" {
		t.Fatalf("package-manager capability not preserved: %#v", values)
	}
	if !Isolated() {
		t.Fatal("test isolation should remain independent of package-manager capability")
	}
}

func TestValidateModeAndOwnerHandlesMissingSystemInfo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership is not enforced on Windows")
	}
	if err := validateModeAndOwner("fixture", noSysFileInfo{}, 0o700); err == nil {
		t.Fatal("missing ownership metadata accepted")
	}
}

func TestCleanupRequiresOwnedMarker(t *testing.T) {
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox.Root, markerName), []byte("not-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.Cleanup(); err == nil {
		t.Fatal("cleanup accepted a mismatched marker")
	}
	if _, err := os.Stat(sandbox.Root); err != nil {
		t.Fatalf("unsafe cleanup removed sandbox root: %v", err)
	}
	_ = os.RemoveAll(sandbox.Root)
}

func TestSandboxModesAndToolDirectories(t *testing.T) {
	sandbox, err := CreateSandbox()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Cleanup() })
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{
			sandbox.Root:                            0o700,
			filepath.Join(sandbox.Root, markerName): 0o600,
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Fatalf("%s mode=%04o, want %04o", path, got, want)
			}
		}
	}
	for _, path := range []string{
		filepath.Join(sandbox.Config, "git"), filepath.Join(sandbox.Config, "gnupg"),
		filepath.Join(sandbox.Config, "kube"), filepath.Join(sandbox.Config, "docker"),
		filepath.Join(sandbox.Data, "go"), filepath.Join(sandbox.Data, "cargo"),
		filepath.Join(sandbox.Data, "rustup"), filepath.Join(sandbox.Cache, "go-build"),
		filepath.Join(sandbox.Cache, "go-mod"), filepath.Join(sandbox.Cache, "npm"),
	} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("tool directory %q missing: %v", path, err)
		}
	}
}

func TestMutationGuardRequiresLiveOwnedMarker(t *testing.T) {
	marker := filepath.Join(os.Getenv(rootEnv), markerName)
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(marker, data, 0o600); err != nil {
			t.Errorf("restoring marker: %v", err)
		}
	})
	if err := EnsureSafeEnv(); err == nil {
		t.Fatal("EnsureSafeEnv accepted a sandbox without its marker after initialization")
	}
	if err := RequireTempPath("mutation", filepath.Join(os.Getenv(rootEnv), "work", "target")); err == nil {
		t.Fatal("mutation guard accepted a sandbox without its marker")
	}
}

func withoutEnv(env []string, unwanted string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, unwanted+"=") {
			result = append(result, entry)
		}
	}
	return result
}

func replaceEnv(env []string, key, value string) []string {
	return append(withoutEnv(env, key), key+"="+value)
}

func envValues(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func outputValue(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func copyExecutable(source, target string) error {
	if err := os.Link(source, target); err == nil {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

type noSysFileInfo struct{}

func (noSysFileInfo) Name() string       { return "fixture" }
func (noSysFileInfo) Size() int64        { return 0 }
func (noSysFileInfo) Mode() os.FileMode  { return 0o700 }
func (noSysFileInfo) ModTime() time.Time { return time.Time{} }
func (noSysFileInfo) IsDir() bool        { return true }
func (noSysFileInfo) Sys() any           { return nil }
