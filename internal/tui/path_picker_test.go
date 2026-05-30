package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestPathPickerFiltersTypedPath(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "cider"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})

	if got := p.input.Value(); got != filepath.Join(tmp, "a") {
		t.Fatalf("input = %q, want %q", got, filepath.Join(tmp, "a"))
	}
	if len(p.filtered) != 1 || p.filtered[0].name != "alpha" {
		t.Fatalf("filtered = %#v, want alpha only", p.filtered)
	}
}

func TestPathPickerQueryAllowsDotDotPrefixChild(t *testing.T) {
	tmp := t.TempDir()
	child := filepath.Join(tmp, "..config")
	mustMkdir(t, child)

	p, _ := newPathPicker(tmp, false, 60, 8)
	p.input.SetValue(child)

	if got := p.query(); got != "..config" {
		t.Fatalf("query = %q, want ..config", got)
	}
}

func TestPathPickerTabCyclesFilteredMatches(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "alpine"))
	mustMkdir(t, filepath.Join(tmp, "cider"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if got, want := p.input.Value(), filepath.Join(tmp, "alpha"); got != want {
		t.Fatalf("input after first tab = %q, want %q", got, want)
	}
	if got := pathPickerFilteredNames(p); strings.Join(got, ",") != "alpha,alpine" {
		t.Fatalf("filtered after first tab = %#v, want original cycle candidates", got)
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := p.input.Value(), filepath.Join(tmp, "alpine"); got != want {
		t.Fatalf("input after second tab = %q, want %q", got, want)
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := p.input.Value(), filepath.Join(tmp, "alpha"); got != want {
		t.Fatalf("input after third tab = %q, want wrapped %q", got, want)
	}
}

func TestPathPickerTabStartsCycleFromHighlightedMatch(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "alpine"))
	mustMkdir(t, filepath.Join(tmp, "amber"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if got, want := p.input.Value(), filepath.Join(tmp, "alpine"); got != want {
		t.Fatalf("input after tab from highlighted row = %q, want %q", got, want)
	}
}

func TestPathPickerTabCycleResetsAfterTextEdit(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "alpine"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := p.input.Value(), filepath.Join(tmp, "alpha"); got != want {
		t.Fatalf("input after first tab = %q, want %q", got, want)
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.completionActive {
		t.Fatal("completion cycle should reset after text edit")
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := p.input.Value(), filepath.Join(tmp, "alpha"); got != want {
		t.Fatalf("input after reset tab = %q, want rebuilt single match %q", got, want)
	}
}

func TestPathPickerTabWithNoMatchesLeavesInput(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	before := p.input.Value()
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if got := p.input.Value(); got != before {
		t.Fatalf("input after no-match tab = %q, want unchanged %q", got, before)
	}
	if p.completionActive {
		t.Fatal("no-match tab should not start a completion cycle")
	}
}

func TestPathPickerTabCycleRespectsAllowFiles(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "alpha")
	file := filepath.Join(tmp, "alpine")
	mustMkdir(t, dir)
	mustWriteFile(t, file)

	dirsOnly, _ := newPathPicker(tmp, false, 60, 8)
	dirsOnly, _ = dirsOnly.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	dirsOnly, _ = dirsOnly.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	dirsOnly, _ = dirsOnly.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	dirsOnly, _ = dirsOnly.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := dirsOnly.input.Value(); got != dir {
		t.Fatalf("directory-only input after tab cycle = %q, want only dir %q", got, dir)
	}

	withFiles, _ := newPathPicker(tmp, true, 60, 8)
	withFiles, _ = withFiles.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	withFiles, _ = withFiles.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	withFiles, _ = withFiles.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	withFiles, _ = withFiles.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := withFiles.input.Value(); got != file {
		t.Fatalf("file-allowed input after second tab = %q, want file %q", got, file)
	}
}

func TestPathPickerDisplaysHomeAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, "alpha"))
	mustMkdir(t, filepath.Join(home, "alpine"))

	p, _ := newPathPicker("", false, 60, 8)
	if got := p.input.Value(); got != "~/" {
		t.Fatalf("input = %q, want ~/", got)
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.input.Value(); got != "~/alpha" {
		t.Fatalf("input after tab = %q, want ~/alpha", got)
	}
	if got := p.SelectedPath(); got != filepath.Join(home, "alpha") {
		t.Fatalf("SelectedPath = %q, want first home child", got)
	}
}

func TestPathPickerSuggestsPartialRepeatedHomeBasename(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "lkshrk")
	mustMkdir(t, home)
	t.Setenv("HOME", home)

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range "lks" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := p.input.Value(); got != "~/lks" {
		t.Fatalf("input = %q, want ~/lks before accepting fallback", got)
	}
	if got := pathPickerFilteredNames(p); strings.Join(got, ",") != "lkshrk" {
		t.Fatalf("filtered names = %#v, want home basename fallback", got)
	}
	if got := p.HighlightedPath(); got != home {
		t.Fatalf("HighlightedPath = %q, want home %q", got, home)
	}
	if got := p.SelectedPath(); got != home {
		t.Fatalf("SelectedPath = %q, want home fallback %q", got, home)
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.input.Value(); got != "~/" {
		t.Fatalf("input after fallback tab = %q, want ~/", got)
	}
}

func TestPathPickerPartialHomeBasenamePrefersRealChild(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "lkshrk")
	mustMkdir(t, home)
	t.Setenv("HOME", home)
	child := filepath.Join(home, "lkshrk")
	mustMkdir(t, child)

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range "lks" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := pathPickerFilteredNames(p); strings.Join(got, ",") != "lkshrk" {
		t.Fatalf("filtered names = %#v, want real child", got)
	}
	if got := p.HighlightedPath(); got != child {
		t.Fatalf("HighlightedPath = %q, want child %q", got, child)
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.input.Value(); got != "~/lkshrk" {
		t.Fatalf("input after tab = %q, want real child path", got)
	}
}

func TestPathPickerSelectedPathUsesTypedExistingPath(t *testing.T) {
	tmp := t.TempDir()
	selected := filepath.Join(tmp, "dotfiles")
	mustMkdir(t, selected)

	p, _ := newPathPicker(tmp, false, 60, 8)
	p.input.SetValue(selected)
	p.syncDirectoryFromInput()
	p.applyFilter()

	if got := p.SelectedPath(); got != selected {
		t.Fatalf("SelectedPath = %q, want %q", got, selected)
	}
}

func TestPathPickerAllowsFilesWhenConfigured(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "settings.json")
	mustWriteFile(t, file)

	p, _ := newPathPicker(tmp, true, 60, 8)
	p.input.SetValue(file)
	p.syncDirectoryFromInput()
	p.applyFilter()

	if got := p.SelectedPath(); got != file {
		t.Fatalf("SelectedPath = %q, want file %q", got, file)
	}
}

func TestPathPickerSlashDescendsAndShowsFiles(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, ".cache")
	mustMkdir(t, cache)
	mustMkdir(t, filepath.Join(cache, "nested"))
	file := filepath.Join(cache, "settings.json")
	mustWriteFile(t, file)

	p, _ := newPathPicker(tmp, true, 60, 8)
	p.input.SetValue(cache)
	p.input.CursorEnd()
	p.syncDirectoryFromInput()
	p.applyFilter()
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})

	if got := p.CurrentDirectory(); got != cache {
		t.Fatalf("CurrentDirectory = %q, want descended directory %q", got, cache)
	}
	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if !containsString(names, "nested") || !containsString(names, "settings.json") {
		t.Fatalf("filtered names after slash = %#v, want nested dir and settings.json file", names)
	}
}

