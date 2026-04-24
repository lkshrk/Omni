package node_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/node"
)

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "node", Package: name}
}

// listOutput mimics `pnpm ls -g --depth=0` / `npm list -g --depth=0`.
func listOutput(pkgs ...string) string {
	out := "/home/user/.local/share/pnpm/global/5:\n"
	for i, p := range pkgs {
		if i < len(pkgs)-1 {
			out += "├── " + p + "\n"
		} else {
			out += "└── " + p + "\n"
		}
	}
	return out
}

// --- Available / auto-detect ---

func TestAvailable_BunPreferred(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Stdout: "1.1.0"}},
	)
	p := node.New(m, "")
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
	m.AssertCalled(t, "bun --version")
}

func TestAvailable_FallsBackToPnpm(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
	)
	p := node.New(m, "")
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
	m.AssertCalled(t, "pnpm --version")
}

func TestAvailable_FallsBackToNpm(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	m.AddRule(executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "10.2.4"}})
	p := node.New(m, "")
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
	m.AssertCalled(t, "npm --version")
}

func TestAvailable_NoneFound(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	p := node.New(m, "")
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAvailable_HintPinnedToNpm(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "10.2.4"}},
	)
	p := node.New(m, "npm")
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
	// pnpm should not have been called.
	if len(m.CallsMatching("pnpm")) > 0 {
		t.Error("should not have probed pnpm when hint=npm")
	}
}

func TestAvailable_HintMissing(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := node.New(m, "pnpm")
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

// --- Install ---

func TestInstall_UsesPnpm(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm add -g", Response: executor.MockCall{}},
	)
	p := node.New(m, "pnpm")
	if err := p.Install(context.Background(), tool("typescript")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m.AssertCalled(t, "pnpm add -g typescript")
}

func TestInstall_UsesBun(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Stdout: "1.1.0"}},
		executor.MatchRule{Pattern: "bun add -g", Response: executor.MockCall{}},
	)
	p := node.New(m, "bun")
	if err := p.Install(context.Background(), tool("typescript")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m.AssertCalled(t, "bun add -g typescript")
}

func TestInstall_FallsBackToNpmCommand(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	m.AddRule(executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "10.2.4"}})
	m.AddRule(executor.MatchRule{Pattern: "npm install -g", Response: executor.MockCall{}})
	p := node.New(m, "")
	if err := p.Install(context.Background(), tool("typescript")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m.AssertCalled(t, "npm install -g typescript")
}

func TestInstall_HintedNpmUsesNpmArgs(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "10.2.4"}},
		executor.MatchRule{Pattern: "npm install -g", Response: executor.MockCall{}},
	)
	p := node.New(m, "npm")
	if err := p.Install(context.Background(), tool("prettier")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m.AssertCalled(t, "npm install -g prettier")
}

// --- Uninstall ---

func TestUninstall_PnpmArgs(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm remove -g", Response: executor.MockCall{}},
	)
	p := node.New(m, "pnpm")
	if err := p.Uninstall(context.Background(), tool("typescript")); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	m.AssertCalled(t, "pnpm remove -g typescript")
}

func TestUninstallFrom_UsesInstalledManager(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "npm uninstall -g", Response: executor.MockCall{}},
	)
	p := node.New(m, "pnpm")
	if err := p.UninstallFrom(context.Background(), tool("typescript"), "npm"); err != nil {
		t.Fatalf("UninstallFrom: %v", err)
	}
	m.AssertCalled(t, "npm uninstall -g typescript")
	if len(m.CallsMatching("pnpm remove")) > 0 {
		t.Fatal("should not uninstall through active pnpm manager when installed manager is npm")
	}
}

// --- IsInstalled ---

func TestIsInstalled_Found(t *testing.T) {
	out := listOutput("typescript@5.3.3")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0 typescript", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "pnpm")
	ok, ver, err := p.IsInstalled(context.Background(), tool("typescript"))
	if err != nil || !ok || ver != "5.3.3" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 5.3.3, nil)", ok, ver, err)
	}
}

func TestIsInstalled_NotFound(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0 nonexistent", Response: executor.MockCall{Err: errors.New("exit 1")}},
	)
	p := node.New(m, "pnpm")
	ok, _, err := p.IsInstalled(context.Background(), tool("nonexistent"))
	if err != nil || ok {
		t.Errorf("expected (false, nil), got (%v, _, %v)", ok, err)
	}
}

