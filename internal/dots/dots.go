// Package dots manages stow-backed dotfile links declared in config groups.
// DotEntry names are logical identities; resolved entries carry the stow
// package directory selected by the app layer.
package dots

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// OpKind describes what happened (or would happen) for a single file.
type OpKind int

const (
	OpSkip           OpKind = iota // symlink already correct
	OpLink                         // new symlink created
	OpRepair                       // broken/wrong symlink replaced
	OpAdopt                        // real file moved into repo + linked
	OpConflict                     // real file exists; needs adoption or manual conflict resolution
	OpDryLink                      // dry-run: would create
	OpDryRepair                    // dry-run: would repair
	OpDryAdopt                     // dry-run: would adopt
	OpUnlink                       // symlink removed and replaced with a real file copy
	OpUnlinkSkip                   // no managed symlink at target; nothing done
	OpUnlinkConflict               // real (non-managed) file at target; conflict strategy applied
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

// ExpandPath expands env vars, then expands a supported tilde prefix.
func ExpandPath(path string) (string, error) {
	return ExpandTilde(os.ExpandEnv(path))
}

// Op records the outcome for a single file within a dots entry.
type Op struct {
	Kind  OpKind
	Entry string // entry name
	File  string // relative path within the entry (e.g. "init.lua")
	Src   string // absolute source path in repo
	Dst   string // absolute target path in home
	Err   error
}

// SyncProgressEvent describes progress for one resolved dots entry.
type SyncProgressEvent struct {
	Entry string
	Index int
	Total int
	Done  bool
	Err   error
	Ops   []Op
}

// SyncOptions configures the behaviour of DotsSync.
type SyncOptions struct {
	DryRun bool
	// EntryOrder optionally names entries in the preferred sync/progress order.
	// Entries not listed keep their relative order after the ordered entries.
	EntryOrder               []string
	SuppressUnchangedHistory bool
	Progress                 func(SyncProgressEvent)
}

// UnlinkOptions configures the behaviour of Manager.UnlinkAll.
type UnlinkOptions struct {
	// ConflictOverwrite, when true, causes real (non-managed) files at target
	// paths to be moved to trash and replaced with the repo version. When false
	// they are kept as-is (OpUnlinkConflict is emitted but no change is made).
	ConflictOverwrite bool
	// KeepExistingLocal, when true, leaves real non-managed local files in
	// place instead of treating them as unlink conflicts.
	KeepExistingLocal bool
	// RemoveLocal removes local real targets via trash, or unlinks local
	// symlinks, instead of materializing a copy from the repo source.
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
}

// Manager holds a resolved set of dot entries and the stow package root.
type Manager struct {
	RepoPath string
	Entries  []ResolvedEntry
}

// ValidateEntryName rejects logical names that cannot also safely serve as the
// default single stow package directory under the dots repo.
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

// New creates a Manager from a stow package root and a slice of DotEntry values.
// repoPath is expanded (~ resolved). The root does not need to exist: read-only
// status paths use the resolved source paths to report missing packages without
// creating the stow content directory.
//
// SourcePath for each entry is derived from the stow package tree convention:
//
//	<stow-root>/<package>/<rel-from-home>
//
// where rel-from-home is filepath.Rel($HOME, expanded(entry.Path)).
func New(repoPath string, entries []config.DotEntry) (*Manager, error) {
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
		})
	}

	return &Manager{RepoPath: expanded, Entries: resolved}, nil
}

// UnlinkAll removes managed symlinks for all entries and replaces them with
// real file copies sourced from the repo, leaving the user in a clean local
// state after disabling dots management.
func (m *Manager) UnlinkAll(opts UnlinkOptions) ([]Op, error) {
	var all []Op
	var failures unlinkFailures
	for _, e := range m.Entries {
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

// ExpandTilde replaces a leading "~" with the current user's home directory.
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
			// Backslash is a path separator on Windows, but a legal filename char
			// elsewhere. Keep "~\\foo" unsupported on non-Windows.
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
