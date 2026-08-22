//go:build windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func captureFragmentIdentity(path string) (fragmentIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fragmentIdentity{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fragmentIdentity{}, errors.New("reparse/special config fragment refused")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fragmentIdentity{}, err
	}
	sd, err := windows.GetNamedSecurityInfo(canonical, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fragmentIdentity{}, err
	}
	sum := sha256.Sum256([]byte(sd.String()))
	return fragmentIdentity{CanonicalPath: canonical, Kind: "file", Mode: uint32(info.Mode().Perm()), ACLFingerprint: hex.EncodeToString(sum[:])}, nil
}
