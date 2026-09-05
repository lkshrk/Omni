package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

const adoptTestHost = "adopt-host"

func adoptPluginRules() []executor.MatchRule {
	return []executor.MatchRule{
		nativeRule("claude plugins list --json", `[{"id":"demo@official"},{"id":"orphan@nosource"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"},{"name":"nosource","source":"github","repo":""}]`),
	}
}

func newAdoptApp(t *testing.T, settings string, rules ...executor.MatchRule) (*App, *nativeInventoryExecutor, string) {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", adoptTestHost)
	a, exec := newNativeInventoryApp(t, map[string]bool{"claude": true}, rules...)
	if settings == "" {
		settings = "{}\n"
	}
	if err := os.WriteFile(a.ConfigPath, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return a, exec, home
}

func adoptTemplatePath(t *testing.T) string {
	t.Helper()
	path, err := agentsAdoptTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeAdoptFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func homeSnapshot(t *testing.T, home string) string {
	t.Helper()
	var rows []string
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		row := fmt.Sprintf("%s %s", path, info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			row += " -> " + target
		case info.Mode().IsRegular():
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			row += " " + hex.EncodeToString(sum[:])
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

func adoptPreviewNoWrite(t *testing.T, a *App, home string) string {
	t.Helper()
	before := homeSnapshot(t, home)
	out, err := a.AgentsAdopt(t.Context(), adoptTestHost)
	if err != nil {
		t.Fatal(err)
	}
	if after := homeSnapshot(t, home); after != before {
		t.Fatalf("adopt preview changed the home tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	return out
}

func adoptDotsSettings(t *testing.T, repo, pkg string) string {
	t.Helper()
	return fmt.Sprintf(`{"settings":{"dots_repo":%q},"groups":[{"name":"dev","dots":[{"name":"omni-config","path":"~/config/omni","package":%q}]}]}`+"\n", repo, pkg)
}

func TestAgentsAdoptBareHostWouldWriteTheTemplate(t *testing.T) {
	a, _, home := newAdoptApp(t, "", adoptPluginRules()...)
	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out,
		"Template: "+adoptTemplatePath(t)+" ("+adoptShapeBare+")",
		"Action: "+adoptActionWriteTemplate,
		adoptAppendedTitle,
		"apm  mkt:official/demo",
		adoptFooter,
	)
}

func TestAgentsAdoptMigrationOwnedTemplateWouldUpdateIt(t *testing.T) {
	a, _, home := newAdoptApp(t, "", adoptPluginRules()...)
	writeAdoptFile(t, adoptTemplatePath(t), agentsMigrationMarker+"\nname: omni-migrated\nversion: 1.0.0\ndependencies: {}\n")
	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out, "("+adoptShapeBare+")", "Action: "+adoptActionUpdateTemplate, "apm  mkt:official/demo")
}

func TestAgentsAdoptForeignTemplateIsRefused(t *testing.T) {
	a, _, home := newAdoptApp(t, "", adoptPluginRules()...)
	writeAdoptFile(t, adoptTemplatePath(t), "name: hand-written\n")
	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out, "("+adoptShapeForeign+")", "Action: "+adoptActionRefuse)
}

func TestAgentsAdoptUnlinkedRepoTemplateWouldLinkIt(t *testing.T) {
	repo := t.TempDir()
	a, _, home := newAdoptApp(t, adoptDotsSettings(t, repo, "omni-config"), adoptPluginRules()...)
	source := filepath.Join(repo, "dotfiles", "omni-config", "config", "omni", "apm.yml")
	writeAdoptFile(t, source, agentsMigrationMarker+"\nname: omni-migrated\nversion: 1.0.0\ndependencies: {}\n")

	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out,
		"("+adoptShapeDotfilesUnlinked+")",
		"Repo template: "+source,
		"Action: "+adoptActionLinkRepo,
		"apm  mkt:official/demo",
	)
}

func TestAgentsAdoptLinkedTemplateWouldEmitAManifestToCommit(t *testing.T) {
	repo := t.TempDir()
	a, _, home := newAdoptApp(t, adoptDotsSettings(t, repo, "omni-config"), adoptPluginRules()...)
	source := filepath.Join(repo, "dotfiles", "omni-config", "config", "omni", "apm.yml")
	writeAdoptFile(t, source, agentsMigrationMarker+"\nname: omni-migrated\nversion: 1.0.0\ndependencies: {}\n")
	template := adoptTemplatePath(t)
	if err := os.MkdirAll(filepath.Dir(template), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, template); err != nil {
		t.Fatal(err)
	}

	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out,
		"("+adoptShapeDotfilesLinked+")",
		"Link target: ",
		"Action: "+adoptActionEmitManifest,
		"apm  mkt:official/demo",
	)
}

func TestAgentsAdoptRunsNoMutatingClientVerb(t *testing.T) {
	a, exec, home := newAdoptApp(t, "", adoptPluginRules()...)
	adoptPreviewNoWrite(t, a, home)
	if len(exec.Calls) == 0 {
		t.Fatal("adopt gathered no native evidence")
	}
	for _, call := range exec.Calls {
		line := call.Name + " " + strings.Join(call.Args, " ")
		for _, verb := range []string{"uninstall", "remove", "install", "add", "rm", "sync", "write"} {
			if slicesContainsWord(call.Args, verb) {
				t.Fatalf("adopt ran a mutating verb %q: %s", verb, line)
			}
		}
	}
}

func slicesContainsWord(args []string, word string) bool {
	for _, arg := range args {
		if arg == word {
			return true
		}
	}
	return false
}

func TestAgentsAdoptReportsMergeConflicts(t *testing.T) {
	a, _, home := newAdoptApp(t, "", adoptPluginRules()...)
	writeAdoptFile(t, adoptTemplatePath(t), agentsMigrationMarker+`
name: omni-migrated
version: 1.0.0
dependencies:
  apm:
    - name: demo
      marketplace: official
      ref: pinned
      targets:
        - claude
`)
	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out, adoptRejectedTitle, "apm  mkt:official/demo  declared ref differs from the candidate")
	assertNotContains(t, out, adoptAppendedTitle+"\n  apm  mkt:official/demo")
}

