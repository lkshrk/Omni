package tui

// startAgentsOp records key as the agents-all row with an op in flight.
func (m *Model) startAgentsOp(key string) {
	m.agentsOpKey = key
}

// clearAgentsOp clears the in-flight agents-all row op key. Every
// agents/mcp/plugin msg handler that completes an op started via
// startAgentsOp (success or error) must call this exactly once, so a
// stuck spinner can't survive an error path that forgets to clear it.
func (m *Model) clearAgentsOp() {
	m.agentsOpKey = ""
}
