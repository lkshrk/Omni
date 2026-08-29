package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type agentsModuleReusedDirectoryInfo struct {
	os.FileInfo
	modTime time.Time
}

func (i agentsModuleReusedDirectoryInfo) ModTime() time.Time { return i.modTime }

type manifestlessSkillFixture struct {
	workspace string
	module    string
	rows      []AgentsPackageRow
}

func newManifestlessSkillFixture(t *testing.T, repo, virtualPath, packageType string, deployed []string) manifestlessSkillFixture {
	t.Helper()
	manifestPath := ""
	if virtualPath != "" {
		manifestPath = "\n    path: " + virtualPath
	}
	lockPath := ""
	name := filepath.Base(repo)
	if virtualPath != "" {
		lockPath = "\n  virtual_path: " + virtualPath
		name = filepath.Base(virtualPath)
	}
	deployedYAML := ""
	for _, path := range deployed {
		deployedYAML += "\n  - " + path
	}
	_ = setupAgentsWorkspace(t, "dependencies:\n  apm:\n  - git: "+repo+manifestPath+"\n", "dependencies:\n- repo_url: "+repo+lockPath+"\n  name: "+name+"\n  version: unknown\n  package_type: "+packageType+"\n  resolved_commit: deadbeef\n  deployed_files:"+deployedYAML+"\n")
	manifest, lock, err := readAPMWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	rows := joinAPMPackages(manifest, lock)
	if len(rows) != 1 {
		t.Fatalf("package rows = %#v", rows)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(home, ".apm", "apm_modules", filepath.FromSlash(rows[0].ModuleSource))
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	return manifestlessSkillFixture{workspace: filepath.Join(home, ".apm"), module: module, rows: rows}
}

func (f manifestlessSkillFixture) evidence() agentsOwnershipEvidence {
	return readAPMModuleManifests(f.workspace, f.rows)
}

func writeSkillManifest(t *testing.T, module, relative string) {
	t.Helper()
	writeFile(t, filepath.Join(module, filepath.FromSlash(relative)), "---\nname: fixture\n---\n")
}

func mkdirManifestless(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func requireManifestlessKnownEmpty(t *testing.T, f manifestlessSkillFixture) {
	t.Helper()
	evidence := f.evidence()
	if len(evidence.Unavailable) != 0 || len(evidence.Children) != 0 {
		t.Fatalf("manifestless evidence = %#v", evidence)
	}
}

func requireManifestlessUnavailable(t *testing.T, f manifestlessSkillFixture) {
	t.Helper()
	evidence := f.evidence()
	if !slices.Contains(evidence.Unavailable, f.rows[0].Name) || len(evidence.Children) != 0 {
		t.Fatalf("unsafe package accepted: rows=%#v evidence=%#v", f.rows, evidence)
	}
}

func TestManifestlessDeepWikiSkillBundleIsKnownServiceFree(t *testing.T) {
	f := newManifestlessSkillFixture(t, "sopaco/deepwiki-rs", "", "skill_bundle", []string{
		".agents/skills/smart-docs/SKILL.md",
		".claude/skills/smart-docs/SKILL.md",
	})
	writeSkillManifest(t, f.module, "skills/smart-docs/SKILL.md")
	requireManifestlessKnownEmpty(t, f)
	if f.rows[0].Version != "unknown" {
		t.Fatalf("version = %q", f.rows[0].Version)
	}
}

func TestManifestlessSkillBundleRequiresEveryInventoriedSkill(t *testing.T) {
	f := newManifestlessSkillFixture(t, "acme/skills", "", "skill_bundle", []string{
		".agents/skills/one/SKILL.md",
		".claude/skills/one/reference.md",
		".agents/skills/two/SKILL.md",
	})
	writeSkillManifest(t, f.module, "skills/one/SKILL.md")
	requireManifestlessUnavailable(t, f)
	writeSkillManifest(t, f.module, "skills/two/SKILL.md")
	requireManifestlessKnownEmpty(t, f)
}

func TestManifestlessShiplightSkillKeepsStandaloneMCPIndependent(t *testing.T) {
	manifest := `targets:
- claude
dependencies:
  apm:
  - git: ShiplightAI/agent-skills-v2
    path: shiplight
  mcp:
  - name: shiplight
    registry: false
    transport: stdio
    command: sh
`
	lock := `dependencies:
- repo_url: shiplightai/agent-skills-v2
  virtual_path: shiplight
  name: shiplight
  version: unknown
  package_type: claude_skill
  resolved_commit: deadbeef
  deployed_files:
  - .agents/skills/shiplight/SKILL.md
mcp_servers:
- shiplight
mcp_configs:
  shiplight:
    name: shiplight
    transport: stdio
    command: sh
`
	a := setupAgentsWorkspace(t, manifest, lock)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeSkillManifest(t, filepath.Join(home, ".apm", "apm_modules", "shiplightai", "agent-skills-v2", "shiplight"), "SKILL.md")
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	row := ownedPackageRow(t, status.Packages, "shiplight")
	if row.Status != AgentsPackageInstalled || len(row.Provides) != 0 || strings.Contains(strings.Join(row.Issues, "\n"), "ownership evidence unavailable") {
		t.Fatalf("Shiplight package = %#v", row)
	}
	if !hasAgentsService(status.MCP, "shiplight") {
		t.Fatalf("standalone Shiplight MCP missing: %#v", status.MCP)
	}
}

func TestManifestlessSkillProofRejectsInvalidLockEvidence(t *testing.T) {
	tests := []struct {
		name        string
		packageType string
		commit      bool
		deployed    []string
	}{
		{name: "missing package type", commit: true, deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "unknown package type", packageType: "unknown", commit: true, deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "hybrid package type", packageType: "hybrid", commit: true, deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "plugin package type", packageType: "marketplace_plugin", commit: true, deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "hook package type", packageType: "hook", commit: true, deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "malformed package type", packageType: "claude skill", commit: true, deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "missing commit", packageType: "claude_skill", deployed: []string{".agents/skills/demo/SKILL.md"}},
		{name: "empty inventory", packageType: "claude_skill", commit: true},
		{name: "absolute path", packageType: "claude_skill", commit: true, deployed: []string{"/tmp/SKILL.md"}},
		{name: "backslash path", packageType: "claude_skill", commit: true, deployed: []string{`.agents\skills\demo\SKILL.md`}},
		{name: "traversal path", packageType: "claude_skill", commit: true, deployed: []string{".agents/skills/demo/../../escape"}},
		{name: "dot segment", packageType: "claude_skill", commit: true, deployed: []string{".agents/skills/./demo/SKILL.md"}},
		{name: "leading dot slash", packageType: "claude_skill", commit: true, deployed: []string{"./.agents/skills/demo/SKILL.md"}},
		{name: "duplicate separator", packageType: "claude_skill", commit: true, deployed: []string{".agents/skills//demo/SKILL.md"}},
		{name: "trailing separator", packageType: "claude_skill", commit: true, deployed: []string{".agents/skills/demo/"}},
		{name: "outside skill roots", packageType: "claude_skill", commit: true, deployed: []string{".codex/skills/demo/SKILL.md"}},
		{name: "mixed primitives", packageType: "claude_skill", commit: true, deployed: []string{".agents/skills/demo/SKILL.md", ".claude/mcp.json"}},
		{name: "missing skill name", packageType: "claude_skill", commit: true, deployed: []string{".agents/skills/SKILL.md"}},
		{name: "ambiguous skill name case", packageType: "skill_bundle", commit: true, deployed: []string{".agents/skills/Demo/SKILL.md", ".claude/skills/demo/SKILL.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commit := ""
			if tt.commit {
				commit = "\n  resolved_commit: deadbeef"
			}
			deployed := ""
			for _, path := range tt.deployed {
				deployed += "\n  - " + path
			}
			_ = setupAgentsWorkspace(t, "dependencies:\n  apm:\n  - git: acme/demo\n", "dependencies:\n- repo_url: acme/demo\n  name: demo\n  version: unknown\n  package_type: "+tt.packageType+commit+"\n  deployed_files:"+deployed+"\n")
			manifest, lock, err := readAPMWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			rows := joinAPMPackages(manifest, lock)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			f := manifestlessSkillFixture{workspace: filepath.Join(home, ".apm"), module: filepath.Join(home, ".apm", "apm_modules", "acme", "demo"), rows: rows}
			writeSkillManifest(t, f.module, "SKILL.md")
			requireManifestlessUnavailable(t, f)
		})
	}
}

func TestManifestlessSkillProofRejectsUnsafeSkillManifest(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T, string)
	}{
		{name: "missing", make: func(*testing.T, string) {}},
		{name: "directory", make: func(t *testing.T, path string) { mkdirManifestless(t, path) }},
		{name: "symlink", make: func(t *testing.T, path string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
		{name: "case collision", make: func(t *testing.T, path string) {
			t.Helper()
			writeFile(t, path, "name: demo\n")
			writeFile(t, filepath.Join(filepath.Dir(path), "skill.md"), "name: collision\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
			tc.make(t, filepath.Join(f.module, "SKILL.md"))
			requireManifestlessUnavailable(t, f)
		})
	}
}

func TestManifestlessSkillProofRejectsAPMManifestUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T, string)
	}{
		{name: "malformed", make: func(t *testing.T, path string) { writeFile(t, path, "dependencies: [") }},
		{name: "directory", make: func(t *testing.T, path string) { mkdirManifestless(t, path) }},
		{name: "symlink", make: func(t *testing.T, path string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
			writeSkillManifest(t, f.module, "SKILL.md")
			tc.make(t, filepath.Join(f.module, "apm.yml"))
			requireManifestlessUnavailable(t, f)
		})
	}
}

