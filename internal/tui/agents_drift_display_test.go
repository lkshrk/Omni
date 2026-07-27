package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

// A package drifted on one agent is still linked on the others, so an agent cell built from the linked agents names every agent except the one the row is about.
func TestAgentsRow_DriftedSkillShowsTheDriftedAgent(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{{
		Name:      "caveman",
		Source:    "github.com/foo/caveman",
		Installed: true,
		PerAgentStatus: map[string]app.SkillStatus{
			"claude-code": app.SkillStatusInstalled,
			"codex":       app.SkillStatusDrifted,
		},
	}}, nil, nil)
	m.enabledAgents = []string{"claude-code", "codex"}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "caveman") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no caveman row rendered, got:\n%s", out)
	}
	if !strings.Contains(line, "codex") {
		t.Errorf("drifted row = %q, want the drifted agent (codex) in the agent cell", line)
	}
	if strings.Contains(line, "claude-code") {
		t.Errorf("drifted row = %q, want the drifted agent, not the still-linked claude-code", line)
	}
}

func TestAgentsRow_CleanSkillKeepsLinkedAgent(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{{
		Name:           "caveman",
		Source:         "github.com/foo/caveman",
		Installed:      true,
		PerAgentStatus: map[string]app.SkillStatus{"claude-code": app.SkillStatusInstalled},
	}}, nil, nil)
	m.enabledAgents = []string{"claude-code"}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "claude-code") {
		t.Errorf("clean row lost its linked agent, got:\n%s", out)
	}
}

// 'l use local' adopts the live registration, so both sides have to be readable on the row.
func TestAgentsRow_DriftedMcpDetailsShowBothSides(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{{
		Name:      "context7",
		Transport: "http",
		URL:       "https://manifest.example/mcp",
		Agents:    []string{"codex"},
		PerAgentStatus: map[string]app.McpStatus{
			"codex": app.McpStatusDrifted,
		},
		DriftFields: map[string][]string{"codex": {"url"}},
		DriftLive: map[string]app.InstalledMcpServer{
			"codex": {Name: "context7", Transport: "http", URL: "https://live.example/mcp"},
		},
	}}, nil)
	m.enabledAgents = []string{"codex"}

	var drifted agentsAllRow
	found := false
	for _, row := range agentsAllRowsList(m) {
		if row.mark == agentsMarkDrifted {
			drifted, found = row, true
		}
	}
	if !found {
		t.Fatal("fixture produced no drifted mcp row")
	}

	details := strings.Join(agentsRowDetailLines(m, drifted), "\n")
	details = stripANSIEscapeSequences(details)
	for _, want := range []string{
		"manifest:", "https://manifest.example/mcp",
		"codex:", "https://live.example/mcp",
		"differs: url",
	} {
		if !strings.Contains(details, want) {
			t.Errorf("drifted mcp details missing %q, got:\n%s", want, details)
		}
	}
}

func TestAgentsRow_DriftedMcpDetailsMarkAbsentFields(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{{
		Name:           "local-tools",
		Transport:      "stdio",
		Command:        "npx local-tools",
		Agents:         []string{"codex"},
		PerAgentStatus: map[string]app.McpStatus{"codex": app.McpStatusDrifted},
		DriftFields:    map[string][]string{"codex": {"transport", "command", "url"}},
		DriftLive: map[string]app.InstalledMcpServer{
			"codex": {Name: "local-tools", Transport: "http", URL: "https://live.example/mcp"},
		},
	}}, nil)
	m.enabledAgents = []string{"codex"}

	var drifted agentsAllRow
	for _, row := range agentsAllRowsList(m) {
		if row.mark == agentsMarkDrifted {
			drifted = row
		}
	}
	details := stripANSIEscapeSequences(strings.Join(agentsRowDetailLines(m, drifted), "\n"))
	if !strings.Contains(details, "command: —") {
		t.Errorf("live side should mark the missing command as absent, got:\n%s", details)
	}
	if !strings.Contains(details, "url: —") {
		t.Errorf("manifest side should mark the missing url as absent, got:\n%s", details)
	}
}

