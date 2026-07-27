package brew_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/brew"
)

func newBrew(responses ...executor.MockCall) (*brew.Provider, *executor.MockExecutor) {
	m := &executor.MockExecutor{Responses: responses}
	return brew.New(m), m
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "brew", Package: name}
}

type concurrentBrewExecutor struct {
	active atomic.Int32
	max    atomic.Int32
	delay  time.Duration
}

func (e *concurrentBrewExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	cur := e.active.Add(1)
	for {
		maxSeen := e.max.Load()
		if cur <= maxSeen || e.max.CompareAndSwap(maxSeen, cur) {
			break
		}
	}
	time.Sleep(e.delay)
	e.active.Add(-1)

	if name != "brew" || len(args) == 0 {
		return "", "", nil
	}
	switch args[0] {
	case "--version", "update", "tap":
		return "", "", nil
	case "outdated":
		return `{"formulae":[],"casks":[]}`, "", nil
	case "info":
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--installed") {
			return `{"formulae":[],"casks":[]}`, "", nil
		}
		return `{"formulae":[{"name":"ripgrep","desc":"fast search"}],"casks":[]}`, "", nil
	default:
		return "", "", nil
	}
}

func TestProviderSerializesWriteCommands(t *testing.T) {
	exec := &concurrentBrewExecutor{delay: 10 * time.Millisecond}
	p := brew.New(exec)
	ctx := context.Background()
	errs := make(chan error, 3)
	var wg sync.WaitGroup
	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fn()
		}()
	}

	// All three are write ops that acquire exclusive Lock.
	run(func() error { return p.Install(ctx, tool("a")) })
	run(func() error { return p.Uninstall(ctx, tool("b")) })
	run(func() error { return p.Upgrade(ctx, tool("c")) })
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("provider call failed: %v", err)
		}
	}
	if got := exec.max.Load(); got != 1 {
		t.Fatalf("concurrent write commands = %d, want serialized", got)
	}
}

func TestProviderAllowsConcurrentReads(t *testing.T) {
	exec := &concurrentBrewExecutor{delay: 10 * time.Millisecond}
	p := brew.New(exec)
	ctx := context.Background()
	errs := make(chan error, 3)
	var wg sync.WaitGroup
	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fn()
		}()
	}

	// All three are read ops that acquire RLock — they should overlap.
	run(func() error {
		_, err := p.InstalledMap(ctx)
		return err
	})
	run(func() error {
		_, err := p.BulkDescribe(ctx, []provider.Tool{tool("ripgrep")})
		return err
	})
	run(func() error {
		_, err := p.Available(ctx)
		return err
	})
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("provider call failed: %v", err)
		}
	}
	if got := exec.max.Load(); got < 2 {
		t.Fatalf("concurrent read commands = %d, want >= 2 (reads should overlap)", got)
	}
}

func TestAvailable_True(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "Homebrew 4.0.0"})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_False(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("not found")})
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestIsInstalled_Found(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "ripgrep 14.1.1\n"})
	ok, ver, err := p.IsInstalled(context.Background(), tool("ripgrep"))
	if err != nil || !ok || ver != "14.1.1" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 14.1.1, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("IsInstalled() = (%v, _, %v), want (false, nil)", ok, err)
	}
}

func TestIsInstalled_EmptyOutput(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: ""})
	ok, _, err := p.IsInstalled(context.Background(), tool("pkg"))
	if err != nil || ok {
		t.Errorf("expected not installed for empty output, got (%v, _, %v)", ok, err)
	}
}

func TestInstall_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{Stdout: "==> Installed"})
	if err := p.Install(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(m.Calls) != 1 || m.Calls[0].Name != "brew" {
		t.Errorf("unexpected calls: %+v", m.Calls)
	}
	if m.Calls[0].Args[0] != "install" || m.Calls[0].Args[1] != "ripgrep" {
		t.Errorf("unexpected args: %v", m.Calls[0].Args)
	}
}

func TestInstall_UsesFormulaKindOption(t *testing.T) {
	p, m := newBrew(executor.MockCall{Stdout: "==> Installed"})
	tl := tool("flux")
	tl.Options = map[string]string{"brew_kind": "formula"}
	if err := p.Install(context.Background(), tl); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := strings.Join(m.Calls[0].Args, " "); got != "install --formula flux" {
		t.Fatalf("brew args = %q, want install --formula flux", got)
	}
}

func TestInstall_UsesCaskKindOption(t *testing.T) {
	p, m := newBrew(executor.MockCall{Stdout: "==> Installed"})
	tl := tool("visual-studio-code")
	tl.Options = map[string]string{"brew_kind": "cask"}
	if err := p.Install(context.Background(), tl); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := strings.Join(m.Calls[0].Args, " "); got != "install --cask visual-studio-code" {
		t.Fatalf("brew args = %q, want install --cask visual-studio-code", got)
	}
}

func TestInstall_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "formula not found"})
	err := p.Install(context.Background(), tool("nonexistent"))
	if err == nil {
		t.Fatal("expected error from failed install")
	}
}

