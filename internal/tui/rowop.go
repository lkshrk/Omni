package tui

import (
	"strconv"
	"strings"
)

func (m *Model) startAgentsOp(key string) {
	m.agentsOpKey = key
}

// Every agents/mcp/plugin msg handler that completes an op started via startAgentsOp must call this exactly once, so a stuck spinner cannot survive an error path that forgets it.
func (m *Model) clearAgentsOp() {
	m.agentsOpKey = ""
}

// One op can fan reloads out to several sections (a plugin install also reloads marketplace rows), so whichever reload lands first must not kill the op row's spinner while its own section is still stale.
func (m *Model) clearAgentsOpFor(section agentsSection) {
	if strings.HasPrefix(m.agentsOpKey, strconv.Itoa(int(section))+"\x00") {
		m.agentsOpKey = ""
	}
}
