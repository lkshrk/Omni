package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

var errBoom = errors.New("boom")

func TestClaudeCodePluginAdapter_ID(t *testing.T) {
	t.Parallel()
	a := NewClaudeCodePluginAdapter(nil, nil)
	if a.ID() != "claude-code" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestClaudeCodePluginAdapter_InstallPlugin(t *testing.T) {
	t.Parallel()
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	p := config.Plugin{Name: "useful-skills", Marketplace: "lkshrk"}
	if err := a.InstallPlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "claude" {
		t.Fatalf("expected claude binary, got %q", gotCmd)
	}
	want := []string{"plugins", "install", "useful-skills@lkshrk"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodePluginAdapter_RemovePlugin(t *testing.T) {
	t.Parallel()
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	p := config.Plugin{Name: "useful-skills", Marketplace: "lkshrk"}
	if err := a.RemovePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugins", "uninstall", "useful-skills@lkshrk", "--yes"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodePluginAdapter_ExitZeroFailureMarkers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		stdout string
		op     func(a PluginAdapter) error
	}{
		{"install", `Installing plugin "x@m"...✘ Failed to install plugin "x@m": Plugin "x" not found in marketplace "m"`, func(a PluginAdapter) error {
			return a.InstallPlugin(context.Background(), config.Plugin{Name: "x", Marketplace: "m"})
		}},
		{"uninstall", `✘ Failed to uninstall plugin "x@m": Plugin "x@m" not found in installed plugins`, func(a PluginAdapter) error {
			return a.RemovePlugin(context.Background(), config.Plugin{Name: "x", Marketplace: "m"})
		}},
		{"marketplace add", `Adding marketplace…✘ Failed to add marketplace: Failed to clone marketplace repository`, func(a PluginAdapter) error {
			return a.AddMarketplace(context.Background(), config.Marketplace{Name: "m", Source: "o/r"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
				return tc.stdout, "", nil
			}
			a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
			err := tc.op(a)
			if err == nil {
				t.Fatal("expected error for exit-0 failure output, got nil")
			}
			if !strings.Contains(err.Error(), "Failed to") {
				t.Fatalf("expected failure output in error, got %v", err)
			}
		})
	}
}

func TestClaudeCodePluginAdapter_UpdatePlugin_Success(t *testing.T) {
	t.Parallel()
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return `✔ useful-skills is already at the latest version (0.2.0)`, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.UpdatePlugin(context.Background(), "useful-skills", "lkshrk"); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "claude" {
		t.Fatalf("expected claude binary, got %q", gotCmd)
	}
	want := []string{"plugin", "update", "useful-skills@lkshrk"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

// `claude plugin update <bare-name>` returns the not-found failure marker, so the bare-name arg shape must not come back.
func TestClaudeCodePluginAdapter_UpdatePlugin_BareNameArgsWouldFail(t *testing.T) {
	t.Parallel()
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return `✔ useful-skills is already at the latest version (0.2.0)`, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.UpdatePlugin(context.Background(), "useful-skills", "lkshrk"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, arg := range gotArgs {
		if strings.Contains(arg, "@") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an arg containing name@marketplace, got %v", gotArgs)
	}
}

// `claude plugin update` prints a failure line while still exiting 0, so the exit code alone cannot detect it.
func TestClaudeCodePluginAdapter_UpdatePlugin_ExitZeroFailureMarkerIsError(t *testing.T) {
	t.Parallel()
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return `✘ Failed to update plugin "useful-skills": Plugin "useful-skills" not found`, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.UpdatePlugin(context.Background(), "useful-skills", "lkshrk"); err == nil {
		t.Fatal("expected an error from the exit-0 failure marker")
	}
}

// The marker-parsing path must not swallow genuine exec failures.
func TestClaudeCodePluginAdapter_UpdatePlugin_RealExecErrorStillPropagates(t *testing.T) {
	t.Parallel()
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return "", "connection refused", errBoom
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.UpdatePlugin(context.Background(), "useful-skills", "lkshrk"); err == nil {
		t.Fatal("expected the real exec error to propagate")
	}
}

func TestClaudeCodePluginAdapter_AddMarketplace(t *testing.T) {
	t.Parallel()
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	m := config.Marketplace{Name: "lkshrk", Source: "lkshrk/agent-marketplace"}
	if err := a.AddMarketplace(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugins", "marketplace", "add", "lkshrk/agent-marketplace"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

// Exact stdout of `claude plugins list --json` with no plugins installed.
const claudePluginListEmptyFixture = `[]`

func TestClaudeCodePluginAdapter_ListPlugins_Empty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if mcpSliceEq(args, []string{"plugins", "list", "--json", "--available"}) {
			return "", "unknown flag --available", errBoom
		}
		return claudePluginListEmptyFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected no plugins, got %v", plugins)
	}
}

// Exact stdout of `claude plugins list --json` with one installed plugin.
const claudePluginListInstalledFixture = `[
  {
    "id": "useful-skills@lkshrk",
    "version": "0.2.0",
    "scope": "user",
    "enabled": true,
    "installPath": "/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/claude-config/plugins/cache/lkshrk/useful-skills/0.2.0",
    "installedAt": "2026-07-02T09:11:33.969Z",
    "lastUpdated": "2026-07-02T09:11:33.969Z"
  }
]`

func TestClaudeCodePluginAdapter_ListPlugins_ParsesRealFixture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if mcpSliceEq(args, []string{"plugins", "list", "--json", "--available"}) {
			return "", "unknown flag --available", errBoom
		}
		return claudePluginListInstalledFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InstalledPlugin{
		{Name: "useful-skills", Marketplace: "lkshrk", Version: "0.2.0"},
	}
	if len(plugins) != len(want) {
		t.Fatalf("length mismatch\ngot:  %v\nwant: %v", plugins, want)
	}
	for i := range want {
		if plugins[i] != want[i] {
			t.Fatalf("entry %d mismatch\ngot:  %+v\nwant: %+v", i, plugins[i], want[i])
		}
	}
}

// Exact stdout of `claude plugins marketplace list --json` with one marketplace configured.
const claudeMarketplaceListConfiguredFixture = `[
  {
    "name": "lkshrk",
    "source": "github",
    "repo": "lkshrk/agent-marketplace",
    "installLocation": "/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/claude-config/plugins/marketplaces/lkshrk"
  }
]`

func TestClaudeCodePluginAdapter_ListMarketplaces_ParsesRealFixture(t *testing.T) {
	t.Parallel()
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return claudeMarketplaceListConfiguredFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InstalledMarketplace{{Name: "lkshrk", Source: "lkshrk/agent-marketplace"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
	wantArgs := []string{"plugins", "marketplace", "list", "--json"}
	if !mcpSliceEq(gotArgs, wantArgs) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, wantArgs)
	}
}

// Reconstructed rather than captured: the empty case only ever printed plain text, and this is the sole shape matching the entry schema.
const claudeMarketplaceListEmptyFixture = `[]`

func TestClaudeCodePluginAdapter_ListMarketplaces_Empty(t *testing.T) {
	t.Parallel()
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return claudeMarketplaceListEmptyFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no marketplaces, got %v", got)
	}
}

// The marketplace list JSON carries no date field, so UpdatedAt must come from the installLocation mtime.
func TestClaudeCodePluginAdapter_ListMarketplaces_UpdatedAtFromInstallLocationMtime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixture := `[{"name":"lkshrk","source":"github","repo":"lkshrk/agent-marketplace","installLocation":"` + dir + `"}]`
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return fixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 marketplace, got %v", got)
	}
	wantModTime, statErr := os.Stat(dir)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if !got[0].UpdatedAt.Equal(wantModTime.ModTime()) {
		t.Fatalf("UpdatedAt = %v, want %v (installLocation mtime)", got[0].UpdatedAt, wantModTime.ModTime())
	}
}

// Enrichment is best-effort: an unreadable installLocation yields a zero UpdatedAt, never an error.
func TestClaudeCodePluginAdapter_ListMarketplaces_UpdatedAtZeroWhenLocationMissing(t *testing.T) {
	t.Parallel()
	fixture := `[{"name":"lkshrk","source":"github","repo":"lkshrk/agent-marketplace","installLocation":"/does/not/exist"}]`
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return fixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt for a missing installLocation, got %+v", got)
	}
}

func TestClaudeCodePluginAdapter_UpdateMarketplaces(t *testing.T) {
	t.Parallel()
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	if err := a.UpdateMarketplaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "claude" {
		t.Fatalf("expected claude binary, got %q", gotCmd)
	}
	want := []string{"plugin", "marketplace", "update"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodePluginAdapter_UpdateMarketplaces_PropagatesError(t *testing.T) {
	t.Parallel()
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return "", "boom stderr", errBoom
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	if err := a.UpdateMarketplaces(context.Background()); err == nil {
		t.Fatal("expected the real exec error to propagate")
	}
}

// Available entries carry no version field in practice; this fixture adds one to prove the join logic works when it exists.
const claudePluginListAvailableFixture = `{
  "installed": [
    {
      "id": "foo@mkt",
      "version": "1.0.0",
      "scope": "user",
      "enabled": true
    }
  ],
  "available": [
    {
      "pluginId": "foo@mkt",
      "name": "foo",
      "marketplaceName": "mkt",
      "version": "2.0.0"
    }
  ]
}`

func TestClaudeCodePluginAdapter_ListPlugins_JoinsLatestVersionFromAvailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return claudePluginListAvailableFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"plugins", "list", "--json", "--available"}
	if !mcpSliceEq(gotArgs, wantArgs) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, wantArgs)
	}
	want := []InstalledPlugin{
		{Name: "foo", Marketplace: "mkt", Version: "1.0.0", LatestVersion: "2.0.0"},
	}
	if len(plugins) != len(want) || plugins[0] != want[0] {
		t.Fatalf("mismatch\ngot:  %+v\nwant: %+v", plugins, want)
	}
}