func TestUninstall_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.Uninstall(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if m.Calls[0].Args[0] != "uninstall" {
		t.Errorf("expected uninstall subcommand, got %v", m.Calls[0].Args)
	}
}

func TestIsInstalled_TapPackage(t *testing.T) {
	// Tap-qualified package: IsInstalled should probe just the formula name.
	p, m := newBrew(executor.MockCall{Stdout: "terraform 1.7.5\n"})
	tapTool := provider.Tool{Name: "terraform", Provider: "brew", Package: "hashicorp/tap/terraform"}
	ok, ver, err := p.IsInstalled(context.Background(), tapTool)
	if err != nil || !ok || ver != "1.7.5" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 1.7.5, nil)", ok, ver, err)
	}
	if len(m.Calls) == 0 || m.Calls[0].Args[len(m.Calls[0].Args)-1] != "terraform" {
		t.Errorf("expected brew called with 'terraform', got %v", m.Calls)
	}
}

func TestInstall_AmbiguousRetriesFormula(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "Error: foo is both a formula and a cask."},
		executor.MockCall{Stdout: "==> Installed"}, // --formula retry succeeds
	)
	if err := p.Install(context.Background(), tool("foo")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(m.Calls) != 2 {
		t.Fatalf("want 2 calls (bare + formula retry), got %d: %+v", len(m.Calls), m.Calls)
	}
	if got := strings.Join(m.Calls[1].Args, " "); got != "install --formula foo" {
		t.Fatalf("retry args = %q, want install --formula foo", got)
	}
}

func TestInstall_AmbiguousFallsBackToCask(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "is both a formula and a cask"},
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "no available formula"},
		executor.MockCall{Stdout: "==> Installed"}, // --cask retry succeeds
	)
	if err := p.Install(context.Background(), tool("foo")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("want 3 calls, got %d: %+v", len(m.Calls), m.Calls)
	}
	if got := strings.Join(m.Calls[2].Args, " "); got != "install --cask foo" {
		t.Fatalf("final args = %q, want install --cask foo", got)
	}
}

func TestInstall_NoAmbiguousRetryWhenKindSet(t *testing.T) {
	p, m := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "is both a formula and a cask"})
	tl := tool("foo")
	tl.Options = map[string]string{"brew_kind": "formula"}
	if err := p.Install(context.Background(), tl); err == nil {
		t.Fatal("Install should return the error, not retry")
	}
	if len(m.Calls) != 1 {
		t.Fatalf("want 1 call (no retry when kind set), got %d", len(m.Calls))
	}
}

func TestInstall_SelfHealsUntrustedTap(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "Error: Refusing to load formula getsentry/xcodebuildmcp/xcodebuildmcp from untrusted tap getsentry/xcodebuildmcp."},
		executor.MockCall{Stdout: ""},              // brew trust succeeds
		executor.MockCall{Stdout: "==> Installed"}, // retry install succeeds
	)
	if err := p.Install(context.Background(), tool("xcodebuildmcp")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("want 3 calls (install + trust + retry), got %d: %+v", len(m.Calls), m.Calls)
	}
	if got := strings.Join(m.Calls[1].Args, " "); got != "trust getsentry/xcodebuildmcp" {
		t.Fatalf("call[1] = %q, want trust getsentry/xcodebuildmcp", got)
	}
	if got := strings.Join(m.Calls[2].Args, " "); got != "install xcodebuildmcp" {
		t.Fatalf("call[2] = %q, want install xcodebuildmcp", got)
	}
}

func TestInstall_AlreadyInstalledFromOtherTapIsNoOp(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "Error: flux was installed from the fluxcd/tap tap\nbut you are trying to install it from the homebrew/core tap.\nFormulae with the same name from different taps cannot be installed at the same time."},
	)
	if err := p.Install(context.Background(), tool("flux")); err != nil {
		t.Fatalf("Install should treat already-installed-from-tap as success, got %v", err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("want 1 call (no trust/retry), got %d: %+v", len(m.Calls), m.Calls)
	}
}

func TestUpgrade_SelfHealsUntrustedTap(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{Stdout: ""}, // list --versions (not a formula)
		executor.MockCall{Stdout: ""}, // list --versions --cask (not a cask)
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "Error: Refusing to load formula quarkdown-labs/quarkdown/quarkdown from untrusted tap quarkdown-labs/quarkdown."},
		executor.MockCall{Stdout: ""},             // brew trust succeeds
		executor.MockCall{Stdout: "==> Upgraded"}, // retry upgrade succeeds
	)
	if err := p.Upgrade(context.Background(), tool("quarkdown")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(m.Calls) != 5 {
		t.Fatalf("want 5 calls, got %d: %+v", len(m.Calls), m.Calls)
	}
	if got := strings.Join(m.Calls[3].Args, " "); got != "trust quarkdown-labs/quarkdown" {
		t.Fatalf("call[3] = %q, want trust quarkdown-labs/quarkdown", got)
	}
}

