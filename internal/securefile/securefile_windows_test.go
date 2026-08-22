//go:build windows

package securefile

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

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
