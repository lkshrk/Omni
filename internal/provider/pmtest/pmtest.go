//go:build pmcontainer

package pmtest

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

const testTimeout = 2 * time.Minute

// Context returns a timeout context long enough for distro metadata refreshes.
func Context(t *testing.T, providerName string) (context.Context, context.CancelFunc) {
	t.Helper()
	requireContainerGuard(t, providerName)
	return context.WithTimeout(context.Background(), testTimeout)
}

func Run(t *testing.T, ctx context.Context, name string, args ...string) {
	t.Helper()
	_, stderr, err := executor.New().Run(ctx, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v (stderr: %s)", name, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
}

func RequireAvailable(t *testing.T, ctx context.Context, p provider.Provider, image string) {
	t.Helper()
	available, err := p.Available(ctx)
	if err != nil {
		t.Fatalf("%s Available() error: %v", p.Name(), err)
	}
	if !available {
		t.Fatalf("%s provider should be available in %s", p.Name(), image)
	}
}

func RequireInstalled(t *testing.T, ctx context.Context, p provider.Provider, pkg string) string {
	t.Helper()
	installed, version, err := p.IsInstalled(ctx, toolFor(p, pkg))
	if err != nil {
		t.Fatalf("%s IsInstalled(%s) error: %v", p.Name(), pkg, err)
	}
	if !installed {
		t.Fatalf("%s should report %s installed", p.Name(), pkg)
	}
	if strings.TrimSpace(version) == "" {
		t.Fatalf("%s IsInstalled(%s) returned empty version", p.Name(), pkg)
	}
	return version
}

// RequireMissing verifies a fake package is absent without surfacing a provider error.
func RequireMissing(t *testing.T, ctx context.Context, p provider.Provider, pkg string) {
	t.Helper()
	installed, _, err := p.IsInstalled(ctx, toolFor(p, pkg))
	if err != nil {
		t.Fatalf("%s IsInstalled(%s) unexpected error: %v", p.Name(), pkg, err)
	}
	if installed {
		t.Fatalf("%s should not report fake package %s installed", p.Name(), pkg)
	}
}

func RequireInstalledMap(t *testing.T, ctx context.Context, p provider.Provider, pkg string) {
	t.Helper()
	checker, ok := p.(provider.BulkChecker)
	if !ok {
		t.Fatalf("%s does not implement provider.BulkChecker", p.Name())
	}
	m, err := checker.InstalledMap(ctx)
	if err != nil {
		t.Fatalf("%s InstalledMap() error: %v", p.Name(), err)
	}
	if got := strings.TrimSpace(m[strings.ToLower(pkg)]); got == "" {
		t.Fatalf("%s InstalledMap() missing %s version; map has %d entries", p.Name(), pkg, len(m))
	}
}

func RequireListInstalled(t *testing.T, ctx context.Context, p provider.Provider, pkg string) {
	t.Helper()
	tools, err := p.ListInstalled(ctx)
	if err != nil {
		t.Fatalf("%s ListInstalled() error: %v", p.Name(), err)
	}
	for _, tool := range tools {
		if strings.EqualFold(tool.Tool.Name, pkg) && tool.Tool.Provider == p.Name() && strings.TrimSpace(tool.Version) != "" {
			return
		}
	}
	t.Fatalf("%s ListInstalled() did not include %s with provider %s", p.Name(), pkg, p.Name())
}

func RequireDescription(t *testing.T, ctx context.Context, p provider.Provider, pkg string) {
	t.Helper()
	describer, ok := p.(provider.Descriptor)
	if !ok {
		t.Fatalf("%s does not implement provider.Descriptor", p.Name())
	}
	desc, err := describer.Describe(ctx, toolFor(p, pkg))
	if err != nil {
		t.Fatalf("%s Describe(%s) error: %v", p.Name(), pkg, err)
	}
	if strings.TrimSpace(desc) == "" {
		t.Fatalf("%s Describe(%s) returned empty description", p.Name(), pkg)
	}
}

func RequireBulkDescription(t *testing.T, ctx context.Context, p provider.Provider, pkg string) {
	t.Helper()
	describer, ok := p.(provider.BulkDescriber)
	if !ok {
		t.Fatalf("%s does not implement provider.BulkDescriber", p.Name())
	}
	bulk, err := describer.BulkDescribe(ctx, []provider.Tool{toolFor(p, pkg)})
	if err != nil {
		t.Fatalf("%s BulkDescribe(%s) error: %v", p.Name(), pkg, err)
	}
	if strings.TrimSpace(bulk[pkg]) == "" {
		t.Fatalf("%s BulkDescribe() missing %s description: %#v", p.Name(), pkg, bulk)
	}
}

func ExercisePackageLifecycle(t *testing.T, ctx context.Context, p provider.Provider, pkg string) {
	t.Helper()
	tool := toolFor(p, pkg)
	alreadyInstalled, _, err := p.IsInstalled(ctx, tool)
	if err != nil {
		t.Fatalf("%s preflight IsInstalled(%s) error: %v", p.Name(), pkg, err)
	}
	if alreadyInstalled {
		t.Skipf("%s lifecycle package %s is already installed in the base image", p.Name(), pkg)
	}

	if err := p.Install(ctx, tool); err != nil {
		t.Fatalf("%s Install(%s) error: %v", p.Name(), pkg, err)
	}
	installed := true
	defer func() {
		if installed {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			if err := p.Uninstall(cleanupCtx, tool); err != nil {
				t.Logf("%s cleanup Uninstall(%s) error: %v", p.Name(), pkg, err)
			}
		}
	}()

	RequireInstalled(t, ctx, p, pkg)
	RequireInstalledMap(t, ctx, p, pkg)
	RequireListInstalled(t, ctx, p, pkg)
	RequireDescription(t, ctx, p, pkg)
	RequireBulkDescription(t, ctx, p, pkg)

	if err := p.Upgrade(ctx, tool); err != nil {
		t.Fatalf("%s Upgrade(%s) error: %v", p.Name(), pkg, err)
	}
	if err := p.Uninstall(ctx, tool); err != nil {
		t.Fatalf("%s Uninstall(%s) error: %v", p.Name(), pkg, err)
	}
	installed = false
	RequireMissing(t, ctx, p, pkg)
}

// System package managers always require privilege for mutations.
func RequirePrivilegePlan(t *testing.T, ctx context.Context, p provider.Provider, action provider.PrivilegeAction, pkg string) {
	t.Helper()
	planner, ok := p.(provider.PrivilegePlanner)
	if !ok {
		t.Fatalf("%s does not implement provider.PrivilegePlanner", p.Name())
	}
	plan, err := planner.PrivilegePlan(ctx, action, toolFor(p, pkg))
	if err != nil {
		t.Fatalf("%s PrivilegePlan(%s, %s) error: %v", p.Name(), action, pkg, err)
	}
	if !plan.RequiresPrivilege() {
		t.Fatalf("%s PrivilegePlan(%s, %s) = %+v, want RequiresPrivilege=true", p.Name(), action, pkg, plan)
	}
}

func toolFor(p provider.Provider, pkg string) provider.Tool {
	return provider.Tool{Name: pkg, Provider: p.Name(), Package: pkg}
}

func requireContainerGuard(t *testing.T, providerName string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Fatalf("pmcontainer tests must run inside disposable Linux containers, not GOOS=%s", runtime.GOOS)
	}
	if os.Getenv("OMNI_PMCONTAINER") != "1" {
		t.Fatal("pmcontainer tests are disabled outside make test-package-managers; refusing to touch the live package manager")
	}
	if got := os.Getenv("OMNI_PMCONTAINER_PROVIDER"); got != providerName {
		t.Fatalf("pmcontainer provider guard = %q, want %q", got, providerName)
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("pmcontainer tests require a Docker container marker; refusing to touch the live package manager")
	}
}
