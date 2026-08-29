package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/apm"
)

func setupOwnedChildFixTest(t *testing.T, template, module string) (string, *App) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	workspace := filepath.Join(home, ".apm")
	if err := os.MkdirAll(filepath.Join(workspace, "apm_modules", "acme", "bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "apm.yml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "apm.lock.yaml"), []byte(`dependencies:
- repo_url: acme/bundle
  name: bundle-a
  virtual_path: ""
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "apm_modules", "acme", "bundle", "apm.yml"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(template), 0o640); err != nil {
		t.Fatal(err)
	}
	return path, New(filepath.Join(home, "settings.json"))
}

func TestFixAgentsOwnedChildrenRemovesOnlyExactBlockItems(t *testing.T) {
	template := `name: test
version: 1.0.0
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
    # keep this conflict and its comment
    - name: context-mode
      registry: false
      transport: stdio
      command: local
targets: [claude]
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
    - name: context-mode
      registry: false
      transport: stdio
      command: package
`
	path, a := setupOwnedChildFixTest(t, template, module)
	report, err := a.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) != 1 || report.Removed[0].Name != "owned-mcp" || !report.SyncRequired {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Kept) != 1 || report.Kept[0].Name != "context-mode" || strings.Join(report.Kept[0].Fields, ",") != "command" {
		t.Fatalf("kept = %#v", report.Kept)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "name: owned-mcp") || !strings.Contains(got, "# keep this conflict") || !strings.Contains(got, "command: local") {
		t.Fatalf("repaired template:\n%s", got)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, err = %v", info, err)
	}
}

func TestFixAgentsOwnedChildrenDryRunPreservesSymlinkAndTarget(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  lsp:
    - name: owned-lsp
      command: server
`
	module := `name: bundle-a
dependencies:
  lsp:
    - name: owned-lsp
      command: server
`
	path, a := setupOwnedChildFixTest(t, template, module)
	target := filepath.Join(t.TempDir(), "apm.yml")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	report, err := a.FixAgentsOwnedChildren(t.Context(), true)
	if err != nil || len(report.Removed) != 1 {
		t.Fatalf("dry run = %#v, %v", report, err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("template symlink lost: %v, %v", info, err)
	}
	if raw, err := os.ReadFile(target); err != nil || !strings.Contains(string(raw), "owned-lsp") {
		t.Fatalf("dry run changed target: %q, %v", raw, err)
	}
	report, err = a.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Removed) != 1 {
		t.Fatalf("fix = %#v, %v", report, err)
	}
	link, err := os.Readlink(path)
	if err != nil || link != target {
		t.Fatalf("symlink = %q, %v", link, err)
	}
	if raw, err := os.ReadFile(target); err != nil || strings.Contains(string(raw), "owned-lsp") {
		t.Fatalf("target not repaired: %q, %v", raw, err)
	}
}

func TestFixAgentsOwnedChildrenRefusesFlowStyleWithoutWriting(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp: [{name: owned-mcp, registry: false, transport: stdio, command: sh}]
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	path, a := setupOwnedChildFixTest(t, template, module)
	before, _ := os.ReadFile(path)
	report, err := a.FixAgentsOwnedChildren(t.Context(), false)
	if err == nil || len(report.Kept) != 1 || report.Kept[0].Name != "owned-mcp" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("unsupported template changed:\n%s", after)
	}
}

func TestAgentsOwnedChildRemovalStopsBeforeNextTopLevelKey(t *testing.T) {
	raw := []byte("dependencies:\n  mcp:\n    - name: owned-mcp\n      command: sh\ntargets: [claude]\n")
	dep := apmMCPDep{Name: "owned-mcp", Command: "sh"}
	exact := map[string]agentsChildCollision{
		agentsChildKey(agentsChildMCP, dep.Name): {Child: agentsOwnedChild{Kind: agentsChildMCP, Name: dep.Name, MCP: &dep}, Exact: true},
	}
	ranges, unsupported, err := agentsOwnedChildRemovalRanges("apm.yml", raw, exact)
	if err != nil || len(unsupported) != 0 || len(ranges) != 1 {
		t.Fatalf("ranges = %#v, unsupported = %#v, err = %v", ranges, unsupported, err)
	}
	got := string(removeAgentsSourceRanges(raw, ranges))
	if !strings.Contains(got, "targets: [claude]") || strings.Contains(got, "owned-mcp") {
		t.Fatalf("result:\n%s", got)
	}
}

