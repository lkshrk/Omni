package app

import "github.com/lkshrk/omni/internal/agent"

// McpAdapter keeps app callers stable while MCP ownership lives in agent.
type McpAdapter = agent.McpAdapter

// InstalledMcpServer keeps app callers stable while MCP ownership lives in agent.
type InstalledMcpServer = agent.InstalledMcpServer
