package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

const agentsNativeTestHost = "testhost"

var errAgentsNativeTest = errors.New("native op failed")

// Cursor layout: packages 0-3, MCP 4, LSP 5, natives 6-8.
const (
	agentsNativeAdoptableIdx = 6
	agentsNativeBlockedIdx   = 7
	agentsNativeIgnoredIdx   = 8
)

func agentsNativeModel(t *testing.T) (Model, *executor.MatchMockExecutor) {
	t.Helper()
	m, mock := agentsRowOpModel(t)
	m.height = 60
	m.agentsLSPRows = []app.AgentsServiceRow{{Name: "gopls", Detail: "gopls", Status: app.AgentsPackageInstalled}}
	m.agentsNativeRows = []app.AgentsNativeRow{
		{
			Target:      "claude",
			Kind:        "plugin",
			Identity:    "adoptme@mkt",
			Source:      "~/.claude/plugins/config.json",
			IgnoreHost:  agentsNativeTestHost,
			Adoptable:   true,
			InstallRoot: "/roots/adoptme",
		},
		{
			Target:     "codex",
			Kind:       "mcp",
			Identity:   "blocked-server",
			Source:     "~/.codex/config.toml",
			IgnoreHost: agentsNativeTestHost,
			Reason:     "local command paths are not importable",
		},
		{
			Target:     "claude",
			Kind:       "plugin",
			Identity:   "kept@mkt",
			Source:     "~/.claude/plugins/config.json",
			IgnoreHost: agentsNativeTestHost,
			Ignored:    true,
			Reason:     "hand managed",
		},
	}
	return m, mock
}

// Driving from the binding keeps these tests honest when a key moves.
func agentsPressBinding(t *testing.T, m *Model, binding key.Binding) []tea.Cmd {
	t.Helper()
	keys := binding.Keys()
	if len(keys) != 1 || len([]rune(keys[0])) != 1 {
		t.Fatalf("binding keys = %v, want one single-rune key", keys)
	}
	return agentsPressRowKey(t, m, keys[0])
}

func TestAgentsNativeSectionListsIgnoredRowsInsteadOfHidingThem(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsCursor = agentsNativeIgnoredIdx
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	for _, want := range []string{
		agentsNativeSectionTitle,
		"adoptme@mkt",
		"blocked-server",
		"kept@mkt",
		"state: ignored — hand managed",
		"read from: ~/.claude/plugins/config.json",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("native section missing %q:\n%s", want, view)
		}
	}
	if got := agentsNativeDetail(m.agentsNativeRows[2]); got != "plugin · ignored" {
		t.Fatalf("ignored marker = %q", got)
	}
}

func TestAgentsNativeRowDetailsExplainRetainedAndUndeclaredRows(t *testing.T) {
	m, _ := agentsNativeModel(t)
	for name, tc := range map[string]struct {
		cursor int
		want   string
	}{
		"undeclared": {agentsNativeAdoptableIdx, "state: not declared in the host template"},
		"retained":   {agentsNativeBlockedIdx, "state: retained — local command paths are not importable"},
	} {
		t.Run(name, func(t *testing.T) {
			m.agentsCursor = tc.cursor
			view := stripANSIEscapeSequences(m.viewSkillsBody())
			if !strings.Contains(view, tc.want) {
				t.Fatalf("details missing %q:\n%s", tc.want, view)
			}
		})
	}
}

func TestAgentsNativeSectionIsOmittedWithoutRows(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsNativeRows = nil
	view := m.viewSkillsBody()
	if strings.Contains(view, agentsNativeSectionTitle) {
		t.Fatalf("empty native section rendered:\n%s", view)
	}
	if !strings.Contains(view, "MCP servers") {
		t.Fatalf("other sections dropped with the natives:\n%s", view)
	}
}

