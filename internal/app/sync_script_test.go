package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	isync "github.com/lkshrk/omni/internal/sync"
)

func TestLastNonEmptyLine(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/usr/bin\n/new/bin\n": "/new/bin",
		"/only/bin":            "/only/bin",
		"  /trimmed/bin  \n\n": "/trimmed/bin",
		"":                     "",
		"\n\n":                 "",
	}
	for in, want := range cases {
		if got := lastNonEmptyLine(in); got != want {
			t.Errorf("lastNonEmptyLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPass1InstalledScriptTool(t *testing.T) {
	t.Parallel()
	none := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall, Tool: provider.Tool{Provider: "brew"}},
	}}
	if pass1InstalledScriptTool(none) {
		t.Error("pass1InstalledScriptTool = true; want false (no script op)")
	}
	yes := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall, Tool: provider.Tool{Provider: "script"}},
	}}
	if !pass1InstalledScriptTool(yes) {
		t.Error("pass1InstalledScriptTool = false; want true (script install op)")
	}
	if pass1InstalledScriptTool(nil) {
		t.Error("pass1InstalledScriptTool(nil) = true; want false")
	}
}

func TestRefreshPathAfterScriptInstalls_AppliesNewPath(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "/bin/sh -lic",
		Response: executor.MockCall{Stdout: "/new/bin:/usr/bin"},
	}).WithFallback(executor.MockCall{})
	pass1 := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall, Tool: provider.Tool{Provider: "script"}},
	}}
	var got string
	refreshPathAfterScriptInstalls(context.Background(), mock, pass1, func(p string) { got = p })
	if got != "/new/bin:/usr/bin" {
		t.Errorf("new PATH = %q, want /new/bin:/usr/bin", got)
	}
}

func TestRefreshPathAfterScriptInstalls_SkipsWithoutScriptInstall(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	pass1 := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall, Tool: provider.Tool{Provider: "brew"}},
	}}
	called := false
	refreshPathAfterScriptInstalls(context.Background(), mock, pass1, func(string) { called = true })
	if called {
		t.Error("setenv called; want skipped when no script tool installed")
	}
}

func TestRefreshPathAfterScriptInstalls_ShellErrorKeepsPath(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	mock := executor.NewMatchMock(executor.MatchRule{
		Pattern:  "/bin/sh -lic",
		Response: executor.MockCall{Err: context.DeadlineExceeded},
	}).WithFallback(executor.MockCall{})
	pass1 := &isync.SyncResult{Ops: []isync.SyncOp{
		{Kind: isync.OpInstall, Tool: provider.Tool{Provider: "script"}},
	}}
	called := false
	refreshPathAfterScriptInstalls(context.Background(), mock, pass1, func(string) { called = true })
	if called {
		t.Error("setenv called; want skipped when shell probe errors")
	}
}
