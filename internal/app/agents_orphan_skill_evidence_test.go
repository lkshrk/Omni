package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const orphanSkillTemplate = `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`

// A transitive skill package is locked but absent from the manifest, so it joins as an orphan row.
const orphanSkillLock = `dependencies:
- repo_url: acme/bundle
  name: bundle-a
  virtual_path: ""
- repo_url: shiplightai/agent-skills-v2
  name: shiplight
  virtual_path: shiplight
  package_type: claude_skill
  resolved_commit: deadbeef
  deployed_files:
  - .agents/skills/shiplight/SKILL.md
`

func setupOrphanSkillWorkspace(t *testing.T) (workspace, skillModule string, a *App) {
	t.Helper()
	a = setupAgentsWorkspace(t, orphanSkillTemplate, orphanSkillLock)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	workspace = filepath.Join(home, ".apm")
	writeFile(t, filepath.Join(workspace, "apm_modules", "acme", "bundle", "apm.yml"), `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`)
	skillModule = filepath.Join(workspace, "apm_modules", "ShiplightAI", "agent-skills-v2", "shiplight")
	writeSkillManifest(t, skillModule, "SKILL.md")
	return workspace, skillModule, a
}

func orphanSkillEvidence(t *testing.T, workspace string) agentsOwnershipEvidence {
	t.Helper()
	manifest, lock, err := readAPMWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	return readAPMModuleManifests(workspace, joinAPMPackages(manifest, lock))
}

func TestOrphanedSkillOnlySubdirPackageIsEvaluated(t *testing.T) {
	workspace, _, _ := setupOrphanSkillWorkspace(t)
	evidence := orphanSkillEvidence(t, workspace)
	if len(evidence.Unavailable) != 0 {
		t.Fatalf("unavailable = %#v", evidence.Unavailable)
	}
	if len(evidence.Children) != 1 || evidence.Children[0].Owner != "bundle-a" {
		t.Fatalf("children = %#v", evidence.Children)
	}
}

func TestOrphanedSkillOnlySubdirPackageStaysUnavailableWithoutRoot(t *testing.T) {
	workspace, skillModule, _ := setupOrphanSkillWorkspace(t)
	if err := os.RemoveAll(skillModule); err != nil {
		t.Fatal(err)
	}
	evidence := orphanSkillEvidence(t, workspace)
	if !slices.Contains(evidence.Unavailable, "shiplight") {
		t.Fatalf("unavailable = %#v", evidence.Unavailable)
	}
}

func setupOrphanSkillOwnedFixTest(t *testing.T) (target, skillModule string, a *App) {
	t.Helper()
	_, skillModule, a = setupOrphanSkillWorkspace(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	templatePath, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(home, "agents-template.yml")
	if err := os.WriteFile(target, []byte(orphanSkillTemplate), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, templatePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return target, skillModule, a
}

func TestFixAgentsOwnedChildrenAcceptsOrphanedSkillOnlySubdirPackage(t *testing.T) {
	target, _, a := setupOrphanSkillOwnedFixTest(t)
	report, err := a.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Unavailable) != 0 || len(report.Removed) != 1 || report.Removed[0].Name != "owned-mcp" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || strings.Contains(string(raw), "name: owned-mcp") {
		t.Fatalf("template = %q, err = %v", raw, err)
	}
}

func TestFixAgentsOwnedChildrenFailsClosedWhenOrphanedPackageRootMissing(t *testing.T) {
	target, skillModule, a := setupOrphanSkillOwnedFixTest(t)
	if err := os.RemoveAll(skillModule); err != nil {
		t.Fatal(err)
	}
	report, err := a.FixAgentsOwnedChildren(t.Context(), false)
	if err == nil || len(report.Removed) != 0 || !slices.Contains(report.Unavailable, "shiplight") {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(raw), "name: owned-mcp") {
		t.Fatalf("template = %q, err = %v", raw, err)
	}
}