func TestAgentsNativeSectionIsLastAndEveryCursorIndexResolves(t *testing.T) {
	m, _ := agentsNativeModel(t)
	if got, want := m.agentsRowCount(), 9; got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
	if got, want := m.agentsTotalRowCount(), 9; got != want {
		t.Fatalf("total row count = %d, want %d", got, want)
	}

	for _, tc := range []struct {
		idx  int
		kind agentsRowKind
		name string
	}{
		{0, agentsRowPackage, "floating"},
		{3, agentsRowPackage, "stray"},
		{4, agentsRowService, "mcp-one"},
		{5, agentsRowService, "gopls"},
		{agentsNativeAdoptableIdx, agentsRowNative, "adoptme@mkt"},
		{agentsNativeBlockedIdx, agentsRowNative, "blocked-server"},
		{agentsNativeIgnoredIdx, agentsRowNative, "kept@mkt"},
	} {
		m.agentsCursor = tc.idx
		row, ok := m.agentsSelectedRow()
		if !ok || row.kind != tc.kind {
			t.Fatalf("index %d: kind = %v ok = %v", tc.idx, row.kind, ok)
		}
		got := row.pkg.Name
		switch tc.kind {
		case agentsRowService:
			got = row.service.Name
		case agentsRowNative:
			got = row.native.Identity
		}
		if got != tc.name {
			t.Fatalf("index %d: row = %q, want %q", tc.idx, got, tc.name)
		}
	}

	view := stripANSIEscapeSequences(m.viewSkillsBody())
	last := -1
	for _, title := range []string{"Packages", "MCP servers", "LSP servers", agentsNativeSectionTitle} {
		at := strings.Index(view, title)
		if at <= last {
			t.Fatalf("section %q renders at %d, out of cursor order:\n%s", title, at, view)
		}
		last = at
	}
}

func TestAgentsNativeCursorIsClampedWhenRowsShrink(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsCursor = agentsNativeIgnoredIdx
	m.agentsNativeGen = 4
	shrunk := drive(m, agentsNativeRowsMsg{gen: 4, rows: m.agentsNativeRows[:1]})
	if len(shrunk.agentsNativeRows) != 1 {
		t.Fatalf("rows = %#v", shrunk.agentsNativeRows)
	}
	if shrunk.agentsCursor != 6 {
		t.Fatalf("cursor = %d, want the last surviving row", shrunk.agentsCursor)
	}
}

func TestAgentsNativeRowsMsgAppliesOnlyTheCurrentGeneration(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsNativeGen = 2
	stale := drive(m, agentsNativeRowsMsg{gen: 1, rows: nil})
	if len(stale.agentsNativeRows) != 3 {
		t.Fatalf("stale generation replaced the rows: %#v", stale.agentsNativeRows)
	}
	failed := drive(m, agentsNativeRowsMsg{gen: 2, rows: nil, err: errAgentsNativeTest})
	if len(failed.agentsNativeRows) != 3 {
		t.Fatalf("failed load wiped the rows: %#v", failed.agentsNativeRows)
	}
	fresh := drive(m, agentsNativeRowsMsg{gen: 2, rows: []app.AgentsNativeRow{{Target: "claude", Kind: "plugin", Identity: "new@mkt"}}})
	if len(fresh.agentsNativeRows) != 1 || fresh.agentsNativeRows[0].Identity != "new@mkt" {
		t.Fatalf("fresh rows not applied: %#v", fresh.agentsNativeRows)
	}
}

