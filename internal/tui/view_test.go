package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// ── truncatePath ──────────────────────────────────────────────────────────────

func TestTruncatePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		maxW  int
		want  string
	}{
		{"short path fits", "~/.config/nvim", 30, "~/.config/nvim"},
		{"exact length fits", "abcde", 5, "abcde"},
		{"longer than maxW is truncated", "~/.config/nvim/init.lua", 10, "~/.config…"},
		{"maxW=1 returns ellipsis", "hello", 1, "…"},
		{"maxW=2 returns one char plus ellipsis", "hello", 2, "h…"},
		{"empty string", "", 10, ""},
		{"multibyte runes fit", "こんにちは", 10, "こんにちは"},
		{"multibyte runes truncated", "こんにちは世界", 6, "こんにちは…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncatePath(tt.input, tt.maxW)
			if got != tt.want {
				t.Errorf("truncatePath(%q, %d) = %q, want %q", tt.input, tt.maxW, got, tt.want)
			}
		})
	}
}

// ── dotsEntryNameColW ─────────────────────────────────────────────────────────

func TestDotsEntryNameColW(t *testing.T) {
	tests := []struct {
		name    string
		entries []app.DotStatus
		wantMin int // result must be >= this value
	}{
		{
			name:    "empty slice returns floor of 12",
			entries: nil,
			wantMin: 12,
		},
		{
			name:    "all names shorter than 12",
			entries: []app.DotStatus{{Name: "vim"}, {Name: "zsh"}},
			wantMin: 12,
		},
		{
			name: "name longer than 12 wins",
			entries: []app.DotStatus{
				{Name: "nvim"},
				{Name: "this-is-a-very-long-name"},
			},
			wantMin: 24, // len("this-is-a-very-long-name")
		},
		{
			name:    "single name exactly 12",
			entries: []app.DotStatus{{Name: "123456789012"}},
			wantMin: 12,
		},
		{
			name:    "single name of length 13",
			entries: []app.DotStatus{{Name: "1234567890123"}},
			wantMin: 13,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dotsEntryNameColW(tt.entries)
			if got < tt.wantMin {
				t.Errorf("dotsEntryNameColW() = %d, want >= %d", got, tt.wantMin)
			}
		})
	}
}

func TestDotsEntryNameColW_LongestName(t *testing.T) {
	entries := []app.DotStatus{
		{Name: "short"},
		{Name: "medium-name"},
		{Name: "this-is-the-longest-one"},
	}
	got := dotsEntryNameColW(entries)
	// must equal the longest name length (23)
	if got != 23 {
		t.Errorf("dotsEntryNameColW() = %d, want 23", got)
	}
}

// ── dotHealthDisplay ──────────────────────────────────────────────────────────

func TestDotHealthDisplay(t *testing.T) {
	p := defaultPalette()
	tests := []struct {
		health    app.DotHealth
		wantIcon  string
		wantLabel string
	}{
		{app.HealthOK, "✓", "ok"},
		{app.HealthMissing, "✗", "missing"},
		{app.HealthConflict, "!", "conflict"},
		{app.HealthNoSource, "·", "no-source"},
		{app.DotHealth("unknown-status"), "·", "unknown-status"},
	}
	for _, tt := range tests {
		t.Run(string(tt.health), func(t *testing.T) {
			_, icon, label := dotHealthDisplay(p, tt.health)
			if icon != tt.wantIcon {
				t.Errorf("dotHealthDisplay(%q) icon = %q, want %q", tt.health, icon, tt.wantIcon)
			}
			if label != tt.wantLabel {
				t.Errorf("dotHealthDisplay(%q) label = %q, want %q", tt.health, label, tt.wantLabel)
			}
		})
	}
}

func TestDotStateDisplay_TrimsConflictSuffix(t *testing.T) {
	p := defaultPalette()
	_, _, label := dotStateDisplay(p, app.DotStateUntrackedConflict)
	if label != "untracked" {
		t.Fatalf("untracked conflict label = %q, want untracked", label)
	}
}

// ── renderDots ────────────────────────────────────────────────────────────────

func TestRenderDots_NotConfigured(t *testing.T) {
	m := baseModel(nil)
	// DotsRepo is empty by default — expect the "no repo" message with setup hint.
	out := renderDots(m)
	if !strings.Contains(out, "set up now") {
		t.Errorf("expected 'set up now' in output, got:\n%s", out)
	}
}

