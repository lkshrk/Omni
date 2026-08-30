// Package testguard centralizes test filesystem and subprocess isolation.
package testguard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	isolatedEnv      = "OMNI_TEST_ISOLATED"
	rootEnv          = "OMNI_TEST_ROOT"
	nonceEnv         = "OMNI_TEST_NONCE"
	approvedToolsEnv = "OMNI_TEST_APPROVED_TOOLS"
	commandChildEnv  = "OMNI_TEST_COMMAND_CHILD"
	markerName       = ".omni-test-sandbox"
)

const testscriptCommandChild = "testscript"

var approvedTestTools = []string{
	"go", "bash", "sh", "git", "stow", "python3", "node", "npm",
	"awk", "basename", "cat", "chmod", "cmp", "cp", "cut", "date", "dirname", "du", "echo", "env", "find", "grep", "head",
	"ln", "ls", "make", "mkdir", "mktemp", "mv", "od", "printenv", "pwd", "readlink", "realpath", "rm",
	"printf", "sed", "seq", "sleep", "sort", "stat", "tail", "tee", "test", "touch", "tr", "uname", "wc", "which", "xargs",
	"cc", "gcc", "clang", "as", "ld", "pkg-config",
}

var optionalTestTools = map[string]struct{}{
	"apm": {}, "claude": {}, "codex": {}, "grok": {}, "cowsay": {},
}

// Sandbox is a disposable, process-local test environment.
type Sandbox struct {
	Root          string
	Home          string
	Config        string
	Data          string
	Cache         string
	State         string
	Tmp           string
	Work          string
	Bin           string
	OmniConfig    string
	OmniCache     string
	OmniState     string
	nonce         string
	approvedTools []string
}

var (
	ensureOnce      sync.Once
	ensureErr       error
	initialTempRoot = os.TempDir() // Windows may place its owned OS temp below the user profile.
	networkOnce     sync.Once
)

func init() {
	if Active() {
		MustEnsureSafeEnv()
	}
}

// Active reports whether filesystem safety checks must be enforced.
func Active() bool {
	return runningUnderGoTest() || os.Getenv(isolatedEnv) == "1"
}

// Isolated reports explicit test isolation. Package-manager containers are
// deliberately not trusted as filesystem sandboxes.
func Isolated() bool {
	return os.Getenv(isolatedEnv) == "1"
}

func MustEnsureSafeEnv() {
	if err := EnsureSafeEnv(); err != nil {
		panic(err)
	}
}

// EnsureSafeEnv validates an explicitly assigned sandbox or creates one for a
// direct `go test` invocation. Explicit but malformed isolation fails closed.
func EnsureSafeEnv() error {
	if !Active() {
		return nil
	}
	ensureOnce.Do(func() {
		commandChild := os.Getenv(commandChildEnv)
		if commandChild != "" && commandChild != testscriptCommandChild {
			ensureErr = fmt.Errorf("unsafe test sandbox: unknown %s value %q", commandChildEnv, commandChild)
			return
		}
		if commandChild != "" && !Isolated() {
			ensureErr = fmt.Errorf("unsafe test sandbox: %s requires an isolated test environment", commandChildEnv)
			return
		}
		if Isolated() {
			if commandChild == testscriptCommandChild {
				_, ensureErr = sandboxFromEnv()
				return
			}
			if runningUnderGoTest() {
				var parent *Sandbox
				parent, ensureErr = sandboxParentFromEnv()
				var child *Sandbox
				if ensureErr == nil {
					child, ensureErr = createSandboxUnder(parent.Root)
				}
				if ensureErr == nil {
					ensureErr = child.apply()
				}
			} else {
				_, ensureErr = sandboxFromEnv()
			}
			return
		}
		var sandbox *Sandbox
		sandbox, ensureErr = CreateSandbox()
		if ensureErr == nil {
			// A direct go test intentionally leaves this crash-confined temp tree:
			// package init has no reliable process-exit cleanup hook.
			ensureErr = sandbox.apply()
		}
	})
	if ensureErr != nil {
		return ensureErr
	}
	_, err := sandboxFromEnv()
	if err != nil {
		return err
	}
	installDefaultNetworkGuard()
	return nil
}

