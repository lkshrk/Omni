package config_test

import (
	"encoding/json"
	"fmt"
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
	ripgrepProviders := cfg.Tools["ripgrep"].Providers
	if len(ripgrepProviders) != 1 || ripgrepProviders[0].Provider != "brew" {
		t.Errorf("ripgrep providers = %+v, want brew", ripgrepProviders)
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

func TestCurrentVersionIncludesGitHubFallbackReleaseMetadata(t *testing.T) {
	const wantVersion = 4
	if config.CurrentVersion < wantVersion {
		t.Fatalf("CurrentVersion = %d, want at least %d for GitHub fallback release metadata", config.CurrentVersion, wantVersion)
	}
}

func TestFallbackRecipeGitHubReleaseMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Provider: "system",
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
					Status: config.FallbackStatusUnverified,
					Binary: "gh",
					Recipe: config.FallbackRecipe{
						Type:             config.FallbackRecipeGitHubReleaseAsset,
						AssetPattern:     "gh_2.93.0_macOS_arm64.zip",
						ReleaseID:        "330388700",
						TagName:          "v2.93.0",
						PublishedAt:      "2026-05-27T17:47:41Z",
						AssetID:          "431301800",
						AssetName:        "gh_2.93.0_macOS_arm64.zip",
						AssetDownloadURL: "https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_macOS_arm64.zip",
					},
				},
			},
		},
		Groups: []*config.GroupConfig{{Tools: []config.ToolEntry{{Name: "gh"}}}},
	}

	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	recipe := loaded.Tools["gh"].Fallback.Recipe
	if recipe.ReleaseID != "330388700" {
		t.Fatalf("release_id = %q, want 330388700", recipe.ReleaseID)
	}
	if recipe.TagName != "v2.93.0" {
		t.Fatalf("tag_name = %q, want v2.93.0", recipe.TagName)
	}
	if recipe.PublishedAt != "2026-05-27T17:47:41Z" {
		t.Fatalf("published_at = %q, want GitHub release published_at", recipe.PublishedAt)
	}
	if recipe.AssetID != "431301800" {
		t.Fatalf("asset_id = %q, want 431301800", recipe.AssetID)
	}
	if recipe.AssetName != "gh_2.93.0_macOS_arm64.zip" {
		t.Fatalf("asset_name = %q, want gh_2.93.0_macOS_arm64.zip", recipe.AssetName)
	}
	if recipe.AssetDownloadURL != "https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_macOS_arm64.zip" {
		t.Fatalf("asset_download_url = %q, want matched GitHub asset URL", recipe.AssetDownloadURL)
	}
}

