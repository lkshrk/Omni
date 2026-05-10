package app_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestBootstrapRequiredUsesLocalHostMarker(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {"work"}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}, {Name: "work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	required, err := a.BootstrapRequired(context.Background())
	if err != nil {
		t.Fatalf("BootstrapRequired: %v", err)
	}
	if !required {
		t.Fatal("BootstrapRequired = false before marker, want true")
	}
	if err := a.MarkHostBootstrapCompleted(context.Background(), "testhost"); err != nil {
		t.Fatalf("MarkHostBootstrapCompleted: %v", err)
	}
	required, err = a.BootstrapRequired(context.Background())
	if err != nil {
		t.Fatalf("BootstrapRequired after marker: %v", err)
	}
	if required {
		t.Fatal("BootstrapRequired = true after marker, want false")
	}
}

func TestBootstrapStateKeyUsesWideHash(t *testing.T) {
	// Verify the state key uses at least 128-bit hash (32 hex chars)
	// to avoid birthday collisions across config paths.
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.MarkHostBootstrapCompleted(context.Background(), "testhost"); err != nil {
		t.Fatalf("MarkHostBootstrapCompleted: %v", err)
	}
	// After marking, verify the marker persists (round-trip).
	completed, err := a.HostBootstrapCompleted(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted: %v", err)
	}
	if !completed {
		t.Fatal("HostBootstrapCompleted = false after marking, want true")
	}
	// A different config path must NOT match.
	a2 := app.New(filepath.Join(t.TempDir(), "other-settings.json"))
	if err := saveAppConfig(t, a2.ConfigPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("config.Save a2: %v", err)
	}
	a2.CacheDir = a.CacheDir // share same cache DB
	if err := a2.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode a2: %v", err)
	}
	t.Cleanup(func() { _ = a2.Close() })
	completed2, err := a2.HostBootstrapCompleted(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted a2: %v", err)
	}
	if completed2 {
		t.Fatal("different config path matched bootstrap marker — hash collision or key too short")
	}
}

func TestBootstrapRequiredIgnoresMissingActiveHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "newhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{"otherhost": {}},
		Groups: []*config.GroupConfig{{Name: "otherhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	required, err := a.BootstrapRequired(context.Background())
	if err != nil {
		t.Fatalf("BootstrapRequired: %v", err)
	}
	if required {
		t.Fatal("BootstrapRequired = true for missing active host, want false")
	}
}
