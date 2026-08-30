package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownedChildrenStatusFixture(t *testing.T, standalone, module, lock string) (*App, string) {
	t.Helper()
	manifest := "targets:\n- claude\ndependencies:\n  apm:\n  - git: acme/bundle-a\n" + standalone
	a := setupAgentsWorkspace(t, manifest, "dependencies:\n- repo_url: acme/bundle-a\n  name: bundle-a\n  version: 1.0.0\n"+lock)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".apm", "apm_modules", "acme", "bundle-a")
	writeFile(t, filepath.Join(root, "apm.yml"), "name: bundle-a\nversion: 1.0.0\n"+module)
	return a, root
}

func ownedPackageRow(t *testing.T, rows []AgentsPackageRow, name string) AgentsPackageRow {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("no package row named %q in %#v", name, rows)
	return AgentsPackageRow{}
}

func hasAgentsService(rows []AgentsServiceRow, name string) bool {
	for _, row := range rows {
		if row.Name == name {
			return true
		}
	}
	return false
}

func providedChild(t *testing.T, row AgentsPackageRow, kind, name string) AgentsProvidedChild {
	t.Helper()
	for _, child := range row.Provides {
		if child.Kind == kind && child.Name == name {
			return child
		}
	}
	t.Fatalf("package %q does not provide %s %q: %#v", row.Name, kind, name, row.Provides)
	return AgentsProvidedChild{}
}

func TestAgentsStatusHidesHealthyPackageOwnedChildren(t *testing.T) {
	a, _ := ownedChildrenStatusFixture(t, "", `dependencies:
  mcp:
  - name: owned-mcp
    registry: false
    transport: stdio
    command: sh
  lsp:
  - name: owned-lsp
    command: sh
`, `mcp_servers:
- owned-mcp
mcp_configs:
  owned-mcp:
    name: owned-mcp
    transport: stdio
    command: sh
lsp_servers:
- owned-lsp
lsp_configs:
  owned-lsp:
    name: owned-lsp
    command: sh
`)

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if hasAgentsService(status.MCP, "owned-mcp") || hasAgentsService(status.LSP, "owned-lsp") {
		t.Fatalf("owned children leaked into top-level rows: MCP=%#v LSP=%#v", status.MCP, status.LSP)
	}
	row := ownedPackageRow(t, status.Packages, "bundle-a")
	if got := providedChild(t, row, "mcp", "owned-mcp").Status; got != AgentsPackageInstalled {
		t.Fatalf("owned MCP status = %q", got)
	}
	if got := providedChild(t, row, "lsp", "owned-lsp").Status; got != AgentsPackageInstalled {
		t.Fatalf("owned LSP status = %q", got)
	}
}

func TestAgentsStatusRetainsIndependentChildren(t *testing.T) {
	a, _ := ownedChildrenStatusFixture(t, `  mcp:
  - name: independent-mcp
    registry: false
    transport: stdio
    command: sh
  lsp:
  - name: independent-lsp
    command: sh
`, `dependencies:
  mcp:
  - name: owned-mcp
    registry: false
    transport: stdio
    command: sh
`, `mcp_servers:
- owned-mcp
- independent-mcp
mcp_configs:
  owned-mcp:
    name: owned-mcp
    transport: stdio
    command: sh
  independent-mcp:
    name: independent-mcp
    transport: stdio
    command: sh
lsp_servers:
- independent-lsp
lsp_configs:
  independent-lsp:
    name: independent-lsp
    command: sh
`)

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !hasAgentsService(status.MCP, "independent-mcp") || !hasAgentsService(status.LSP, "independent-lsp") {
		t.Fatalf("independent children missing: MCP=%#v LSP=%#v", status.MCP, status.LSP)
	}
}

func TestAgentsStatusRetainsUnmanagedChildren(t *testing.T) {
	a, _ := ownedChildrenStatusFixture(t, "", `dependencies:
  mcp:
  - name: owned-mcp
    registry: false
    transport: stdio
    command: sh
`, `mcp_servers:
- owned-mcp
mcp_configs:
  owned-mcp:
    name: owned-mcp
    transport: stdio
    command: sh
`)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"hand-added":{"command":"sh"}}}`)

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !hasAgentsService(status.MCP, "hand-added") {
		t.Fatalf("unmanaged MCP missing: %#v", status.MCP)
	}
}

func TestAgentsStatusHidesExactDuplicateAndDegradesOwner(t *testing.T) {
	a, _ := ownedChildrenStatusFixture(t, `  mcp:
  - name: owned-mcp
    registry: false
    transport: stdio
    command: sh
