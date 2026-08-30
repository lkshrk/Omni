//go:build integration

package integration_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinarySettingsReadCommandsUseIsolatedConfig(t *testing.T) {
	root, _, cache, env := newCLIBinarySandbox(t)
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AutoImport: true},
		Hosts:    map[string][]string{"testhost": {}},
		Groups:   []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	bin := buildOmniBinary(t)
	get := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "get", "auto_import")
	if strings.TrimSpace(get) != "true" {
		t.Fatalf("settings get auto_import = %q", get)
	}
	show := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "show", "auto_import", "--format", "json")
	var shown struct {
		AutoImport bool `json:"auto_import"`
	}
	if err := json.Unmarshal([]byte(show), &shown); err != nil || !shown.AutoImport {
		t.Fatalf("settings show auto_import = %q, %v", show, err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "lint")
}