func TestManifestlessSkillProofRejectsEveryPinnedServiceCarrier(t *testing.T) {
	carriers := []string{
		"plugin.json",
		".claude-plugin",
		".github/plugin/plugin.json",
		".claude-plugin/plugin.json",
		".cursor-plugin/plugin.json",
		"mcp.json",
		".mcp.json",
		".github/.mcp.json",
		"com.microsoft.apm/mcp.json",
		"com.microsoft.apm/lsp.json",
		"lsp.json",
		".lsp.json",
	}
	for _, carrier := range carriers {
		t.Run(carrier, func(t *testing.T) {
			f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
			writeSkillManifest(t, f.module, "SKILL.md")
			path := filepath.Join(f.module, filepath.FromSlash(carrier))
			if carrier == ".claude-plugin" {
				mkdirManifestless(t, path)
			} else {
				writeFile(t, path, "")
			}
			requireManifestlessUnavailable(t, f)
		})
	}
}

func TestManifestlessSkillProofRejectsCarrierCaseCollision(t *testing.T) {
	f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
	writeSkillManifest(t, f.module, "SKILL.md")
	writeFile(t, filepath.Join(f.module, "MCP.json"), "{}")
	requireManifestlessUnavailable(t, f)
}

func TestManifestlessSkillProofRejectsSkillNameCaseCollision(t *testing.T) {
	f := newManifestlessSkillFixture(t, "acme/demo", "", "skill_bundle", []string{
		".agents/skills/Demo/SKILL.md",
		".claude/skills/demo/SKILL.md",
	})
	writeSkillManifest(t, f.module, "skills/Demo/SKILL.md")
	writeSkillManifest(t, f.module, "skills/demo/SKILL.md")
	requireManifestlessUnavailable(t, f)
}

