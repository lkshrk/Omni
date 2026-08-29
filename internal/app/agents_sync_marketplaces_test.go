package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

var errFakeAPM = errors.New("apm marketplace add failed")

type marketplaceMatchMock struct{ *executor.MatchMockExecutor }

func (*marketplaceMatchMock) CommandAvailable(name string) bool { return name == "apm" }

func TestParseTemplateMarketplaces(t *testing.T) {
	tmpl := `name: host
dependencies:
  mcp:
    - name: shiplight
      # a comment that is not a marketplace command
      args:
        - '@shiplightai/mcp@latest'
targets:
  - claude
# apm marketplace add JuliusBrussee/caveman --name caveman
#apm marketplace add https://github.com/mksglu/context-mode.git --name context-mode
# apm install -g something
# apm marketplace remove stale --name stale
# apm marketplace add
`
	got := parseTemplateMarketplaces([]byte(tmpl))
	want := []marketplaceDecl{
		{name: "caveman", source: "JuliusBrussee/caveman", args: []string{"marketplace", "add", "JuliusBrussee/caveman", "--name", "caveman"}},
		{name: "context-mode", source: "https://github.com/mksglu/context-mode.git", args: []string{"marketplace", "add", "https://github.com/mksglu/context-mode.git", "--name", "context-mode"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTemplateMarketplaces = %#v, want %#v", got, want)
	}
}

func TestRegisteredMarketplaceNames(t *testing.T) {
	ws := t.TempDir()
	if got := registeredMarketplaceNames(ws); len(got) != 0 {
		t.Fatalf("missing file = %v, want none registered", got)
	}
	writeFile(t, filepath.Join(ws, "marketplaces.json"), `{
  "marketplaces": [
    {
      "name": "caveman",
      "url": "https://github.com/JuliusBrussee/caveman",
      "path": ".claude-plugin/marketplace.json",
      "owner": "JuliusBrussee",
      "repo": "caveman"
    },
    {
      "name": "litellm",
      "url": "https://example.invalid/marketplace.json",
      "path": ""
    }
  ]
}`)
	got := registeredMarketplaceNames(ws)
	if !got["caveman"] || !got["litellm"] || len(got) != 2 {
		t.Fatalf("registered = %v", got)
	}

	writeFile(t, filepath.Join(ws, "marketplaces.json"), "not json")
	if got := registeredMarketplaceNames(ws); len(got) != 0 {
		t.Fatalf("unreadable file = %v, want none registered", got)
	}
}

func setupMarketplaceSyncEnv(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	writeFile(t, filepath.Join(cfg, "omni", "apm.yml"), `name: host
targets:
  - claude
# apm marketplace add JuliusBrussee/caveman --name caveman
# apm marketplace add https://github.com/lkshrk/agent-marketplace.git --name lkshrk
`)
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: host\n")
	writeFile(t, filepath.Join(home, ".apm", "marketplaces.json"),
		`{"marketplaces":[{"name":"caveman","url":"https://github.com/JuliusBrussee/caveman"}]}`)
	return home
}

func TestAgentsSyncAllRegistersMissingMarketplaces(t *testing.T) {
	home := setupMarketplaceSyncEnv(t)
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "APM CLI version " + apmVersionPin + "\n"}},
	).WithFallback(executor.MockCall{Stdout: "ok\n"})
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.StateDir = t.TempDir()
	a.SetFallbackExecutor(&marketplaceMatchMock{mock})

	var progress []string
	if _, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{
		ForceTemplate: true,
		Progress:      func(msg string) { progress = append(progress, msg) },
	}); err != nil {
		t.Fatal(err)
	}

	adds := mock.CallsMatching("apm marketplace add")
	if len(adds) != 1 {
		t.Fatalf("marketplace adds = %#v, want only the unregistered one", adds)
	}
	wantArgs := []string{"marketplace", "add", "https://github.com/lkshrk/agent-marketplace.git", "--name", "lkshrk"}
	if !reflect.DeepEqual(adds[0].Args, wantArgs) || adds[0].Dir != filepath.Join(home, ".apm") {
		t.Fatalf("add call = dir %q args %v, want dir %q args %v", adds[0].Dir, adds[0].Args, filepath.Join(home, ".apm"), wantArgs)
	}
	mock.MustHaveCalledN(t, "apm install -g", 1)
	var announced bool
	for _, msg := range progress {
		if msg == "Registering marketplace lkshrk..." {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("progress = %q, want the added marketplace announced", progress)
	}
}

func TestAgentsSyncAllDryRunSkipsMarketplaceAdds(t *testing.T) {
	setupMarketplaceSyncEnv(t)
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "APM CLI version " + apmVersionPin + "\n"}},
	).WithFallback(executor.MockCall{Stdout: "ok\n"})
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.StateDir = t.TempDir()
	a.SetFallbackExecutor(&marketplaceMatchMock{mock})

	var progress []string
	if _, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{
		DryRun:   true,
		Progress: func(msg string) { progress = append(progress, msg) },
	}); err != nil {
		t.Fatal(err)
	}
	mock.MustHaveCalledN(t, "apm marketplace add", 0)
	var announced bool
	for _, msg := range progress {
		if msg == "Would register marketplace lkshrk..." {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("progress = %q, want the dry run to report the pending registration", progress)
	}
}

func TestAgentsSyncAllSkipsMarketplacesWithoutTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: host\n")
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "APM CLI version " + apmVersionPin + "\n"}},
	).WithFallback(executor.MockCall{Stdout: "ok\n"})
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(&marketplaceMatchMock{mock})

	if _, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	mock.MustHaveCalledN(t, "apm marketplace add", 0)
}

func TestAgentsSyncAllReportsFailedMarketplaceAdds(t *testing.T) {
	home := setupMarketplaceSyncEnv(t)
	if err := os.Remove(filepath.Join(home, ".apm", "marketplaces.json")); err != nil {
		t.Fatal(err)
	}
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "APM CLI version " + apmVersionPin + "\n"}},
		executor.MatchRule{Pattern: "apm marketplace add JuliusBrussee/caveman", Response: executor.MockCall{Err: errFakeAPM}},
	).WithFallback(executor.MockCall{Stdout: "ok\n"})
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.StateDir = t.TempDir()
	a.SetFallbackExecutor(&marketplaceMatchMock{mock})

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{ForceTemplate: true})
	if err == nil {
		t.Fatal("expected the failed registration to surface")
	}
	mock.MustHaveCalledN(t, "apm marketplace add", 2)
	mock.MustHaveCalledN(t, "apm install -g", 0)
}

func TestMarketplaceDeclRoundTrips(t *testing.T) {
	for _, want := range []marketplaceDecl{
		{name: "caveman", source: "JuliusBrussee/caveman"},
		{name: "context-mode", source: "https://github.com/mksglu/context-mode.git"},
	} {
		got, ok := parseMarketplaceDecl("# " + want.Render())
		if !ok || got.name != want.name || got.source != want.source {
			t.Fatalf("round trip of %+v = %+v (ok %v)", want, got, ok)
		}
	}
}
