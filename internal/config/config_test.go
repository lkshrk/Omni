package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/testguard"
)

// ─── Load / Save ──────────────────────────────────────────────────────────────

func TestLoad_MissingFile(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "missing", "settings.json"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil RootConfig for missing file")
	}
	if cfg.Version != config.CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
}

func TestLoad_RejectsLivePathInLocalTests(t *testing.T) {
	if testguard.Isolated() {
		t.Skip("Docker-isolated tests do not enforce local live-path rejection")
	}
	if _, err := config.Load("/nonexistent/path/settings.json"); err == nil {
		t.Fatal("Load accepted live path in local test")
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "tools": {
    "ripgrep": {"provider": "brew"},
    "black": {"provider": "pip", "package": "black"}
  },
  "groups": [
    {
      "tools": [
        "ripgrep",
        "black"
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(cfg.Groups))
	}
	if len(cfg.Groups[0].Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(cfg.Groups[0].Tools))
	}
	if cfg.Groups[0].Tools[0].Name != "ripgrep" {
		t.Errorf("got name %q, want %q", cfg.Groups[0].Tools[0].Name, "ripgrep")
	}
	if cfg.Tools["ripgrep"].Provider != "brew" {
		t.Errorf("ripgrep provider = %q, want brew", cfg.Tools["ripgrep"].Provider)
	}
	if cfg.Version != config.CurrentVersion {
		t.Fatalf("legacy config version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
}

func TestLoad_RejectsFutureConfigVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"version": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected future config version to be rejected")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("error = %v, want newer-than-supported message", err)
	}
}

func TestLoad_OldGroupToolObjectRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "groups": [
    {"tools": [{"name": "ripgrep", "provider": "brew"}]}
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected old group-owned tool object config to be rejected")
	}
	if !strings.Contains(err.Error(), "old group tool object") {
		t.Fatalf("error = %v, want old object message", err)
	}
}

func TestLoad_NormalizesGroupAndHostOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "hosts": {
    "laptop": ["work", "apps"]
  },
  "groups": [
    {"name": "work"},
    {"name": "laptop", "special": "host"},
    {"name": "apps"}
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gotGroups := []string{cfg.Groups[0].BaseName(), cfg.Groups[1].BaseName(), cfg.Groups[2].BaseName()}
	if strings.Join(gotGroups, ",") != "apps,work,laptop" {
		t.Fatalf("groups = %v, want [apps work laptop]", gotGroups)
	}
	if got := strings.Join(cfg.Hosts["laptop"], ","); got != "apps,work" {
		t.Fatalf("host groups = [%s], want [apps work]", got)
	}
}

