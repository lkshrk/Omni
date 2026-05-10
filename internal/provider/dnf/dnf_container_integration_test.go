//go:build pmcontainer

package dnf_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/dnf"
	"github.com/lkshrk/omni/internal/provider/pmtest"
)

func TestDNFProvider_FedoraUseCases(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "dnf")
	defer cancel()

	p := dnf.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "fedora:42")
	pmtest.RequireMissing(t, ctx, p, "omni-fake-pkg-zzz-dnf")
	pmtest.RequirePrivilegePlan(t, ctx, p, provider.PrivilegeActionInstall, "tree")
	pmtest.ExercisePackageLifecycle(t, ctx, p, "tree")
}
