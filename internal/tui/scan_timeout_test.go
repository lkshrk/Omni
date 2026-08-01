package tui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// wedgedProvider models a package manager whose bulk query never returns and
// ignores context cancellation — the dpkg/apt-lock case that used to leave the
// Tools tab stuck on loading forever.
type wedgedProvider struct {
	okProvider
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func newWedgedProvider(t *testing.T, name string) *wedgedProvider {
	t.Helper()
	p := &wedgedProvider{
		okProvider: okProvider{name: name},
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	t.Cleanup(func() { close(p.release) })
	return p
}

func (p *wedgedProvider) InstalledMap(_ context.Context) (map[string]string, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return nil, nil
}

func shortenProviderScanTimeout(t *testing.T) {
	t.Helper()
	previous := providerScanTimeout
	providerScanTimeout = 50 * time.Millisecond
	t.Cleanup(func() { providerScanTimeout = previous })
}

func TestDoScanProvider_WedgedProviderStillEmitsMsg(t *testing.T) {
	shortenProviderScanTimeout(t)
	prov := newWedgedProvider(t, "brew")
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- m.doScanProvider("brew", 7, nil, 0)() }()

	select {
	case msg := <-msgs:
		got, ok := msg.(providerScannedMsg)
		if !ok {
			t.Fatalf("expected providerScannedMsg, got %T", msg)
		}
		if got.provider != "brew" || got.gen != 7 {
			t.Fatalf("msg = %+v, want brew/gen 7", got)
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("doScanProvider never returned for a wedged provider")
	}
}

func TestProviderScan_WedgedProviderClearsLoadingState(t *testing.T) {
	shortenProviderScanTimeout(t)
	prov := newWedgedProvider(t, "brew")
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	cmds := m.startCurrentProviderScans()
	if len(m.scanningProviders) == 0 {
		t.Fatal("no provider scan started")
	}

	msgs := make(chan tea.Msg, len(cmds))
	for _, cmd := range cmds {
		go func(cmd tea.Cmd) {
			if msg := cmd(); msg != nil {
				msgs <- msg
			}
		}(cmd)
	}

	deadline := time.After(5 * time.Second)
	for len(m.scanningProviders) > 0 {
		select {
		case msg := <-msgs:
			if scanned, ok := msg.(providerScannedMsg); ok {
				m = drive(m, scanned)
			}
		case <-deadline:
			t.Fatalf("scanningProviders never drained: %v", m.scanningProviders)
		}
	}

	if m.statusMsg != "" {
		t.Fatalf("timed-out provider surfaced before refresh settled: statusMsg=%q", m.statusMsg)
	}
	m = drive(m,
		allProvidersDoneMsg{gen: m.scanGen},
		discoveredRefreshedMsg{gen: m.discoveryGen},
		providerOutdatedCheckedMsg{gen: m.scanGen, provider: "brew"},
		outdatedProvidersDoneMsg{gen: m.scanGen},
	)
	if m.providerSnapshotRefreshing {
		t.Fatal("provider snapshot still marked refreshing after allProvidersDoneMsg")
	}
	if m.statusMsg == "" || !m.statusIsErr {
		t.Fatalf("settled provider timeout not surfaced: statusMsg=%q isErr=%v", m.statusMsg, m.statusIsErr)
	}
}
