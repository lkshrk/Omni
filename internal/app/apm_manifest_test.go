package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

func renderForTest(t *testing.T, existing string, plan apmManifestPlan) (apmManifestRender, map[string]any) {
	t.Helper()
	render, err := renderAPMManifest([]byte(existing), plan)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(render.Content, &doc); err != nil {
		t.Fatalf("rendered manifest %q: %v", render.Content, err)
	}
	return render, doc
}

func manifestSurface(t *testing.T, doc map[string]any, key string) []any {
	t.Helper()
	deps, ok := doc["dependencies"].(map[string]any)
	if !ok {
		t.Fatalf("dependencies = %#v, want a mapping", doc["dependencies"])
	}
	if deps[key] == nil {
		return nil
	}
	list, ok := deps[key].([]any)
	if !ok {
		t.Fatalf("dependencies.%s = %#v, want a sequence", key, deps[key])
	}
	return list
}

func TestRenderAPMManifestSuppliesTheRequiredHeaderForAnEmptyPlan(t *testing.T) {
	render, doc := renderForTest(t, "", apmManifestPlan{})
	if doc["name"] != apmManifestDefaultName || doc["version"] != apmManifestDefaultVersion {
		t.Fatalf("manifest = %#v, want the required name/version", doc)
	}
	if render.PackageCount != 0 || render.McpCount != 0 {
		t.Fatalf("render = %#v, want empty surfaces", render)
	}
	if got := manifestSurface(t, doc, "apm"); len(got) != 0 {
		t.Fatalf("apm = %#v, want an empty sequence", got)
	}
}

func TestRenderAPMManifestKeepsAnExistingHeaderAndUnknownTopLevelKeys(t *testing.T) {
	existing := "name: mine\nversion: 2.3.4\nregistries:\n  default: https://registry.example\n"
	_, doc := renderForTest(t, existing, apmManifestPlan{})
	if doc["name"] != "mine" || doc["version"] != "2.3.4" {
		t.Fatalf("manifest = %#v, want the author's header preserved", doc)
	}
	registries, ok := doc["registries"].(map[string]any)
	if !ok || registries["default"] != "https://registry.example" {
		t.Fatalf("registries = %#v, want it preserved", doc["registries"])
	}
}

func TestRenderAPMManifestPreservesForeignEntriesAndRegeneratesManagedOnes(t *testing.T) {
	existing := `name: mine
version: 1.0.0
dependencies:
  apm:
    - someone/else#pinned
    - git: acme/managed
      ref: old
      targets: [codex]
    - git: acme/dropped
  mcp:
    - name: foreign
      registry: false
      transport: http
      url: https://foreign.example/mcp
    - name: managed
      registry: false
      transport: stdio
      command: old
`
	plan := apmManifestPlan{
		Packages:        []apmPackageDependency{{Git: "acme/managed", Ref: "new", Targets: []string{"claude"}}},
		Mcp:             []apmMcpDependency{{Name: "managed", Transport: "stdio", Command: "new", Args: []string{"--flag"}}},
		ManagedPackages: map[string]bool{"acme/managed": true, "acme/dropped": true},
		ManagedMcp:      map[string]bool{"managed": true},
	}
	render, doc := renderForTest(t, existing, plan)

	wantAPM := []any{
		"someone/else#pinned",
		map[string]any{"git": "acme/managed", "ref": "new", "targets": []any{"claude"}},
	}
	if got := manifestSurface(t, doc, "apm"); !reflect.DeepEqual(got, wantAPM) {
		t.Fatalf("apm = %#v, want %#v", got, wantAPM)
	}
	wantMcp := []any{
		map[string]any{"name": "foreign", "registry": false, "transport": "http", "url": "https://foreign.example/mcp"},
		map[string]any{"name": "managed", "registry": false, "transport": "stdio", "command": "new", "args": []any{"--flag"}},
	}
	if got := manifestSurface(t, doc, "mcp"); !reflect.DeepEqual(got, wantMcp) {
		t.Fatalf("mcp = %#v, want %#v", got, wantMcp)
	}
	if render.PackageCount != 2 || render.McpCount != 2 {
		t.Fatalf("render = %#v, want two entries per surface", render)
	}
}

