package app_test

// Tests for HCL-26: detect installed tools via PATH when the configured
// provider is unavailable or unregistered on the current host.
//
// Acceptance criteria:
//   A1: provider unavailable/unregistered + binary on PATH → tool marked installed (InstalledWith="")
//   A2: provider available → PATH detection does not fire (provider's authoritative answer wins)
//   A3: binary absent → tool remains not-installed
//   A5: no oscillation — RefreshInstalled then Sync(DryRun) emits no OpInstall

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	isync "github.com/lkshrk/omni/internal/sync"
)

// makeBin creates a zero-byte executable named binName in a temp dir and
// prepends that dir to PATH for the duration of the test.
func makeBin(t *testing.T, binName string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, binName)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("makeBin %s: %v", binName, err)
	}
	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+":"+old)
}

// TestRefreshInstalled_PathDetection_ProviderUnavailable checks A1:
// configured provider is registered but Available=false; binary is on PATH.
// Expected: tool marked Installed=true, InstalledWith="".
func TestRefreshInstalled_PathDetection_ProviderUnavailable(t *testing.T) {
	makeBin(t, "mytool")

	brew := &stubProvider{name: "brew", available: false}
	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"mytool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if !tc.Installed {
				t.Errorf("mytool.Installed = false, want true (A1/unavailable)")
			}
			if tc.InstalledWith != "" {
				t.Errorf("mytool.InstalledWith = %q, want empty (A1)", tc.InstalledWith)
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}

// TestRefreshInstalled_PathDetection_ProviderUnregistered checks A1 for the
// unregistered case: no provider is registered at all (registry.Get returns !ok).
func TestRefreshInstalled_PathDetection_ProviderUnregistered(t *testing.T) {
	makeBin(t, "mytool")

	// No providers registered at all.
	a, cfgPath := newImportApp(t)

	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"mytool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if !tc.Installed {
				t.Errorf("mytool.Installed = false, want true (A1/unregistered)")
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}

// TestRefreshInstalled_PathDetection_ProviderAvailable_NotBypassed checks A2:
// when provider IS available, its authoritative "not installed" answer must win.
// Having the binary on PATH must NOT cause a false positive.
func TestRefreshInstalled_PathDetection_ProviderAvailable_NotBypassed(t *testing.T) {
	makeBin(t, "mytool") // on PATH — must NOT trigger installed

	brew := &stubProvider{name: "brew", available: true} // available, but mytool not in its installed list
	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"mytool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if tc.Installed {
				t.Errorf("mytool.Installed = true despite available provider reporting not-installed (A2 violation)")
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}

// TestRefreshInstalled_PathDetection_BinaryAbsent checks A3:
// provider unavailable AND binary not on PATH → tool must not be marked installed.
func TestRefreshInstalled_PathDetection_BinaryAbsent(t *testing.T) {
	// Override PATH to an empty dir so no binary can be found.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	brew := &stubProvider{name: "brew", available: false}
	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"definitelymissingtool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "definitelymissingtool"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("definitelymissingtool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "definitelymissingtool" {
			if tc.Installed {
				t.Errorf("definitelymissingtool.Installed = true despite binary absent (A3 violation)")
			}
			return
		}
	}
	t.Error("definitelymissingtool not found in ListTools output")
}

// TestRefreshInstalled_PathDetection_NoOscillation checks A5:
// after RefreshInstalled marks a PATH-detected tool installed, a subsequent
// Sync(DryRun) must not emit OpInstall for it.
func TestRefreshInstalled_PathDetection_NoOscillation(t *testing.T) {
	makeBin(t, "mytool")

	brew := &stubProvider{name: "brew", available: false}
	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"mytool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	result, err := a.Sync(context.Background(), isync.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync(DryRun): %v", err)
	}
	for _, op := range result.Ops {
		if op.Tool.Name == "mytool" && op.Kind == isync.OpInstall {
			t.Errorf("Sync(DryRun) emitted OpInstall for PATH-detected mytool (A5 oscillation): %+v", op)
		}
	}
}

// TestRefreshProviderInstalled_PathDetection_ProviderUnavailable checks A1 via
// RefreshProviderInstalled (the scoped parallel-refresh path).
func TestRefreshProviderInstalled_PathDetection_ProviderUnavailable(t *testing.T) {
	makeBin(t, "mytool")

	brew := &stubProvider{name: "brew", available: false}
	a, cfgPath := newImportApp(t, brew)

	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"mytool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("mytool")},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.RefreshProviderInstalled(context.Background(), "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if !tc.Installed {
				t.Errorf("mytool.Installed = false via RefreshProviderInstalled, want true (A1)")
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}