func TestListInstalled_CasksCarryBrewKind(t *testing.T) {
	p, _ := newBrew(
		executor.MockCall{Stdout: ""},                 // brew list (formulae) → none
		executor.MockCall{Stdout: "rectangle\n"},      // brew list --cask
		executor.MockCall{Stdout: "rectangle 0.84\n"}, // brew list --versions --cask
	)
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1 cask: %+v", len(tools), tools)
	}
	if tools[0].Options["brew_kind"] != "cask" {
		t.Fatalf("cask Options = %v, want brew_kind=cask", tools[0].Options)
	}
}

func TestListInstalled(t *testing.T) {
	p, _ := newBrew(
		executor.MockCall{Stdout: "git\nnode\nripgrep\n"},
		executor.MockCall{Stdout: "git 2.43.0\nnode 21.5.0\nripgrep 14.1.1\n"},
	)
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 3 {
		t.Errorf("got %d tools, want 3", len(tools))
	}
	if tools[0].Name != "git" || tools[0].Version != "2.43.0" {
		t.Errorf("unexpected first tool: %+v", tools[0])
	}
}

func TestListInstalled_TapPackage(t *testing.T) {
	p, _ := newBrew(
		executor.MockCall{Stdout: "git\nhashicorp/tap/terraform\n"},
		executor.MockCall{Stdout: "git 2.43.0\nhashicorp/tap/terraform 1.7.5\n"},
	)
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	tf := tools[1]
	if tf.Name != "terraform" {
		t.Errorf("Name = %q, want terraform", tf.Name)
	}
	if tf.Package != "hashicorp/tap/terraform" {
		t.Errorf("Package = %q, want hashicorp/tap/terraform", tf.Package)
	}
	if tf.Version != "1.7.5" {
		t.Errorf("Version = %q, want 1.7.5", tf.Version)
	}
}

func TestInstalledMetadataMap_CaskPkgutilRequiresPrivilege(t *testing.T) {
	installedInfo := `{"formulae":[{"name":"ripgrep","full_name":"ripgrep","installed":[{"version":"14.1.1","installed_on_request":true}]}],"casks":[` +
		`{"token":"parsec","installed":"150-103a","artifacts":[{"uninstall":[{"quit":"tv.parsec.www","pkgutil":"tv.parsec.www"}]},{"pkg":["parsec-macos.pkg"]}]},` +
		`{"token":"normal-app","installed":"1.0.0"}` +
		`]}`
	p, m := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{Stdout: "parsec\n"},
	)

	got, err := p.InstalledMetadataMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	if got["ripgrep"].Privilege.RequiresPrivilege() {
		t.Fatalf("formula privilege = %+v, want none", got["ripgrep"].Privilege)
	}
	parsec := got["parsec"]
	if parsec.Version != "150-103a" {
		t.Fatalf("parsec version = %q, want 150-103a", parsec.Version)
	}
	if parsec.Privilege.Requirement != provider.PrivilegeMaybe {
		t.Fatalf("parsec privilege = %+v, want maybe", parsec.Privilege)
	}
	if !strings.Contains(parsec.Privilege.Reason, "pkgutil") {
		t.Fatalf("parsec privilege reason = %q, want pkgutil", parsec.Privilege.Reason)
	}
	if _, ok := got["normal-app"]; ok {
		t.Fatalf("metadata included non-Homebrew cask artifact: %+v", got["normal-app"])
	}
	if len(m.Calls) != 3 ||
		strings.Join(m.Calls[0].Args, " ") != "info --json=v2 --installed" ||
		strings.Join(m.Calls[1].Args, " ") != "list --cask" ||
		strings.Join(m.Calls[2].Args, " ") != "list --versions --formula" {
		t.Fatalf("calls = %+v, want installed info, cask ownership filter, and formula list union", m.Calls)
	}
}

func TestInstalledMetadataMap_NullInfoReturnsError(t *testing.T) {
	p, _ := newBrew(
		executor.MockCall{Stdout: "null"},
		executor.MockCall{},
		executor.MockCall{},
	)
	if _, err := p.InstalledMetadataMap(context.Background()); err == nil {
		t.Fatal("expected top-level null brew info to be rejected")
	}
}

func TestPrivilegePlan_CaskPkgInstallerRequiresPrivilege(t *testing.T) {
	output := `{"formulae":[],"casks":[` +
		`{"token":"parsec","installed":"150-103a","artifacts":[{"uninstall":[{"pkgutil":"tv.parsec.www"}]},{"pkg":["parsec-macos.pkg"]}]}` +
		`]}`
	p, m := newBrew(executor.MockCall{Stdout: output})

	plan, err := p.PrivilegePlan(context.Background(), provider.PrivilegeActionUninstall, tool("parsec"))
	if err != nil {
		t.Fatalf("PrivilegePlan: %v", err)
	}
	if plan.Requirement != provider.PrivilegeMaybe {
		t.Fatalf("PrivilegePlan = %+v, want maybe", plan)
	}
	if len(m.Calls) != 1 || strings.Join(m.Calls[0].Args, " ") != "info --json=v2 --cask parsec" {
		t.Fatalf("brew args = %+v, want info --json=v2 --cask parsec", m.Calls)
	}
}

