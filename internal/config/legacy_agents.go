package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const legacyAgentsSnapshotPrefix = ".omni-apm-migration-backup-"

const (
	maxLegacyEvidenceEntries = 4096
	maxLegacyEvidenceDepth   = 32
	maxLegacyEvidenceFile    = 64 << 20
	maxLegacyEvidenceBytes   = 512 << 20
)

type legacyConfigFile struct {
	path string
	raw  []byte
}

type legacyConfigChange struct {
	legacyConfigFile
	rendered []byte
}

// HasRemovedAgentConfig detects whether onboarding must run before normal
// config repair. It deliberately decodes no removed DTOs.
func HasRemovedAgentConfig(path string) (bool, error) {
	seen := map[string]bool{}
	var visit func(string) (bool, error)
	visit = func(current string) (bool, error) {
		abs, err := filepath.Abs(current)
		if err != nil {
			return false, err
		}
		if seen[abs] {
			return false, nil
		}
		seen[abs] = true
		data, err := os.ReadFile(abs)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return false, fmt.Errorf("parse config for onboarding detection: %w", err)
		}
		if _, ok := raw["agents"]; ok {
			return true, nil
		}
		delete(raw, "agents")
		if err := validateRemovedAgentConfigFields(raw); err != nil {
			return true, nil
		}
		var includes []string
		if value := raw["$include"]; len(value) > 0 {
			if err := json.Unmarshal(value, &includes); err != nil {
				return false, err
			}
		}
		for _, include := range includes {
			if !filepath.IsAbs(include) {
				include = filepath.Join(filepath.Dir(abs), include)
			}
			found, err := visit(include)
			if err != nil || found {
				return found, err
			}
		}
		return false, nil
	}
	return visit(path)
}

// CaptureLegacyAgentsSnapshot copies the resolved config and its recursive includes verbatim.
// An exact existing snapshot is reused so retries do not accumulate duplicates.
func CaptureLegacyAgentsSnapshot(configPath string) (snapshotDir string, retErr error) {
	lock, err := AcquireWriteLock(configPath)
	if err != nil {
		return "", err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()

	files, root, err := legacyConfigFiles(configPath)
	if err != nil {
		return "", err
	}
	if snapshotDir, err = matchingLegacyAgentsSnapshot(root, files); err != nil || snapshotDir != "" {
		return snapshotDir, err
	}
	evidencePaths, err := legacyAgentEvidencePaths(files)
	if err != nil {
		return "", err
	}
	if len(evidencePaths) != 0 {
		return "", errors.New("local agent bundle evidence requires an existing migration snapshot; automatic capture only supports remote packages and inline MCP declarations")
	}
	for _, original := range evidencePaths {
		info, err := os.Lstat(original)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			if err := requireLegacyBundleRoot(original); err != nil {
				return "", fmt.Errorf("local agent evidence %q requires manual review: %w", original, err)
			}
			if err := inspectLegacyEvidenceTree(original); err != nil {
				return "", fmt.Errorf("local agent evidence %q requires manual review: %w", original, err)
			}
		}
	}
	snapshotDir, err = os.MkdirTemp(filepath.Dir(root), legacyAgentsSnapshotPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("create legacy agents snapshot: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = makeSnapshotWritable(snapshotDir)
			_ = os.RemoveAll(snapshotDir)
		}
	}()

	paths := make(map[string]string, len(files))
	for i, file := range files {
		name := fmt.Sprintf("%s%03d.json", snapshotConfigPrefix, i)
		if err := os.WriteFile(filepath.Join(snapshotDir, name), file.raw, 0o400); err != nil {
			return "", fmt.Errorf("write legacy agents snapshot: %w", err)
		}
		paths[name] = file.path
	}
	var evidenceBytes int64
	evidenceEntries := 0
	for i, original := range evidencePaths {
		name := fmt.Sprintf("evidence-%03d", i)
		info, err := os.Lstat(original)
		if err != nil {
			return "", err
		}
		if info.Mode().IsRegular() && filepath.Base(original) == "marketplaces.json" {
			name = "marketplaces.json"
		} else if info.IsDir() {
			if err := requireLegacyBundleRoot(original); err != nil {
				return "", fmt.Errorf("local agent evidence %q requires manual review: %w", original, err)
			}
		}
		if _, exists := paths[name]; exists {
			return "", fmt.Errorf("legacy evidence snapshot name collision %q", name)
		}
		if err := copyLegacyEvidence(original, filepath.Join(snapshotDir, name), &evidenceEntries, &evidenceBytes); err != nil {
			return "", fmt.Errorf("copy legacy agent evidence %q: %w", original, err)
		}
		paths[name] = original
	}
	manifest, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return "", err
	}
	manifest = append(manifest, '\n')
	if err := os.WriteFile(filepath.Join(snapshotDir, "paths.json"), manifest, 0o400); err != nil {
		return "", fmt.Errorf("write legacy agents snapshot paths: %w", err)
	}
	for _, file := range files {
		current, err := readStableRegularFile(file.path)
		if err != nil || !bytes.Equal(current, file.raw) {
			if err == nil {
				err = errors.New("file changed while snapshot was created")
			}
			return "", fmt.Errorf("verify legacy config %q: %w", file.path, err)
		}
	}
	if err := makeSnapshotReadOnly(snapshotDir); err != nil {
		return "", fmt.Errorf("make legacy agents snapshot immutable: %w", err)
	}
	complete = true
	return snapshotDir, nil
}

func inspectLegacyEvidenceTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && unsafeLegacyEvidenceName(entry.Name()) {
			return fmt.Errorf("sensitive or generated path %q", path)
		}
		return nil
	})
}

// CleanupLegacyAgentsConfig is retained only to fail closed for old callers.
func CleanupLegacyAgentsConfig(configPath string) error {
	return errors.New("legacy agents cleanup requires the snapshot returned by CaptureLegacyAgentsSnapshot")
}

// CleanupLegacyAgentsConfigFromSnapshot removes retired fields only when every
// live config/include path and byte still matches the captured snapshot.
func CleanupLegacyAgentsConfigFromSnapshot(configPath, snapshotDir string) (retErr error) {
	lock, err := AcquireWriteLock(configPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()

	files, root, err := legacyConfigFiles(configPath)
	if err != nil {
		return err
	}
	if err := verifyLegacyConfigSnapshot(root, snapshotDir, files); err != nil {
		return err
	}
	changes := make([]legacyConfigChange, 0, len(files))
	for _, file := range files {
		var raw map[string]json.RawMessage
		if err := unmarshalJSONObject(file.raw, &raw); err != nil {
			return fmt.Errorf("parse legacy config %q: %w", file.path, err)
		}
		if removeLegacyAgentFields(raw) {
			changes = append(changes, legacyConfigChange{legacyConfigFile: file, rendered: renderFragmentRaw(raw, nil)})
		}
	}
	for _, change := range changes {
		current, err := readStableRegularFile(change.path)
		if err != nil || !bytes.Equal(current, change.raw) {
			if err == nil {
				err = errors.New("file changed before cleanup")
			}
			return fmt.Errorf("verify legacy config %q: %w", change.path, err)
		}
	}
	return applyLegacyAgentCleanup(changes, atomicWriteUnlocked)
}

func matchingLegacyAgentsSnapshot(configRoot string, files []legacyConfigFile) (string, error) {
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(configRoot), legacyAgentsSnapshotPrefix+"*"))
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	for _, candidate := range matches {
		if verifyLegacyConfigSnapshot(configRoot, candidate, files) == nil {
			return candidate, nil
		}
	}
	return "", nil
}