func TestFixAgentsOwnedChildrenLocksWorkspaceAcrossEvidenceAndWrite(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	path, a := setupOwnedChildFixTest(t, template, module)
	evidenceRead := make(chan struct{})
	release := make(chan struct{})
	type fixResult struct {
		report AgentsOwnedChildrenFixReport
		err    error
	}
	done := make(chan fixResult, 1)
	go func() {
		//nolint:staticcheck // Nil is intentional: the public workspace-lock boundary must normalize it safely.
		report, err := a.fixAgentsOwnedChildren(nil, false, func() {
			close(evidenceRead)
			<-release
		})
		done <- fixResult{report: report, err: err}
	}()
	<-evidenceRead

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	updated := false
	err := apm.WithGlobalWorkspaceLock(ctx, func(context.Context) error {
		updated = true
		return os.WriteFile(filepath.Join(os.Getenv("HOME"), ".apm", "apm_modules", "acme", "bundle", "apm.yml"), []byte(strings.Replace(module, "command: sh", "command: changed", 1)), 0o644)
	})
	close(release)
	result := <-done
	if err == nil || updated {
		t.Fatalf("concurrent APM update entered fixer workspace lock: updated=%v err=%v", updated, err)
	}
	if result.err != nil || len(result.report.Removed) != 1 {
		t.Fatalf("fix = %#v, err = %v", result.report, result.err)
	}
	if raw, readErr := os.ReadFile(path); readErr != nil || strings.Contains(string(raw), "owned-mcp") {
		t.Fatalf("template not repaired from stable evidence: %q, %v", raw, readErr)
	}
}

