package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillsInstallLocalSelectedAndMissingSelectionRollsBack(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	writeTestSkill(t, filepath.Join(source, "skills", "two"), "two", "second")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: source, Skills: []string{"one"}}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(home, ".test", "skills", "one")
	if target, err := filepath.EvalSymlinks(link); err != nil {
		t.Fatalf("installed link: %v", err)
	} else if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".test", "skills", "two")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill installed: %v", err)
	}

	before, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source, Skills: []string{"missing"}}, []string{"test"}); err == nil {
		t.Fatal("missing selected skill install succeeded")
	}
	after, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed update changed the previous installation")
	}
}

func TestSkillsInstallRejectsMalformedManifest(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	dir := filepath.Join(source, "skills", "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# missing frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err == nil {
		t.Fatal("malformed manifest install succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, ".test", "skills", "broken")); !os.IsNotExist(err) {
		t.Fatalf("malformed skill was linked: %v", err)
	}
}

func TestSkillsInstallSkipsMalformedSiblingSkill(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "good"), "good", "usable")
	broken := filepath.Join(source, "skills", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "SKILL.md"), []byte("# missing frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")

	warnings, err := svc.Install(
		context.Background(),
		config.SkillPackage{Source: source, Skills: []string{"good"}},
		[]string{"test"},
	)
	if err != nil {
		t.Fatalf("installing alongside a malformed skill: %v", err)
	}
	assertFileContains(t, filepath.Join(home, ".test", "skills", "good", "SKILL.md"), "usable")
	if !containsSubstring(warnings, "skipping skill directory skills/broken") {
		t.Fatalf("warnings = %v, want the skipped directory reported", warnings)
	}

	if _, err := svc.Install(
		context.Background(),
		config.SkillPackage{Source: source, Skills: []string{"broken"}},
		[]string{"test"},
	); err == nil {
		t.Fatal("selecting the malformed skill succeeded")
	}
}

func TestDiscoverSkillsRejectsDuplicateNames(t *testing.T) {
	t.Parallel()
	for name, container := range map[string]string{
		"container walk": "skills",
		"fallback walk":  "packages",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTestSkill(t, filepath.Join(root, container, "first"), "dup", "first")
			writeTestSkill(t, filepath.Join(root, container, "second"), "dup", "second")
			if _, _, err := discoverSkills(root); err == nil ||
				!strings.Contains(err.Error(), "duplicate discovered skill") {
				t.Fatalf("error = %v, want duplicate discovered skill", err)
			}
		})
	}
}

func TestSkillsInstallLocalGitRefAndUpdate(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	writeTestSkill(t, filepath.Join(repo, "skills", "one"), "one", "version one")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "v1")
	runGit(t, repo, "tag", "v1")
	if err := os.WriteFile(
		filepath.Join(repo, "skills", "one", "SKILL.md"),
		[]byte("---\nname: one\ndescription: test\n---\n\nversion two\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "v2")
	runGit(t, repo, "tag", "v2")

	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: repo, Ref: "v1"}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".test", "skills", "one", "SKILL.md")
	assertFileContains(t, path, "version one")
	pkg.Ref = "v2"
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, path, "version two")
}

func TestSkillsRejectsUnsafeConfiguredSourceAndRef(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")

	for _, pkg := range []config.SkillPackage{
		{Source: "ext::sh -c touch /tmp/omni-skills-pwned"},
		{Source: "owner/repo", Ref: "--upload-pack=touch /tmp/omni-skills-pwned"},
	} {
		if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err == nil {
			t.Fatalf("unsafe package accepted: %+v", pkg)
		}
	}
}

func TestSkillsInstallPrunesOwnedLinksFromDeselectedTargets(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{
		{ID: "a", configDir: ".a"},
		{ID: "b", configDir: ".b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), pkg, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".a", "skills", "one")); !os.IsNotExist(err) {
		t.Fatalf("deselected target link remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".b", "skills", "one")); err != nil {
		t.Fatalf("selected target link removed: %v", err)
	}
}

func TestSkillsInventoryRequiresEveryCanonicalSkill(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	writeTestSkill(t, filepath.Join(source, "skills", "two"), "two", "second")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, ".test", "skills", "two")); err != nil {
		t.Fatal(err)
	}

	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.PerTarget["test"] || inventory.Installed {
		t.Fatalf("partial target reported installed: %+v", inventory)
	}
}

