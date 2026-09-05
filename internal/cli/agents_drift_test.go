package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsDriftReportsNativeItemsAndIgnoredCount(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	fakeNativeCLIs(t, home)
	cfgPath := filepath.Join(home, "settings.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":25,"hosts":{"testhost":["dev"]},"groups":[{"name":"testhost","special":"host"},{"name":"dev"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "--state-dir", t.TempDir(), "agents", "drift")
	if err != nil {
		t.Fatalf("drift failed: %v\n%s", err, output)
	}
	for _, want := range []string{"Native items APM does not manage:", "  claude  plugin  demo@official  ", "Ignored: 0"} {
		if !strings.Contains(output, want) {
			t.Fatalf("drift report missing %q:\n%s", want, output)
		}
	}
}