// CreateSandbox creates a uniquely marked disposable filesystem tree.
func CreateSandbox() (*Sandbox, error) {
	return createSandboxUnder("")
}

func createSandboxUnder(parent string) (*Sandbox, error) {
	if parent == "" {
		parent = directSandboxParent()
	}
	root, err := os.MkdirTemp(parent, fmt.Sprintf("omni-test-%d-*", os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("creating test sandbox: %w", err)
	}
	sandbox := newSandbox(root, newNonce())
	if sandbox.nonce == "" {
		_ = os.RemoveAll(root)
		return nil, errors.New("creating test sandbox nonce")
	}
	if err := sandbox.initialize(); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return sandbox, nil
}

func directSandboxParent() string {
	if runtime.GOOS == "windows" {
		return initialTempRoot
	}
	for _, candidate := range []string{"/tmp", "/private/tmp"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return "/tmp"
}

func newSandbox(root, nonce string) *Sandbox {
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "config")
	cache := filepath.Join(root, "cache")
	state := filepath.Join(root, "state")
	return &Sandbox{
		Root:       root,
		Home:       home,
		Config:     config,
		Data:       filepath.Join(root, "data"),
		Cache:      cache,
		State:      state,
		Tmp:        filepath.Join(root, "tmp"),
		Work:       filepath.Join(root, "work"),
		Bin:        filepath.Join(root, "bin"),
		OmniConfig: filepath.Join(config, "omni", "settings.json"),
		OmniCache:  filepath.Join(cache, "omni"),
		OmniState:  filepath.Join(state, "omni"),
		nonce:      nonce,
	}
}

func (s *Sandbox) initialize() error {
	if err := validateRootCandidate(s.Root); err != nil {
		return err
	}
	approved, err := parseApprovedTools(os.Getenv(approvedToolsEnv))
	if err != nil {
		return err
	}
	s.approvedTools = approved
	for _, dir := range s.directories() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating test sandbox directory %q: %w", dir, err)
		}
	}
	if err := s.installApprovedTools(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Root, markerName), []byte(s.nonce+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing test sandbox marker: %w", err)
	}
	return validateSandbox(s)
}

func (s *Sandbox) installApprovedTools() error {
	for _, name := range append(append([]string(nil), approvedTestTools...), s.approvedTools...) {
		source, err := exec.LookPath(name)
		if err != nil {
			if slices.Contains(s.approvedTools, name) {
				return fmt.Errorf("approved optional test tool %q is unavailable: %w", name, err)
			}
			continue
		}
		resolved, err := resolveApprovedTool(source)
		if err != nil {
			return fmt.Errorf("resolving approved test tool %q: %w", name, err)
		}
		target := filepath.Join(s.Bin, filepath.Base(source))
		if err := os.Symlink(resolved, target); err == nil {
			continue
		}
		if err := os.Link(resolved, target); err == nil {
			continue
		}
		if err := copyApprovedTool(resolved, target); err != nil {
			return fmt.Errorf("installing approved test tool %q: %w", name, err)
		}
	}
	return nil
}

func resolveApprovedTool(source string) (string, error) {
	resolved, err := filepath.EvalSymlinks(source)
	if err == nil {
		return resolved, nil
	}
	// Windows toolcache executables may be reachable through junctions that EvalSymlinks cannot traverse.
	if runtime.GOOS == "windows" {
		if info, statErr := os.Stat(source); statErr == nil && !info.IsDir() {
			return source, nil
		}
	}
	return "", err
}

func parseApprovedTools(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	var tools []string
	seen := make(map[string]struct{})
	for _, name := range strings.Split(value, ",") {
		if name == "" || strings.TrimSpace(name) != name {
			return nil, fmt.Errorf("invalid %s value %q", approvedToolsEnv, value)
		}
		if _, ok := optionalTestTools[name]; !ok {
			return nil, fmt.Errorf("unknown %s tool %q", approvedToolsEnv, name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate %s tool %q", approvedToolsEnv, name)
		}
		seen[name] = struct{}{}
		tools = append(tools, name)
	}
	return tools, nil
}

func copyApprovedTool(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(target)
		return err
	}
	return out.Close()
}

