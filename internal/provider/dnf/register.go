package dnf

import (
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func init() {
	provider.RegisterConcrete("dnf", func(e executor.Executor) provider.Provider { return New(e) })
}
