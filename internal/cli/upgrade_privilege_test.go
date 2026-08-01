package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type privilegedUpgradeProvider struct {
	cliStubProvider
	upgrades int
}

func (p *privilegedUpgradeProvider) Upgrade(context.Context, provider.Tool) error {
	p.upgrades++
	return nil
}

func (p *privilegedUpgradeProvider) PrivilegePlan(context.Context, provider.PrivilegeAction, provider.Tool) (provider.PrivilegePlan, error) {
	return provider.PrivilegePlan{Requirement: provider.PrivilegeMaybe, Reason: "brew cask uninstall delete artifact requires sudo"}, nil
}

func (p *privilegedUpgradeProvider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	if action != provider.PrivilegeActionUpgrade {
		return "", nil, false
	}
	return "brew", []string{"upgrade", "--cask", "--greedy", "--no-ask", tool.EffectivePackage()}, true
}

func newPrivilegedUpgradeCLIApp(t *testing.T, outdated bool) (*app.App, *privilegedUpgradeProvider) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	providerStub := &privilegedUpgradeProvider{cliStubProvider: cliStubProvider{
		name:      "brew",
		installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "obs", Provider: "brew", Package: "obs"}, Version: "31.1.0"}},
	}}
	a := app.New(cfgPath)
	a.CacheDir = t.TempDir()
	if err := a.InitTestMode(context.Background(), providerStub); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name: "obs", Provider: "brew", Package: "obs", Installed: true, Tracked: true,
	}); err != nil {
		t.Fatalf("seed obs: %v", err)
	}
	if outdated {
		if err := a.DB().UpdateOutdated(context.Background(), "obs", "brew", "obs", true, "31.1.0"); err != nil {
			t.Fatalf("mark obs outdated: %v", err)
		}
		if err := a.DB().UpsertUpdateMetadata(context.Background(), database.UpdateMetadata{
			Provider: "brew", Package: "obs", Version: "31.1.0", AvailableAt: time.Now(), CheckedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed update metadata: %v", err)
		}
	}
	return a, providerStub
}

func TestUpgrade_PrivilegedCaskRunsInteractively(t *testing.T) {
	a, providerStub := newPrivilegedUpgradeCLIApp(t, false)
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	stdinFile := filepath.Join(t.TempDir(), "stdin")
	brew := filepath.Join(binDir, "brew")
	if err := os.WriteFile(brew, []byte("#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$@\" > \"$OMNI_TEST_ARGS\"\nprintf '%s' \"$input\" > \"$OMNI_TEST_STDIN\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMNI_TEST_ARGS", argsFile)
	t.Setenv("OMNI_TEST_STDIN", stdinFile)
	originalTerminal := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = originalTerminal })

	cmd := newUpgradeCmd(&rootState{app: a})
	cmd.SetIn(strings.NewReader("approval\n"))
	cmd.SetArgs([]string{"obs"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade obs: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read brew args: %v", err)
	}
	if want := "upgrade\n--cask\n--greedy\n--no-ask\nobs\n"; string(got) != want {
		t.Fatalf("brew args = %q, want %q", got, want)
	}
	if got, err := os.ReadFile(stdinFile); err != nil || string(got) != "approval" {
		t.Fatalf("brew stdin = %q, %v", got, err)
	}
	if providerStub.upgrades != 0 {
		t.Fatalf("capture-only provider upgrade called %d times", providerStub.upgrades)
	}
}

func TestUpgrade_PrivilegedCaskNonTTYReturnsManualCommand(t *testing.T) {
	a, providerStub := newPrivilegedUpgradeCLIApp(t, false)
	originalTerminal := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = originalTerminal })

	cmd := newUpgradeCmd(&rootState{app: a})
	cmd.SetArgs([]string{"obs"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "admin approval required; run interactively: brew upgrade --cask --greedy --no-ask obs") {
		t.Fatalf("error = %v", err)
	}
	if providerStub.upgrades != 0 {
		t.Fatalf("capture-only provider upgrade called %d times", providerStub.upgrades)
	}
}

func TestUpgrade_PrivilegedCaskStillHonorsIgnore(t *testing.T) {
	a, providerStub := newPrivilegedUpgradeCLIApp(t, false)
	withConfig(t, a.ConfigPath, &config.RootConfig{Ignore: config.GlobalIgnore{Tools: []string{"obs"}}})

	cmd := newUpgradeCmd(&rootState{app: a})
	cmd.SetArgs([]string{"obs"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `tool "obs" is ignored`) {
		t.Fatalf("error = %v", err)
	}
	if providerStub.upgrades != 0 {
		t.Fatalf("capture-only provider upgrade called %d times", providerStub.upgrades)
	}
}

func TestUpgradeAll_SkipsPrivilegedCask(t *testing.T) {
	a, providerStub := newPrivilegedUpgradeCLIApp(t, true)
	cmd := newUpgradeCmd(&rootState{app: a})
	cmd.SetArgs([]string{"--all"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "admin approval required; run interactively: brew upgrade --cask --greedy --no-ask obs") {
		t.Fatalf("error = %v", err)
	}
	if providerStub.upgrades != 0 {
		t.Fatalf("capture-only provider upgrade called %d times", providerStub.upgrades)
	}
}
