package app_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// ─── AddProfile / DeleteProfile ───────────────────────────────────────────────

func TestAddProfile_CreatesProfile(t *testing.T) {
	a, _ := newImportApp(t)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	if err := a.AddProfile("work", []string{"base", "work"}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	info, err := a.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	p, ok := info.Profiles["work"]
	if !ok {
		t.Fatal("profile 'work' not found")
	}
	if len(p.Groups) != 2 || p.Groups[0] != "base" || p.Groups[1] != "work" {
		t.Errorf("groups = %v, want [base work]", p.Groups)
	}
	groups, err := a.Groups(context.Background())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.BaseName())
	}
	if !slices.Contains(names, "base") || !slices.Contains(names, "testhost") {
		t.Errorf("config groups = %v, want base and testhost created", names)
	}
}

func TestAddProfile_EmptyGroupsStaysEmpty(t *testing.T) {
	a, _ := newImportApp(t)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	if err := a.AddProfile("empty", []string{}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	info, err := a.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	if groups := info.Profiles["empty"].Groups; len(groups) != 0 {
		t.Fatalf("profile groups = %v, want empty", groups)
	}
	configGroups, err := a.Groups(context.Background())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	var names []string
	for _, g := range configGroups {
		names = append(names, g.BaseName())
	}
	if !slices.Contains(names, "base") || !slices.Contains(names, "testhost") {
		t.Errorf("config groups = %v, want base and testhost created", names)
	}
}

func TestAddProfile_ReplacesExisting(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base", "work"})
	if err := a.AddProfile("work", []string{"base"}); err != nil {
		t.Fatalf("AddProfile replace: %v", err)
	}

	info, _ := a.ProfileStatus()
	// ["base"] — hostname is not stored in the Groups list.
	if len(info.Profiles["work"].Groups) != 1 {
		t.Errorf("expected 1 group after replace, got %d: %v", len(info.Profiles["work"].Groups), info.Profiles["work"].Groups)
	}
}

func TestDeleteProfile_RemovesProfile(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	_ = a.SetHostname("laptop", "work")
	if err := a.DeleteProfile("work"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	info, _ := a.ProfileStatus()
	if _, ok := info.Profiles["work"]; ok {
		t.Error("profile 'work' still present after delete")
	}
	if _, ok := info.Hostnames["laptop"]; ok {
		t.Error("hostname mapping to deleted profile still present")
	}
}

func TestDeleteProfile_Idempotent(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.DeleteProfile("nonexistent"); err != nil {
		t.Errorf("DeleteProfile on missing profile: %v", err)
	}
}

func TestRenameProfile_UpdatesProfileAndHostnames(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base", "tools"})
	_ = a.SetHostname("laptop", "work")
	if err := a.RenameProfile("work", "office"); err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}

	info, _ := a.ProfileStatus()
	if _, ok := info.Profiles["work"]; ok {
		t.Fatal("old profile still present after rename")
	}
	if got := info.Profiles["office"].Groups; !slices.Equal(got, []string{"base", "tools"}) {
		t.Fatalf("renamed profile groups = %v, want [base tools]", got)
	}
	if got := info.Hostnames["laptop"]; got != "office" {
		t.Fatalf("hostname mapping = %q, want office", got)
	}
}

func TestRenameProfile_RejectsExistingTarget(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	_ = a.AddProfile("home", []string{"personal"})
	if err := a.RenameProfile("work", "home"); err == nil {
		t.Fatal("RenameProfile should reject an existing target profile")
	}

	info, _ := a.ProfileStatus()
	if got := info.Profiles["work"].Groups; !slices.Equal(got, []string{"base"}) {
		t.Fatalf("source profile changed after rejected rename: %v", got)
	}
	if got := info.Profiles["home"].Groups; !slices.Equal(got, []string{"personal"}) {
		t.Fatalf("target profile changed after rejected rename: %v", got)
	}
}

// ─── AddGroupToProfile / RemoveGroupFromProfile ────────────────────────────────

func TestAddGroupToProfile_AppendsGroup(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	if err := a.AddGroupToProfile("work", "python"); err != nil {
		t.Fatalf("AddGroupToProfile: %v", err)
	}

	info, _ := a.ProfileStatus()
	groups := info.Profiles["work"].Groups
	if len(groups) != 2 || groups[0] != "base" || groups[1] != "python" {
		t.Errorf("groups = %v, want [base python]", groups)
	}
}

func TestAddGroupToProfile_NoDuplicates(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	_ = a.AddGroupToProfile("work", "base") // already present

	info, _ := a.ProfileStatus()
	if len(info.Profiles["work"].Groups) != 1 {
		t.Errorf("expected 1 group (no dup), got %d", len(info.Profiles["work"].Groups))
	}
}

func TestAddGroupToProfile_CreatesProfileIfAbsent(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.AddGroupToProfile("new", "base"); err != nil {
		t.Fatalf("AddGroupToProfile on new profile: %v", err)
	}

	info, _ := a.ProfileStatus()
	if _, ok := info.Profiles["new"]; !ok {
		t.Error("profile 'new' not created")
	}
}

func TestRemoveGroupFromProfile_RemovesGroup(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base", "python"})
	if err := a.RemoveGroupFromProfile("work", "python"); err != nil {
		t.Fatalf("RemoveGroupFromProfile: %v", err)
	}

	info, _ := a.ProfileStatus()
	groups := info.Profiles["work"].Groups
	if len(groups) != 1 || groups[0] != "base" {
		t.Errorf("groups = %v, want [base]", groups)
	}
}

func TestRemoveGroupFromProfile_Idempotent(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	if err := a.RemoveGroupFromProfile("work", "nonexistent"); err != nil {
		t.Errorf("RemoveGroupFromProfile on missing group: %v", err)
	}
}

// ─── SetHostname / RemoveHostname ─────────────────────────────────────────────

func TestSetHostname_MapsHostname(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.SetHostname("myhost", "work"); err != nil {
		t.Fatalf("SetHostname: %v", err)
	}

	info, _ := a.ProfileStatus()
	if info.Hostnames["myhost"] != "work" {
		t.Errorf("hostname myhost = %q, want work", info.Hostnames["myhost"])
	}
}

func TestSetHostname_RejectsBlankValues(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.SetHostname("   ", "work"); err == nil {
		t.Fatal("SetHostname blank hostname succeeded")
	}
	if err := a.SetHostname("myhost", "   "); err == nil {
		t.Fatal("SetHostname blank profile succeeded")
	}

	info, err := a.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	if len(info.Hostnames) != 0 {
		t.Fatalf("hostnames = %v, want none", info.Hostnames)
	}
}

func TestSetHostname_TrimsValues(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.SetHostname(" myhost ", " work "); err != nil {
		t.Fatalf("SetHostname: %v", err)
	}

	info, _ := a.ProfileStatus()
	if info.Hostnames["myhost"] != "work" {
		t.Fatalf("hostname map = %v, want myhost -> work", info.Hostnames)
	}
}

func TestRemoveHostname_RemovesMapping(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.SetHostname("myhost", "work")
	if err := a.RemoveHostname("myhost"); err != nil {
		t.Fatalf("RemoveHostname: %v", err)
	}

	info, _ := a.ProfileStatus()
	if _, ok := info.Hostnames["myhost"]; ok {
		t.Error("hostname 'myhost' still present after remove")
	}
}

func TestRemoveHostname_Idempotent(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.RemoveHostname("nonexistent"); err != nil {
		t.Errorf("RemoveHostname on missing hostname: %v", err)
	}
}

// ─── Sync with explicit profile ───────────────────────────────────────────────

func TestSync_ExplicitProfile_FiltersByGroups(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "dev", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Create profile that includes only the work group (not dev).
	if err := a.AddProfile("work-profile", []string{"work"}); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{Profile: "work-profile"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(brew.installCalled) != 1 || brew.installCalled[0] != "slack" {
		t.Errorf("installCalled = %v, want [slack]", brew.installCalled)
	}
}

func TestSync_ExplicitProfile_UnknownProfile(t *testing.T) {
	a, _ := newImportApp(t)

	_, err := a.Sync(context.Background(), gosync.SyncOptions{Profile: "nonexistent"})
	if err == nil {
		t.Error("expected error for unknown profile, got nil")
	}
}

// ─── Sync with hostname auto-detection ────────────────────────────────────────

func TestSync_AutoProfile_ActivatesViaHostname(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "dev", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Map the real hostname to a profile containing only "work" (not dev).
	hostname, _ := os.Hostname()
	if err := a.AddProfile("work-profile", []string{"work"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetHostname(hostname, "work-profile"); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(brew.installCalled) != 1 || brew.installCalled[0] != "slack" {
		t.Errorf("installCalled = %v, want [slack]", brew.installCalled)
	}
}

// ─── Hostname-group fallback ──────────────────────────────────────────────────

// testShortHostname mirrors the app's internal helper so tests can derive the
// expected group name without importing unexported symbols.
func testShortHostname() string {
	h, _ := os.Hostname()
	if idx := strings.IndexByte(h, '.'); idx != -1 {
		return h[:idx]
	}
	if h == "" {
		return "localhost"
	}
	return h
}

func TestSync_HostnameGroup_SyncsExistingGroup(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	// Base: ripgrep (should NOT be synced when hostname group is active).
	// Hostname group: slack.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: short, Tools: groupTools("slack")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// No profile mapping — should fall back to hostname group.
	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(brew.installCalled) != 1 || brew.installCalled[0] != "slack" {
		t.Errorf("installCalled = %v, want [slack]", brew.installCalled)
	}
}

// ─── AddIgnoreToProfile / RemoveIgnoreFromProfile ────────────────────────────

func TestAddIgnoreToProfile_AddsEntry(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	if err := a.AddIgnoreToProfile("work", "node"); err != nil {
		t.Fatalf("AddIgnoreToProfile: %v", err)
	}

	info, err := a.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	if len(info.Profiles["work"].Ignore) != 1 || info.Profiles["work"].Ignore[0] != "node" {
		t.Errorf("ignore = %v, want [node]", info.Profiles["work"].Ignore)
	}
}

func TestAddIgnoreToProfile_NoDuplicate(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	_ = a.AddIgnoreToProfile("work", "node")
	if err := a.AddIgnoreToProfile("work", "node"); err != nil {
		t.Fatalf("second AddIgnoreToProfile: %v", err)
	}

	info, _ := a.ProfileStatus()
	if len(info.Profiles["work"].Ignore) != 1 {
		t.Errorf("expected 1 ignore entry (no dup), got %d", len(info.Profiles["work"].Ignore))
	}
}

func TestRemoveIgnoreFromProfile_RemovesEntry(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	_ = a.AddIgnoreToProfile("work", "node")
	_ = a.AddIgnoreToProfile("work", "ripgrep")

	if err := a.RemoveIgnoreFromProfile("work", "node"); err != nil {
		t.Fatalf("RemoveIgnoreFromProfile: %v", err)
	}

	info, _ := a.ProfileStatus()
	ignore := info.Profiles["work"].Ignore
	if len(ignore) != 1 || ignore[0] != "ripgrep" {
		t.Errorf("ignore = %v, want [ripgrep]", ignore)
	}
}

func TestRemoveIgnoreFromProfile_IdempotentOnMissing(t *testing.T) {
	a, _ := newImportApp(t)

	_ = a.AddProfile("work", []string{"base"})
	// Remove a tool that was never added — should not error.
	if err := a.RemoveIgnoreFromProfile("work", "nonexistent"); err != nil {
		t.Fatalf("RemoveIgnoreFromProfile on missing: %v", err)
	}
}

func TestAddIgnoreToProfile_UnknownProfile_ReturnsError(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.AddIgnoreToProfile("nonexistent", "node")
	if err == nil {
		t.Error("expected error for unknown profile, got nil")
	}
}

// ─── RequireActiveProfile ─────────────────────────────────────────────────────

func TestRequireActiveProfile_WithActiveProfile_ReturnsNil(t *testing.T) {
	a, _ := newImportApp(t)

	hostname, _ := os.Hostname()
	_ = a.AddProfile("work", []string{"base"})
	_ = a.SetHostname(hostname, "work")

	if err := a.RequireActiveProfile(); err != nil {
		t.Errorf("RequireActiveProfile with active profile = %v, want nil", err)
	}
}

func TestRequireActiveProfile_NoProfile_ReturnsError(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.RequireActiveProfile()
	if err == nil {
		t.Error("expected error when no profile mapped, got nil")
	}
	if !strings.Contains(err.Error(), "no active profile") {
		t.Errorf("error = %q, want it to mention 'no active profile'", err.Error())
	}
}

// ─── Sync threads ignore list from active profile ─────────────────────────────

func TestSync_ProfileIgnoreList_SkipsTool(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("node", "brew"),
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep", "node"),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	hostname, _ := os.Hostname()
	_ = a.AddProfile("work", []string{"base"})
	_ = a.AddIgnoreToProfile("work", "node")
	_ = a.SetHostname(hostname, "work")

	result, err := a.Sync(context.Background(), gosync.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	ignored := result.Ignored()
	if len(ignored) != 1 || ignored[0].Tool.Name != "node" {
		t.Errorf("Ignored() = %v, want [node]", ignored)
	}
}

func TestSync_HostnameGroup_CreatesGroupFromInstalledTools(t *testing.T) {
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "14.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	// No groups at all — sync should create hostname group via import.
	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Verify the hostname group was created in settings.json.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	var hostGroup *config.GroupConfig
	for _, g := range updated.Groups {
		if g.Name == short {
			hostGroup = g
			break
		}
	}
	if hostGroup == nil {
		t.Fatalf("hostname group %q not found in config", short)
	}
	if len(hostGroup.Tools) != 1 || hostGroup.Tools[0].Name != "ripgrep" {
		t.Errorf("hostname group tools = %v, want [ripgrep]", hostGroup.Tools)
	}

	// Base group must not have been populated.
	for _, g := range updated.Groups {
		if g.IsBase() && len(g.Tools) > 0 {
			t.Errorf("base group should be empty, got %v", g.Tools)
		}
	}
}
