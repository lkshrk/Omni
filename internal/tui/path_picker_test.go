package tui

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestPathPickerTabCompletesCommonPrefix(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "alpine"))
	mustMkdir(t, filepath.Join(tmp, "cider"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if got, want := p.input.Value(), filepath.Join(tmp, "alp"); got != want {
		t.Fatalf("input after tab = %q, want %q", got, want)
	}
}

func TestPathPickerDisplaysHomeAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, "alpha"))
	mustMkdir(t, filepath.Join(home, "alpine"))

	p, _ := newPathPicker("", false, 60, 8)
	if got := p.input.Value(); got != "~" {
		t.Fatalf("input = %q, want ~", got)
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: '/', Text: string(filepath.Separator)})
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.input.Value(); got != "~/alp" {
		t.Fatalf("input after tab = %q, want ~/alp", got)
	}
	if got := p.SelectedPath(); got != filepath.Join(home, "alpha") && got != filepath.Join(home, "alpine") {
		t.Fatalf("SelectedPath = %q, want one home child", got)
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

func TestPathPickerPasteUpdatesMatches(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))
	mustMkdir(t, filepath.Join(tmp, "cider"))

	p, _ := newPathPicker(tmp, false, 60, 8)
	p, _ = p.Update(tea.PasteMsg{Content: string(filepath.Separator) + "a"})

	if got := p.input.Value(); got != filepath.Join(tmp, "a") {
		t.Fatalf("input after paste = %q, want %q", got, filepath.Join(tmp, "a"))
	}
	if len(p.filtered) != 1 || p.filtered[0].name != "alpha" {
		t.Fatalf("filtered = %#v, want alpha only", p.filtered)
	}
}

func TestPathPickerParentNavigation(t *testing.T) {
	tmp := t.TempDir()
	child := filepath.Join(tmp, "child")
	mustMkdir(t, child)

	p, _ := newPathPicker(child, false, 60, 8)
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyLeft})

	if got := p.CurrentDirectory(); got != tmp {
		t.Fatalf("CurrentDirectory = %q, want parent %q", got, tmp)
	}
	if got := p.input.Value(); got != tmp {
		t.Fatalf("input = %q, want parent %q", got, tmp)
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

	if got.settings.DotsRepo != selected {
		t.Fatalf("settings.DotsRepo = %q, want %q", got.settings.DotsRepo, selected)
	}
	if got.showFilePicker {
		t.Fatal("picker should close after accepting path")
	}
}

func TestModelFilePickerStoresDotsRepoWithHomeAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	selected := filepath.Join(home, "dotfiles")
	mustMkdir(t, selected)

	m := baseModel(nil)
	m.mode = viewSettings
	m.acceptFilePickerPath(selected)

	if m.settings.DotsRepo != "~/dotfiles" {
		t.Fatalf("settings.DotsRepo = %q, want ~/dotfiles", m.settings.DotsRepo)
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

func TestModelFilePickerCapturesPasteFromBackgroundInputs(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "alpha"))

	m := baseModel(nil)
	m.mode = viewSearch
	m.openFilePicker("Dots repo path", tmp, false)
	got := drive(m, tea.PasteMsg{Content: string(filepath.Separator) + "a"})

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