func (s *Sandbox) directories() map[string]string {
	return map[string]string{
		"HOME": s.Home, "APPDATA": filepath.Join(s.Home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(s.Home, "AppData", "Local"),
		"XDG_CONFIG_HOME": s.Config, "XDG_DATA_HOME": s.Data, "XDG_CACHE_HOME": s.Cache,
		"XDG_STATE_HOME": s.State, "TMPDIR": s.Tmp, "work": s.Work, "PATH": s.Bin,
		"OMNI config directory": filepath.Dir(s.OmniConfig), "OMNI_CACHE_DIR": s.OmniCache,
		"OMNI_STATE_DIR": s.OmniState, "Git config directory": filepath.Join(s.Config, "git"),
		"GNUPGHOME": filepath.Join(s.Config, "gnupg"), "DOCKER_CONFIG": filepath.Join(s.Config, "docker"),
		"Kube config directory": filepath.Join(s.Config, "kube"), "GOPATH": filepath.Join(s.Data, "go"),
		"CARGO_HOME": filepath.Join(s.Data, "cargo"), "RUSTUP_HOME": filepath.Join(s.Data, "rustup"),
		"GOCACHE": filepath.Join(s.Cache, "go-build"), "GOMODCACHE": filepath.Join(s.Cache, "go-mod"),
		"NPM_CONFIG_CACHE": filepath.Join(s.Cache, "npm"), "NPM_CONFIG_PREFIX": filepath.Join(s.Data, "npm-global"),
		"npm global lib": filepath.Join(s.Data, "npm-global", "lib"),
	}
}

func newNonce() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(value[:]))
}

