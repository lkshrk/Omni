package testguard

import (
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
