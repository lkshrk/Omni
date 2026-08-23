package apm

import (
	"context"

	commandexec "github.com/lkshrk/omni/internal/executor"
)

func runInDir(ctx context.Context, runner commandexec.Executor, env []string, dir, name string, args ...string) (string, string, error) {
	return commandexec.RunInDirWithEnv(ctx, runner, dir, env, name, args...)
}
