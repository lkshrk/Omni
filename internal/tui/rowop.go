package tui

import (
	"strconv"
	"strings"
)

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

// clearAgentsOpFor clears the in-flight row op only when it belongs to
// section (agentsRowRunKey's first component). One op can fan out reloads to
// several sections — e.g. a plugin install also reloads marketplace rows —
// and whichever reload lands first must not kill the op row's spinner while
// its own section is still stale.
func (m *Model) clearAgentsOpFor(section agentsSection) {
	if strings.HasPrefix(m.agentsOpKey, strconv.Itoa(int(section))+"\x00") {
		m.agentsOpKey = ""
	}
}