`, `dependencies:
  mcp:
  - name: owned-mcp
    registry: false
    transport: stdio
    command: sh
`, `mcp_servers:
- owned-mcp
mcp_configs:
  owned-mcp:
    name: owned-mcp
    transport: stdio
    command: sh
`)

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if hasAgentsService(status.MCP, "owned-mcp") {
		t.Fatalf("exact duplicate remained top-level: %#v", status.MCP)
	}
	owner := ownedPackageRow(t, status.Packages, "bundle-a")
	if owner.Status != AgentsPackageDrifted || !strings.Contains(strings.Join(owner.Issues, "\n"), "duplicate") {
		t.Fatalf("owner did not expose exact duplicate: %+v", owner)
	}
}

func TestAgentsStatusRetainsConflictingContextModeAndDegradesOwner(t *testing.T) {
	a, root := ownedChildrenStatusFixture(t, `  mcp:
  - name: context-mode
    registry: false
    transport: stdio
    command: node
    args: [./start.mjs]
`, "", `mcp_servers:
- context-mode
mcp_configs:
  context-mode:
    name: context-mode
    transport: stdio
    command: node
    args: [./start.mjs]
`)
	writeFile(t, filepath.Join(root, "apm.yml"), `name: bundle-a
version: 1.0.0
dependencies:
  mcp:
  - name: context-mode
    registry: false
    transport: stdio
    command: node
    args: [`+filepath.ToSlash(filepath.Join(root, "start.mjs"))+`]
`)

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	conflict := serviceRow(t, status.MCP, "context-mode")
	if conflict.Status != AgentsPackageDrifted {
		t.Fatalf("conflicting standalone status = %q", conflict.Status)
	}
	owner := ownedPackageRow(t, status.Packages, "bundle-a")
	if owner.Status != AgentsPackageDrifted || owner.SyncActionable || !strings.Contains(strings.Join(owner.Issues, "\n"), "conflict") {
		t.Fatalf("owner did not expose conflict: %+v", owner)
	}
	if status.SyncActionable != 0 {
		t.Fatalf("ownership conflict reported %d actionable sync items", status.SyncActionable)
	}
}

func TestAgentsStatusRollsOwnedChildFailureIntoPackage(t *testing.T) {
	a, _ := ownedChildrenStatusFixture(t, "", `dependencies:
  mcp:
  - name: broken-mcp
    registry: false
    transport: stdio
    command: omni-test-absent-binary
`, `mcp_servers:
- broken-mcp
mcp_configs:
  broken-mcp:
    name: broken-mcp
    transport: stdio
    command: omni-test-absent-binary
`)

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	row := ownedPackageRow(t, status.Packages, "bundle-a")
	if row.Status != AgentsPackageUnavailable {
		t.Fatalf("package status = %q, want unavailable: %+v", row.Status, row)
	}
	if got := providedChild(t, row, "mcp", "broken-mcp").Status; got != AgentsPackageUnavailable {
		t.Fatalf("provided child status = %q", got)
	}
}

func TestAgentsStatusDegradesEveryAmbiguousOwner(t *testing.T) {
	manifest := `targets:
- claude
dependencies:
  apm:
  - git: acme/bundle-a
  - git: acme/bundle-b
`
	lock := `dependencies:
- repo_url: acme/bundle-a
  name: bundle-a
  version: 1.0.0
- repo_url: acme/bundle-b
  name: bundle-b
  version: 1.0.0
mcp_servers:
- shared-mcp
mcp_configs:
  shared-mcp:
    name: shared-mcp
    transport: stdio
    command: sh
`
	a := setupAgentsWorkspace(t, manifest, lock)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	module := `name: bundle
version: 1.0.0
dependencies:
  mcp:
  - name: shared-mcp
    registry: false
    transport: stdio
    command: sh