func TestPrivilegePlan_CaskInstallerRequiresPrivilegeOnInstall(t *testing.T) {
	output := `{"formulae":[],"casks":[` +
		`{"token":"littlesnitch","installed":"5.0","artifacts":[{"installer":[{"script":{"executable":"install.sh","sudo":true}}]}]}` +
		`]}`
	p, _ := newBrew(executor.MockCall{Stdout: output})

	plan, err := p.PrivilegePlan(context.Background(), provider.PrivilegeActionInstall, tool("littlesnitch"))
	if err != nil {
		t.Fatalf("PrivilegePlan: %v", err)
	}
	if plan.Requirement != provider.PrivilegeMaybe {
		t.Fatalf("PrivilegePlan = %+v, want maybe", plan)
	}
	if !strings.Contains(plan.Reason, "installer") {
		t.Fatalf("reason = %q, want installer mention", plan.Reason)
	}
}

func TestPrivilegePlan_CaskLaunchctlRequiresPrivilegeOnUninstall(t *testing.T) {
	output := `{"formulae":[],"casks":[` +
		`{"token":"stats","installed":"2.0","artifacts":[{"app":["Stats.app"]},{"uninstall":[{"launchctl":"eu.exelban.Stats.SMC.Helper","quit":"eu.exelban.Stats"}]}]}` +
		`]}`
	p, _ := newBrew(executor.MockCall{Stdout: output})

	plan, err := p.PrivilegePlan(context.Background(), provider.PrivilegeActionUninstall, tool("stats"))
	if err != nil {
		t.Fatalf("PrivilegePlan: %v", err)
	}
	if plan.Requirement != provider.PrivilegeMaybe {
		t.Fatalf("PrivilegePlan = %+v, want maybe", plan)
	}
	if !strings.Contains(plan.Reason, "launchctl") {
		t.Fatalf("reason = %q, want launchctl mention", plan.Reason)
	}
}