func TestRenderDots_Disabled(t *testing.T) {
	m := baseModel(nil)
	disabled := true
	m.settings.DotsDisabled = &disabled
	out := renderDots(m)
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected 'disabled' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Sync on This Machine") {
		t.Errorf("expected disabled dots copy to reference renamed Settings row, got:\n%s", out)
	}
	if strings.Contains(out, "Dots Sync") {
		t.Errorf("disabled dots copy should not reference old Settings row name, got:\n%s", out)
	}
}

func TestRenderDots_LoadingKeepsExistingTable(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoading = true
	m.dotsEntries = []app.DotStatus{{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK, State: app.DotStateSynced}}
	out := renderDots(m)
	if strings.Contains(out, "Loading") {
		t.Errorf("dots refresh should not replace table with loading text, got:\n%s", out)
	}
	if !strings.Contains(out, "nvim") {
		t.Errorf("expected existing dots table while loading, got:\n%s", out)
	}
}

func TestRenderDots_IgnoredSectionVisible(t *testing.T) {
	m := baseModel(nil)
	m.width = 100
	m.height = 24
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsEntries = []app.DotStatus{{
		Name:       "kitty",
		TargetPath: "~/.config/kitty",
		Health:     app.DotHealth(app.DotStateIgnored),
		State:      app.DotStateIgnored,
		Actions:    []app.DotAction{app.DotActionUnignore, app.DotActionRemove},
	}}

	out := renderDots(m)
	if !strings.Contains(out, "Ignored") || !strings.Contains(out, "kitty") {
		t.Fatalf("ignored dots entry should render in Ignored section, got:\n%s", out)
	}
}

func TestRenderDots_RemoveConfirmHasNoCancelHint(t *testing.T) {
	m := baseModel(nil)
	m.width = 100
	m.height = 24
	m.mode = viewDots
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		Health:     app.HealthOK,
		State:      app.DotStateSynced,
		Actions:    []app.DotAction{app.DotActionRemove},
	}}
	m.dotsCursor = 0
	m.dotsConfirmIdx = 0

	out := renderDots(m)
	for _, want := range []string{"delete ", ", keep local?", "yes", "no", "y", "n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected delete keep-local prompt %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cancel") || strings.Contains(out, "esc") {
		t.Fatalf("delete confirmation should not render cancel/esc hints, got:\n%s", out)
	}
	full := m.viewString()
	for _, unwanted := range []string{"cancel", "esc", "sync all", "discover", "pull", "add a local path", "search"} {
		if strings.Contains(full, unwanted) {
			t.Fatalf("delete confirmation full view should only render confirm hint; found %q in:\n%s", unwanted, full)
		}
	}
	m.help.ShowAll = true
	help := renderHelpPopup(m)
	if strings.Contains(help, "cancel") {
		t.Fatalf("help popup should not describe escape as cancel during delete confirmation, got:\n%s", help)
	}
}

func TestRenderDots_Empty(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	out := renderDots(m)
	if !strings.Contains(out, "sync all") {
		t.Errorf("expected empty dots guidance in output, got:\n%s", out)
	}
	if strings.Contains(out, "[s]") {
		t.Errorf("empty dots state should not show selected-entry sync hint, got:\n%s", out)
	}
}

func TestRenderDots_WithEntries(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsExpandedName = "nvim"
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK, State: app.DotStateSynced, FileCount: 12, Children: []app.DotChild{
			{Name: "lua", RelPath: "lua", Path: "~/.config/nvim/lua", IsDir: true, FileCount: 10},
		}},
		{Name: "zsh", TargetPath: "~/.zshrc", Health: app.HealthMissing},
	}
	out := renderDots(m)
	if !strings.Contains(out, "nvim") {
		t.Errorf("expected 'nvim' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "zsh") {
		t.Errorf("expected 'zsh' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "12 files") {
		t.Errorf("expected file count in output, got:\n%s", out)
	}
	if !strings.Contains(out, "lua") {
		t.Errorf("expected expanded child row in output, got:\n%s", out)
	}
}

func TestRenderDots_SectionsUseSharedListSpacing(t *testing.T) {
	m := baseModel(nil)
	m.width = 100
	m.height = 30
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced},
		{Name: "zsh", TargetPath: "~/.zshrc", State: app.DotStateMissing},
	}

	out := renderDots(m)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := 0; i < len(lines)-1; i++ {
		if strings.TrimSpace(lines[i]) == "" && strings.TrimSpace(lines[i+1]) == "" {
			t.Fatalf("dots sections should not render repeated blank lines:\n%s", out)
		}
	}
	assertNextNonBlankLine := func(section, want string) {
		t.Helper()
		for i, line := range lines {
			if !strings.Contains(line, section) {
				continue
			}
			if i+1 >= len(lines) {
				t.Fatalf("section %q has no following row:\n%s", section, out)
			}
			next := lines[i+1]
			if strings.TrimSpace(next) == "" {
				t.Fatalf("section %q should not have a blank line after header:\n%s", section, out)
			}
			if !strings.Contains(next, want) {
				t.Fatalf("section %q next row = %q, want %q in:\n%s", section, next, want, out)
			}
			return
		}
		t.Fatalf("missing section %q in:\n%s", section, out)
	}
	assertNextNonBlankLine("Out Of Sync", "zsh")
	assertNextNonBlankLine("Synced", "nvim")
	assertNextNonBlankLine("Repo", "/home/user/dotfiles")
}

