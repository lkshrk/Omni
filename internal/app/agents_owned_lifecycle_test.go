package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

type ownedLifecycleFixture struct {
	workspace string
	template  string
	target    string
	app       *App
	mock      *executor.MockExecutor
}

func newOwnedLifecycleFixture(t *testing.T, template string, module func(string) string) ownedLifecycleFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	workspace := filepath.Join(home, ".apm")
	moduleRoot := filepath.Join(workspace, "apm_modules", "acme", "bundle")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
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
	if module != nil {
		if err := os.WriteFile(filepath.Join(moduleRoot, "apm.yml"), []byte(module(moduleRoot)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	templatePath, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "agents-template.yml")
	if err := os.WriteFile(target, []byte(template), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, templatePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(home, "settings.json"))
	a.StateDir = filepath.Join(home, "state")
	a.SetFallbackExecutor(mock)
	return ownedLifecycleFixture{workspace: workspace, template: templatePath, target: target, app: a, mock: mock}
}

func (f ownedLifecycleFixture) doctor(t *testing.T) DoctorCheck {
	t.Helper()
	result := &DoctorResult{}
	f.app.doctorAgentsOwnedChildren(result, f.workspace)
	if len(result.Checks) != 1 {
		t.Fatalf("doctor checks = %#v", result.Checks)
	}
	return result.Checks[0]
}

func (f ownedLifecycleFixture) readTemplate(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(f.template)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f ownedLifecycleFixture) assertTemplateIdentity(t *testing.T) {
	t.Helper()
	info, err := os.Lstat(f.template)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("template symlink = %v, err = %v", info, err)
	}
	link, err := os.Readlink(f.template)
	if err != nil || link != f.target {
		t.Fatalf("template target = %q, err = %v", link, err)
	}
	info, err = os.Stat(f.target)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("template mode = %v, err = %v", info, err)
	}
}

func (f ownedLifecycleFixture) syncToAPM(t *testing.T) {
	t.Helper()
	f.mock.Responses = []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n"},
	}
	result, err := f.app.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{ForceTemplate: true})
	if err != nil || result.Output != "installed\n" {
		t.Fatalf("sync result = %#v, err = %v", result, err)
	}
	if len(f.mock.Calls) != 2 || f.mock.Calls[0].Name != "apm" || strings.Join(f.mock.Calls[0].Args, " ") != "--version" ||
		f.mock.Calls[1].Name != "apm" || strings.Join(f.mock.Calls[1].Args, " ") != "install -g --trust-transitive-mcp" {
		t.Fatalf("APM calls = %#v", f.mock.Calls)
	}
}

