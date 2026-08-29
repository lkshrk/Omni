package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupManifestlessOwnedFixTest(t *testing.T) (templatePath, targetPath string, a *App, skillModule string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
    - git: acme/skills
      name: skills
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	lock := `dependencies:
- repo_url: acme/bundle
  name: bundle-a
  virtual_path: ""
- repo_url: acme/skills
  name: skills
  virtual_path: ""
  package_type: skill_bundle
  resolved_commit: deadbeef
  deployed_files:
  - .agents/skills/demo/SKILL.md
`
	workspace := filepath.Join(home, ".apm")
	writeFile(t, filepath.Join(workspace, "apm.yml"), template)
	writeFile(t, filepath.Join(workspace, "apm.lock.yaml"), lock)
	writeFile(t, filepath.Join(workspace, "apm_modules", "acme", "bundle", "apm.yml"), `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`)
	skillModule = filepath.Join(workspace, "apm_modules", "acme", "skills")
	writeSkillManifest(t, skillModule, "skills/demo/SKILL.md")
	templatePath, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	targetPath = filepath.Join(home, "agents-template.yml")
	if err := os.WriteFile(targetPath, []byte(template), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, templatePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return templatePath, targetPath, New(filepath.Join(home, "settings.json")), skillModule
}

func TestFixAgentsOwnedChildrenAcceptsManifestlessSkillEvidence(t *testing.T) {
	_, target, a, _ := setupManifestlessOwnedFixTest(t)
	report, err := a.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Removed) != 1 || report.Removed[0].Name != "owned-mcp" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || strings.Contains(string(raw), "name: owned-mcp") {
		t.Fatalf("template = %q, err = %v", raw, err)
	}
}

func TestFixAgentsOwnedChildrenRejectsChangedManifestlessEvidenceWithoutWriting(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, mutation := range []string{"candidate", "skill", "root", "commit", "inventory"} {
			t.Run(map[bool]string{true: "dry-run", false: "write"}[dryRun]+"/"+mutation, func(t *testing.T) {
				link, target, a, module := setupManifestlessOwnedFixTest(t)
				before, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				beforeInfo, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				report, err := a.fixAgentsOwnedChildren(t.Context(), dryRun, func() {
					switch mutation {
					case "candidate":
						writeFile(t, filepath.Join(module, "mcp.json"), "{}\n")
					case "skill":
						writeFile(t, filepath.Join(module, "skills", "demo", "SKILL.md"), "changed\n")
					case "root":
						if renameErr := os.Rename(module, module+".old"); renameErr != nil {
							t.Errorf("replace module root: %v", renameErr)
							return
						}
						writeSkillManifest(t, module, "skills/demo/SKILL.md")
					case "commit", "inventory":
						lockPath := filepath.Join(os.Getenv("HOME"), ".apm", "apm.lock.yaml")
						raw, readErr := os.ReadFile(lockPath)
						if readErr != nil {
							t.Errorf("read lock: %v", readErr)
							return
						}
						changed := strings.Replace(string(raw), "deadbeef", "changed", 1)
						if mutation == "inventory" {
							changed = strings.Replace(string(raw), ".agents/skills/demo/SKILL.md", ".agents/skills/other/SKILL.md", 1)
						}
						writeFile(t, lockPath, changed)
					}
				})
				if err == nil || !strings.Contains(err.Error(), "package ownership evidence changed") || len(report.Removed) != 0 {
					t.Fatalf("report = %#v, err = %v", report, err)
				}
				after, readErr := os.ReadFile(target)
				afterInfo, statErr := os.Stat(target)
				resolved, linkErr := os.Readlink(link)
				if readErr != nil || string(after) != string(before) || statErr != nil || !os.SameFile(beforeInfo, afterInfo) ||
					linkErr != nil || resolved != target {
					t.Fatalf("template changed: bytes=%q read=%v stat=%v link=%q/%v", after, readErr, statErr, resolved, linkErr)
				}
			})
		}
	}
}

func TestFixAgentsOwnedChildrenSkipsInactiveInaccessibleHomeWithoutMutation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "no-home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	report, err := New(filepath.Join(home, "settings.json")).FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Removed) != 0 || len(report.Kept) != 0 || report.SyncRequired {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if _, statErr := os.Lstat(home); !os.IsNotExist(statErr) {
		t.Fatalf("inactive fixer mutated home: %v", statErr)
	}
}

func TestFixAgentsOwnedChildrenFailsClosedWhenLiveStateExistsWithoutTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	live := filepath.Join(home, ".apm", "apm.yml")
	writeFile(t, live, "dependencies: {}\n")
	report, err := New(filepath.Join(home, "settings.json")).FixAgentsOwnedChildren(t.Context(), false)
	if err == nil || len(report.Removed) != 0 || len(report.Kept) != 0 {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if raw, readErr := os.ReadFile(live); readErr != nil || string(raw) != "dependencies: {}\n" {
		t.Fatalf("live manifest changed: %q, %v", raw, readErr)
	}
}