func TestNormalizeFile_PersistsOrderAndPreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "future": {"keep": true},
  "hosts": {
    "laptop": ["work", "apps"]
  },
  "groups": [
    {"name": "work"},
    {"name": "laptop", "special": "host"},
    {"name": "apps"}
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := config.NormalizeFile(path)
	if err != nil {
		t.Fatalf("NormalizeFile: %v", err)
	}
	if !changed {
		t.Fatal("NormalizeFile changed = false, want true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"future": {`) {
		t.Fatalf("NormalizeFile did not preserve unknown top-level key:\n%s", data)
	}
	if !strings.Contains(string(data), `"version": `+strconv.Itoa(config.CurrentVersion)) {
		t.Fatalf("NormalizeFile did not persist config version:\n%s", data)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gotGroups := []string{cfg.Groups[0].BaseName(), cfg.Groups[1].BaseName(), cfg.Groups[2].BaseName()}
	if strings.Join(gotGroups, ",") != "apps,work,laptop" {
		t.Fatalf("groups = %v, want [apps work laptop]", gotGroups)
	}
	if got := strings.Join(cfg.Hosts["laptop"], ","); got != "apps,work" {
		t.Fatalf("host groups = [%s], want [apps work]", got)
	}
}

func TestNormalizeFile_PersistsLegacyVersionWithoutOrderChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "future": {"keep": true},
  "groups": [
    {"name": "apps"}
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := config.NormalizeFile(path)
	if err != nil {
		t.Fatalf("NormalizeFile: %v", err)
	}
	if !changed {
		t.Fatal("NormalizeFile changed = false, want true for legacy version migration")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"version": `+strconv.Itoa(config.CurrentVersion)) {
		t.Fatalf("NormalizeFile did not write version:\n%s", content)
	}
	if !strings.Contains(content, `"future": {`) {
		t.Fatalf("NormalizeFile did not preserve unknown top-level key:\n%s", content)
	}
}

func TestSave_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "settings.json")
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Tools: []config.ToolEntry{{Name: "ripgrep", Provider: "brew"}}},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestSave_InjectsSchemaAndVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := config.Save(path, &config.RootConfig{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"$schema"`) {
		t.Error("Save did not inject $schema key")
	}
	if !strings.Contains(content, config.SchemaURL) {
		t.Errorf("Save did not inject SchemaURL %q", config.SchemaURL)
	}
	if !strings.Contains(content, `"version": `+strconv.Itoa(config.CurrentVersion)) {
		t.Error("Save did not inject current config version")
	}
	// $schema must be the very first key so editors pick it up immediately.
	// encoding/json serialises struct fields in declaration order (Go spec §reflect),
	// and Schema is declared first in RootConfig — so this check is stable.
	// If Schema is ever moved from position 0, this test will catch it.
	if !strings.HasPrefix(strings.TrimSpace(content), `{`+"\n"+`  "$schema"`) {
		t.Errorf("$schema is not the first key; got:\n%s", content[:min(len(content), 80)])
	}
	if !strings.HasPrefix(strings.TrimSpace(content), "{\n  \"$schema\":") || !strings.Contains(content[:min(len(content), 120)], "\n  \"version\": "+strconv.Itoa(config.CurrentVersion)) {
		t.Errorf("version is not stamped near the top; got:\n%s", content[:min(len(content), 120)])
	}
}

func TestPatch_InjectsSchemaAndVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	if err := config.Patch(path, settingsPatch{}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, config.SchemaURL) {
		t.Error("Patch did not inject $schema")
	}
	if !strings.Contains(content, `"version": `+strconv.Itoa(config.CurrentVersion)) {
		t.Error("Patch did not inject current config version")
	}
	// Patch explicitly reconstructs output with $schema first (map iteration order
	// is non-deterministic, so a naive MarshalIndent on the raw map would not
	// guarantee position). Verify the contract holds.
	if !strings.HasPrefix(strings.TrimSpace(content), `{`+"\n"+`  "$schema"`) {
		t.Errorf("$schema is not the first key after Patch; got:\n%s", content[:min(len(content), 80)])
	}
	if !strings.Contains(content[:min(len(content), 120)], "\n  \"version\": "+strconv.Itoa(config.CurrentVersion)) {
		t.Errorf("version is not stamped near the top after Patch; got:\n%s", content[:min(len(content), 120)])
	}
}

func TestPatch_RejectsFutureConfigVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"version": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	err := config.Patch(path, settingsPatch{})
	if err == nil {
		t.Fatal("expected future config version to be rejected")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("error = %v, want newer-than-supported message", err)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep":    {Provider: "system", InstallWith: "brew"},
			"black":      {Provider: "python", Package: "black", InstallWith: "pip"},
			"typescript": {Provider: "node", Package: "typescript"},
		},
		Groups: []*config.GroupConfig{
			{
				Tools: []config.ToolEntry{
					{Name: "ripgrep"},
					{Name: "black"},
					{Name: "typescript"},
				},
			},
		},
	}

	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(loaded.Groups))
	}
	got := loaded.Groups[0]
	want := original.Groups[0]
	if len(got.Tools) != len(want.Tools) {
		t.Fatalf("got %d memberships, want %d", len(got.Tools), len(want.Tools))
	}
	expectedProvider := map[string]string{"ripgrep": "system", "black": "python", "typescript": "node"}
	expectedInstallWith := map[string]string{"ripgrep": "brew", "black": "pip"}
	for i, w := range want.Tools {
		g := got.Tools[i]
		if g.Name != w.Name || g.Provider != "" || g.Package != "" {
			t.Errorf("membership[%d]: got %+v, want logical name %q", i, g, w.Name)
		}
		spec := loaded.Tools[w.Name]
		if spec.Provider != expectedProvider[w.Name] || spec.Package != original.Tools[w.Name].Package || spec.InstallWith != expectedInstallWith[w.Name] {
			t.Errorf("spec[%q]: got %+v, want provider=%q package=%q install_with=%q", w.Name, spec, expectedProvider[w.Name], original.Tools[w.Name].Package, expectedInstallWith[w.Name])
		}
	}

	// Modify, save again, reload.
	loaded.Tools["jq"] = config.ToolSpec{Provider: "system", InstallWith: "brew"}
	loaded.Groups[0].Tools = append(loaded.Groups[0].Tools, config.ToolEntry{Name: "jq"})
	if err := config.Save(path, loaded); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if len(reloaded.Groups[0].Tools) != 4 {
		t.Errorf("after append: got %d tools, want 4", len(reloaded.Groups[0].Tools))
	}
}

