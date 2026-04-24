package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/dots"
)

type pathPickerModel struct {
	input      textinput.Model
	cwd        string
	entries    []pathPickerEntry
	filtered   []pathPickerEntry
	cursor     int
	width      int
	height     int
	allowFiles bool
	showHidden bool
	err        error

	// Kept as public-looking fields so picker-focused tests can assert the
	// same TUI-owned bounds and cursor contract the old filepicker exposed.
	AutoHeight bool
	Cursor     string
}

type pathPickerEntry struct {
	name  string
	path  string
	isDir bool
}

func newPathPicker(currentPath string, allowFiles bool, width, height int) (pathPickerModel, tea.Cmd) {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Path"
	input.ShowSuggestions = false
	input.SetWidth(pathPickerInputWidth(width))
	input.SetVirtualCursor(true)
	cmd := input.Focus()

	p := pathPickerModel{
		input:      input,
		width:      max(width, 1),
		height:     max(height, 1),
		allowFiles: allowFiles,
		showHidden: true,
		Cursor:     "›",
	}
	p.setStartPath(currentPath)
	p.refreshEntries()
	p.applyFilter()
	return p, cmd
}

func (p *pathPickerModel) setStartPath(currentPath string) {
	start := expandPath(currentPath)
	if start != "" {
		if info, err := os.Stat(start); err == nil && info.IsDir() {
			p.cwd = start
			p.input.SetValue(tildePath(start))
			p.input.CursorEnd()
			return
		}
		parent := filepath.Dir(start)
		if parent != "." && parent != "" {
			p.cwd = parent
			p.input.SetValue(tildePath(start))
			p.input.CursorEnd()
			return
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p.cwd = home
		p.input.SetValue("~")
		p.input.CursorEnd()
		return
	}
	p.cwd = string(filepath.Separator)
	p.input.SetValue(p.cwd)
	p.input.CursorEnd()
}

func (p *pathPickerModel) Init() tea.Cmd {
	return p.input.Focus()
}

func (p pathPickerModel) Update(msg tea.Msg) (pathPickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		p.input.SetValue(p.input.Value() + msg.Content)
		p.input.CursorEnd()
		p.syncDirectoryFromInput()
		p.applyFilter()
		return p, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "ctrl+p":
			p.moveCursor(-1)
			return p, nil
		case "down", "ctrl+n":
			p.moveCursor(1)
			return p, nil
		case "pgup":
			p.moveCursor(-p.height)
			return p, nil
		case "pgdown":
			p.moveCursor(p.height)
			return p, nil
		case "tab":
			p.complete()
			return p, nil
		case "right":
			p.descendHighlighted()
			return p, nil
		case "left":
			p.goParent()
			return p, nil
		case "backspace":
			if p.input.Position() == 0 || pathClean(p.input.Value()) == pathClean(p.cwd) {
				p.goParent()
				return p, nil
			}
		}
	}

	var cmd tea.Cmd
	old := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != old {
		p.syncDirectoryFromInput()
		p.applyFilter()
	}
	return p, cmd
}

func (p *pathPickerModel) SetHeight(h int) {
	p.height = max(h, 1)
	if p.cursor >= p.height && len(p.filtered) > 0 {
		p.cursor = min(p.cursor, len(p.filtered)-1)
	}
}

func (p pathPickerModel) Height() int {
	return p.height
}

func (p *pathPickerModel) SetWidth(w int) {
	p.width = max(w, 1)
	p.input.SetWidth(pathPickerInputWidth(p.width))
}

func (p pathPickerModel) CurrentDirectory() string {
	return p.cwd
}

func (p pathPickerModel) HighlightedPath() string {
	if p.cursor >= 0 && p.cursor < len(p.filtered) {
		return p.filtered[p.cursor].path
	}
	return ""
}

