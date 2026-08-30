package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const legacyAgentsSnapshotPrefix = ".omni-apm-migration-backup-"

type legacyConfigFile struct {
	path string
	raw  []byte
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
// The returned directory is read-only and remains as recovery evidence after migration.
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
	snapshotDir, err = os.MkdirTemp(filepath.Dir(root), legacyAgentsSnapshotPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("create legacy agents snapshot: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
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
	if err := os.Chmod(snapshotDir, 0o500); err != nil {
		return "", fmt.Errorf("make legacy agents snapshot immutable: %w", err)
	}
	complete = true
	return snapshotDir, nil
}

// CleanupLegacyAgentsConfig removes only the retired agent fields from the
// resolved config and its includes. Every source is identity-checked before an
// atomic replacement; callers must capture a snapshot first.
func CleanupLegacyAgentsConfig(configPath string) (retErr error) {
	lock, err := AcquireWriteLock(configPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()

	files, _, err := legacyConfigFiles(configPath)
	if err != nil {
		return err
	}
	type change struct {
		legacyConfigFile
		rendered []byte
	}
	changes := make([]change, 0, len(files))
	for _, file := range files {
		var raw map[string]json.RawMessage
		if err := unmarshalJSONObject(file.raw, &raw); err != nil {
			return fmt.Errorf("parse legacy config %q: %w", file.path, err)
		}
		if removeLegacyAgentFields(raw) {
			changes = append(changes, change{legacyConfigFile: file, rendered: renderFragmentRaw(raw, nil)})
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
	for _, change := range changes {
		if err := atomicWriteUnlocked(change.path, change.rendered); err != nil {
			return fmt.Errorf("clean legacy config %q: %w", change.path, err)
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
	raw, err := io.ReadAll(io.LimitReader(file, maxSnapshotFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxSnapshotFileBytes)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("path changed while reading")
	}
	return raw, nil
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