// ─── Validate ─────────────────────────────────────────────────────────────────

func TestValidate_Valid(t *testing.T) {
	cfg := &config.Config{
		Tools: []config.ToolEntry{
			{Name: "ripgrep", Provider: "brew"},
			{Name: "black", Provider: "pip"},
		},
	}
	errs := config.Validate(cfg, []string{"brew", "pip", "node"})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_DuplicateToolName(t *testing.T) {
	cfg := &config.Config{
		Tools: []config.ToolEntry{
			{Name: "ripgrep", Provider: "brew"},
			{Name: "ripgrep", Provider: "brew"},
		},
	}
	errs := config.Validate(cfg, nil)
	if len(errs) == 0 {
		t.Fatal("expected duplicate error")
	}
}

func TestValidate_UnknownProvider(t *testing.T) {
	cfg := &config.Config{
		Tools: []config.ToolEntry{
			{Name: "something", Provider: "cargo"},
		},
	}
	errs := config.Validate(cfg, []string{"brew", "node", "pip"})
	if len(errs) == 0 {
		t.Fatal("expected unknown provider error")
	}
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &config.Config{
		Tools: []config.ToolEntry{
			{Name: "", Provider: "brew"},
		},
	}
	errs := config.Validate(cfg, nil)
	if len(errs) == 0 {
		t.Fatal("expected missing name error")
	}
}

// ─── EffectivePackage ─────────────────────────────────────────────────────────

func TestEffectivePackage_DefaultsToName(t *testing.T) {
	e := config.ToolEntry{Name: "ripgrep", Provider: "brew"}
	if got := e.EffectivePackage(); got != "ripgrep" {
		t.Errorf("got %q, want %q", got, "ripgrep")
	}
}

func TestEffectivePackage_UsesPackageWhenSet(t *testing.T) {
	e := config.ToolEntry{Name: "black", Provider: "pip", Package: "black"}
	if got := e.EffectivePackage(); got != "black" {
		t.Errorf("got %q, want %q", got, "black")
	}
}

// ─── ValidateGroups ───────────────────────────────────────────────────────────

func TestValidateGroups_NoDuplicates(t *testing.T) {
	groups := []*config.GroupConfig{
		{Tools: []config.ToolEntry{{Name: "ripgrep", Provider: "brew"}}},
		{
			Name:  "work",
			Tools: []config.ToolEntry{{Name: "slack", Provider: "brew"}},
		},
	}
	if errs := config.ValidateGroups(groups); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestValidateGroups_DuplicateAllowedAcrossGroups(t *testing.T) {
	groups := []*config.GroupConfig{
		{Tools: []config.ToolEntry{{Name: "git", Provider: "brew"}}},
		{
			Name:  "work",
			Tools: []config.ToolEntry{{Name: "git", Provider: "brew"}},
		},
	}
	if errs := config.ValidateGroups(groups); len(errs) != 0 {
		t.Errorf("logical tools can belong to multiple groups, got %v", errs)
	}
}

func TestValidateGroups_SameNameDifferentProvider(t *testing.T) {
	// Logical tool memberships are provider-independent.
	groups := []*config.GroupConfig{
		{Tools: []config.ToolEntry{{Name: "ripgrep", Provider: "brew"}}},
		{
			Name:  "linux",
			Tools: []config.ToolEntry{{Name: "ripgrep", Provider: "apt"}},
		},
	}
	if errs := config.ValidateGroups(groups); len(errs) != 0 {
		t.Errorf("unexpected errors for cross-provider tools: %v", errs)
	}
}

// ─── GroupConfig methods ──────────────────────────────────────────────────────

func TestGroupConfig_NamedGroup(t *testing.T) {
	g := &config.GroupConfig{Name: "work"}
	if g.GroupName() != "work" {
		t.Errorf("GroupName = %q, want work", g.GroupName())
	}
	if g.BaseName() != "work" {
		t.Errorf("BaseName = %q, want work", g.BaseName())
	}
}

// ─── Host round-trip ──────────────────────────────────────────────────────────

func TestRootConfig_HostRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "myhost", Special: "host"},
			{Name: "work"},
		},
		Hosts: map[string][]string{"myhost": {"work"}},
	}
	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(loaded.Hosts["myhost"], ","); got != "work" {
		t.Errorf("host groups = %q, want work", got)
	}
}