func TestPathPickerTrailingSlashShowsTypedHomeDirectoryContents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := filepath.Join(home, ".config")
	mustMkdir(t, config)
	mustMkdir(t, filepath.Join(config, "nvim"))
	mustWriteFile(t, filepath.Join(config, "settings.json"))

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range ".config/" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := p.CurrentDirectory(); got != config {
		t.Fatalf("CurrentDirectory = %q, want typed directory %q", got, config)
	}
	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if !containsString(names, "nvim") || !containsString(names, "settings.json") {
		t.Fatalf("filtered names after trailing slash = %#v, want typed directory contents", names)
	}
}

func TestPathPickerCollapsesRepeatedHomeBasenameInput(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "lkshrk")
	mustMkdir(t, home)
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, ".claude"))

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range "lkshrk/.cla" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := p.input.Value(); got != "~/.cla" {
		t.Fatalf("input = %q, want home-relative path without repeated username", got)
	}
	if got := p.CurrentDirectory(); got != home {
		t.Fatalf("CurrentDirectory = %q, want home %q", got, home)
	}
	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if !containsString(names, ".claude") {
		t.Fatalf("filtered names after repeated home basename = %#v, want .claude", names)
	}
}

func TestPathPickerKeepsRealHomeBasenameChild(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "lkshrk")
	mustMkdir(t, home)
	t.Setenv("HOME", home)
	child := filepath.Join(home, "lkshrk")
	mustMkdir(t, child)
	mustMkdir(t, filepath.Join(child, ".claude"))

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range "lkshrk/.cla" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := p.input.Value(); got != "~/lkshrk/.cla" {
		t.Fatalf("input = %q, want real home-basename child path", got)
	}
	if got := p.CurrentDirectory(); got != child {
		t.Fatalf("CurrentDirectory = %q, want child %q", got, child)
	}
	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if !containsString(names, ".claude") {
		t.Fatalf("filtered names under real child = %#v, want .claude", names)
	}
}

