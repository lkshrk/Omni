//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUIConfiguredHostStartsDashboard(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(home, cache)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	screen := runTUISmoke(t, bin, root, env, "--config", configPath, "--cache-dir", cache)
	if !strings.Contains(screen, "Dashboard") || !strings.Contains(screen, "Tools") {
		t.Fatalf("TUI did not render main tabs; screen:\n%s", screen)
	}
	if strings.Contains(strings.ToLower(screen), "setup") {
		t.Fatalf("configured host opened setup instead of dashboard; screen:\n%s", screen)
	}
}

func TestTUIFallbackProviderListSmoke(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(home, cache)

	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"node", "python", "pip"}},
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "rg"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Binary: "rg",
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
			"jq": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "jq"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "jqlang", Repo: "jq"},
					Status: config.FallbackStatusVerified,
					Commands: config.FallbackCommands{
						Check: "command -v jq",
					},
				},
			},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "rg"}, {Name: "jq"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache,
		&database.ToolCache{Name: "rg", Provider: "apt", Package: "rg", Installed: false, Tracked: true},
		&database.ToolCache{Name: "jq", Provider: "apt", Package: "jq", Installed: true, InstalledWith: "apt", Version: sql.NullString{String: "1.7", Valid: true}, Tracked: true},
	)
	listOut := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")
	if !strings.Contains(listOut, "rg") || !strings.Contains(listOut, "jq") {
		t.Fatalf("seeded tools are not visible through app list:\n%s", listOut)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(tty *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, tty, "\t")
		toolsScreen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "rg") &&
				strings.Contains(text, "gh?") &&
				strings.Contains(text, "jq") &&
				strings.Contains(text, "apt")
		}, "TUI did not render provider-list fallback/native tool states")
		writeTUIKeys(t, tty, "f")
		editorScreen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Set Fallback: rg") &&
				strings.Contains(text, "BurntSushi/ripgrep") &&
				strings.Contains(text, "install rg")
		}, "TUI did not open fallback editor for missing provider-list tool")
		return toolsScreen + "\n" + editorScreen
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error during fallback/provider-list smoke; screen:\n%s", screen)
	}
}

func TestTUIFallbackEditorPrefillsConfiguredGitHint(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(home, cache)

	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"node", "python", "pip"}},
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "rg"}},
				Git:       "https://github.com/BurntSushi/ripgrep",
			},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "rg"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache,
		&database.ToolCache{Name: "rg", Provider: "apt", Package: "rg", Installed: false, Tracked: true},
	)
	listOut := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")
	if !strings.Contains(listOut, "rg") {
		t.Fatalf("seeded tool is not visible through app list:\n%s", listOut)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(tty *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, tty, "\t")
		toolsScreen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "rg") &&
				strings.Contains(text, "apt") &&
				!strings.Contains(text, "gh?")
		}, "TUI did not render provider-list tool without fallback status")
		writeTUIKeys(t, tty, "f")
		editorScreen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Set Fallback: rg") &&
				strings.Contains(text, "BurntSushi/ripgrep")
		}, "TUI did not prefill fallback editor from configured git hint")
		writeTUIKeys(t, tty, "\r")
		savedScreen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "fallback saved for rg from gh BurntSushi/ripgrep")
		}, "TUI did not save fallback from configured git hint")
		return toolsScreen + "\n" + editorScreen + "\n" + savedScreen
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error during configured-git fallback smoke; screen:\n%s", screen)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config after fallback save: %v", err)
	}
	fallback := cfg.Tools["rg"].Fallback
	if fallback == nil ||
		fallback.Source.Type != config.FallbackSourceGitHub ||
		fallback.Source.Owner != "BurntSushi" ||
		fallback.Source.Repo != "ripgrep" ||
		fallback.Status != config.FallbackStatusUnresolved {
		t.Fatalf("saved fallback = %+v, want unresolved GitHub fallback for BurntSushi/ripgrep", fallback)
	}
}

func TestTUIIncludesStaticIgnoredDotCandidate(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(home, cache)
	repo := filepath.Join(home, "dotfiles")

	if err := os.MkdirAll(filepath.Join(home, ".config", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	initDotsRepo(t, repo, env)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "set", "dots_repo", "~/dotfiles")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(tty *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, tty, "\t", "\t")
		screen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "node_modules") && strings.Contains(text, "Ignored")
		}, "TUI did not render ignored node_modules candidate")
		writeTUIKeys(t, tty, "x", "x")
		if !waitForConfigDot(configPath, "testhost", "node_modules", false, 8*time.Second) {
			t.Fatalf("node_modules was not included after x,x; screen:\n%s", screen)
		}
		return capture.Text()
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while including ignored candidate; screen:\n%s", screen)
	}
}

func TestTUISyncsDiscoveredDotCandidate(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(home, cache)
	repo := filepath.Join(home, "dotfiles")

	candidateDir := filepath.Join(home, ".config", "ghost")
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initDotsRepo(t, repo, env)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "set", "dots_repo", "~/dotfiles")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(tty *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, tty, "\t", "\t")
		screen := waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "ghost") && strings.Contains(text, "~/.config/ghost")
		}, "TUI did not render discovered ghost candidate")
		writeTUIKeys(t, tty, "s")
		if !waitForConfigDot(configPath, "testhost", "ghost", false, 8*time.Second) {
			t.Fatalf("ghost was not persisted after sync; screen:\n%s", screen)
		}
		return capture.Text()
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while syncing discovered candidate; screen:\n%s", screen)
	}
}