func TestAgentsRow_CleanMcpKeepsSummaryDetail(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{{
		Name:           "context7",
		Transport:      "http",
		URL:            "https://manifest.example/mcp",
		Agents:         []string{"codex"},
		PerAgentStatus: map[string]app.McpStatus{"codex": app.McpStatusInstalled},
	}}, nil)
	m.enabledAgents = []string{"codex"}

	var row agentsAllRow
	for _, e := range agentsAllRowsList(m) {
		if e.feature == agentsSectionMcp {
			row = e
		}
	}
	details := agentsRowDetailLines(m, row)
	if len(details) != 1 {
		t.Fatalf("clean mcp details = %#v, want the single summary line", details)
	}
	if !strings.Contains(stripANSIEscapeSequences(details[0]), "transport: http") {
		t.Errorf("clean mcp detail = %q, want the manifest summary", details[0])
	}
}

func driftedMcpUseLocalModel(url string) (Model, agentsAllRow) {
	m := agentsAllModel(nil, []app.McpServerRow{{
		Name:           "context7",
		Transport:      "http",
		URL:            "https://manifest.example/mcp",
		Agents:         []string{"codex"},
		PerAgentStatus: map[string]app.McpStatus{"codex": app.McpStatusDrifted},
		DriftFields:    map[string][]string{"codex": {"url"}},
		DriftLive: map[string]app.InstalledMcpServer{
			"codex": {Name: "context7", Transport: "http", URL: url},
		},
	}}, nil)
	m.enabledAgents = []string{"codex"}
	m.agentsResolveConfirm = true
	m.agentsResolveUseLocal = true

	var drifted agentsAllRow
	for _, row := range agentsAllRowsList(m) {
		if row.mark == agentsMarkDrifted {
			drifted = row
		}
	}
	return m, drifted
}

// The armed confirm is the last place to see what 'l' writes, so it has to name the value and not just the action.
func TestAgentsHints_ArmedUseLocalNamesTheAdoptedMcpValue(t *testing.T) {
	t.Parallel()
	m, drifted := driftedMcpUseLocalModel("https://DIFFERENT.example.com/mcp")

	hint := stripANSIEscapeSequences(renderHintItems(m.palette, "", agentsRowHints(m, drifted)))
	want := "press l again to use local (url: https://DIFFERENT.example.com/mcp)"
	if hint != want {
		t.Errorf("armed use-local hint = %q, want %q", hint, want)
	}
}

func TestAgentsHints_ArmedUseLocalClipsALongValueFromTheHead(t *testing.T) {
	t.Parallel()
	m, drifted := driftedMcpUseLocalModel("https://a-very-long-host.example.com/deeply/nested/mcp")
	m.width = 60

	hint := stripANSIEscapeSequences(renderHintItems(m.palette, "", agentsRowHints(m, drifted)))
	if !strings.Contains(hint, "…") || !strings.HasSuffix(hint, "/deeply/nested/mcp)") {
		t.Errorf("clipped hint = %q, want the tail kept behind a leading ellipsis", hint)
	}
	if lipgloss.Width(hint) > m.width {
		t.Errorf("clipped hint width = %d, want it to fit %d", lipgloss.Width(hint), m.width)
	}
}

// Skills' use-local keeps what is already installed rather than adopting a value, so its armed confirm stays bare.
func TestAgentsHints_ArmedUseLocalOnASkillStaysBare(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{{
		Name:           "caveman",
		Source:         "github.com/foo/caveman",
		PerAgentStatus: map[string]app.SkillStatus{"codex": app.SkillStatusDrifted},
	}}, nil, nil)
	m.enabledAgents = []string{"codex"}
	m.agentsResolveConfirm = true
	m.agentsResolveUseLocal = true

	var drifted agentsAllRow
	for _, row := range agentsAllRowsList(m) {
		if row.mark == agentsMarkDrifted {
			drifted = row
		}
	}
	hint := stripANSIEscapeSequences(renderHintItems(m.palette, "", agentsRowHints(m, drifted)))
	if hint != "press l again to confirm use local" {
		t.Errorf("skill armed use-local hint = %q, want the bare label", hint)
	}
}
