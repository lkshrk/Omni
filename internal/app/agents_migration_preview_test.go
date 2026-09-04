package app

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeFixtureApp(t *testing.T, fixture string) (*App, *nativeInventoryExecutor, string) {
	t.Helper()
	return seedNativeFixture(t, filepath.Join("testdata", "agents_native", fixture))
}

func hashTrees(t *testing.T, roots ...string) string {
	t.Helper()
	digest := sha256.New()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			fmt.Fprintf(digest, "%s\x00%s\x00", path, info.Mode())
			if entry.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest.Write(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func harnessDirs(t *testing.T, home string) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(home, ".claude*"))
	if err != nil {
		t.Fatal(err)
	}
	return append(dirs, filepath.Join(home, ".codex"), filepath.Join(home, ".apm"))
}

func TestMigratePreviewIsReadOnly(t *testing.T) {
	a, _, home := nativeFixtureApp(t, "plugin-owned-child")
	before := hashTrees(t, home)
	preview, err := a.BuildAgentsMigrationPreview(t.Context(), "host", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Render(), replacedSectionTitle) {
		t.Fatalf("preview reported nothing to replace:\n%s", preview.Render())
	}
	if after := hashTrees(t, home); after != before {
		t.Fatalf("preview changed HOME: %s -> %s", before, after)
	}
}

func TestMigratePreviewUnionsMCPReachWithTrailer(t *testing.T) {
	a, exec, _ := nativeFixtureApp(t, "claude-only-stdio")
	exec.available["codex"] = true
	exec.AddRule(nativeRule("codex plugin list --json", `[{"name":"tools","marketplaceName":"official"}]`))
	exec.AddRule(nativeRule("codex plugin marketplace list --json", `[{"name":"official","marketplaceSource":{"source":"acme/plugins"}}]`))

	preview, err := a.BuildAgentsMigrationPreview(t.Context(), "host", "")
	if err != nil {
		t.Fatal(err)
	}
	rendered := preview.Render()
	if count := strings.Count(rendered, "name: demo"); count != 1 {
		t.Fatalf("server rendered %d times:\n%s", count, rendered)
	}
	for _, want := range []string{"# reach: claude (apm deploys to all MCP targets): demo", "targets:\n  - claude\n  - codex"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preview missing %q:\n%s", want, rendered)
		}
	}
}

func TestMigratePreviewKeepsLegacyAndNativeTogether(t *testing.T) {
	a, _, _ := nativeFixtureApp(t, "claude-only-stdio")
	preview, err := a.BuildAgentsMigrationPreview(t.Context(), "h", migrationWriteFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	rendered := preview.Render()
	for _, want := range []string{"name: independent", "name: demo", "  claude  mcp  demo  "} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preview missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "mcp  independent") {
		t.Fatalf("snapshot declaration was reported as a replaced native entry:\n%s", rendered)
	}
}

