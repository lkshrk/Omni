package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/lkshrk/omni/internal/apm"
)

// A deployed primitive is a text file; past these bounds the bytes are not APM's and are not worth holding to restore.
const (
	apmLedgerFileLimit  = 1 << 20
	apmLedgerTotalLimit = 32 << 20
)

type apmFileSnapshot struct {
	content []byte
	mode    os.FileMode
	exists  bool
	// skipped marks a tracked file whose bytes were not held, so an in-place rewrite of it cannot be undone.
	skipped bool
}

type apmPathSnapshot struct {
	path string
	snap apmFileSnapshot
	// mcp marks a config holding an MCP registry, which is reverted entry by entry rather than whole-file.
	mcp bool
}

// apmStateSnapshot — the files a sync rewrites before APM ever runs, as they stood beforehand; capturing them afterwards would record the rewritten file as the state to return to.
type apmStateSnapshot struct {
	lock     apmFileSnapshot
	configs  map[string]apmFileSnapshot
	warnings []string
}

// apmLedger reverses APM's side effects only: the deployed files, the merged client configs and the lockfile.
// The manifest is omni's declarative intent and the ownership record tracks what a surface actually applied,
// so neither is this ledger's to undo.
type apmLedger struct {
	root     string
	roots    []string
	lockPath string
	files    map[string]apmFileSnapshot
	dirs     map[string]bool
	state    apmStateSnapshot
	configs  []apmPathSnapshot
	warnings []string
}

func apmLockPath(manifestPath string) string {
	return filepath.Join(filepath.Dir(manifestPath), "apm.lock.yaml")
}

// targets is the widest set the caller may install into: MCP normalization rewrites a client config before the install, which a ledger taken later would snapshot as the pre-install state.
func (a *App) snapshotAPMState(targets []string) (apmStateSnapshot, error) {
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return apmStateSnapshot{}, err
	}
	root, err := a.apmDeployRoot()
	if err != nil {
		return apmStateSnapshot{}, err
	}
	state := apmStateSnapshot{configs: map[string]apmFileSnapshot{}}
	if state.lock, err = readAPMFileSnapshot(apmLockPath(manifestPath)); err != nil {
		return apmStateSnapshot{}, err
	}
	budget := int64(apmLedgerTotalLimit)
	for _, path := range a.apmMergedConfigsForTargets(root, targets) {
		snap, oversized, err := readAPMConfigSnapshot(path, &budget)
		if err != nil {
			return apmStateSnapshot{}, err
		}
		if oversized {
			state.warnings = append(state.warnings, apmOversizedWarning(path))
		}
		state.configs[path] = snap
	}
	return state, nil
}

func apmOversizedWarning(path string) string {
	return fmt.Sprintf("%s is too large to snapshot; a failed APM install cannot restore it", path)
}

func (a *App) snapshotAPMLedger(targets []string, pre *apmStateSnapshot) (*apmLedger, error) {
	root, err := a.apmDeployRoot()
	if err != nil {
		return nil, err
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return nil, err
	}
	ledger := &apmLedger{
		root:     root,
		roots:    apmUserRootsForTargets(targets),
		lockPath: apmLockPath(manifestPath),
		files:    map[string]apmFileSnapshot{},
		dirs:     map[string]bool{},
	}
	budget := int64(apmLedgerTotalLimit)
	if err := walkAPMDeployRoots(root, ledger.roots, func(path string, isDir bool) error {
		if isDir {
			ledger.dirs[path] = true
			return nil
		}
		snap, oversized, err := readAPMTrackedSnapshot(path, &budget)
		if err != nil {
			return err
		}
		if oversized {
			ledger.warnings = append(ledger.warnings, apmOversizedWarning(path))
		}
		ledger.files[path] = snap
		return nil
	}); err != nil {
		return nil, err
	}
	state := pre
	if state == nil {
		captured, err := a.snapshotAPMState(targets)
		if err != nil {
			return nil, err
		}
		state = &captured
	}
	ledger.state = *state
	ledger.warnings = append(ledger.warnings, state.warnings...)
	mcpConfigs := identitySet(a.apmMcpConfigsForTargets(targets))
	for _, path := range a.apmMergedConfigsForTargets(root, targets) {
		snap, ok := state.configs[path]
		if !ok {
			var oversized bool
			var err error
			if snap, oversized, err = readAPMConfigSnapshot(path, &budget); err != nil {
				return nil, err
			}
			if oversized {
				ledger.warnings = append(ledger.warnings, apmOversizedWarning(path))
			}
		}
		ledger.configs = append(ledger.configs, apmPathSnapshot{path: path, snap: snap, mcp: mcpConfigs[path]})
	}
	return ledger, nil
}

