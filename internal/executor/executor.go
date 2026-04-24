package executor

import "context"

// Executor abstracts shell command execution.
// Inject a mock in tests to avoid touching the real system.
type Executor interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}
