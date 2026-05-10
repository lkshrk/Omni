//go:build pmcontainer

package zypper_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/pmtest"
	"github.com/lkshrk/omni/internal/provider/zypper"
)

func TestZypperProvider_OpenSUSEUseCases(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "zypper")
	defer cancel()

	p := zypper.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "opensuse/leap:15.6")
	pmtest.RequireMissing(t, ctx, p, "omni-fake-pkg-zzz-zypper")
	pmtest.RequirePrivilegePlan(t, ctx, p, provider.PrivilegeActionInstall, "tree")
	pmtest.ExercisePackageLifecycle(t, ctx, p, "tree")
}