func TestPathPickerDotFiltersHiddenEntries(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, ".cache"))
	mustMkdir(t, filepath.Join(tmp, ".config"))
	mustMkdir(t, filepath.Join(tmp, "docs"))

	p, _ := newPathPicker(tmp, true, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: '.', Text: "."})

	if got := p.CurrentDirectory(); got != tmp {
		t.Fatalf("CurrentDirectory = %q, want %q after typing dot", got, tmp)
	}
	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if !containsString(names, ".cache") || !containsString(names, ".config") {
		t.Fatalf("filtered names after dot = %#v, want hidden matches", names)
	}
	if containsString(names, "docs") {
		t.Fatalf("filtered names after dot = %#v, should not include visible non-match", names)
	}
}

func TestPathPickerDoesNotFuzzyMatchSubsequence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, ".luarocks"))
	mustMkdir(t, filepath.Join(home, "lkshrk"))

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range "lks" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if containsString(names, ".luarocks") {
		t.Fatalf("filtered names for lks = %#v, should not fuzzy-match .luarocks", names)
	}
	if !containsString(names, "lkshrk") {
		t.Fatalf("filtered names for lks = %#v, want prefix match lkshrk", names)
	}
}

func TestPathPickerMatchesHiddenNamesWithDotPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(home, ".config"))
	mustMkdir(t, filepath.Join(home, "client"))

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range ".cla" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	var names []string
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	if !containsString(names, ".claude") {
		t.Fatalf("filtered names for .cla = %#v, want hidden prefix .claude", names)
	}
	if containsString(names, ".config") || containsString(names, "client") {
		t.Fatalf("filtered names for .cla = %#v, should only include prefix-style matches", names)
	}
}

func TestPathPickerPredictionsMatchFindFilteredChildren(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, "lkshrk"))
	mustMkdir(t, filepath.Join(home, ".luarocks"))
	mustMkdir(t, filepath.Join(home, ".claude"))
	config := filepath.Join(home, ".config")
	mustMkdir(t, config)
	mustMkdir(t, filepath.Join(config, "zathura"))
	mustMkdir(t, filepath.Join(config, "zed"))
	mustMkdir(t, filepath.Join(config, "nvim"))
	mustWriteFile(t, filepath.Join(config, "zellij.kdl"))

	tests := []struct {
		name     string
		typed    string
		dir      string
		query    string
		wantCwd  string
		wantText string
	}{
		{
			name:     "home prefix",
			typed:    "lks",
			dir:      home,
			query:    "lks",
			wantCwd:  home,
			wantText: "~/lks",
		},
		{
			name:     "hidden prefix",
			typed:    ".cla",
			dir:      home,
			query:    ".cla",
			wantCwd:  home,
			wantText: "~/.cla",
		},
		{
			name:     "nested directory prefix",
			typed:    ".config/ze",
			dir:      config,
			query:    "ze",
			wantCwd:  config,
			wantText: "~/.config/ze",
		},
		{
			name:     "trailing slash lists directory",
			typed:    ".config/",
			dir:      config,
			query:    "",
			wantCwd:  config,
			wantText: "~/.config/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newPathPicker("", true, 60, 8)
			for _, r := range tt.typed {
				p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}

			if got := p.input.Value(); got != tt.wantText {
				t.Fatalf("input = %q, want %q", got, tt.wantText)
			}
			if got := p.CurrentDirectory(); got != tt.wantCwd {
				t.Fatalf("CurrentDirectory = %q, want %q", got, tt.wantCwd)
			}
			got := pathPickerFilteredNames(p)
			want := findFilteredChildren(t, tt.dir, tt.query, true)
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("filtered names = %#v, want find-filtered children %#v", got, want)
			}
		})
	}
}