`
	for _, name := range []string{"bundle-a", "bundle-b"} {
		writeFile(t, filepath.Join(home, ".apm", "apm_modules", "acme", name, "apm.yml"), module)
	}

	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bundle-a", "bundle-b"} {
		row := ownedPackageRow(t, status.Packages, name)
		issues := strings.Join(row.Issues, "\n")
		if row.Status != AgentsPackageDrifted || !strings.Contains(issues, "multiple") || !strings.Contains(issues, "bundle-a, bundle-b") {
			t.Fatalf("ambiguous owner %q = %+v", name, row)
		}
	}
}

func TestAgentsStatusDegradesSameOwnerWithDifferingDefinitions(t *testing.T) {
	a, _ := ownedChildrenStatusFixture(t, `  mcp:
  - name: shared-mcp
    registry: false
    transport: stdio
    command: sh
`, `dependencies:
  mcp:
  - name: shared-mcp
    registry: false
    transport: stdio
    command: sh
  - name: shared-mcp
    registry: false
    transport: stdio
    command: other
`, `mcp_servers:
- shared-mcp
mcp_configs:
  shared-mcp:
    name: shared-mcp
    transport: stdio
    command: sh
`)
	status, err := a.AgentsStatus()
	if err != nil {
		t.Fatal(err)
	}
	row := ownedPackageRow(t, status.Packages, "bundle-a")
	if row.Status != AgentsPackageUnavailable || !strings.Contains(strings.Join(row.Issues, "\n"), "conflicting definitions") {
		t.Fatalf("same-owner conflict = %+v", row)
	}
	if conflict := serviceRow(t, status.MCP, "shared-mcp"); conflict.Status != AgentsPackageDrifted {
		t.Fatalf("standalone conflict = %+v", conflict)
	}
}

func TestAgentsRollPackageStatusNeverImprovesPackage(t *testing.T) {
	for _, status := range []AgentsPackageStatus{
		AgentsPackageDrifted,
		AgentsPackageUnavailable,
		AgentsPackageMissing,
		AgentsPackageOrphaned,
	} {
		t.Run(string(status), func(t *testing.T) {
			row := AgentsPackageRow{Status: status}
			agentsRollPackageStatus(&row, AgentsPackageInstalled)
			if row.Status != status {
				t.Fatalf("status improved from %q to %q", status, row.Status)
			}
		})
	}
}

func TestAgentsStatusRollsUnavailableOwnershipEvidenceIntoPackage(t *testing.T) {
	manifest := apmManifest{Dependencies: apmDependencies{MCP: []apmMCPDep{{Name: "standalone"}}}}
	packages := []AgentsPackageRow{{Name: "bundle", Status: AgentsPackageInstalled}}
	reconcileAgentsOwnedChildren(packages, manifest, agentsOwnershipEvidence{Unavailable: []string{"bundle"}}, agentsServiceInput{}, agentsServiceInput{})
	if packages[0].Status != AgentsPackageUnavailable || strings.Join(packages[0].Issues, "\n") != "package ownership evidence unavailable" {
		t.Fatalf("package = %+v", packages[0])
	}

	packages = []AgentsPackageRow{{Name: "bundle", Status: AgentsPackageMissing}}
	reconcileAgentsOwnedChildren(packages, manifest, agentsOwnershipEvidence{Unavailable: []string{"bundle"}}, agentsServiceInput{}, agentsServiceInput{})
	if packages[0].Status != AgentsPackageMissing || len(packages[0].Issues) != 1 {
		t.Fatalf("missing package was improved: %+v", packages[0])
	}

	packages = []AgentsPackageRow{{Name: "bundle", Status: AgentsPackageInstalled}}
	reconcileAgentsOwnedChildren(packages, apmManifest{}, agentsOwnershipEvidence{Unavailable: []string{"bundle"}}, agentsServiceInput{}, agentsServiceInput{})
	if packages[0].Status != AgentsPackageInstalled || len(packages[0].Issues) != 0 {
		t.Fatalf("package-only workspace degraded: %+v", packages[0])
	}
}

func TestReadAPMModuleManifestsReportsFirstInstallUnavailableEvidence(t *testing.T) {
	a := setupAgentsWorkspace(t, `dependencies:
  apm:
  - git: acme/not-installed
  mcp:
  - name: maybe-owned
    registry: false
    transport: stdio
    command: sh
`, "dependencies: []\n")
	_ = a
	manifest, lock, err := readAPMWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	rows := joinAPMPackages(manifest, lock)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	evidence := readAPMModuleManifests(filepath.Join(home, ".apm"), rows)
	if len(evidence.Unavailable) != 1 || !strings.Contains(evidence.Unavailable[0], "not-installed") {
		t.Fatalf("unavailable evidence = %#v", evidence.Unavailable)
	}
}