func TestInstalledMetadataMap_SelfUpdatingCask(t *testing.T) {
	installedInfo := `{"formulae":[],"casks":[` +
		`{"token":"battle-net","installed":"1.0","artifacts":[{"installer":[{"manual":"Battle.net-Setup.app"}]}]},` +
		`{"token":"stats","installed":"2.0","artifacts":[{"app":["Stats.app"]}]}` +
		`]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{Stdout: "battle-net\nstats\n"},
	)

	got, err := p.InstalledMetadataMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	if !got["battle-net"].SelfUpdates {
		t.Error("battle-net (manual installer) should be SelfUpdates=true")
	}
	if got["stats"].SelfUpdates {
		t.Error("stats (app cask) should be SelfUpdates=false")
	}
}

func TestTap_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.Tap(context.Background(), "hashicorp/tap"); err != nil {
		t.Fatalf("Tap: %v", err)
	}
	if len(m.Calls) != 1 || m.Calls[0].Args[0] != "tap" || m.Calls[0].Args[1] != "hashicorp/tap" {
		t.Errorf("unexpected calls: %+v", m.Calls)
	}
}

func TestTap_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "tap not found"})
	if err := p.Tap(context.Background(), "bad/tap"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTrust_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.Trust(context.Background(), "hashicorp/tap"); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if len(m.Calls) != 1 || m.Calls[0].Args[0] != "trust" || m.Calls[0].Args[1] != "hashicorp/tap" {
		t.Errorf("unexpected calls: %+v", m.Calls)
	}
}

func TestTrust_UnsupportedSubcommandIsNoOp(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "Error: Unknown command: trust"})
	if err := p.Trust(context.Background(), "hashicorp/tap"); err != nil {
		t.Fatalf("Trust on old brew should be a no-op, got: %v", err)
	}
}

func TestTrust_RealErrorPropagates(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "Error: Tap not installed"})
	if err := p.Trust(context.Background(), "bad/tap"); err == nil {
		t.Fatal("expected error for a real trust failure")
	}
}

func TestUntap_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.Untap(context.Background(), "hashicorp/tap"); err != nil {
		t.Fatalf("Untap: %v", err)
	}
	if m.Calls[0].Args[0] != "untap" {
		t.Errorf("expected untap subcommand, got %v", m.Calls[0].Args)
	}
}

func TestListTaps(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "hashicorp/tap\nhomebrew/cask-fonts\n"})
	taps, err := p.ListTaps(context.Background())
	if err != nil {
		t.Fatalf("ListTaps: %v", err)
	}
	if len(taps) != 2 || taps[0] != "hashicorp/tap" || taps[1] != "homebrew/cask-fonts" {
		t.Errorf("unexpected taps: %v", taps)
	}
}

func TestIsTapped_True(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "hashicorp/tap\nhomebrew/cask-fonts\n"})
	ok, err := p.IsTapped(context.Background(), "hashicorp/tap")
	if err != nil || !ok {
		t.Errorf("IsTapped() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestIsTapped_False(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "homebrew/cask-fonts\n"})
	ok, err := p.IsTapped(context.Background(), "hashicorp/tap")
	if err != nil || ok {
		t.Errorf("IsTapped() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestSearch_ReturnsFormulaeAndCasks(t *testing.T) {
	info := `{
		"formulae": [
			{"name": "bat", "desc": "cat clone with wings", "homepage": "https://github.com/sharkdp/bat"},
			{"name": "bat-extras", "desc": "extra scripts"}
		],
		"casks": [
			{"token": "batman", "desc": "dark knight app", "homepage": "https://github.com/example/batman", "artifacts": [{"pkg": ["Batman.pkg"]}]}
		]
	}`
	p, m := newBrew(
		executor.MockCall{Stdout: "==> Formulae\nbat\nbat-extras\n==> Casks\nbatman\n"},
		executor.MockCall{Stdout: info},
	)
	results, err := p.Search(context.Background(), "bat")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Name != "bat" {
		t.Errorf("results[0].Name = %q, want bat", results[0].Name)
	}
	if results[0].Provider != "brew" {
		t.Errorf("results[0].Provider = %q, want brew", results[0].Provider)
	}
	if results[0].Options["brew_kind"] != "formula" {
		t.Errorf("results[0].Options[brew_kind] = %q, want formula", results[0].Options["brew_kind"])
	}
	if results[2].Name != "batman" {
		t.Errorf("results[2].Name = %q, want batman cask", results[2].Name)
	}
	if results[2].Options["brew_kind"] != "cask" {
		t.Errorf("results[2].Options[brew_kind] = %q, want cask", results[2].Options["brew_kind"])
	}
	if results[0].Description != "cat clone with wings" {
		t.Errorf("results[0].Description = %q, want enriched formula description", results[0].Description)
	}
	if results[2].Description != "dark knight app" {
		t.Errorf("results[2].Description = %q, want enriched cask description", results[2].Description)
	}
	if results[0].Source.Type != provider.SourceTypeGitHub || results[0].Source.Owner != "sharkdp" || results[0].Source.Repo != "bat" {
		t.Fatalf("results[0].Source = %+v, want github sharkdp/bat", results[0].Source)
	}
	if results[2].Source.Owner != "example" || results[2].Source.Repo != "batman" {
		t.Fatalf("results[2].Source = %+v, want example/batman", results[2].Source)
	}
	if results[2].Privilege.Requirement != provider.PrivilegeMaybe {
		t.Fatalf("results[2].Privilege = %+v, want maybe", results[2].Privilege)
	}
	if !strings.Contains(results[2].Privilege.Reason, "pkg installer") {
		t.Fatalf("results[2].Privilege.Reason = %q, want pkg installer", results[2].Privilege.Reason)
	}
	if len(m.Calls) != 2 {
		t.Fatalf("brew calls = %+v, want search and info calls", m.Calls)
	}
	if strings.Join(m.Calls[0].Args, " ") != "search bat" {
		t.Fatalf("brew args[0] = %+v, want search bat", m.Calls[0].Args)
	}
	if strings.Join(m.Calls[1].Args, " ") != "info --json=v2 bat bat-extras batman" {
		t.Fatalf("brew args[1] = %+v, want info --json=v2 bat bat-extras batman", m.Calls[1].Args)
	}
}

func TestSearch_MultipleNamesPerLine(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "==> Formulae\nbat     bat-extras   bat-utils\n"})
	results, err := p.Search(context.Background(), "bat")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
}

func TestSearch_BrewError(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	_, err := p.Search(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSearch_NoResultsReturnsEmpty(t *testing.T) {
	p, _ := newBrew(executor.MockCall{
		Stderr: `Error: No formulae or casks found for "zzzzzz"`,
		Err:    errors.New("exit 1"),
	})
	results, err := p.Search(context.Background(), "zzzzzz")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %d, want none", len(results))
	}
}

func TestName(t *testing.T) {
	p, _ := newBrew()
	if got := p.Name(); got != "brew" {
		t.Errorf("Name() = %q, want brew", got)
	}
}

func TestDescription_NonEmpty(t *testing.T) {
	p, _ := newBrew()
	if p.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestUpgrade_Success(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{},
		executor.MockCall{},
		executor.MockCall{},
	)
	if err := p.Upgrade(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %+v, want formula probe, cask probe, upgrade fallback", m.Calls)
	}
	if got := strings.Join(m.Calls[2].Args, " "); got != "upgrade ripgrep" {
		t.Fatalf("upgrade args = %q, want upgrade ripgrep", got)
	}
}

func TestUpgrade_DisambiguatesInstalledFormula(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{Stdout: "flux 2.8.8\n"},
		executor.MockCall{},
	)
	if err := p.Upgrade(context.Background(), tool("flux")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(m.Calls) != 2 {
		t.Fatalf("calls = %+v, want formula probe then formula upgrade", m.Calls)
	}
	if got := strings.Join(m.Calls[0].Args, " "); got != "list --versions flux" {
		t.Fatalf("probe args = %q, want list --versions flux", got)
	}
	if got := strings.Join(m.Calls[1].Args, " "); got != "upgrade --formula flux" {
		t.Fatalf("upgrade args = %q, want upgrade --formula flux", got)
	}
}

func TestUpgrade_DisambiguatesInstalledCask(t *testing.T) {
	p, m := newBrew(
		executor.MockCall{},
		executor.MockCall{Stdout: "iterm2 3.4.23\n"},
		executor.MockCall{},
	)
	if err := p.Upgrade(context.Background(), tool("iterm2")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %+v, want formula probe, cask probe, cask upgrade", m.Calls)
	}
	if got := strings.Join(m.Calls[1].Args, " "); got != "list --versions --cask iterm2" {
		t.Fatalf("cask probe args = %q, want list --versions --cask iterm2", got)
	}
	if got := strings.Join(m.Calls[2].Args, " "); got != "upgrade --cask --greedy iterm2" {
		t.Fatalf("upgrade args = %q, want upgrade --cask --greedy iterm2", got)
	}
}

func TestUpgrade_Error(t *testing.T) {
	p, _ := newBrew(
		executor.MockCall{},
		executor.MockCall{},
		executor.MockCall{Err: errors.New("exit 1"), Stderr: "formula not found"},
	)
	if err := p.Upgrade(context.Background(), tool("bad")); err == nil {
		t.Fatal("expected error from failed upgrade")
	}
}

func TestInstalledMap_ReturnsFormulae(t *testing.T) {
	installedInfo := `{"formulae":[` +
		`{"name":"git","full_name":"git","installed":[{"version":"2.43.0","installed_on_request":true}]},` +
		`{"name":"dep","full_name":"dep","installed":[{"version":"1.0.0","installed_on_request":false}]}` +
		`],"casks":[]}`
	p, m := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{},
	)
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["git"] != "2.43.0" {
		t.Errorf("map[git] = %q, want 2.43.0", got["git"])
	}
	if _, ok := got["dep"]; ok {
		t.Errorf("transitive formula dep should not be in map: %v", got)
	}
	if len(m.Calls) != 3 ||
		strings.Join(m.Calls[0].Args, " ") != "info --json=v2 --installed" ||
		strings.Join(m.Calls[1].Args, " ") != "list --cask" ||
		strings.Join(m.Calls[2].Args, " ") != "list --versions --formula" {
		t.Fatalf("calls = %+v, want fast installed info scan, cask filter, and formula list union", m.Calls)
	}
}

func TestInstalledMetadataMap_IncludesFormulaSource(t *testing.T) {
	installedInfo := `{"formulae":[` +
		`{"name":"ripgrep","full_name":"ripgrep","homepage":"https://github.com/BurntSushi/ripgrep","installed":[{"version":"14.1.1","installed_on_request":true}]}` +
		`],"casks":[]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{},
	)
	got, err := p.InstalledMetadataMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	source := got["ripgrep"].Source
	if source.Type != provider.SourceTypeGitHub || source.Owner != "BurntSushi" || source.Repo != "ripgrep" {
		t.Fatalf("source = %+v, want github BurntSushi/ripgrep", source)
	}
}

