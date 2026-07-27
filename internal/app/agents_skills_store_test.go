package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type skillStoreFixture struct {
	app        *App
	home       string
	source     string
	root       string // canonical packages root
	packageDir string
	skillsDir  string // the claude-code target skills dir
}

func newSkillStoreFixture(t *testing.T, hostname string) *skillStoreFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", hostname)
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "store-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code"}}
	if _, err := service.Install(context.Background(), pkg, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, "data", "omni", "skills", "packages")
	return &skillStoreFixture{
		app:        a,
		home:       home,
		source:     source,
		root:       root,
		packageDir: onlyPackageDir(t, root),
		skillsDir:  filepath.Join(home, ".claude", "skills"),
	}
}

func onlyPackageDir(t *testing.T, root string) string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 64 {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("package dirs under %s = %v, want exactly 1", root, dirs)
	}
	return dirs[0]
}

func (f *skillStoreFixture) fix(t *testing.T, dryRun bool) SkillStoreFixReport {
	t.Helper()
	report, err := f.app.FixSkillStore(context.Background(), dryRun)
	if err != nil {
		t.Fatalf("FixSkillStore(dryRun=%v): %v", dryRun, err)
	}
	return report
}

func (f *skillStoreFixture) doctorSkillsItems(t *testing.T) []string {
	t.Helper()
	result, err := f.app.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range result.Checks {
		if check.ID != "agents" {
			continue
		}
		for _, group := range check.Groups {
			if group.Header == "skills" {
				return group.Items
			}
		}
	}
	t.Fatal("doctor result has no agents/skills group")
	return nil
}

func requireItemContaining(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if strings.Contains(item, want) {
			return
		}
	}
	t.Fatalf("doctor skills items = %v, want one containing %q", items, want)
}

func requireNoItemContaining(t *testing.T, items []string, unwanted string) {
	t.Helper()
	for _, item := range items {
		if strings.Contains(item, unwanted) {
			t.Fatalf("doctor skills items = %v, want none containing %q", items, unwanted)
		}
	}
}

func mustExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func mustNotExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be gone, Lstat err = %v", path, err)
		}
	}
}