func TestIsInstalled_BunScansFullList(t *testing.T) {
	// bun pm ls -g does not accept a package filter — IsInstalled scans full output.
	out := listOutput("typescript@5.3.3", "prettier@3.1.1")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Stdout: "1.1.0"}},
		executor.MatchRule{Pattern: "bun pm ls -g", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "bun")
	ok, ver, err := p.IsInstalled(context.Background(), tool("typescript"))
	if err != nil || !ok || ver != "5.3.3" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 5.3.3, nil)", ok, ver, err)
	}
}

// --- ListInstalled ---

func TestListInstalled_Multiple(t *testing.T) {
	out := listOutput("typescript@5.3.3", "prettier@3.1.1", "ts-node@10.9.2")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "pnpm")
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 3 {
		t.Errorf("got %d tools, want 3", len(tools))
	}
	byName := make(map[string]string, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool.Version
	}
	if byName["typescript"] != "5.3.3" {
		t.Errorf("typescript version = %q, want 5.3.3", byName["typescript"])
	}
	if byName["prettier"] != "3.1.1" {
		t.Errorf("prettier version = %q, want 3.1.1", byName["prettier"])
	}
}

func TestListInstalled_ScopedPackage(t *testing.T) {
	out := listOutput("@scope/pkg@1.2.3")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "pnpm")
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if tools[0].Name != "@scope/pkg" || tools[0].Version != "1.2.3" {
		t.Errorf("scoped package parsed as {%s %s}", tools[0].Name, tools[0].Version)
	}
}

func TestListInstalled_IgnoresNestedDependencies(t *testing.T) {
	out := "/home/user/.npm-global/lib\n" +
		"├── parent-tool@1.0.0\n" +
		"│   └── neovim@5.3.0\n" +
		"└── typescript@5.3.3\n"
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "10.2.4"}},
		executor.MatchRule{Pattern: "npm list -g --depth=0", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "npm")
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	byName := make(map[string]bool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = true
	}
	if byName["neovim"] {
		t.Fatalf("nested dependency neovim was reported as a top-level tool: %+v", tools)
	}
	if !byName["parent-tool"] || !byName["typescript"] {
		t.Fatalf("top-level tools missing from parsed result: %+v", tools)
	}
}

func TestIsInstalled_IgnoresNestedDependencyMatch(t *testing.T) {
	out := "/home/user/.npm-global/lib\n" +
		"└── parent-tool@1.0.0\n" +
		"    └── neovim@5.3.0\n"
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "10.2.4"}},
		executor.MatchRule{Pattern: "npm list -g --depth=0 neovim", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "npm")
	ok, ver, err := p.IsInstalled(context.Background(), tool("neovim"))
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if ok || ver != "" {
		t.Fatalf("IsInstalled = (%v, %q), want nested dependency ignored", ok, ver)
	}
}

func TestListInstalled_ProviderIsNode(t *testing.T) {
	out := listOutput("typescript@5.3.3")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "pnpm")
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if tools[0].Provider != "node" {
		t.Errorf("Provider = %q, want node", tools[0].Provider)
	}
}

// --- Name / Description ---

func TestName(t *testing.T) {
	p := node.New(executor.NewMatchMock(), "")
	if got := p.Name(); got != "node" {
		t.Errorf("Name() = %q, want node", got)
	}
}

func TestDescription_NonEmpty(t *testing.T) {
	p := node.New(executor.NewMatchMock(), "")
	if p.Description() == "" {
		t.Error("Description() is empty")
	}
}

// --- Upgrade ---

func TestUpgrade_PnpmArgs(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm update -g typescript", Response: executor.MockCall{}},
	)
	p := node.New(m, "pnpm")
	if err := p.Upgrade(context.Background(), tool("typescript")); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	m.AssertCalled(t, "pnpm update -g typescript")
}

func TestUpgrade_Error(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm update -g bad", Response: executor.MockCall{Err: errors.New("exit 1")}},
	)
	p := node.New(m, "pnpm")
	if err := p.Upgrade(context.Background(), tool("bad")); err == nil {
		t.Fatal("expected error from failed upgrade")
	}
}

func TestUpgradeWithManager_UsesInstalledManager(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "npm update -g typescript", Response: executor.MockCall{}},
	)
	p := node.New(m, "bun")
	if err := p.UpgradeWithManager(context.Background(), tool("typescript"), "npm"); err != nil {
		t.Fatalf("UpgradeWithManager: %v", err)
	}
	m.AssertCalled(t, "npm update -g typescript")
	if len(m.CallsMatching("bun update")) > 0 {
		t.Fatal("should not upgrade through active bun manager when installed manager is npm")
	}
}

