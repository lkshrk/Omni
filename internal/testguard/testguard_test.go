package testguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireTempPathRejectsLiveLocalPath(t *testing.T) {
	if !Active() {
		t.Skip("local test guard inactive")
	}
	err := RequireTempPath("config", filepath.Join(string(filepath.Separator), "etc", "omni", "settings.json"))
	if err == nil {
		t.Fatal("RequireTempPath accepted a live path")
	}
	if !strings.Contains(err.Error(), "outside a temp directory") {
		t.Fatalf("err = %v, want outside-temp message", err)
	}
}

func TestRequireTempPathAcceptsTempPath(t *testing.T) {
	if err := RequireTempPath("config", filepath.Join(t.TempDir(), "settings.json")); err != nil {
		t.Fatalf("RequireTempPath rejected temp path: %v", err)
	}
}

func TestEnsureSafeEnvSetsExplicitOmniPaths(t *testing.T) {
	if !Active() {
		t.Skip("local test guard inactive")
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "OMNI_CACHE_DIR", "OMNI_CONFIG"} {
		got := os.Getenv(key)
		if got == "" {
			t.Fatalf("%s is empty", key)
		}
		if !PathInTempRoot(got) {
			t.Fatalf("%s = %q, want path under temp root", key, got)
		}
	}
	if got := os.Getenv("OMNI_CONFIG"); !strings.HasSuffix(got, filepath.Join("xdg-config", "omni", "settings.json")) {
		t.Fatalf("OMNI_CONFIG = %q, want xdg-config/omni/settings.json", got)
	}
}
