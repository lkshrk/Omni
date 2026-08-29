package app

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func setupAgentsWorkspace(t *testing.T, manifest, lock string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if manifest != "" {
		writeFile(t, filepath.Join(home, ".apm", "apm.yml"), manifest)
	}
	if lock != "" {
		writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), lock)
	}
	return New(filepath.Join(home, "settings.json"))
}

const agentsTestManifest = `name: omni
version: 1.0.0
dependencies:
  apm:
  - git: acme/zulu
    targets:
    - claude
  - git: acme/plugins
    path: plugins/alpha
  - git: acme/notinstalled
  - git: Acme/Mixed
`

const agentsTestLock = `lockfile_version: '1'
dependencies:
- repo_url: acme/zulu
  name: zulu
  version: 1.2.3
  target_subset:
  - claude
  deployed_files:
  - .claude/skills/zulu/SKILL.md
- repo_url: acme/plugins
  name: alpha
  version: 2.0.0
  virtual_path: plugins/alpha
  is_virtual: true
  package_type: marketplace_plugin
  target_subset:
  - codex
- repo_url: acme/mixed
  name: mixed
  version: 3.1.0
- repo_url: acme/ghost
  name: ghost
  version: 0.9.0
`

func TestAgentsPackagesJoinsManifestAndLock(t *testing.T) {
	a := setupAgentsWorkspace(t, agentsTestManifest, agentsTestLock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 5 {
		t.Fatalf("rows = %#v", rows)
	}

	want := []struct {
		name   string
		source string
		status AgentsPackageStatus
	}{
		{"alpha", "acme/plugins/plugins/alpha", AgentsPackageInstalled},
		{"mixed", "Acme/Mixed", AgentsPackageInstalled},
		{"zulu", "acme/zulu", AgentsPackageInstalled},
		{"notinstalled", "acme/notinstalled", AgentsPackageMissing},
		{"ghost", "acme/ghost", AgentsPackageOrphaned},
	}
	for i, w := range want {
		got := rows[i]
		if got.Name != w.name || got.Source != w.source || got.Status != w.status {
			t.Fatalf("row %d = %+v, want %v", i, got, w)
		}
	}

	if rows[0].Version != "2.0.0" || len(rows[0].Targets) != 1 || rows[0].Targets[0] != "codex" {
		t.Fatalf("marketplace plugin row = %+v", rows[0])
	}
	if rows[2].DeployedFiles != 1 || rows[2].Targets[0] != "claude" {
		t.Fatalf("skill row = %+v", rows[2])
	}
	if rows[3].Version != "" {
		t.Fatalf("missing row carries a version: %+v", rows[3])
	}
}

const agentsMarketplaceManifest = `dependencies:
  apm:
  - git: acme/zulu
  - name: caveman
    marketplace: omni-plugins
    targets:
    - claude
`

func TestAgentsPackagesMarketplaceDepWithoutLockIsMissing(t *testing.T) {
	a := setupAgentsWorkspace(t, agentsMarketplaceManifest, "dependencies: []\n")
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	row := rows[0]
	if row.Name != "caveman" || row.Source != "caveman@omni-plugins" || row.Status != AgentsPackageMissing {
		t.Fatalf("marketplace row = %+v", row)
	}
}

func TestAgentsPackagesMarketplaceDepJoinsLockByPluginName(t *testing.T) {
	lock := `dependencies:
- repo_url: JuliusBrussee/caveman
  name: caveman
  version: 1.4.0
  package_type: marketplace_plugin
  target_subset:
  - claude
  deployed_files:
  - .claude/skills/caveman/SKILL.md
  - .claude/skills/caveman/reference.md
- repo_url: acme/zulu
  name: zulu
  version: 1.2.3
`
	a := setupAgentsWorkspace(t, agentsMarketplaceManifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	for _, row := range rows {
		if row.Status != AgentsPackageInstalled {
			t.Fatalf("row %+v is not installed", row)
		}
	}
	plugin := rows[0]
	if plugin.Name != "caveman" || plugin.Source != "caveman@omni-plugins" {
		t.Fatalf("marketplace row = %+v", plugin)
	}
	if plugin.Version != "1.4.0" || plugin.DeployedFiles != 2 || plugin.Targets[0] != "claude" {
		t.Fatalf("marketplace row not joined to lock: %+v", plugin)
	}
	if rows[1].Name != "zulu" || rows[1].Version != "1.2.3" {
		t.Fatalf("git row = %+v", rows[1])
	}
}

func TestAgentsPackagesUndeclaredMarketplacePluginIsOrphaned(t *testing.T) {
	lock := `dependencies:
- repo_url: someone/stray
  name: stray
  version: 2.0.0
  package_type: marketplace_plugin
`
	a := setupAgentsWorkspace(t, agentsMarketplaceManifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 3 || rows[2].Name != "stray" || rows[2].Status != AgentsPackageOrphaned {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestAgentsPackagesNormalizesGitURLForms(t *testing.T) {
	manifest := "dependencies:\n  apm:\n  - git: https://github.com/acme/tool.git\n"
	lock := "dependencies:\n- repo_url: acme/tool\n  name: tool\n  version: 4.5.6\n"
	a := setupAgentsWorkspace(t, manifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 1 || rows[0].Status != AgentsPackageInstalled || rows[0].Version != "4.5.6" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Source != "https://github.com/acme/tool" {
		t.Fatalf("source = %q", rows[0].Source)
	}
}

func TestAgentsPackagesMarketplaceDepJoinsNonPluginLockEntry(t *testing.T) {
	lock := `dependencies:
- repo_url: JuliusBrussee/caveman
  name: caveman
  version: 1.4.0
  package_type: claude_skill
`
	a := setupAgentsWorkspace(t, agentsMarketplaceManifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Name != "caveman" || rows[0].Status != AgentsPackageInstalled || rows[0].Version != "1.4.0" {
		t.Fatalf("skill-type lock entry not joined: %+v", rows[0])
	}
}

func TestAgentsPackagesAmbiguousNameDoesNotJoin(t *testing.T) {
	lock := `dependencies:
- repo_url: one/caveman
  name: caveman
  version: 1.0.0
  package_type: claude_skill
- repo_url: two/caveman
  name: caveman
  version: 2.0.0
  package_type: marketplace_plugin
`
	a := setupAgentsWorkspace(t, agentsMarketplaceManifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	var missing, orphaned int
	for _, row := range rows {
		switch row.Status {
		case AgentsPackageMissing:
			missing++
		case AgentsPackageOrphaned:
			orphaned++
		}
	}
	if missing != 2 || orphaned != 2 {
		t.Fatalf("ambiguous name was guessed: %#v", rows)
	}
}

func TestAgentsPackagesUnrecognizedDeclarationStillListed(t *testing.T) {
	manifest := "dependencies:\n  apm:\n  - name: mystery\n    ref: v1\n"
	a := setupAgentsWorkspace(t, manifest, "dependencies: []\n")
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Name != "mystery" || rows[0].Status != AgentsPackageMissing || rows[0].Source != agentsUnrecognizedSource {
		t.Fatalf("unrecognized row = %+v", rows[0])
	}
}

func TestAgentsPackagesJoinsSCPGitForm(t *testing.T) {
	manifest := "dependencies:\n  apm:\n  - git: git@github.com:acme/tool.git\n"
	lock := "dependencies:\n- repo_url: acme/tool\n  name: tool\n  version: 7.0.0\n"
	a := setupAgentsWorkspace(t, manifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 1 || rows[0].Status != AgentsPackageInstalled || rows[0].Version != "7.0.0" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestAgentsPackagesSourceDisplayDropsTrailingSlash(t *testing.T) {
	manifest := "dependencies:\n  apm:\n  - git: acme/repo//\n"
	a := setupAgentsWorkspace(t, manifest, "dependencies: []\n")
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 1 || rows[0].Source != "acme/repo" || rows[0].Name != "repo" {
		t.Fatalf("rows = %#v", rows)
	}
}

// Mirrors the shape apm 0.29.0 writes for a filesystem-path dependency.
const agentsLocalLock = `dependencies:
- repo_url: _local/fixture-skill
  name: fixture-skill
  version: 1.0.0
  package_type: hybrid
  local_path: /src/fixture-skill
  deployed_files:
  - .agents/skills/fixture-skill/SKILL.md
`

func TestAgentsPackagesLocalPathDepJoinsLocalLockEntry(t *testing.T) {
	lockWithoutLocalPath := strings.ReplaceAll(agentsLocalLock, "  local_path: /src/fixture-skill\n", "")
	for name, tc := range map[string]struct{ manifest, lock string }{
		"scalar":            {"dependencies:\n  apm:\n  - /src/fixture-skill\n", agentsLocalLock},
		"mapping":           {"dependencies:\n  apm:\n  - git: /src/fixture-skill\n", agentsLocalLock},
		"without localpath": {"dependencies:\n  apm:\n  - /src/fixture-skill\n", lockWithoutLocalPath},
	} {
		t.Run(name, func(t *testing.T) {
			a := setupAgentsWorkspace(t, tc.manifest, tc.lock)
			status, err := a.AgentsStatus()
			if err != nil {
				t.Fatal(err)
			}
			rows := status.Packages
			if len(rows) != 1 {
				t.Fatalf("rows = %#v", rows)
			}
			if rows[0].Name != "fixture-skill" || rows[0].Status != AgentsPackageInstalled || rows[0].Version != "1.0.0" {
				t.Fatalf("local dep row = %+v", rows[0])
			}
		})
	}
}

func TestAgentsPackagesNeverNameJoinsAcrossRepos(t *testing.T) {
	for name, tc := range map[string]struct{ manifest, missing, orphan string }{
		"wrong repo": {
			manifest: "dependencies:\n  apm:\n  - git: acme/foo\n",
			missing:  "acme/foo",
			orphan:   "totally/other",
		},
		"repointed fork": {
			manifest: "dependencies:\n  apm:\n  - git: myfork/superpowers\n",
			missing:  "myfork/superpowers",
			orphan:   "obra/superpowers",
		},
		"path form": {
			manifest: "dependencies:\n  apm:\n  - git: acme/plugins\n    path: plugins/alpha\n",
			missing:  "acme/plugins/plugins/alpha",
			orphan:   "totally/other",
		},
	} {
		t.Run(name, func(t *testing.T) {
			lockName := path.Base(tc.missing)
			lock := "dependencies:\n- repo_url: " + tc.orphan + "\n  name: " + lockName + "\n  version: 9.9.9\n"
			a := setupAgentsWorkspace(t, tc.manifest, lock)
			status, err := a.AgentsStatus()
			if err != nil {
				t.Fatal(err)
			}
			rows := status.Packages
			if len(rows) != 2 {
				t.Fatalf("rows = %#v", rows)
			}
			var missing, orphan *AgentsPackageRow
			for i := range rows {
				switch rows[i].Status {
				case AgentsPackageMissing:
					missing = &rows[i]
				case AgentsPackageOrphaned:
					orphan = &rows[i]
				}
			}
			if missing == nil || missing.Source != tc.missing || missing.Version != "" {
				t.Fatalf("missing row = %+v (rows %#v)", missing, rows)
			}
			if orphan == nil || orphan.Source != tc.orphan {
				t.Fatalf("orphan row = %+v (rows %#v)", orphan, rows)
			}
		})
	}
}

func TestAgentsPackagesWithoutWorkspaceIsEmpty(t *testing.T) {
	a := setupAgentsWorkspace(t, "", "")
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 0 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestAgentsPackagesWithoutLockMarksEverythingMissing(t *testing.T) {
	a := setupAgentsWorkspace(t, agentsTestManifest, "")
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	rows := status.Packages
	if len(rows) != 4 {
		t.Fatalf("rows = %#v", rows)
	}
	for _, row := range rows {
		if row.Status != AgentsPackageMissing {
			t.Fatalf("row %+v is not missing", row)
		}
	}
}

func TestAgentsPackagesRejectsInvalidYAML(t *testing.T) {
	a := setupAgentsWorkspace(t, "dependencies: [\n", "")
	if _, err := a.AgentsStatus(); err == nil {
		t.Fatal("invalid manifest accepted")
	}
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), agentsTestManifest)
	writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "dependencies: [\n")
	if _, err := a.AgentsStatus(); err == nil {
		t.Fatal("invalid lockfile accepted")
	}
}

func TestAPMPackageSpecSplitsSourceAndRef(t *testing.T) {
	for spec, want := range map[string][2]string{
		"owner/repo":                         {"owner/repo", ""},
		"owner/repo/":                        {"owner/repo", ""},
		"owner/repo@v1.2.3":                  {"owner/repo", "v1.2.3"},
		"owner/repo.git":                     {"owner/repo", ""},
		"owner/repo.git@abc123":              {"owner/repo", "abc123"},
		"https://github.com/owner/repo":      {"owner/repo", ""},
		"https://github.com/owner/repo@main": {"owner/repo", "main"},
		"http://github.com/owner/repo":       {"owner/repo", ""},
		"git@github.com:owner/repo.git":      {"owner/repo", ""},
		"git@github.com:owner/repo@v2":       {"owner/repo", "v2"},
		"github.com/owner/repo":              {"owner/repo", ""},
		"https://example.invalid/owner/repo": {"https://example.invalid/owner/repo", ""},
	} {
		repo, ref := APMPackageSpec(spec)
		if repo != want[0] || ref != want[1] {
			t.Fatalf("APMPackageSpec(%q) = %q, %q; want %q, %q", spec, repo, ref, want[0], want[1])
		}
	}
}

func TestSplitAPMGitRefPinsCurrentBehavior(t *testing.T) {
	for spec, want := range map[string][2]string{
		"https://github.com/microsoft/apm-cli":        {"https://github.com/microsoft/apm-cli", ""},
		"https://github.com/microsoft/apm-cli@abc123": {"https://github.com/microsoft/apm-cli", "abc123"},
		"ssh://git@github.com/owner/repo@v1":          {"ssh://git@github.com/owner/repo", "v1"},
		"owner/repo@a@b":                              {"owner/repo", "a@b"},
	} {
		base, ref := splitAPMGitRef(spec)
		if base != want[0] || ref != want[1] {
			t.Fatalf("splitAPMGitRef(%q) = %q, %q; want %q, %q", spec, base, ref, want[0], want[1])
		}
	}
}

const agentsDetailLock = `dependencies:
- repo_url: juliusbrussee/caveman
  name: caveman
  resolved_commit: 17f9f2ec2377b0bfe16b52ee03a462e7f0a02bc8
  version: 17f9f2e
  package_type: marketplace_plugin
  discovered_via: caveman
  marketplace_plugin_name: caveman
  declared_license: MIT
  target_subset:
  - claude
  deployed_files:
  - .claude/skills/caveman/SKILL.md
`

func TestAgentsPackagesExposeLockDetailFields(t *testing.T) {
	manifest := "targets:\n- claude\ndependencies:\n  apm:\n  - git: JuliusBrussee/caveman\n    ref: v1.2.3\n"
	a := setupAgentsWorkspace(t, manifest, agentsDetailLock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Packages) != 1 {
		t.Fatalf("rows = %#v", status.Packages)
	}
	row := status.Packages[0]
	if row.Ref != "v1.2.3" || row.Commit != "17f9f2ec2377b0bfe16b52ee03a462e7f0a02bc8" {
		t.Fatalf("ref/commit = %+v", row)
	}
	if row.License != "MIT" || row.Marketplace != "caveman" || row.DeployedFiles != 1 {
		t.Fatalf("provenance = %+v", row)
	}
	if row.Local() {
		t.Fatalf("repo row reported local: %+v", row)
	}
}

func TestAgentsPackageRowLocalDetection(t *testing.T) {
	for source, want := range map[string]bool{
		"_local/fixture-skill": true,
		"/src/fixture-skill":   true,
		"./relative/pkg":       true,
		"~/pkg":                true,
		"acme/repo":            false,
	} {
		if got := (AgentsPackageRow{Source: source}).Local(); got != want {
			t.Fatalf("Local(%q) = %v, want %v", source, got, want)
		}
	}
	if !(AgentsPackageRow{Source: "acme/repo", LocalPath: "/src/pkg"}).Local() {
		t.Fatal("local_path row not reported local")
	}
}

func TestAgentsServiceRowsExposeDetailWithoutSecrets(t *testing.T) {
	manifest := `targets:
- claude
dependencies:
  mcp:
  - name: litellm-tools
    registry: false
    transport: http
    url: https://user:hunter2@api.invalid/mcp/?token=abc
    headers:
      x-api-key: super-secret
  - name: local-tool
    registry: false
    transport: stdio
    command: /usr/local/bin/local-tool
`
	lock := "dependencies: []\nmcp_servers:\n- litellm-tools\n- local-tool\n"
	a := setupAgentsWorkspace(t, manifest, lock)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	remote := serviceRow(t, status.MCP, "litellm-tools")
	if remote.URLHost != "api.invalid" {
		t.Fatalf("url host = %q", remote.URLHost)
	}
	local := serviceRow(t, status.MCP, "local-tool")
	if local.Command != "local-tool" {
		t.Fatalf("command base = %q", local.Command)
	}
	for _, row := range status.MCP {
		blob := row.Name + row.Detail + row.Command + row.URLHost + strings.Join(row.Targets, " ") + strings.Join(row.Harnesses, " ")
		for _, secret := range []string{"hunter2", "super-secret", "token=abc", "/usr/local/bin"} {
			if strings.Contains(blob, secret) {
				t.Fatalf("row %q leaked %q", row.Name, secret)
			}
		}
	}
}

func TestAgentsPackagesReadDescriptionsFromAPMModules(t *testing.T) {
	manifest := "targets:\n- claude\ndependencies:\n  apm:\n  - git: JuliusBrussee/caveman\n  - git: acme/never-installed\n"
	a := setupAgentsWorkspace(t, manifest, agentsDetailLock)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// The lock lowercases repo_url while the deployed directory keeps the declared casing.
	writeFile(t, filepath.Join(home, ".apm", "apm_modules", "JuliusBrussee", "caveman", "apm.yml"),
		"name: caveman\nversion: 1.0.0\ndescription: Talk like caveman.\nauthor: Julius\n")

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	var installed, missing AgentsPackageRow
	for _, row := range status.Packages {
		switch row.Status {
		case AgentsPackageInstalled:
			installed = row
		case AgentsPackageMissing:
			missing = row
		}
	}
	if installed.Description != "Talk like caveman." || installed.Author != "Julius" {
		t.Fatalf("installed row = %+v", installed)
	}
	// A declared-but-uninstalled dep has no local source, and nothing may be invented for it.
	if missing.Description != "" || missing.Author != "" {
		t.Fatalf("uninstalled row carries a description: %+v", missing)
	}
}
