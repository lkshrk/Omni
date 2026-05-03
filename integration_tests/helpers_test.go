//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/brew"
	"github.com/lkshrk/omni/internal/provider/node"
	"github.com/lkshrk/omni/internal/provider/pip"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// errNotExist simulates a "binary not found" error from the shell.
var errNotExist = errors.New("exec: no such file or directory")

// testApp builds a complete syncer stack with an injected executor.
type testApp struct {
	ConfigPath string
	DB         *database.DB
	Registry   *provider.Registry
	Syncer     interface {
		Sync(ctx context.Context, cfg *config.Config, opts gosync.SyncOptions) (*gosync.SyncResult, error)
	}
}

// newTestStackWithMock creates a fully-wired stack using the provided executor.
func newTestStackWithMock(t *testing.T, exec executor.Executor) *testApp {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	dbPath := filepath.Join(dir, "omni.db")

	reg := provider.NewRegistry()
	reg.Register(brew.New(exec))
	reg.Register(node.New(exec, "npm"))
	reg.Register(pip.New(exec))

	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	syncer := gosync.New(reg, db)
	t.Cleanup(func() { _ = db.Close() })

	return &testApp{
		ConfigPath: cfgPath,
		DB:         db,
		Registry:   reg,
		Syncer:     syncer,
	}
}

// newTestStack creates a testApp using a sequential MockExecutor.
// Use newTestStackWithMock + buildBrewStatefulMock for concurrent-safe tests.
func newTestStack(t *testing.T, responses ...executor.MockCall) *testApp {
	t.Helper()
	mock := &executor.MockExecutor{Responses: responses}
	return newTestStackWithMock(t, mock)
}

// writeConfig writes a Config to the test config file.
func (ta *testApp) writeConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	root := &config.RootConfig{
		Settings: cfg.Settings,
		Groups:   []*config.GroupConfig{{Tools: cfg.Tools}},
	}
	if err := config.Save(ta.ConfigPath, root); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}

// simpleConfig creates a config from a slice of (name, provider) pairs.
func simpleConfig(tools ...string) *config.Config {
	var entries []config.ToolEntry
	for i := 0; i+1 < len(tools); i += 2 {
		entries = append(entries, config.ToolEntry{
			Name:     tools[i],
			Provider: tools[i+1],
			Package:  tools[i],
		})
	}
	return &config.Config{Tools: entries}
}

// --- Stateful brew mock --------------------------------------------------

// brewStatefulMock is an executor.Executor that simulates brew with state.
// It tracks which packages are installed and returns correct responses for
// `brew list --versions <pkg>`, `brew install <pkg>`, `brew uninstall <pkg>`.
type brewStatefulMock struct {
	mu        sync.Mutex
	installed map[string]string // pkg → version
}

// buildBrewStatefulMock creates a stateful brew executor.
// installVersions: packages to install on sync (not yet installed), pkg → expected version
// preInstalled:    packages already installed, pkg → version
func buildBrewStatefulMock(installVersions, preInstalled map[string]string) executor.Executor {
	installed := make(map[string]string)
	for pkg, ver := range preInstalled {
		installed[pkg] = ver
	}
	// Store installVersions separately so we can set them after install.
	m := &brewStatefulMock{installed: installed}
	return &brewStatefulInstaller{
		mock:            m,
		installVersions: installVersions,
	}
}

// brewStatefulInstaller wraps brewStatefulMock and handles install/uninstall.
type brewStatefulInstaller struct {
	mock            *brewStatefulMock
	installVersions map[string]string
}

func (b *brewStatefulInstaller) Run(_ context.Context, name string, args ...string) (string, string, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}

	switch {
	case key == "brew --version":
		return "Homebrew 4.0.0", "", nil

	case strings.HasPrefix(key, "brew list --versions ") && len(args) == 3:
		// args: ["list", "--versions", "<pkg>"]
		pkg := args[2]
		b.mock.mu.Lock()
		ver, ok := b.mock.installed[pkg]
		b.mock.mu.Unlock()
		if !ok {
			return "", "", os.ErrProcessDone // not installed
		}
		return fmt.Sprintf("%s %s\n", pkg, ver), "", nil

	case strings.HasPrefix(key, "brew list --versions") && len(args) == 2:
		// brew list --versions (no package = list all)
		b.mock.mu.Lock()
		var lines []string
		for pkg, ver := range b.mock.installed {
			lines = append(lines, fmt.Sprintf("%s %s", pkg, ver))
		}
		b.mock.mu.Unlock()
		return strings.Join(lines, "\n"), "", nil

	case strings.HasPrefix(key, "brew install ") && len(args) == 2:
		pkg := args[1]
		b.mock.mu.Lock()
		if ver, ok := b.installVersions[pkg]; ok {
			b.mock.installed[pkg] = ver
		}
		b.mock.mu.Unlock()
		return "==> Installing " + pkg, "", nil

	case strings.HasPrefix(key, "brew uninstall ") && len(args) == 2:
		pkg := args[1]
		b.mock.mu.Lock()
		delete(b.mock.installed, pkg)
		b.mock.mu.Unlock()
		return "", "", nil

	case strings.HasPrefix(key, "brew upgrade "):
		return "", "", nil
	}

	return "", "", nil
}
