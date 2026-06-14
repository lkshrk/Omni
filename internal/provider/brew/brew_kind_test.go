package brew_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// brewInfoInstalled is a minimal `brew info --json=v2 --installed` response.
const brewInfoInstalled = `{
  "formulae": [
    {
      "name": "ripgrep",
      "full_name": "ripgrep",
      "homepage": "https://github.com/BurntSushi/ripgrep",
      "desc": "fast search",
      "versions": {"stable": "14.1.0"},
      "urls": {"stable": {"url": "https://github.com/BurntSushi/ripgrep/archive/14.1.0.tar.gz"}},
      "installed": [{"version": "14.1.0", "installed_on_request": true}]
    }
  ],
  "casks": [
    {
      "token": "iterm2",
      "homepage": "https://iterm2.com",
      "desc": "terminal emulator",
      "url": "https://iterm2.com/downloads/stable/iTerm2-3_5_0.zip",
      "installed": "3.5.0",
      "artifacts": []
    }
  ]
}`

// brewListCask is the response to `brew list --cask`.
const brewListCask = "iterm2\n"

// TestInstalledMetadataMap_FormulaKind verifies that formulae get ArtifactKind="formula".
func TestInstalledMetadataMap_FormulaKind(t *testing.T) {
	ctx := context.Background()
	p, _ := newBrew(
		// brew info --json=v2 --installed
		executor.MockCall{Stdout: brewInfoInstalled},
		// brew list --cask
		executor.MockCall{Stdout: brewListCask},
	)

	metadata, err := p.InstalledMetadataMap(ctx)
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}

	entry, ok := metadata["ripgrep"]
	if !ok {
		t.Fatal("ripgrep not found in metadata map")
	}
	if entry.ArtifactKind != "formula" {
		t.Errorf("ripgrep ArtifactKind = %q, want %q", entry.ArtifactKind, "formula")
	}
	if entry.Version != "14.1.0" {
		t.Errorf("ripgrep Version = %q, want %q", entry.Version, "14.1.0")
	}
}

// TestInstalledMetadataMap_CaskKind verifies that casks get ArtifactKind="cask".
func TestInstalledMetadataMap_CaskKind(t *testing.T) {
	ctx := context.Background()
	p, _ := newBrew(
		// brew info --json=v2 --installed
		executor.MockCall{Stdout: brewInfoInstalled},
		// brew list --cask
		executor.MockCall{Stdout: brewListCask},
	)

	metadata, err := p.InstalledMetadataMap(ctx)
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}

	entry, ok := metadata["iterm2"]
	if !ok {
		t.Fatal("iterm2 not found in metadata map")
	}
	if entry.ArtifactKind != "cask" {
		t.Errorf("iterm2 ArtifactKind = %q, want %q", entry.ArtifactKind, "cask")
	}
	if entry.Version != "3.5.0" {
		t.Errorf("iterm2 Version = %q, want %q", entry.Version, "3.5.0")
	}
}

// TestInstalledMetadataMap_CaskNotInInfoResponse verifies that casks that
// appear in `brew list --cask` but are absent from the JSON info response still
// get ArtifactKind="cask" (the fallback path for the seenCasks loop).
func TestInstalledMetadataMap_CaskNotInInfoResponse(t *testing.T) {
	ctx := context.Background()
	const infoNoOtherCask = `{"formulae":[],"casks":[]}`
	const listCask = "unknown-app\n"

	p, _ := newBrew(
		executor.MockCall{Stdout: infoNoOtherCask},
		executor.MockCall{Stdout: listCask},
	)

	metadata, err := p.InstalledMetadataMap(ctx)
	if err != nil {
		t.Fatalf("InstalledMetadataMap: %v", err)
	}

	entry, ok := metadata["unknown-app"]
	if !ok {
		t.Fatal("unknown-app not found in metadata map")
	}
	if entry.ArtifactKind != "cask" {
		t.Errorf("unknown-app ArtifactKind = %q, want %q", entry.ArtifactKind, "cask")
	}
}

// TestUpgrade_UsesPersistedCaskKind verifies that when tool.Options["brew_kind"]
// is "cask", Upgrade uses `brew upgrade --cask <name>` without probing via
// `brew list --versions --cask` (A3, I6).
func TestUpgrade_UsesPersistedCaskKind(t *testing.T) {
	ctx := context.Background()
	// Pre-load: only the upgrade call itself should fire; no list probes.
	p, mock := newBrew(
		executor.MockCall{Stdout: "", Stderr: "", Err: nil}, // upgrade --cask iterm2
	)

	caskTool := provider.Tool{
		Name:     "iterm2",
		Provider: "brew",
		Package:  "iterm2",
		Options:  map[string]string{"brew_kind": "cask"},
	}
	if err := p.Upgrade(ctx, caskTool); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// Verify only one brew call was made and it used --cask.
	calls := mock.Calls
	if len(calls) != 1 {
		t.Fatalf("expected 1 brew call, got %d: %v", len(calls), calls)
	}
	joined := strings.Join(calls[0].Args, " ")
	if !strings.Contains(joined, "--cask") {
		t.Errorf("upgrade args = %q, want --cask in args", joined)
	}
	if strings.Contains(joined, "--formula") {
		t.Errorf("upgrade args = %q, must not contain --formula for a cask", joined)
	}
}

