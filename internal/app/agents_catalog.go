package app

import (
	"os/exec"

	"github.com/lkshrk/omni/internal/agent"
)

// AgentInfo — App-facing alias; target identity and detection live in the agent module.
type AgentInfo = agent.Target

func mustAgentRegistry(registry *agent.Registry, err error) *agent.Registry {
	if err != nil {
		panic(err)
	}
	return registry
}

func newAgentRegistry(opts ...agent.Option) *agent.Registry {
	return mustAgentRegistry(agent.NewRegistry(opts...))
}

func (a *App) agentRegistry() *agent.Registry {
	if a.agentTargets == nil {
		a.initAgentTargets()
	}
	return a.agentTargets
}

func InstalledAgents(home string) []AgentInfo {
	return newAgentRegistry().Installed(home)
}

func (a *App) installedAgents(home string) []AgentInfo {
	return a.agentRegistry().Installed(home)
}

// lookPath remains an app seam for agent CLI adapters and doctor checks.
var lookPath = exec.LookPath