func (p pathPickerModel) SelectedPath() string {
	typed := expandPath(p.input.Value())
	if typed != "" && pathClean(typed) != pathClean(p.cwd) {
		if info, err := os.Stat(typed); err == nil && (info.IsDir() || p.allowFiles) {
			return typed
		}
	}
	if h := p.HighlightedPath(); h != "" {
		if info, err := os.Stat(h); err == nil && (info.IsDir() || p.allowFiles) {
			return h
		}
	}
	if typed != "" {
		if info, err := os.Stat(typed); err == nil && (info.IsDir() || p.allowFiles) {
			return typed
		}
	}
	if p.cwd != "" {
		return p.cwd
	}
	return ""
}

func (p pathPickerModel) View(pal palette) string {
	rows := make([]string, 0, p.height)
	if p.err != nil {
		rows = append(rows, pal.styleErr.Render(p.err.Error()))
	} else if len(p.filtered) == 0 {
		rows = append(rows, pal.styleHelp.Render("no matches"))
	} else {
		visibleRows := min(p.height, len(p.filtered))
		start := 0
		if p.cursor >= visibleRows {
			start = p.cursor - visibleRows + 1
		}
		for i := 0; i < visibleRows; i++ {
			idx := start + i
			entry := p.filtered[idx]
			selected := idx == p.cursor
			rows = append(rows, p.renderRow(entry, selected, pal))
		}
	}
	for len(rows) < p.height {
		rows = append(rows, "")
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(rows, "\n"))
	sb.WriteString("\n\n")
	sb.WriteString(pal.styleHelp.Render("path "))
	sb.WriteString(p.input.View())
	return sb.String()
}

func (p pathPickerModel) renderRow(entry pathPickerEntry, selected bool, pal palette) string {
	prefix := "  "
	if selected {
		prefix = pal.styleCursor.Render(p.Cursor) + " "
	}
	name := entry.name
	if entry.isDir {
		name += string(filepath.Separator)
	}
	available := max(p.width-lipgloss.Width(prefix)-2, 8)
	name = truncatePath(name, available)
	style := pal.styleNormal
	if entry.isDir {
		style = pal.styleProvider
	} else if !p.allowFiles {
		style = pal.styleHelp
	}
	row := prefix + style.Render(name)
	if selected {
		return row
	}
	return row
}

func (p *pathPickerModel) moveCursor(delta int) {
	if len(p.filtered) == 0 {
		p.cursor = 0
		return
	}
	p.cursor = min(max(p.cursor+delta, 0), len(p.filtered)-1)
}

func (p *pathPickerModel) goParent() {
	if p.cwd == "" {
		return
	}
	parent := filepath.Dir(p.cwd)
	if parent == p.cwd {
		return
	}
	p.cwd = parent
	p.input.SetValue(tildePath(parent))
	p.input.CursorEnd()
	p.refreshEntries()
	p.applyFilter()
}

func (p *pathPickerModel) descendHighlighted() {
	h := p.HighlightedPath()
	if h == "" {
		return
	}
	info, err := os.Stat(h)
	if err != nil || !info.IsDir() {
		return
	}
	p.cwd = h
	p.input.SetValue(tildePath(h))
	p.input.CursorEnd()
	p.refreshEntries()
	p.applyFilter()
}

func (p *pathPickerModel) complete() {
	if len(p.filtered) == 0 {
		return
	}
	paths := make([]string, 0, len(p.filtered))
	for _, entry := range p.filtered {
		if entry.isDir || p.allowFiles {
			paths = append(paths, entry.path)
		}
	}
	if len(paths) == 0 {
		return
	}
	prefix := longestCommonPrefix(paths)
	if prefix == "" {
		return
	}
	p.input.SetValue(tildePath(prefix))
	p.input.CursorEnd()
	p.syncDirectoryFromInput()
	p.applyFilter()
}

