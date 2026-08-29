package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestDoctorAgentsOwnedChildrenWarnsThenClearsExactDuplicate(t *testing.T) {
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
	_, a := setupOwnedChildFixTest(t, template, module)
	a.SetFallbackExecutor(&availExecutor{available: map[string]bool{"apm": true}})
	result := &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	check := result.Checks[len(result.Checks)-1]
	if check.ID != "agents-owned-children" || check.Status != DoctorStatusWarn || !strings.Contains(strings.Join(check.Details, " "), "provided identically") {
		t.Fatalf("before fix = %#v", check)
	}
	if _, err := a.FixAgentsOwnedChildren(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	result = &DoctorResult{}
	a.doctorAgents(context.Background(), result, &config.RootConfig{})
	check = result.Checks[len(result.Checks)-1]
	if check.Status != DoctorStatusOK {
		t.Fatalf("after fix = %#v", check)
	}
}

func TestDoctorAgentsOwnedChildrenFailsConflictAndWarnsUnreadableModule(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: context-mode
      registry: false
      transport: stdio
      command: local
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: context-mode
      registry: false
      transport: stdio
      command: package
`
	_, a := setupOwnedChildFixTest(t, template, module)
	dir := filepath.Join(os.Getenv("HOME"), ".apm")
	result := &DoctorResult{}
	a.doctorAgentsOwnedChildren(result, dir)
	if check := result.Checks[0]; check.Status != DoctorStatusFail || !strings.Contains(strings.Join(check.Details, " "), "command differ") {
		t.Fatalf("conflict = %#v", check)
	}
	if err := os.WriteFile(filepath.Join(dir, "apm_modules", "acme", "bundle", "apm.yml"), []byte("dependencies: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	result = &DoctorResult{}
	a.doctorAgentsOwnedChildren(result, dir)
	if check := result.Checks[0]; check.Status != DoctorStatusWarn || !strings.Contains(strings.Join(check.Details, " "), "bundle-a") {
		t.Fatalf("unavailable = %#v", check)
	}
}

func TestDoctorAgentsOwnedChildrenFailsSameOwnerConflictWithoutStandalone(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: shared
      command: one
    - name: shared
      command: two
`
	_, a := setupOwnedChildFixTest(t, template, module)
	result := &DoctorResult{}
	a.doctorAgentsOwnedChildren(result, filepath.Join(os.Getenv("HOME"), ".apm"))
	check := result.Checks[0]
	if check.Status != DoctorStatusFail || !strings.Contains(strings.Join(check.Details, " "), "conflicting definitions") {
		t.Fatalf("check = %#v", check)
	}
}

func TestDoctorAgentsOwnedChildrenRedactsInvalidYAML(t *testing.T) {
	secret := "TOP_SECRET_SCALAR"
	template := "dependencies:\n  mcp:\n    - name: owned\n      command: \"" + secret + "\n"
	path, a := setupOwnedChildFixTest(t, template, "name: bundle-a\n")
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".apm", "apm.yml"), []byte("dependencies: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := &DoctorResult{}
	a.doctorAgentsOwnedChildren(result, filepath.Join(os.Getenv("HOME"), ".apm"))
	check := result.Checks[0]
	all := check.Message + " " + strings.Join(check.Details, " ")
	if check.Status != DoctorStatusFail || strings.Contains(all, secret) || !strings.Contains(all, path) {
		t.Fatalf("check = %#v", check)
	}
}

func TestDoctorAgentsOwnedChildrenTemplateClearsStaleLiveServices(t *testing.T) {
	template := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
`
	module := `name: bundle-a
dependencies:
  mcp:
    - name: stale-mcp
      command: sh
`
	_, a := setupOwnedChildFixTest(t, template, module)
	live := `dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: stale-mcp
      command: sh
`
	workspace := filepath.Join(os.Getenv("HOME"), ".apm")
	if err := os.WriteFile(filepath.Join(workspace, "apm.yml"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	result := &DoctorResult{}
	a.doctorAgentsOwnedChildren(result, workspace)
	if check := result.Checks[0]; check.Status != DoctorStatusOK {
		t.Fatalf("stale live MCP survived canonical template replacement: %#v", check)
	}
}