func TestFixSkillStore_RemovesOperationDebris(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-store-debris-host")
	debris := []string{
		f.packageDir + ".previous",
		f.packageDir + ".removing",
		filepath.Join(f.root, ".install-123456"),
		filepath.Join(f.root, ".adopt-654321"),
		filepath.Join(f.skillsDir, ".demo.omni-legacy-987"),
		filepath.Join(f.skillsDir, ".demo.omni-adopt-987"),
	}
	for _, dir := range debris {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	staging := filepath.Join(f.skillsDir, "demo.omni-tmp")
	if err := os.Symlink(filepath.Join(f.packageDir, "skills", "demo"), staging); err != nil {
		t.Fatal(err)
	}
	debris = append(debris, staging)
	bystander := filepath.Join(f.skillsDir, "other-tool-skill")
	writeAppSkill(t, bystander, "other-tool-skill")

	requireItemContaining(t, f.doctorSkillsItems(t), "leftover artifact(s) from an interrupted operation")

	dry := f.fix(t, true)
	if got := len(dry.Debris); got != len(debris) {
		t.Fatalf("dry-run debris = %v, want %d entries", dry.Debris, len(debris))
	}
	mustExist(t, debris...)

	report := f.fix(t, false)
	sort.Strings(debris)
	if got := report.Debris; len(got) != len(debris) {
		t.Fatalf("removed debris = %v, want %v", got, debris)
	}
	mustNotExist(t, debris...)
	mustExist(t, f.packageDir, bystander, filepath.Join(f.skillsDir, "demo"))
	requireNoItemContaining(t, f.doctorSkillsItems(t), "leftover artifact(s)")
}

func TestFixSkillStore_RemovesDanglingOwnedLinks(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-store-dangling-host")
	link := filepath.Join(f.skillsDir, "demo")
	if err := os.RemoveAll(f.packageDir); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(f.skillsDir, "foreign")
	if err := os.Symlink(filepath.Join(f.home, "nowhere"), foreign); err != nil {
		t.Fatal(err)
	}

	requireItemContaining(t, f.doctorSkillsItems(t), "skill link(s) point at a removed package")

	dry := f.fix(t, true)
	if want := []string{link}; len(dry.DanglingLinks) != 1 || dry.DanglingLinks[0] != want[0] {
		t.Fatalf("dry-run dangling links = %v, want %v", dry.DanglingLinks, want)
	}
	mustExist(t, link, foreign)

	report := f.fix(t, false)
	if len(report.DanglingLinks) != 1 || report.DanglingLinks[0] != link {
		t.Fatalf("removed dangling links = %v, want [%s]", report.DanglingLinks, link)
	}
	mustNotExist(t, link)
	mustExist(t, foreign)
}

func TestFixSkillStore_RemovesOrphanedPackages(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-store-orphan-host")
	orphan := filepath.Join(f.root, strings.Repeat("a", 64))
	writeAppSkill(t, filepath.Join(orphan, "skills", "orphaned"), "orphaned")
	foreignShape := filepath.Join(f.root, "not-a-package-hash")
	writeAppSkill(t, filepath.Join(foreignShape, "skills", "keep"), "keep")
	linked := filepath.Join(f.root, strings.Repeat("b", 64))
	writeAppSkill(t, filepath.Join(linked, "skills", "linked"), "linked")
	if err := os.Symlink(filepath.Join(linked, "skills", "linked"), filepath.Join(f.skillsDir, "linked")); err != nil {
		t.Fatal(err)
	}

	requireItemContaining(t, f.doctorSkillsItems(t), "installed package(s) no manifest entry references")

	dry := f.fix(t, true)
	if len(dry.OrphanedPackages) != 1 || dry.OrphanedPackages[0] != orphan {
		t.Fatalf("dry-run orphans = %v, want [%s]", dry.OrphanedPackages, orphan)
	}
	mustExist(t, orphan)

	report := f.fix(t, false)
	if len(report.OrphanedPackages) != 1 || report.OrphanedPackages[0] != orphan {
		t.Fatalf("removed orphans = %v, want [%s]", report.OrphanedPackages, orphan)
	}
	mustNotExist(t, orphan)
	mustExist(t, foreignShape, linked, f.packageDir)
}

func TestFixSkillStore_KeepsPackageDeclaredByAnInactiveGroup(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-store-group-host")
	if err := f.app.withConfig(func(cfg *config.RootConfig) error {
		cfg.Groups = []*config.GroupConfig{{Name: "elsewhere", Skills: []string{f.source}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.skillsDir, "demo")); err != nil {
		t.Fatal(err)
	}
	if rows, err := f.app.SkillPackageRows(context.Background()); err != nil || len(rows) != 0 {
		t.Fatalf("rows = %v (err %v), want none resolved for this host", rows, err)
	}

	report := f.fix(t, false)
	if len(report.OrphanedPackages) != 0 {
		t.Fatalf("orphans = %v, want none: the manifest still declares the source", report.OrphanedPackages)
	}
	mustExist(t, f.packageDir)
}

func TestFixSkillStore_RebuildsMissingMetadata(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-store-metadata-host")
	key := "agent.skills." + filepath.Base(f.packageDir)
	db := f.app.readDB()
	if db == nil {
		t.Fatal("app has no local state database")
	}
	ctx := context.Background()
	if err := db.SetState(ctx, key, "{not json"); err != nil {
		t.Fatal(err)
	}

	requireItemContaining(t, f.doctorSkillsItems(t), "missing local install metadata")

	dry := f.fix(t, true)
	if len(dry.RebuiltMetadata) != 1 || dry.RebuiltMetadata[0] != f.source {
		t.Fatalf("dry-run metadata gaps = %v, want [%s]", dry.RebuiltMetadata, f.source)
	}
	if raw, err := db.GetState(ctx, key); err != nil || raw != "{not json" {
		t.Fatalf("dry run rewrote metadata: %q, %v", raw, err)
	}

	report := f.fix(t, false)
	if len(report.RebuiltMetadata) != 1 || report.RebuiltMetadata[0] != f.source {
		t.Fatalf("rebuilt metadata = %v, want [%s]", report.RebuiltMetadata, f.source)
	}
	raw, err := db.GetState(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var rebuilt struct {
		Source      string `json:"source"`
		Skills      []string
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal([]byte(raw), &rebuilt); err != nil {
		t.Fatalf("rebuilt metadata is not valid JSON: %v (%q)", err, raw)
	}
	if rebuilt.Source != f.source || rebuilt.ContentHash == "" {
		t.Fatalf("rebuilt metadata = %+v, want source %q and a content hash", rebuilt, f.source)
	}
	requireNoItemContaining(t, f.doctorSkillsItems(t), "missing local install metadata")
}

func TestFixSkillStore_SkipsWhenSkillsDisabled(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-store-disabled-host")
	debris := filepath.Join(f.root, ".install-abc")
	if err := os.MkdirAll(debris, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.app.withConfig(func(cfg *config.RootConfig) error {
		cfg.Settings.SkillsDisabled = config.BoolPtr(true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	report := f.fix(t, false)
	if !report.Empty() {
		t.Fatalf("report = %+v, want empty when skills are disabled", report)
	}
	mustExist(t, debris)
}

func TestDoctorAgentsSkills_UnmanagedLockPackagesStayInfo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "skills-store-unmanaged-host")
	stubBinariesOnPath(t, "claude")
	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"skills":{"demo":{"source":"owner/legacy-repo","updated_at":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(agentsDir, ".skill-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	a := New(cfgPath)
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var check *DoctorCheck
	for i := range result.Checks {
		if result.Checks[i].ID == "agents" {
			check = &result.Checks[i]
		}
	}
	if check == nil {
		t.Fatal("agents check missing")
	}
	if !strings.Contains(check.Message, "skills ok") {
		t.Errorf("message = %q, want the skills group ok: an unadopted legacy package is not breakage", check.Message)
	}
	var items []string
	for _, group := range check.Groups {
		if group.Header == "skills" {
			items = group.Items
		}
	}
	requireItemContaining(t, items, "1 legacy skill package(s) not in manifest")
	requireItemContaining(t, items, "omni agents skills import")
}