// SanitizedEnv returns an explicit subprocess environment. Extra KEY=VALUE
// entries may override non-sandbox values; sandbox paths cannot be overridden.
func (s *Sandbox) SanitizedEnv(extra ...string) []string {
	allowed := []string{
		"PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "CI", "GITHUB_ACTIONS",
		"TERM", "COLORTERM", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
		"GOTOOLCHAIN", "GOSUMDB",
		"GOROOT", "GOEXPERIMENT", "CGO_ENABLED", "CC", "CXX", "AR", "PKG_CONFIG_PATH", "SDKROOT",
		"MACOSX_DEPLOYMENT_TARGET", "DEVELOPER_DIR",
	}
	env := make(map[string]string, len(allowed)+32)
	for _, key := range allowed {
		if value := os.Getenv(key); value != "" {
			putEnv(env, key, value, runtime.GOOS == "windows")
		}
	}
	if os.Getenv("OMNI_PMCONTAINER") == "1" {
		putEnv(env, "OMNI_PMCONTAINER", "1", runtime.GOOS == "windows")
		if provider := os.Getenv("OMNI_PMCONTAINER_PROVIDER"); provider != "" {
			putEnv(env, "OMNI_PMCONTAINER_PROVIDER", provider, runtime.GOOS == "windows")
		}
	}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(normalizeEnvKey(key, runtime.GOOS == "windows"), "OMNI_TEST_HELPER_") {
			putEnv(env, key, value, runtime.GOOS == "windows")
		}
	}
	if value := os.Getenv(commandChildEnv); value == testscriptCommandChild {
		putEnv(env, commandChildEnv, value, runtime.GOOS == "windows")
	}
	for _, entry := range extra {
		if key, value, ok := strings.Cut(entry, "="); ok && key != "" && !isProtectedEnvKey(key, runtime.GOOS == "windows") {
			putEnv(env, key, value, runtime.GOOS == "windows")
		}
	}
	for key, value := range s.envMap() {
		putEnv(env, key, value, runtime.GOOS == "windows")
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

func (s *Sandbox) envMap() map[string]string {
	env := map[string]string{
		isolatedEnv:             "1",
		rootEnv:                 s.Root,
		nonceEnv:                s.nonce,
		"HOME":                  s.Home,
		"USERPROFILE":           s.Home,
		"APPDATA":               filepath.Join(s.Home, "AppData", "Roaming"),
		"LOCALAPPDATA":          filepath.Join(s.Home, "AppData", "Local"),
		"XDG_CONFIG_HOME":       s.Config,
		"XDG_DATA_HOME":         s.Data,
		"XDG_CACHE_HOME":        s.Cache,
		"XDG_STATE_HOME":        s.State,
		"OMNI_CONFIG":           s.OmniConfig,
		"OMNI_CACHE_DIR":        s.OmniCache,
		"OMNI_STATE_DIR":        s.OmniState,
		"TMPDIR":                s.Tmp,
		"TMP":                   s.Tmp,
		"TEMP":                  s.Tmp,
		"PATH":                  s.Bin,
		"SHELL":                 s.shellPath(),
		"GIT_CONFIG_GLOBAL":     filepath.Join(s.Config, "git", "config"),
		"GIT_CONFIG_SYSTEM":     nullDevice(),
		"GIT_CONFIG_NOSYSTEM":   "1",
		"GIT_TERMINAL_PROMPT":   "0",
		"GIT_ASKPASS":           falseCommand(),
		"GNUPGHOME":             filepath.Join(s.Config, "gnupg"),
		"KUBECONFIG":            filepath.Join(s.Config, "kube", "config"),
		"DOCKER_CONFIG":         filepath.Join(s.Config, "docker"),
		"GOCACHE":               filepath.Join(s.Cache, "go-build"),
		"GOMODCACHE":            filepath.Join(s.Cache, "go-mod"),
		"GOPATH":                filepath.Join(s.Data, "go"),
		"CARGO_HOME":            filepath.Join(s.Data, "cargo"),
		"RUSTUP_HOME":           filepath.Join(s.Data, "rustup"),
		"NPM_CONFIG_USERCONFIG": filepath.Join(s.Config, "npmrc"),
		"NPM_CONFIG_CACHE":      filepath.Join(s.Cache, "npm"),
		"NPM_CONFIG_PREFIX":     filepath.Join(s.Data, "npm-global"),
		"HTTP_PROXY":            "http://127.0.0.1:1",
		"HTTPS_PROXY":           "http://127.0.0.1:1",
		"ALL_PROXY":             "http://127.0.0.1:1",
		"http_proxy":            "http://127.0.0.1:1",
		"https_proxy":           "http://127.0.0.1:1",
		"all_proxy":             "http://127.0.0.1:1",
		"NO_PROXY":              "localhost,127.0.0.1,::1",
		"no_proxy":              "localhost,127.0.0.1,::1",
	}
	if len(s.approvedTools) > 0 {
		env[approvedToolsEnv] = strings.Join(s.approvedTools, ",")
	}
	return env
}

func (s *Sandbox) shellPath() string {
	if path, ok := approvedToolPath(s.Bin, "sh", os.Getenv("PATHEXT"), runtime.GOOS == "windows"); ok {
		return path
	}
	return filepath.Join(s.Bin, "sh")
}

func approvedToolPath(bin, name, pathExt string, windows bool) (string, bool) {
	for _, candidate := range toolCandidateNames(name, pathExt, windows) {
		path := filepath.Join(bin, candidate)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func toolCandidateNames(name, pathExt string, windows bool) []string {
	if !windows || filepath.Ext(name) != "" {
		return []string{name}
	}
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD"
	}
	result := []string{name}
	for _, ext := range strings.Split(pathExt, ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		result = append(result, name+ext)
	}
	return result
}

func nullDevice() string {
	if runtime.GOOS == "windows" {
		return "NUL"
	}
	return "/dev/null"
}

func falseCommand() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 1"
	}
	return "/bin/false"
}