func TestInstalledMetadataMap_UntrustedTapFormulaHiddenFromInfo(t *testing.T) {
	// tap-trust hides flux from `brew info` though it is installed; only `brew list` reports it. ripgrep is in both.
	installedInfo := `{"formulae":[` +
		`{"name":"ripgrep","full_name":"ripgrep","installed":[{"version":"14.1.1","installed_on_request":true}]}` +
		`],"casks":[]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{},
		executor.MockCall{Stdout: "ripgrep 14.1.1\nflux 2.4.0\n"},
	)
	got, err := p.InstalledMetadataMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	if _, ok := got["flux"]; !ok {
		t.Fatalf("flux not detected; tap-trust-hidden formula must be unioned from brew list: %+v", got)
	}
	if got["flux"].Version != "2.4.0" {
		t.Errorf("flux version = %q, want 2.4.0", got["flux"].Version)
	}
	if got["ripgrep"].Version != "14.1.1" {
		t.Errorf("ripgrep version = %q, want 14.1.1 (info metadata preserved)", got["ripgrep"].Version)
	}
	if got["flux"].ArtifactKind != "formula" {
		t.Errorf("flux ArtifactKind = %q, want formula (union entries are formulae)", got["flux"].ArtifactKind)
	}
}

func TestInstalledMetadataMap_ListFormulaErrorPropagates(t *testing.T) {
	installedInfo := `{"formulae":[` +
		`{"name":"ripgrep","full_name":"ripgrep","installed":[{"version":"14.1.1","installed_on_request":true}]}` +
		`],"casks":[]}`
	for _, runErr := range []error{errors.New("exit status 2"), context.Canceled} {
		p, _ := newBrew(
			executor.MockCall{Stdout: installedInfo},
			executor.MockCall{},
			executor.MockCall{Err: runErr},
		)
		if _, err := p.InstalledMetadataMap(context.Background()); !errors.Is(err, runErr) {
			t.Fatalf("InstalledMetadataMap error = %v, want %v", err, runErr)
		}
	}
}

func TestInstalledMetadataMap_FormulaCaskCollisionReturnsError(t *testing.T) {
	installedInfo := `{"formulae":[{"name":"shared","installed":[{"version":"1.0","installed_on_request":true}]}],` +
		`"casks":[{"token":"shared","installed":"2.0"}]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{Stdout: "shared\n"},
	)
	if _, err := p.InstalledMetadataMap(context.Background()); err == nil {
		t.Fatal("expected ambiguity error for installed formula and cask with the same token")
	}
}

