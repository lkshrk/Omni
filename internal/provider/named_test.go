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
