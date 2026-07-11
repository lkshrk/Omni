package app

import (
	"context"
	"os"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCodexPluginAdapter_ID(t *testing.T) {
	a := NewCodexPluginAdapter(nil, nil)
	if a.ID() != "codex" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestCodexPluginAdapter_InstallPlugin(t *testing.T) {
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	p := config.Plugin{Name: "useful-skills", Marketplace: "lkshrk"}
	if err := a.InstallPlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "codex" {
		t.Fatalf("expected codex binary, got %q", gotCmd)
	}
	want := []string{"plugin", "add", "useful-skills@lkshrk"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestCodexPluginAdapter_RemovePlugin(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	p := config.Plugin{Name: "useful-skills", Marketplace: "lkshrk"}
	if err := a.RemovePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "remove", "useful-skills@lkshrk"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestCodexPluginAdapter_UpdatePlugin_NotSupported(t *testing.T) {
	called := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		called = true
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.UpdatePlugin(context.Background(), "useful-skills", "lkshrk"); err == nil {
		t.Fatal("expected an error since codex has no plugin update command")
	}
	if called {
		t.Fatal("UpdatePlugin must never shell out to codex")
	}
}

func TestCodexPluginAdapter_AddMarketplace(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	m := config.Marketplace{Name: "lkshrk", Source: "lkshrk/agent-marketplace"}
	if err := a.AddMarketplace(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "marketplace", "add", "lkshrk/agent-marketplace"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

// codexPluginListEmptyFixture is the exact stdout of `codex plugin list --json`
// with no plugins installed and no marketplace configured, captured in
// docs/superpowers/research/2026-07-02-plugin-cli-probe.md.
const codexPluginListEmptyFixture = `{
  "installed": [],
  "available": []
}`

func TestCodexPluginAdapter_ListPlugins_Empty(t *testing.T) {
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		want := []string{"plugin", "list", "--json"}
		if !mcpSliceEq(args, want) {
			t.Fatalf("args mismatch\ngot:  %v\nwant: %v", args, want)
		}
		return codexPluginListEmptyFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected empty fixture to parse to zero plugins, got %v", plugins)
	}
}

// codexPluginListInstalledFixture is the exact stdout of
// `codex plugin list --json` with one installed plugin, captured in
// docs/superpowers/research/2026-07-02-plugin-cli-probe.md. Note the
// {installed, available} wrapper is present even though --available was not
// passed (probe Deviation 3) — a different shape convention than claude's
// bare-array default.
const codexPluginListInstalledFixture = `{
  "installed": [
    {
      "pluginId": "useful-skills@lkshrk",
      "name": "useful-skills",
      "marketplaceName": "lkshrk",
      "version": "0.2.0",
      "installed": true,
      "enabled": true,
      "source": {
        "source": "git",
        "url": "https://github.com/lkshrk/useful-skills.git",
        "ref": "v0.2.0"
      },
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      },
      "installPolicy": "AVAILABLE",
      "authPolicy": "ON_INSTALL"
    }
  ],
  "available": []
}`

func TestCodexPluginAdapter_ListPlugins_ParsesRealFixture(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return codexPluginListInstalledFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
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

// codexPluginListAvailableFixture is the exact stdout of
// `codex plugin list --json --available`, captured in the probe transcript.
// version is JSON null for an available-but-uninstalled plugin, which must
// not be mistaken for the installed entry (ListPlugins only surfaces
// resp.Installed).
const codexPluginListAvailableFixture = `{
  "installed": [],
  "available": [
    {
      "pluginId": "linear-ai@lkshrk",
      "name": "linear-ai",
      "marketplaceName": "lkshrk",
      "version": null,
      "installed": false,
      "enabled": false,
      "source": {
        "source": "git",
        "url": "https://github.com/lkshrk/linear-ai.git",
        "ref": "v1.5.0"
      },
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      },
      "installPolicy": "AVAILABLE",
      "authPolicy": "ON_INSTALL"
    }
  ]
}`

func TestCodexPluginAdapter_ListPlugins_IgnoresAvailableOnlyEntries(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return codexPluginListAvailableFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected available-only entries to be excluded, got %v", plugins)
	}
}

// codexPluginListInstalledWithAvailableUpdateFixture matches an installed
// plugin's name+marketplaceName against an available entry carrying a newer
// version, mirroring codexPluginListResponse.Available which is parsed but
// currently discarded.
const codexPluginListInstalledWithAvailableUpdateFixture = `{
  "installed": [
    {
      "name": "useful-skills",
      "marketplaceName": "lkshrk",
      "version": "0.2.0"
    }
  ],
  "available": [
    {
      "name": "useful-skills",
      "marketplaceName": "lkshrk",
      "version": "0.3.0"
    },
    {
      "name": "unrelated-plugin",
      "marketplaceName": "lkshrk",
      "version": "9.9.9"
    }
  ]
}`

func TestCodexPluginAdapter_ListPlugins_JoinsLatestVersionFromAvailable(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return codexPluginListInstalledWithAvailableUpdateFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InstalledPlugin{
		{Name: "useful-skills", Marketplace: "lkshrk", Version: "0.2.0", LatestVersion: "0.3.0"},
	}
	if len(plugins) != len(want) || plugins[0] != want[0] {
		t.Fatalf("mismatch\ngot:  %+v\nwant: %+v", plugins, want)
	}
}

// codexMarketplaceListFixture is the exact stdout of
// `codex plugin marketplace list --json` with one marketplace configured,
// captured in docs/superpowers/research/2026-07-02-plugin-cli-probe.md.
const codexMarketplaceListFixture = `{
  "marketplaces": [
    {
      "name": "lkshrk",
      "root": "/private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/.tmp/marketplaces/lkshrk",
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      }
    }
  ]
}`

func TestCodexPluginAdapter_ListMarketplaces_ParsesRealFixture(t *testing.T) {
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		want := []string{"plugin", "marketplace", "list", "--json"}
		if !mcpSliceEq(args, want) {
			t.Fatalf("args mismatch\ngot:  %v\nwant: %v", args, want)
		}
		return codexMarketplaceListFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InstalledMarketplace{{Name: "lkshrk", Source: "https://github.com/lkshrk/agent-marketplace.git"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

// codexMarketplaceListEmptyFixture is not captured verbatim in the probe
// transcript (only the plain-text "No plugin marketplaces in scope." form
// was captured for the empty case) but is the only shape consistent with
// codexMarketplaceListResponse's schema for an empty marketplaces array.
const codexMarketplaceListEmptyFixture = `{
  "marketplaces": []
}`

func TestCodexPluginAdapter_ListMarketplaces_Empty(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return codexMarketplaceListEmptyFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no marketplaces, got %v", got)
	}
}

// TestCodexPluginAdapter_ListMarketplaces_UpdatedAtFromRootMtime verifies
// UpdatedAt is derived from the root clone directory's mtime, since codex's
// marketplace list JSON carries no date field of its own.
func TestCodexPluginAdapter_ListMarketplaces_UpdatedAtFromRootMtime(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"marketplaces":[{"name":"lkshrk","root":"` + dir + `","marketplaceSource":{"sourceType":"git","source":"https://github.com/lkshrk/agent-marketplace.git"}}]}`
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return fixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
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
		t.Fatalf("UpdatedAt = %v, want %v (root mtime)", got[0].UpdatedAt, wantModTime.ModTime())
	}
}

// TestCodexPluginAdapter_ListMarketplaces_UpdatedAtZeroWhenRootMissing
// verifies a missing/unreadable root yields a zero UpdatedAt rather than an
// error — best-effort enrichment, never fatal.
func TestCodexPluginAdapter_ListMarketplaces_UpdatedAtZeroWhenRootMissing(t *testing.T) {
	fixture := `{"marketplaces":[{"name":"lkshrk","root":"/does/not/exist","marketplaceSource":{"sourceType":"git","source":"https://github.com/lkshrk/agent-marketplace.git"}}]}`
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return fixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt for a missing root, got %+v", got)
	}
}

func TestCodexPluginAdapter_UpdateMarketplaces(t *testing.T) {
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	if err := a.UpdateMarketplaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "codex" {
		t.Fatalf("expected codex binary, got %q", gotCmd)
	}
	want := []string{"plugin", "marketplace", "upgrade"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestCodexPluginAdapter_UpdateMarketplaces_PropagatesError(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return "", "boom stderr", errBoom
	}
	a := NewCodexPluginAdapter(exec, nil)
	if err := a.UpdateMarketplaces(context.Background()); err == nil {
		t.Fatal("expected the real exec error to propagate")
	}
}