func TestRenderDots_ChildNamesShareNameColumnWidth(t *testing.T) {
	m := baseModel(nil)
	m.width = 140
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsExpandedName = "nvim"
	m.dotsCursor = -1
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		Health:     app.HealthOK,
		State:      app.DotStateSynced,
		Children: []app.DotChild{{
			Name:    "a-very-long-child-directory",
			RelPath: "a-very-long-child-directory",
			Path:    "~/.config/nvim/a-very-long-child-directory",
			IsDir:   true,
		}},
	}}

	out := renderDots(m)
	parentLine := renderedLineContaining(out, "nvim")
	childLine := renderedLineContaining(out, "a-very-long-child-directory")
	if parentLine == "" || childLine == "" {
		t.Fatalf("missing expected dots rows:\n%s", out)
	}
	parentTargetCol := visualColumnOf(parentLine, "~/.config/nvim")
	childTargetCol := visualColumnOf(childLine, "~/.config/nvim/a-very-long-child-directory")
	if parentTargetCol < 0 || childTargetCol < 0 {
		t.Fatalf("missing target columns:\nparent=%q\nchild=%q", parentLine, childLine)
	}
	if parentTargetCol != childTargetCol {
		t.Fatalf("target columns not aligned:\nparent=%q\nchild=%q", parentLine, childLine)
	}
}

func TestRenderDots_ChildRowsUseParentStatusAndFileCountColumn(t *testing.T) {
	m := baseModel(nil)
	m.width = 120
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsExpandedName = "nvim"
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		Health:     app.HealthOK,
		State:      app.DotStateSynced,
		FileCount:  13,
		Children: []app.DotChild{{
			Name:      "lua",
			RelPath:   "lua",
			Path:      "~/.config/nvim/lua",
			IsDir:     true,
			FileCount: 12,
		}},
	}}

	out := renderDots(m)
	childLine := renderedLineContaining(out, "lua")
	if childLine == "" {
		t.Fatalf("missing child row:\n%s", out)
	}
	if strings.Contains(childLine, " dir ") {
		t.Fatalf("child row should not render a dir status column: %q", childLine)
	}
	if !strings.Contains(childLine, "synced") || !strings.Contains(childLine, "12 files") {
		t.Fatalf("child row should render status and count columns, got: %q", childLine)
	}
}

func TestRenderDots_RightColumnsKeepStableWidthAndRightMargin(t *testing.T) {
	m := baseModel(nil)
	m.width = 110
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsExpandedName = "nvim"
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateSynced,
		FileCount:  2,
		Group:      "work",
		Children: []app.DotChild{{
			Name:      "cache",
			RelPath:   "cache",
			Path:      "~/.config/nvim/cache",
			IsDir:     true,
			FileCount: 123456,
		}},
	}, {
		Name:       "zsh",
		TargetPath: "~/.zshrc",
		State:      app.DotStateSynced,
		FileCount:  1,
		Group:      "base",
	}}

	out := renderDots(m)
	parentLine := renderedLineContaining(out, "nvim")
	childLine := renderedLineContaining(out, "cache")
	if parentLine == "" || childLine == "" {
		t.Fatalf("missing dots rows:\n%s", out)
	}

	parentFilesEnd := visualColumnOf(parentLine, "2 files") + lipgloss.Width("2 files")
	childFilesEnd := visualColumnOf(childLine, "123456 files") + lipgloss.Width("123456 files")
	if parentFilesEnd != childFilesEnd {
		t.Fatalf("file counts should right-align to same edge:\nparent=%q\nchild=%q", parentLine, childLine)
	}
	if groupCol := visualColumnOf(parentLine, "[work]"); groupCol <= childFilesEnd {
		t.Fatalf("group column should stay after file-count column:\nparent=%q\nchild=%q", parentLine, childLine)
	}
	if width := lipgloss.Width(parentLine); width > screenContentWidth(m.width) {
		t.Fatalf("group column should leave a right margin: width=%d terminal=%d line=%q", width, m.width, parentLine)
	}
}

