package app

import (
	"path/filepath"
	"testing"
)

// The registered name comes from the marketplace's own manifest, so it need not match the repo.
const registryIndex = `{"marketplaces":[
  {"name":"superpowers-dev","url":"https://github.com/obra/superpowers","owner":"obra","repo":"superpowers"},
  {"name":"caveman","url":"https://github.com/JuliusBrussee/caveman","owner":"JuliusBrussee","repo":"caveman"}
]}`

// Real catalogs spell author as an object; older ones use a bare string. The catalog names itself,
// which is how a hashed url-marketplace filename is resolved back to its registered entry.
const superpowersCatalog = `{"name":"superpowers-dev","owner":{"name":"obra"},"plugins":[
  {"name":"superpowers","description":"skills","version":"6.3.0","source":"./"},
  {"name":"brainstorming","description":"ideas","author":{"name":"Jesse"},"source":{"source":"git-subdir","url":"https://github.com/obra/superpowers","path":"plugins/brainstorming"}}
]}`

const cavemanCatalog = `{"name":"caveman","owner":{"name":"Julius Brussee"},"plugins":[
  {"name":"caveman","description":"Talk like caveman.","author":"Julius","source":"./"}
]}`

func setupRegistry(t *testing.T, index, lock string, catalogs map[string]string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if index != "" {
		writeFile(t, filepath.Join(home, ".apm", "marketplaces.json"), index)
	}
	for file, catalog := range catalogs {
		writeFile(t, filepath.Join(home, ".apm", "cache", "marketplace", file+".json"), catalog)
	}
	if lock != "" {
		writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), lock)
	}
	return New(filepath.Join(home, "settings.json"))
}

func registryEntry(t *testing.T, entries []AgentsRegistryEntry, name string) AgentsRegistryEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("no entry named %q in %#v", name, entries)
	return AgentsRegistryEntry{}
}

func TestAgentsRegistryParsesEveryMarketplaceCache(t *testing.T) {
	a := setupRegistry(t, registryIndex, "dependencies: []\n", map[string]string{
		"superpowers-dev": superpowersCatalog,
		"caveman":         cavemanCatalog,
	})
	entries, notices, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("notices = %v", notices)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %#v", entries)
	}
	sp := registryEntry(t, entries, "superpowers")
	if sp.Marketplace != "superpowers-dev" || sp.Version != "6.3.0" || sp.Author != "obra" {
		t.Fatalf("entry = %+v", sp)
	}
	if sp.Spec() != "superpowers@superpowers-dev" {
		t.Fatalf("spec = %q", sp.Spec())
	}
	if got := registryEntry(t, entries, "caveman").Author; got != "Julius" {
		t.Fatalf("string author = %q", got)
	}
	if got := registryEntry(t, entries, "brainstorming").Author; got != "Jesse" {
		t.Fatalf("object author = %q", got)
	}
	if got := registryEntry(t, entries, "superpowers").Author; got != "obra" {
		t.Fatalf("owner fallback = %q", got)
	}
	for _, entry := range entries {
		if entry.Installed {
			t.Fatalf("nothing is locked yet: %+v", entry)
		}
	}
}

func TestAgentsRegistryMarksInstalledByPluginAndByRepo(t *testing.T) {
	lock := `dependencies:
- repo_url: juliusbrussee/caveman
  name: caveman
  package_type: marketplace_plugin
  discovered_via: caveman
  marketplace_plugin_name: caveman
- repo_url: obra/superpowers
  name: superpowers
`
	a := setupRegistry(t, registryIndex, lock, map[string]string{
		"superpowers-dev": superpowersCatalog,
		"caveman":         cavemanCatalog,
	})
	entries, _, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if !registryEntry(t, entries, "caveman").Installed {
		t.Fatal("plugin-name join missed the marketplace install")
	}
	// Directly installed: no marketplace provenance in the lock, only the repo.
	if !registryEntry(t, entries, "superpowers").Installed {
		t.Fatal("repo fallback join missed the direct install")
	}
	if registryEntry(t, entries, "brainstorming").Installed {
		t.Fatal("repo fallback marked a sibling plugin installed")
	}
}