func (p *pathPickerModel) syncDirectoryFromInput() {
	raw := p.input.Value()
	value := expandPathPreserveInput(raw)
	if value == "" {
		return
	}
	parent := filepath.Dir(value)
	if pathInputHasTrailingSeparator(raw) {
		parent = pathClean(value)
	}
	if parent == "" || parent == "." || pathClean(parent) == pathClean(p.cwd) {
		return
	}
	if info, err := os.Stat(parent); err == nil && info.IsDir() {
		p.cwd = parent
		p.refreshEntries()
	}
}

func pathInputHasTrailingSeparator(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return true
	}
	return filepath.Separator != '/' && strings.HasSuffix(path, "/")
}

func (p *pathPickerModel) refreshEntries() {
	p.entries = nil
	p.filtered = nil
	p.err = nil
	entries, err := os.ReadDir(p.cwd)
	if err != nil {
		p.err = fmt.Errorf("read %s: %w", p.cwd, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !p.showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		p.entries = append(p.entries, pathPickerEntry{
			name:  name,
			path:  filepath.Join(p.cwd, name),
			isDir: info.IsDir(),
		})
	}
}

func (p *pathPickerModel) applyFilter() {
	query := p.query()
	type scored struct {
		entry pathPickerEntry
		score int
	}
	var matches []scored
	for _, entry := range p.entries {
		if !entry.isDir && !p.allowFiles {
			continue
		}
		score, ok := matchPathPickerEntry(entry.name, query)
		if ok {
			matches = append(matches, scored{entry: entry, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if a.entry.isDir != b.entry.isDir {
			return a.entry.isDir
		}
		return strings.ToLower(a.entry.name) < strings.ToLower(b.entry.name)
	})
	p.filtered = p.filtered[:0]
	suggestions := make([]string, 0, len(matches))
	for _, match := range matches {
		p.filtered = append(p.filtered, match.entry)
		suggestions = append(suggestions, tildePath(match.entry.path))
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = max(len(p.filtered)-1, 0)
	}
	p.input.SetSuggestions(suggestions)
}

func (p pathPickerModel) query() string {
	raw := p.input.Value()
	value := expandPathPreserveInput(raw)
	if value == "" {
		return ""
	}
	if pathClean(value) == pathClean(p.cwd) {
		if !pathInputHasTrailingSeparator(raw) && filepath.Base(value) == "." {
			return "."
		}
		return ""
	}
	if pathInputHasTrailingSeparator(raw) {
		return ""
	}
	if rel, err := filepath.Rel(p.cwd, value); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		parts := strings.Split(rel, string(filepath.Separator))
		return parts[0]
	}
	return filepath.Base(value)
}

func matchPathPickerEntry(name, query string) (int, bool) {
	if query == "" {
		return 2, true
	}
	nameLower := strings.ToLower(name)
	queryLower := strings.ToLower(query)
	if strings.HasPrefix(nameLower, queryLower) {
		return 0, true
	}
	if fuzzySubsequence(nameLower, queryLower) {
		return 1, true
	}
	return 0, false
}

func fuzzySubsequence(s, query string) bool {
	if query == "" {
		return true
	}
	i := 0
	q := []rune(query)
	for _, r := range s {
		if r == q[i] {
			i++
			if i == len(q) {
				return true
			}
		}
	}
	return false
}

func longestCommonPrefix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	prefix := paths[0]
	for _, path := range paths[1:] {
		for !strings.HasPrefix(path, prefix) {
			rs := []rune(prefix)
			prefix = string(rs[:len(rs)-1])
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if expanded, err := dots.ExpandPath(path); err == nil {
		path = expanded
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
	}
	return filepath.Clean(path)
}

func expandPathPreserveInput(path string) string {
	if path == "" {
		return ""
	}
	if expanded, err := dots.ExpandPath(path); err == nil {
		path = expanded
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
	}
	return path
}

func pathClean(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func pathPickerInputWidth(width int) int {
	return max(width-5, 8)
}
