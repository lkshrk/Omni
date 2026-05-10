//go:build pmcontainer

package apt_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/apt"
	"github.com/lkshrk/omni/internal/provider/pmtest"
)

func TestAPTProvider_DebianSlimUseCases(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "apt")
	defer cancel()

	pmtest.Run(t, ctx, "apt-get", "update")
	p := apt.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "debian:bookworm-slim")
	pmtest.RequireInstalled(t, ctx, p, "base-files")
	pmtest.RequireDescription(t, ctx, p, "base-files")
	pmtest.RequireBulkDescription(t, ctx, p, "base-files")
	pmtest.RequireMissing(t, ctx, p, "omni-fake-pkg-zzz-apt")
	pmtest.RequirePrivilegePlan(t, ctx, p, provider.PrivilegeActionInstall, "hello")
	pmtest.ExercisePackageLifecycle(t, ctx, p, "hello")
}
