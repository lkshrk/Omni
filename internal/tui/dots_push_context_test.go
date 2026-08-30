package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestDotsPushContextRetainsShutdownAndHardDeadline(t *testing.T) {
	parent, shutdown := context.WithCancel(context.Background())
	m := Model{ctx: parent}
	ctx, cancel := m.newDotsPushContext()
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > dotsPushTimeout {
		t.Fatalf("push deadline = %v, ok=%v", deadline, ok)
	}
	shutdown()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("push context after shutdown = %v", ctx.Err())
	}
}

func TestDotsPushBlocksAnotherPaletteDotsOperation(t *testing.T) {
	m := Model{dotsPushRunning: true}
	ran := false
	cmd := m.runDotsPaletteCommand("Refreshing…", func() tea.Cmd {
		ran = true
		return nil
	})
	if ran || cmd == nil || m.dotsOpGen != 0 || m.statusMsg != "dots push in progress — wait for it to finish" {
		t.Fatalf("blocked operation = ran:%v cmd:%v gen:%d status:%q", ran, cmd != nil, m.dotsOpGen, m.statusMsg)
	}
}

func TestSupersededDotsPushFailureIsSurfaced(t *testing.T) {
	m := Model{dotsPushRunning: true, dotsOpGen: 2}
	errPush := errors.New("remote rejected push")
	cmds := m.handleDotsPushedMsg(dotsPushedMsg{gen: 1, err: errPush})
	if m.dotsPushRunning || len(cmds) == 0 || !m.statusIsErr || m.statusMsg != "✗ remote rejected push" {
		t.Fatalf("stale failure = running:%v cmds:%d err:%v status:%q", m.dotsPushRunning, len(cmds), m.statusIsErr, m.statusMsg)
	}
}