func TestFixAgentsOwnedChildrenRechecksModuleEvidenceBeforeRename(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	path, a := setupOwnedChildFixTest(t, template, module)
	modulePath := filepath.Join(os.Getenv("HOME"), ".apm", "apm_modules", "acme", "bundle", "apm.yml")
	report, err := a.fixAgentsOwnedChildren(t.Context(), false, func() {
		if writeErr := os.WriteFile(modulePath, []byte(strings.Replace(module, "command: sh", "command: changed", 1)), 0o644); writeErr != nil {
			t.Errorf("mutate module: %v", writeErr)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "manifest changed") {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if raw, readErr := os.ReadFile(path); readErr != nil || string(raw) != template {
		t.Fatalf("template changed with stale evidence: %q, %v", raw, readErr)
	}
}

func TestFixAgentsOwnedChildrenKeepsExactDuplicateWhenAnotherPackageIsUnavailable(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry-run", false: "fix"}[dryRun], func(t *testing.T) {
			template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
    - git: acme/missing
      name: missing
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
			module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
			path, a := setupOwnedChildFixTest(t, template, module)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			report, err := a.FixAgentsOwnedChildren(t.Context(), dryRun)
			if err == nil || !strings.Contains(err.Error(), "cannot verify") {
				t.Fatalf("report = %#v, err = %v", report, err)
			}
			if len(report.Removed) != 0 || len(report.Kept) != 1 || report.Kept[0].Name != "owned-mcp" || len(report.Unavailable) != 1 {
				t.Fatalf("report = %#v", report)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != string(before) {
				t.Fatalf("template changed with unavailable evidence: %q, %v", after, readErr)
			}
		})
	}
}

func TestFixAgentsOwnedChildrenRechecksEveryClassificationInput(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	for _, target := range []string{"template", "live", "lock"} {
		t.Run(target, func(t *testing.T) {
			path, a := setupOwnedChildFixTest(t, template, module)
			home := os.Getenv("HOME")
			changedPath := map[string]string{
				"template": path,
				"live":     filepath.Join(home, ".apm", "apm.yml"),
				"lock":     filepath.Join(home, ".apm", "apm.lock.yaml"),
			}[target]
			beforeInfo, err := os.Stat(changedPath)
			if err != nil {
				t.Fatal(err)
			}
			changed := ""
			report, err := a.fixAgentsOwnedChildren(t.Context(), false, func() {
				raw, readErr := os.ReadFile(changedPath)
				if readErr != nil {
					t.Errorf("read input: %v", readErr)
					return
				}
				changed = string(raw) + "# external edit\n"
				if writeErr := os.WriteFile(changedPath, []byte(changed), beforeInfo.Mode()); writeErr != nil {
					t.Errorf("edit input: %v", writeErr)
				}
			})
			if err == nil || !strings.Contains(err.Error(), "ownership input changed") {
				t.Fatalf("report = %#v, err = %v", report, err)
			}
			afterInfo, statErr := os.Stat(changedPath)
			if statErr != nil || !os.SameFile(beforeInfo, afterInfo) {
				t.Fatalf("edit did not preserve inode: before=%v after=%v err=%v", beforeInfo, afterInfo, statErr)
			}
			if raw, readErr := os.ReadFile(changedPath); readErr != nil || string(raw) != changed {
				t.Fatalf("external edit overwritten: %q, %v", raw, readErr)
			}
			if target != "template" {
				if raw, readErr := os.ReadFile(path); readErr != nil || string(raw) != template {
					t.Fatalf("template changed after %s edit: %q, %v", target, raw, readErr)
				}
			}
		})
	}
}

func TestFixAgentsOwnedChildrenRedactsInvalidYAML(t *testing.T) {
	secret := "TOP_SECRET_SCALAR"
	template := "dependencies:\n  mcp:\n    - name: owned\n      command: \"" + secret + "\n"
	path, a := setupOwnedChildFixTest(t, template, "name: bundle-a\n")
	report, err := a.FixAgentsOwnedChildren(t.Context(), false)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if raw, readErr := os.ReadFile(path); readErr != nil || string(raw) != template {
		t.Fatalf("invalid template changed: %q, %v", raw, readErr)
	}
}

func TestFixAgentsOwnedChildrenRefusesUnsafeYAMLByteForByte(t *testing.T) {
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	tests := map[string]string{
		"anchor alias and merge": `defaults: &owned
  registry: false
  transport: stdio
  command: sh
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - <<: *owned
      name: owned-mcp
`,
		"same-line sibling content": `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - {name: owned-mcp, registry: false, transport: stdio, command: sh}
`,
		"ambiguous interleaved comment": `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp # belongs to this item
      registry: false
      transport: stdio
      command: sh
`,
	}
	for name, template := range tests {
		t.Run(name, func(t *testing.T) {
			path, a := setupOwnedChildFixTest(t, template, module)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			report, _ := a.FixAgentsOwnedChildren(t.Context(), false)
			if len(report.Removed) != 0 || len(report.Kept) == 0 {
				t.Fatalf("unsafe layout was not detected and kept: %#v", report)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != string(before) {
				t.Fatalf("unsafe layout changed: %q, %v", after, readErr)
			}
		})
	}
}

func TestFixAgentsOwnedChildrenRefusesConcurrentTemplateSwaps(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	for _, swap := range []string{"symlink", "target"} {
		t.Run(swap, func(t *testing.T) {
			path, a := setupOwnedChildFixTest(t, template, module)
			target := filepath.Join(t.TempDir(), "apm.yml")
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			external := "# external replacement\n" + template
			var alternate string
			report, err := a.fixAgentsOwnedChildren(t.Context(), false, func() {
				switch swap {
				case "symlink":
					alternate = filepath.Join(t.TempDir(), "alternate.yml")
					if writeErr := os.WriteFile(alternate, []byte(external), 0o600); writeErr != nil {
						t.Errorf("write alternate: %v", writeErr)
					}
					if removeErr := os.Remove(path); removeErr != nil {
						t.Errorf("remove symlink: %v", removeErr)
					}
					if linkErr := os.Symlink(alternate, path); linkErr != nil {
						t.Errorf("swap symlink: %v", linkErr)
					}
				case "target":
					backup := target + ".original"
					if renameErr := os.Rename(target, backup); renameErr != nil {
						t.Errorf("move target: %v", renameErr)
					}
					if writeErr := os.WriteFile(target, []byte(external), 0o600); writeErr != nil {
						t.Errorf("replace target: %v", writeErr)
					}
				}
			})
			if err == nil || len(report.Removed) != 0 {
				t.Fatalf("swap accepted: report=%#v err=%v", report, err)
			}
			if swap == "symlink" {
				if link, readErr := os.Readlink(path); readErr != nil || link != alternate {
					t.Fatalf("external symlink swap overwritten: %q, %v", link, readErr)
				}
			} else if raw, readErr := os.ReadFile(target); readErr != nil || string(raw) != external {
				t.Fatalf("external target swap overwritten: %q, %v", raw, readErr)
			}
		})
	}
}

func TestWriteAgentsOwnedTemplateFailuresPreserveTargetAndSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yml")
	link := filepath.Join(dir, "apm.yml")
	original := []byte("# keep comment\ndependencies: {}\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	identity := []agentsFileContentIdentity{{path: target, hash: manifestHash(original)}}
	tests := map[string]struct {
		create func(string, string) (*os.File, error)
		rename func(string, string) error
	}{
		"temp write": {
			create: func(string, string) (*os.File, error) { return nil, errors.New("injected temp failure") },
			rename: os.Rename,
		},
		"rename": {
			create: os.CreateTemp,
			rename: func(string, string) error { return errors.New("injected rename failure") },
		},
	}
	for name, failure := range tests {
		t.Run(name, func(t *testing.T) {
			err := writeAgentsOwnedTemplateWith(target, []byte("dependencies: {}\n"), info.Mode(), nil, info, nil, identity, failure.create, failure.rename)
			if err == nil {
				t.Fatal("injected failure succeeded")
			}
			if raw, readErr := os.ReadFile(target); readErr != nil || string(raw) != string(original) {
				t.Fatalf("target changed: %q, %v", raw, readErr)
			}
			if current, statErr := os.Stat(target); statErr != nil || current.Mode().Perm() != 0o640 {
				t.Fatalf("mode changed: %v, %v", current, statErr)
			}
			if resolved, readErr := os.Readlink(link); readErr != nil || resolved != target {
				t.Fatalf("symlink changed: %q, %v", resolved, readErr)
			}
		})
	}
}