func TestOwnedChildrenLifecycleRepairsExactDuplicateBeforeSync(t *testing.T) {
	template := `# lifecycle template comment stays
name: lifecycle
targets: [claude]
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	module := func(string) string {
		return `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      registry: false
      transport: stdio
      command: sh
`
	}
	f := newOwnedLifecycleFixture(t, template, module)
	before := f.readTemplate(t)

	if check := f.doctor(t); check.Status != DoctorStatusWarn || !strings.Contains(strings.Join(check.Details, " "), "provided identically") {
		t.Fatalf("doctor before fix = %#v", check)
	}
	report, err := f.app.FixAgentsOwnedChildren(t.Context(), true)
	if err != nil || len(report.Removed) != 1 || report.Removed[0].Name != "owned-mcp" {
		t.Fatalf("dry-run report = %#v, err = %v", report, err)
	}
	if got := f.readTemplate(t); got != before {
		t.Fatalf("dry run changed template:\n%s", got)
	}
	f.assertTemplateIdentity(t)

	report, err = f.app.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Removed) != 1 || !report.SyncRequired {
		t.Fatalf("fix report = %#v, err = %v", report, err)
	}
	got := f.readTemplate(t)
	if strings.Contains(got, "name: owned-mcp") || !strings.Contains(got, "# lifecycle template comment stays") {
		t.Fatalf("fixed template:\n%s", got)
	}
	f.assertTemplateIdentity(t)
	if check := f.doctor(t); check.Status != DoctorStatusOK {
		t.Fatalf("doctor after fix = %#v", check)
	}
	f.syncToAPM(t)
}

func TestOwnedChildrenLifecycleLeavesConflictUntouchedUntilManualReconciliation(t *testing.T) {
	template := `name: lifecycle
targets: [claude]
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    # user override must survive automatic repair
    - name: context-mode
      registry: false
      transport: stdio
      command: ./bin/context-mode
`
	module := func(root string) string {
		return `name: bundle-a
dependencies:
  mcp:
    - name: context-mode
      registry: false
      transport: stdio
      command: ` + filepath.ToSlash(filepath.Join(root, "bin", "context-mode")) + "\n"
	}
	f := newOwnedLifecycleFixture(t, template, module)
	beforeTemplate := f.readTemplate(t)
	beforeLive, err := os.ReadFile(filepath.Join(f.workspace, "apm.yml"))
	if err != nil {
		t.Fatal(err)
	}

	if check := f.doctor(t); check.Status != DoctorStatusFail || !strings.Contains(strings.Join(check.Details, " "), "command differ") {
		t.Fatalf("doctor conflict = %#v", check)
	}
	report, err := f.app.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Kept) != 1 || report.Kept[0].Name != "context-mode" {
		t.Fatalf("fix conflict report = %#v, err = %v", report, err)
	}
	if got := f.readTemplate(t); got != beforeTemplate {
		t.Fatalf("conflict fix changed template:\n%s", got)
	}
	if _, err = f.app.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{ForceTemplate: true}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("sync conflict err = %v", err)
	}
	if len(f.mock.Calls) != 0 {
		t.Fatalf("APM invoked for conflict: %#v", f.mock.Calls)
	}
	if live, readErr := os.ReadFile(filepath.Join(f.workspace, "apm.yml")); readErr != nil || string(live) != string(beforeLive) {
		t.Fatalf("live manifest changed on conflict: %q, %v", live, readErr)
	}

	manual := `name: lifecycle
targets: [claude]
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
`
	if err := os.WriteFile(f.target, []byte(manual), 0o640); err != nil {
		t.Fatal(err)
	}
	if check := f.doctor(t); check.Status != DoctorStatusOK {
		t.Fatalf("doctor after manual reconciliation = %#v", check)
	}
	f.mock.Reset()
	f.syncToAPM(t)
}

func TestOwnedChildrenLifecycleKeepsIndependentMCPAndLSPTopLevel(t *testing.T) {
	template := `name: lifecycle
targets: [claude]
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: owned-mcp
      command: owned
    - name: independent-mcp
      command: independent
  lsp:
    - name: owned-lsp
      command: owned-lsp
    - name: independent-lsp
      command: independent-lsp
`
	module := func(string) string {
		return `name: bundle-a
dependencies:
  mcp:
    - name: owned-mcp
      command: owned
  lsp:
    - name: owned-lsp
      command: owned-lsp
`
	}
	f := newOwnedLifecycleFixture(t, template, module)
	report, err := f.app.FixAgentsOwnedChildren(t.Context(), false)
	if err != nil || len(report.Removed) != 2 {
		t.Fatalf("fix report = %#v, err = %v", report, err)
	}
	got := f.readTemplate(t)
	for _, want := range []string{"name: independent-mcp", "name: independent-lsp"} {
		if !strings.Contains(got, want) {
			t.Fatalf("independent service %q removed:\n%s", want, got)
		}
	}
	for _, removed := range []string{"name: owned-mcp", "name: owned-lsp"} {
		if strings.Contains(got, removed) {
			t.Fatalf("owned service %q remains:\n%s", removed, got)
		}
	}
}

func TestOwnedChildrenLifecycleBlocksUnavailableEvidenceOnlyWithStandaloneServices(t *testing.T) {
	template := `name: lifecycle
targets: [claude]
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
  mcp:
    - name: standalone
      command: standalone
`
	f := newOwnedLifecycleFixture(t, template, nil)
	beforeTemplate := f.readTemplate(t)
	beforeLive, err := os.ReadFile(filepath.Join(f.workspace, "apm.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if check := f.doctor(t); check.Status != DoctorStatusWarn || !strings.Contains(strings.Join(check.Details, " "), "bundle-a") {
		t.Fatalf("doctor unavailable evidence = %#v", check)
	}
	if report, fixErr := f.app.FixAgentsOwnedChildren(t.Context(), false); fixErr == nil || len(report.Unavailable) != 1 {
		t.Fatalf("fix unavailable report = %#v, err = %v", report, fixErr)
	}
	if got := f.readTemplate(t); got != beforeTemplate {
		t.Fatalf("unavailable evidence changed template:\n%s", got)
	}
	if _, err = f.app.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{ForceTemplate: true}); err == nil || !strings.Contains(err.Error(), "cannot verify package-owned") {
		t.Fatalf("sync unavailable err = %v", err)
	}
	if len(f.mock.Calls) != 0 {
		t.Fatalf("APM invoked without ownership evidence: %#v", f.mock.Calls)
	}
	if live, readErr := os.ReadFile(filepath.Join(f.workspace, "apm.yml")); readErr != nil || string(live) != string(beforeLive) {
		t.Fatalf("live manifest changed without ownership evidence: %q, %v", live, readErr)
	}

	packageOnly := `name: lifecycle
targets: [claude]
dependencies:
  apm:
    - git: acme/bundle
      name: bundle-a
`
	if err := os.WriteFile(f.target, []byte(packageOnly), 0o640); err != nil {
		t.Fatal(err)
	}
	f.mock.Reset()
	f.syncToAPM(t)
}
