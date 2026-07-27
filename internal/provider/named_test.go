package provider_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

type namedBaseProvider struct{}

func (namedBaseProvider) Name() string        { return "node" }
func (namedBaseProvider) Description() string { return "Node tools" }
func (namedBaseProvider) Available(context.Context) (bool, error) {
	return true, nil
}
func (namedBaseProvider) Install(context.Context, provider.Tool) error   { return nil }
func (namedBaseProvider) Uninstall(context.Context, provider.Tool) error { return nil }
func (namedBaseProvider) Upgrade(context.Context, provider.Tool) error   { return nil }
func (namedBaseProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return true, "1.0.0", nil
}
func (namedBaseProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return []provider.InstalledTool{
		{Tool: provider.Tool{Name: "typescript", Provider: "node", Package: "typescript"}, Version: "5.4.0"},
	}, nil
}
func (namedBaseProvider) Search(context.Context, string) ([]provider.SearchResult, error) {
	return []provider.SearchResult{{Name: "typescript", Provider: "node"}}, nil
}

// Implements MultiManagerBulkChecker the way node/python do.
type multiBaseProvider struct {
	namedBaseProvider
}

func (multiBaseProvider) InstalledByManager(context.Context) (map[string]provider.InstalledEntry, error) {
	return map[string]provider.InstalledEntry{
		"context-mode": {Version: "1.0.0", ConcreteManager: "bun"},
	}, nil
}

func TestNamed_ForwardsMultiManagerBulkChecker(t *testing.T) {
	p := provider.Named("bun", multiBaseProvider{})

	mbc, ok := p.(provider.MultiManagerBulkChecker)
	if !ok {
		t.Fatal("named provider should expose MultiManagerBulkChecker when base implements it")
	}
	entries, err := mbc.InstalledByManager(context.Background())
	if err != nil {
		t.Fatalf("InstalledByManager: %v", err)
	}
	if e := entries["context-mode"]; e.ConcreteManager != "bun" {
		t.Fatalf("context-mode manager = %q, want bun", e.ConcreteManager)
	}
}

func TestNamed_DoesNotClaimMultiWhenBaseLacksIt(t *testing.T) {
	p := provider.Named("npm", namedBaseProvider{})
	if _, ok := p.(provider.MultiManagerBulkChecker); ok {
		t.Fatal("named provider must not claim MultiManagerBulkChecker when base lacks it")
	}
}

// Implements ManagerOutdatedInfoChecker the way node/python do.
type managerOutdatedInfoBaseProvider struct {
	namedBaseProvider
}

func (managerOutdatedInfoBaseProvider) OutdatedInfoByManager(context.Context) (map[string]map[string]provider.OutdatedInfo, error) {
	return map[string]map[string]provider.OutdatedInfo{
		"bun": {"typescript": {LatestVersion: "1.0.169"}},
	}, nil
}

func TestNamed_ForwardsManagerOutdatedInfoChecker(t *testing.T) {
	p := provider.Named("bun", managerOutdatedInfoBaseProvider{})

	moc, ok := p.(provider.ManagerOutdatedInfoChecker)
	if !ok {
		t.Fatal("named provider should expose ManagerOutdatedInfoChecker when base implements it")
	}
	byManager, err := moc.OutdatedInfoByManager(context.Background())
	if err != nil {
		t.Fatalf("OutdatedInfoByManager: %v", err)
	}
	if info := byManager["bun"]["typescript"]; info.LatestVersion != "1.0.169" {
		t.Fatalf("typescript latest = %q, want 1.0.169", info.LatestVersion)
	}
}

func TestNamed_DoesNotClaimManagerOutdatedInfoWhenBaseLacksIt(t *testing.T) {
	p := provider.Named("npm", namedBaseProvider{})
	if _, ok := p.(provider.ManagerOutdatedInfoChecker); ok {
		t.Fatal("named provider must not claim ManagerOutdatedInfoChecker when base lacks it")
	}
}

func TestNamed_OverridesProviderIdentity(t *testing.T) {
	p := provider.Named("npm", namedBaseProvider{})

	if got := p.Name(); got != "npm" {
		t.Fatalf("Name() = %q, want npm", got)
	}
	installed, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(installed) != 1 || installed[0].Provider != "npm" {
		t.Fatalf("installed = %+v, want provider npm", installed)
	}

	searcher, ok := p.(provider.Searcher)
	if !ok {
		t.Fatal("named provider should preserve Searcher")
	}
	results, err := searcher.Search(context.Background(), "typescript")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Provider != "npm" || results[0].SourceProvider != "npm" {
		t.Fatalf("results = %+v, want provider/source npm", results)
	}
}