// TestUpgrade_UsesPersistedFormulaKind verifies that when tool.Options["brew_kind"]
// is "formula", Upgrade uses `brew upgrade --formula <pkg>` without probing (I6).
func TestUpgrade_UsesPersistedFormulaKind(t *testing.T) {
	ctx := context.Background()
	p, mock := newBrew(
		executor.MockCall{Stdout: "", Stderr: "", Err: nil}, // upgrade --formula ripgrep
	)

	formulaTool := provider.Tool{
		Name:     "ripgrep",
		Provider: "brew",
		Package:  "ripgrep",
		Options:  map[string]string{"brew_kind": "formula"},
	}
	if err := p.Upgrade(ctx, formulaTool); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	calls := mock.Calls
	if len(calls) != 1 {
		t.Fatalf("expected 1 brew call, got %d: %v", len(calls), calls)
	}
	joined := strings.Join(calls[0].Args, " ")
	if !strings.Contains(joined, "--formula") {
		t.Errorf("upgrade args = %q, want --formula in args", joined)
	}
	if strings.Contains(joined, "--cask") {
		t.Errorf("upgrade args = %q, must not contain --cask for a formula", joined)
	}
}

// TestUpgrade_FallsBackToProbeWhenKindUnknown verifies that when no brew_kind
// option is present, Upgrade still works by probing installed formulae/casks (A4).
func TestUpgrade_FallsBackToProbeWhenKindUnknown(t *testing.T) {
	ctx := context.Background()
	// Sequence: list --versions (empty), list --versions --cask (non-empty), then upgrade --cask.
	p, mock := newBrew(
		executor.MockCall{Stdout: ""},           // brew list --versions git  (not a formula)
		executor.MockCall{Stdout: "git 2.44.0"}, // brew list --versions --cask git  (found)
		executor.MockCall{Stdout: ""},           // brew upgrade --cask git
	)

	noKindTool := provider.Tool{Name: "git", Provider: "brew", Package: "git"}
	if err := p.Upgrade(ctx, noKindTool); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// Should have made 3 calls: two probes + one upgrade.
	if len(mock.Calls) < 2 {
		t.Fatalf("expected probe calls before upgrade, got %d calls: %v", len(mock.Calls), mock.Calls)
	}
	// Final call must be the upgrade.
	lastArgs := strings.Join(mock.Calls[len(mock.Calls)-1].Args, " ")
	if !strings.HasPrefix(lastArgs, "upgrade") {
		t.Errorf("last call args = %q, want upgrade ...", lastArgs)
	}
}

// TestInstalledFormulae_CarryBrewKindOption verifies that ListInstalled returns
// formula tools with Options["brew_kind"]="formula", symmetric with casks.
func TestInstalledFormulae_CarryBrewKindOption(t *testing.T) {
	ctx := context.Background()
	// brew leaves --installed-on-request → ripgrep
	// brew list --versions ripgrep → ripgrep 14.1.0
	// brew list --cask → (empty, no casks)
	p, _ := newBrew(
		executor.MockCall{Stdout: "ripgrep"},        // leaves --installed-on-request
		executor.MockCall{Stdout: "ripgrep 14.1.0"}, // list --versions ripgrep
		executor.MockCall{Stdout: ""},               // list --cask
	)

	tools, err := p.ListInstalled(ctx)
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one installed tool")
	}
	got := tools[0].Options["brew_kind"]
	if got != "formula" {
		t.Errorf("ListInstalled formula Options[brew_kind] = %q, want %q", got, "formula")
	}
}

// TestInstalledCasks_CarryBrewKindOption verifies that ListInstalled returns
// cask tools with Options["brew_kind"]="cask" (used by lifecycle commands).
func TestInstalledCasks_CarryBrewKindOption(t *testing.T) {
	ctx := context.Background()
	// brew leaves --installed-on-request → empty (no formulae)
	// brew list --cask → iterm2
	// brew list --versions --cask iterm2 → iterm2 3.5.0
	p, _ := newBrew(
		executor.MockCall{Stdout: ""},             // leaves --installed-on-request
		executor.MockCall{Stdout: "iterm2"},       // list --cask
		executor.MockCall{Stdout: "iterm2 3.5.0"}, // list --versions --cask iterm2
	)

	tools, err := p.ListInstalled(ctx)
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one installed tool")
	}
	got := tools[0].Options["brew_kind"]
	if got != "cask" {
		t.Errorf("ListInstalled cask Options[brew_kind] = %q, want %q", got, "cask")
	}
}
