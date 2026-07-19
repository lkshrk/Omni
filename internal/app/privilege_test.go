package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	apkpkg "github.com/lkshrk/omni/internal/provider/apk"
	aptpkg "github.com/lkshrk/omni/internal/provider/apt"
	"github.com/lkshrk/omni/internal/provider/brew"
	dnfpkg "github.com/lkshrk/omni/internal/provider/dnf"
	pacmanpkg "github.com/lkshrk/omni/internal/provider/pacman"
	zypppkg "github.com/lkshrk/omni/internal/provider/zypper"
)

func TestRecordPrivilegeError_NilDB(t *testing.T) {
	t.Parallel(
	// App with no DB initialised — readDB() returns nil.
	// recordPrivilegeError must not panic.
	)

	a := &App{}
	err := errors.New("sudo: a password is required")
	a.recordPrivilegeError(context.Background(), "vim", "apt", "vim", err)
	// If we get here without panic, the nil-guard works.
}

func TestRecordPrivilegeError_NonPrivilegeError(t *testing.T) {
	t.Parallel(
	// Non-privilege errors should be silently ignored (no DB access).
	)

	a := &App{} // nil DB — would panic if it tried to access DB
	err := errors.New("network timeout")
	a.recordPrivilegeError(context.Background(), "vim", "apt", "vim", err)
}

func TestExternalPrivilegeActionText(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		action provider.PrivilegeAction
		want   string
	}{
		{action: provider.PrivilegeActionInstall, want: "install"},
		{action: provider.PrivilegeActionUninstall, want: "uninstall"},
		{action: provider.PrivilegeActionUpgrade, want: "upgrade"},
		{action: provider.PrivilegeAction("repair"), want: "repair"},
	} {
		if got := externalToolActionVerb(tt.action); got != tt.want {
			t.Fatalf("externalToolActionVerb(%q) = %q, want %q", tt.action, got, tt.want)
		}
	}

	if got := privilegeReason(provider.PrivilegePlan{Reason: "apt requires sudo"}); got != "apt requires sudo" {
		t.Fatalf("privilegeReason explicit = %q, want provider reason", got)
	}
	if got := privilegeReason(provider.PrivilegePlan{}); got != "package manager needs privileged access" {
		t.Fatalf("privilegeReason default = %q, want fallback reason", got)
	}
}