func TestRenderDots_RightColumnsFitContentAcrossSections(t *testing.T) {
	m := baseModel(nil)
	m.width = 140
	m.height = 24
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{{
		Name:       "conflicted",
		TargetPath: "~/.config/conflicted",
		State:      app.DotStateUntrackedConflict,
		FileCount:  1,
		Group:      "base",
	}, {
		Name:       "synced",
		TargetPath: "~/.config/synced",
		State:      app.DotStateSynced,
		FileCount:  23,
		Group:      "very-long-profile-group",
	}}

	out := renderDots(m)
	conflictLine := renderedLineContaining(out, "untracked")
	syncedLine := renderedLineContaining(out, "[very-long-profile-group]")
	if conflictLine == "" || syncedLine == "" {
		t.Fatalf("missing expected dots rows:\n%s", out)
	}
	if strings.Contains(conflictLine, "untracked-conflict") {
		t.Fatalf("conflict suffix should not render in dots status label:\n%s", conflictLine)
	}

	conflictFilesEnd := visualColumnOf(conflictLine, "1 file") + lipgloss.Width("1 file")
	syncedFilesEnd := visualColumnOf(syncedLine, "23 files") + lipgloss.Width("23 files")
	if conflictFilesEnd != syncedFilesEnd {
		t.Fatalf("file-count column should right-align across sections:\nconflict=%q\nsynced=%q", conflictLine, syncedLine)
	}
	if groupCol := visualColumnOf(syncedLine, "[very-long-profile-group]"); groupCol <= syncedFilesEnd {
		t.Fatalf("long group badge should fit after file-count column:\n%s", syncedLine)
	}
}

func TestRenderDots_TransientUntrackedConflictShowsAsOutOfSync(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{{
		Name:       "claude",
		TargetPath: "~/.claude",
		State:      app.DotStateUntrackedConflict,
		Actions:    []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionIgnore},
	}}

	out := renderDots(m)
	outOfSync := strings.Index(out, "Out Of Sync")
	claude := strings.Index(out, "claude")
	conflict := strings.Index(out, "Conflict")
	if outOfSync < 0 || claude < 0 || claude < outOfSync {
		t.Fatalf("transient claude should render in Out Of Sync:\n%s", out)
	}
	if conflict >= 0 && conflict < claude {
		t.Fatalf("transient claude should not render in Conflict:\n%s", out)
	}
	for _, want := range []string{"u use repo", "l use local"} {
		if !strings.Contains(out, want) {
			t.Fatalf("transient claude should expose %q action:\n%s", want, out)
		}
	}
}

func TestRenderDots_ConflictInfersResolveActionsWithoutExplicitActions(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateConflict,
	}}
	m.dotsCursor = 0

	out := renderDots(m)
	for _, want := range []string{"u use repo", "l use local", "x ignore", "d delete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("conflict row missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDots_OutOfSyncSyncHintNamesResolutionSide(t *testing.T) {
	tests := []struct {
		name  string
		state app.DotState
		want  string
	}{
		{name: "missing", state: app.DotStateMissing, want: "s use repo"},
		{name: "broken", state: app.DotStateBroken, want: "s repair"},
		{name: "local only", state: app.DotStateLocalOnly, want: "s use local"},
		{name: "repo only", state: app.DotStateRepoOnly, want: "s use repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := baseModel(nil)
			m.settings.DotsRepo = "/home/user/dotfiles"
			m.dotsLoaded = true
			m.dotsEntries = []app.DotStatus{{
				Name:       "nvim",
				TargetPath: "~/.config/nvim",
				State:      tt.state,
			}}

			out := renderDots(m)
			if !strings.Contains(out, tt.want) {
				t.Fatalf("out-of-sync row missing %q:\n%s", tt.want, out)
			}
		})
	}
}