// apmMergedConfigsForTargets — the files APM merges into rather than owns: package hooks and the per-target MCP registrations, several of which sit outside the deploy roots.
func (a *App) apmMergedConfigsForTargets(root string, targets []string) []string {
	var paths []string
	for _, rel := range apmHookConfigsForTargets(targets) {
		paths = append(paths, filepath.Join(root, rel))
	}
	for _, path := range a.apmMcpConfigsForTargets(targets) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// revert returns one line per reversal, including the ones that could not be completed.
func (l *apmLedger) revert() []string {
	actions := append([]string(nil), l.warnings...)
	var addedFiles, addedDirs []string
	if err := walkAPMDeployRoots(l.root, l.roots, func(path string, isDir bool) error {
		switch {
		case isDir && !l.dirs[path]:
			addedDirs = append(addedDirs, path)
		case !isDir:
			if _, tracked := l.files[path]; !tracked {
				addedFiles = append(addedFiles, path)
			}
		}
		return nil
	}); err != nil {
		return append(actions, "failed-install cleanup could not inspect the APM deploy roots: "+err.Error())
	}

	sort.Strings(addedFiles)
	for _, path := range addedFiles {
		if err := os.Remove(path); err != nil {
			actions = append(actions, fmt.Sprintf("could not remove %s left by the failed APM install: %v", path, err))
			continue
		}
		actions = append(actions, "removed "+path+" left by the failed APM install")
	}
	// Deepest first, so a directory tree the batch created empties out before its parent is tried.
	sort.Sort(sort.Reverse(sort.StringSlice(addedDirs)))
	for _, dir := range addedDirs {
		if err := os.Remove(dir); err == nil {
			actions = append(actions, "removed empty directory "+dir+" left by the failed APM install")
		}
	}

	tracked := make([]string, 0, len(l.files))
	for path := range l.files {
		tracked = append(tracked, path)
	}
	sort.Strings(tracked)
	for _, path := range tracked {
		if snap := l.files[path]; !snap.skipped {
			actions = append(actions, l.restoreTracked(path, snap)...)
		}
	}

	for _, config := range l.configs {
		if config.snap.skipped {
			continue
		}
		actions = append(actions, l.restoreConfig(config)...)
	}
	actions = append(actions, l.restore(l.lockPath, l.state.lock, "lockfile")...)
	return actions
}

// restore reads the live file through any symlink, which is how APM wrote it in the first place.
func (l *apmLedger) restore(path string, snap apmFileSnapshot, label string) []string {
	current, err := readAPMFileSnapshot(path)
	if err != nil {
		return []string{apmInspectFailure(path, label, err)}
	}
	return applyAPMRestore(path, snap, current, label)
}

// Writing through a symlink the install left would rewrite whatever it now points at, so a non-regular path is refused.
func (l *apmLedger) restoreTracked(path string, snap apmFileSnapshot) []string {
	const label = "deployed file"
	info, err := os.Lstat(path)
	switch {
	case err != nil && !os.IsNotExist(err):
		return []string{apmInspectFailure(path, label, err)}
	case err == nil && !info.Mode().IsRegular():
		return []string{fmt.Sprintf("left %s (%s) alone: the failed APM install put a non-regular file there", path, label)}
	}
	current := apmFileSnapshot{mode: 0o600}
	if err == nil {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return []string{apmInspectFailure(path, label, readErr)}
		}
		current = apmFileSnapshot{content: content, mode: info.Mode().Perm(), exists: true}
	}
	return applyAPMRestore(path, snap, current, label)
}