func TestTUIDashboardReconcileFixesDotIgnorePatterns(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(home, cache)
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DisabledProviders: []string{"system", "node", "python", "pip"},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{
				Name: "dev",
				Dots: []config.DotEntry{
					{Name: "claude", Path: "~/.claude", Ignore: []string{"*", "!/settings.json", "!/skills/", "skills", "!/hooks/", "hooks"}},
				},
			},
		},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(tty *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")

		var planScreen string
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			writeTUIKeys(t, tty, "A")
			if screen, ok := waitForScreen(capture, 500*time.Millisecond, func(text string) bool {
				return strings.Contains(text, "Reconcile Plan") && strings.Contains(text, "Fix ignore patterns")
			}); ok {
				planScreen = screen
				break
			}
		}
		if planScreen == "" {
			t.Fatalf("TUI did not open reconcile plan with fix-ignore step; screen:\n%s", capture.Text())
		}

		writeTUIKeys(t, tty, "\r")
		if !waitForConfigDotIgnore(configPath, "dev", "claude", []string{"*", "!/settings.json"}, 8*time.Second) {
			t.Fatalf("claude ignore patterns were not fixed from dashboard reconcile; screen:\n%s", planScreen)
		}
		return capture.Text()
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while fixing dot ignore patterns; screen:\n%s", screen)
	}
}

func buildOmniBinary(t *testing.T) string {
	t.Helper()
	repo := integrationRepoRoot(t)
	bin := filepath.Join(t.TempDir(), "omni")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/omni")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build omni: %v\n%s", err, out)
	}
	return bin
}

func integrationRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("resolve repository root from working directory")
		}
		dir = parent
	}
}

func isolatedTUIEnv(home, cache string) []string {
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"OMNI_CACHE_DIR="+cache,
		"OMNI_HOSTNAME=testhost",
		"OMNI_TEST_ISOLATED=1",
		"TERM=xterm-256color",
	)
}

func initDotsRepo(t *testing.T, repo string, env []string) {
	t.Helper()
	runCommand(t, filepath.Dir(repo), env, "git", "init", repo)
	runCommand(t, repo, env, "git", "config", "user.email", "t@t.com")
	runCommand(t, repo, env, "git", "config", "user.name", "T")
	runCommand(t, repo, env, "git", "config", "commit.gpgsign", "false")
	runCommand(t, repo, env, "git", "config", "tag.gpgsign", "false")
	runCommand(t, repo, env, "git", "config", "core.hooksPath", "/dev/null")
	runCommand(t, repo, env, "git", "commit", "--allow-empty", "-m", "init")
}

func runCommand(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func runOmniCommand(t *testing.T, bin, dir string, env []string, args ...string) {
	t.Helper()
	_ = runOmniOutput(t, bin, dir, env, args...)
}

func runOmniOutput(t *testing.T, bin, dir string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("omni %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func seedTUIToolCache(t *testing.T, cache string, tools ...*database.ToolCache) {
	t.Helper()
	db, err := database.Open(filepath.Join(cache, "omni.db"))
	if err != nil {
		t.Fatalf("open TUI cache db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate TUI cache db: %v", err)
	}
	now := time.Now().UTC()
	for _, tool := range tools {
		if tool.Package == "" {
			tool.Package = tool.Name
		}
		if tool.LastChecked.IsZero() {
			tool.LastChecked = now
		}
		if err := db.Upsert(ctx, tool); err != nil {
			t.Fatalf("seed TUI tool cache %s/%s: %v", tool.Provider, tool.Name, err)
		}
	}
}

func runTUISmoke(t *testing.T, bin, dir string, env []string, args ...string) string {
	return runTUI(t, bin, dir, env, args, func(_ *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render expected startup screen")
		time.Sleep(200 * time.Millisecond)
		return capture.Text()
	})
}

func runTUI(t *testing.T, bin, dir string, env []string, args []string, interact func(*os.File, *lockedBuffer) string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 100})
	if err != nil {
		t.Fatalf("start TUI: %v", err)
	}
	defer tty.Close()

	var capture lockedBuffer
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&capture, tty)
		close(copyDone)
	}()

	screen := interact(tty, &capture)
	writeTUIKeys(t, tty, "q", "q")

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("TUI exit: %v\n%s", err, screen)
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("TUI did not quit after confirmation keys; screen:\n%s", capture.Text())
	}
	_ = tty.Close()
	<-copyDone

	return screen
}

func writeTUIKeys(t *testing.T, tty *os.File, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, err := tty.Write([]byte(key)); err != nil {
			t.Fatalf("write TUI key %q: %v", key, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForRequiredScreen(t *testing.T, capture *lockedBuffer, timeout time.Duration, ready func(string) bool, message string) string {
	t.Helper()
	screen, ok := waitForScreen(capture, timeout, ready)
	if !ok {
		t.Fatalf("%s; screen:\n%s", message, screen)
	}
	return screen
}

func waitForScreen(capture *lockedBuffer, timeout time.Duration, ready func(string) bool) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		text := capture.Text()
		if ready(text) {
			return text, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return capture.Text(), false
}

func waitForConfigDot(path, groupName, dotName string, ignored bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cfg, err := config.Load(path)
		if err == nil {
			for _, group := range cfg.Groups {
				if group == nil || group.Name != groupName {
					continue
				}
				for _, dot := range group.Dots {
					if dot.Name == dotName && dot.Ignored == ignored {
						return true
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func waitForConfigDotIgnore(path, groupName, dotName string, ignore []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cfg, err := config.Load(path)
		if err == nil {
			for _, group := range cfg.Groups {
				if group == nil || group.Name != groupName {
					continue
				}
				for _, dot := range group.Dots {
					if dot.Name == dotName && slices.Equal(dot.Ignore, ignore) {
						return true
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return truncateForFailure(stripANSI(b.buf.String()), 4000)
}

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[=><])`)

func stripANSI(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func truncateForFailure(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return fmt.Sprintf("%s\n... truncated %d bytes ...", s[:max], len(s)-max)
}
