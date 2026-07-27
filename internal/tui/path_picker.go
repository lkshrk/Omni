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

	"github.com/lkshrk/omni/internal/app"
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

	completionActive  bool
	completionEntries []pathPickerEntry
	completionIndex   int

	// Public-looking so picker-focused tests can assert the same TUI-owned bounds and cursor contract the old filepicker exposed.
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
	input.Placeholder = ""
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
	currentPath = normalizeRepeatedHomeInput(currentPath)
	start := expandPath(currentPath)
	if start != "" {
		if info, err := os.Stat(start); err == nil && info.IsDir() {
			p.cwd = start
			p.input.SetValue(tildePathForInput(start))
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
		p.input.SetValue("~/")
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
		if pathPasteReplacesInput(msg.Content) {
			p.input.SetValue(msg.Content)
		} else {
			p.input.SetValue(p.input.Value() + msg.Content)
		}
		p.resetCompletion()
		p.normalizeInputValue()
		p.input.CursorEnd()
		p.syncDirectoryFromInput()
		p.applyFilter()
		return p, p.input.Focus()
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
		case "ctrl+left":
			p.goParent()
			return p, nil
		case "ctrl+right":
			p.descendHighlighted()
			return p, nil
		}
	}

	var cmd tea.Cmd
	old := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != old {
		p.resetCompletion()
		p.normalizeInputValue()
		p.syncDirectoryFromInput()
		p.applyFilter()
	}
	return p, cmd
}

func pathPasteReplacesInput(content string) bool {
	if strings.HasPrefix(content, "~") {
		return true
	}
	return filepath.IsAbs(content)
}