func TestSkillsInstallRejectsForeignPackageSymlinkCollision(t *testing.T) {
	home := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	writeTestSkill(t, filepath.Join(first, "skills", "same"), "same", "identical")
	writeTestSkill(t, filepath.Join(second, "skills", "same"), "same", "identical")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: first}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".test", "skills", "same")
	before, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: second}, []string{"test"}); err == nil {
		t.Fatal("foreign package symlink collision succeeded")
	}
	after, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("collision changed link from %q to %q", before, after)
	}
}

func TestSkillsResolveDriftReassignsManagedPackageSymlinkCollision(t *testing.T) {
	home := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	writeTestSkill(t, filepath.Join(first, "skills", "same"), "same", "first")
	writeTestSkill(t, filepath.Join(second, "skills", "same"), "same", "second")
	registry, err := newRegistry([]Target{
		{ID: "test", configDir: ".test"},
		{ID: "seed", configDir: ".seed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: first}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: second}, []string{"seed"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".test", "skills", "same")
	before, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = svc.ResolveDrift(
		context.Background(),
		config.SkillPackage{Source: second},
		[]string{"test", "seed"},
		SkillDriftSelection{Targets: []string{"test"}},
	); err != nil {
		t.Fatalf("ResolveDrift: %v", err)
	}
	after, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if before == after || !pathWithin(svc.packageDir(second), after) {
		t.Fatalf("resolve changed link from %q to %q, want second package", before, after)
	}
	firstInventory, err := svc.Inventory(context.Background(), first, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if firstInventory.PerTargetDrifted["test"] || !firstInventory.PerTargetShadowed["test"] {
		t.Fatalf("first inventory = %+v, want shadowed without drift", firstInventory)
	}
	secondInventory, err := svc.Inventory(context.Background(), second, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !secondInventory.PerTarget["test"] || secondInventory.PerTargetShadowed["test"] {
		t.Fatalf("second inventory = %+v, want installed", secondInventory)
	}
}

func TestSkillsInstallRequiresExplicitTargets(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, nil); err == nil {
		t.Fatal("install without explicit targets succeeded")
	}
}

func TestSkillsInventoryAllowsExplicitEmptyTargets(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	inventory, err := svc.Inventory(context.Background(), "owner/repo", []string{})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Installed || len(inventory.PerTarget) != 0 {
		t.Fatalf("empty-target inventory = %+v", inventory)
	}
}

func TestSplitGitRemoteSubpath(t *testing.T) {
	t.Parallel()
	remote, subpath := splitGitRemoteSubpath("ssh://git@example.com/team/skills.git/catalog/review")
	if remote != "ssh://git@example.com/team/skills.git" || subpath != "catalog/review" {
		t.Fatalf("remote=%q subpath=%q", remote, subpath)
	}
}

func TestSkillsInstallReusesValidCanonicalContent(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, ".test", "skills", "one")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatalf("reconcile valid canonical content without source: %v", err)
	}
	assertFileContains(t, filepath.Join(home, ".test", "skills", "one", "SKILL.md"), "first")
}

func TestSkillsAdoptsLegacyManagedInstallWithoutAcquisition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	source := filepath.Join(t.TempDir(), "unavailable-source")
	legacyDir := filepath.Join(home, ".agents", "skills", "one")
	writeTestSkill(t, legacyDir, "one", "legacy")
	lockPath := config.SkillLockPath(home)
	lock := `{"version":3,"skills":{"one":{"source":"` + filepath.ToSlash(source) + `"}}}`
	if err := os.WriteFile(lockPath, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	targetLink := filepath.Join(home, ".codex", "skills", "one")
	if err := os.MkdirAll(filepath.Dir(targetLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "one"), targetLink); err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry([]Target{{
		ID:              "test",
		configDir:       ".codex",
		extraSkillsDirs: []string{".agents/skills"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: source}
	adopted, _, err := svc.Adopt(context.Background(), pkg, []string{"test"})
	if err != nil {
		t.Fatalf("adopting legacy install: %v", err)
	}
	if !adopted {
		t.Fatal("legacy install was not adopted")
	}

	packageDir := svc.packageDir(source)
	assertFileContains(t, filepath.Join(packageDir, "skills", "one", "SKILL.md"), "legacy")
	for _, link := range []string{legacyDir, targetLink} {
		if !ownedLink(link, packageDir) {
			t.Errorf("%s does not reference native package %s", link, packageDir)
		}
	}
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(lockAfter) != string(lockBefore) {
		t.Fatalf("legacy lockfile changed:\nbefore: %s\nafter:  %s", lockBefore, lockAfter)
	}
	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Installed || !inventory.PerTarget["test"] {
		t.Fatalf("adopted inventory = %+v", inventory)
	}
}

func TestSkillsRefreshLeavesLegacyInstallToExplicitImport(t *testing.T) {
	svc, fixture := newLegacySkillFixture(t)
	warnings, err := svc.Refresh(
		context.Background(),
		config.SkillPackage{Source: fixture.source},
		[]string{"test"},
	)
	if err == nil {
		t.Fatal("refresh took over the legacy-managed directory")
	}
	info, statErr := os.Lstat(fixture.legacyDir)
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("legacy directory was replaced: %v, %v", info, statErr)
	}
	assertFileContains(t, filepath.Join(fixture.legacyDir, "SKILL.md"), "legacy")
	if !containsSubstring(warnings, "agents skills import") {
		t.Fatalf("warnings = %v, want a legacy install notice", warnings)
	}
	fixture.assertLockUnchanged(t)
}

func TestSkillsRefreshUpdatesAdoptedLegacyInstall(t *testing.T) {
	svc, fixture := newLegacySkillFixture(t)
	pkg := config.SkillPackage{Source: fixture.source}
	if _, _, err := svc.Adopt(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatalf("adopting legacy install: %v", err)
	}
	if _, err := svc.Refresh(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatalf("refreshing adopted install: %v", err)
	}

	packageDir := svc.packageDir(fixture.source)
	assertFileContains(t, filepath.Join(packageDir, "skills", "one", "SKILL.md"), "updated")
	for _, link := range []string{fixture.legacyDir, fixture.targetLink} {
		if !ownedLink(link, packageDir) {
			t.Errorf("%s does not reference native package %s", link, packageDir)
		}
	}
	fixture.assertLockUnchanged(t)
}

// A CLI-era install recorded in the legacy lockfile and linked from a target, whose source now carries newer content.
type legacySkillFixture struct {
	source     string
	legacyDir  string
	targetLink string
	lockPath   string
	lock       string
}

func newLegacySkillFixture(t *testing.T) (*Skills, legacySkillFixture) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "updated")
	fixture := legacySkillFixture{
		source:     source,
		legacyDir:  filepath.Join(home, ".agents", "skills", "one"),
		targetLink: filepath.Join(home, ".codex", "skills", "one"),
		lockPath:   config.SkillLockPath(home),
		lock:       `{"version":3,"skills":{"one":{"source":"` + filepath.ToSlash(source) + `"}}}`,
	}
	writeTestSkill(t, fixture.legacyDir, "one", "legacy")
	if err := os.WriteFile(fixture.lockPath, []byte(fixture.lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(fixture.targetLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "one"), fixture.targetLink); err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry([]Target{{
		ID:              "test",
		configDir:       ".codex",
		extraSkillsDirs: []string{".agents/skills"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, ""), fixture
}

func (f legacySkillFixture) assertLockUnchanged(t *testing.T) {
	t.Helper()
	after, err := os.ReadFile(f.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != f.lock {
		t.Fatalf("legacy lockfile changed:\nbefore: %s\nafter:  %s", f.lock, after)
	}
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func TestSkillsInstallReacquiresWhenSelectionNarrows(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	writeTestSkill(t, filepath.Join(source, "skills", "two"), "two", "second")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source, Skills: []string{"one"}}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Installed || !reflect.DeepEqual(inventory.Skills, []string{"one"}) {
		t.Fatalf("narrowed inventory = %+v", inventory)
	}
	if _, err := os.Stat(filepath.Join(home, ".test", "skills", "two")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill remains: %v", err)
	}
}

func TestSkillsInstallReacquiresWhenSelectionResetsToAll(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	writeTestSkill(t, filepath.Join(source, "skills", "two"), "two", "second")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	state := memorySkillState{}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, &state, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source, Skills: []string{"one"}}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	svc = NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Installed || !reflect.DeepEqual(inventory.Skills, []string{"one", "two"}) {
		t.Fatalf("reset-to-all inventory = %+v", inventory)
	}
}

func TestSkillsReuseValidatesContentHashAndInventoryManifests(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "original")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	state := memorySkillState{}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, &state, "")
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(svc.packageDir(source), "skills", "one", "SKILL.md")
	if err := os.WriteFile(manifest, []byte("---\nname: one\ndescription: test\n---\n\ntampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, manifest, "original")

	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Installed || inventory.PerTarget["test"] {
		t.Fatalf("missing manifest reported installed: %+v", inventory)
	}
}

func TestSkillsStoresMachineLocalFingerprints(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	state := memorySkillState{}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, &state, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range state {
		if strings.Contains(raw, `"source_fingerprint"`) && strings.Contains(raw, `"content_hash"`) {
			return
		}
	}
	t.Fatalf("local metadata missing fingerprints: %v", state)
}

func TestSkillsRemoveRetainsSharedContentUntilLastTarget(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{
		{ID: "a", configDir: ".a"},
		{ID: "b", configDir: ".b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	packageDir := svc.packageDir(source)

	if _, err := svc.Remove(context.Background(), source, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(packageDir); err != nil {
		t.Fatalf("shared package removed while target b still references it: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".b", "skills", "one")); err != nil {
		t.Fatalf("target b link removed: %v", err)
	}

	if _, err := svc.Remove(context.Background(), source, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(packageDir); !os.IsNotExist(err) {
		t.Fatalf("unreferenced package remains: %v", err)
	}
}

func TestSkillsConvergesIdenticalUnmanagedTargetDirectory(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	unmanaged := filepath.Join(home, ".test", "skills", "one")
	writeTestSkill(t, unmanaged, "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")

	warnings, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(warnings, "adopted identical unmanaged copy at "+unmanaged) {
		t.Fatalf("install warnings = %v, want the adopted copy reported", warnings)
	}
	if !ownedLink(unmanaged, svc.packageDir(source)) {
		t.Fatalf("%s is not a managed link into the package store", unmanaged)
	}
	assertNoBackupsLeft(t, filepath.Dir(unmanaged))

	warnings, err = svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"})
	if err != nil {
		t.Fatalf("reinstall over the converged link: %v", err)
	}
	for _, warning := range warnings {
		t.Fatalf("unexpected warning on converged reinstall: %s", warning)
	}

	if _, err := svc.Remove(context.Background(), source, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(unmanaged); !os.IsNotExist(err) {
		t.Fatalf("converged link survived remove: %v", err)
	}
}

func TestSkillsRejectsDifferingUnmanagedTargetDirectory(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	unmanaged := filepath.Join(home, ".test", "skills", "one")
	writeTestSkill(t, unmanaged, "one", "local edits")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")

	_, err = svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"})
	if err == nil || !strings.Contains(err.Error(), `skill "one" already exists for target test`) {
		t.Fatalf("install error = %v, want the collision reported", err)
	}
	info, statErr := os.Lstat(unmanaged)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("differing directory was replaced: %v, %v", info, statErr)
	}
	assertFileContains(t, filepath.Join(unmanaged, "SKILL.md"), "local edits")
}

func TestSkillsRollsBackConvergedDirectoryWhenLinkingFails(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	unmanaged := filepath.Join(home, ".a", "skills", "one")
	writeTestSkill(t, unmanaged, "one", "first")
	blocked := filepath.Join(home, ".b", "skills")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry([]Target{
		{ID: "a", configDir: ".a"},
		{ID: "b", configDir: ".b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")

	// A read-only second target dir fails after the first converged, exercising the backup rewind.
	if err := os.Chmod(blocked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"a", "b"}); err == nil {
		t.Fatal("install into a read-only target dir succeeded")
	}

	info, statErr := os.Lstat(unmanaged)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("converged directory was not restored: %v, %v", info, statErr)
	}
	assertFileContains(t, filepath.Join(unmanaged, "SKILL.md"), "first")
	assertNoBackupsLeft(t, filepath.Dir(unmanaged))
}

func TestSkillsRemoveReportsUnmanagedCopyThatTookOverALink(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	unmanaged := filepath.Join(home, ".test", "skills", "one")
	if err := os.Remove(unmanaged); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, unmanaged, "one", "another tool's copy")

	warnings, err := svc.Remove(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(warnings, "unmanaged copy of skill \"one\" remains at "+unmanaged) {
		t.Fatalf("remove warnings = %v, want the orphaned copy reported", warnings)
	}
	info, err := os.Lstat(unmanaged)
	if err != nil || !info.IsDir() {
		t.Fatalf("unmanaged directory was deleted: %v, %v", info, err)
	}
	assertFileContains(t, filepath.Join(unmanaged, "SKILL.md"), "another tool's copy")
}

func TestSkillsInventoryReportsDriftedTargets(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".test", "skills", "one")

	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.PerTarget["test"] || inventory.PerTargetDrifted["test"] {
		t.Fatalf("installed inventory = %+v", inventory)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	inventory, err = svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.PerTarget["test"] || inventory.PerTargetDrifted["test"] {
		t.Fatalf("missing inventory = %+v", inventory)
	}

	writeTestSkill(t, link, "one", "another tool's copy")
	inventory, err = svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.PerTarget["test"] || !inventory.PerTargetDrifted["test"] || inventory.Installed {
		t.Fatalf("drifted inventory = %+v", inventory)
	}

	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "skills", "one"), link); err != nil {
		t.Fatal(err)
	}
	inventory, err = svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.PerTarget["test"] || !inventory.PerTargetDrifted["test"] {
		t.Fatalf("foreign-symlink inventory = %+v", inventory)
	}
}

func assertNoBackupsLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".omni-adopt-") {
			t.Fatalf("backup copy remains at %s", filepath.Join(dir, entry.Name()))
		}
	}
}

func TestSkillsInstallExcludesRepositoryMetadataFromRootSkill(t *testing.T) {
	repo := t.TempDir()
	writeTestSkill(t, repo, "root", "repo root skill")
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "v1")
	runGit(t, repo, "tag", "v1")

	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	pkg := config.SkillPackage{Source: repo, Ref: "v1"}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(svc.packageDir(repo), "skills", "root")
	if _, err := os.Stat(filepath.Join(skillDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("installed package carries repository metadata: %v", err)
	}
	before, err := hashTree(skillDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Refresh(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	after, err := hashTree(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("content hash changed across re-clone: %s != %s", before, after)
	}
}

func TestDiscoverSkillsIncludesPluginManifestPaths(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, filepath.Join(root, "skills", "standard"), "standard", "standard")
	writeTestSkill(t, filepath.Join(root, "plugin-assets", "custom"), "custom", "custom")
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, ".claude-plugin", "plugin.json"),
		[]byte(`{"name":"test","skills":["./plugin-assets/custom"]}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	got, _, err := discoverSkills(root)
	if err != nil {
		t.Fatal(err)
	}
	if got["standard"] == "" || got["custom"] == "" {
		t.Fatalf("discovered = %v, want standard and custom", got)
	}
}

func TestSkillsDiscoversNestedStandardSkillDirectory(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "category", "nested"), "nested", "nested")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(home, ".test", "skills", "nested", "SKILL.md"), "nested")
}

func TestSkillsReinstallLeavesCorrectLinksUntouched(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	state := memorySkillState{}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, &state, "")
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(home, ".test", "skills")
	// A read-only link dir makes any link rewrite fail, proving the no-op.
	if err := os.Chmod(linkDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(linkDir, 0o755) })
	warnings, err := svc.Install(context.Background(), pkg, []string{"test"})
	if err != nil {
		t.Fatalf("reinstall over correct links: %v", err)
	}
	for _, warning := range warnings {
		t.Fatalf("unexpected warning: %s", warning)
	}
}

func TestPackageDirDerivesFromResolvedSource(t *testing.T) {
	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	baseA := t.TempDir()
	baseB := t.TempDir()
	svcA := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, baseA)
	svcB := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, baseB)
	if svcA.packageDir("./skills") == svcB.packageDir("./skills") {
		t.Fatal("same package dir for ./skills under different source bases")
	}
	if got, want := svcA.packageDir("./skills"), svcA.packageDir(filepath.Join(baseA, "skills")); got != want {
		t.Fatalf("relative and resolved spellings diverge: %s vs %s", got, want)
	}
	if got, want := svcA.packageDir("~/skills"), svcA.packageDir(filepath.Join(home, "skills")); got != want {
		t.Fatalf("tilde and resolved spellings diverge: %s vs %s", got, want)
	}
}

func writeTestSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s does not contain %q: %s", path, want, data)
	}
}

type memorySkillState map[string]string

func (s *memorySkillState) GetState(_ context.Context, key string) (string, error) {
	value, ok := (*s)[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (s *memorySkillState) SetState(_ context.Context, key, value string) error {
	(*s)[key] = value
	return nil
}

// A gitignored generated dir is absent in a fresh clone, and refusing that package would be a security error for a link that reaches nothing.
func TestCopyTreeKeepsDanglingRelativeSymlink(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(".", "generated"), filepath.Join(src, "data")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst, src); err != nil {
		t.Fatalf("copyTree = %v, want the dangling link copied", err)
	}
	link, err := os.Readlink(filepath.Join(dst, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if link != filepath.Join(".", "generated") {
		t.Fatalf("copied link = %q, want the original target", link)
	}
}

func TestCopyTreeRejectsSymlinkResolvingOutsideRoot(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(base, "skill")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "outside"), filepath.Join(src, "hop")); err != nil {
		t.Fatal(err)
	}
	// Lexically inside the root; only resolving through hop reveals the escape.
	if err := os.Symlink(filepath.Join("hop", "secret"), filepath.Join(src, "attack")); err != nil {
		t.Fatal(err)
	}
	err := copyTree(src, filepath.Join(base, "out"), src)
	if err == nil || !strings.Contains(err.Error(), "escapes the skill directory") {
		t.Fatalf("copyTree = %v, want an escape rejection", err)
	}
}