func TestGlobalIgnore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := &config.RootConfig{
		Ignore: config.GlobalIgnore{Tools: []string{"node", "ripgrep"}},
	}
	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Ignore.Tools) != 2 {
		t.Fatalf("got %d ignore entries, want 2", len(loaded.Ignore.Tools))
	}
	if loaded.Ignore.Tools[0] != "node" || loaded.Ignore.Tools[1] != "ripgrep" {
		t.Errorf("ignore = %v, want [node ripgrep]", loaded.Ignore.Tools)
	}
}

func TestGlobalIgnore_EmptyOmittedFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := &config.RootConfig{}
	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"ignore": {}`) {
		t.Errorf("empty ignore should round-trip as an empty object: %s", data)
	}
}

// ─── DotEntry ─────────────────────────────────────────────────────────────────

func TestGroupConfig_WithDotEntries_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{
				Name: "work",
				Tools: []config.ToolEntry{
					{Name: "slack", Provider: "brew"},
				},
				Dots: []config.DotEntry{
					{Name: "nvim", Path: "~/.config/nvim"},
					{Name: "zshrc", Path: "~/.zshrc", Ignore: []string{"*.zwc"}},
				},
			},
		},
	}
	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(loaded.Groups))
	}
	g := loaded.Groups[0]
	if len(g.Dots) != 2 {
		t.Fatalf("got %d dots, want 2", len(g.Dots))
	}
	nvim := g.Dots[0]
	if nvim.Name != "nvim" || nvim.Path != "~/.config/nvim" {
		t.Errorf("nvim dot = %+v", nvim)
	}
	zshrc := g.Dots[1]
	if zshrc.Name != "zshrc" || zshrc.Path != "~/.zshrc" {
		t.Errorf("zshrc dot = %+v", zshrc)
	}
	if len(zshrc.Ignore) != 1 || zshrc.Ignore[0] != "*.zwc" {
		t.Errorf("zshrc.Ignore = %v, want [*.zwc]", zshrc.Ignore)
	}
}

// ─── Settings round-trip ──────────────────────────────────────────────────────

func TestSettings_DotsRepo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := &config.RootConfig{
		Settings: config.Settings{DotsRepo: "~/Dev/dotfiles"},
	}
	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Settings.DotsRepo != "~/Dev/dotfiles" {
		t.Errorf("DotsRepo = %q, want ~/Dev/dotfiles", loaded.Settings.DotsRepo)
	}
}

func TestDotsGitConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := &config.RootConfig{
		Settings: config.Settings{
			DotsRepo: "~/dotfiles",
			DotsGit: config.DotsGitConfig{
				AutoCommit: true,
				AutoPush:   false,
			},
		},
	}
	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.Settings.DotsGit
	if !got.AutoCommit {
		t.Error("AutoCommit: want true, got false")
	}
	if got.AutoPush {
		t.Error("AutoPush: want false, got true")
	}
}

func TestDotsGitConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := config.Save(path, &config.RootConfig{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.Settings.DotsGit
	if got.AutoCommit || got.AutoPush {
		t.Errorf("expected zero DotsGitConfig, got %+v", got)
	}
}

func TestSettings_JSONTagsRemainStableAcrossUILabelRenames(t *testing.T) {
	disabled := true
	settings := config.Settings{
		AutoImport:        true,
		DotsRepo:          "~/dotfiles",
		DotsDisabled:      &disabled,
		DisabledProviders: []string{"node"},
		DotsGit: config.DotsGitConfig{
			AutoCommit: true,
			AutoPush:   true,
		},
	}
	settings.SetEcosystemManager("node", "pnpm")
	settings.SetEcosystemPriority("system", []string{"brew", "apt"})

	raw, err := json.Marshal(config.RootConfig{Settings: settings})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("Unmarshal root: %v", err)
	}
	got, ok := root["settings"].(map[string]any)
	if !ok {
		t.Fatalf("settings object missing or wrong type in %s", raw)
	}

	for _, key := range []string{
		"auto_import",
		"ecosystems",
		"dots_repo",
		"dots_disabled",
		"dots_git",
		"disabled_providers",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("Settings JSON tag %q missing from %s", key, raw)
		}
	}
	dotsGit, ok := got["dots_git"].(map[string]any)
	if !ok {
		t.Fatalf("dots_git object missing or wrong type in %s", raw)
	}
	for _, key := range []string{"auto_commit", "auto_push"} {
		if _, ok := dotsGit[key]; !ok {
			t.Fatalf("DotsGit JSON tag %q missing from %s", key, raw)
		}
	}

	for _, key := range []string{
		"import_installed_tools",
		"system_provider_order",
		"system_provider",
		"node_provider",
		"python_provider",
		"repository",
		"sync_on_this_machine",
		"commit_changes",
		"push_changes",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("user-facing Settings label leaked into persisted JSON key %q in %s", key, raw)
		}
	}
}

// ─── Patch ────────────────────────────────────────────────────────────────────

func TestPatch_UpdatesTargetKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write an initial file with both settings and groups.
	initialSettings := config.Settings{DotsRepo: "~/dotfiles"}
	initialSettings.SetEcosystemManager("node", "bun")
	initial := &config.RootConfig{
		Settings: initialSettings,
		Groups:   []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "ripgrep", Provider: "brew"}}}},
	}
	if err := config.Save(path, initial); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Patch only the settings key.
	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	newSettings := config.Settings{DotsRepo: "~/newdots"}
	newSettings.SetEcosystemManager("node", "pnpm")
	if err := config.Patch(path, settingsPatch{Settings: newSettings}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Settings must be updated.
	if loaded.Settings.DotsRepo != "~/newdots" {
		t.Errorf("DotsRepo = %q, want ~/newdots", loaded.Settings.DotsRepo)
	}
	if loaded.Settings.EcosystemManager("node") != "pnpm" {
		t.Errorf("node manager = %q, want pnpm", loaded.Settings.EcosystemManager("node"))
	}

	// Groups must be preserved unchanged.
	if len(loaded.Groups) != 1 || len(loaded.Groups[0].Tools) != 1 {
		t.Fatalf("Groups changed unexpectedly: %+v", loaded.Groups)
	}
	if loaded.Groups[0].Tools[0].Name != "ripgrep" {
		t.Errorf("tool name = %q, want ripgrep", loaded.Groups[0].Tools[0].Name)
	}
}

func TestPatch_CreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	if err := config.Patch(path, settingsPatch{Settings: config.Settings{DotsRepo: "~/dots"}}); err != nil {
		t.Fatalf("Patch on missing file: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Settings.DotsRepo != "~/dots" {
		t.Errorf("DotsRepo = %q, want ~/dots", loaded.Settings.DotsRepo)
	}
}

func TestPatch_PreservesSymlinkedSettingsFile(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "omni")
	repoDir := filepath.Join(dir, "repo", "dotfiles", "omni", ".config", "omni")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repoDir, "settings.json")
	if err := os.WriteFile(target, []byte(`{"settings":{},"groups":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(configDir, "settings.json")
	relTarget, err := filepath.Rel(configDir, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	if err := config.Patch(link, settingsPatch{Settings: config.Settings{DotsRepo: "~/dots"}}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if info, err := os.Lstat(link); err != nil {
		t.Fatalf("Lstat link: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings path mode = %v, want symlink preserved", info.Mode())
	}
	loaded, err := config.Load(target)
	if err != nil {
		t.Fatalf("Load target: %v", err)
	}
	if loaded.Settings.DotsRepo != "~/dots" {
		t.Fatalf("target DotsRepo = %q, want ~/dots", loaded.Settings.DotsRepo)
	}
}

func TestPatchRaw_NullRemovesKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	initial := []byte(`{"settings":{},"alpha":"a","beta":"b"}` + "\n")
	if err := os.WriteFile(path, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	patch := map[string]json.RawMessage{
		"alpha": json.RawMessage(`null`),
	}
	if err := config.PatchRaw(path, patch); err != nil {
		t.Fatalf("PatchRaw: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, `"alpha"`) {
		t.Errorf("alpha key should be removed; got:\n%s", content)
	}
	if !strings.Contains(content, `"beta"`) {
		t.Error("beta key should be preserved")
	}
	if !strings.Contains(content, `"settings"`) {
		t.Error("settings key should be preserved")
	}
}

func TestPatch_PreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write a file with an extra key that RootConfig doesn't know about.
	raw := []byte(`{"settings":{},"_custom_field":"preserved","groups":[]}` + "\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	patchSettings := config.Settings{}
	patchSettings.SetEcosystemManager("node", "bun")
	if err := config.Patch(path, settingsPatch{Settings: patchSettings}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"_custom_field"`) {
		t.Error("Patch removed _custom_field — unknown keys must be preserved")
	}
	if !strings.Contains(string(data), `"groups"`) {
		t.Error("Patch removed groups key")
	}
}

func TestPatch_MultiKeyFilePreservesAllKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// File with four top-level keys beyond $schema.
	initial := []byte(`{
  "$schema": "http://example.com/schema.json",
  "alpha": "a",
  "beta": "b",
  "gamma": "g",
  "settings": {}
}
`)
	if err := os.WriteFile(path, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	// Patch only settings.
	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	patchSettings := config.Settings{}
	patchSettings.SetEcosystemManager("node", "bun")
	if err := config.Patch(path, settingsPatch{Settings: patchSettings}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// All original keys must be preserved.
	for _, key := range []string{`"alpha"`, `"beta"`, `"gamma"`, `"settings"`} {
		if !strings.Contains(content, key) {
			t.Errorf("Patch removed key %s from output", key)
		}
	}

	// $schema must remain the first key.
	if !strings.HasPrefix(strings.TrimSpace(content), `{`+"\n"+`  "$schema"`) {
		t.Errorf("$schema is not first key after multi-key patch; got:\n%s", content[:min(len(content), 120)])
	}
}

func TestPatch_SchemaFirstAfterManyKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write a full config with many keys.
	initialSettings := config.Settings{DotsRepo: "~/dots"}
	initialSettings.SetEcosystemManager("node", "bun")
	initial := &config.RootConfig{
		Settings: initialSettings,
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "ripgrep", Provider: "brew"},
				{Name: "black", Provider: "pip"},
			},
		}},
	}
	if err := config.Save(path, initial); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Patch settings only.
	type settingsPatch struct {
		Settings config.Settings `json:"settings"`
	}
	patchSettings := config.Settings{}
	patchSettings.SetEcosystemManager("node", "pnpm")
	if err := config.Patch(path, settingsPatch{Settings: patchSettings}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// $schema must be the first non-whitespace key regardless of how many keys exist.
	if !strings.HasPrefix(strings.TrimSpace(content), `{`+"\n"+`  "$schema"`) {
		t.Errorf("$schema is not first key after patch with many keys; got:\n%s", content[:min(len(content), 120)])
	}
	// Groups must be preserved.
	if !strings.Contains(content, `"ripgrep"`) {
		t.Error("groups key lost after patch")
	}
	// Updated value must be present.
	if !strings.Contains(content, `"pnpm"`) {
		t.Error("patched node manager value not found")
	}
}

// ─── DefaultCacheDir ──────────────────────────────────────────────────────────

func TestDefaultCacheDir_OmniCacheDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMNI_CACHE_DIR", dir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	got, err := config.DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir: %v", err)
	}
	if got != dir {
		t.Errorf("DefaultCacheDir = %q, want %q (OMNI_CACHE_DIR)", got, dir)
	}
}

func TestDefaultCacheDir_UsesXDGCacheHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMNI_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", dir)
	got, err := config.DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Errorf("dir %q does not start with XDG_CACHE_HOME %q", got, dir)
	}
	if filepath.Base(got) != "omni" {
		t.Errorf("dir %q should end in 'omni'", got)
	}
}

func TestDefaultCacheDir_FallsBackToHomeCache(t *testing.T) {
	t.Setenv("OMNI_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := config.DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir: %v", err)
	}
	if !strings.Contains(got, filepath.Join(".cache", "omni")) {
		t.Errorf("dir %q does not contain .cache/omni", got)
	}
}

// ─── DefaultConfigPath ────────────────────────────────────────────────────────

func TestDefaultConfigPath_OmniConfigOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("OMNI_CONFIG", want)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, err := config.DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

func TestDefaultConfigPath_RejectsLiveOverrideInLocalTests(t *testing.T) {
	if testguard.Isolated() {
		t.Skip("Docker-isolated tests do not enforce local live-path rejection")
	}
	t.Setenv("OMNI_CONFIG", "/custom/path/settings.json")
	if _, err := config.DefaultConfigPath(); err == nil {
		t.Fatal("DefaultConfigPath accepted live OMNI_CONFIG path in local test")
	}
}

func TestDefaultConfigPath_UsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMNI_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", dir)

	p, err := config.DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if !strings.HasPrefix(p, dir) {
		t.Errorf("path %q does not start with XDG_CONFIG_HOME %q", p, dir)
	}
	if !strings.HasSuffix(p, "settings.json") {
		t.Errorf("path %q does not end with settings.json", p)
	}
}

func TestDefaultConfigPath_FallsBackToHomeConfig(t *testing.T) {
	t.Setenv("OMNI_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	p, err := config.DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if !strings.Contains(p, filepath.Join(".config", "omni", "settings.json")) {
		t.Errorf("path %q does not contain .config/omni/settings.json", p)
	}
}

// ─── DotEntry ─────────────────────────────────────────────────────────────────

func TestDotEntry_Path_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{
				Dots: []config.DotEntry{
					{Name: "nvim", Path: "~/.config/nvim"},
					{
						Name:    "zsh",
						Path:    "~/.zshrc",
						Package: "zsh-default",
						Hosts: map[string]config.DotVariant{
							"work": {Package: "zsh-work"},
						},
						Ignore: []string{"*.zwc"},
					},
				},
			},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dots := loaded.Groups[0].Dots
	if len(dots) != 2 {
		t.Fatalf("got %d dots, want 2", len(dots))
	}
	if dots[0].Name != "nvim" || dots[0].Path != "~/.config/nvim" {
		t.Errorf("dots[0] = %+v", dots[0])
	}
	if dots[1].Name != "zsh" || dots[1].Path != "~/.zshrc" || dots[1].Package != "zsh-default" || dots[1].Hosts["work"].Package != "zsh-work" || len(dots[1].Ignore) != 1 {
		t.Errorf("dots[1] = %+v", dots[1])
	}
}