// --- InstalledMap ---

func TestInstalledMap_Pnpm(t *testing.T) {
	out := listOutput("typescript@5.3.3", "prettier@3.1.1")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Stdout: out}},
	)
	p := node.New(m, "pnpm")
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["typescript"] != "5.3.3" {
		t.Errorf("map[typescript] = %q, want 5.3.3", got["typescript"])
	}
	if got["prettier"] != "3.1.1" {
		t.Errorf("map[prettier] = %q, want 3.1.1", got["prettier"])
	}
}

func TestInstalledMap_Error(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Err: errors.New("exit 1")}},
	)
	p := node.New(m, "pnpm")
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- InstalledByManager ---

func TestInstalledByManager_EffectiveManagerFirst(t *testing.T) {
	// pnpm is effective; npm also installed with some packages.
	// pnpm package wins when same name exists in both.
	pnpmOut := listOutput("typescript@5.3.3", "prettier@3.1.1")
	npmOut := listOutput("typescript@5.0.0", "yaml-lint@1.7.0") // old typescript should lose
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "10.0.0"}},
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "11.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Stdout: pnpmOut}},
		executor.MatchRule{Pattern: "npm list -g --depth=0", Response: executor.MockCall{Stdout: npmOut}},
	)
	p := node.New(m, "")
	got, err := p.InstalledByManager(context.Background())
	if err != nil {
		t.Fatalf("InstalledByManager: %v", err)
	}
	if got["typescript"].Version != "5.3.3" {
		t.Errorf("typescript version = %q, want 5.3.3 (pnpm wins)", got["typescript"].Version)
	}
	if got["typescript"].ConcreteManager != "pnpm" {
		t.Errorf("typescript manager = %q, want pnpm", got["typescript"].ConcreteManager)
	}
	if got["prettier"].ConcreteManager != "pnpm" {
		t.Errorf("prettier manager = %q, want pnpm", got["prettier"].ConcreteManager)
	}
	if got["yaml-lint"].ConcreteManager != "npm" {
		t.Errorf("yaml-lint manager = %q, want npm", got["yaml-lint"].ConcreteManager)
	}
}

func TestInstalledByManager_AllManagersProbed(t *testing.T) {
	// Only npm installed; bun and pnpm not available.
	npmOut := listOutput("typescript@5.3.3")
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "11.0.0"}},
		executor.MatchRule{Pattern: "npm list -g --depth=0", Response: executor.MockCall{Stdout: npmOut}},
	)
	p := node.New(m, "")
	got, err := p.InstalledByManager(context.Background())
	if err != nil {
		t.Fatalf("InstalledByManager: %v", err)
	}
	if got["typescript"].ConcreteManager != "npm" {
		t.Errorf("typescript manager = %q, want npm", got["typescript"].ConcreteManager)
	}
}

func TestInstalledByManager_AvailableManagerListFailureReturnsError(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "10.0.0"}},
		executor.MatchRule{Pattern: "pnpm ls -g --depth=0", Response: executor.MockCall{Err: errors.New("list failed")}},
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := node.New(m, "")
	if _, err := p.InstalledByManager(context.Background()); err == nil {
		t.Fatal("InstalledByManager error = nil, want list failure")
	}
}

// --- OutdatedMap ---

func TestOutdatedMap_Empty(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm outdated -g --json", Response: executor.MockCall{Stdout: "{}"}},
	)
	p := node.New(m, "pnpm")
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil map for empty output, got %v", got)
	}
}

func TestOutdatedMap_Found(t *testing.T) {
	out := `{"typescript":{"latest":"5.4.0"},"prettier":{"latest":"3.2.0"}}`
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
		executor.MatchRule{Pattern: "pnpm outdated -g --json", Response: executor.MockCall{Stdout: out, Err: errors.New("exit 1")}},
	)
	p := node.New(m, "pnpm")
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["typescript"] != "5.4.0" {
		t.Errorf("map[typescript] = %q, want 5.4.0", got["typescript"])
	}
}

