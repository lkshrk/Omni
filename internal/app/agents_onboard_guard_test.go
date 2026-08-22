package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboardingLegacyReaderIsNotReachableFromLifecycleCode(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, "agents_onboard") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"ExtractLegacyCandidates", "LegacyInventory", "legacyDocument"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s imports onboarding-only legacy parser symbol %s", name, forbidden)
			}
		}
	}
}
