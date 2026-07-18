package tui

import (
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestMcpUnmanagedAgentsFor(t *testing.T) {
	unmanaged := map[string][]app.InstalledMcpServer{
		"claude": {{Name: "srv", Transport: "stdio", Command: "echo"}},
		"codex":  {{Name: "srv", Transport: "stdio", Command: "echo"}},
	}

	t.Run("union of clicked plus all agents with the name", func(t *testing.T) {
		got := mcpUnmanagedAgentsFor(unmanaged, "srv", "claude")
		want := []string{"claude", "codex"}
		if !slices.Equal(got, want) {
			t.Errorf("mcpUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})

	t.Run("clicked agent outside the unmanaged set is still included", func(t *testing.T) {
		got := mcpUnmanagedAgentsFor(unmanaged, "srv", "gemini")
		want := []string{"claude", "codex", "gemini"}
		if !slices.Equal(got, want) {
			t.Errorf("mcpUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})

	t.Run("name absent everywhere returns just the clicked agent", func(t *testing.T) {
		got := mcpUnmanagedAgentsFor(unmanaged, "other-srv", "claude")
		want := []string{"claude"}
		if !slices.Equal(got, want) {
			t.Errorf("mcpUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})
}

func TestMcpUnmanagedConflict(t *testing.T) {
	first := app.InstalledMcpServer{Name: "srv", Transport: "stdio", Command: "echo"}

	t.Run("differing command across agents is a conflict", func(t *testing.T) {
		unmanaged := map[string][]app.InstalledMcpServer{
			"claude": {first},
			"codex":  {{Name: "srv", Transport: "stdio", Command: "cat"}},
		}
		if !mcpUnmanagedConflict(unmanaged, "srv", first) {
			t.Error("expected conflict for differing Command")
		}
	})

	t.Run("matching config across agents is not a conflict", func(t *testing.T) {
		unmanaged := map[string][]app.InstalledMcpServer{
			"claude": {first},
			"codex":  {first},
		}
		if mcpUnmanagedConflict(unmanaged, "srv", first) {
			t.Error("expected no conflict for matching config")
		}
	})

	t.Run("differing headers across agents is a conflict", func(t *testing.T) {
		withHeaders := app.InstalledMcpServer{Name: "srv", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "one"}}
		unmanaged := map[string][]app.InstalledMcpServer{
			"claude": {withHeaders},
			"codex":  {{Name: "srv", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "two"}}},
		}
		if !mcpUnmanagedConflict(unmanaged, "srv", withHeaders) {
			t.Error("expected conflict for differing headers")
		}
	})
}

func TestPluginUnmanagedAgentsFor(t *testing.T) {
	unmanaged := map[string][]app.InstalledPlugin{
		"claude": {{Name: "plugin", Marketplace: "acme"}},
		"codex":  {{Name: "plugin", Marketplace: "acme"}},
	}

	t.Run("union of clicked plus all agents with the name", func(t *testing.T) {
		got := pluginUnmanagedAgentsFor(unmanaged, "plugin", "codex")
		want := []string{"claude", "codex"}
		if !slices.Equal(got, want) {
			t.Errorf("pluginUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})

	t.Run("clicked agent outside the unmanaged set is still included", func(t *testing.T) {
		got := pluginUnmanagedAgentsFor(unmanaged, "plugin", "gemini")
		want := []string{"claude", "codex", "gemini"}
		if !slices.Equal(got, want) {
			t.Errorf("pluginUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})

	t.Run("name absent everywhere returns just the clicked agent", func(t *testing.T) {
		got := pluginUnmanagedAgentsFor(unmanaged, "other-plugin", "claude")
		want := []string{"claude"}
		if !slices.Equal(got, want) {
			t.Errorf("pluginUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})
}

func TestPluginUnmanagedConflict(t *testing.T) {
	first := app.InstalledPlugin{Name: "plugin", Marketplace: "acme"}

	t.Run("differing marketplace across agents is a conflict", func(t *testing.T) {
		unmanaged := map[string][]app.InstalledPlugin{
			"claude": {first},
			"codex":  {{Name: "plugin", Marketplace: "other"}},
		}
		if !pluginUnmanagedConflict(unmanaged, "plugin", first) {
			t.Error("expected conflict for differing Marketplace")
		}
	})

	t.Run("matching config across agents is not a conflict", func(t *testing.T) {
		unmanaged := map[string][]app.InstalledPlugin{
			"claude": {first},
			"codex":  {first},
		}
		if pluginUnmanagedConflict(unmanaged, "plugin", first) {
			t.Error("expected no conflict for matching config")
		}
	})
}

func TestMarketplaceUnmanagedAgentsFor(t *testing.T) {
	unmanaged := map[string][]app.InstalledMarketplace{
		"claude": {{Name: "mk", Source: "acme/mk"}},
		"codex":  {{Name: "mk", Source: "acme/mk"}},
	}

	t.Run("union of clicked plus all agents with the name", func(t *testing.T) {
		got := marketplaceUnmanagedAgentsFor(unmanaged, "mk", "claude")
		want := []string{"claude", "codex"}
		if !slices.Equal(got, want) {
			t.Errorf("marketplaceUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})

	t.Run("clicked agent outside the unmanaged set is still included", func(t *testing.T) {
		got := marketplaceUnmanagedAgentsFor(unmanaged, "mk", "gemini")
		want := []string{"claude", "codex", "gemini"}
		if !slices.Equal(got, want) {
			t.Errorf("marketplaceUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})

	t.Run("name absent everywhere returns just the clicked agent", func(t *testing.T) {
		got := marketplaceUnmanagedAgentsFor(unmanaged, "other-mk", "claude")
		want := []string{"claude"}
		if !slices.Equal(got, want) {
			t.Errorf("marketplaceUnmanagedAgentsFor = %v, want %v", got, want)
		}
	})
}

func TestMarketplaceUnmanagedConflict(t *testing.T) {
	first := app.InstalledMarketplace{Name: "mk", Source: "acme/mk"}

	t.Run("differing source across agents is a conflict", func(t *testing.T) {
		unmanaged := map[string][]app.InstalledMarketplace{
			"claude": {first},
			"codex":  {{Name: "mk", Source: "other/mk"}},
		}
		if !marketplaceUnmanagedConflict(unmanaged, "mk", first) {
			t.Error("expected conflict for differing Source")
		}
	})

	t.Run("matching config across agents is not a conflict", func(t *testing.T) {
		unmanaged := map[string][]app.InstalledMarketplace{
			"claude": {first},
			"codex":  {first},
		}
		if marketplaceUnmanagedConflict(unmanaged, "mk", first) {
			t.Error("expected no conflict for matching config")
		}
	})
}