func TestModuleDirectoryIdentityRejectsReusedInodeMetadata(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reused := agentsModuleReusedDirectoryInfo{FileInfo: info, modTime: info.ModTime().Add(time.Nanosecond)}
	if !sameAgentsModuleDirectoryMetadata(info.Mode(), info.Size(), info.ModTime(), info) {
		t.Fatal("unchanged directory metadata was rejected")
	}
	if sameAgentsModuleDirectoryMetadata(info.Mode(), info.Size(), info.ModTime(), reused) {
		t.Fatal("directory replacement with reused inode identity was accepted")
	}
}

func TestManifestlessSkillProofRejectsUnsafeCarrierEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T, string)
	}{
		{name: "directory", make: func(t *testing.T, path string) { mkdirManifestless(t, path) }},
		{name: "symlink", make: func(t *testing.T, path string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
			writeSkillManifest(t, f.module, "SKILL.md")
			tc.make(t, filepath.Join(f.module, "mcp.json"))
			requireManifestlessUnavailable(t, f)
		})
	}
}

func TestManifestlessSkillProofRejectsDeterministicReadFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*testing.T, manifestlessSkillFixture)
	}{
		{name: "unreadable apm manifest", fail: func(t *testing.T, f manifestlessSkillFixture) {
			old := agentsModuleLstat
			t.Cleanup(func() { agentsModuleLstat = old })
			agentsModuleLstat = func(path string) (os.FileInfo, error) {
				if path == filepath.Join(f.module, "apm.yml") {
					return nil, &os.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
				}
				return old(path)
			}
		}},
		{name: "unreadable skill manifest", fail: func(t *testing.T, f manifestlessSkillFixture) {
			old := agentsModuleOpen
			t.Cleanup(func() { agentsModuleOpen = old })
			agentsModuleOpen = func(path string) (*os.File, error) {
				if path == filepath.Join(f.module, "SKILL.md") {
					return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
				}
				return old(path)
			}
		}},
		{name: "unreadable carrier parent", fail: func(t *testing.T, f manifestlessSkillFixture) {
			parent := filepath.Join(f.module, ".github")
			mkdirManifestless(t, parent)
			old := agentsModuleReadDir
			t.Cleanup(func() { agentsModuleReadDir = old })
			agentsModuleReadDir = func(path string) ([]os.DirEntry, error) {
				if path == parent {
					return nil, &os.PathError{Op: "readdir", Path: path, Err: fs.ErrPermission}
				}
				return old(path)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
			writeSkillManifest(t, f.module, "SKILL.md")
			tc.fail(t, f)
			requireManifestlessUnavailable(t, f)
		})
	}
}