// An MCP registry is reverted one key deep: the rest of the file is live state the agent rewrites on its own.
func (l *apmLedger) restoreConfig(config apmPathSnapshot) []string {
	const label = "merged client config"
	current, err := readAPMFileSnapshot(config.path)
	if err != nil {
		return []string{apmInspectFailure(config.path, label, err)}
	}
	if !config.mcp || !config.snap.exists || !current.exists || bytes.Equal(config.snap.content, current.content) {
		return applyAPMRestore(config.path, config.snap, current, label)
	}
	scoped, ok := revertAPMMcpServers(config.snap.content, current.content)
	if !ok {
		return []string{fmt.Sprintf("left %s (%s) alone: its mcp entries could not be parsed, so the servers the failed APM install registered are still there", config.path, label)}
	}
	return applyAPMRestore(config.path, apmFileSnapshot{content: scoped, mode: config.snap.mode, exists: true}, current, label)
}

func apmInspectFailure(path, label string, err error) string {
	return fmt.Sprintf("could not inspect %s (%s) after the failed APM install: %v", path, label, err)
}

func applyAPMRestore(path string, snap, current apmFileSnapshot, label string) []string {
	switch {
	case !snap.exists && !current.exists:
		return nil
	case !snap.exists:
		if err := os.Remove(path); err != nil {
			return []string{fmt.Sprintf("could not remove %s (%s) created by the failed APM install: %v", path, label, err)}
		}
		return []string{fmt.Sprintf("removed %s (%s) created by the failed APM install", path, label)}
	case current.exists && bytes.Equal(snap.content, current.content):
		return restoreAPMMode(path, snap, current, label)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return []string{fmt.Sprintf("could not restore %s (%s): %v", path, label, err)}
	}
	if err := os.WriteFile(path, snap.content, snap.mode); err != nil {
		return []string{fmt.Sprintf("could not restore %s (%s): %v", path, label, err)}
	}
	actions := []string{fmt.Sprintf("restored %s (%s) to its pre-install contents", path, label)}
	if current.exists {
		// WriteFile applies the mode only when it creates the file.
		actions = append(actions, restoreAPMMode(path, snap, current, label)...)
	}
	return actions
}

func restoreAPMMode(path string, snap, current apmFileSnapshot, label string) []string {
	if snap.mode == current.mode {
		return nil
	}
	if err := os.Chmod(path, snap.mode); err != nil {
		return []string{fmt.Sprintf("could not restore the permissions of %s (%s): %v", path, label, err)}
	}
	return []string{fmt.Sprintf("restored the permissions of %s (%s)", path, label)}
}

// The keys every JSON client config APM registers into nests its servers under.
var apmMcpServerKeys = []string{"mcpServers", "mcp_servers", "servers"}

// revertAPMMcpServers keeps every key but the server registry, which goes back to what the snapshot recorded.
func revertAPMMcpServers(before, current []byte) ([]byte, bool) {
	var prior, live map[string]json.RawMessage
	if json.Unmarshal(before, &prior) != nil || json.Unmarshal(current, &live) != nil {
		return nil, false
	}
	key := apmMcpServersKey(prior, live)
	if key == "" {
		return nil, false
	}
	if apmConfigEqualOutside(prior, live, key) {
		return before, true
	}
	if raw, ok := prior[key]; ok {
		live[key] = raw
	} else {
		delete(live, key)
	}
	out, err := json.MarshalIndent(live, "", "  ")
	if err != nil {
		return nil, false
	}
	return append(out, '\n'), true
}