func applyLegacyAgentCleanup(changes []legacyConfigChange, write func(string, []byte) error) error {
	for i, change := range changes {
		if err := write(change.path, change.rendered); err != nil {
			rollbackErr := error(nil)
			for j := i - 1; j >= 0; j-- {
				rollbackErr = errors.Join(rollbackErr, write(changes[j].path, changes[j].raw))
			}
			return errors.Join(fmt.Errorf("clean legacy config %q: %w", change.path, err), rollbackErr)
		}
	}
	return nil
}

// RemoveLegacyAgentsSnapshot deletes recovery evidence only after the caller
// has verified the migration and completed legacy cleanup.
func RemoveLegacyAgentsSnapshot(configPath, snapshotDir string) error {
	_, root, err := legacyConfigFiles(configPath)
	if err != nil {
		return err
	}
	snapshotRoot, err := filepath.Abs(snapshotDir)
	if err != nil {
		return err
	}
	if filepath.Dir(snapshotRoot) != filepath.Dir(root) || !strings.HasPrefix(filepath.Base(snapshotRoot), legacyAgentsSnapshotPrefix) {
		return errors.New("legacy agents snapshot is not beside the resolved config")
	}
	info, err := os.Lstat(snapshotRoot)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("legacy agents snapshot must be a real directory")
	}
	if err := makeSnapshotWritable(snapshotRoot); err != nil {
		return err
	}
	return os.RemoveAll(snapshotRoot)
}

func verifyLegacyConfigSnapshot(configRoot, snapshotDir string, files []legacyConfigFile) error {
	snapshotRoot, err := filepath.Abs(snapshotDir)
	if err != nil {
		return err
	}
	if filepath.Dir(snapshotRoot) != filepath.Dir(configRoot) || !strings.HasPrefix(filepath.Base(snapshotRoot), legacyAgentsSnapshotPrefix) {
		return errors.New("legacy agents snapshot is not beside the resolved config")
	}
	var budget int64
	manifest, err := readSnapshotFile(snapshotRoot, "paths.json", &budget)
	if err != nil {
		return fmt.Errorf("read legacy agents snapshot paths: %w", err)
	}
	var paths map[string]string
	if err := json.Unmarshal(manifest, &paths); err != nil {
		return fmt.Errorf("parse legacy agents snapshot paths: %w", err)
	}
	expected := make(map[string][]byte)
	for copied, original := range paths {
		if !strings.HasPrefix(copied, snapshotConfigPrefix) {
			continue
		}
		original = canonicalEvidencePath(original)
		if _, exists := expected[original]; exists {
			return fmt.Errorf("legacy agents snapshot repeats config path %q", original)
		}
		raw, err := readSnapshotFile(snapshotRoot, copied, &budget)
		if err != nil {
			return fmt.Errorf("read legacy agents snapshot config %q: %w", copied, err)
		}
		expected[original] = raw
	}
	if len(expected) != len(files) {
		return errors.New("live config include set differs from the captured snapshot")
	}
	for _, file := range files {
		captured, ok := expected[canonicalEvidencePath(file.path)]
		if !ok || !bytes.Equal(captured, file.raw) {
			return fmt.Errorf("legacy config %q changed after snapshot; cleanup refused", file.path)
		}
	}
	return nil
}

