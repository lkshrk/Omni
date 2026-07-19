package provider_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestLookupString_NoMatch(t *testing.T) {
	got, ok := provider.LookupString(map[string]string{"a": "1"}, []string{"b", "c"})
	if ok || got != "" {
		t.Fatalf("LookupString = %q/%v, want empty/false", got, ok)
	}
}

func TestLookupInstalledEntry(t *testing.T) {
	m := map[string]provider.InstalledEntry{
		"@playwright/test": {Version: "1.52.0", ConcreteManager: "bun"},
		"test":             {Version: "0.0.1", ConcreteManager: "npm"},
	}
	keys := provider.PackageLookupKeys("playwright-test", "@playwright/test")
	entry := provider.LookupInstalledEntry(m, keys)
	if entry.Version != "1.52.0" || entry.ConcreteManager != "bun" {
		t.Fatalf("LookupInstalledEntry = %+v, want 1.52.0/bun (first matching key)", entry)
	}
	if miss := provider.LookupInstalledEntry(m, []string{"nope"}); miss != (provider.InstalledEntry{}) {
		t.Fatalf("LookupInstalledEntry miss = %+v, want zero value", miss)
	}
}

func TestLookupInstalledMetadata(t *testing.T) {
	m := map[string]provider.InstalledMetadata{
		"@playwright/test": {Version: "1.52.0"},
		"test":             {Version: "0.0.1"},
	}
	keys := provider.PackageLookupKeys("playwright-test", "@playwright/test")
	meta, ok := provider.LookupInstalledMetadata(m, keys)
	if !ok || meta.Version != "1.52.0" {
		t.Fatalf("LookupInstalledMetadata = %+v/%v, want 1.52.0/true", meta, ok)
	}
	if _, ok := provider.LookupInstalledMetadata(m, []string{"nope"}); ok {
		t.Fatal("LookupInstalledMetadata should miss for absent keys")
	}
}
