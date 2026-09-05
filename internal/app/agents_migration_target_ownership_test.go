package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func nativeBothClientsApp(t *testing.T) *App {
	t.Helper()
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
		nativeRule("codex plugin list --json", `[{"name":"demo","marketplaceName":"official"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"official","marketplaceSource":{"source":"acme/plugins"}}]`),
	)
	return a
}

func writeAPMManagedLock(t *testing.T, body string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), body)
}

func TestAPMOwnershipIsScopedToTheDeployingTarget(t *testing.T) {
	for _, test := range []struct {
		name        string
		subset      string
		wantManaged []string
		wantNative  []string
	}{
		{
			name:        "claude only",
			subset:      "  target_subset:\n  - claude\n",
			wantManaged: []string{"claude  plugin  demo@official"},
			wantNative:  []string{"codex  plugin  demo@official"},
		},
		{
			name:        "both targets",
			subset:      "  target_subset:\n  - claude\n  - codex\n",
			wantManaged: []string{"claude  plugin  demo@official", "codex  plugin  demo@official"},
		},
		{
			name:        "no subset deploys everywhere",
			wantManaged: []string{"claude  plugin  demo@official", "codex  plugin  demo@official"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := nativeBothClientsApp(t)
			writeAPMManagedLock(t, "lockfile_version: '1'\ndependencies:\n- repo_url: acme/plugins\n  name: demo\n"+test.subset)

			_, rendered, err := nativePlanFor(t, a)
			if err != nil {
				t.Fatal(err)
			}
			managed := sectionRows(rendered, managedSectionTitle)
			if !slices.Equal(managed, test.wantManaged) {
				t.Fatalf("managed = %q, want %q:\n%s", managed, test.wantManaged, rendered)
			}
			replaced := sectionRows(rendered, replacedSectionTitle)
			for _, want := range test.wantNative {
				if !slices.ContainsFunc(replaced, func(row string) bool { return strings.HasPrefix(row, want) }) {
					t.Fatalf("replaced = %q, want a row for %q:\n%s", replaced, want, rendered)
				}
			}
			for _, unwanted := range test.wantManaged {
				if slices.ContainsFunc(replaced, func(row string) bool { return strings.HasPrefix(row, unwanted) }) {
					t.Fatalf("managed row %q was also replaced: %q", unwanted, replaced)
				}
			}
		})
	}
}

func TestAPMOwnershipOnOneTargetLeavesTheOtherInTheManifest(t *testing.T) {
	a := nativeBothClientsApp(t)
	writeAPMManagedLock(t, "lockfile_version: '1'\ndependencies:\n- repo_url: acme/plugins\n  name: demo\n  target_subset:\n  - claude\n")

	plan, rendered, err := nativePlanFor(t, a)
	if err != nil {
		t.Fatal(err)
	}
	decl := string(plan.Decls.Plugins["demo@official"])
	if !strings.Contains(decl, `"agents":["codex"]`) {
		t.Fatalf("plugin declaration = %s:\n%s", decl, rendered)
	}
}