func TestPrivilegedApprovalRowMessage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		action provider.PrivilegeAction
		want   string
	}{
		{action: provider.PrivilegeActionInstall, want: "admin approval required to install"},
		{action: provider.PrivilegeActionUpgrade, want: "admin approval required to upgrade"},
		{action: provider.PrivilegeActionUninstall, want: "admin approval required to uninstall"},
		{action: provider.PrivilegeAction("repair"), want: "admin approval required to install"},
	} {
		if got := privilegedApprovalRowMessage(tt.action); got != tt.want {
			t.Fatalf("privilegedApprovalRowMessage(%q) = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestShellJoinQuotesUnsafeCommandParts(t *testing.T) {
	t.Parallel()
	got := shellJoin([]string{"apt-get", "install", "-y", "safe_pkg", "name with spaces", "owner's-tool", ""})
	want := "apt-get install -y safe_pkg 'name with spaces' 'owner'\\''s-tool' ''"
	if got != want {
		t.Fatalf("shellJoin = %q, want %q", got, want)
	}
}

func TestPrivilegedToolCommand_BrewCaskActions(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(brew.New(nil))
	plan := provider.PrivilegePlan{
		Requirement: provider.PrivilegeMaybe,
		Reason:      "brew cask parsec uses pkgutil uninstall",
	}
	tool := database.ToolCache{
		Name:          "parsec",
		Provider:      provider.EcosystemSystem,
		Package:       "parsec",
		InstalledWith: "brew",
		Options:       map[string]string{"brew_kind": "cask"},
	}
	tests := []struct {
		name   string
		action provider.PrivilegeAction
		want   string
	}{
		{name: "install", action: provider.PrivilegeActionInstall, want: "brew install --cask --no-ask parsec"},
		{name: "uninstall", action: provider.PrivilegeActionUninstall, want: "brew uninstall --cask parsec"},
		{name: "upgrade", action: provider.PrivilegeActionUpgrade, want: "brew upgrade --cask --greedy --no-ask parsec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := a.PrivilegedToolCommand(context.Background(), toolViewFromCache(&tool), tt.action, plan)
			if !ok {
				t.Fatal("PrivilegedToolCommand ok = false")
			}
			if got.Display != tt.want {
				t.Fatalf("Display = %q, want %q", got.Display, tt.want)
			}
			if got.ProviderName != provider.EcosystemSystem || got.InstalledWith != "brew" {
				t.Fatalf("provider state = %q/%q, want system/brew", got.ProviderName, got.InstalledWith)
			}
			if got.Options["brew_kind"] != "cask" {
				t.Fatalf("Options[brew_kind] = %q, want cask", got.Options["brew_kind"])
			}
		})
	}
}

func TestPrivilegedToolCommand_SudoBackedProviders(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(
		aptpkg.New(nil),
		apkpkg.New(nil),
		dnfpkg.New(nil),
		pacmanpkg.New(nil),
		zypppkg.New(nil),
	)
	plan := provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "package manager needs sudo/root access"}
	tests := []struct {
		name   string
		action provider.PrivilegeAction
		tool   database.ToolCache
		want   string
	}{
		{
			name:   "apt install",
			action: provider.PrivilegeActionInstall,
			tool:   database.ToolCache{Name: "vim", Provider: "apt", Package: "vim"},
			want:   "apt-get install -y vim",
		},
		{
			name:   "apt install uses name when package missing",
			action: provider.PrivilegeActionInstall,
			tool:   database.ToolCache{Name: "vim", Provider: "apt"},
			want:   "apt-get install -y vim",
		},
		{
			name:   "apk uninstall",
			action: provider.PrivilegeActionUninstall,
			tool:   database.ToolCache{Name: "vim", Provider: "apk", Package: "vim"},
			want:   "apk del vim",
		},
		{
			name:   "dnf upgrade",
			action: provider.PrivilegeActionUpgrade,
			tool:   database.ToolCache{Name: "vim", Provider: "dnf", Package: "vim"},
			want:   "dnf upgrade -y vim",
		},
		{
			name:   "pacman upgrade",
			action: provider.PrivilegeActionUpgrade,
			tool:   database.ToolCache{Name: "vim", Provider: "pacman", Package: "vim"},
			want:   "pacman -S --noconfirm vim",
		},
		{
			name:   "zypper uninstall",
			action: provider.PrivilegeActionUninstall,
			tool:   database.ToolCache{Name: "vim", Provider: "zypper", Package: "vim"},
			want:   "zypper remove -y vim",
		},
		{
			name:   "system install uses plan provider when resolution missing",
			action: provider.PrivilegeActionInstall,
			tool:   database.ToolCache{Name: "vim", Provider: provider.EcosystemSystem, Package: "vim"},
			want:   "dnf install -y vim",
		},
		{
			name:   "system uninstall uses installed manager",
			action: provider.PrivilegeActionUninstall,
			tool:   database.ToolCache{Name: "vim", Provider: provider.EcosystemSystem, Package: "vim", InstalledWith: "dnf"},
			want:   "dnf remove -y vim",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localPlan := plan
			if tt.name == "system install uses plan provider when resolution missing" {
				localPlan.Reason = "dnf install vim"
			}
			got, ok := a.PrivilegedToolCommand(context.Background(), toolViewFromCache(&tt.tool), tt.action, localPlan)
			if !ok {
				t.Fatal("PrivilegedToolCommand ok = false")
			}
			if got.Display != expectedInteractiveAdminDisplay(tt.want) {
				t.Fatalf("Display = %q, want %q", got.Display, expectedInteractiveAdminDisplay(tt.want))
			}
		})
	}
}

