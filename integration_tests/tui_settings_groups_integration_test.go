//go:build integration

package integration_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAssignsHostGroupAndPersistsSetting(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)

	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"system", "node", "python", "pip"},
		},
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")

		writeTUIKeys(t, term, "\t", "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Group Assignments") && strings.Contains(text, "testhost")
		}, "TUI did not render group assignments")
		writeTUIKeys(t, term, "g")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "Edit Groups: testhost") && strings.Contains(text, "work")
		}, "TUI did not open host group editor")
		writeTUIKeys(t, term, "j", " ", "\r")
		waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && slices.Contains(cfg.Hosts["testhost"], "work")
		}, "TUI did not persist host group assignment")

		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 5*time.Second, func(text string) bool {
			return strings.Contains(text, "Import Installed Tools")
		}, "TUI did not render settings")
		writeTUIKeys(t, term, " ")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && cfg.Settings.AutoImport
		}, "TUI did not persist auto-import setting")
	})

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !slices.Contains(cfg.Hosts["testhost"], "work") || !cfg.Settings.AutoImport {
		t.Fatalf("persisted host/settings state = groups:%v auto_import:%v", cfg.Hosts["testhost"], cfg.Settings.AutoImport)
	}
}
