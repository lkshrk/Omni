//go:build integration

package integration_test

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIDotsEditGroupsPersistsHostMembership(t *testing.T) {
	bin, root, cache, configPath, _, _, env := newTUIDotActionSandbox(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIDotActionRow(t, term)
		writeTUIKeys(t, term, "g")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Change Groups: nvim", "testhost", "work"), "TUI did not open dot group membership")
		writeTUIKeys(t, term, " ", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && dotInConfigGroup(cfg, "testhost", "nvim") && dotInConfigGroup(cfg, "work", "nvim")
		}, "TUI did not persist the dot host-group membership")
	})
}

func TestTUIDotsDisableKeepsLocalFileAndPersistsSetting(t *testing.T) {
	bin, root, cache, configPath, target, _, env := newTUIDotActionSandbox(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIDotsSyncSetting(t, term)
		writeTUIKeys(t, term, "\r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("disable dotfile sync", "keep local?"), "TUI did not open disable-dots confirmation")
		writeTUIKeys(t, term, "y")
		return waitForRequiredScreen(t, term, 10*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			disabled := cfg.HostSettings["testhost"].DotsDisabled
			return disabled != nil && *disabled && regularFileHasContent(target, "repo version\n")
		}, "TUI did not disable dots safely")
	})
}

func TestTUIDotsEnableRestoresManagedLinkAndPersistsSetting(t *testing.T) {
	bin, root, cache, configPath, target, source, env := newTUIDotActionSandbox(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.HostSettings = map[string]config.Settings{"testhost": {DotsDisabled: config.BoolPtr(true)}}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIDotsSyncSetting(t, term)
		writeTUIKeys(t, term, "\r")
		return waitForRequiredScreen(t, term, 10*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			disabled := cfg.HostSettings["testhost"].DotsDisabled
			return disabled != nil && !*disabled && remainingDotsSymlinkContent(target, source, "repo version\n")
		}, "TUI did not re-enable dots")
	})
}

func openTUIDotsSyncSetting(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
	writeTUIKeys(t, term, "\t", "\t", "\t", "\t", "\t")
	waitForRequiredScreen(t, term, 8*time.Second, screenHas("Import Installed Tools", "Dotfile Sync"), "TUI did not render dot settings")
	revealTUISettingsCursor(t, term)
	writeTUIKeys(t, term, "j", "j", "j")
	waitForRequiredScreen(t, term, 3*time.Second, screenHas("> Dotfile Sync"), "TUI did not select dotfile sync")
}

func remainingDotsSymlinkContent(target, source, want string) bool {
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	targetContent, targetErr := os.ReadFile(target)
	sourceContent, sourceErr := os.ReadFile(source)
	return targetErr == nil && sourceErr == nil && string(targetContent) == want && string(sourceContent) == want
}
