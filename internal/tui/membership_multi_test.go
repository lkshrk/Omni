package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// Skills (an agent-kind) may belong to any number of groups including several reusable ones; only dots cap reusable membership at one.

func TestRenderGroupMembershipPicker_MarksEveryMember(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.mode = viewGroupMembership
	m.cursor = 0
	m.pickerGroups = []string{"base", "work", "personal"}
	m.toolMemberships = map[string][]string{
		toolMembershipKey(m.selectedTool()): {"base", "personal"},
	}
	out := renderGroupMembershipPicker(m)
	if got := strings.Count(out, "[x]"); got != 2 {
		t.Fatalf("checked marks = %d, want 2 (base + personal):\n%s", got, out)
	}
	if got := strings.Count(out, "[ ]"); got != 1 {
		t.Fatalf("unchecked marks = %d, want 1 (work):\n%s", got, out)
	}
}

func TestRenderGroupMembershipPicker_FooterFitsOnOneLine(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.mode = viewGroupMembership
	m.pickerGroups = []string{"base", "work"}
	m.toolMemberships = map[string][]string{
		toolMembershipKey(m.selectedTool()): {"base"},
	}

	out := stripANSIEscapeSequences(renderGroupMembershipPicker(m))
	contentW := groupMembershipContentWidth(m)
	requiredW := lipgloss.Width(renderActionHintText(m.palette, membershipPickerActionItems(m)))
	if contentW < requiredW {
		t.Fatalf("membership content width = %d, footer requires %d", contentW, requiredW)
	}
	footer := renderedLineContaining(out, "enter confirm")
	if footer == "" {
		t.Fatalf("membership footer missing primary action:\n%s", out)
	}
	for _, want := range []string{"esc cancel", "space toggle", "enter confirm"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("membership footer wrapped %q off the action row:\n%s", want, out)
		}
	}
}

func TestRenderGroupMembershipPicker_FooterLayoutAcrossThemesAndWidths(t *testing.T) {
	t.Parallel()
	for _, isDark := range []bool{true, false} {
		for _, width := range []int{60, 90} {
			name := fmt.Sprintf("dark=%t/width=%d", isDark, width)
			t.Run(name, func(t *testing.T) {
				m := baseModel(threeTools())
				m.palette = buildPaletteFor(isDark)
				m.width = width
				m.mode = viewGroupMembership
				m.pickerMembershipKind = pickerMembershipTool
				m.pickerMembershipName = "git"
				m.pickerGroups = []string{"base", "work"}
				m.toolMemberships = map[string][]string{
					toolMembershipKey(m.selectedTool()): {"base"},
				}

				const paddingX = 2
				contentW := groupMembershipContentWidth(m)
				frame := popupFrame{
					Title:          groupMembershipPopupTitle(m),
					PaddingY:       1,
					PaddingX:       paddingX,
					Width:          popupFrameWidthForContent(contentW, paddingX),
					NoTitleDivider: true,
				}
				out := stripANSIEscapeSequences(renderPopupFrame(m.palette, renderGroupMembershipPicker(m), frame))
				assertLinesFitWidth(t, out, frame.Width)
				footer := renderedLineContaining(out, "enter confirm")
				for _, want := range []string{"esc cancel", "space toggle", "enter confirm"} {
					if !strings.Contains(footer, want) {
						t.Fatalf("footer missing %q on its action row:\n%s", want, out)
					}
				}
			})
		}
	}
}

// Every item kind shares selectGroupMembership but stores drafts in per-kind maps; only "dot" caps reusable membership at one.

func membershipToggleTestModel(t *testing.T, kind string) *Model {
	t.Helper()
	m := baseModel(nil)
	m.pickerMembershipKind = kind
	if kind == pickerMembershipDot {
		m.pickerMembershipName = "x"
		m.dotMemberships = map[string][]string{"x": {}}
	} else {
		m.pickerMembershipKey = "x"
		m.toolMemberships = map[string][]string{"x": {}}
	}
	return &m
}

func TestSelectGroupMembership_ToolKeepsTwoReusable(t *testing.T) {
	t.Parallel()
	m := membershipToggleTestModel(t, pickerMembershipTool)
	m.groupNames = []string{"work", "base"}
	m.setSelectedMemberships([]string{"work"})
	m.pickerGroups = []string{"work", "base"}
	m.pickerCursor = 1 // "base"
	m.selectGroupMembership()
	_, got, _ := m.selectedMembershipTarget()
	if len(got) != 2 {
		t.Fatalf("tool memberships = %v, want both reusable kept", got)
	}
}

