package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func newAgentsSyncTestApp(t *testing.T, settings config.Settings, opts ...func(*app.App)) *app.App {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{Version: config.CurrentVersion, Settings: settings}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