func isProtectedEnvKey(key string, windows bool) bool {
	switch normalizeEnvKey(key, windows) {
	case isolatedEnv, rootEnv, nonceEnv, approvedToolsEnv, commandChildEnv, "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
		"OMNI_CONFIG", "OMNI_CACHE_DIR", "OMNI_STATE_DIR", "TMPDIR", "TMP", "TEMP", "PATH",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
		"NO_PROXY", "no_proxy", "SHELL", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
		"GIT_TERMINAL_PROMPT", "GIT_ASKPASS", "GNUPGHOME", "KUBECONFIG", "DOCKER_CONFIG", "GOCACHE",
		"GOMODCACHE", "GOPATH", "CARGO_HOME", "RUSTUP_HOME", "NPM_CONFIG_USERCONFIG", "NPM_CONFIG_CACHE",
		"NPM_CONFIG_PREFIX":
		return true
	case "OMNI_PMCONTAINER", "OMNI_PMCONTAINER_PROVIDER":
		return true
	default:
		return false
	}
}

func normalizeEnvKey(key string, windows bool) string {
	if windows {
		return strings.ToUpper(key)
	}
	return key
}

func putEnv(env map[string]string, key, value string, windows bool) {
	normalized := normalizeEnvKey(key, windows)
	for existing := range env {
		if normalizeEnvKey(existing, windows) == normalized {
			delete(env, existing)
		}
	}
	env[key] = value
}

func (s *Sandbox) apply() error {
	env := s.SanitizedEnv()
	os.Clearenv()
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("setting %s for test sandbox: %w", key, err)
		}
	}
	return validateSandbox(s)
}

// Cleanup removes only a still-valid sandbox owned by this instance.
func (s *Sandbox) Cleanup() error {
	if err := validateSandboxIdentity(s); err != nil {
		return fmt.Errorf("refusing unsafe test sandbox cleanup: %w", err)
	}
	if err := os.RemoveAll(s.Root); err != nil {
		return fmt.Errorf("cleaning test sandbox: %w", err)
	}
	return nil
}

func sandboxFromEnv() (*Sandbox, error) {
	sandbox, err := sandboxIdentityFromEnv()
	if err != nil {
		return nil, err
	}
	if err := validateEffectiveEnv(sandbox); err != nil {
		return nil, err
	}
	return sandbox, nil
}

func sandboxIdentityFromEnv() (*Sandbox, error) {
	sandbox, err := sandboxParentFromEnv()
	if err != nil {
		return nil, err
	}
	if err := validateSecurityEnv(sandbox); err != nil {
		return nil, err
	}
	return sandbox, nil
}

func sandboxParentFromEnv() (*Sandbox, error) {
	root := strings.TrimSpace(os.Getenv(rootEnv))
	nonce := strings.TrimSpace(os.Getenv(nonceEnv))
	if root == "" || nonce == "" {
		return nil, fmt.Errorf("unsafe test sandbox: %s and %s are required", rootEnv, nonceEnv)
	}
	sandbox := newSandbox(root, nonce)
	approved, err := parseApprovedTools(os.Getenv(approvedToolsEnv))
	if err != nil {
		return nil, err
	}
	sandbox.approvedTools = approved
	if err := validateSandboxIdentity(sandbox); err != nil {
		return nil, err
	}
	return sandbox, nil
}

func validateEffectiveEnv(s *Sandbox) error {
	if err := validateSandbox(s); err != nil {
		return err
	}
	return validateEffectivePaths(s)
}

func validateSecurityEnv(s *Sandbox) error {
	for key, want := range map[string]string{
		isolatedEnv: "1", rootEnv: s.Root, nonceEnv: s.nonce,
		"GIT_CONFIG_SYSTEM": nullDevice(), "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": falseCommand(),
		"HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1",
		"ALL_PROXY": "http://127.0.0.1:1", "http_proxy": "http://127.0.0.1:1",
		"https_proxy": "http://127.0.0.1:1", "all_proxy": "http://127.0.0.1:1",
		"NO_PROXY": "localhost,127.0.0.1,::1", "no_proxy": "localhost,127.0.0.1,::1",
		"SHELL": s.shellPath(),
	} {
		if got := os.Getenv(key); got != want {
			return fmt.Errorf("unsafe test sandbox: %s=%q, want %q", key, got, want)
		}
	}
	return nil
}

