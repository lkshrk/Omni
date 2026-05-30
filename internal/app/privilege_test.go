package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
	// App with no DB initialised — readDB() returns nil.
	// recordPrivilegeError must not panic.
	a := &App{}
	err := errors.New("sudo: a password is required")
	a.recordPrivilegeError(context.Background(), "vim", "apt", "vim", err)
	// If we get here without panic, the nil-guard works.
}

func TestRecordPrivilegeError_NonPrivilegeError(t *testing.T) {
	// Non-privilege errors should be silently ignored (no DB access).
	a := &App{} // nil DB — would panic if it tried to access DB
	err := errors.New("network timeout")
	a.recordPrivilegeError(context.Background(), "vim", "apt", "vim", err)
}

func TestPrivilegedToolCommand_BrewCaskActions(t *testing.T) {
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
		{name: "install", action: provider.PrivilegeActionInstall, want: "brew install --cask parsec"},
		{name: "uninstall", action: provider.PrivilegeActionUninstall, want: "brew uninstall --cask parsec"},
		{name: "upgrade", action: provider.PrivilegeActionUpgrade, want: "brew upgrade --cask parsec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := a.PrivilegedToolCommand(context.Background(), &tool, tt.action, plan)
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
			got, ok := a.PrivilegedToolCommand(context.Background(), &tt.tool, tt.action, localPlan)
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
	a := newPrivilegeCommandTestApp(dnfpkg.New(nil))
	plan := provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "dnf install vim"}
	tool := database.ToolCache{Name: "vim", Provider: provider.EcosystemSystem, Package: "vim"}

	got, ok := a.PrivilegedToolCommand(context.Background(), &tool, provider.PrivilegeActionInstall, plan)
	if !ok {
		t.Fatal("PrivilegedToolCommand ok = false")
	}
	if got.ProviderName != provider.EcosystemSystem || got.InstalledWith != "dnf" {
		t.Fatalf("provider state = %q/%q, want system/dnf", got.ProviderName, got.InstalledWith)
	}
}

func TestPrivilegedToolCommand_UnsupportedOrGenericBrewProvider(t *testing.T) {
	tool := database.ToolCache{Name: "vim", Provider: "pip", Package: "vim"}
	plan := provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "package manager needs sudo/root access"}
	if _, ok := newPrivilegeCommandTestApp().PrivilegedToolCommand(context.Background(), &tool, provider.PrivilegeActionInstall, plan); ok {
		t.Fatal("PrivilegedToolCommand ok = true for unsupported provider")
	}

	brewTool := database.ToolCache{Name: "parsec", Provider: "brew", Package: "parsec"}
	if _, ok := newPrivilegeCommandTestApp(brew.New(nil)).PrivilegedToolCommand(context.Background(), &brewTool, provider.PrivilegeActionInstall, plan); ok {
		t.Fatal("PrivilegedToolCommand ok = true for generic brew privilege")
	}
}

func TestPrivilegeQueuePlan_PrefersSpecificRowReason(t *testing.T) {
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
		Tools: []*database.ToolCache{tool},
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
	if item.Command.Display != "brew install --cask karabiner-elements" {
		t.Fatalf("display command = %q", item.Command.Display)
	}
	if item.ApprovalMessage != "admin approval required to install" {
		t.Fatalf("approval = %q", item.ApprovalMessage)
	}
}

func TestPrivilegeQueuePlan_UsesProviderPlanWhenRowReasonIsGeneric(t *testing.T) {
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
		Tools: []*database.ToolCache{tool},
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
	if item.Command.Display != "brew install --cask karabiner-elements" {
		t.Fatalf("display command = %q", item.Command.Display)
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
		return "brew", []string{"install", "--cask", tool.EffectivePackage()}, true
	case provider.PrivilegeActionUninstall:
		return "brew", []string{"uninstall", "--cask", tool.EffectivePackage()}, true
	case provider.PrivilegeActionUpgrade:
		return "brew", []string{"upgrade", "--cask", tool.EffectivePackage()}, true
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
