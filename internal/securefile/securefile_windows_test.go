//go:build windows

package securefile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsAtomicWriteCommitsAndCleansStagingFile(t *testing.T) {
	root, err := NewRoot(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "replacement"} {
		if err := root.WriteFileAtomic("journal.json", []byte(want)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root.Path(), "journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("committed content = %q, want %q", got, want)
		}
		if err := root.Verify("journal.json"); err != nil {
			t.Fatal(err)
		}
	}
	staging, err := filepath.Glob(filepath.Join(root.Path(), ".secure-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staging) != 0 {
		t.Fatalf("staging files remain after commit: %v", staging)
	}
}

func TestVerifyDetectsWeakenedWindowsDACL(t *testing.T) {
	root, err := NewRoot(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFileAtomic("secret", []byte("canary")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root.Path(), "secret")
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.GENERIC_READ, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP, TrusteeValue: windows.TrusteeValueFromSID(world)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := root.Verify("secret"); err == nil {
		t.Fatal("Verify accepted weakened DACL")
	}
}