// A regenerated MCP entry may only carry schema keys: an unknown one is swept into APM's extra passthrough
// and flattened verbatim into every deployed agent config.
func TestRenderAPMManifestEmitsOnlySchemaKeysOnMcpEntries(t *testing.T) {
	plan := apmManifestPlan{
		Mcp: []apmMcpDependency{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"}},
	}
	_, doc := renderForTest(t, "", plan)
	entry, ok := manifestSurface(t, doc, "mcp")[0].(map[string]any)
	if !ok {
		t.Fatalf("mcp entry = %#v, want a mapping", manifestSurface(t, doc, "mcp")[0])
	}
	allowed := map[string]bool{
		"name": true, "registry": true, "transport": true, "command": true,
		"args": true, "url": true, "env": true, "headers": true,
	}
	for key := range entry {
		if !allowed[key] {
			t.Fatalf("mcp entry carries unknown key %q: %#v", key, entry)
		}
	}
	if _, present := entry["targets"]; present {
		t.Fatalf("mcp entry declares targets, which has no scoping effect: %#v", entry)
	}
	if entry["registry"] != false {
		t.Fatalf("mcp entry = %#v, want registry: false for a self-defined server", entry)
	}
}

func TestRenderAPMManifestLeavesTheMcpSurfaceAloneWhenSkipped(t *testing.T) {
	existing := "name: mine\nversion: 1.0.0\ndependencies:\n  mcp:\n    - name: managed\n      registry: false\n      transport: stdio\n      command: keep\n"
	plan := apmManifestPlan{ManagedMcp: map[string]bool{"managed": true}, SkipMcp: true}
	render, doc := renderForTest(t, existing, plan)

	entry := manifestSurface(t, doc, "mcp")[0].(map[string]any)
	if entry["command"] != "keep" {
		t.Fatalf("mcp = %#v, want the surface untouched", entry)
	}
	if render.McpCount != 1 {
		t.Fatalf("render = %#v, want the existing entry counted", render)
	}
}

func TestRenderAPMManifestRejectsANonMappingManifest(t *testing.T) {
	if _, err := renderAPMManifest([]byte("- a\n- b\n"), apmManifestPlan{}); err == nil {
		t.Fatal("expected an error for a manifest that is not a mapping")
	}
}

// The APM CLI reindents the whole file on its own installs; a text diff would report that as churn.
func TestAPMManifestEquivalenceIsSemantic(t *testing.T) {
	flat := "dependencies:\n  apm:\n  - git: acme/one\n    targets:\n    - claude\n"
	indented := "dependencies:\n  apm:\n    - git: acme/one\n      targets:\n        - claude\n"
	if !apmManifestEquivalent([]byte(flat), []byte(indented)) {
		t.Fatal("reindented manifests compared as different")
	}
	if apmManifestEquivalent([]byte(flat), []byte("dependencies:\n  apm:\n  - git: acme/two\n")) {
		t.Fatal("different manifests compared as equal")
	}
}

func TestWriteAPMManifestCreatesTheFileAndLeavesEquivalentContentAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".apm", "apm.yml")
	if err := writeAPMManifest(path, []byte("name: mine\nversion: 1.0.0\ndependencies:\n  apm:\n  - acme/one\n")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reindented := "name: mine\nversion: 1.0.0\ndependencies:\n  apm:\n    - acme/one\n"
	if err := writeAPMManifest(path, []byte(reindented)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("manifest rewritten for a semantically identical render:\n%s\n%s", before, after)
	}
	if err := writeAPMManifest(path, []byte("name: mine\nversion: 1.0.0\ndependencies:\n  apm: []\n")); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), "acme/one") {
		t.Fatalf("manifest = %s, want the changed render written", updated)
	}
}

// The applied half of the ownership record is the surface install's to advance: advancing it here would drop
// a pruned identity the install never retired, leaving nothing for the next sync to retry from. The rendered
// half is the manifest write's own, so a config entry whose install never succeeds is still omni's to retire.
func TestConvergeAPMManifestLeavesTheOwnershipRecordToTheSurfaceInstall(t *testing.T) {
	home := t.TempDir()
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeAPMOwnedIdentities(manifestPath, appliedOwned([]string{"acme/retired"}, []string{"linear"})); err != nil {
		t.Fatal(err)
	}

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/added"}}},
	}
	converged, err := a.convergeAPMManifest(cfg, manifestPath, apmConvergePlan{
		Packages:     []apmPackageDependency{{Git: "acme/added"}},
		SyncPackages: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if !reflect.DeepEqual(converged.PendingOwned.Packages, []string{"acme/added"}) {
		t.Fatalf("pending packages = %v", converged.PendingOwned.Packages)
	}
	owned := readAPMOwnedIdentities(manifestPath)
	if !reflect.DeepEqual(owned.Applied.Packages, []string{"acme/retired"}) || !reflect.DeepEqual(owned.Applied.Mcp, []string{"linear"}) {
		t.Fatalf("applied record = %+v, want it untouched until the install succeeds", owned.Applied)
	}
	if !reflect.DeepEqual(owned.Rendered.Packages, []string{"acme/added"}) {
		t.Fatalf("rendered packages = %v, want the manifest write recorded", owned.Rendered.Packages)
	}
	if !reflect.DeepEqual(owned.Rendered.Mcp, []string{"linear"}) {
		t.Fatalf("rendered mcp = %v, want the surface this converge left alone untouched", owned.Rendered.Mcp)
	}

	if err := advanceAPMApplied(manifestPath, apm.SurfacePackages, converged.PendingOwned.Packages); err != nil {
		t.Fatal(err)
	}
	advanced := readAPMOwnedIdentities(manifestPath)
	if !reflect.DeepEqual(advanced.Applied.Packages, []string{"acme/added"}) {
		t.Fatalf("packages = %v, want the succeeded surface advanced", advanced.Applied.Packages)
	}
	if !reflect.DeepEqual(advanced.Applied.Mcp, []string{"linear"}) {
		t.Fatalf("mcp = %v, want the other surface left where its own install put it", advanced.Applied.Mcp)
	}
}
