package system_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/system"
)

type fakeProvider struct {
	name      string
	available bool
	availErr  error
	installed bool
	version   string
	installFn func() error
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) Description() string { return f.name }
func (f *fakeProvider) Available(_ context.Context) (bool, error) {
	return f.available, f.availErr
}
func (f *fakeProvider) Install(_ context.Context, _ provider.Tool) error {
	if f.installFn != nil {
		return f.installFn()
	}
	return nil
}
func (f *fakeProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (f *fakeProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (f *fakeProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return f.installed, f.version, nil
}
func (f *fakeProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func TestAvailable_FirstDelegate(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true})
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_SecondDelegate(t *testing.T) {
	p := system.New(
		&fakeProvider{name: "apt", available: false},
		&fakeProvider{name: "dnf", available: true},
	)
	ok, err := p.Available(context.Background())
	if err != nil || !ok {
		t.Errorf("Available() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAvailable_NoneAvailable(t *testing.T) {
	p := system.New(
		&fakeProvider{name: "apt", available: false},
		&fakeProvider{name: "dnf", available: false},
	)
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAvailable_NoDelegates(t *testing.T) {
	p := system.New()
	ok, err := p.Available(context.Background())
	if err != nil || ok {
		t.Errorf("Available() = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAvailable_DelegateError(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", availErr: errors.New("check failed")})
	_, err := p.Available(context.Background())
	if err == nil {
		t.Error("expected error from delegate, got nil")
	}
}

func TestInstall_Delegates(t *testing.T) {
	var calledProvider string
	p := system.New(
		&fakeProvider{name: "apt", available: false},
		&fakeProvider{
			name:      "dnf",
			available: true,
			installFn: func() error { calledProvider = "dnf"; return nil },
		},
	)
	tool := provider.Tool{Name: "ripgrep", Provider: "system", Package: "ripgrep"}
	if err := p.Install(context.Background(), tool); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if calledProvider != "dnf" {
		t.Errorf("expected dnf to handle install, got %q", calledProvider)
	}
}

func TestInstall_NoDelegate_Error(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: false})
	err := p.Install(context.Background(), provider.Tool{Name: "ripgrep"})
	if err == nil {
		t.Fatal("expected error when no PM available")
	}
}

func TestIsInstalled_Delegates(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true, installed: true, version: "14.1.1"})
	ok, ver, err := p.IsInstalled(context.Background(), provider.Tool{Name: "ripgrep"})
	if err != nil || !ok || ver != "14.1.1" {
		t.Errorf("IsInstalled() = (%v, %q, %v), want (true, 14.1.1, nil)", ok, ver, err)
	}
}

func TestUninstall_Delegates(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true})
	err := p.Uninstall(context.Background(), provider.Tool{Name: "ripgrep"})
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
}

func TestUninstall_NoDelegate_Error(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: false})
	if err := p.Uninstall(context.Background(), provider.Tool{Name: "ripgrep"}); err == nil {
		t.Fatal("expected error when no PM available")
	}
}

func TestUpgrade_Delegates(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true})
	if err := p.Upgrade(context.Background(), provider.Tool{Name: "ripgrep"}); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
}

func TestUpgrade_NoDelegate_Error(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: false})
	if err := p.Upgrade(context.Background(), provider.Tool{Name: "ripgrep"}); err == nil {
		t.Fatal("expected error when no PM available")
	}
}

func TestListInstalled_Delegates(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true})
	tools, err := p.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	_ = tools // fakeProvider returns nil; just checking no error
}

func TestListInstalled_NoDelegate_Error(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: false})
	if _, err := p.ListInstalled(context.Background()); err == nil {
		t.Fatal("expected error when no PM available")
	}
}

type fakeBulkProvider struct {
	fakeProvider
	bulkMap map[string]string
}

func (f *fakeBulkProvider) InstalledMap(_ context.Context) (map[string]string, error) {
	return f.bulkMap, nil
}

func TestInstalledMap_DelegatesBulk(t *testing.T) {
	delegate := &fakeBulkProvider{
		fakeProvider: fakeProvider{name: "brew", available: true},
		bulkMap:      map[string]string{"ripgrep": "14.1.1", "jq": "1.7.1"},
	}
	p := system.New(delegate)
	got, err := p.InstalledMap(context.Background())
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["ripgrep"] != "14.1.1" {
		t.Errorf("map[ripgrep] = %q, want 14.1.1", got["ripgrep"])
	}
	if got["jq"] != "1.7.1" {
		t.Errorf("map[jq] = %q, want 1.7.1", got["jq"])
	}
}

func TestInstalledMap_DelegateNoBulk_Error(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true})
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected error when delegate does not implement BulkChecker")
	}
}

func TestInstalledMap_NoDelegate_Error(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: false})
	if _, err := p.InstalledMap(context.Background()); err == nil {
		t.Fatal("expected error when no PM available")
	}
}

func TestName(t *testing.T) {
	p := system.New()
	if p.Name() != "system" {
		t.Errorf("Name() = %q, want %q", p.Name(), "system")
	}
}

func TestDescription(t *testing.T) {
	p := system.New()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestResolvedName_FirstDelegate(t *testing.T) {
	first := &fakeProvider{name: "apt", available: true}
	second := &fakeProvider{name: "dnf", available: true}
	p := system.New(first, second)

	name, err := p.ResolvedName(context.Background())
	if err != nil {
		t.Fatalf("ResolvedName: %v", err)
	}
	if name != "apt" {
		t.Errorf("ResolvedName() = %q, want apt (first available delegate)", name)
	}
}

func TestResolvedName_SecondDelegate(t *testing.T) {
	first := &fakeProvider{name: "apt", available: false}
	second := &fakeProvider{name: "dnf", available: true}
	p := system.New(first, second)

	name, err := p.ResolvedName(context.Background())
	if err != nil {
		t.Fatalf("ResolvedName: %v", err)
	}
	if name != "dnf" {
		t.Errorf("ResolvedName() = %q, want dnf (second delegate)", name)
	}
}

func TestResolvedName_NoneAvailable(t *testing.T) {
	p := system.New(
		&fakeProvider{name: "apt", available: false},
		&fakeProvider{name: "dnf", available: false},
	)

	name, err := p.ResolvedName(context.Background())
	if err == nil {
		t.Fatal("expected error when no delegate available, got nil")
	}
	if name != "" {
		t.Errorf("ResolvedName() = %q on error, want empty", name)
	}
}
