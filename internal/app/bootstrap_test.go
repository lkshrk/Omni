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