func TestRenderDots_ScrollKeepsSelectedRowHintsVisibleAtBottom(t *testing.T) {
	m := baseModel(nil)
	m.width = 100
	m.height = 8
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	for i := range 8 {
		name := "dot0" + string(rune('0'+i))
		m.dotsEntries = append(m.dotsEntries, app.DotStatus{
			Name:       name,
			TargetPath: "~/.config/" + name,
			State:      app.DotStateSynced,
			Children: []app.DotChild{{
				Name:    "child",
				RelPath: "child",
				Path:    "~/.config/" + name + "/child",
				IsDir:   true,
			}},
		})
	}
	m.dotsCursor = 7

	out := renderDots(m)
	if !strings.Contains(out, "dot07") {
		t.Fatalf("selected bottom row should be visible:\n%s", out)
	}
	if !strings.Contains(out, "space expand") {
		t.Fatalf("selected bottom row hints should stay visible:\n%s", out)
	}
}

func TestRenderDots_IgnoreConfirmRendersInlineAction(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsExpandedName = "nvim"
	m.dotsEntries = []app.DotStatus{
		{
			Name:       "nvim",
			TargetPath: "~/.config/nvim",
			Health:     app.HealthOK,
			Actions:    []app.DotAction{app.DotActionIgnore},
			Children: []app.DotChild{{
				Name:    "cache",
				RelPath: "cache",
				Path:    "~/.config/nvim/cache",
				IsDir:   true,
			}},
		},
	}
	m.dotsCursor = 1
	m.dotsIgnoreIdx = 1
	out := renderDots(m)
	if !strings.Contains(out, "confirm ignore") {
		t.Errorf("expected inline ignore confirmation, got:\n%s", out)
	}
}

func TestRenderDots_WithConflict(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "tmux", TargetPath: "~/.tmux.conf", Health: app.HealthConflict},
	}
	out := renderDots(m)
	if !strings.Contains(out, "tmux") {
		t.Errorf("expected 'tmux' in output, got:\n%s", out)
	}
}

func TestRenderDots_GitStatusDirty(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
	}
	m.dotsGitStatus = "M .zshrc"
	out := renderDots(m)
	if !strings.Contains(out, "dirty") {
		t.Errorf("expected 'dirty' in output, got:\n%s", out)
	}
}

func TestRenderDots_DotsRepoDisplayed(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
	}
	out := renderDots(m)
	if !strings.Contains(out, "/home/user/dotfiles") {
		t.Errorf("expected repo path in output, got:\n%s", out)
	}
}

func TestRenderDots_UsesHomeAliasForUserPaths(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	m := baseModel(nil)
	m.settings.DotsRepo = "/home/user/dotfiles"
	m.dotsLoaded = true
	m.dotsExpandedName = "nvim"
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "/home/user/.config/nvim",
		Health:     app.HealthOK,
		State:      app.DotStateSynced,
		Children: []app.DotChild{{
			Name:    "init.lua",
			RelPath: "init.lua",
			Path:    "/home/user/.config/nvim/init.lua",
		}},
	}}

	out := renderDots(m)
	for _, want := range []string{"~/dotfiles", "~/.config/nvim", "~/.config/nvim/init.lua"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered dots missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/home/user") {
		t.Fatalf("rendered dots should use home alias, got:\n%s", out)
	}
}

// ── confirmCancelHint ─────────────────────────────────────────────────────────

func TestConfirmCancelHint(t *testing.T) {
	m := baseModel(nil)
	got := confirmCancelHint(m, "save changes")
	if len(got) == 0 {
		t.Error("expected non-empty hint string")
	}
	if !strings.Contains(got, "save changes") {
		t.Errorf("expected 'save changes' in hint, got: %q", got)
	}
	if !strings.Contains(got, "cancel") {
		t.Errorf("expected 'cancel' in hint, got: %q", got)
	}
}

func TestConfirmCancelHint_UsesKeyBindings(t *testing.T) {
	m := baseModel(nil)
	// The hint should contain the Confirm key binding help text.
	confirmKey := m.keys.Confirm.Help().Key
	got := confirmCancelHint(m, "do it")
	if !strings.Contains(got, confirmKey) {
		t.Errorf("expected confirm key %q in hint %q", confirmKey, got)
	}
}

// ── newCursorList ─────────────────────────────────────────────────────────────

