package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsLegacyAgentsDeclarations(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	for _, tc := range []struct {
		name     string
		settings string
		want     bool
	}{
		{
			name: "agents section",
			settings: `{
  "version": 24,
  "agents": {"codex": {"skills": []}},
  "hosts": {"testhost": ["dev"]},
  "groups": [{"name": "testhost", "special": "host"}, {"name": "dev"}]
}`,
			want: true,
		},
		{
			name: "clean config",
			settings: `{
  "version": 24,
  "hosts": {"testhost": ["dev"]},
  "groups": [{"name": "testhost", "special": "host"}, {"name": "dev"}]
}`,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(cfgPath, []byte(tc.settings), 0o644); err != nil {
				t.Fatal(err)
			}
			output, _ := runRootCommand(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "--state-dir", t.TempDir(), "doctor")
			got := strings.Contains(output, "agents declarations present")
			if got != tc.want {
				t.Fatalf("legacy finding = %v, want %v\n%s", got, tc.want, output)
			}
			if tc.want && !strings.Contains(output, "omni agents migrate --host <host>") {
				t.Fatalf("finding must name the migrate command:\n%s", output)
			}
		})
	}
}