func validateEffectivePaths(s *Sandbox) error {
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		return errors.New("unsafe test sandbox: PATH is empty")
	}
	for _, path := range filepath.SplitList(pathValue) {
		if err := validateEffectiveDirectory("PATH", path, s.Root, false); err != nil {
			return err
		}
	}
	for _, key := range []string{
		"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TMPDIR", "TMP", "TEMP",
		"GNUPGHOME", "DOCKER_CONFIG", "GOCACHE", "GOMODCACHE", "GOPATH", "CARGO_HOME", "RUSTUP_HOME",
		"NPM_CONFIG_CACHE", "NPM_CONFIG_PREFIX",
	} {
		if err := validateEffectiveDirectory(key, os.Getenv(key), s.Root, false); err != nil {
			return err
		}
	}
	for _, key := range []string{
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "OMNI_CACHE_DIR", "OMNI_STATE_DIR",
	} {
		if err := validateEffectiveDirectory(key, os.Getenv(key), s.Root, true); err != nil {
			return err
		}
	}
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "KUBECONFIG", "NPM_CONFIG_USERCONFIG"} {
		path := os.Getenv(key)
		if err := validateEffectiveFile(key, path, s.Root, false); err != nil {
			return err
		}
	}
	return validateEffectiveFile("OMNI_CONFIG", os.Getenv("OMNI_CONFIG"), s.Root, true)
}

func validateEffectiveDirectory(key, path, root string, optional bool) error {
	if path == "" && optional {
		return nil
	}
	if err := requireDescendant(key, path, root); err != nil {
		return fmt.Errorf("unsafe test sandbox: %w", err)
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unsafe test sandbox: inspect %s=%q: %w", key, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe test sandbox: %s=%q must be a directory path", key, path)
	}
	return nil
}

func validateEffectiveFile(key, path, root string, optional bool) error {
	if path == "" && optional {
		return nil
	}
	if err := requireDescendant(key, path, root); err != nil {
		return fmt.Errorf("unsafe test sandbox: %w", err)
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("unsafe test sandbox: %s=%q must be a file path", key, path)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unsafe test sandbox: inspect %s=%q: %w", key, path, err)
	}
	return validateEffectiveDirectory(key+" parent", filepath.Dir(path), root, false)
}

func validateSandbox(s *Sandbox) error {
	if err := validateSandboxIdentity(s); err != nil {
		return err
	}
	for name, path := range s.directories() {
		if err := requireDescendant(name, path, s.Root); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("unsafe test sandbox: %s=%q must be a directory: %w", name, path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("unsafe test sandbox: %s=%q must be a directory", name, path)
		}
	}
	return requireDescendant("OMNI_CONFIG", s.OmniConfig, s.Root)
}

func validateSandboxIdentity(s *Sandbox) error {
	if s == nil || strings.TrimSpace(s.Root) == "" || strings.TrimSpace(s.nonce) == "" {
		return errors.New("unsafe test sandbox: missing root or nonce")
	}
	if err := validateRootCandidate(s.Root); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(s.Root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe test sandbox: root must be a real directory: %w", err)
	}
	if err := validateModeAndOwner("root", rootInfo, 0o700); err != nil {
		return err
	}
	marker := filepath.Join(s.Root, markerName)
	markerInfo, err := os.Lstat(marker)
	if err != nil || !markerInfo.Mode().IsRegular() {
		return fmt.Errorf("unsafe test sandbox: marker must be a regular file: %w", err)
	}
	if err := validateModeAndOwner("marker", markerInfo, 0o600); err != nil {
		return err
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return fmt.Errorf("reading test sandbox marker: %w", err)
	}
	if strings.TrimSpace(string(data)) != s.nonce {
		return errors.New("unsafe test sandbox: marker nonce mismatch")
	}
	return nil
}