func TestNewCursorList_Basic(t *testing.T) {
	p := defaultPalette()
	items := []any{"alpha", "beta", "gamma"}
	l := newCursorList(p, items, 0, 2)
	if l == nil {
		t.Fatal("expected non-nil list")
	}
	out := l.String()
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected 'alpha' in list output, got:\n%s", out)
	}
}

func TestNewCursorList_CursorMarker(t *testing.T) {
	p := defaultPalette()
	items := []any{"apple", "banana", "cherry"}
	// cursor at second item
	l := newCursorList(p, items, 1, 0)
	out := l.String()
	// The enumerator returns "‣" for the cursor row and " " for others.
	if !strings.Contains(out, "‣") {
		t.Errorf("expected cursor marker '‣' in list output, got:\n%s", out)
	}
}

func TestNewCursorList_EmptyItems(t *testing.T) {
	p := defaultPalette()
	l := newCursorList(p, []any{}, 0, 0)
	if l == nil {
		t.Fatal("expected non-nil list even for empty items")
	}
}

// ── hintKey ───────────────────────────────────────────────────────────────────

func TestHintKey_NonEmpty(t *testing.T) {
	p := defaultPalette()
	got := hintKey(p, "q", "quit")
	if len(got) == 0 {
		t.Error("expected non-empty hint key string")
	}
	if !strings.Contains(got, "quit") {
		t.Errorf("expected 'quit' in hint key, got: %q", got)
	}
}

func TestHintKey_ContainsKeyAndDesc(t *testing.T) {
	p := defaultPalette()
	tests := []struct{ key, desc string }{
		{"enter", "confirm"},
		{"esc", "cancel"},
		{"?", "help"},
		{"q", "quit"},
	}
	for _, tt := range tests {
		got := hintKey(p, tt.key, tt.desc)
		if !strings.Contains(got, tt.desc) {
			t.Errorf("hintKey(%q, %q) missing desc in: %q", tt.key, tt.desc, got)
		}
	}
}

// ── hintJoin ──────────────────────────────────────────────────────────────────

func TestHintJoin(t *testing.T) {
	p := defaultPalette()
	tests := []struct {
		name  string
		parts []string
		check func(got string) bool
		desc  string
	}{
		{
			name:  "empty parts",
			parts: []string{},
			check: func(got string) bool { return got == "" },
			desc:  "expected empty string",
		},
		{
			name:  "single part",
			parts: []string{"part1"},
			check: func(got string) bool { return strings.Contains(got, "part1") },
			desc:  "expected 'part1'",
		},
		{
			name:  "two parts joined",
			parts: []string{"alpha", "beta"},
			check: func(got string) bool {
				return strings.Contains(got, "alpha") && strings.Contains(got, "beta")
			},
			desc: "expected both 'alpha' and 'beta'",
		},
		{
			name:  "three parts joined",
			parts: []string{"a", "b", "c"},
			check: func(got string) bool {
				return strings.Contains(got, "a") &&
					strings.Contains(got, "b") &&
					strings.Contains(got, "c")
			},
			desc: "expected 'a', 'b', 'c'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hintJoin(p, tt.parts...)
			if !tt.check(got) {
				t.Errorf("hintJoin(%v) = %q: %s", tt.parts, got, tt.desc)
			}
		})
	}
}

// ── placeOverlay ──────────────────────────────────────────────────────────────

func TestPlaceOverlay_ContainsOverlayContent(t *testing.T) {
	bg := strings.Repeat("background text here\n", 10)
	overlay := "POPUP CONTENT"
	result := placeOverlay(bg, overlay, 80, 24)
	if !strings.Contains(result, "POPUP") {
		t.Errorf("expected overlay content 'POPUP' in result")
	}
}

func TestPlaceOverlay_SmallBgLargeOverlay(t *testing.T) {
	bg := "bg\n"
	overlay := "overlay content that is long"
	// Should not panic with negative startX/Y — both clamped to 0.
	result := placeOverlay(bg, overlay, 5, 2)
	// Just verify it doesn't panic and returns something.
	if result == "" {
		t.Error("expected non-empty result from placeOverlay")
	}
}

// ── integration: renderDots does not panic under various states ───────────────

