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

var errNotExist = errors.New("exec: no such file or directory")

type testApp struct {
	ConfigPath string
	DB         *database.DB
	Registry   *provider.Registry
	Syncer     interface {
		Sync(ctx context.Context, cfg *config.Config, opts gosync.SyncOptions) (*gosync.SyncResult, error)
	}
}

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

// Use newTestStackWithMock + buildBrewStatefulMock for concurrent-safe tests.
func newTestStack(t *testing.T, responses ...executor.MockCall) *testApp {
	t.Helper()
	mock := &executor.MockExecutor{Responses: responses}
	return newTestStackWithMock(t, mock)
}

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

type brewStatefulMock struct {
	mu        sync.Mutex
	installed map[string]string // pkg → version
}

func buildBrewStatefulMock(installVersions, preInstalled map[string]string) executor.Executor {
	installed := make(map[string]string)
	for pkg, ver := range preInstalled {
		installed[pkg] = ver
	}
	m := &brewStatefulMock{installed: installed}
	return &brewStatefulInstaller{
		mock:            m,
		installVersions: installVersions,
	}
}

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

	case key == "brew info --json=v2 --installed":
		b.mock.mu.Lock()
		installed := make(map[string]string, len(b.mock.installed))
		for pkg, ver := range b.mock.installed {
			installed[pkg] = ver
		}
		b.mock.mu.Unlock()
		return brewInfoJSON(installed), "", nil

	case key == "brew leaves --installed-on-request":
		b.mock.mu.Lock()
		var names []string
		for pkg := range b.mock.installed {
			names = append(names, pkg)
		}
		b.mock.mu.Unlock()
		return strings.Join(names, "\n"), "", nil

	case key == "brew list --cask":
		return "", "", nil

	case strings.HasPrefix(key, "brew list --versions --cask"):
		return "", "", nil

	case strings.HasPrefix(key, "brew list --versions") && len(args) >= 2:
		b.mock.mu.Lock()
		var lines []string
		requested := args[2:]
		if len(requested) == 0 {
			for pkg := range b.mock.installed {
				requested = append(requested, pkg)
			}
		}
		for _, pkg := range requested {
			if len(requested) > 0 && pkg == "--cask" {
				continue
			}
			if ver, ok := b.mock.installed[pkg]; ok {
				lines = append(lines, fmt.Sprintf("%s %s", pkg, ver))
			}
		}
		b.mock.mu.Unlock()
		if len(lines) == 0 && len(args) == 3 {
			return "", "", os.ErrProcessDone // not installed
		}
		return strings.Join(lines, "\n"), "", nil

	case key == "brew update":
		return "", "", nil

	case key == "brew outdated --json=v2":
		return `{"formulae":[],"casks":[]}`, "", nil

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
