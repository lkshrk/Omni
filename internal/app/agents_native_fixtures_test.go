package app

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const nativeFixtureHomeToken = "@HOME@"

var nativeFixtureCLICommands = map[string]string{
	"claude-plugins-list.json":             "claude plugins list --json",
	"claude-plugins-marketplace-list.json": "claude plugins marketplace list --json",
	"codex-plugin-list.json":               "codex plugin list --json",
	"codex-plugin-marketplace-list.json":   "codex plugin marketplace list --json",
	"codex-mcp-list.json":                  "codex mcp list --json",
}

type nativeFixturePlugin struct {
	Name        string `json:"name"`
	Marketplace string `json:"marketplace"`
	Target      string `json:"target"`
	Version     string `json:"version,omitempty"`
	InstallRoot string `json:"installRoot,omitempty"`
}

type nativeFixtureMarketplace struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type nativeFixtureMCP struct {
	Name       string      `json:"name"`
	Target     string      `json:"target"`
	Definition legacyEntry `json:"definition"`
}

type nativeFixtureWant struct {
	Plugins             []nativeFixturePlugin      `json:"plugins"`
	Marketplaces        []nativeFixtureMarketplace `json:"marketplaces"`
	MCP                 []nativeFixtureMCP         `json:"mcp"`
	Error               string                     `json:"error"`
	ErrorMustNotContain []string                   `json:"errorMustNotContain"`
}

func TestNativeInventoryFixtures(t *testing.T) {
	root := filepath.Join("testdata", "agents_native")
	names, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range names {
		if !dir.IsDir() {
			continue
		}
		t.Run(dir.Name(), func(t *testing.T) {
			runNativeInventoryFixture(t, filepath.Join(root, dir.Name()))
		})
	}
}

func runNativeInventoryFixture(t *testing.T, dir string) {
	t.Helper()
	a, exec := newNativeInventoryApp(t, map[string]bool{})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	seedNativeFixtureHome(t, filepath.Join(dir, "home"), home)
	for _, file := range slices.Sorted(maps.Keys(nativeFixtureCLICommands)) {
		raw, err := os.ReadFile(filepath.Join(dir, "cli", file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		command := nativeFixtureCLICommands[file]
		exec.available[strings.SplitN(command, " ", 2)[0]] = true
		exec.AddRule(nativeRule(command, strings.ReplaceAll(string(raw), nativeFixtureHomeToken, home)))
	}

	want := readNativeFixtureWant(t, dir, home)
	gotPlugins, gotMarketplaces, gotMCP, invErr := collectNativeInventory(t, a)
	if want.Error != "" {
		if invErr == nil || !strings.Contains(invErr.Error(), want.Error) {
			t.Fatalf("error = %v, want it to contain %q", invErr, want.Error)
		}
		for _, forbidden := range want.ErrorMustNotContain {
			if strings.Contains(invErr.Error(), forbidden) {
				t.Fatalf("error leaked %q: %v", forbidden, invErr)
			}
		}
		return
	}
	if invErr != nil {
		t.Fatal(invErr)
	}
	assertNativeFixtureEqual(t, "plugins", gotPlugins, want.Plugins)
	assertNativeFixtureEqual(t, "marketplaces", gotMarketplaces, want.Marketplaces)
	assertNativeFixtureEqual(t, "mcp", gotMCP, want.MCP)
}

func collectNativeInventory(t *testing.T, a *App) ([]nativeFixturePlugin, []nativeFixtureMarketplace, []nativeFixtureMCP, error) {
	t.Helper()
	plugins := []nativeFixturePlugin{}
	marketplaces := []nativeFixtureMarketplace{}
	servers := []nativeFixtureMCP{}
	for _, cli := range []string{"claude", "codex"} {
		if !a.commandAvailable(cli) {
			continue
		}
		listedPlugins, err := a.listNativePlugins(t.Context(), cli)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, plugin := range listedPlugins {
			plugins = append(plugins, nativeFixturePlugin(plugin))
		}
		listedMarketplaces, err := a.listNativeMarketplaces(t.Context(), cli)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, marketplace := range listedMarketplaces {
			marketplaces = append(marketplaces, nativeFixtureMarketplace(marketplace))
		}
		listedServers, err := a.listNativeMCP(t.Context(), cli)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, server := range listedServers {
			definition := server.Definition
			normalizeNativeMCP(&definition)
			servers = append(servers, nativeFixtureMCP{Name: server.Name, Target: server.Target, Definition: definition})
		}
	}
	sort.SliceStable(plugins, func(i, j int) bool { return nativeFixtureLess(plugins[i], plugins[j]) })
	sort.SliceStable(marketplaces, func(i, j int) bool { return nativeFixtureLess(marketplaces[i], marketplaces[j]) })
	sort.SliceStable(servers, func(i, j int) bool { return nativeFixtureLess(servers[i], servers[j]) })
	return plugins, marketplaces, servers, nil
}

func nativeFixtureLess[T any](a, b T) bool {
	return string(mustNativeFixtureJSON(a)) < string(mustNativeFixtureJSON(b))
}

func mustNativeFixtureJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func assertNativeFixtureEqual[T any](t *testing.T, kind string, got, want []T) {
	t.Helper()
	if want == nil {
		want = []T{}
	}
	gotJSON, wantJSON := mustNativeFixtureJSON(got), mustNativeFixtureJSON(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s inventory\n got: %s\nwant: %s", kind, gotJSON, wantJSON)
	}
}

func readNativeFixtureWant(t *testing.T, dir, home string) nativeFixtureWant {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "want.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want nativeFixtureWant
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), nativeFixtureHomeToken, home)), &want); err != nil {
		t.Fatal(err)
	}
	for i := range want.MCP {
		normalizeNativeMCP(&want.MCP[i].Definition)
	}
	sort.SliceStable(want.Plugins, func(i, j int) bool { return nativeFixtureLess(want.Plugins[i], want.Plugins[j]) })
	sort.SliceStable(want.Marketplaces, func(i, j int) bool { return nativeFixtureLess(want.Marketplaces[i], want.Marketplaces[j]) })
	sort.SliceStable(want.MCP, func(i, j int) bool { return nativeFixtureLess(want.MCP[i], want.MCP[j]) })
	return want
}

// Fixture trees spell dotfiles "dot-x" so .gitignore entries like .claude/ cannot swallow them.
func nativeFixtureDotPath(rel string) string {
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if after, ok := strings.CutPrefix(part, "dot-"); ok {
			parts[i] = "." + after
		}
	}
	return filepath.Join(parts...)
}

func seedNativeFixtureHome(t *testing.T, source, home string) {
	t.Helper()
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return
	}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(home, nativeFixtureDotPath(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, []byte(strings.ReplaceAll(string(raw), nativeFixtureHomeToken, home)), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
