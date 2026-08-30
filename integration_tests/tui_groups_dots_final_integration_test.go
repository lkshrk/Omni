//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIGroupsEditDotsPersistsAdditionalDotMembership(t *testing.T) {
	bin, root, cache, configPath, env := newTUIDotActionSandbox(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Hosts["testhost"] = append(cfg.Hosts["testhost"], "personal")
	cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: "personal"})
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Group Assignments", "personal", "work"), "TUI did not render dot groups")
		writeTUIKeys(t, term, "j", "j", "j")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("> personal", "edit dotfiles"), "TUI did not select the personal group")
		writeTUIKeys(t, term, "f")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Edit Dots: personal", "nvim"), "TUI did not open the group dot editor")
		writeTUIKeys(t, term, " ", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && dotInConfigGroup(cfg, "personal", "nvim") && !dotInConfigGroup(cfg, "work", "nvim")
		}, "TUI did not persist the additional dot membership")
	})
}