func TestAgentsAdoptReportsUnreachableClientAsIncompleteCoverage(t *testing.T) {
	rules := append(adoptPluginRules(), executor.MatchRule{
		Pattern:  "codex plugin list --json",
		Response: executor.MockCall{Err: errors.New("codex exploded")},
	})
	a, _, home := newAdoptApp(t, "", rules...)
	a.SetFallbackExecutor(&nativeInventoryExecutor{
		MatchMockExecutor: executor.NewMatchMock(rules...),
		available:         map[string]bool{"claude": true, "codex": true},
	})
	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out, adoptCoverageTitle, "codex: ")
	assertNotContains(t, out, "nothing to adopt")
}

func TestAgentsAdoptReportsLegacyDarwinTemplateWithoutAdoptingIt(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("legacy template lives only on darwin")
	}
	a, _, home := newAdoptApp(t, "", adoptPluginRules()...)
	legacy := filepath.Join(home, "Library", "Application Support", "omni", "apm.yml")
	writeAdoptFile(t, legacy, agentsMigrationMarker+"\nname: legacy\nversion: 1.0.0\ndependencies: {}\n")

	out := adoptPreviewNoWrite(t, a, home)
	assertContains(t, out, adoptLegacyDarwinTitle, legacy, "Action: "+adoptActionWriteTemplate)
	if _, err := os.Lstat(adoptTemplatePath(t)); !os.IsNotExist(err) {
		t.Fatalf("adopt materialised the canonical template: %v", err)
	}
}

func TestAgentsAdoptRequiresAHost(t *testing.T) {
	a, _, _ := newAdoptApp(t, "")
	if _, err := a.AgentsAdopt(t.Context(), " "); err == nil {
		t.Fatal("adopt accepted an empty host")
	}
}