func legacyConfigFiles(configPath string) ([]legacyConfigFile, string, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, "", err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, "", fmt.Errorf("resolve config path: %w", err)
	}
	root = filepath.Clean(root)
	base := filepath.Dir(root)
	seen := map[string]uint8{}
	var files []legacyConfigFile
	var visit func(string) error
	visit = func(path string) error {
		path = filepath.Clean(path)
		switch seen[path] {
		case 1:
			return fmt.Errorf("config include cycle at %q", path)
		case 2:
			return nil
		}
		seen[path] = 1
		raw, err := readStableRegularFile(path)
		if err != nil {
			return fmt.Errorf("read legacy config %q: %w", path, err)
		}
		var object map[string]json.RawMessage
		if err := unmarshalJSONObject(raw, &object); err != nil {
			return fmt.Errorf("parse legacy config %q: %w", path, err)
		}
		files = append(files, legacyConfigFile{path: path, raw: raw})
		var includes []string
		if value := object["$include"]; len(value) > 0 {
			if err := json.Unmarshal(value, &includes); err != nil {
				return fmt.Errorf("parse config includes in %q: %w", path, err)
			}
		}
		for _, include := range includes {
			includePath, err := safeLegacyIncludePath(base, filepath.Dir(path), include)
			if err != nil {
				return fmt.Errorf("unsafe config include %q in %q: %w", include, path, err)
			}
			if err := visit(includePath); err != nil {
				return err
			}
		}
		seen[path] = 2
		return nil
	}
	if err := visit(root); err != nil {
		return nil, "", err
	}
	return files, root, nil
}

func safeLegacyIncludePath(base, parent, include string) (string, error) {
	include = strings.TrimSpace(include)
	if include == "" {
		return "", errors.New("empty path")
	}
	if strings.ContainsAny(include, "\r\n\x00") {
		return "", errors.New("path contains CR/LF/NUL")
	}
	for _, part := range strings.FieldsFunc(include, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	path := include
	if !filepath.IsAbs(path) {
		path = filepath.Join(parent, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the resolved config directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if resolved != path {
		return "", fmt.Errorf("symlink component in %q", path)
	}
	return path, nil
}

func readStableRegularFile(path string) ([]byte, error) {
	return readStableRegularFileLimit(path, maxSnapshotFileBytes)
}

func readStableRegularFileLimit(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("path must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("path changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("path changed while reading")
	}
	return raw, nil
}

func legacyAgentEvidencePaths(files []legacyConfigFile) ([]string, error) {
	set := map[string]struct{}{}
	for _, file := range files {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(file.raw, &root); err != nil {
			return nil, err
		}
		var agents map[string]json.RawMessage
		if json.Unmarshal(root["agents"], &agents) != nil {
			continue
		}
		for _, kind := range []string{"packages", "plugins", "marketplaces"} {
			var entries []map[string]json.RawMessage
			if json.Unmarshal(agents[kind], &entries) != nil {
				continue
			}
			for _, entry := range entries {
				for _, key := range []string{"path", "install_path", "installPath", "source_path", "sourcePath", "installLocation", "source"} {
					var value string
					if json.Unmarshal(entry[key], &value) != nil || value == "" || strings.ContainsAny(value, "\r\n\x00") {
						continue
					}
					local := key != "source" || filepath.IsAbs(value) || strings.HasPrefix(value, ".")
					if !local {
						continue
					}
					path, err := filepath.Abs(value)
					if err != nil {
						return nil, err
					}
					resolved, err := filepath.EvalSymlinks(path)
					if err != nil {
						return nil, fmt.Errorf("resolve local %s evidence %q: %w", kind, value, err)
					}
					if resolved != filepath.Clean(path) {
						return nil, fmt.Errorf("local %s evidence %q contains a symlink", kind, value)
					}
					set[path] = struct{}{}
				}
			}
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	if len(paths) > maxLegacyEvidenceEntries {
		return nil, fmt.Errorf("local evidence roots exceed limit %d", maxLegacyEvidenceEntries)
	}
	sort.Strings(paths)
	return paths, nil
}

func copyLegacyEvidence(source, destination string, entries *int, total *int64) error {
	baseDepth := len(strings.Split(filepath.Clean(source), string(filepath.Separator)))
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		*entries++
		if *entries > maxLegacyEvidenceEntries {
			return fmt.Errorf("entry limit %d exceeded", maxLegacyEvidenceEntries)
		}
		if len(strings.Split(filepath.Clean(path), string(filepath.Separator)))-baseDepth > maxLegacyEvidenceDepth {
			return fmt.Errorf("depth limit %d exceeded", maxLegacyEvidenceDepth)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %q is not allowed", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel != "." && unsafeLegacyEvidenceName(filepath.Base(path)) {
			return fmt.Errorf("sensitive or generated path %q requires manual review", path)
		}
		target := destination
		if rel != "." {
			target = filepath.Join(destination, rel)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular evidence %q is not allowed", path)
		}
		raw, err := readStableRegularFileLimit(path, maxLegacyEvidenceFile)
		if err != nil {
			return err
		}
		*total += int64(len(raw))
		if *total > maxLegacyEvidenceBytes {
			return fmt.Errorf("evidence exceeds %d bytes", maxLegacyEvidenceBytes)
		}
		mode := info.Mode().Perm() &^ 0o222
		if mode&0o400 == 0 {
			mode |= 0o400
		}
		return os.WriteFile(target, raw, mode)
	})
}

func requireLegacyBundleRoot(root string) error {
	markers := []string{
		"apm.yml",
		"SKILL.md",
		filepath.Join(".codex-plugin", "plugin.json"),
		filepath.Join(".claude-plugin", "plugin.json"),
		filepath.Join(".claude-plugin", "marketplace.json"),
	}
	for _, marker := range markers {
		if info, err := os.Lstat(filepath.Join(root, marker)); err == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	skills, _ := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if len(skills) > 0 {
		return nil
	}
	return errors.New("path is not a recognized APM, plugin, marketplace, or skill bundle")
}

func unsafeLegacyEvidenceName(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".git" || lower == "node_modules" || lower == "target" || lower == "build" || lower == "dist" || lower == ".venv" || lower == "venv" {
		return true
	}
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}
	switch lower {
	case "credentials", "credentials.json", "id_rsa", "id_ed25519", ".npmrc", ".pypirc", ".netrc":
		return true
	}
	return false
}

func makeSnapshotReadOnly(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], 0o500); err != nil {
			return err
		}
	}
	return nil
}

func makeSnapshotWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return err
	})
}

func removeLegacyAgentFields(raw map[string]json.RawMessage) bool {
	changed := false
	if _, ok := raw["agents"]; ok {
		delete(raw, "agents")
		changed = true
	}
	if removeRawObjectFields(raw, "settings", removedAgentSettingsFields) {
		changed = true
	}
	var hosts map[string]json.RawMessage
	if value := raw["host_settings"]; len(value) > 0 && json.Unmarshal(value, &hosts) == nil {
		hostsChanged := false
		for name := range hosts {
			var host map[string]json.RawMessage
			if json.Unmarshal(hosts[name], &host) != nil || !deleteRawFields(host, removedAgentSettingsFields) {
				continue
			}
			hosts[name], _ = json.Marshal(host)
			hostsChanged = true
			changed = true
		}
		if changedHosts, _ := json.Marshal(hosts); hostsChanged {
			raw["host_settings"] = changedHosts
		}
	}
	var groups []json.RawMessage
	if value := raw["groups"]; len(value) > 0 && json.Unmarshal(value, &groups) == nil {
		groupsChanged := false
		for i := range groups {
			var group map[string]json.RawMessage
			if json.Unmarshal(groups[i], &group) != nil || !deleteRawFields(group, removedAgentGroupFields) {
				continue
			}
			groups[i], _ = json.Marshal(group)
			groupsChanged = true
		}
		if groupsChanged {
			raw["groups"], _ = json.Marshal(groups)
			changed = true
		}
	}
	return changed
}

func removeRawObjectFields(raw map[string]json.RawMessage, key string, fields []string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw[key], &object) != nil || !deleteRawFields(object, fields) {
		return false
	}
	raw[key], _ = json.Marshal(object)
	return true
}

func deleteRawFields(raw map[string]json.RawMessage, fields []string) bool {
	changed := false
	for _, field := range fields {
		if _, ok := raw[field]; ok {
			delete(raw, field)
			changed = true
		}
	}
	return changed
}
