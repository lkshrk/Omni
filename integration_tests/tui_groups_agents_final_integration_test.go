//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIGroupsEditToolsPersistsToolMembership(t *testing.T) {
	bin, root, cache, configPath, env := newTUIToolActionSandbox(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIGroupEditors(t, term)
		writeTUIKeys(t, term, "j", "j", "j", "j")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("> work", "edit tools"), "TUI did not select the work group")
		writeTUIKeys(t, term, "t")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Edit Tools: work", "omni-local"), "TUI did not open the group tool editor")
		writeTUIKeys(t, term, " ", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && toolInConfigGroup(cfg, "dev", "omni-local") && toolInConfigGroup(cfg, "work", "omni-local")
		}, "TUI did not persist the group tool membership")
	})
}

func openTUIGroupEditors(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
	writeTUIKeys(t, term, "\t", "\t", "\t", "\t")
	waitForRequiredScreen(t, term, 8*time.Second, screenHas("Group Assignments", "Groups"), "TUI did not render groups")
}
