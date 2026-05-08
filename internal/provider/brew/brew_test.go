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

func TestProviderSerializesBrewCommands(t *testing.T) {
	exec := &concurrentBrewExecutor{delay: 10 * time.Millisecond}
	p := brew.New(exec)
	ctx := context.Background()
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fn()
		}()
	}

	run(func() error {
		_, err := p.InstalledMap(ctx)
		return err
	})
	run(func() error {
		_, err := p.OutdatedMap(ctx)
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
	if got := exec.max.Load(); got != 1 {
		t.Fatalf("concurrent brew commands = %d, want serialized", got)
	}
}

// --- Available ---

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

// --- IsInstalled ---

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

// --- Install ---

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

func TestInstall_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "formula not found"})
	err := p.Install(context.Background(), tool("nonexistent"))
	if err == nil {
		t.Fatal("expected error from failed install")
	}
}

// --- Uninstall ---

func TestUninstall_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.Uninstall(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if m.Calls[0].Args[0] != "uninstall" {
		t.Errorf("expected uninstall subcommand, got %v", m.Calls[0].Args)
	}
}

// --- IsInstalled (tap packages) ---

func TestIsInstalled_TapPackage(t *testing.T) {
	// Tap-qualified package: IsInstalled should probe just the formula name.
	p, m := newBrew(executor.MockCall{Stdout: "terraform 1.7.5\n"})
	tapTool := provider.Tool{Name: "terraform", Provider: "brew", Package: "hashicorp/tap/terraform"}
	ok, ver, err := p.IsInstalled(context.Background(), tapTool)
	if err != nil || !ok || ver != "1.7.5" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 1.7.5, nil)", ok, ver, err)
	}
	// Verify brew was called with short name, not the tap-qualified path.
	if len(m.Calls) == 0 || m.Calls[0].Args[len(m.Calls[0].Args)-1] != "terraform" {
		t.Errorf("expected brew called with 'terraform', got %v", m.Calls)
	}
}

// --- ListInstalled ---

func TestListInstalled(t *testing.T) {
	// brew info --json=v2 --installed; only installed_on_request=true entries shown.
	output := `{"formulae":[` +
		`{"full_name":"git","installed":[{"version":"2.43.0","installed_on_request":true}]},` +
		`{"full_name":"node","installed":[{"version":"21.5.0","installed_on_request":true}]},` +
		`{"full_name":"ripgrep","installed":[{"version":"14.1.1","installed_on_request":true}]}` +
		`]}`
	p, _ := newBrew(executor.MockCall{Stdout: output})
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
	// Tap-qualified full_name — formula name extracted as the bare name.
	output := `{"formulae":[` +
		`{"full_name":"git","installed":[{"version":"2.43.0","installed_on_request":true}]},` +
		`{"full_name":"hashicorp/tap/terraform","installed":[{"version":"1.7.5","installed_on_request":true}]}` +
		`]}`
	p, _ := newBrew(executor.MockCall{Stdout: output})
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
	output := `{"formulae":[` +
		`{"full_name":"ripgrep","installed":[{"version":"14.1.1","installed_on_request":true}]}` +
		`],"casks":[` +
		`{"token":"parsec","installed":"150-103a","artifacts":[{"uninstall":[{"quit":"tv.parsec.www","pkgutil":"tv.parsec.www"}]},{"pkg":["parsec-macos.pkg"]}]}` +
		`]}`
	p, _ := newBrew(executor.MockCall{Stdout: output})

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

// --- Tap / Untap / ListTaps / IsTapped ---

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

// --- Search ---

func TestSearch_ReturnsFormulaeAndCasks(t *testing.T) {
	info := `{
		"formulae": [
			{"name": "bat", "desc": "cat clone with wings"},
			{"name": "bat-extras", "desc": "extra scripts"}
		],
		"casks": [
			{"token": "batman", "desc": "dark knight app", "artifacts": [{"pkg": ["Batman.pkg"]}]}
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
	if results[2].Name != "batman" {
		t.Errorf("results[2].Name = %q, want batman cask", results[2].Name)
	}
	if results[0].Description != "cat clone with wings" {
		t.Errorf("results[0].Description = %q, want enriched formula description", results[0].Description)
	}
	if results[2].Description != "dark knight app" {
		t.Errorf("results[2].Description = %q, want enriched cask description", results[2].Description)
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

// --- Name / Description ---

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

// --- Upgrade ---

func TestUpgrade_Success(t *testing.T) {
	p, m := newBrew(executor.MockCall{})
	if err := p.Upgrade(context.Background(), tool("ripgrep")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(m.Calls) != 1 || m.Calls[0].Args[0] != "upgrade" {
		t.Errorf("expected 'brew upgrade', got %+v", m.Calls)
	}
}

func TestUpgrade_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1"), Stderr: "formula not found"})
	if err := p.Upgrade(context.Background(), tool("bad")); err == nil {
		t.Fatal("expected error from failed upgrade")
	}
}

// --- InstalledMap ---

func TestInstalledMap_ReturnsFormulae(t *testing.T) {
	out := `{"formulae":[{"full_name":"git","installed":[{"version":"2.43.0","installed_on_request":true}]},{"full_name":"dep","installed":[{"version":"1.0.0","installed_on_request":false}]}],"casks":[]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
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
}

func TestInstalledMap_IncludesCasks(t *testing.T) {
	out := `{"formulae":[],"casks":[{"token":"iterm2","installed":"3.4.23"}]}`
	p, _ := newBrew(executor.MockCall{Stdout: out})
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["iterm2"] != "3.4.23" {
		t.Errorf("map[iterm2] = %q, want 3.4.23", got["iterm2"])
	}
}

func TestInstalledMap_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- OutdatedMap ---

func TestOutdatedMap_ReturnsFormulae(t *testing.T) {
	out := `{"formulae":[{"name":"git","current_version":"2.44.0"}],"casks":[]}`
	p, m := newBrew(executor.MockCall{}, executor.MockCall{Stdout: out})
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["git"] != "2.44.0" {
		t.Errorf("map[git] = %q, want 2.44.0", got["git"])
	}
	if len(m.Calls) != 2 || m.Calls[0].Args[0] != "update" || m.Calls[1].Args[0] != "outdated" {
		t.Fatalf("calls = %+v, want brew update before brew outdated", m.Calls)
	}
}

func TestOutdatedMap_Error(t *testing.T) {
	p, _ := newBrew(executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOutdatedMap_OutdatedError(t *testing.T) {
	p, _ := newBrew(executor.MockCall{}, executor.MockCall{Err: errors.New("exit 1")})
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Describe ---

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

// --- BulkDescribe ---

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
