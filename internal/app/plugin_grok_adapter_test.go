package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestGrokPluginAdapter_ID(t *testing.T) {
	a := NewGrokPluginAdapter(nil, nil)
	if a.ID() != "grok" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestGrokPluginAdapter_InstallPlugin(t *testing.T) {
	var gotArgs []string
	marketJSON := `[{"name":"ecc","kind":"git","source":{"url":"https://github.com/affaan-m/everything-claude-code.git"}}]`
	call := 0
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		if cmd != "grok" {
			t.Fatalf("expected grok binary, got %q", cmd)
		}
		if len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "list" {
			call++
			return marketJSON, "", nil
		}
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokPluginAdapter(exec, nil)
	p := config.Plugin{Name: "superpowers", Marketplace: "ecc"}
	if err := a.InstallPlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if call != 1 {
		t.Fatalf("expected marketplace list before install, calls=%d", call)
	}
	want := []string{"plugin", "install", "superpowers@affaan-m/everything-claude-code", "--trust"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestGrokPluginAdapter_RemovePlugin(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokPluginAdapter(exec, nil)
	p := config.Plugin{Name: "superpowers", Marketplace: "ecc"}
	if err := a.RemovePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "uninstall", "superpowers", "--confirm"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestGrokPluginAdapter_UpdatePlugin(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokPluginAdapter(exec, nil)
	if err := a.UpdatePlugin(context.Background(), "superpowers", "ecc"); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "update", "superpowers"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestGrokPluginAdapter_AddMarketplace(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokPluginAdapter(exec, nil)
	m := config.Marketplace{Name: "ecc", Source: "affaan-m/everything-claude-code"}
	if err := a.AddMarketplace(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "marketplace", "add", "affaan-m/everything-claude-code"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestParseGrokPluginList_Available(t *testing.T) {
	out := `[
	  {"status":"installed","name":"caveman","version":"1.2.3","marketplace":"caveman"},
	  {"status":"available","name":"caveman","version":"9.9.9","marketplace":"caveman"}
	]`
	got, err := parseGrokPluginList(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want one installed plugin", got)
	}
	if got[0].Version != "1.2.3" || got[0].LatestVersion != "9.9.9" {
		t.Fatalf("versions = %+v, want 1.2.3 / latest 9.9.9", got[0])
	}
}

func TestGrokMarketplaceQualifier(t *testing.T) {
	entry := grokMarketplaceListEntry{Name: "ecc"}
	entry.Source.URL = "https://github.com/affaan-m/everything-claude-code.git"
	if got := grokMarketplaceQualifier(entry); got != "affaan-m/everything-claude-code" {
		t.Fatalf("qualifier = %q", got)
	}
}

func TestGrokPluginAdapter_ListMarketplaces(t *testing.T) {
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if !mcpSliceEq(args, []string{"plugin", "marketplace", "list", "--json"}) {
			t.Fatalf("unexpected args: %v", args)
		}
		return `[{"name":"ecc","kind":"git","source":{"url":"https://github.com/affaan-m/everything-claude-code.git"}}]`, "", nil
	}
	a := NewGrokPluginAdapter(exec, nil)
	got, err := a.ListMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "ecc" || got[0].Source != "affaan-m/everything-claude-code" {
		t.Fatalf("got %+v", got)
	}
}
