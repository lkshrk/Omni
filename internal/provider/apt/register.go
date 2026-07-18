package apt

import (
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// init registers this concrete provider's factory so internal/app can build it
// without importing this package by name. See provider.RegisterConcrete.
func init() {
	provider.RegisterConcrete("apt", func(e executor.Executor) provider.Provider { return New(e) })
}