func (p *pathPickerModel) normalizeInputValue() {
	normalized := normalizeRepeatedHomeInput(p.input.Value())
	if normalized == p.input.Value() {
		return
	}
	p.input.SetValue(normalized)
	p.input.CursorEnd()
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
	typed := expandPath(normalizeRepeatedHomeInput(p.input.Value()))
	if typed != "" {
		if info, err := os.Stat(typed); err == nil && (info.IsDir() || p.allowFiles) {
			return typed
		}
	}
	if h := p.HighlightedPath(); h != "" {
		if info, err := os.Stat(h); err == nil && (info.IsDir() || p.allowFiles) {
			return h
		}
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
	sb.WriteString(p.inputLine(pal))
	return sb.String()
}

func (p pathPickerModel) inputLine(pal palette) string {
	return pal.styleHelp.Render("path ") + renderEmptyAwareTextInputView(pal, p.input, "", pathPickerInputWidth(p.width))
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
	p.resetCompletion()
	p.input.SetValue(tildePathForInput(parent))
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
	p.resetCompletion()
	p.input.SetValue(tildePathForInput(h))
	p.input.CursorEnd()
	p.refreshEntries()
	p.applyFilter()
}

func (p *pathPickerModel) complete() {
	if !p.shouldContinueCompletionCycle() {
		p.completionEntries = p.completableFilteredEntries()
		if len(p.completionEntries) == 0 {
			p.resetCompletion()
			return
		}
		p.completionIndex = completionStartIndex(p.completionEntries, p.HighlightedPath())
		p.completionActive = true
	} else {
		p.completionIndex = (p.completionIndex + 1) % len(p.completionEntries)
	}
	if p.completionIndex < 0 || p.completionIndex >= len(p.completionEntries) {
		p.resetCompletion()
		return
	}
	entry := p.completionEntries[p.completionIndex]
	p.input.SetValue(tildePathForInput(entry.path))
	p.input.CursorEnd()
	p.filtered = append(p.filtered[:0], p.completionEntries...)
	p.cursor = p.completionIndex
}

func (p pathPickerModel) completableFilteredEntries() []pathPickerEntry {
	entries := make([]pathPickerEntry, 0, len(p.filtered))
	for _, entry := range p.filtered {
		if entry.isDir || p.allowFiles {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (p pathPickerModel) shouldContinueCompletionCycle() bool {
	if !p.completionActive || len(p.completionEntries) == 0 || p.completionIndex < 0 || p.completionIndex >= len(p.completionEntries) {
		return false
	}
	current := expandPathPreserveInput(p.input.Value())
	if current == "" {
		return false
	}
	return pathClean(current) == pathClean(p.completionEntries[p.completionIndex].path)
}

func (p *pathPickerModel) resetCompletion() {
	p.completionActive = false
	p.completionEntries = nil
	p.completionIndex = 0
}

func completionStartIndex(entries []pathPickerEntry, highlighted string) int {
	if highlighted == "" {
		return 0
	}
	for i, entry := range entries {
		if pathClean(entry.path) == pathClean(highlighted) {
			return i
		}
	}
	return 0
}

func (p *pathPickerModel) syncDirectoryFromInput() {
	p.normalizeInputValue()
	dir, _, ok := p.inputDirectoryAndQuery()
	if !ok {
		p.err = nil
		p.entries = nil
		p.filtered = nil
		return
	}
	if pathClean(dir) == pathClean(p.cwd) {
		if p.entries == nil || p.err != nil {
			p.refreshEntries()
		}
		return
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		p.cwd = dir
		p.refreshEntries()
		return
	}
	p.err = nil
	p.entries = nil
	p.filtered = nil
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
	p.syncDirectoryFromInput()
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
	if len(matches) == 0 {
		if entry, ok := p.repeatedHomeBasenameFallback(query); ok {
			matches = append(matches, scored{entry: entry, score: 0})
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

func (p pathPickerModel) repeatedHomeBasenameFallback(query string) (pathPickerEntry, bool) {
	if query == "" {
		return pathPickerEntry{}, false
	}
	raw := normalizeRepeatedHomeInput(p.input.Value())
	rest := strings.TrimPrefix(raw, "~/")
	if !strings.HasPrefix(raw, "~/") || strings.Contains(rest, "/") || (filepath.Separator != '/' && strings.Contains(rest, string(filepath.Separator))) {
		return pathPickerEntry{}, false
	}
	home := pathPickerHomeDir()
	if pathClean(p.cwd) != pathClean(home) {
		return pathPickerEntry{}, false
	}
	homeBase := filepath.Base(filepath.Clean(home))
	if homeBase == "" || homeBase == "." || homeBase == string(filepath.Separator) {
		return pathPickerEntry{}, false
	}
	if !strings.HasPrefix(strings.ToLower(homeBase), strings.ToLower(query)) {
		return pathPickerEntry{}, false
	}
	return pathPickerEntry{name: homeBase, path: home, isDir: true}, true
}

func (p pathPickerModel) query() string {
	_, query, ok := p.inputDirectoryAndQuery()
	if !ok {
		return ""
	}
	return query
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
	return 0, false
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if expanded, err := app.DotExpandPath(path); err == nil {
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
	path = normalizeRepeatedHomeInput(path)
	if path == "" {
		return ""
	}
	if expanded, err := app.DotExpandPath(path); err == nil {
		path = expanded
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
	}
	return path
}

func (p pathPickerModel) inputDirectoryAndQuery() (string, string, bool) {
	raw := normalizeRepeatedHomeInput(p.input.Value())
	if raw == "" || raw == "~" {
		return pathPickerHomeDir(), "", true
	}
	if pathInputHasTrailingSeparator(raw) {
		return resolvePathInput(raw, p.cwd), "", true
	}

	full := resolvePathInput(raw, p.cwd)
	base := filepath.Base(raw)
	if base == ".." {
		return full, "", true
	}
	if pathClean(full) == pathClean(p.cwd) && base != "." {
		return p.cwd, "", true
	}

	dirInput := filepath.Dir(raw)
	if dirInput == "." && (strings.HasPrefix(raw, "~") || filepath.IsAbs(raw)) {
		dirInput = raw
	}
	dir := resolvePathInput(dirInput, p.cwd)
	if dir == "" {
		return "", "", false
	}
	return dir, base, true
}

func resolvePathInput(path, baseDir string) string {
	path = normalizeRepeatedHomeInput(path)
	if path == "" || path == "." {
		if baseDir != "" {
			return pathClean(baseDir)
		}
		return pathPickerHomeDir()
	}
	if expanded, err := app.DotExpandPath(path); err == nil {
		path = expanded
	}
	if filepath.IsAbs(path) {
		return pathClean(path)
	}
	if baseDir == "" {
		baseDir = pathPickerHomeDir()
	}
	return pathClean(filepath.Join(baseDir, path))
}

func pathClean(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func pathPickerHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return string(filepath.Separator)
}

func normalizeRepeatedHomeInput(path string) string {
	if path == "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	homeBase := filepath.Base(filepath.Clean(home))
	if homeBase == "" || homeBase == "." || homeBase == string(filepath.Separator) {
		return path
	}
	prefix := "~/" + homeBase + "/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	if _, err := os.Stat(filepath.Join(home, homeBase)); err == nil {
		return path
	}
	return "~/" + strings.TrimPrefix(path, prefix)
}

func pathPickerInputWidth(width int) int {
	return max(width-5, 8)
}

func tildePathForInput(path string) string {
	value := tildePath(path)
	if value == "~" {
		return "~/"
	}
	return value
}
