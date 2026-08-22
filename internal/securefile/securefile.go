// Package securefile provides capability-scoped private state writes.
package securefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxCopyBytes = 250 << 20

var operationIDPattern = regexp.MustCompile(`^[a-f0-9]{24,64}$`)

type Root struct{ path string }

func NewRoot(stateDir string) (*Root, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("secure root is required")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve secure root: %w", err)
	}
	if err := secureMkdirAll(abs); err != nil {
		return nil, err
	}
	r := &Root{path: filepath.Clean(abs)}
	if err := r.verifyDir(""); err != nil {
		return nil, err
	}
	return r, nil
}

func OpenRoot(stateDir string) (*Root, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("secure root is required")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	r := &Root{path: filepath.Clean(abs)}
	if err := r.verifyDir(""); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Root) Path() string { return r.path }

func (r *Root) Child(relativeDir string) (*Root, error) {
	if !operationIDPattern.MatchString(relativeDir) {
		return nil, errors.New("invalid operation ID")
	}
	if err := r.Mkdir(relativeDir); err != nil {
		return nil, err
	}
	return &Root{path: filepath.Join(r.path, relativeDir)}, nil
}

func (r *Root) OpenChild(relativeDir string) (*Root, error) {
	if !operationIDPattern.MatchString(relativeDir) {
		return nil, errors.New("invalid operation ID")
	}
	child := &Root{path: filepath.Join(r.path, relativeDir)}
	if err := child.verifyDir(""); err != nil {
		return nil, err
	}
	return child, nil
}

func (r *Root) Mkdir(relativePath string) error {
	if _, err := r.resolve(relativePath, false); err != nil {
		return err
	}
	return secureMkdirRoot(r.path, relativePath)
}

func (r *Root) WriteFileAtomic(relativePath string, data []byte) error {
	if _, err := r.resolve(relativePath, false); err != nil {
		return err
	}
	return secureWriteRoot(r.path, relativePath, data)
}

func (r *Root) CopyIn(sourceAbsolutePath, destinationRelativePath string) (retErr error) {
	if !filepath.IsAbs(sourceAbsolutePath) {
		return errors.New("copy source must be absolute")
	}
	info, err := os.Lstat(sourceAbsolutePath)
	if err != nil {
		return fmt.Errorf("inspect copy source: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxCopyBytes {
		return errors.New("copy source must be a bounded regular file")
	}
	data, err := secureReadSource(sourceAbsolutePath, maxCopyBytes+1)
	if err != nil {
		return err
	}
	if len(data) > maxCopyBytes {
		return errors.New("copy source exceeds size limit")
	}
	return r.WriteFileAtomic(destinationRelativePath, data)
}

func (r *Root) Verify(relativePath string) error {
	if _, err := r.resolve(relativePath, true); err != nil {
		return err
	}
	return secureVerifyRoot(r.path, relativePath)
}

func (r *Root) Remove(relativePath string) error {
	if _, err := r.resolve(relativePath, true); err != nil {
		return err
	}
	return secureRemoveRoot(r.path, relativePath)
}

func (r *Root) resolve(relativePath string, allowExistingLeaf bool) (string, error) {
	if r == nil || r.path == "" {
		return "", errors.New("secure root is not initialized")
	}
	if relativePath == "" || filepath.IsAbs(relativePath) || filepath.Clean(relativePath) != relativePath {
		return "", errors.New("secure path must be a clean relative path")
	}
	for _, part := range strings.FieldsFunc(relativePath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("secure path contains an invalid component")
		}
	}
	p := filepath.Join(r.path, relativePath)
	rel, err := filepath.Rel(r.path, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("secure path escapes root")
	}
	end := p
	if allowExistingLeaf {
		end = filepath.Dir(p)
	}
	for current := r.path; current != end; {
		rest, _ := filepath.Rel(current, end)
		part := strings.Split(rest, string(filepath.Separator))[0]
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("secure path contains a symlink")
		}
	}
	return p, nil
}

func (r *Root) verifyDir(relative string) error {
	p := r.path
	if relative != "" {
		var err error
		p, err = r.resolve(relative, true)
		if err != nil {
			return err
		}
	}
	return secureVerifyDir(p)
}