func TestOutdatedMap_ProbesAllAvailableManagers(t *testing.T) {
	pnpmOut := `{"typescript":{"latest":"5.4.0"}}`
	npmOut := `{"prettier":{"latest":"3.2.0"}}`
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Stdout: "1.1.0"}},
		executor.MatchRule{Pattern: "bun outdated -g --json", Response: executor.MockCall{Err: errors.New("unsupported")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "10.0.0"}},
		executor.MatchRule{Pattern: "pnpm outdated -g --json", Response: executor.MockCall{Stdout: pnpmOut, Err: errors.New("exit 1")}},
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "11.0.0"}},
		executor.MatchRule{Pattern: "npm outdated -g --json", Response: executor.MockCall{Stdout: npmOut, Err: errors.New("exit 1")}},
	)
	p := node.New(m, "")
	got, err := p.OutdatedMap(context.Background())
	if err != nil {
		t.Fatalf("OutdatedMap: %v", err)
	}
	if got["typescript"] != "5.4.0" {
		t.Errorf("map[typescript] = %q, want 5.4.0", got["typescript"])
	}
	if got["prettier"] != "3.2.0" {
		t.Errorf("map[prettier] = %q, want 3.2.0", got["prettier"])
	}
	m.AssertCalled(t, "bun outdated -g --json")
	m.AssertCalled(t, "pnpm outdated -g --json")
	m.AssertCalled(t, "npm outdated -g --json")
}

func TestOutdatedByManager_PreservesManagerAttribution(t *testing.T) {
	pnpmOut := `{"typescript":{"latest":"5.4.0"}}`
	npmOut := `{"prettier":{"latest":"3.2.0"}}`
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "10.0.0"}},
		executor.MatchRule{Pattern: "pnpm outdated -g --json", Response: executor.MockCall{Stdout: pnpmOut, Err: errors.New("exit 1")}},
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Stdout: "11.0.0"}},
		executor.MatchRule{Pattern: "npm outdated -g --json", Response: executor.MockCall{Stdout: npmOut, Err: errors.New("exit 1")}},
	)
	p := node.New(m, "")
	got, err := p.OutdatedByManager(context.Background())
	if err != nil {
		t.Fatalf("OutdatedByManager: %v", err)
	}
	if got["pnpm"]["typescript"] != "5.4.0" {
		t.Fatalf("pnpm typescript latest = %q, want 5.4.0", got["pnpm"]["typescript"])
	}
	if got["npm"]["prettier"] != "3.2.0" {
		t.Fatalf("npm prettier latest = %q, want 3.2.0", got["npm"]["prettier"])
	}
}

func TestOutdatedMap_EmptyFailureReturnsError(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Stdout: "1.1.0"}},
		executor.MatchRule{Pattern: "bun outdated -g --json", Response: executor.MockCall{Err: errors.New("unsupported"), Stderr: "unknown command"}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "npm --version", Response: executor.MockCall{Err: errors.New("not found")}},
	)
	p := node.New(m, "")
	if _, err := p.OutdatedMap(context.Background()); err == nil {
		t.Fatal("OutdatedMap error = nil, want error for failed empty outdated output")
	}
}

// --- ResolvedName ---

func TestResolvedName_Bun(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Stdout: "1.1.0"}},
	)
	p := node.New(m, "bun")
	name, err := p.ResolvedName(context.Background())
	if err != nil {
		t.Fatalf("ResolvedName: %v", err)
	}
	if name != "bun" {
		t.Errorf("ResolvedName() = %q, want bun", name)
	}
}

func TestResolvedName_Pnpm(t *testing.T) {
	m := executor.NewMatchMock(
		executor.MatchRule{Pattern: "bun --version", Response: executor.MockCall{Err: errors.New("not found")}},
		executor.MatchRule{Pattern: "pnpm --version", Response: executor.MockCall{Stdout: "8.0.0"}},
	)
	p := node.New(m, "")
	name, err := p.ResolvedName(context.Background())
	if err != nil {
		t.Fatalf("ResolvedName: %v", err)
	}
	if name != "pnpm" {
		t.Errorf("ResolvedName() = %q, want pnpm", name)
	}
}

func TestResolvedName_NoneFound(t *testing.T) {
	m := executor.NewMatchMock().WithFallback(executor.MockCall{Err: errors.New("not found")})
	p := node.New(m, "")
	name, err := p.ResolvedName(context.Background())
	if err == nil {
		t.Fatal("expected error when no manager found, got nil")
	}
	if name != "" {
		t.Errorf("ResolvedName() = %q on error, want empty", name)
	}
}