func TestPathPickerRefreshesAfterInvalidDirectoryInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, "lkshrk"))

	p, _ := newPathPicker("", true, 60, 8)
	p.input.SetValue("~/missing/path")
	p.input.CursorEnd()
	p.syncDirectoryFromInput()
	p.applyFilter()
	if len(p.filtered) != 0 {
		t.Fatalf("filtered after invalid directory = %#v, want no matches", pathPickerFilteredNames(p))
	}

	p.input.SetValue("~/lks")
	p.input.CursorEnd()
	p.syncDirectoryFromInput()
	p.applyFilter()

	if got := pathPickerFilteredNames(p); !containsString(got, "lkshrk") {
		t.Fatalf("filtered after returning to home query = %#v, want lkshrk", got)
	}
}

func TestPathPickerRejectsFilesWhenDirectoryOnly(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "settings.json")
	mustWriteFile(t, file)

	p, _ := newPathPicker(tmp, false, 60, 8)
	p.input.SetValue(file)
	p.syncDirectoryFromInput()
	p.applyFilter()

	if got := p.SelectedPath(); got == file {
		t.Fatalf("SelectedPath = %q, directory-only picker should not select file", got)
	}
}

func TestPathPickerRejectsInvalidTypedPath(t *testing.T) {
	tmp := t.TempDir()
	p, _ := newPathPicker(tmp, false, 60, 8)
	p.input.SetValue(filepath.Join(tmp, "missing"))
	p.input.CursorEnd()
	p.syncDirectoryFromInput()
	p.applyFilter()

	if got := p.SelectedPath(); got != "" {
		t.Fatalf("SelectedPath = %q, want no selection for invalid typed path", got)
	}
}

func TestPathPickerPasteUpdatesMatches(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "cider"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.PasteMsg{Content: filepath.Join(tmp, "a")})

	if got := p.input.Value(); got != filepath.Join(tmp, "a") {
		t.Fatalf("input after paste = %q, want pasted absolute path", got)
	}
	if len(p.filtered) != 1 || p.filtered[0].name != "alpha" {
		t.Fatalf("filtered = %#v, want alpha only", p.filtered)
	}
}

func TestPathPickerLeftKeyEditsCursorInsteadOfParent(t *testing.T) {
	tmp := t.TempDir()
	child := filepath.Join(tmp, "child")
	mustMkdir(t, child)

	p, _ := newPathPicker(child, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyLeft})

	if got := p.CurrentDirectory(); got != child {
		t.Fatalf("CurrentDirectory = %q, want unchanged %q", got, child)
	}
	if got := p.input.Position(); got != len(child)-1 {
		t.Fatalf("input cursor = %d, want one position left", got)
	}
}

func TestPathPickerBackspaceEditsPathInsteadOfParent(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

	if got := p.CurrentDirectory(); got != tmp {
		t.Fatalf("CurrentDirectory = %q, want unchanged %q", got, tmp)
	}
	if got := p.input.Value(); got != tmp+string(filepath.Separator) {
		t.Fatalf("input after backspace = %q, want trailing separator path", got)
	}
}

func TestPathPickerDotDotInputMovesAboveHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "lkshrk")
	mustMkdir(t, home)
	mustMkdir(t, filepath.Join(root, "other-user"))
	t.Setenv("HOME", home)

	p, _ := newPathPicker("", true, 60, 8)
	for _, r := range ".." {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := p.CurrentDirectory(); got != root {
		t.Fatalf("CurrentDirectory = %q, want parent %q", got, root)
	}
	if got := p.query(); got != "" {
		t.Fatalf("query = %q, want empty parent listing query", got)
	}
	if got := p.SelectedPath(); got != root {
		t.Fatalf("SelectedPath = %q, want parent %q", got, root)
	}
	if got := pathPickerFilteredNames(p); !containsString(got, "other-user") {
		t.Fatalf("filtered names = %#v, want parent directory contents", got)
	}
}

func TestPathPickerViewScrollsToCursor(t *testing.T) {
	tmp := t.TempDir()
	for i := range 12 {
		mustMkdir(t, filepath.Join(tmp, fmt.Sprintf("dir-%02d", i)))
	}

	p, _ := newPathPicker(tmp, false, 60, 4)
	p.cursor = 10

	out := p.View(defaultPalette())
	if strings.Contains(out, "dir-01/") {
		t.Fatalf("view should scroll away from early rows when cursor is low:\n%s", out)
	}
	if !strings.Contains(out, "dir-10") {
		t.Fatalf("view should include selected low row:\n%s", out)
	}
}

func TestPathPickerSelectedRowHasNoBackgroundFill(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))

	p, _ := newPathPicker(tmp, false, 60, 4)
	out := p.View(defaultPalette())
	row := renderedLineContaining(out, "alpha/")
	if row == "" {
		t.Fatalf("missing selected row:\n%s", out)
	}
	if strings.Contains(row, "\x1b[48;") || strings.Contains(row, "\x1b[4") {
		t.Fatalf("selected file picker row should not use background fill, got %q", row)
	}
}

func TestModelFilePickerResizesWithWindow(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))

	m := baseModel(nil)
	m.width = 120
	m.height = 80
	m.openFilePicker("Dots repo path", tmp, false)

	got := drive(m, tea.WindowSizeMsg{Width: 50, Height: 24})
	if got.dotsFilePicker.width != filePickerContentWidth(got) {
		t.Fatalf("picker width = %d, want %d", got.dotsFilePicker.width, filePickerContentWidth(got))
	}
	if got.dotsFilePicker.Height() != filePickerListHeight(got) {
		t.Fatalf("picker height = %d, want %d", got.dotsFilePicker.Height(), filePickerListHeight(got))
	}
	popup := renderPopupFrame(got.palette, renderFilePickerPopup(got), filePickerPopupFrame(got))
	for _, line := range strings.Split(popup, "\n") {
		if lipgloss.Width(line) > got.width {
			t.Fatalf("popup line width = %d, terminal width = %d:\n%s", lipgloss.Width(line), got.width, popup)
		}
	}
}

func TestModelFilePickerAcceptsTypedCompletedPath(t *testing.T) {
	tmp := t.TempDir()
	selected := filepath.Join(tmp, "alpha")
	mustMkdir(t, selected)
	mustMkdir(t, filepath.Join(tmp, "cider"))

	m := baseModel(nil)
	m.openFilePicker("Dots repo path", tmp, false)
	got := drive(m,
		tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)},
		tea.KeyPressMsg{Code: 'a', Text: "a"},
		tea.KeyPressMsg{Code: tea.KeyTab},
		pressEnter(),
	)

	if got.settings.DotsRepo != "" {
		t.Fatalf("settings.DotsRepo = %q, want unchanged until app result", got.settings.DotsRepo)
	}
	if got.showFilePicker {
		t.Fatal("picker should close after accepting path")
	}
	if !got.dotsLoading {
		t.Fatal("dots operation should start after accepting path")
	}
}

func TestTildePathUsesHomeAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	selected := filepath.Join(home, "dotfiles")
	mustMkdir(t, selected)

	if got := tildePath(selected); got != "~/dotfiles" {
		t.Fatalf("tildePath() = %q, want ~/dotfiles", got)
	}
}