func TestPrivilegedToolCommand_SystemPlanStoresConcreteInstalledWith(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(dnfpkg.New(nil))
	plan := provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "dnf install vim"}
	tool := database.ToolCache{Name: "vim", Provider: provider.EcosystemSystem, Package: "vim"}

	got, ok := a.PrivilegedToolCommand(context.Background(), toolViewFromCache(&tool), provider.PrivilegeActionInstall, plan)
	if !ok {
		t.Fatal("PrivilegedToolCommand ok = false")
	}
	if got.ProviderName != provider.EcosystemSystem || got.InstalledWith != "dnf" {
		t.Fatalf("provider state = %q/%q, want system/dnf", got.ProviderName, got.InstalledWith)
	}
}

func TestPrivilegedToolCommand_UnsupportedOrGenericBrewProvider(t *testing.T) {
	t.Parallel()
	tool := database.ToolCache{Name: "vim", Provider: "pip", Package: "vim"}
	plan := provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "package manager needs sudo/root access"}
	if _, ok := newPrivilegeCommandTestApp().PrivilegedToolCommand(context.Background(), toolViewFromCache(&tool), provider.PrivilegeActionInstall, plan); ok {
		t.Fatal("PrivilegedToolCommand ok = true for unsupported provider")
	}

	brewTool := database.ToolCache{Name: "parsec", Provider: "brew", Package: "parsec"}
	if _, ok := newPrivilegeCommandTestApp(brew.New(nil)).PrivilegedToolCommand(context.Background(), toolViewFromCache(&brewTool), provider.PrivilegeActionInstall, plan); ok {
		t.Fatal("PrivilegedToolCommand ok = true for generic brew privilege")
	}
}

