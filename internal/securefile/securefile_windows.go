//go:build windows

package securefile

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func secureReadSource(path string, limit int64) (data []byte, retErr error) {
	handles, err := openSafeComponentHandles(filepath.Dir(path), filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer closeHandles(handles)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	return io.ReadAll(io.LimitReader(f, limit))
}

func secureMkdirAll(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return protectPath(path)
}

func secureMkdirRoot(root, relative string) error {
	return secureMkdirAll(filepath.Join(root, relative))
}
func secureWriteRoot(root, relative string, data []byte) error {
	path := filepath.Join(root, relative)
	handles, err := openSafeComponentHandles(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer closeHandles(handles)
	if err := rejectReparseAncestors(root, path); err != nil {
		return err
	}
	if err := secureMkdirAll(filepath.Dir(path)); err != nil {
		return err
	}
	return secureWriteAtomic(path, data)
}
func secureVerifyRoot(root, relative string) error {
	path := filepath.Join(root, relative)
	handles, err := openSafeComponentHandles(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer closeHandles(handles)
	if err := rejectReparseAncestors(root, path); err != nil {
		return err
	}
	return secureVerify(path)
}
func secureRemoveRoot(root, relative string) error {
	path := filepath.Join(root, relative)
	handles, handleErr := openSafeComponentHandles(root, filepath.Dir(path))
	if handleErr != nil {
		return handleErr
	}
	defer closeHandles(handles)
	if err := rejectReparseAncestors(root, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse reparse cleanup")
	}
	return os.RemoveAll(path)
}

type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func openSafeComponentHandles(root, end string) ([]windows.Handle, error) {
	handles := []windows.Handle{}
	current := filepath.Clean(root)
	for {
		ptr, err := windows.UTF16PtrFromString(current)
		if err != nil {
			closeHandles(handles)
			return nil, err
		}
		handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			closeHandles(handles)
			return nil, err
		}
		var info fileAttributeTagInfo
		if err := windows.GetFileInformationByHandleEx(handle, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(handle)
			closeHandles(handles)
			return nil, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			windows.CloseHandle(handle)
			closeHandles(handles)
			return nil, errors.New("reparse-point ancestor refused")
		}
		handles = append(handles, handle)
		if current == filepath.Clean(end) {
			return handles, nil
		}
		rel, _ := filepath.Rel(current, end)
		current = filepath.Join(current, strings.Split(rel, string(filepath.Separator))[0])
	}
}
func closeHandles(handles []windows.Handle) {
	for i := len(handles) - 1; i >= 0; i-- {
		_ = windows.CloseHandle(handles[i])
	}
}
func rejectReparseAncestors(root, path string) error {
	for current := root; ; {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("reparse-point ancestor refused")
		}
		if filepath.Clean(current) == filepath.Clean(path) {
			return nil
		}
		rel, _ := filepath.Rel(current, path)
		part := strings.Split(rel, string(filepath.Separator))[0]
		current = filepath.Join(current, part)
	}
}

func secureWriteAtomic(path string, data []byte) (retErr error) {
	dir, name := filepath.Dir(path), filepath.Base(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	staging := ".secure-" + rand.Text()
	f, err := root.OpenFile(staging, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	committed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if !committed {
			if err := root.Remove(staging); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove private staging file: %w", err))
			}
		}
	}()
	if err = protectPath(filepath.Join(dir, staging)); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := root.Rename(staging, name); err != nil {
		return err
	}
	committed = true
	committedFile, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("open committed private file: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, committedFile.Close()) }()
	info, err := committedFile.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private path %q is not a regular file", path)
	}
	return verifyProtectedHandle(path, windows.Handle(committedFile.Fd()))
}

func secureVerify(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private path %q is not a regular file", path)
	}
	return verifyProtected(path)
}

func secureVerifyDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("private path %q is not a directory", path)
	}
	return verifyProtected(path)
}

func protectPath(path string) error {
	acl, err := protectedACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func protectedACL() (*windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	entries := []windows.EXPLICIT_ACCESS{
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid)}},
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP, TrusteeValue: windows.TrusteeValueFromSID(system)}},
	}
	return windows.ACLFromEntries(entries, nil)
}

func verifyProtected(path string) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return verifyProtectedDescriptor(path, descriptor)
}

func verifyProtectedHandle(path string, handle windows.Handle) error {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return verifyProtectedDescriptor(path, descriptor)
}

func verifyProtectedDescriptor(path string, descriptor *windows.SECURITY_DESCRIPTOR) error {
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("private path %q has an inherited DACL", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if dacl == nil || dacl.AceCount != 2 {
		return fmt.Errorf("private path %q has unsafe DACL", path)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	seenUser, seenSystem := false, false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 {
			return fmt.Errorf("private path %q has an unsafe ACE", path)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if user.User.Sid.Equals(sid) {
			seenUser = true
		} else if system.Equals(sid) {
			seenSystem = true
		} else {
			return fmt.Errorf("private path %q grants an unexpected SID", path)
		}
		if ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
			return fmt.Errorf("private path %q lacks full-control ACE", path)
		}
	}
	if !seenUser || !seenSystem {
		return fmt.Errorf("private path %q lacks owner/SYSTEM ACL", path)
	}
	return nil
}
