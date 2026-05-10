//go:build pmcontainer

package pacman_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/pacman"
	"github.com/lkshrk/omni/internal/provider/pmtest"
)

func TestPacmanProvider_ArchUseCases(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "pacman")
	defer cancel()

	pmtest.Run(t, ctx, "sed", "-i", "s/^#DisableSandboxSyscalls/DisableSandboxSyscalls/", "/etc/pacman.conf")
	pmtest.Run(t, ctx, "pacman", "-Sy", "--noconfirm")

	p := pacman.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "archlinux/archlinux:base")
	pmtest.RequireMissing(t, ctx, p, "omni-fake-pkg-zzz-pacman")
	pmtest.RequirePrivilegePlan(t, ctx, p, provider.PrivilegeActionInstall, "tree")
	pmtest.ExercisePackageLifecycle(t, ctx, p, "tree")
}
