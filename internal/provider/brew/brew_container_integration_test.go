//go:build pmcontainer

package brew_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/brew"
	"github.com/lkshrk/omni/internal/provider/pmtest"
)

func TestBrewProvider_LinuxUseCases(t *testing.T) {
	ctx, cancel := pmtest.Context(t, "brew")
	defer cancel()

	p := brew.New(executor.New())
	pmtest.RequireAvailable(t, ctx, p, "homebrew/brew:latest")
	pmtest.RequireMissing(t, ctx, p, "omni-fake-pkg-zzz-brew")
	pmtest.ExercisePackageLifecycle(t, ctx, p, "hello")
}
