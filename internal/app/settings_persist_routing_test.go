package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// When host_settings lives in an included fragment, a per-host settings write
// must land in that fragment — not force a second copy into the main file that
// would union-merge back and resurrect stale entries on the next load. This is
// the bug that using the non-routed Patch verb caused; the write seam routes
// the changed key to its owner instead.
func TestSaveDotsDisabled_RoutesToOwningFragment(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	fragPath := filepath.Join(dir, "settings.d", "hosts.json")

	writeFile(t, cfgPath, `{
  "version": 17,
  "$include": ["settings.d/hosts.json"],
  "settings": { "auto_import": true }
}`)
	writeFile(t, fragPath, `{
  "host_settings": { "testhost": { "dots_repo": "~/dotfiles" } }
}`)

	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled: %v", err)
	}

	// The fragment owns host_settings and must carry the new flag.
	frag := readKeys(t, fragPath)
	if _, ok := frag["host_settings"]; !ok {
		t.Fatal("host_settings vanished from the owning fragment")
	}
	var hs map[string]map[string]json.RawMessage
	if err := json.Unmarshal(frag["host_settings"], &hs); err != nil {
		t.Fatalf("parse fragment host_settings: %v", err)
	}
	if _, ok := hs["testhost"]["dots_disabled"]; !ok {
		t.Fatalf("dots_disabled not written to the fragment: %s", frag["host_settings"])
	}
	if _, ok := hs["testhost"]["dots_repo"]; !ok {
		t.Fatal("existing dots_repo dropped from the fragment host entry")
	}

	// The main file must NOT gain a competing host_settings copy.
	main := readKeys(t, cfgPath)
	if raw, ok := main["host_settings"]; ok && string(raw) != "null" {
		t.Fatalf("host_settings resurrected into the main file: %s", raw)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return raw
}
