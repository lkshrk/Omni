package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedUnderNvm_ActiveAndVersionBins(t *testing.T) {
	home := t.TempDir()
	versionsDir := filepath.Join(home, ".nvm", "versions", "node")
	activeBin := filepath.Join(versionsDir, "v22.1.0", "bin")
	otherBin := filepath.Join(versionsDir, "v20.0.0", "bin")
	for _, dir := range []string{activeBin, otherBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	t.Setenv("HOME", home)
	t.Setenv("NVM_DIR", "")
	t.Setenv("NVM_BIN", activeBin)

	pnpm := filepath.Join(activeBin, "pnpm")
	if err := os.WriteFile(pnpm, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write pnpm: %v", err)
	}
	if !ResolvedUnderNvm(pnpm) {
		t.Fatalf("ResolvedUnderNvm(%q) = false, want true for active NVM_BIN", pnpm)
	}

	nodeOther := filepath.Join(otherBin, "node")
	if err := os.WriteFile(nodeOther, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write node: %v", err)
	}
	if !ResolvedUnderNvm(nodeOther) {
		t.Fatalf("ResolvedUnderNvm(%q) = false, want true for version bin", nodeOther)
	}

	brewNode := filepath.Join(home, "brew-bin", "node")
	if err := os.MkdirAll(filepath.Dir(brewNode), 0o755); err != nil {
		t.Fatalf("mkdir brew-bin: %v", err)
	}
	if err := os.WriteFile(brewNode, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write brew node: %v", err)
	}
	if ResolvedUnderNvm(brewNode) {
		t.Fatalf("ResolvedUnderNvm(%q) = true, want false outside nvm", brewNode)
	}
}

func TestResolvedUnderNvm_EmptyPath(t *testing.T) {
	if ResolvedUnderNvm("") {
		t.Fatal("ResolvedUnderNvm(\"\") = true, want false")
	}
}
