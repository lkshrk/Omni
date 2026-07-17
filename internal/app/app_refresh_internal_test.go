package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestFetchLatestGitHubReleaseCached_CanceledLeaderDoesNotPoisonWaiter(t *testing.T) {
	var calls int32
	firstStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(firstStarted)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":1,"tag_name":"v2.0.0","published_at":"2026-07-17T00:00:00Z","assets":[]}`)
	}))
	t.Cleanup(server.Close)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(server.URL, server.Client())
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := a.fetchLatestGitHubReleaseCached(leaderCtx, nil, "owner", "repo")
		leaderErr <- err
	}()
	<-firstStarted

	type lookupResult struct {
		release githubRelease
		err     error
	}
	waiterResult := make(chan lookupResult, 1)
	go func() {
		release, err := a.fetchLatestGitHubReleaseCached(context.Background(), nil, "owner", "repo")
		waiterResult <- lookupResult{release: release, err: err}
	}()
	cancelLeader()

	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want context canceled", err)
	}
	result := <-waiterResult
	if result.err != nil || result.release.TagName != "v2.0.0" {
		t.Fatalf("waiter release=%q error=%v; want v2.0.0, nil", result.release.TagName, result.err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("GitHub calls = %d, want canceled request plus waiter retry", got)
	}
}

func TestFetchLatestGitHubReleaseCached_ScopesCompletedResultsToOperation(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%d,"tag_name":"v%d.0.0","published_at":"2026-07-17T00:00:00Z","assets":[]}`, call, call)
	}))
	t.Cleanup(server.Close)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(server.URL, server.Client())
	firstOperation := make(githubReleaseLookupCache)
	first, err := a.fetchLatestGitHubReleaseCached(context.Background(), firstOperation, "owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.fetchLatestGitHubReleaseCached(context.Background(), firstOperation, "owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.fetchLatestGitHubReleaseCached(context.Background(), make(githubReleaseLookupCache), "owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if first.TagName != "v1.0.0" || again.TagName != "v1.0.0" || second.TagName != "v2.0.0" {
		t.Fatalf("tags = %q, %q, %q; want operation cache then fresh lookup", first.TagName, again.TagName, second.TagName)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("GitHub calls = %d, want one per operation", got)
	}
}
