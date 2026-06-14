package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	isync "github.com/lkshrk/omni/internal/sync"
)

// TestRefreshInstalled_PathDetection_FailureRecordPreserved: active failure records must survive RefreshInstalled — PATH detection must not clear them.
func TestRefreshInstalled_PathDetection_FailureRecordPreserved(t *testing.T) {
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

	// Seed an active failure record for mytool/brew before running RefreshInstalled.
	ctx := context.Background()
	if err := a.DB().MarkFailed(ctx, "mytool", "brew", "mytool", "prior install failure"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if tc.Installed {
				t.Errorf("mytool.Installed = true despite active failure record — retry-failed state was cleared by PATH detection (safety violation)")
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}

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

func TestRefreshInstalled_PathDetection_ProviderAvailable_NotBypassed(t *testing.T) {
	makeBin(t, "mytool")

	brew := &stubProvider{name: "brew", available: true}
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

func TestRefreshInstalled_PathDetection_BinaryAbsent(t *testing.T) {
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

// TestRefreshInstalled_PathDetection_StaleRowCleared verifies that a stale
// Installed=true cache row is corrected when the binary is no longer on PATH
// and the provider is unavailable.
func TestRefreshInstalled_PathDetection_StaleRowCleared(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mytool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

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
		t.Fatalf("RefreshInstalled pass 1: %v", err)
	}

	// Remove binary; next refresh must clear the stale installed row.
	if err := os.Remove(bin); err != nil {
		t.Fatalf("remove bin: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled pass 2: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if tc.Installed {
				t.Errorf("mytool.Installed = true after binary removed — stale row not cleared")
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}

// TestRefreshProviderInstalled_PathDetection_StaleRowCleared mirrors the stale-
// clear check via the scoped RefreshProviderInstalled path.
func TestRefreshProviderInstalled_PathDetection_StaleRowCleared(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mytool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

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
		t.Fatalf("RefreshProviderInstalled pass 1: %v", err)
	}

	if err := os.Remove(bin); err != nil {
		t.Fatalf("remove bin: %v", err)
	}
	if err := a.RefreshProviderInstalled(context.Background(), "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled pass 2: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range tools {
		if tc.Name == "mytool" {
			if tc.Installed {
				t.Errorf("mytool.Installed = true after binary removed via RefreshProviderInstalled — stale row not cleared")
			}
			return
		}
	}
	t.Error("mytool not found in ListTools output")
}

// TestRefreshInstalled_PathDetection_ConfiguredFallback_NotMasked verifies that
// a tool with a configured fallback (any status) is NOT marked installed by PATH
// detection — the fallback status lifecycle (gh?/gh/gh!) must remain visible
// and not be silently overridden because the binary happens to be on PATH.
func TestRefreshInstalled_PathDetection_ConfiguredFallback_NotMasked(t *testing.T) {
	makeBin(t, "mytool")

	brew := &stubProvider{name: "brew", available: false}
	a, cfgPath := newImportApp(t, brew)

	for _, status := range []string{
		config.FallbackStatusUnverified,
		config.FallbackStatusVerified,
		config.FallbackStatusFailed,
		config.FallbackStatusUnresolved,
	} {
		status := status
		t.Run(status, func(t *testing.T) {
			cfg := &config.RootConfig{
				Tools: map[string]config.ToolSpec{
					"mytool": {
						Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "mytool"}},
						Fallback: &config.FallbackSpec{
							Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "example", Repo: "mytool"},
							Status: status,
							Commands: config.FallbackCommands{
								Check: "command -v mytool",
							},
						},
					},
				},
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
						t.Errorf("fallback status %q: mytool.Installed = true -- PATH detection masked configured fallback", status)
					}
					return
				}
			}
			t.Error("mytool not found in ListTools output")
		})
	}
}
