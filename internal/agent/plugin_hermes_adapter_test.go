package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestHermesPluginAdapterAvailable(t *testing.T) {
	originalLookPath := lookPath
	t.Cleanup(func() { lookPath = originalLookPath })
	lookPath = func(name string) (string, error) {
		if name != "hermes" {
			t.Fatalf("lookPath(%q), want hermes", name)
		}
		return "", errors.New("missing")
	}
	if NewHermesPluginAdapter(nil, nil).Available() {
		t.Fatal("Available() = true without hermes")
	}
	lookPath = func(string) (string, error) { return "/bin/hermes", nil }
	if !NewHermesPluginAdapter(nil, nil).Available() {
		t.Fatal("Available() = false with hermes")
	}
}

func TestHermesPluginAdapterCapabilities(t *testing.T) {
	a := NewHermesPluginAdapter(nil, nil).(PluginAdapterCapabilities)
	if a.SupportsMarketplaces() || !a.SupportsDirectSources() {
		t.Fatalf("capabilities = marketplaces:%v direct:%v", a.SupportsMarketplaces(), a.SupportsDirectSources())
	}
}

func TestHermesPluginAdapterCommands(t *testing.T) {
	var calls [][]string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		calls = append(calls, append([]string{cmd}, args...))
		return "", "", nil
	}
	a := NewHermesPluginAdapter(exec, nil)
	p := config.Plugin{Name: "weather", Source: "owner/weather"}
	if err := a.InstallPlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.RemovePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdatePlugin(context.Background(), p.Name, ""); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"hermes", "plugins", "install", "owner/weather", "--enable"},
		{"hermes", "plugins", "remove", "weather"},
		{"hermes", "plugins", "update", "weather"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestHermesPluginAdapterListPlugins(t *testing.T) {
	var got []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		got = append([]string{cmd}, args...)
		return `[
			{"name":"weather","status":"enabled","version":"1.2.3","description":"Weather","source":"user"},
			{"name":"disabled","status":"disabled","version":"2.0.0","description":"Disabled","source":"git"},
			{"name":"entrypoint","status":"enabled","version":"","description":"Internal","source":"entrypoint"},
			{"name":"bundled","status":"enabled","version":"9.9.9","description":"Bundled","source":"bundled"}
		]`, "", nil
	}
	a := NewHermesPluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"hermes", "plugins", "list", "--user", "--no-bundled", "--json"}
	if !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("command = %v, want %v", got, wantArgs)
	}
	want := []InstalledPlugin{
		{Name: "weather", Version: "1.2.3"},
		{Name: "disabled", Version: "2.0.0"},
	}
	if !reflect.DeepEqual(plugins, want) {
		t.Fatalf("plugins = %+v, want %+v", plugins, want)
	}
}

func TestHermesPluginAdapterListPluginsEmptyMessage(t *testing.T) {
	exec := func(context.Context, string, ...string) (string, string, error) {
		return "No plugins installed.\nInstall with: hermes plugins install owner/repo", "", nil
	}
	plugins, err := NewHermesPluginAdapter(exec, nil).ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("plugins = %v, want empty", plugins)
	}
}

func TestHermesPluginAdapterErrors(t *testing.T) {
	boom := errors.New("boom")
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if len(args) >= 2 && args[1] == "list" {
			return "", "list failed", boom
		}
		return "", "operation failed", boom
	}
	a := NewHermesPluginAdapter(exec, nil)
	checks := []struct {
		name string
		err  error
		want string
	}{
		{"install", a.InstallPlugin(context.Background(), config.Plugin{Source: "owner/weather"}), "hermes plugins install owner/weather"},
		{"remove", a.RemovePlugin(context.Background(), config.Plugin{Name: "weather"}), "hermes plugins remove weather"},
		{"update", a.UpdatePlugin(context.Background(), "weather", ""), "hermes plugins update weather"},
	}
	for _, check := range checks {
		if check.err == nil || !strings.Contains(check.err.Error(), check.want) || !errors.Is(check.err, boom) {
			t.Errorf("%s error = %v, want wrapped %q", check.name, check.err, check.want)
		}
	}
	if _, err := a.ListPlugins(context.Background()); err == nil || !strings.Contains(err.Error(), "list failed") || !errors.Is(err, boom) {
		t.Fatalf("list error = %v", err)
	}
	if err := a.AddMarketplace(context.Background(), config.Marketplace{}); err == nil {
		t.Fatal("AddMarketplace() error = nil")
	}
	if err := a.UpdateMarketplaces(context.Background()); err == nil {
		t.Fatal("UpdateMarketplaces() error = nil")
	}
}

func TestHermesPluginAdapterListPluginsRejectsInvalidJSON(t *testing.T) {
	exec := func(context.Context, string, ...string) (string, string, error) {
		return "not json", "", nil
	}
	_, err := NewHermesPluginAdapter(exec, nil).ListPlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), "parse json") {
		t.Fatalf("error = %v, want parse json", err)
	}
}
