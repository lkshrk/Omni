//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryGroupsEditDotsPersistsExactMembership(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	repo := filepath.Join(home, "dotfiles")
	initDotsRepo(t, repo, env)
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {"work"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "dots", "groups", "nvim", "--move", "work")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if finalGroupHasDot(cfg, "testhost", "nvim") || !finalGroupHasDot(cfg, "work", "nvim") {
		t.Fatalf("dot memberships = %#v", cfg.Groups)
	}
	dot := finalGroupDot(cfg, "work", "nvim")
	if dot == nil || dot.Path != filepath.Join(home, ".config", "nvim") {
		t.Fatalf("moved dot declaration = %#v", dot)
	}
}

func TestTUIAgentsUpdateInvokesSelectedPackage(t *testing.T) {
	root := newParitySandbox(t, t.TempDir())
	seedAgentsActionsParity(t, root)
	marker := filepath.Join(root.root, "apm-state", "row-updated")
	writeExecutable(t, filepath.Join(root.root, "bin", "apm"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo 'Agent Package Manager (APM) CLI version 0.29.0'
  exit 0
fi
printf '%s|%s\n' "$PWD" "$*" >> "${OMNI_TEST_APM_LOG:?}"
case "$*" in
  outdated*) echo '[✓] All dependencies are up-to-date' ;;
  'update -g --yes https://github.com/acme/tool') touch "${OMNI_TEST_APM_ROW_MARKER:?}"; echo '[✓] updated selected package' ;;
  *) echo "delegated: $*" ;;
esac
`)
	root.env = append(root.env, "OMNI_TEST_APM_ROW_MARKER="+marker)
	bin := buildOmniBinary(t)
	runAgentsActionsTUI(t, bin, root, func(term *vttest.Terminal) {
		sendAgentsActionsKeyUntil(t, term, "j", func(text string) bool {
			return strings.Contains(text, ">") && strings.Contains(text, "tool")
		}, "TUI did not select the APM package")
		sendAgentsActionsKeyUntil(t, term, "u", func(string) bool {
			_, err := os.Stat(marker)
			return err == nil
		}, "TUI did not update the selected APM package")
	}, func(*paritySandbox) bool {
		_, err := os.Stat(marker)
		return err == nil
	})
	assertFileContains(t, filepath.Join(root.root, "apm.log"), "|update -g --yes https://github.com/acme/tool")
}

func finalGroupHasDot(cfg *config.RootConfig, groupName, dotName string) bool {
	return finalGroupDot(cfg, groupName, dotName) != nil
}

func finalGroupDot(cfg *config.RootConfig, groupName, dotName string) *config.DotEntry {
	for _, group := range cfg.Groups {
		if group == nil || group.BaseName() != groupName {
			continue
		}
		for i := range group.Dots {
			if group.Dots[i].Name == dotName {
				return &group.Dots[i]
			}
		}
	}
	return nil
}