func apmMcpServersKey(prior, live map[string]json.RawMessage) string {
	for _, key := range apmMcpServerKeys {
		if _, ok := prior[key]; ok {
			return key
		}
		if _, ok := live[key]; ok {
			return key
		}
	}
	return ""
}

// Equal outside the registry means the snapshot's own bytes can go back verbatim, keeping the file's formatting.
func apmConfigEqualOutside(prior, live map[string]json.RawMessage, key string) bool {
	for name, raw := range prior {
		if name != key && !bytes.Equal(raw, live[name]) {
			return false
		}
	}
	for name := range live {
		if _, ok := prior[name]; name != key && !ok {
			return false
		}
	}
	return true
}

func walkAPMDeployRoots(root string, rels []string, visit func(path string, isDir bool) error) error {
	for _, rel := range rels {
		dir := filepath.Join(root, rel)
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			return visit(path, entry.IsDir())
		})
		if err != nil {
			return fmt.Errorf("inspecting APM deploy root %s: %w", dir, err)
		}
	}
	return nil
}

// A deployed file is judged by its own mode: a symlink or device node is tracked as present but never rewritten.
func readAPMTrackedSnapshot(path string, budget *int64) (apmFileSnapshot, bool, error) {
	info, err := os.Lstat(path)
	return apmSnapshotWithin(path, info, err, budget)
}

// readAPMConfigSnapshot follows a symlinked client config, which APM writes through rather than replacing.
func readAPMConfigSnapshot(path string, budget *int64) (apmFileSnapshot, bool, error) {
	info, err := os.Stat(path)
	return apmSnapshotWithin(path, info, err, budget)
}

// apmSnapshotWithin holds a file's bytes only while the budget lasts.
func apmSnapshotWithin(path string, info os.FileInfo, statErr error, budget *int64) (snap apmFileSnapshot, oversized bool, err error) {
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return apmFileSnapshot{mode: 0o600}, false, nil
		}
		return apmFileSnapshot{}, false, fmt.Errorf("inspecting %s: %w", path, statErr)
	}
	snap = apmFileSnapshot{mode: info.Mode().Perm(), exists: true, skipped: true}
	if !info.Mode().IsRegular() {
		return snap, false, nil
	}
	if info.Size() > apmLedgerFileLimit || info.Size() > *budget {
		return snap, true, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return apmFileSnapshot{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	*budget -= int64(len(content))
	snap.content, snap.skipped = content, false
	return snap, false, nil
}

func readAPMFileSnapshot(path string) (apmFileSnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return apmFileSnapshot{mode: 0o600}, nil
		}
		return apmFileSnapshot{}, fmt.Errorf("inspecting %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return apmFileSnapshot{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return apmFileSnapshot{content: content, mode: info.Mode().Perm(), exists: true}, nil
}

// APM's own transaction never rolls back the primitives it deployed, and no global-scope verb can reclaim them afterwards.
func (a *App) withAPMLedger(dryRun bool, targets []string, pre *apmStateSnapshot, install func() (apm.Result, error)) (apm.Result, []string, error) {
	if dryRun {
		result, err := install()
		return result, nil, err
	}
	ledger, ledgerErr := a.snapshotAPMLedger(targets, pre)
	result, err := install()
	if err == nil {
		return result, nil, nil
	}
	if ledgerErr != nil {
		return result, []string{"failed-install cleanup skipped: " + ledgerErr.Error()}, err
	}
	return result, ledger.revert(), err
}

// pre carries the lockfile and client configs captured before the caller rewrote any of them; nil snapshots the files as they stand.
func (a *App) installAPMSurface(
	ctx context.Context,
	client *apm.Client,
	surface string,
	targets []string,
	pre *apmStateSnapshot,
	opts apm.InstallOptions,
) (apm.Result, []string, error) {
	return a.withAPMLedger(opts.DryRun, targets, pre, func() (apm.Result, error) {
		return client.InstallOnly(ctx, surface, targets, opts)
	})
}