func TestModelFilePickerDefersDotsRepoSettingsToAppResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	selected := filepath.Join(home, "dotfiles")
	mustMkdir(t, selected)

	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.DotsRepo = "~/old-dotfiles"

	cmds := m.acceptFilePickerPath(selected)

	if m.settings.DotsRepo != "~/old-dotfiles" {
		t.Fatalf("settings.DotsRepo = %q, want unchanged until app result", m.settings.DotsRepo)
	}
	if !m.dotsLoading {
		t.Fatal("dots operation should start after accepting the repo path")
	}
	if len(cmds) == 0 {
		t.Fatal("accepting the repo path should queue the app save/sync command")
	}
}

func TestModelFilePickerCapturesQuitKey(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "quitdir"))

	m := baseModel(nil)
	m.openFilePicker("Dots repo path", tmp, false)
	got := drive(m, tea.KeyPressMsg{Code: 'q', Text: "q"})

	if got.confirmQuit {
		t.Fatal("q should be captured by picker input, not trigger quit confirmation")
	}
	if !got.showFilePicker {
		t.Fatal("picker should remain open after typing q")
	}
	if got.dotsFilePicker.input.Value() != tmp+"q" {
		t.Fatalf("picker input = %q, want typed q path", got.dotsFilePicker.input.Value())
	}
}

func TestModelFilePickerEscClosesPathInput(t *testing.T) {
	tmp := t.TempDir()
	m := baseModel(nil)
	m.openFilePicker("Dots repo path", tmp, false)

	got := drive(m, pressEsc())
	if got.showFilePicker {
		t.Fatal("esc should close picker")
	}
}

func TestModelFilePickerKeepsInvalidTypedPathOpen(t *testing.T) {
	tmp := t.TempDir()
	m := baseModel(nil)
	m.mode = viewSettings
	m.openFilePicker("Dots repo path", tmp, false)

	got := drive(m,
		tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)},
		tea.KeyPressMsg{Code: 'm', Text: "m"},
		tea.KeyPressMsg{Code: 'i', Text: "i"},
		tea.KeyPressMsg{Code: 's', Text: "s"},
		tea.KeyPressMsg{Code: 's', Text: "s"},
		pressEnter(),
	)

	if !got.showFilePicker {
		t.Fatal("picker should remain open for an invalid typed path")
	}
	if got.settings.DotsRepo != "" {
		t.Fatalf("settings.DotsRepo = %q, want unchanged", got.settings.DotsRepo)
	}
}

func TestModelFilePickerCapturesPasteFromBackgroundInputs(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))

	m := baseModel(nil)
	m.mode = viewSearch
	m.openFilePicker("Dots repo path", tmp, false)
	got := drive(m, tea.PasteMsg{Content: filepath.Join(tmp, "a")})

	if got.filter.Value() != "" {
		t.Fatalf("search filter = %q, want unchanged while picker is open", got.filter.Value())
	}
	if got.dotsFilePicker.input.Value() != filepath.Join(tmp, "a") {
		t.Fatalf("picker input = %q, want pasted path fragment", got.dotsFilePicker.input.Value())
	}
}

func TestModelFilePickerCapturesMouseWheel(t *testing.T) {
	m := baseModel(threeTools())
	m.cursor = 0
	m.showFilePicker = true

	got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if got.cursor != 0 {
		t.Fatalf("background cursor = %d, want unchanged while picker is open", got.cursor)
	}
}

func TestPathPickerViewShowsInputAndMatches(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "dotfiles"))
	p, _ := newPathPicker(tmp, false, 60, 8)

	out := p.View(defaultPalette())
	if p.input.Value() != tmp {
		t.Fatalf("input path = %q, want %q", p.input.Value(), tmp)
	}
	if !strings.Contains(out, "dotfiles") {
		t.Fatalf("view missing directory match:\n%s", out)
	}
	if strings.Index(out, "dotfiles") > strings.LastIndex(out, "path") {
		t.Fatalf("result rows should render above the path input:\n%s", out)
	}
}

