package tui_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/tui"
)

// newTestApp creates a minimal App backed by an in-memory DB in a temp dir.
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.Init(context.Background()); err != nil {
		t.Fatalf("App.Init: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// driveModel feeds messages sequentially into a model and returns the final state.
func driveModel(m tea.Model, msgs ...tea.Msg) tea.Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

// TestTUI_InitialRender verifies the TUI renders without panicking.
func TestTUI_InitialRender(t *testing.T) {
	a := newTestApp(t)
	m := tui.New(a, context.Background())

	out := m.View()
	if len(out.Content) == 0 {
		t.Error("expected non-empty initial view")
	}
}

// TestTUI_QuitWithQ verifies that pressing q does not panic and returns non-nil model.
func TestTUI_QuitWithQ(t *testing.T) {
	a := newTestApp(t)
	m := tui.New(a, context.Background())

	m2 := driveModel(m,
		tea.WindowSizeMsg{Width: 120, Height: 40},
		tea.KeyPressMsg{Code: 'q'},
	)
	if m2 == nil {
		t.Error("expected non-nil model after q")
	}
}

// TestTUI_ViewDoesNotPanic verifies the View method doesn't panic under various sizes.
func TestTUI_ViewDoesNotPanic(t *testing.T) {
	a := newTestApp(t)
	m := tui.New(a, context.Background())

	sizes := [][2]int{{120, 40}, {80, 24}, {40, 20}, {200, 60}}
	for _, sz := range sizes {
		m2, _ := m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		out := m2.(interface {
			View() tea.View
		}).View()
		if out.Content == "" {
			t.Errorf("empty view at %dx%d", sz[0], sz[1])
		}
	}
}

// TestTUI_SyncMessageNoPanic verifies pressing S does not panic.
func TestTUI_SyncMessageNoPanic(t *testing.T) {
	a := newTestApp(t)
	m := tui.New(a, context.Background())

	m2 := driveModel(m,
		tea.WindowSizeMsg{Width: 120, Height: 40},
		tea.KeyPressMsg{Code: 'S', Text: "S"},
	)
	if m2 == nil {
		t.Error("expected non-nil model after S")
	}
}