// The real fleet lock leaves discovered_via null even for marketplace plugins installed from a subdirectory.
func TestAgentsRegistryMarksSubdirectoryPluginsByLockedName(t *testing.T) {
	index := `{"marketplaces":[{"name":"official","owner":"anthropics","repo":"claude-plugins-official"}]}`
	catalog := `{"name":"official","owner":{"name":"anthropic"},"plugins":[
	  {"name":"claude-md-management","description":"a","source":{"path":"plugins/claude-md-management"}},
	  {"name":"code-simplifier","description":"b","source":{"path":"plugins/code-simplifier"}},
	  {"name":"never-installed","description":"c"}
	]}`
	lock := `dependencies:
- repo_url: anthropics/claude-plugins-official
  name: claude-md-management
  virtual_path: plugins/claude-md-management
  package_type: marketplace_plugin
`
	a := setupRegistry(t, index, lock, map[string]string{"official": catalog})
	entries, _, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if !registryEntry(t, entries, "claude-md-management").Installed {
		t.Fatal("subdirectory plugin not marked installed")
	}
	for _, name := range []string{"code-simplifier", "never-installed"} {
		if registryEntry(t, entries, name).Installed {
			t.Fatalf("%s wrongly marked installed", name)
		}
	}
}

func TestAgentsRegistryReportsAMissingCache(t *testing.T) {
	a := setupRegistry(t, registryIndex, "dependencies: []\n", nil)
	entries, notices, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
	if len(notices) != 1 || notices[0] != AgentsRegistryEmptyNotice {
		t.Fatalf("notices = %v", notices)
	}
}

func TestAgentsRegistryWithoutMarketplacesIsEmpty(t *testing.T) {
	a := setupRegistry(t, "", "", nil)
	entries, notices, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || len(notices) != 1 {
		t.Fatalf("entries = %#v notices = %v", entries, notices)
	}
}

// APM hashes a url marketplace's cache filename, so the file is found by the name the catalog carries.
func TestAgentsRegistryResolvesAHashedCacheFilename(t *testing.T) {
	index := `{"marketplaces":[{"name":"litellm","url":"https://api.invalid/marketplace.json","path":""}]}`
	catalog := `{"name":"litellm","owner":{"name":"h"},"plugins":[
	  {"name":"harness","version":"1.2.0","source":{"source":"github","repo":"revfactory/harness"}},
	  {"name":"smart-docs","version":"1.5.0","source":{"source":"git-subdir","url":"https://github.com/sopaco/deepwiki-rs","path":"skills/smart-docs"}}
	]}`
	lock := "dependencies:\n- repo_url: revfactory/harness\n  name: harness\n- repo_url: sopaco/deepwiki-rs\n  name: deepwiki-rs\n"
	a := setupRegistry(t, index, lock, map[string]string{"url__10d28ebf49c59062": catalog})
	entries, notices, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("a resolvable cache produced a notice: %v", notices)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	// The plugin is a whole repo the lock already holds.
	if !registryEntry(t, entries, "harness").Installed {
		t.Fatal("per-plugin repo join missed an installed package")
	}
	// A subdirectory plugin shares its repo with siblings, so the repo alone must not mark it.
	if registryEntry(t, entries, "smart-docs").Installed {
		t.Fatal("subdirectory plugin marked installed by its parent repo")
	}
}

func TestAgentsRegistryNoticesOnlyForUnresolvableMarketplaces(t *testing.T) {
	index := `{"marketplaces":[
	  {"name":"caveman","owner":"JuliusBrussee","repo":"caveman"},
	  {"name":"uncached","owner":"acme","repo":"uncached"}
	]}`
	a := setupRegistry(t, index, "dependencies: []\n", map[string]string{
		"caveman": cavemanCatalog,
		"stray":   `{"name":"stray","plugins":[{"name":"unregistered"}]}`,
	})
	entries, notices, err := a.AgentsRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "caveman" {
		t.Fatalf("entries = %#v", entries)
	}
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one for the uncached marketplace", notices)
	}
}
