//go:build pmcontainer

package apk_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/apk"
	"github.com/lkshrk/omni/internal/provider/pmtest"
)

func TestAPKProvider_AlpineUseCases(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "apk")
	defer cancel()

	p := apk.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "alpine:3.20")
	pmtest.RequireInstalled(t, ctx, p, "busybox")
	pmtest.RequireInstalledMap(t, ctx, p, "busybox")
	pmtest.RequireListInstalled(t, ctx, p, "busybox")
	pmtest.RequireDescription(t, ctx, p, "busybox")
	pmtest.RequireBulkDescription(t, ctx, p, "busybox")
	pmtest.RequireMissing(t, ctx, p, "omni-fake-pkg-zzz-apk")
	pmtest.RequirePrivilegePlan(t, ctx, p, provider.PrivilegeActionInstall, "tree")
	pmtest.ExercisePackageLifecycle(t, ctx, p, "tree")
}