func TestMigratePreviewOmitsAPMManagedServers(t *testing.T) {
	lock := filepath.Join(".apm", "apm.lock.yaml")
	for _, test := range []struct {
		name        string
		lockfile    string
		wantManaged bool
	}{
		{name: "listed in mcp_servers", wantManaged: true},
		{name: "command path under apm_modules", lockfile: "dependencies:\n- repo_url: acme/unrelated\n  name: unrelated\n", wantManaged: true},
		{name: "no lockfile", lockfile: "absent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := nativeFixtureApp(t, "already-managed")
			switch {
			case test.lockfile == "absent":
				if err := os.Remove(filepath.Join(home, lock)); err != nil {
					t.Fatal(err)
				}
			case test.lockfile != "":
				if err := os.WriteFile(filepath.Join(home, lock), []byte(test.lockfile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := a.BuildAgentsMigrationPreview(t.Context(), "host", "")
			if err != nil {
				t.Fatal(err)
			}
			rendered := preview.Render()
			inManifest := strings.Contains(preview.Manifest, "name: context-mode")
			managed := strings.Contains(rendered, managedSectionTitle) && strings.Contains(rendered, "  codex  mcp  context-mode")
			if inManifest == test.wantManaged || managed != test.wantManaged {
				t.Fatalf("manifest=%v managed=%v, want managed %v:\n%s", inManifest, managed, test.wantManaged, rendered)
			}
		})
	}
}

func TestMigratePreviewClassifiesGeneratedLSPPluginAsManaged(t *testing.T) {
	for _, test := range []struct {
		name     string
		lockfile bool
	}{
		{name: "lockfile locks an LSP server", lockfile: true},
		{name: "no lockfile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := nativeFixtureApp(t, "apm-generated-lsp-plugin")
			if !test.lockfile {
				if err := os.Remove(filepath.Join(home, ".apm", "apm.lock.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := a.BuildAgentsMigrationPreview(t.Context(), "host", "")
			if err != nil {
				t.Fatal(err)
			}
			rendered := preview.Render()
			managed := sectionRows(rendered, managedSectionTitle)
			retained := sectionRows(rendered, retainedSectionTitle)
			want := []string{"claude  marketplace  skills-dir", "claude  plugin  apm-lsp@skills-dir"}
			if !test.lockfile {
				if len(managed) != 0 || len(retained) != 2 || !strings.Contains(retained[0], agentReasonNoSource) {
					t.Fatalf("managed=%q retained=%q:\n%s", managed, retained, rendered)
				}
				return
			}
			if len(retained) != 0 || len(managed) != len(want) {
				t.Fatalf("managed=%q retained=%q:\n%s", managed, retained, rendered)
			}
			for i, row := range want {
				if managed[i] != row {
					t.Fatalf("managed row %d = %q, want %q:\n%s", i, managed[i], row, rendered)
				}
			}
		})
	}
}

func TestMigrateWriteNeverTouchesHarnessDirs(t *testing.T) {
	a, _, home := nativeFixtureApp(t, "plugin-owned-child")
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.unrelated]\ncommand = \"true\"\n")
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "live: unchanged\n")
	dirs := harnessDirs(t, home)
	before := hashTrees(t, dirs...)

	if _, err := a.AgentsMigrateWrite(t.Context(), "host", ""); err != nil {
		t.Fatal(err)
	}
	template, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(template); err != nil {
		t.Fatalf("write did not publish the host template: %v", err)
	}
	if after := hashTrees(t, dirs...); after != before {
		t.Fatalf("write changed a harness directory: %s -> %s", before, after)
	}
}

func TestMigratePreviewClassifiesManagedBeforeSecretRetention(t *testing.T) {
	const literal = "sk-fixture-literal"
	for _, test := range []struct {
		name     string
		lockfile bool
	}{
		{name: "lockfile lists the server", lockfile: true},
		{name: "fresh host"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := nativeFixtureApp(t, "managed-literal-secret")
			if !test.lockfile {
				if err := os.Remove(filepath.Join(home, ".apm", "apm.lock.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := a.BuildAgentsMigrationPreview(t.Context(), "host", "")
			if err != nil {
				t.Fatal(err)
			}
			rendered := preview.Render()
			if strings.Contains(rendered, literal) {
				t.Fatalf("preview echoed the literal secret:\n%s", rendered)
			}
			if !strings.Contains(preview.Manifest, "name: clean") {
				t.Fatalf("clean server was not proposed:\n%s", rendered)
			}
			managed := sectionRows(rendered, managedSectionTitle)
			retained := sectionRows(rendered, retainedSectionTitle)
			if test.lockfile {
				if len(managed) != 1 || !strings.HasPrefix(managed[0], "claude  mcp  remote") || len(retained) != 0 {
					t.Fatalf("managed=%q retained=%q:\n%s", managed, retained, rendered)
				}
				if managed[0] != "claude  mcp  remote" {
					t.Fatalf("managed row carried a reason: %q", managed[0])
				}
				return
			}
			if len(managed) != 0 || len(retained) != 1 || !strings.Contains(retained[0], "header x-litellm-api-key") {
				t.Fatalf("managed=%q retained=%q:\n%s", managed, retained, rendered)
			}
		})
	}
}

func sectionRows(rendered, title string) []string {
	_, after, found := strings.Cut(rendered, title+"\n")
	if !found {
		return nil
	}
	var rows []string
	for _, line := range strings.Split(after, "\n") {
		row, indented := strings.CutPrefix(line, "  ")
		if !indented {
			break
		}
		rows = append(rows, row)
	}
	return rows
}