func TestSelectGroupMembership_DotEvictsSecondReusable(t *testing.T) {
	t.Parallel()
	m := membershipToggleTestModel(t, pickerMembershipDot)
	m.groupNames = []string{"work", "base"}
	m.setSelectedMemberships([]string{"work"})
	m.pickerGroups = []string{"work", "base"}
	m.pickerCursor = 1 // "base"
	m.selectGroupMembership()
	_, got, _ := m.selectedMembershipTarget()
	if len(got) != 1 || got[0] != "base" {
		t.Fatalf("dot memberships = %v, want [base] (evicted work)", got)
	}
}

// These drive the picker as a user would: seed the draft like the open* helpers, toggle via selectGroupMembership, then run the real save against a live app and on-disk config.

func TestFlow_ToolsMembership_FreeMultiToggleSavesBothReusableGroups(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	if err := a.CreateGroup("work"); err != nil {
		t.Fatalf("CreateGroup(work): %v", err)
	}
	if err := a.CreateGroup("base"); err != nil {
		t.Fatalf("CreateGroup(base): %v", err)
	}

	m := modelForCmds(a)
	key := toolKey("ripgrep", "brew")

	// Mirrors openGroupMembershipPicker: seed the draft with the tool's current memberships and the picker's candidate group list.
	m.pickerMembershipKind = pickerMembershipTool
	m.pickerMembershipName = "ripgrep"
	m.pickerMembershipKey = key
	m.pickerOriginalGroups = nil
	m.toolMemberships = map[string][]string{key: {}}
	m.pickerGroups = []string{"work", "base"}

	m.pickerCursor = 0
	m.selectGroupMembership()
	m.pickerCursor = 1
	m.selectGroupMembership()

	if got := m.toolMemberships[key]; !slices.Equal(got, []string{"work", "base"}) {
		t.Fatalf("draft memberships before save = %v, want [work base]", got)
	}

	var cmds []tea.Cmd
	m.saveGroupMembershipPicker(&cmds)
	if len(cmds) == 0 {
		t.Fatal("save produced no command")
	}
	msg := runLastBatchCommand(t, tea.Batch(cmds...))
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("save command result = %T, want groupChangedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("save returned error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := findTestGroup(cfg, "work")
	if work == nil || !containsToolMembership(work.Tools, "ripgrep") {
		t.Fatalf("work group missing ripgrep membership: %+v", work)
	}
	base := findTestGroup(cfg, "base")
	if base == nil || !containsToolMembership(base.Tools, "ripgrep") {
		t.Fatalf("base group missing ripgrep membership: %+v", base)
	}
}

func TestFlow_DotsMembership_SecondReusableReplacesFirstOnSave(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop.local")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{
				Name:    "laptop",
				Special: "host",
				Dots:    []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
			},
			{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
			{Name: "base"},
		},
		Hosts: map[string][]string{"laptop": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	m.settings = config.Settings{DotsRepo: repoDir}
	m.hostInfo, _ = a.HostStatus()

	// The dot starts a member of its host group plus one reusable group ("work").
	m.pickerMembershipKind = pickerMembershipDot
	m.pickerMembershipName = "nvim"
	m.pickerOriginalGroups = []string{"laptop", "work"}
	m.dotMemberships = map[string][]string{"nvim": {"laptop", "work"}}
	m.groupNames = []string{"work", "base"}
	m.pickerGroups = []string{"laptop", "work", "base"}

	// User presses Space on "base" — the dot invariant must evict "work".
	m.pickerCursor = 2
	m.selectGroupMembership()

	if got := m.dotMemberships["nvim"]; !slices.Equal(got, []string{"laptop", "base"}) {
		t.Fatalf("draft memberships before save = %v, want [laptop base] (work evicted)", got)
	}

	m.beginDotsOperation("Updating groups for nvim…")
	var cmds []tea.Cmd
	m.saveGroupMembershipPicker(&cmds)
	if len(cmds) == 0 {
		t.Fatal("save produced no command")
	}
	msg := runLastBatchCommand(t, tea.Batch(cmds...))
	got, ok := msg.(dotsLoadedMsg)
	if !ok {
		t.Fatalf("save command result = %T, want dotsLoadedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("save returned error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	laptop := findTestGroup(cfg, "laptop")
	if laptop == nil || !containsDotMembership(laptop.Dots, "nvim") {
		t.Fatalf("laptop host group missing nvim membership: %+v", laptop)
	}
	work := findTestGroup(cfg, "work")
	if work != nil && containsDotMembership(work.Dots, "nvim") {
		t.Fatalf("work group still has nvim membership, want evicted: %+v", work.Dots)
	}
	base := findTestGroup(cfg, "base")
	if base == nil || !containsDotMembership(base.Dots, "nvim") {
		t.Fatalf("base group missing nvim membership: %+v", base)
	}
}