func TestPrivilegeQueuePlan_PrefersSpecificRowReason(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(brew.New(nil))
	tool := &database.ToolCache{
		Name:            "karabiner-elements",
		Provider:        "brew",
		Package:         "karabiner-elements",
		Privilege:       string(provider.PrivilegeRequired),
		PrivilegeReason: sql.NullString{String: "package manager needs sudo/root access", Valid: true},
	}
	key := privilegeQueueToolKey(tool.Name, tool.Provider)

	plan, err := a.PrivilegeQueuePlan(context.Background(), PrivilegeQueueRequest{
		RowErrors: map[string]string{
			key: "requires sudo: brew cask karabiner-elements uses a pkg installer",
		},
		Actions: map[string]provider.PrivilegeAction{
			key: provider.PrivilegeActionInstall,
		},
		Tools: toolViewsFromCache([]*database.ToolCache{tool}),
	})
	if err != nil {
		t.Fatalf("PrivilegeQueuePlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("queue items = %#v, want one item", plan.Items)
	}
	item := plan.Items[0]
	if item.Key != key || item.Action != provider.PrivilegeActionInstall {
		t.Fatalf("item key/action = %q/%q", item.Key, item.Action)
	}
	if item.Plan.Reason != "brew cask karabiner-elements uses a pkg installer" {
		t.Fatalf("reason = %q, want row-specific cask reason", item.Plan.Reason)
	}
	if item.Command.Display != "brew install --cask --no-ask karabiner-elements" {
		t.Fatalf("display command = %q", item.Command.Display)
	}
	if item.ApprovalMessage != "admin approval required to install" {
		t.Fatalf("approval = %q", item.ApprovalMessage)
	}
}

func TestPrivilegeQueuePlan_UsesProviderPlanWhenRowReasonIsGeneric(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(&privilegePlanningProvider{
		name: "brew",
		plan: provider.PrivilegePlan{
			Requirement: provider.PrivilegeMaybe,
			Reason:      "brew cask karabiner-elements uses a pkg installer",
		},
	})
	tool := &database.ToolCache{
		Name:     "karabiner-elements",
		Provider: "brew",
		Package:  "karabiner-elements",
	}
	key := privilegeQueueToolKey(tool.Name, tool.Provider)

	plan, err := a.PrivilegeQueuePlan(context.Background(), PrivilegeQueueRequest{
		RowErrors: map[string]string{
			key: "installer requires administrator privileges",
		},
		Actions: map[string]provider.PrivilegeAction{
			key: provider.PrivilegeActionInstall,
		},
		Tools: toolViewsFromCache([]*database.ToolCache{tool}),
	})
	if err != nil {
		t.Fatalf("PrivilegeQueuePlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("queue items = %#v, want one item", plan.Items)
	}
	item := plan.Items[0]
	if item.Plan.Reason != "brew cask karabiner-elements uses a pkg installer" {
		t.Fatalf("reason = %q, want provider cask reason", item.Plan.Reason)
	}
	if item.Command.Display != "brew install --cask --no-ask karabiner-elements" {
		t.Fatalf("display command = %q", item.Command.Display)
	}
}

func TestPrivilegeQueuePlanUsesMarkedPrivilegeMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, aptpkg.New(nil)); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	reason := "apt install vim needs sudo"
	if err := a.MarkToolPrivilegeRequired(ctx, "vim", "apt", "vim", string(provider.PrivilegeRequired), reason); err != nil {
		t.Fatalf("MarkToolPrivilegeRequired: %v", err)
	}
	tool, err := a.DB().Get(ctx, "vim", "apt", "vim")
	if err != nil {
		t.Fatalf("Get marked tool: %v", err)
	}
	if tool.Privilege != string(provider.PrivilegeRequired) {
		t.Fatalf("Privilege = %q, want required", tool.Privilege)
	}
	if !tool.PrivilegeReason.Valid || tool.PrivilegeReason.String != reason {
		t.Fatalf("PrivilegeReason = %+v, want cached sudo reason", tool.PrivilegeReason)
	}

	key := privilegeQueueToolKey(tool.Name, tool.Provider)
	plan, err := a.PrivilegeQueuePlan(ctx, PrivilegeQueueRequest{
		RowErrors: map[string]string{
			key: "install failed",
		},
		Actions: map[string]provider.PrivilegeAction{
			key: provider.PrivilegeActionInstall,
		},
		Tools: toolViewsFromCache([]*database.ToolCache{tool}),
	})
	if err != nil {
		t.Fatalf("PrivilegeQueuePlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("queue items = %#v, want one cached-privilege item", plan.Items)
	}
	item := plan.Items[0]
	if item.Plan.Requirement != provider.PrivilegeRequired || item.Plan.Reason != reason {
		t.Fatalf("plan = %+v, want cached required sudo reason", item.Plan)
	}
	if item.Command.Display != expectedInteractiveAdminDisplay("apt-get install -y vim") {
		t.Fatalf("display command = %q, want apt-get install admin command", item.Command.Display)
	}
	if len(plan.RowErrors) != 0 {
		t.Fatalf("row errors = %#v, want none", plan.RowErrors)
	}
}

func TestPrivilegeQueuePlanBuildsItemFromRowErrorOnly(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(aptpkg.New(nil))
	key := privilegeQueueToolKey("vim", "apt")

	plan, err := a.PrivilegeQueuePlan(context.Background(), PrivilegeQueueRequest{
		RowErrors: map[string]string{
			key: "requires sudo/root privileges",
		},
		Actions: map[string]provider.PrivilegeAction{
			key: provider.PrivilegeActionInstall,
		},
	})
	if err != nil {
		t.Fatalf("PrivilegeQueuePlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("queue items = %#v, want synthesized row-error item", plan.Items)
	}
	item := plan.Items[0]
	if item.Tool.Name != "vim" || item.Tool.Provider != "apt" || item.Tool.Package != "vim" {
		t.Fatalf("tool = %+v, want synthesized vim/apt row", item.Tool)
	}
	if item.Command.Display != expectedInteractiveAdminDisplay("apt-get install -y vim") {
		t.Fatalf("display command = %q, want synthesized apt install command", item.Command.Display)
	}
}

func TestPrivilegeQueuePlanSkipsInvalidActionsAndInstalledInstalls(t *testing.T) {
	t.Parallel()
	a := newPrivilegeCommandTestApp(aptpkg.New(nil))
	installed := &database.ToolCache{Name: "vim", Provider: "apt", Package: "vim", Installed: true}
	invalid := &database.ToolCache{Name: "fd", Provider: "apt", Package: "fd"}
	installedKey := privilegeQueueToolKey(installed.Name, installed.Provider)
	invalidKey := privilegeQueueToolKey(invalid.Name, invalid.Provider)

	plan, err := a.PrivilegeQueuePlan(context.Background(), PrivilegeQueueRequest{
		RowErrors: map[string]string{
			installedKey: "requires sudo/root privileges",
			invalidKey:   "requires sudo/root privileges",
		},
		Actions: map[string]provider.PrivilegeAction{
			installedKey: provider.PrivilegeActionInstall,
			invalidKey:   provider.PrivilegeAction("repair"),
		},
		Tools: toolViewsFromCache([]*database.ToolCache{installed, invalid}),
	})
	if err != nil {
		t.Fatalf("PrivilegeQueuePlan: %v", err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("queue items = %#v, want no privileged actions for stale install or invalid action", plan.Items)
	}
	if len(plan.RowErrors) != 0 {
		t.Fatalf("row errors = %#v, want none", plan.RowErrors)
	}
}

func TestPrivilegeQueuePlanRejectsProviderToolUninstall(t *testing.T) {
	t.Parallel()
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	ctx := context.Background()
	if err := a.InitTestMode(ctx, aptpkg.New(nil)); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	tool := &database.ToolCache{
		Name:      "apt",
		Provider:  "apt",
		Package:   "apt",
		Installed: true,
		Privilege: string(provider.PrivilegeRequired),
	}
	key := privilegeQueueToolKey(tool.Name, tool.Provider)

	plan, err := a.PrivilegeQueuePlan(ctx, PrivilegeQueueRequest{
		RowErrors: map[string]string{
			key: "requires sudo/root privileges",
		},
		Actions: map[string]provider.PrivilegeAction{
			key: provider.PrivilegeActionUninstall,
		},
		Tools: toolViewsFromCache([]*database.ToolCache{tool}),
	})
	if err != nil {
		t.Fatalf("PrivilegeQueuePlan: %v", err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("queue items = %#v, want none for protected provider tool", plan.Items)
	}
	got := plan.RowErrors[key]
	want := `tool "apt" is a package manager/provider and cannot be deleted or uninstalled by omni`
	if got != want {
		t.Fatalf("row error = %q, want %q", got, want)
	}
}

type privilegePlanningProvider struct {
	name string
	plan provider.PrivilegePlan
}

func (p *privilegePlanningProvider) Name() string { return p.name }

func (p *privilegePlanningProvider) Description() string { return p.name }

func (p *privilegePlanningProvider) Available(context.Context) (bool, error) { return true, nil }

func (p *privilegePlanningProvider) Install(context.Context, provider.Tool) error { return nil }

func (p *privilegePlanningProvider) Uninstall(context.Context, provider.Tool) error { return nil }

func (p *privilegePlanningProvider) Upgrade(context.Context, provider.Tool) error { return nil }

func (p *privilegePlanningProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return false, "", nil
}

func (p *privilegePlanningProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func (p *privilegePlanningProvider) PrivilegePlan(context.Context, provider.PrivilegeAction, provider.Tool) (provider.PrivilegePlan, error) {
	return p.plan, nil
}

func (p *privilegePlanningProvider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	switch action {
	case provider.PrivilegeActionInstall:
		return "brew", []string{"install", "--cask", "--no-ask", tool.EffectivePackage()}, true
	case provider.PrivilegeActionUninstall:
		return "brew", []string{"uninstall", "--cask", tool.EffectivePackage()}, true
	case provider.PrivilegeActionUpgrade:
		return "brew", []string{"upgrade", "--cask", "--no-ask", tool.EffectivePackage()}, true
	default:
		return "", nil, false
	}
}

func newPrivilegeCommandTestApp(providers ...provider.Provider) *App {
	a := &App{registry: provider.NewRegistry()}
	for _, p := range providers {
		a.registry.RegisterWithMetadata(p, provider.BuiltinMetadata(p.Name()))
	}
	return a
}

func expectedInteractiveAdminDisplay(direct string) string {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return direct
	}
	return "sudo " + direct
}
