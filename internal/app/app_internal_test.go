package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestTapFromPackage(t *testing.T) {
	tests := []struct {
		pkg  string
		want string
	}{
		{"hashicorp/tap/terraform", "hashicorp/tap"},
		{"homebrew-cask/font-fira-code/something", "homebrew-cask/font-fira-code"},
		{"git", ""},
		{"owner/repo", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := tapFromPackage(tt.pkg)
		if got != tt.want {
			t.Errorf("tapFromPackage(%q) = %q, want %q", tt.pkg, got, tt.want)
		}
	}
}

func TestShortHostname(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"macbook.corp.local", "macbook"},
		{"macbook", "macbook"},
		{"a.b.c.d", "a"},
		{"", "localhost"},
	}
	for _, tt := range tests {
		got := shortHostname(tt.input)
		if got != tt.want {
			t.Errorf("shortHostname(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMachineGroupName(t *testing.T) {
	if got := machineGroupName("macbook.corp.local"); got != "macbook" {
		t.Errorf("machineGroupName = %q, want macbook", got)
	}
}

func TestBackupConfigOnLaunch_CopiesExistingFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	body := []byte(`{"$schema":"x","settings":{}}` + "\n")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: src}
	a.backupConfigOnLaunch()
	got, err := os.ReadFile(src + settingsBackupSuffix)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("backup mismatch:\n got %q\nwant %q", got, body)
	}
}

func TestBackupConfigOnLaunch_MissingSourceIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	a := &App{ConfigPath: src}
	a.backupConfigOnLaunch()
	if _, err := os.Stat(src + settingsBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup file when source missing, got err=%v", err)
	}
}

func TestBackupConfigOnLaunch_OverwritesPriorBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	bak := src + settingsBackupSuffix
	if err := os.WriteFile(bak, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := []byte(`{"settings":{}}`)
	if err := os.WriteFile(src, fresh, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: src}
	a.backupConfigOnLaunch()
	got, err := os.ReadFile(bak)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(fresh) {
		t.Fatalf("backup not overwritten: got %q, want %q", got, fresh)
	}
}

func TestResolveRepoPath_ExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo, err := resolveRepoPath("$HOME/dotfiles")
	if err != nil {
		t.Fatalf("resolveRepoPath: %v", err)
	}

	if repo != filepath.Join(home, "dotfiles") {
		t.Fatalf("resolveRepoPath = %q, want %q", repo, filepath.Join(home, "dotfiles"))
	}
}

func TestNormalisePath_ExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := normalisePath("$HOME/.config/zsh")
	want := filepath.ToSlash(filepath.Join("~", ".config", "zsh"))
	if got != want {
		t.Fatalf("normalisePath($HOME/.config/zsh) = %q, want %q", got, want)
	}
}

func TestExpandAndStat_ExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoDir := filepath.Join(home, "dotfiles")
	if err := os.Mkdir(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := expandAndStat("$HOME/dotfiles")
	if err != nil {
		t.Fatalf("expandAndStat: %v", err)
	}
	if got != filepath.Clean(repoDir) {
		t.Fatalf("expandAndStat = %q, want %q", got, filepath.Clean(repoDir))
	}
}

func TestEffectiveHostGroupsIncludesHostGroupFirst(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example.com")
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
			"slack":   {Provider: "system", InstallWith: "brew"},
			"fd":      {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "shared", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			{Name: "work", Tools: []config.ToolEntry{{Name: "slack"}}},
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fd"}}},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}

	effective, active, ok := effectiveHostGroups(cfg, cfg.Groups, "testhost.example.com")
	if !ok {
		t.Fatal("effectiveHostGroups ok=false")
	}
	if got := groupBaseNames(effective); len(got) != 2 || got[0] != "testhost" || got[1] != "work" {
		t.Fatalf("effective groups = %v, want [testhost work]", got)
	}
	if got := groupBaseNames(active); len(got) != 2 || got[0] != "testhost" || got[1] != "work" {
		t.Fatalf("active groups = %v, want [testhost work]", got)
	}
}

func TestDotsHistoryIDUniqueness(t *testing.T) {
	// Generate IDs rapidly — the atomic counter suffix must prevent collisions
	// even when time.Now().UnixNano() returns the same value.
	const n = 100
	ids := make(map[string]struct{}, n)
	for range n {
		id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), dotsHistoryIDCounter.Add(1))
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate dots history ID: %s", id)
		}
		ids[id] = struct{}{}
	}
}