func TestPathPickerFocusedEmptyInputDoesNotRenderPlaceholder(t *testing.T) {
	p, _ := newPathPicker(t.TempDir(), false, 60, 8)
	p.input.SetValue("")
	p.input.Focus()

	out := p.View(defaultPalette())
	if strings.Contains(out, "Path") {
		t.Fatalf("path picker should not render placeholder text:\n%s", out)
	}
}

func TestPathPickerViewAnchorsInputBelowFixedResultArea(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "dotfiles"))
	p, _ := newPathPicker(tmp, false, 60, 8)
	p.input.SetValue(filepath.Join(tmp, "zzz"))
	p.input.CursorEnd()
	p.syncDirectoryFromInput()
	p.applyFilter()

	out := p.View(defaultPalette())
	if h := lipgloss.Height(out); h != p.Height()+2 {
		t.Fatalf("view height = %d, want fixed picker height %d:\n%s", h, p.Height()+2, out)
	}
	lines := strings.Split(out, "\n")
	if got := len(lines); got != p.Height()+2 {
		t.Fatalf("line count = %d, want %d:\n%s", got, p.Height()+2, out)
	}
	if !strings.Contains(lines[len(lines)-1], "path") {
		t.Fatalf("last line should be the path input, got %q in:\n%s", lines[len(lines)-1], out)
	}
	if !strings.Contains(out, "no matches") {
		t.Fatalf("view missing no-match state:\n%s", out)
	}
}

func TestExpandPath_PreservesUnsupportedTildePrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	if err := os.Chdir(working); err != nil {
		t.Fatal(err)
	}
	working, err = os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(startWD)
	})

	want := filepath.Clean(filepath.Join(working, "~alice/.config"))
	got := expandPath("~alice/.config")
	if got != want {
		t.Fatalf("expandPath(~alice/.config) = %q, want %q", got, want)
	}
}

func TestExpandPath_ExpandsEnvironmentVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := expandPath("$HOME/.config")
	want := filepath.Join(home, ".config")
	if got != want {
		t.Fatalf("expandPath($HOME/.config) = %q, want %q", got, want)
	}
}

func TestExpandPathPreserveInput_PreservesUnsupportedTildePrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	if err := os.Chdir(working); err != nil {
		t.Fatal(err)
	}
	working, err = os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(startWD)
	})

	want := filepath.Clean(filepath.Join(working, "~alice/.config"))
	got := expandPathPreserveInput("~alice/.config")
	if got != want {
		t.Fatalf("expandPathPreserveInput(~alice/.config) = %q, want %q", got, want)
	}
}

func TestExpandPathPreserveInput_ExpandsEnvironmentVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := expandPathPreserveInput("$HOME/.config")
	want := filepath.Join(home, ".config")
	if got != want {
		t.Fatalf("expandPathPreserveInput($HOME/.config) = %q, want %q", got, want)
	}
}

func TestExpandPathExpandsHomeTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := expandPath("~/.config")
	want := filepath.Join(home, ".config")
	if got != want {
		t.Fatalf("expandPath(~/.config) = %q, want %q", got, want)
	}
}

func TestPathPickerContainsString(t *testing.T) {
	values := []string{"alpha", "bravo"}
	if !containsString(values, "alpha") || containsString(values, "charlie") {
		t.Fatalf("containsString failed: %#v", values)
	}
}

func TestPathPickerContainsString_Empty(t *testing.T) {
	if containsString(nil, "alpha") {
		t.Fatal("containsString on empty slice should be false")
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func pathPickerFilteredNames(p pathPickerModel) []string {
	names := make([]string, 0, len(p.filtered))
	for _, entry := range p.filtered {
		names = append(names, entry.name)
	}
	sort.Strings(names)
	return names
}

func findFilteredChildren(t *testing.T, dir, query string, allowFiles bool) []string {
	t.Helper()
	out, err := exec.Command("find", dir).Output()
	if err != nil {
		t.Fatalf("find %s: %v", dir, err)
	}
	dir = pathClean(dir)
	query = strings.ToLower(query)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		path := pathClean(line)
		if path == dir || pathClean(filepath.Dir(path)) != dir {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !allowFiles && !info.IsDir() {
			continue
		}
		name := filepath.Base(path)
		if query != "" && !strings.HasPrefix(strings.ToLower(name), query) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
