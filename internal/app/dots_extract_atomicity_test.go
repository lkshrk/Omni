package app_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestDotsExtractInvalidDestinationGroupLeavesParentAndChildUnchanged(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "stow"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	parentTarget := filepath.Join(home, ".config", "nvim")
	parentSource := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	childTarget := filepath.Join(parentTarget, "lua", "plugins")
	childSource := filepath.Join(parentSource, "lua", "plugins")
	childFile := filepath.Join(childSource, "work.lua")
	if err := os.MkdirAll(childSource, 0o755); err != nil {
		t.Fatal(err)
	}
	wantBody := []byte("-- work")
	if err := os.WriteFile(childFile, wantBody, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(childTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	wantLink, err := filepath.Rel(filepath.Dir(childTarget), childSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wantLink, childTarget); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: parentTarget}}, home)
	wantConfig, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.DotsExtract(context.Background(), "nvim", "lua/plugins", app.DotsExtractOptions{Group: " "})
	if err == nil || !strings.Contains(err.Error(), "group name is required") {
		t.Fatalf("DotsExtract error = %v, want invalid destination group", err)
	}

	gotConfig, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(gotConfig, wantConfig) {
		t.Fatalf("config changed after failed extract\nwant: %s\n got: %s", wantConfig, gotConfig)
	}
	gotBody, readErr := os.ReadFile(childFile)
	if readErr != nil || !bytes.Equal(gotBody, wantBody) {
		t.Fatalf("parent source changed after failed extract: body=%q err=%v", gotBody, readErr)
	}
	info, statErr := os.Lstat(childTarget)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("parent target changed after failed extract: mode=%v err=%v", info, statErr)
	}
	gotLink, readErr := os.Readlink(childTarget)
	if readErr != nil || gotLink != wantLink {
		t.Fatalf("parent target link = %q err=%v, want %q", gotLink, readErr, wantLink)
	}
	childPackage := filepath.Join(dotsContentDir(repoDir), "nvim-plugins")
	if _, statErr := os.Lstat(childPackage); !os.IsNotExist(statErr) {
		t.Fatalf("child package created after failed extract: stat err=%v", statErr)
	}
}

func TestDotsExtractChildStowFailureRollsBackParentAndChild(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	stow := "#!/bin/sh\ncase \" $* \" in *\" nvim-plugins \"*) exit 23;; *) exit 0;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "stow"), []byte(stow), 0o755); err != nil {
		t.Fatal(err)
	}

	parentTarget := filepath.Join(home, ".config", "nvim")
	parentSource := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	childTarget := filepath.Join(parentTarget, "lua", "plugins")
	childSource := filepath.Join(parentSource, "lua", "plugins")
	childFile := filepath.Join(childSource, "work.lua")
	if err := os.MkdirAll(childSource, 0o755); err != nil {
		t.Fatal(err)
	}
	wantBody := []byte("-- work")
	if err := os.WriteFile(childFile, wantBody, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(childTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	wantLink, err := filepath.Rel(filepath.Dir(childTarget), childSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wantLink, childTarget); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: parentTarget}}, home)
	wantConfig, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.DotsExtract(context.Background(), "nvim", "lua/plugins", app.DotsExtractOptions{Group: "work"})
	if err == nil || !strings.Contains(err.Error(), "stow") {
		t.Fatalf("DotsExtract error = %v, want child stow failure", err)
	}
	gotConfig, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(gotConfig, wantConfig) {
		t.Fatalf("config changed after failed extract\nwant: %s\n got: %s", wantConfig, gotConfig)
	}

	loaded, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, group := range loaded.Groups {
		for _, entry := range group.Dots {
			if entry.Name == "nvim-plugins" {
				t.Fatalf("child config persisted after failed extract: %+v", entry)
			}
			if entry.Name == "nvim" && len(entry.Ignore) != 0 {
				t.Fatalf("parent ignore changed after failed extract: %v", entry.Ignore)
			}
		}
	}
	gotBody, readErr := os.ReadFile(childFile)
	if readErr != nil || !bytes.Equal(gotBody, wantBody) {
		t.Fatalf("parent source changed after failed extract: body=%q err=%v", gotBody, readErr)
	}
	info, statErr := os.Lstat(childTarget)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("parent target changed after failed extract: mode=%v err=%v", info, statErr)
	}
	gotLink, readErr := os.Readlink(childTarget)
	if readErr != nil || gotLink != wantLink {
		t.Fatalf("parent target link = %q err=%v, want %q", gotLink, readErr, wantLink)
	}
	childPackage := filepath.Join(dotsContentDir(repoDir), "nvim-plugins")
	if _, statErr := os.Lstat(childPackage); !os.IsNotExist(statErr) {
		t.Fatalf("child package persisted after failed extract: stat err=%v", statErr)
	}
}

func TestDotsExtractChildAutoPushFailureRollsBackChildConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	a, cfgDir, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoPush: true})
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(bin, "stow"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("config", "commit.gpgsign", "false")

	parentTarget := filepath.Join(home, ".config", "nvim")
	parentSource := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	childTarget := filepath.Join(parentTarget, "lua", "plugins")
	childSource := filepath.Join(parentSource, "lua", "plugins")
	childFile := filepath.Join(childSource, "work.lua")
	if err := os.MkdirAll(childSource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childFile, []byte("-- work"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(childTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	wantLink, err := filepath.Rel(filepath.Dir(childTarget), childSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wantLink, childTarget); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: parentTarget}}, home)

	_, err = a.DotsExtract(context.Background(), "nvim", "lua/plugins", app.DotsExtractOptions{Group: "work"})
	if err == nil || !strings.Contains(err.Error(), "git push") {
		t.Fatalf("DotsExtract error = %v, want child auto-push failure", err)
	}
	loaded, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, group := range loaded.Groups {
		for _, entry := range group.Dots {
			if entry.Name == "nvim-plugins" {
				t.Fatalf("child config persisted after failed extract: %+v", entry)
			}
		}
	}
}