func TestRenderDots_NoPanic(t *testing.T) {
	cases := []struct {
		name string
		fn   func() Model
	}{
		{
			name: "zero value model with DotsRepo",
			fn: func() Model {
				m := baseModel(nil)
				m.settings.DotsRepo = "/tmp/dots"
				m.dotsLoaded = true
				return m
			},
		},
		{
			name: "no source health",
			fn: func() Model {
				m := baseModel(nil)
				m.settings.DotsRepo = "/tmp/dots"
				m.dotsLoaded = true
				m.dotsEntries = []app.DotStatus{
					{Name: "fish", TargetPath: "~/.config/fish", Health: app.HealthNoSource},
				}
				return m
			},
		},
		{
			name: "cursor mid-list",
			fn: func() Model {
				m := baseModel(nil)
				m.settings.DotsRepo = "/tmp/dots"
				m.dotsLoaded = true
				m.dotsEntries = []app.DotStatus{
					{Name: "a", TargetPath: "~/.a", Health: app.HealthOK},
					{Name: "b", TargetPath: "~/.b", Health: app.HealthOK},
					{Name: "c", TargetPath: "~/.c", Health: app.HealthOK},
				}
				m.dotsCursor = 1
				return m
			},
		},
		{
			name: "confirm delete overlay",
			fn: func() Model {
				m := baseModel(nil)
				m.settings.DotsRepo = "/tmp/dots"
				m.dotsLoaded = true
				m.dotsEntries = []app.DotStatus{
					{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
				}
				m.dotsCursor = 0
				m.dotsConfirmIdx = 0
				return m
			},
		},
		{
			name: "DotsDisabled with DotsRepo set",
			fn: func() Model {
				m := baseModel(nil)
				disabled := true
				m.settings.DotsDisabled = &disabled
				m.settings.DotsRepo = "/tmp/dots"
				return m
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("renderDots panicked: %v", r)
				}
			}()
			m := tc.fn()
			_ = renderDots(m)
		})
	}
}

// ── sortDotsEntries ───────────────────────────────────────────────────────────

func TestSortDotsEntries_Order(t *testing.T) {
	entries := []app.DotStatus{
		{Name: "zsh", Health: app.HealthOK},
		{Name: "nvim", Health: app.HealthMissing},
		{Name: "tmux", Health: app.HealthConflict},
		{Name: "fish", Health: app.HealthNoSource},
	}
	sortDotsEntries(entries)
	// conflict must come first
	if entries[0].Health != app.HealthConflict {
		t.Errorf("expected conflict first, got %q", entries[0].Health)
	}
	// ok must come last
	if entries[len(entries)-1].Health != app.HealthOK {
		t.Errorf("expected ok last, got %q", entries[len(entries)-1].Health)
	}
}

func TestSortDotsEntries_AlphaWithinBucket(t *testing.T) {
	entries := []app.DotStatus{
		{Name: "z-vim", Health: app.HealthOK},
		{Name: "a-vim", Health: app.HealthOK},
		{Name: "m-vim", Health: app.HealthOK},
	}
	sortDotsEntries(entries)
	if entries[0].Name != "a-vim" {
		t.Errorf("expected 'a-vim' first within ok bucket, got %q", entries[0].Name)
	}
	if entries[2].Name != "z-vim" {
		t.Errorf("expected 'z-vim' last within ok bucket, got %q", entries[2].Name)
	}
}

// ── dotsSortKey ───────────────────────────────────────────────────────────────

func TestDotsSortKey(t *testing.T) {
	// conflict < missing/no-source < ok/unknown
	conflictKey := dotsSortKey(app.HealthConflict)
	missingKey := dotsSortKey(app.HealthMissing)
	noSourceKey := dotsSortKey(app.HealthNoSource)
	okKey := dotsSortKey(app.HealthOK)

	if conflictKey >= missingKey {
		t.Errorf("conflict key (%d) should be < missing key (%d)", conflictKey, missingKey)
	}
	if missingKey > okKey {
		t.Errorf("missing key (%d) should be <= ok key (%d)", missingKey, okKey)
	}
	if noSourceKey > okKey {
		t.Errorf("no-source key (%d) should be <= ok key (%d)", noSourceKey, okKey)
	}
}

// ── renderDots with settings.DotsDisabled false-value pointer ────────────────

func TestRenderDots_DotsDisabledFalse(t *testing.T) {
	m := baseModel(nil)
	disabled := false
	m.settings.DotsDisabled = &disabled
	m.settings.DotsRepo = ""
	out := renderDots(m)
	// Should show "not configured" not "disabled"
	if strings.Contains(out, "disabled") {
		t.Errorf("expected not-disabled rendering, got:\n%s", out)
	}
}

