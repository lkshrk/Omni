package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// TestSchemaCoversEveryConfigField prevents additionalProperties:false from rejecting newly added config fields.
func TestSchemaCoversEveryConfigField(t *testing.T) {
	if problems := schemaDrift(t, build()); len(problems) > 0 {
		t.Fatalf("schema/struct drift:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestSchemaDriftIsDetected prevents the drift check from passing vacuously.
func TestSchemaDriftIsDetected(t *testing.T) {
	doc := build()
	delete(doc.Defs["FallbackRecipe"].Properties, "installed_version")
	problems := schemaDrift(t, doc)
	if len(problems) == 0 {
		t.Fatal("removing FallbackRecipe.installed_version was not reported as drift")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "installed_version") {
		t.Fatalf("drift report does not name the removed property: %v", problems)
	}
}

func schemaDrift(t *testing.T, doc *schema) []string {
	t.Helper()
	w := &driftWalker{t: t, defs: doc.Defs, seen: map[string]bool{}}
	w.walk("$", reflect.TypeOf(config.RootConfig{}), doc)
	return w.problems
}

type driftWalker struct {
	t        *testing.T
	defs     map[string]*schema
	seen     map[string]bool
	problems []string
}

func (w *driftWalker) reportf(format string, args ...any) {
	w.problems = append(w.problems, fmt.Sprintf(format, args...))
}

func (w *driftWalker) resolve(node *schema) *schema {
	for node != nil && node.Ref != "" {
		name := strings.TrimPrefix(node.Ref, "#/$defs/")
		resolved := w.defs[name]
		if resolved == nil {
			w.t.Fatalf("unresolvable $ref %q", node.Ref)
		}
		node = resolved
	}
	return node
}

var jsonMarshaler = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

func (w *driftWalker) walk(path string, goType reflect.Type, node *schema) {
	node = w.resolve(node)
	if node == nil {
		w.reportf("%s: no schema for Go type %s", path, goType)
		return
	}
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	// A custom marshaler decides its own wire shape, so struct fields say nothing about the schema.
	if goType.Implements(jsonMarshaler) || reflect.PointerTo(goType).Implements(jsonMarshaler) {
		return
	}

	switch goType.Kind() {
	case reflect.Struct:
		// Include the resolved node because Settings and HostSettings independently cover config.Settings.
		key := fmt.Sprintf("%s|%p", goType, node)
		if w.seen[key] {
			return
		}
		w.seen[key] = true
		w.walkStruct(path, goType, node)
	case reflect.Slice, reflect.Array:
		if node.Items == nil {
			w.reportf("%s: schema for Go %s has no items", path, goType)
			return
		}
		w.walk(path+"[]", goType.Elem(), node.Items)
	case reflect.Map:
		child, ok := node.AdditionalProperties.(*schema)
		if !ok {
			w.reportf("%s: schema for Go map %s must set additionalProperties to a schema", path, goType)
			return
		}
		w.walk(path+".*", goType.Elem(), child)
	}
}

func (w *driftWalker) walkStruct(path string, goType reflect.Type, node *schema) {
	fields := jsonFields(goType)

	var missing, extra []string
	for name := range fields {
		if _, ok := node.Properties[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range node.Properties {
		if _, ok := fields[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		w.reportf("%s (%s): Go fields with no schema property: %v — omni writes these and the schema rejects them under additionalProperties:false", path, goType, missing)
	}
	if len(extra) > 0 {
		w.reportf("%s (%s): schema properties with no Go field: %v", path, goType, extra)
	}
	if node.AdditionalProperties != false {
		w.reportf("%s (%s): schema object must set additionalProperties:false", path, goType)
	}

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child := node.Properties[name]
		if child == nil {
			continue
		}
		w.walk(path+"."+name, fields[name], child)
	}
}

func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			for k, v := range jsonFields(f.Type) {
				out[k] = v
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

// TestCommittedSchemasMatchGenerator covers committed schemas because CI does not run gen-schema.
func TestCommittedSchemasMatchGenerator(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct {
		path string
		id   string
	}{
		{currentOutput(), config.SchemaURL},
		{latestOutput, latestSchemaID},
	} {
		want, err := encodeSchema(buildWithID(tc.id))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, tc.path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is stale; run `make gen-schema`", tc.path)
		}
	}
}

// TestPopulatedRootConfigValidatesAgainstCommittedSchema checks likely drift without adding a JSON Schema dependency.
func TestPopulatedRootConfigValidatesAgainstCommittedSchema(t *testing.T) {
	data, err := json.Marshal(fullyPopulatedRootConfig())
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), currentOutput()))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	v := &objectValidator{t: t, doc: doc}
	v.validate("$", instance, doc)
}

type objectValidator struct {
	t   *testing.T
	doc map[string]any
}

func (v *objectValidator) deref(node map[string]any) map[string]any {
	for {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node
		}
		defs, _ := v.doc["$defs"].(map[string]any)
		next, ok := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		if !ok {
			v.t.Fatalf("unresolvable $ref %q", ref)
		}
		node = next
	}
}

func (v *objectValidator) validate(path string, instance any, node map[string]any) {
	node = v.deref(node)
	switch value := instance.(type) {
	case map[string]any:
		props, _ := node["properties"].(map[string]any)
		additional, hasAdditional := node["additionalProperties"]
		for _, name := range sortedKeys(value) {
			child, ok := props[name].(map[string]any)
			if ok {
				v.validate(path+"."+name, value[name], child)
				continue
			}
			switch extra := additional.(type) {
			case map[string]any:
				v.validate(path+"."+name, value[name], extra)
			case bool:
				if !extra {
					v.t.Errorf("%s.%s: rejected by schema (additionalProperties:false)", path, name)
				}
			default:
				if hasAdditional {
					v.t.Errorf("%s.%s: unexpected additionalProperties schema %T", path, name, additional)
				}
			}
		}
		if required, ok := node["required"].([]any); ok {
			for _, name := range required {
				key, _ := name.(string)
				if _, ok := value[key]; !ok {
					v.t.Errorf("%s: missing required property %q", path, key)
				}
			}
		}
	case []any:
		items, ok := node["items"].(map[string]any)
		if !ok {
			return
		}
		for i, elem := range value {
			v.validate(fmt.Sprintf("%s[%d]", path, i), elem, items)
		}
	case string:
		if enum, ok := node["enum"].([]any); ok {
			for _, allowed := range enum {
				if allowed == value {
					return
				}
			}
			v.t.Errorf("%s: value %q not in enum %v", path, value, enum)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above working directory")
		}
		dir = parent
	}
}

func fullyPopulatedRootConfig() config.RootConfig {
	installSpec := config.ToolInstallSpec{
		Provider:    "brew",
		Package:     "ripgrep",
		Bin:         "rg",
		InstallWith: "brew",
		Options:     map[string]string{"head": "true"},
		Source:      &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep", URL: "https://github.com/BurntSushi/ripgrep"},
		Recipe:      &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "rg-{version}.tar.gz", BinaryPath: "rg", Checksum: "sha256:abc", ChecksumAssetID: "1", ReleaseID: "2", TagName: "v1.0.0", PublishedAt: "2024-01-01T00:00:00Z", AssetID: "3", AssetName: "rg.tar.gz", AssetDownloadURL: "https://example.com/rg.tar.gz", InstalledVersion: "1.0.0"},
		BinDir:      "~/.local/bin",
	}
	settings := config.Settings{
		AutoImport:               true,
		UpdateQuarantine:         "2d",
		ProviderUpdateQuarantine: map[string]string{"brew": "7d"},
		Ecosystems:               map[string]config.EcosystemSettings{"system": {Manager: "brew", Priority: []string{"brew"}}},
		FallbackBinDir:           "~/.local/share/omni/fallback/bin",
		DotsRepo:                 "~/dotfiles",
		DotsDisabled:             config.BoolPtr(true),
		AgentsDisabled:           config.BoolPtr(false),
		SkillsDisabled:           config.BoolPtr(true),
		McpDisabled:              config.BoolPtr(true),
		PluginsDisabled:          config.BoolPtr(true),
		AgentsUse:                []string{"claude-code"},
		DotsGit:                  config.DotsGitConfig{AutoCommit: true, AutoPush: true},
		DisabledProviders:        []string{"node"},
		ProviderPriority:         []string{"brew", "apt"},
		Providers: []config.ProviderEntry{{
			Name:        "brew",
			Provider:    "script",
			Package:     "brew",
			InstallWith: "brew",
			Options:     map[string]string{"install": "true"},
			Variants:    []config.ToolInstallSpec{installSpec},
			Hosts:       map[string]config.ToolInstallSpec{"linuxbox": installSpec},
		}},
	}
	return config.RootConfig{
		Schema:   config.SchemaURL,
		Include:  []string{"shared.json"},
		Version:  config.CurrentVersion,
		Settings: settings,
		Tools: map[string]config.ToolSpec{"ripgrep": {
			Providers:   []config.ToolInstallSpec{installSpec},
			Provider:    "brew",
			Package:     "ripgrep",
			InstallWith: "brew",
			Git:         "https://github.com/BurntSushi/ripgrep",
			Quarantine:  "2d",
			Options:     map[string]string{"head": "true"},
			Taps:        []string{"homebrew/core"},
			Ignore:      true,
			Variants:    []config.ToolInstallSpec{installSpec},
			Hosts:       map[string]config.ToolInstallSpec{"linuxbox": installSpec},
			Fallback: &config.FallbackSpec{
				Source:         config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep", URL: "https://github.com/BurntSushi/ripgrep"},
				Status:         config.FallbackStatusVerified,
				Binary:         "rg",
				BinDir:         "~/.local/bin",
				ReleaseChannel: "stable",
				Recipe:         *installSpec.Recipe,
				Platforms:      map[string]config.FallbackPlatform{"linux/amd64": {AssetPattern: "p", BinaryPath: "rg", Checksum: "sha256:abc"}},
				Commands:       config.FallbackCommands{Install: "i", Check: "c", Uninstall: "u", Upgrade: "up", Version: "v"},
			},
		}},
		Hosts:  map[string][]string{"linuxbox": {"work"}},
		Ignore: config.GlobalIgnore{Tools: []string{"slack"}, Dots: []string{"secrets"}},
		Groups: []*config.GroupConfig{{
			Name:         "work",
			Special:      "host",
			Description:  "Work tools",
			Taps:         []string{"homebrew/cask-fonts"},
			Tools:        []config.ToolEntry{{Name: "ripgrep"}},
			Dots:         []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Package: "nvim", Hosts: map[string]config.DotVariant{"linuxbox": {Package: "nvim-linux"}}, Ignored: true, Ignore: []string{"*.local"}, OnConflict: "use_repo"}},
			Skills:       []string{"review"},
			McpServers:   []string{"context7"},
			Plugins:      []string{"my-plugin"},
			Marketplaces: []string{"my-marketplace"},
		}},
		HostSettings: map[string]config.Settings{"linuxbox": settings},
		Agents: config.AgentsConfig{
			Packages:     []config.SkillPackage{{Source: "vercel-labs/agent-skills", Ref: "main", Skills: []string{"review"}, Agents: []string{"claude-code"}}},
			McpServers:   []config.McpServer{{Name: "context7", Transport: "stdio", Command: "npx -y ctx", URL: "https://mcp.example.com", Env: []string{"KEY"}, EnvLiteral: map[string]string{"A": "b"}, Headers: map[string]string{"Authorization": "Bearer x"}, Agents: []string{"claude-code"}}},
			Marketplaces: []config.Marketplace{{Name: "my-marketplace", Source: "owner/repo", Agents: []string{"claude-code"}}},
			Plugins:      []config.Plugin{{Name: "my-plugin", Marketplace: "my-marketplace", Agents: []string{"claude-code"}}},
			Skills:       []config.ManifestSkill{{Name: "review", Source: "owner/repo", Ref: "main", SkillPath: "skills/review", Agents: []string{"claude-code"}}},
			Ignore:       config.AgentsIgnore{Skills: []string{"s"}, McpServers: []string{"m"}, Plugins: []string{"p"}, Marketplaces: []string{"mk"}},
		},
	}
}