func TestInstalledMetadataMap_RejectsMalformedFormulaName(t *testing.T) {
	// A garbage/control-char name from brew list output must not become a key.
	installedInfo := `{"formulae":[],"casks":[]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{},
		executor.MockCall{Stdout: "good-tool 1.0.0\nbad!!name 2.0.0\n"},
	)
	got, err := p.InstalledMetadataMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	if _, ok := got["good-tool"]; !ok {
		t.Errorf("valid formula name dropped: %+v", got)
	}
	if _, ok := got["bad!!name"]; ok {
		t.Errorf("malformed formula name must be rejected: %+v", got)
	}
}

func TestInstalledMetadataMap_ExcludesTransitiveDepKnownToInfo(t *testing.T) {
	// dep is a transitive dependency brew info reports, so the union must not add it back.
	installedInfo := `{"formulae":[` +
		`{"name":"git","full_name":"git","installed":[{"version":"2.43.0","installed_on_request":true}]},` +
		`{"name":"dep","full_name":"dep","installed":[{"version":"1.0.0","installed_on_request":false}]}` +
		`],"casks":[]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: installedInfo},
		executor.MockCall{},
		executor.MockCall{Stdout: "git 2.43.0\ndep 1.0.0\n"},
	)
	got, err := p.InstalledMetadataMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	if _, ok := got["dep"]; ok {
		t.Fatalf("transitive dep known to brew info must stay excluded: %+v", got)
	}
	if got["git"].Version != "2.43.0" {
		t.Errorf("git version = %q, want 2.43.0", got["git"].Version)
	}
}

func TestInstalledMap_IncludesCasks(t *testing.T) {
	caskInfo := `{"formulae":[],"casks":[{"token":"iterm2","installed":"3.4.23"},{"token":"normal-app","installed":"1.0.0"}]}`
	p, _ := newBrew(
		executor.MockCall{Stdout: caskInfo},
		executor.MockCall{Stdout: "iterm2\n"},
	)
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["iterm2"] != "3.4.23" {
		t.Errorf("map[iterm2] = %q, want 3.4.23", got["iterm2"])
	}
	if _, ok := got["normal-app"]; ok {
		t.Fatalf("InstalledMap included non-Homebrew cask artifact: %v", got)
	}
}

func TestInstalledMap_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Exactly 3 subprocesses: info --installed, list --cask, list --versions --formula. Guards against a duplicated list --cask.
func TestInstalledMetadataMap_ExactCallCount(t *testing.T) {
	installedInfo := `{"formulae":[{"name":"git","full_name":"git","installed":[{"version":"2.43.0","installed_on_request":true}]}],"casks":[]}`
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew info", Response: executor.MockCall{Stdout: installedInfo}},
		executor.MatchRule{Pattern: "brew list --cask", Response: executor.MockCall{Stdout: ""}},
		executor.MatchRule{Pattern: "brew list --versions --formula", Response: executor.MockCall{Stdout: "git 2.43.0\n"}},
	)
	p := brew.New(m)
	if _, err := p.InstalledMetadataMap(context.Background()); err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}
	if got := m.CallCount(); got != 3 {
		t.Errorf("brew subprocess calls = %d, want exactly 3 (info, list --cask, list --versions --formula)", got)
	}
	m.MustHaveCalledN(t, "brew info --json=v2 --installed", 1)
	m.MustHaveCalledN(t, "brew list --cask", 1)
	m.MustHaveCalledN(t, "brew list --versions --formula", 1)
}

func TestListInstalled_ReturnsFormulaeAndBrewCasks(t *testing.T) {
	p, _ := newBrew(
		executor.MockCall{Stdout: "homebrew/core/git\nasmvik/formulae/yabai\n"},
		executor.MockCall{Stdout: "homebrew/core/git 2.43.0\nasmvik/formulae/yabai 7.1.0\n"},
		executor.MockCall{Stdout: "iterm2\n"},
		executor.MockCall{Stdout: "iterm2 3.4.23\n"},
	)
	got, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	byName := make(map[string]provider.InstalledTool)
	for _, item := range got {
		byName[item.Name] = item
	}
	if byName["git"].Package != "homebrew/core/git" || byName["git"].Version != "2.43.0" {
		t.Fatalf("git = %+v, want full package and version", byName["git"])
	}
	if byName["yabai"].Package != "asmvik/formulae/yabai" || byName["yabai"].Version != "7.1.0" {
		t.Fatalf("yabai = %+v, want tap-qualified package and version", byName["yabai"])
	}
	if byName["iterm2"].Package != "iterm2" || byName["iterm2"].Version != "3.4.23" {
		t.Fatalf("iterm2 = %+v, want brew-installed cask and version", byName["iterm2"])
	}
}