func validateRootCandidate(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("unsafe test sandbox: root is empty")
	}
	canonical, err := canonicalPath(root)
	if err != nil {
		return fmt.Errorf("unsafe test sandbox root %q: %w", root, err)
	}
	if canonical == filepath.VolumeName(canonical)+string(filepath.Separator) {
		return fmt.Errorf("unsafe test sandbox: root %q is a filesystem root", canonical)
	}
	if !hasSafeTempAncestry(canonical) {
		return fmt.Errorf("unsafe test sandbox: root %q lacks omni-test ancestry under system temp", canonical)
	}
	if current, err := user.Current(); err == nil {
		if home, err := canonicalPath(current.HomeDir); err == nil && canonical == home {
			return fmt.Errorf("unsafe test sandbox: root %q is the real user home", canonical)
		}
	}
	for _, protected := range protectedRoots() {
		if protected != "" && PathInRoot(canonical, protected) {
			return fmt.Errorf("unsafe test sandbox: root %q is inside protected path %q", canonical, protected)
		}
	}
	return nil
}

func protectedRoots() []string {
	roots := []string{"/etc", "/usr", "/bin", "/sbin", "/opt", "/System", "/Library", "/Applications"}
	if checkout := checkoutRoot(); checkout != "" {
		roots = append(roots, checkout)
	}
	if runtime.GOOS == "windows" {
		roots = append(roots, os.Getenv("SystemRoot"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"))
	}
	return roots
}

func hasSafeTempAncestry(path string) bool {
	for _, tempRoot := range systemTempRoots() {
		canonicalTemp, err := canonicalPath(tempRoot)
		if err != nil || !PathInRoot(path, canonicalTemp) || path == canonicalTemp {
			continue
		}
		rel, err := filepath.Rel(canonicalTemp, path)
		if err != nil {
			continue
		}
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if strings.HasPrefix(part, "omni-test") {
				return true
			}
		}
	}
	return false
}

func systemTempRoots() []string {
	if runtime.GOOS == "windows" {
		return []string{initialTempRoot}
	}
	return []string{"/tmp", "/private/tmp"}
}

func validateModeAndOwner(kind string, info os.FileInfo, wantMode os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if got := info.Mode().Perm(); got != wantMode {
		return fmt.Errorf("unsafe test sandbox: %s mode is %04o, want %04o", kind, got, wantMode)
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("unsafe test sandbox: resolve current owner: %w", err)
	}
	wantUID, err := strconv.ParseUint(current.Uid, 10, 64)
	if err != nil {
		return fmt.Errorf("unsafe test sandbox: parse current owner: %w", err)
	}
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return fmt.Errorf("unsafe test sandbox: %s ownership is unavailable", kind)
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return fmt.Errorf("unsafe test sandbox: %s ownership is unavailable", kind)
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return fmt.Errorf("unsafe test sandbox: %s ownership is unavailable", kind)
	}
	uid := value.FieldByName("Uid")
	if !uid.IsValid() || (uid.Kind() < reflect.Uint || uid.Kind() > reflect.Uint64) || uid.Uint() != wantUID {
		return fmt.Errorf("unsafe test sandbox: %s is not owned by current user", kind)
	}
	return nil
}

func checkoutRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				return dir
			}
		}
		next := filepath.Dir(dir)
		if next == dir {
			return ""
		}
		dir = next
	}
}

func RequireHome(context string) error {
	if !Active() {
		return nil
	}
	return RequireTempPath("HOME for "+context, os.Getenv("HOME"))
}

func RequireTempPath(kind, path string) error {
	if !Active() {
		return nil
	}
	sandbox, err := sandboxFromEnv()
	if err != nil {
		return err
	}
	if err := requireDescendant(kind, path, sandbox.Root); err != nil {
		return fmt.Errorf("unsafe test sandbox: %w", err)
	}
	return nil
}

// RequireTempEntryPath validates a directory entry for destructive operations
// without following its final symlink. Its parent is still fully canonicalized.
func RequireTempEntryPath(kind, path string) error {
	if !Active() {
		return nil
	}
	sandbox, err := sandboxFromEnv()
	if err != nil {
		return err
	}
	if !EntryPathInRoot(path, sandbox.Root) {
		return fmt.Errorf("unsafe test sandbox: %s=%q is not an entry below assigned sandbox root %q", kind, path, sandbox.Root)
	}
	return nil
}

