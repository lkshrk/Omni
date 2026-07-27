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

// Covers the seenCasks fallback: a cask in `brew list --cask` but missing from the JSON info response.
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

// A persisted brew_kind must skip the list probes entirely.
func TestUpgrade_UsesPersistedCaskKind(t *testing.T) {
	ctx := context.Background()
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

// A persisted brew_kind must skip the list probes entirely.
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

func TestUpgrade_FallsBackToProbeWhenKindUnknown(t *testing.T) {
	ctx := context.Background()
	p, mock := newBrew(
		executor.MockCall{Stdout: ""},
		executor.MockCall{Stdout: "git 2.44.0"},
		executor.MockCall{Stdout: ""},
	)

	noKindTool := provider.Tool{Name: "git", Provider: "brew", Package: "git"}
	if err := p.Upgrade(ctx, noKindTool); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if len(mock.Calls) < 2 {
		t.Fatalf("expected probe calls before upgrade, got %d calls: %v", len(mock.Calls), mock.Calls)
	}
	lastArgs := strings.Join(mock.Calls[len(mock.Calls)-1].Args, " ")
	if !strings.HasPrefix(lastArgs, "upgrade") {
		t.Errorf("last call args = %q, want upgrade ...", lastArgs)
	}
}

func TestInstalledFormulae_CarryBrewKindOption(t *testing.T) {
	ctx := context.Background()
	p, _ := newBrew(
		executor.MockCall{Stdout: "ripgrep"},
		executor.MockCall{Stdout: "ripgrep 14.1.0"},
		executor.MockCall{Stdout: ""},
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

func TestInstalledCasks_CarryBrewKindOption(t *testing.T) {
	ctx := context.Background()
	p, _ := newBrew(
		executor.MockCall{Stdout: ""},
		executor.MockCall{Stdout: "iterm2"},
		executor.MockCall{Stdout: "iterm2 3.5.0"},
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
