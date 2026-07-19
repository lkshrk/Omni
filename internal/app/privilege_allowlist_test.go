package app

import (
	"os"
	"runtime"
	"slices"
	"testing"
)

// TestInteractivePrivilegedCommand_RejectsNonPackageManagers is a security guard:
// only known package-manager binaries may be escalated with sudo, so a provider
// (present or future) returning an arbitrary command built from a git-synced
// settings.json cannot be silently run as root.
func TestInteractivePrivilegedCommand_RejectsNonPackageManagers(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"sh", "bash", "rm", "curl", "/bin/sh", "apt", "brew", ""} {
		gotCmd, gotArgs, ok := interactivePrivilegedCommand(cmd, "install", "evil")
		if ok {
			t.Errorf("interactivePrivilegedCommand(%q): ok = true, want false (not allow-listed)", cmd)
		}
		if gotCmd != "" || gotArgs != nil {
			t.Errorf("interactivePrivilegedCommand(%q): got (%q, %v), want empty when refused", cmd, gotCmd, gotArgs)
		}
	}
}

func TestInteractivePrivilegedCommand_AllowsPackageManagers(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"apk", "apt-get", "dnf", "pacman", "zypper"} {
		gotCmd, gotArgs, ok := interactivePrivilegedCommand(cmd, "install", "ripgrep")
		if !ok {
			t.Errorf("interactivePrivilegedCommand(%q): ok = false, want true", cmd)
			continue
		}
		// Non-root, non-Windows: sudo-prefixed with the original command + args
		// preserved as the sudo arguments.
		if runtime.GOOS != "windows" && os.Geteuid() != 0 {
			if gotCmd != "sudo" {
				t.Errorf("interactivePrivilegedCommand(%q): command = %q, want sudo", cmd, gotCmd)
			}
			want := []string{cmd, "install", "ripgrep"}
			if !slices.Equal(gotArgs, want) {
				t.Errorf("interactivePrivilegedCommand(%q): args = %v, want %v", cmd, gotArgs, want)
			}
		}
	}
}