func TestRefreshMetadata_RunsBrewUpdate(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.RefreshMetadata(context.Background()); err != nil {
		t.Fatalf("RefreshMetadata: %v", err)
	}
	if len(m.Calls) != 1 || strings.Join(m.Calls[0].Args, " ") != "update --quiet" {
		t.Fatalf("calls = %+v, want brew update --quiet", m.Calls)
	}
}

func TestRefreshMetadata_PropagatesError(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("network down")})
	if err := p.RefreshMetadata(context.Background()); err == nil {
		t.Fatal("RefreshMetadata: want error, got nil")
	}
}

func TestOutdatedMap_ReturnsFormulae(t *testing.T) {
	out := `{"formulae":[{"name":"git","current_version":"2.44.0"}],"casks":[]}`
	p, m := newBrew(executor.MockCall{Stdout: out})
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["git"] != "2.44.0" {
		t.Errorf("map[git] = %q, want 2.44.0", got["git"])
	}
	if len(m.Calls) != 1 || strings.Join(m.Calls[0].Args, " ") != "outdated --json=v2 --greedy" {
		t.Fatalf("calls = %+v, want brew outdated --greedy without explicit update", m.Calls)
	}
}

func TestOutdatedMap_TapQualifiedName(t *testing.T) {
	out := `{"formulae":[{"name":"lkshrk/tap/omni","current_version":"0.4.5"}],"casks":[]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["omni"] != "0.4.5" {
		t.Errorf("map[omni] = %q, want 0.4.5 (tap prefix should be stripped)", got["omni"])
	}
}

func TestOutdatedMap_TapQualifiedCask(t *testing.T) {
	out := `{"formulae":[],"casks":[{"name":"lkshrk/tap/my-app","current_version":"2.1.0"}]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["my-app"] != "2.1.0" {
		t.Errorf("map[my-app] = %q, want 2.1.0 (tap prefix should be stripped for casks)", got["my-app"])
	}
}

func TestOutdatedMap_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOutdatedMap_OutdatedError(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOutdatedMap_NullReturnsError(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "null"})
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected top-level null to be rejected")
	}
}

func TestDescribe_ReturnsDesc(t *testing.T) {
	out := `{"formulae":[{"name":"ripgrep","desc":"Search tool that recursively searches directories"}],"casks":[]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("ripgrep"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "Search tool that recursively searches directories" {
		t.Errorf("Describe() = %q", desc)
	}
}

func TestDescribe_Cask(t *testing.T) {
	out := `{"formulae":[],"casks":[{"token":"iterm2","desc":"Terminal emulator as alternative to Apple's Terminal app"}]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
	desc, err := p.Describe(context.Background(), tool("iterm2"))
	if err != nil {
		t.Fatalf("Describe cask: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty description for cask")
	}
}

func TestDescribe_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.Describe(context.Background(), tool("bad")); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDescribe_NullInfoReturnsError(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Stdout: "null"})
	if _, err := p.Describe(context.Background(), tool("ripgrep")); err == nil {
		t.Fatal("expected top-level null brew info to be rejected")
	}
}

func TestBulkDescribe_FormulaeAndCasks(t *testing.T) {
	out := `{"formulae":[{"name":"ripgrep","desc":"Search tool"},{"name":"wget","desc":"Internet file retriever"}],"casks":[{"token":"iterm2","desc":"Terminal emulator"}]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
	tools := []provider.Tool{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep"},
		{Name: "wget", Provider: "brew", Package: "wget"},
		{Name: "iterm2", Provider: "brew", Package: "iterm2"},
	}
	got, err := p.BulkDescribe(context.Background(), tools)
	if err != nil {
		t.Fatalf("BulkDescribe: %v", err)
	}
	if got["ripgrep"] != "Search tool" {
		t.Errorf("map[ripgrep] = %q, want 'Search tool'", got["ripgrep"])
	}
	if got["wget"] != "Internet file retriever" {
		t.Errorf("map[wget] = %q, want 'Internet file retriever'", got["wget"])
	}
	if got["iterm2"] != "Terminal emulator" {
		t.Errorf("map[iterm2] = %q, want 'Terminal emulator'", got["iterm2"])
	}
}

func TestBulkDescribe_Empty(t *testing.T) {
	p, _ := newBrew()
	got, err := p.BulkDescribe(context.Background(), nil)
	if err != nil || got != nil {
		t.Errorf("BulkDescribe(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestBulkDescribe_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	tools := []provider.Tool{{Name: "bad", Provider: "brew", Package: "bad"}}
	if _, err := p.BulkDescribe(context.Background(), tools); err == nil {
		t.Fatal("expected error, got nil")
	}
}
