package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDefaultStateDirPrecedence(t *testing.T) {
	t.Setenv("OMNI_STATE_DIR", filepath.Join(t.TempDir(), "explicit"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "xdg"))
	got, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "explicit" {
		t.Fatalf("got %s", got)
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
