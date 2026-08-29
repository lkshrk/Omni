package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCompletionCandidatesFilterAppState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	node := &stubProvider{name: "node", available: true}
	a, cfgPath := newImportApp(t, brew, pip, node)
	repoDir := t.TempDir()
	for _, name := range []string{"zsh", "vim", "git"} {
		if err := os.MkdirAll(filepath.Join(repoDir, "dotfiles", name), 0o755); err != nil {
			t.Fatalf("mkdir dot package %s: %v", name, err)
		}
	}
	cfg := &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Hosts: map[string][]string{
			"testhost":     {},
			"macbook-pro":  {},
			"macbook-air":  {},
			"linux-server": {},
		},
		Groups: []*config.GroupConfig{
			{
				Name:    "testhost",
				Special: "host",
				Dots: []config.DotEntry{
					{Name: "zsh", Path: filepath.Join(home, ".zshrc")},
					{Name: "vim", Path: filepath.Join(home, ".vimrc")},
					{Name: "git", Path: filepath.Join(home, ".gitconfig")},
				},
			},
			{Name: "macbook-pro", Special: "host"},
			{Name: "macbook-air", Special: "host"},
			{Name: "linux-server", Special: "host"},
			{Name: "work"},
			{Name: "personal"},
			{Name: "shared"},
		},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	for _, name := range []string{"ripgrep", "redis", "node", "nginx"} {
		if err := a.DB().Upsert(ctx, &database.ToolCache{Name: name, Provider: "brew", Package: name}); err != nil {
			t.Fatalf("Upsert(%s): %v", name, err)
		}
	}

	toolCandidates, err := a.ToolNameCandidates(ctx, "r")
	assertCandidateSet(t, "tools", mustCandidates(t, toolCandidates, err), []string{"redis", "ripgrep"})
	groupCandidates, err := a.GroupNameCandidates(ctx, "p")
	assertCandidateSet(t, "groups", mustCandidates(t, groupCandidates, err), []string{"personal"})
	hostCandidates, err := a.HostNameCandidates("macbook")
	assertCandidateSet(t, "hosts", mustCandidates(t, hostCandidates, err), []string{"macbook-air", "macbook-pro"})
	providerCandidates, err := a.ProviderNameCandidates(ctx, "b")
	assertCandidateSet(t, "providers", mustCandidates(t, providerCandidates, err), []string{"brew"})
	dotCandidates, err := a.DotNameCandidates("v")
	assertCandidateSet(t, "dots", mustCandidates(t, dotCandidates, err), []string{"vim"})
	assertCandidateSet(t, "settings", app.SettingKeyCandidates("dots_git."), []string{"dots_git.auto_commit", "dots_git.auto_push"})
	settings, err := a.QuerySettings("")
	if err != nil {
		t.Fatalf("QuerySettings: %v", err)
	}
	assertCandidateSet(t, "all settings", app.SettingKeyCandidates(""), mapKeys(settings))
}

func TestSettingKeysExposeCanonicalOrder(t *testing.T) {
	t.Parallel()
	want := []string{
		"auto_import",
		"update_quarantine",
		"dots_repo",
		"dots_disabled",
		"dots_git.auto_commit",
		"dots_git.auto_push",
		"disabled_providers",
		"provider_priority",
		"provider_update_quarantine",
	}
	got := app.SettingKeys()
	if !slices.Equal(got, want) {
		t.Fatalf("SettingKeys = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if app.SettingKeys()[0] == "mutated" {
		t.Fatal("SettingKeys returned mutable package state")
	}
}

func mustCandidates(t *testing.T, names []string, err error) []string {
	t.Helper()
	if err != nil {
		t.Fatalf("completion candidates: %v", err)
	}
	return names
}

func assertCandidateSet(t *testing.T, name string, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s candidates = %v, want %v", name, got, want)
	}
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