func TestLoad_MigratesOldFallbackWithoutSynthesizingGitHubReleaseMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "version": 3,
  "tools": {
    "gh": {
      "provider": "system",
      "fallback": {
        "source": {"type": "github", "owner": "cli", "repo": "cli"},
        "status": "unverified",
        "binary": "gh",
        "recipe": {
          "type": "github_release_asset",
          "asset_pattern": "gh_2.93.0_macOS_arm64.zip"
        },
        "commands": {
          "check": "command -v gh"
        }
      }
    }
  },
  "groups": [{"tools": ["gh"]}]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != config.CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
	recipe := cfg.Tools["gh"].Fallback.Recipe
	if recipe.ReleaseID != "" || recipe.TagName != "" || recipe.PublishedAt != "" || recipe.AssetID != "" || recipe.AssetName != "" || recipe.AssetDownloadURL != "" {
		t.Fatalf("legacy fallback metadata = %+v, want no synthesized GitHub release metadata", recipe)
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

func TestNormalizeFile_PersistsLegacyToolProviderMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{
  "version": 5,
  "tools": {
    "docker": {
      "provider": "brew",
      "package": "docker-desktop",
      "options": {"brew_kind": "cask"}
    }
  },
  "groups": [
    {"name": "system", "tools": ["docker"]}
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
		t.Fatal("NormalizeFile changed = false, want true for legacy provider migration")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{`"providers": [`, `"provider": "brew"`, `"package": "docker-desktop"`, `"brew_kind": "cask"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("NormalizeFile missing %s in migrated config:\n%s", want, content)
		}
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load migrated config: %v", err)
	}
	docker := loaded.Tools["docker"]
	if docker.Provider != "" || docker.Package != "" || docker.Options != nil {
		t.Fatalf("docker legacy fields = provider %q package %q options %v, want empty", docker.Provider, docker.Package, docker.Options)
	}
	if len(docker.Providers) != 1 || docker.Providers[0].Provider != "brew" || docker.Providers[0].Package != "docker-desktop" || docker.Providers[0].Options["brew_kind"] != "cask" {
		t.Fatalf("docker providers = %+v, want brew/docker-desktop cask", docker.Providers)
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
	expectedProviders := map[string][]config.ToolInstallSpec{
		"ripgrep": {{Provider: "brew"}},
		"black":   {{Provider: "pip", Package: "black"}},
		// No cheap local evidence is available for node without install_with.
		"typescript": {},
	}
	for i, w := range want.Tools {
		g := got.Tools[i]
		if g.Name != w.Name || g.Provider != "" || g.Package != "" {
			t.Errorf("membership[%d]: got %+v, want logical name %q", i, g, w.Name)
		}
		spec := loaded.Tools[w.Name]
		wantProviders := expectedProviders[w.Name]
		if len(spec.Providers) != len(wantProviders) {
			t.Errorf("spec[%q]: providers = %+v, want %+v", w.Name, spec.Providers, wantProviders)
			continue
		}
		for j, wantProvider := range wantProviders {
			if spec.Providers[j].Provider != wantProvider.Provider || spec.Providers[j].Package != wantProvider.Package {
				t.Errorf("spec[%q].providers[%d] = %+v, want %+v", w.Name, j, spec.Providers[j], wantProvider)
			}
		}
	}

	// Modify, save again, reload.
	loaded.Tools["jq"] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "brew"}}}
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

func TestAgentsIgnore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := &config.RootConfig{
		Agents: config.AgentsConfig{
			Ignore: config.AgentsIgnore{
				Skills:     []string{"vercel-labs/agent-skills"},
				McpServers: []string{"context7"},
				Plugins:    []string{"my-plugin"},
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

	if len(loaded.Agents.Ignore.Skills) != 1 || loaded.Agents.Ignore.Skills[0] != "vercel-labs/agent-skills" {
		t.Errorf("Ignore.Skills = %v, want [vercel-labs/agent-skills]", loaded.Agents.Ignore.Skills)
	}
	if len(loaded.Agents.Ignore.McpServers) != 1 || loaded.Agents.Ignore.McpServers[0] != "context7" {
		t.Errorf("Ignore.McpServers = %v, want [context7]", loaded.Agents.Ignore.McpServers)
	}
	if len(loaded.Agents.Ignore.Plugins) != 1 || loaded.Agents.Ignore.Plugins[0] != "my-plugin" {
		t.Errorf("Ignore.Plugins = %v, want [my-plugin]", loaded.Agents.Ignore.Plugins)
	}
}

func TestAgentsIgnore_EmptyRoundTripsAsEmptyObject(t *testing.T) {
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
	agentsIdx := strings.Index(string(data), `"agents"`)
	if agentsIdx == -1 {
		t.Fatalf("expected agents key in written JSON: %s", data)
	}
	if !strings.Contains(string(data)[agentsIdx:], `"ignore": {}`) {
		t.Errorf("empty agents.ignore should round-trip as an empty object, matching top-level ignore: %s", data)
	}
}

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
		AgentsDisabled:    &disabled,
		SkillsDisabled:    &disabled,
		McpDisabled:       &disabled,
		PluginsDisabled:   &disabled,
		DisabledProviders: []string{"node"},
		DotsGit: config.DotsGitConfig{
			AutoCommit: true,
			AutoPush:   true,
		},
	}
	settings.ProviderPriority = append([]string{"pnpm"}, settings.ProviderPriority...)
	settings.Ecosystems = map[string]config.EcosystemSettings{
		"system": {Priority: []string{"brew", "apt"}},
	}

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
		"agents_disabled",
		"skills_disabled",
		"mcp_disabled",
		"plugins_disabled",
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
	initialSettings.ProviderPriority = append([]string{"bun"}, initialSettings.ProviderPriority...)
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
	newSettings.ProviderPriority = append([]string{"pnpm"}, newSettings.ProviderPriority...)
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
	if got := loaded.Settings.ProviderPriority; len(got) == 0 || got[0] != "pnpm" {
		t.Errorf("provider_priority = %v, want pnpm first (node manager)", got)
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
	patchSettings.ProviderPriority = append([]string{"bun"}, patchSettings.ProviderPriority...)
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
	patchSettings.ProviderPriority = append([]string{"bun"}, patchSettings.ProviderPriority...)
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
	initialSettings.ProviderPriority = append([]string{"bun"}, initialSettings.ProviderPriority...)
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
	patchSettings.ProviderPriority = append([]string{"pnpm"}, patchSettings.ProviderPriority...)
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

func TestMcpServerValidation(t *testing.T) {
	cases := []struct {
		name    string
		servers []config.McpServer
		wantErr bool
		errFrag string
	}{
		{
			name:    "valid stdio",
			servers: []config.McpServer{{Name: "x", Transport: "stdio", Command: "npx foo"}},
		},
		{
			name:    "valid http",
			servers: []config.McpServer{{Name: "x", Transport: "http", URL: "https://example.com"}},
		},
		{
			name:    "valid sse",
			servers: []config.McpServer{{Name: "x", Transport: "sse", URL: "https://example.com/sse"}},
		},
		{
			name:    "stdio missing command",
			servers: []config.McpServer{{Name: "x", Transport: "stdio"}},
			wantErr: true, errFrag: "command",
		},
		{
			name:    "stdio with url",
			servers: []config.McpServer{{Name: "x", Transport: "stdio", Command: "npx x", URL: "https://x.com"}},
			wantErr: true, errFrag: "url",
		},
		{
			name:    "http missing url",
			servers: []config.McpServer{{Name: "x", Transport: "http"}},
			wantErr: true, errFrag: "url",
		},
		{
			name:    "http with command",
			servers: []config.McpServer{{Name: "x", Transport: "http", URL: "https://x.com", Command: "npx"}},
			wantErr: true, errFrag: "command",
		},
		{
			name:    "unknown transport",
			servers: []config.McpServer{{Name: "x", Transport: "grpc", Command: "npx"}},
			wantErr: true, errFrag: "transport",
		},
		{
			name:    "empty name",
			servers: []config.McpServer{{Name: "", Transport: "stdio", Command: "npx"}},
			wantErr: true, errFrag: "name",
		},
		{
			name: "duplicate name",
			servers: []config.McpServer{
				{Name: "dup", Transport: "stdio", Command: "npx x"},
				{Name: "dup", Transport: "stdio", Command: "npx y"},
			},
			wantErr: true, errFrag: "duplicate",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := &config.RootConfig{
				Version: config.CurrentVersion,
				Agents:  config.AgentsConfig{McpServers: c.servers},
			}
			errs := config.ValidateRoot(root, config.ProviderValidation{})
			if c.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected validation error, got none")
				}
				combined := fmt.Sprintf("%v", errs)
				if !strings.Contains(combined, c.errFrag) {
					t.Fatalf("expected %q in errors, got: %v", c.errFrag, errs)
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestMcpServerValidation_EmptyEnvName(t *testing.T) {
	root := &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "x", Transport: "stdio", Command: "npx x", Env: []string{""}},
		}},
	}
	errs := config.ValidateRoot(root, config.ProviderValidation{})
	if len(errs) == 0 {
		t.Fatal("expected validation error for empty env name, got none")
	}
	combined := fmt.Sprintf("%v", errs)
	if !strings.Contains(combined, "env") {
		t.Fatalf("expected error mentioning env, got: %v", errs)
	}
}

func TestMcpServerValidation_WhitespaceEnvName(t *testing.T) {
	root := &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "x", Transport: "stdio", Command: "npx x", Env: []string{"   "}},
		}},
	}
	errs := config.ValidateRoot(root, config.ProviderValidation{})
	if len(errs) == 0 {
		t.Fatal("expected validation error for whitespace-only env name, got none")
	}
}

func TestGroupMcpServerRef_ValidRef_NoWarning(t *testing.T) {
	root := &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://example.com"},
		}},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"linear"}},
		},
	}
	errs := config.ValidateRoot(root, config.ProviderValidation{})
	for _, e := range errs {
		if strings.Contains(e.Message, "mcp_server ref") {
			t.Fatalf("unexpected mcp_server ref warning for valid ref: %v", e)
		}
	}
}

func TestGroupMcpServerRef_UnknownRef_IsWarn(t *testing.T) {
	root := &config.RootConfig{
		Version: config.CurrentVersion,
		Agents:  config.AgentsConfig{},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"does-not-exist"}},
		},
	}
	errs := config.ValidateRoot(root, config.ProviderValidation{})
	var found *config.ValidationError
	for i := range errs {
		if strings.Contains(errs[i].Message, "mcp_server ref") {
			found = &errs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected mcp_server ref warning, got none")
	}
	if !found.Warn {
		t.Fatalf("expected Warn=true for unknown mcp_server ref, got Warn=false: %+v", *found)
	}
}

func TestExpandGroupAgentRefs_ExpandsShorthandAndDedupes(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Packages:     []config.SkillPackage{{Source: "vercel-labs/agent-skills"}},
			McpServers:   []config.McpServer{{Name: "gh", Transport: "stdio", Command: "gh"}},
			Plugins:      []config.Plugin{{Name: "useful", Marketplace: "lkshrk"}},
			Marketplaces: []config.Marketplace{{Name: "lkshrk", Source: "https://example.com"}},
		},
		Groups: []*config.GroupConfig{{
			Name:         "work",
			Skills:       []string{config.AgentRefPackages, "vercel-labs/agent-skills"},
			McpServers:   []string{config.AgentRefMcpServers},
			Plugins:      []string{config.AgentRefPlugins},
			Marketplaces: []string{config.AgentRefMarketplaces},
		}},
	}
	if !config.ExpandGroupAgentRefs(cfg) {
		t.Fatal("ExpandGroupAgentRefs = false, want true")
	}
	group := cfg.Groups[0]
	if len(group.Skills) != 1 || group.Skills[0] != "vercel-labs/agent-skills" {
		t.Fatalf("skills = %v, want deduped package source", group.Skills)
	}
	if len(group.McpServers) != 1 || group.McpServers[0] != "gh" {
		t.Fatalf("mcp_servers = %v, want expanded gh", group.McpServers)
	}
	if len(group.Plugins) != 1 || group.Plugins[0] != "useful" {
		t.Fatalf("plugins = %v, want expanded useful", group.Plugins)
	}
	if len(group.Marketplaces) != 1 || group.Marketplaces[0] != "lkshrk" {
		t.Fatalf("marketplaces = %v, want expanded lkshrk", group.Marketplaces)
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	for _, err := range errs {
		if strings.Contains(err.Path, ".skills[") || strings.Contains(err.Path, ".mcp_servers[") {
			t.Fatalf("unexpected validation error for expanded refs: %v", err)
		}
	}
}
