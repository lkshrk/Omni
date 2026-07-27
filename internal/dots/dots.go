// Package dots manages stow-backed dotfile links declared in config groups.
package dots

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

type OpKind int

const (
	OpSkip OpKind = iota
	OpLink
	OpRepair
	OpAdopt    // real file moved into repo + linked
	OpConflict // real file exists; needs adoption or manual conflict resolution
	OpDryLink
	OpDryRepair
	OpDryAdopt
	OpUnlink // symlink removed and replaced with a real file copy
	OpUnlinkSkip
	OpUnlinkConflict // real (non-managed) file at target; conflict strategy applied
)

func (k OpKind) String() string {
	switch k {
	case OpSkip:
		return "skip"
	case OpLink:
		return "link"
	case OpRepair:
		return "repair"
	case OpAdopt:
		return "adopt"
	case OpConflict:
		return "conflict"
	case OpDryLink:
		return "dry:link"
	case OpDryRepair:
		return "dry:repair"
	case OpDryAdopt:
		return "dry:adopt"
	case OpUnlink:
		return "unlink"
	case OpUnlinkSkip:
		return "unlink:skip"
	case OpUnlinkConflict:
		return "unlink:conflict"
	default:
		return "unknown"
	}
}

func ExpandPath(path string) (string, error) {
	return ExpandTilde(os.ExpandEnv(path))
}

type Op struct {
	Kind  OpKind
	Entry string
	File  string // relative path within the entry (e.g. "init.lua")
	Src   string // absolute source path in repo
	Dst   string // absolute target path in home
	Err   error
}

type SyncProgressEvent struct {
	Entry string
	Index int
	Total int
	Done  bool
	Err   error
	Ops   []Op
}

type SyncOptions struct {
	DryRun bool
	// Entries not listed keep their relative order after the ordered entries.
	EntryOrder               []string
	SuppressUnchangedHistory bool
	Progress                 func(SyncProgressEvent)
	// For entries with no on_conflict policy: "use_repo" relinks, "use_local" adopts, empty errors.
	ConflictStrategy string
}

type UnlinkOptions struct {
	// When true, real files at target paths are trashed and replaced with the repo version.
	ConflictOverwrite bool
	// When true, real non-managed local files are left alone rather than treated as conflicts.
	KeepExistingLocal bool
	// Trashes or unlinks local targets instead of materializing a copy from the repo source.
	RemoveLocal bool
}

// ResolvedEntry is a DotEntry with all paths expanded to absolute values.
type ResolvedEntry struct {
	Name       string
	Package    string
	SourcePath string   // absolute path of source in repo (may be file or dir)
	TargetPath string   // absolute path of target (dir or file)
	Ignored    bool     // true when present only to suppress management
	Ignore     []string // combined built-in + per-entry patterns
	OnConflict string   // automatic sync conflict policy: "", "use_repo", "use_local"
}

// Engine — Built per operation from an already-resolved entry set; the app layer owns config to entries.
type Engine struct {
	RepoPath string
	Entries  []ResolvedEntry
	exec     executor.Executor
}

type EngineOption func(*Engine)

// WithExecutor — Optional for read-only construction; mutating operations error without it.
func WithExecutor(exec executor.Executor) EngineOption {
	return func(e *Engine) { e.exec = exec }
}

// ValidateEntryName — A name must also work as the default single stow package directory under the dots repo.
func ValidateEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("dots: entry name is required")
	}
	if filepath.IsAbs(name) || name == "." || name == ".." || filepath.Clean(name) != name {
		return fmt.Errorf("dots: invalid entry name %q", name)
	}
	if strings.ContainsRune(name, filepath.Separator) || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("dots: invalid entry name %q: path separators are not allowed", name)
	}
	return nil
}

