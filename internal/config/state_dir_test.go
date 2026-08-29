package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDefaultStateDirPrecedence(t *testing.T) {
	t.Setenv("OMNI_STATE_DIR", filepath.Join(t.TempDir(), "explicit"))
	xdg := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdg, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "explicit" {
		t.Fatalf("got %s", got)
	}
}

func TestDefaultStateDirRejectsPathsOutsideTestSandbox(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("explicit", func(t *testing.T) {
		t.Setenv("OMNI_STATE_DIR", outside)
		if _, err := DefaultStateDir(); err == nil || !strings.Contains(err.Error(), "OMNI_STATE_DIR") {
			t.Fatalf("DefaultStateDir outside OMNI_STATE_DIR error = %v", err)
		}
	})

	t.Run("default", func(t *testing.T) {
		t.Setenv("OMNI_STATE_DIR", "")
		t.Setenv("XDG_STATE_HOME", outside)
		if _, err := DefaultStateDir(); err == nil || !strings.Contains(err.Error(), "XDG_STATE_HOME") {
			t.Fatalf("DefaultStateDir outside XDG_STATE_HOME error = %v", err)
		}
	})
}

func TestDefaultStateDirRejectsFinalSymlinkEscape(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMNI_STATE_DIR", link)

	if _, err := DefaultStateDir(); err == nil || !strings.Contains(err.Error(), "OMNI_STATE_DIR") {
		t.Fatalf("DefaultStateDir symlink escape error = %v", err)
	}
}

func TestWriteConfigLocksEntireReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := Save(path, &RootConfig{Version: CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, provider := range []string{"one", "two"} {
		provider := provider
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- WriteConfig(path, func() (*RootConfig, error) { return Load(path) }, nil, func(cfg *RootConfig) error {
				cfg.Settings.DisabledProviders = append(cfg.Settings.DisabledProviders, provider)
				return nil
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Settings.DisabledProviders) != 2 {
		t.Fatalf("lost update: %v", cfg.Settings.DisabledProviders)
	}
}

func TestConfigLockRootFindsMainAcrossNestedIncludes(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "settings.d", "nested", "agents.json")
	if err := os.WriteFile(main, []byte(`{"version":24}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configLockRoot(fragment); got != dir {
		t.Fatalf("lock root=%s want %s", got, dir)
	}
}