func TestManifestlessSkillProofRejectsEscapingModuleAndAmbiguousLockJoin(t *testing.T) {
	t.Run("escaping module symlink", func(t *testing.T) {
		f := newManifestlessSkillFixture(t, "acme/demo", "", "claude_skill", []string{".agents/skills/demo/SKILL.md"})
		if err := os.RemoveAll(f.module); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		writeSkillManifest(t, outside, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(f.module), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, f.module); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		requireManifestlessUnavailable(t, f)
	})

	t.Run("ambiguous lock join", func(t *testing.T) {
		lockDep := "- repo_url: acme/demo\n  name: demo\n  version: unknown\n  package_type: claude_skill\n  resolved_commit: deadbeef\n  deployed_files:\n  - .agents/skills/demo/SKILL.md\n"
		_ = setupAgentsWorkspace(t, "dependencies:\n  apm:\n  - git: acme/demo\n", "dependencies:\n"+lockDep+lockDep)
		manifest, lock, err := readAPMWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		rows := joinAPMPackages(manifest, lock)
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		f := manifestlessSkillFixture{workspace: filepath.Join(home, ".apm"), module: filepath.Join(home, ".apm", "apm_modules", "acme", "demo"), rows: rows}
		writeSkillManifest(t, f.module, "SKILL.md")
		requireManifestlessUnavailable(t, f)
	})
}
