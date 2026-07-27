package tui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/provider"
)

// wedge ignores cancellation until test cleanup, modeling a subprocess that outlives its deadline.
type wedge struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func newWedge(t *testing.T) *wedge {
	t.Helper()
	w := &wedge{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(w.release) })
	return w
}

func (w *wedge) block() {
	w.once.Do(func() { close(w.started) })
	<-w.release
}

type wedgedListProvider struct {
	okProvider
	wedge *wedge
}

func (p *wedgedListProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	p.wedge.block()
	return nil, nil
}

type wedgedDescribeProvider struct {
	okProvider
	wedge *wedge
}

func (p *wedgedDescribeProvider) Describe(_ context.Context, _ provider.Tool) (string, error) {
	p.wedge.block()
	return "", nil
}

type wedgedSearchProvider struct {
	okProvider
	wedge *wedge
}

type wedgedAvailableProvider struct {
	okProvider
	wedge *wedge
}

func (p *wedgedAvailableProvider) Available(_ context.Context) (bool, error) {
	p.wedge.block()
	return true, nil
}

func (p *wedgedSearchProvider) Search(_ context.Context, _ string) ([]provider.SearchResult, error) {
	p.wedge.block()
	return nil, nil
}

func shortenRefreshTimeouts(t *testing.T) {
	t.Helper()
	previous := []*time.Duration{&discoveryScanTimeout, &descriptionRefreshTimeout, &toolSnapshotTimeout, &searchTimeout, &doctorTimeout}
	saved := make([]time.Duration, len(previous))
	for i, d := range previous {
		saved[i] = *d
		*d = 50 * time.Millisecond
	}
	t.Cleanup(func() {
		for i, d := range previous {
			*d = saved[i]
		}
	})
}

func awaitMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- cmd() }()
	select {
	case msg := <-msgs:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("command never returned for a wedged provider")
		return nil
	}
}

func TestDoRefreshDiscovered_WedgedProviderStillEmitsMsg(t *testing.T) {
	shortenRefreshTimeouts(t)
	prov := &wedgedListProvider{okProvider: okProvider{name: "brew"}, wedge: newWedge(t)}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	ch := make(chan progressUpdate, 4)
	msg, ok := awaitMsg(t, m.doRefreshDiscovered(3, ch, 1)).(discoveredRefreshedMsg)
	if !ok {
		t.Fatal("expected discoveredRefreshedMsg")
	}
	if msg.gen != 3 {
		t.Fatalf("gen = %d, want 3", msg.gen)
	}
	if !errors.Is(msg.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", msg.err)
	}
}

func TestDoRefreshDescriptions_WedgedProviderStillEmitsMsg(t *testing.T) {
	shortenRefreshTimeouts(t)
	prov := &wedgedDescribeProvider{okProvider: okProvider{name: "brew"}, wedge: newWedge(t)}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	ch := make(chan progressUpdate, 4)
	msg, ok := awaitMsg(t, m.doRefreshDescriptions(5, ch, 1)).(descRefreshDoneMsg)
	if !ok {
		t.Fatal("expected descRefreshDoneMsg")
	}
	if msg.gen != 5 {
		t.Fatalf("gen = %d, want 5", msg.gen)
	}
	if !errors.Is(msg.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", msg.err)
	}
}

func TestDoSearch_WedgedProviderReportsTimeout(t *testing.T) {
	shortenRefreshTimeouts(t)
	prov := &wedgedSearchProvider{okProvider: okProvider{name: "brew"}, wedge: newWedge(t)}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()

	msg, ok := awaitMsg(t, m.doSearch(ctx, "ripgrep", 2)).(searchResultsMsg)
	if !ok {
		t.Fatal("expected searchResultsMsg so the spinner stops; a wedged search must not return nil")
	}
	if msg.err == nil {
		t.Fatal("timed-out search reported no error")
	}
}

func TestDoRunDoctor_WedgedProviderStillEmitsMsg(t *testing.T) {
	shortenRefreshTimeouts(t)
	prov := &wedgedAvailableProvider{okProvider: okProvider{name: "brew"}, wedge: newWedge(t)}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg, ok := awaitMsg(t, m.doRunDoctor()).(doctorDoneMsg)
	if !ok {
		t.Fatal("expected doctorDoneMsg")
	}
	if !errors.Is(msg.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", msg.err)
	}
}

func TestRunBounded_ReturnsWhenWorkIgnoresCancellation(t *testing.T) {
	t.Parallel()
	w := newWedge(t)

	start := time.Now()
	err := runBounded(context.Background(), 50*time.Millisecond, nil, func(context.Context) error {
		w.block()
		return nil
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runBounded took %s, want it to return at the deadline", elapsed)
	}
	<-w.started
}

func TestBoundedToolSnapshot_ReturnsWhenProviderWedges(t *testing.T) {
	shortenRefreshTimeouts(t)
	prov := &wedgedListProvider{okProvider: okProvider{name: "brew"}, wedge: newWedge(t)}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})

	done := make(chan error, 1)
	go func() {
		_, err := boundedToolSnapshot(context.Background(), a)
		done <- err
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("boundedToolSnapshot never returned")
	}
}