// ValidateHomeTargetPath — Only the parent is resolved, so an existing managed symlink can still be unlinked or replaced.
func ValidateHomeTargetPath(target string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	if filepath.Clean(target) == filepath.Clean(home) {
		return nil
	}
	resolvedHome, err := resolveRootPath(home)
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	resolvedTarget, err := resolveParentPreservingFinal(target)
	if err != nil {
		return fmt.Errorf("resolve target parent for %q: %w", target, err)
	}
	rel, err := filepath.Rel(resolvedHome, resolvedTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target path %q is outside home directory %q", target, home)
	}
	return nil
}

func resolveRootPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return resolveParentPreservingFinal(abs)
}

func resolveParentPreservingFinal(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	leaf := filepath.Base(abs)
	parent := filepath.Dir(abs)
	var missing []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(parent)
		if resolveErr == nil {
			resolved = filepath.Clean(resolved)
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Join(resolved, leaf), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", resolveErr
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", resolveErr
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
}

// NewEngine — SourcePath follows the stow tree convention <stow-root>/<package>/<rel-from-$HOME>.
func NewEngine(repoPath string, entries []config.DotEntry, opts ...EngineOption) (*Engine, error) {
	expanded, err := ExpandPath(repoPath)
	if err != nil {
		return nil, fmt.Errorf("dots: expand repo path: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("dots: home dir: %w", err)
	}

	resolved := make([]ResolvedEntry, 0, len(entries))
	for _, e := range entries {
		if err := ValidateEntryName(e.Name); err != nil {
			return nil, err
		}
		pkg := e.EffectivePackage()
		if err := ValidateEntryName(pkg); err != nil {
			return nil, fmt.Errorf("dots: entry %q package: %w", e.Name, err)
		}
		dstAbs, err := ExpandPath(e.Path)
		if err != nil {
			return nil, fmt.Errorf("dots: expand path for %q: %w", e.Name, err)
		}
		if err := ValidateHomeTargetPath(dstAbs); err != nil {
			return nil, fmt.Errorf("dots: path %q for entry %q is not under home directory: %w", e.Path, e.Name, err)
		}

		rel, relErr := filepath.Rel(home, dstAbs)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("dots: path %q for entry %q is not under home directory", e.Path, e.Name)
		}
		srcAbs := filepath.Join(expanded, pkg, rel)

		ignorePatterns, err := combinedIgnores(e.Ignore)
		if err != nil {
			return nil, fmt.Errorf("dots: entry %q: %w", e.Name, err)
		}
		resolved = append(resolved, ResolvedEntry{
			Name:       e.Name,
			Package:    pkg,
			SourcePath: srcAbs,
			TargetPath: dstAbs,
			Ignored:    e.Ignored,
			Ignore:     ignorePatterns,
			OnConflict: e.OnConflict,
		})
	}

	e := &Engine{RepoPath: expanded, Entries: resolved}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

func (e *Engine) UnlinkAll(opts UnlinkOptions) ([]Op, error) {
	var all []Op
	var failures unlinkFailures
	for _, e := range e.Entries {
		ops, err := unlinkEntry(e, opts)
		all = append(all, ops...)
		if err != nil {
			failures = append(failures, unlinkFailure{entry: e.Name, err: err})
		}
	}
	if len(failures) > 0 {
		return all, failures
	}
	return all, nil
}

type unlinkFailure struct {
	entry string
	err   error
}

type unlinkFailures []unlinkFailure

func (f unlinkFailures) Error() string {
	parts := make([]string, 0, len(f))
	for _, failure := range f {
		parts = append(parts, fmt.Sprintf("%s: %v", failure.entry, failure.err))
	}
	return "dots: unlink: " + strings.Join(parts, "; ")
}

func ExpandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if len(path) > 1 {
		if path[1] != '/' {
			// Backslash is a legal filename char off Windows, so "~\\foo" stays unsupported there.
			if runtime.GOOS != "windows" || path[1] != '\\' {
				return path, nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	rest := path[1:]
	if runtime.GOOS == "windows" {
		rest = strings.TrimLeft(rest, "/\\")
	} else {
		rest = strings.TrimLeft(rest, "/")
	}
	return filepath.Join(home, rest), nil
}