func TestAgentsNativeRemoveConfirmsBeforeCallingTheClient(t *testing.T) {
	m, mock := agentsNativeModel(t)
	m.agentsCursor = agentsNativeAdoptableIdx

	agentsPressBinding(t, &m, m.keys.AgentsRemove)
	if m.agentsConfirmIdx != agentsNativeAdoptableIdx || len(mock.Calls) != 0 {
		t.Fatalf("first x removed without confirming: idx=%d calls=%#v", m.agentsConfirmIdx, mock.Calls)
	}
	removeKey, removeDesc := m.keys.AgentsRemove.Help().Key, m.keys.AgentsRemove.Help().Desc
	view := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(view, listHintPrefix()+removeKey+" confirm remove") {
		t.Fatalf("confirm hint is not inline on the selected row:\n%s", view)
	}
	if strings.Contains(view, removeKey+" "+removeDesc) || strings.Contains(m.statusMsg, "confirm remove") {
		t.Fatalf("normal row hint or footer status survived confirmation: status=%q\n%s", m.statusMsg, view)
	}
	if !m.hasActiveConfirmation() {
		t.Fatal("native confirm is invisible to hasActiveConfirmation")
	}

	timedOut := drive(m, confirmTimeoutMsg{gen: m.confirmGen})
	if timedOut.agentsConfirmIdx != -1 || timedOut.hasActiveConfirmation() {
		t.Fatalf("timeout did not clear the native confirm: %d", timedOut.agentsConfirmIdx)
	}

	cmds := agentsPressBinding(t, &m, m.keys.AgentsRemove)
	if m.agentsConfirmIdx != -1 {
		t.Fatalf("confirm survived the second press: %d", m.agentsConfirmIdx)
	}
	var removed *agentsNativeOpMsg
	for _, msg := range runBatchCmd(tea.Batch(cmds...)) {
		if op, ok := msg.(agentsNativeOpMsg); ok {
			removed = &op
		}
	}
	if removed == nil || !removed.removed || removed.identity != "adoptme@mkt" || removed.err != nil {
		t.Fatalf("remove message = %#v", removed)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "claude" || strings.Join(mock.Calls[0].Args, " ") != "plugin uninstall adoptme@mkt" {
		t.Fatalf("client call = %#v", mock.Calls)
	}
}

func TestAgentsNativeRemoveRefusesAnIgnoredRow(t *testing.T) {
	m, mock := agentsNativeModel(t)
	m.agentsCursor = agentsNativeIgnoredIdx
	cmds := agentsPressBinding(t, &m, m.keys.AgentsRemove)
	runBatchCmd(tea.Batch(cmds...))
	if m.agentsConfirmIdx != -1 {
		t.Fatalf("ignored row armed a removal confirm: %d", m.agentsConfirmIdx)
	}
	if !strings.Contains(m.statusMsg, "kept@mkt is ignored — press i to unignore before removing") {
		t.Fatalf("status = %q", m.statusMsg)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("ignored row reached the client: %#v", mock.Calls)
	}
}

func TestAgentsNativeAdoptRefusesIgnoredAndUnadoptableRows(t *testing.T) {
	for name, tc := range map[string]struct {
		cursor int
		want   string
	}{
		"ignored":     {agentsNativeIgnoredIdx, "kept@mkt is ignored — press i to unignore before adopting"},
		"unadoptable": {agentsNativeBlockedIdx, "blocked-server cannot be adopted: local command paths are not importable"},
	} {
		t.Run(name, func(t *testing.T) {
			m, mock := agentsNativeModel(t)
			m.agentsCursor = tc.cursor
			cmds := agentsPressBinding(t, &m, m.keys.AgentsNativeAdopt)
			runBatchCmd(tea.Batch(cmds...))
			if !strings.Contains(m.statusMsg, tc.want) {
				t.Fatalf("status = %q, want %q", m.statusMsg, tc.want)
			}
			if len(mock.Calls) != 0 || m.agentsRegistryMode {
				t.Fatalf("blocked adopt did work: calls=%#v registry=%v", mock.Calls, m.agentsRegistryMode)
			}
		})
	}

	unclassified := app.AgentsNativeRow{Target: "claude", Kind: "plugin", Identity: "mystery@mkt"}
	if got := agentsNativeAdoptBlocked(unclassified); !strings.Contains(got, "the migration classifier does not import it") {
		t.Fatalf("reasonless block = %q", got)
	}
}

func TestAgentsNativeAdoptDispatchesForAnAdoptableRow(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsCursor = agentsNativeAdoptableIdx
	cmds := agentsPressBinding(t, &m, m.keys.AgentsNativeAdopt)
	if m.agentsRegistryMode {
		t.Fatal("adopt opened the registry")
	}
	var adopted *agentsNativeOpMsg
	for _, msg := range runBatchCmd(tea.Batch(cmds...)) {
		if op, ok := msg.(agentsNativeOpMsg); ok {
			adopted = &op
		}
	}
	if adopted == nil || !adopted.adopted || adopted.identity != "adoptme@mkt" {
		t.Fatalf("adopt message = %#v", adopted)
	}
}

func TestAgentsNativeIgnoreTogglesTheRecordedEntry(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsCursor = agentsNativeAdoptableIdx

	cmds := agentsPressRowKey(t, &m, "i")
	var op *agentsNativeOpMsg
	for _, msg := range runBatchCmd(tea.Batch(cmds...)) {
		if got, ok := msg.(agentsNativeOpMsg); ok {
			op = &got
		}
	}
	if op == nil || op.err != nil || !op.ignored || op.identity != "adoptme@mkt" {
		t.Fatalf("ignore message = %#v", op)
	}
	entries, err := m.app.AgentIgnoreEntries()
	if err != nil {
		t.Fatalf("AgentIgnoreEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "adoptme@mkt" || entries[0].Host != agentsNativeTestHost {
		t.Fatalf("entries = %#v", entries)
	}

	m.agentsNativeRows[0].Ignored = true
	cmds = agentsPressRowKey(t, &m, "i")
	op = nil
	for _, msg := range runBatchCmd(tea.Batch(cmds...)) {
		if got, ok := msg.(agentsNativeOpMsg); ok {
			op = &got
		}
	}
	if op == nil || op.err != nil || op.ignored {
		t.Fatalf("unignore message = %#v", op)
	}
	if entries, err = m.app.AgentIgnoreEntries(); err != nil || len(entries) != 0 {
		t.Fatalf("entries after unignore = %#v (err %v)", entries, err)
	}
}

func TestAgentsNativeIgnoreClearsAPendingRemoveConfirm(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.agentsCursor = agentsNativeAdoptableIdx
	agentsPressRowKey(t, &m, "x")
	agentsPressRowKey(t, &m, "i")
	if m.agentsConfirmIdx != -1 {
		t.Fatalf("ignore left the remove confirm armed: %d", m.agentsConfirmIdx)
	}
}

func TestAgentsNativeIgnoreKeyNeedsANativeRow(t *testing.T) {
	for name, tc := range map[string]struct {
		cursor int
		hidden bool
	}{
		"package": {0, false},
		"service": {4, false},
		"hidden":  {agentsNativeAdoptableIdx, true},
	} {
		t.Run(name, func(t *testing.T) {
			m, mock := agentsNativeModel(t)
			m.agentsCursor = tc.cursor
			m.cursorHidden = tc.hidden
			cmds := agentsPressBinding(t, &m, m.keys.AgentsNativeIgnore)
			runBatchCmd(tea.Batch(cmds...))
			if !strings.Contains(m.statusMsg, "select a row under "+agentsNativeSectionTitle+" first") {
				t.Fatalf("status = %q", m.statusMsg)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("i ran a command off a native row: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsNativeRemoveKeyStillFallsThroughToPackageUninstall(t *testing.T) {
	m, mock := agentsNativeModel(t)
	m.agentsCursor = 0
	agentsPressRowKey(t, &m, "x")
	if m.agentsConfirmIdx != 0 || strings.Contains(m.statusMsg, agentsNativeSectionTitle) {
		t.Fatalf("x on a package row was swallowed by the native handler: idx=%d status=%q", m.agentsConfirmIdx, m.statusMsg)
	}
	cmds := agentsPressRowKey(t, &m, "x")
	if m.apmCommand != "apm uninstall -g acme/floating" {
		t.Fatalf("command = %q", m.apmCommand)
	}
	runBatchCmd(tea.Batch(cmds...))
	if len(mock.Calls) == 0 || mock.Calls[len(mock.Calls)-1].Name != "apm" {
		t.Fatalf("package uninstall never reached apm: %#v", mock.Calls)
	}

	m, _ = agentsNativeModel(t)
	m.agentsCursor = 4
	agentsPressRowKey(t, &m, "x")
	if !strings.Contains(m.statusMsg, agentsServiceOpStatus) {
		t.Fatalf("x on an MCP row = %q", m.statusMsg)
	}
}

func TestAgentsAddKeyStillOpensTheRegistryBesideNativeRows(t *testing.T) {
	for name, cursor := range map[string]int{"package": 0, "service": 4, "native": agentsNativeAdoptableIdx} {
		t.Run(name, func(t *testing.T) {
			m, _ := agentsNativeModel(t)
			m.agentsCursor = cursor
			m.agentsReadiness = app.AgentsReadiness{State: app.AgentsReadinessReady}
			agentsPressBinding(t, &m, m.keys.AgentsAdd)
			if !m.agentsRegistryMode {
				t.Fatalf("a did not open the registry: status=%q", m.statusMsg)
			}
			if strings.Contains(m.statusMsg, agentsNativeSectionTitle) {
				t.Fatalf("a was swallowed by the native handler: %q", m.statusMsg)
			}
		})
	}
}

func TestAgentsNativeHintsFollowRowState(t *testing.T) {
	m, _ := agentsNativeModel(t)
	ignore := m.keys.AgentsNativeIgnore.Help().Key
	adopt := m.keys.AgentsNativeAdopt.Help().Key
	remove := m.keys.AgentsRemove.Help().Key
	for name, tc := range map[string]struct {
		cursor int
		want   []string
		absent []string
	}{
		"adoptable":   {agentsNativeAdoptableIdx, []string{ignore, adopt, remove}, nil},
		"unadoptable": {agentsNativeBlockedIdx, []string{ignore, remove}, []string{adopt}},
		"ignored":     {agentsNativeIgnoredIdx, []string{ignore}, []string{adopt, remove}},
	} {
		t.Run(name, func(t *testing.T) {
			m.agentsCursor = tc.cursor
			var keys []string
			for _, item := range agentsNativeHintItems(m) {
				keys = append(keys, item.key)
			}
			for _, want := range tc.want {
				if !slices.Contains(keys, want) {
					t.Fatalf("hints %v missing %q", keys, want)
				}
			}
			for _, absent := range tc.absent {
				if slices.Contains(keys, absent) {
					t.Fatalf("hints %v offer %q", keys, absent)
				}
			}
		})
	}

	m.agentsCursor = 0
	if items := agentsNativeHintItems(m); items != nil {
		t.Fatalf("package row got native hints: %#v", items)
	}
}

func TestAgentsNativeOpMsgReportsEveryOutcome(t *testing.T) {
	m, _ := agentsNativeModel(t)
	for name, tc := range map[string]struct {
		msg  agentsNativeOpMsg
		want string
	}{
		"failed":   {agentsNativeOpMsg{err: errAgentsNativeTest, identity: "adoptme@mkt"}, "⚠ native op failed"},
		"adopted":  {agentsNativeOpMsg{adopted: true, identity: "adoptme@mkt", detail: "Declared claude plugin adoptme@mkt in apm.yml.\nNext: omni agents sync\n"}, "Declared claude plugin adoptme@mkt in apm.yml."},
		"removed":  {agentsNativeOpMsg{removed: true, identity: "adoptme@mkt"}, "removed adoptme@mkt"},
		"ignored":  {agentsNativeOpMsg{ignored: true, identity: "kept@mkt"}, "ignoring kept@mkt"},
		"restored": {agentsNativeOpMsg{identity: "kept@mkt"}, "no longer ignoring kept@mkt"},
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(m, tc.msg)
			if !strings.Contains(got.statusMsg, tc.want) {
				t.Fatalf("status = %q, want %q", got.statusMsg, tc.want)
			}
		})
	}

	if got := agentsAdoptStatusLine("  \n", "adoptme@mkt"); got != "declared adoptme@mkt" {
		t.Fatalf("empty detail line = %q", got)
	}
}

func TestAgentsNativeRowsAreFiltered(t *testing.T) {
	m, _ := agentsNativeModel(t)
	m.openAgentsFilter()
	for name, tc := range map[string]struct {
		query string
		want  []string
	}{
		"identity": {"adoptme", []string{"adoptme@mkt"}},
		"target":   {"CODEX", []string{"blocked-server"}},
		"kind":     {"mcp", []string{"blocked-server"}},
		"source":   {"codex/config.toml", []string{"blocked-server"}},
	} {
		t.Run(name, func(t *testing.T) {
			m.filter.SetValue(tc.query)
			var got []string
			for _, row := range m.agentsVisibleNatives() {
				got = append(got, row.Identity)
			}
			if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
				t.Fatalf("visible natives = %v, want %v", got, tc.want)
			}
		})
	}

	m.filter.SetValue("adoptme")
	if got := m.agentsRowCount(); got != 1 {
		t.Fatalf("filtered row count = %d", got)
	}
	m.agentsCursor = 0
	row, ok := m.agentsSelectedRow()
	if !ok || row.kind != agentsRowNative || row.native.Identity != "adoptme@mkt" {
		t.Fatalf("filtered selection = %#v ok=%v", row, ok)
	}
}