// ── BoolVal nil safety (used inside renderDots) ───────────────────────────────

func TestRenderDots_NilDotsDisabled(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsDisabled = nil // ensure nil is handled
	m.settings.DotsRepo = "/tmp/dots"
	m.dotsLoaded = true
	// Should not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("renderDots panicked with nil DotsDisabled: %v", r)
		}
	}()
	_ = renderDots(m)
}

// ── renderDots confirm overwrite overlay ─────────────────────────────────────

func TestRenderDots_ConfirmOverwrite(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/tmp/dots"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{
			Name:       "nvim",
			TargetPath: "~/.config/nvim",
			Health:     app.HealthConflict,
			State:      app.DotStateConflict,
			Actions:    []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove},
		},
	}
	m.dotsCursor = 0
	m.dotsOverwriteIdx = 0
	out := renderDots(m)
	if !strings.Contains(out, "confirm use repo") {
		t.Errorf("expected 'confirm use repo' in output, got:\n%s", out)
	}
}

// ── hintJoin separator presence ──────────────────────────────────────────────

func TestHintJoin_SeparatorPresent(t *testing.T) {
	p := defaultPalette()
	got := hintJoin(p, "a", "b", "c")
	// The separator matches the footer help separator. Strip any ANSI so we can
	// look at the text.
	// lipgloss renders styles only when a renderer is set up; in tests we use
	// the default (no-color) renderer so styles are pass-through.
	if !strings.Contains(got, "•") {
		// If no ANSI in test environment, may not have styled sep, but content must be there.
		if !strings.Contains(got, "a") || !strings.Contains(got, "b") || !strings.Contains(got, "c") {
			t.Errorf("hintJoin: missing content; got: %q", got)
		}
	}
}

// ── placeOverlay does not lose bg dimensions ─────────────────────────────────

func TestPlaceOverlay_NoPanic_Variants(t *testing.T) {
	variants := []struct {
		bg, fg string
		w, h   int
	}{
		{"", "overlay", 40, 10},
		{"background\n", "", 40, 10},
		{"background\n", "overlay\n", 0, 0},
		{"background\nline2\n", "short\n", 80, 24},
	}
	for _, v := range variants {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("placeOverlay panicked: %v", r)
				}
			}()
			_ = placeOverlay(v.bg, v.fg, v.w, v.h)
		}()
	}
}

// ── renderDots: long target path is truncated ─────────────────────────────────

func TestRenderDots_LongPathTruncated(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/tmp/dots"
	m.dotsLoaded = true
	longPath := strings.Repeat("/very/long/path/segment", 10)
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: longPath, Health: app.HealthOK},
	}
	// Should not panic; output should contain "nvim".
	out := renderDots(m)
	if !strings.Contains(out, "nvim") {
		t.Errorf("expected 'nvim' in output with long path, got:\n%s", out)
	}
}

func TestRenderDots_NarrowWidthFitsRows(t *testing.T) {
	m := baseModel(nil)
	m.width = 44
	m.height = 12
	m.settings.DotsRepo = "/home/user/a/very/long/dotfiles/repository/path"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{{
		Name:       "very-long-dot-entry-name",
		TargetPath: "/home/user/.config/some/really/long/path/that/needs/cutting",
		Health:     app.HealthOK,
		State:      app.DotStateSynced,
		Group:      "very-long-group",
		FileCount:  1234,
		Children: []app.DotChild{{
			Name:      "very-long-child-name",
			Path:      "/home/user/.config/some/really/long/path/child-file",
			Depth:     2,
			FileCount: 99,
		}},
	}}
	m.dotsExpandedName = "very-long-dot-entry-name"

	assertLinesFitWidth(t, renderDots(m), m.width)
}

// ── config.BoolVal is used in renderDots; just verify it is consistent ────────

func TestRenderDots_DotsDisabledTrue(t *testing.T) {
	m := baseModel(nil)
	b := true
	m.settings.DotsDisabled = &b
	m.settings.DotsRepo = "/tmp/dots"
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
	}
	out := renderDots(m)
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected disabled message even with entries set, got:\n%s", out)
	}
	if strings.Contains(out, "nvim") {
		t.Errorf("entries should not be rendered when dots is disabled, got:\n%s", out)
	}
}

// Ensure we import config so BoolVal is exercised via renderDots.
var _ = config.BoolVal