// The real payload has no version field at all, so LatestVersion must stay empty rather than be fabricated from source.ref.
const claudePluginListAvailableNoVersionFixture = `{
  "installed": [
    {
      "id": "foo@mkt",
      "version": "1.0.0",
      "scope": "user",
      "enabled": true
    }
  ],
  "available": [
    {
      "pluginId": "foo@mkt",
      "name": "foo",
      "marketplaceName": "mkt",
      "description": "...",
      "source": {"source": "git-subdir", "url": "...", "path": "...", "ref": "main", "sha": "17ef6fb"},
      "installCount": 2269
    }
  ]
}`

func TestClaudeCodePluginAdapter_ListPlugins_NoVersionFieldLeavesLatestEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return claudePluginListAvailableNoVersionFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InstalledPlugin{
		{Name: "foo", Marketplace: "mkt", Version: "1.0.0", LatestVersion: "", LatestSha: "17ef6fb"},
	}
	if len(plugins) != len(want) || plugins[0] != want[0] {
		t.Fatalf("mismatch\ngot:  %+v\nwant: %+v", plugins, want)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_JoinsShaFromInstalledPluginsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedPluginsJSON := `{
	  "version": 2,
	  "plugins": {
	    "foo@mkt": [
	      {"scope": "user", "installPath": "/x", "version": "unknown", "gitCommitSha": "abc123"}
	    ]
	  }
	}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte(installedPluginsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if mcpSliceEq(args, []string{"plugins", "list", "--json", "--available"}) {
			return "", "unknown flag --available", errBoom
		}
		return `[{"id": "foo@mkt", "version": "unknown", "scope": "user", "enabled": true}]`, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].Sha != "abc123" {
		t.Fatalf("expected Sha=abc123, got %+v", plugins)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_MissingInstalledPluginsFileIsNonFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if mcpSliceEq(args, []string{"plugins", "list", "--json", "--available"}) {
			return "", "unknown flag --available", errBoom
		}
		return `[{"id": "foo@mkt", "version": "1.0.0", "scope": "user", "enabled": true}]`, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].Sha != "" {
		t.Fatalf("expected empty Sha when file missing, got %+v", plugins)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_JoinsLatestShaFromAvailableSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	availableWithObjectSource := `{
	  "installed": [{"id": "foo@mkt", "version": "unknown", "scope": "user", "enabled": true}],
	  "available": [{
	    "pluginId": "foo@mkt", "name": "foo", "marketplaceName": "mkt",
	    "source": {"source": "git-subdir", "url": "...", "path": "...", "ref": "v1", "sha": "deadbeef"}
	  }]
	}`
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return availableWithObjectSource, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].LatestSha != "deadbeef" {
		t.Fatalf("expected LatestSha=deadbeef, got %+v", plugins)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_BareStringSourceLeavesLatestShaEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	availableWithStringSource := `{
	  "installed": [{"id": "foo@mkt", "version": "unknown", "scope": "user", "enabled": true}],
	  "available": [{
	    "pluginId": "foo@mkt", "name": "foo", "marketplaceName": "mkt",
	    "source": "./plugins/agent-sdk-dev"
	  }]
	}`
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return availableWithStringSource, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].LatestSha != "" {
		t.Fatalf("expected empty LatestSha for bare-string source, got %+v", plugins)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_FallsBackWhenAvailableFlagFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	calls := 0
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		calls++
		if mcpSliceEq(args, []string{"plugins", "list", "--json", "--available"}) {
			return "", "unknown flag --available", errBoom
		}
		return claudePluginListInstalledFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected fallback to make a second exec call, got %d calls", calls)
	}
	want := []InstalledPlugin{
		{Name: "useful-skills", Marketplace: "lkshrk", Version: "0.2.0"},
	}
	if len(plugins) != len(want) || plugins[0] != want[0] {
		t.Fatalf("mismatch\ngot:  %+v\nwant: %+v", plugins, want)
	}
}

// The common real-world case: a source path in object form with no manifest version.
const claudeAvailableFixtureWithPath = `{
  "installed": [{"id": "superpowers@caveman", "version": "unknown", "scope": "user", "enabled": true}],
  "available": [{
    "pluginId": "superpowers@caveman", "name": "superpowers", "marketplaceName": "caveman",
    "source": {"source": "git-subdir", "url": "...", "path": "plugins/superpowers", "ref": "main"}
  }]
}`

func writeClaudeInstalledPluginsFixture(t *testing.T, home, identity, sha string) {
	t.Helper()
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"version": 2, "plugins": {"` + identity + `": [{"scope": "user", "installPath": "/x", "version": "unknown", "gitCommitSha": "` + sha + `"}]}}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_PathOutdated_NilWhenGitFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeInstalledPluginsFixture(t, home, "superpowers@caveman", "installedsha1")

	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		if cmd == "git" {
			return "", "not a git repository", errBoom
		}
		return claudeAvailableFixtureWithPath, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].PathOutdated != nil {
		t.Fatalf("expected PathOutdated=nil when git fails (never guess), got %+v", plugins)
	}
}

func TestClaudeCodePluginAdapter_ListPlugins_PathOutdated_NilWhenNoInstalledSha(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No installed_plugins.json is written, so no installed sha is available.

	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		if cmd == "git" {
			t.Fatal("git should not be invoked without an installed sha to compare against")
		}
		return claudeAvailableFixtureWithPath, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].PathOutdated != nil {
		t.Fatalf("expected PathOutdated=nil without an installed sha, got %+v", plugins)
	}
}