// EntryPathInRoot reports whether path names a strict descendant entry of root.
// Intermediate symlinks are resolved; the final entry is deliberately not.
func EntryPathInRoot(path, root string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	absRoot = filepath.Clean(absRoot)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	canonicalRoot, err := canonicalPath(absRoot)
	if err != nil {
		return false
	}
	canonicalParent, err := canonicalPath(filepath.Dir(absPath))
	if err != nil {
		return false
	}
	return PathInRoot(canonicalParent, canonicalRoot)
}

func PathInTempRoot(path string) bool {
	sandbox, err := sandboxFromEnv()
	return err == nil && path != sandbox.Root && PathInRoot(path, sandbox.Root)
}

func requireDescendant(kind, path, root string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s path is empty", kind)
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("%s is missing", rootEnv)
	}
	inside, sameRoot, err := writableDescendant(path, root)
	if err != nil {
		return fmt.Errorf("%s=%q cannot be resolved safely: %w", kind, path, err)
	}
	if sameRoot {
		return fmt.Errorf("%s=%q is the sandbox root itself", kind, path)
	}
	if !inside {
		return fmt.Errorf("%s=%q is outside assigned sandbox root %q", kind, path, root)
	}
	return nil
}

// writableDescendant proves containment without requiring the target operation
// to succeed. In-root ENOTDIR/EACCES remains the operation's error to report.
func writableDescendant(path, root string) (inside, sameRoot bool, err error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, false, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, false, err
	}
	absPath = filepath.Clean(absPath)
	absRoot = filepath.Clean(absRoot)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false, false, err
	}
	if rel == "." {
		return false, true, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, false, nil
	}
	canonicalRoot, err := canonicalPath(absRoot)
	if err != nil {
		return false, false, err
	}
	current := canonicalRoot
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		candidate := filepath.Join(current, part)
		info, statErr := os.Lstat(candidate)
		if os.IsNotExist(statErr) || os.IsPermission(statErr) || errors.Is(statErr, syscall.ENOTDIR) {
			return true, false, nil
		}
		if statErr != nil {
			return false, false, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, evalErr := filepath.EvalSymlinks(candidate)
			if evalErr != nil {
				return false, false, evalErr
			}
			if !PathInRoot(resolved, canonicalRoot) {
				return false, false, nil
			}
			current = resolved
			continue
		}
		if !info.IsDir() && i < len(parts)-1 {
			return true, false, nil
		}
		current = candidate
	}
	return PathInRoot(current, canonicalRoot), false, nil
}

func PathInRoot(path, root string) bool {
	canonicalPathValue, err := canonicalPath(path)
	if err != nil {
		return false
	}
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalPathValue)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func runningUnderGoTest() bool {
	if info, ok := debug.ReadBuildInfo(); ok && testBinaryBuildPath(info.Path) {
		return true
	}
	if testRunnerArgs(os.Args[1:]) {
		return true
	}
	// Fallback for unusual test toolchains that omit the standard build path.
	return registeredTestFlags(flag.Lookup)
}

func testBinaryBuildPath(path string) bool {
	return strings.HasSuffix(path, ".test")
}

func testRunnerArgs(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-test.") {
			return true
		}
	}
	return false
}

func registeredTestFlags(lookup func(string) *flag.Flag) bool {
	return lookup("test.v") != nil || lookup("test.run") != nil || lookup("test.timeout") != nil
}

func installDefaultNetworkGuard() {
	networkOnce.Do(func() {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			transport = &http.Transport{}
		}
		guarded := transport.Clone()
		guarded.Proxy = nil
		dial := guarded.DialContext
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil || !loopbackHost(host) {
				return nil, fmt.Errorf("testguard blocked non-loopback network address %q", address)
			}
			return dial(ctx, network, address)
		}
		http.DefaultTransport = guarded
		http.DefaultClient.Transport = guarded
	})
}

func loopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			resolved, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil {
				return "", evalErr
			}
			if len(missing) > 0 && !info.IsDir() {
				return "", syscall.ENOTDIR
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", statErr
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
