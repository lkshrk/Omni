package provider_test

import (
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestPackageLookupKeys_PrefersFullSlashPackage(t *testing.T) {
	got := provider.PackageLookupKeys("playwright-test", "@playwright/test")
	want := []string{"@playwright/test", "playwright-test", "test"}
	if !slices.Equal(got, want) {
		t.Fatalf("PackageLookupKeys = %v, want %v", got, want)
	}
}

func TestLookupString_UsesFirstMatchingKey(t *testing.T) {
	got, ok := provider.LookupString(map[string]string{
		"@playwright/test": "1.52.0",
		"test":             "0.0.1",
	}, provider.PackageLookupKeys("playwright-test", "@playwright/test"))
	if !ok || got != "1.52.0" {
		t.Fatalf("LookupString = %q/%v, want 1.52.0/true", got, ok)
	}
}
