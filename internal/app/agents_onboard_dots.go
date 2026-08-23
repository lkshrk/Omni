package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

type OnboardDotsRef struct {
	Entry                string `json:"entry"`
	Subpath              string `json:"subpath,omitempty"`
	TargetPath           string `json:"target_path"`
	SourcePath           string `json:"source_path"`
	OwnerRoot            string `json:"owner_root"`
	OwnershipFingerprint string `json:"ownership_fingerprint"`
	Native               bool   `json:"native,omitempty"`
}

func extractNativeCandidates(deployDirs []string, owned []OnboardCandidate) ([]OnboardCandidate, []OnboardSourcePreimage, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	ownedPaths := make(map[string]bool, len(owned))
	for _, candidate := range owned {
		if candidate.Dots != nil {
			ownedPaths[filepath.Clean(candidate.Dots.TargetPath)] = true
		}
	}
	var candidates []OnboardCandidate
	var preimages []OnboardSourcePreimage
	seenRoots := map[string]bool{}
	for _, deployDir := range deployDirs {
		deployDir = filepath.Clean(filepath.FromSlash(deployDir))
		if deployDir == "." || filepath.IsAbs(deployDir) || deployDir == ".." || strings.HasPrefix(deployDir, ".."+string(os.PathSeparator)) {
			continue
		}
		root := filepath.Join(home, deployDir)
		if seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		if info, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, nil, err
		} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root {
				return nil
			}
			if nativeClientManagedSubtree(root, path, entry) {
				return filepath.SkipDir
			}
			kind, ok := apmResourceBoundary(root, path, entry)
			if !ok {
				return nil
			}
			if ownedPaths[filepath.Clean(path)] || entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			fingerprint, scanErr := onboardTreeFingerprint(path, root)
			info, err := entry.Info()
			if err != nil {
				return err
			}
			preID := canonicalDigest(map[string]any{"path": path, "kind": "native"})[:24]
			id := digest("omni-native-v1", kind, path, fingerprint)
			payload := map[string]any{"path": path}
			if scanErr != nil {
				payload["blocker"] = "unsafe-unowned-symlink"
				payload["detail"] = scanErr.Error()
				fingerprint = digest("blocked", scanErr.Error())
			}
			raw, _ := json.Marshal(payload)
			preimages = append(preimages, OnboardSourcePreimage{ID: preID, AbsolutePath: path, Kind: "native", Size: info.Size(), Mode: uint32(info.Mode().Perm()), ContentFingerprint: fingerprint})
			candidates = append(candidates, OnboardCandidate{ID: id, Kind: kind, Name: entry.Name(), SourceHandle: "native:" + path, Payload: raw, ContentFingerprint: fingerprint, SourcePreimageIDs: []string{preID}, Dots: &OnboardDotsRef{TargetPath: path, SourcePath: path, OwnerRoot: filepath.Dir(path), OwnershipFingerprint: fingerprint, Native: true}})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	sort.Slice(preimages, func(i, j int) bool { return preimages[i].ID < preimages[j].ID })
	return candidates, preimages, nil
}

func nativeClientManagedSubtree(root, path string, entry fs.DirEntry) bool {
	if !entry.IsDir() {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if parts[0] == ".tmp" || parts[0] == ".cache" {
		return true
	}
	if len(parts) >= 2 && parts[0] == "skills" && parts[1] == ".system" {
		return true
	}
	return len(parts) >= 2 && parts[0] == "plugins" && (parts[1] == "cache" || parts[1] == "marketplaces")
}

func (a *App) extractDotsCandidates() ([]OnboardCandidate, []OnboardSourcePreimage, error) {
	availability, err := a.DotsSyncAvailability()
	var repoSetting string
	var entries []config.DotEntry
	if err != nil {
		repoSetting, entries, err = legacyOnboardDotsConfig(a.ConfigPath)
		if err != nil {
			return nil, nil, err
		}
		if repoSetting == "" {
			return nil, nil, nil
		}
	} else {
		if !availability.Configured {
			return nil, nil, nil
		}
		repoSetting = availability.RepoPath
		rootCfg, loadErr := a.loadConfig()
		if loadErr != nil {
			return nil, nil, loadErr
		}
		groups := rootCfg.Groups
		if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
			groups = effective
		}
		entries = collectDots(rootCfg, groups)
	}
	repoPath, err := resolveRepoPath(repoSetting)
	if err != nil {
		return nil, nil, err
	}
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	dotsRoot := dotsContentPath(repoPath)
	engine, err := dots.NewEngine(dotsRoot, entries)
	if err != nil {
		return nil, nil, err
	}
	var candidates []OnboardCandidate
	var preimages []OnboardSourcePreimage
	for _, entry := range engine.Entries {
		if entry.Ignored {
			continue
		}
		if err := validateOnboardSourceAncestors(dotsRoot, entry.SourcePath); err != nil {
			return nil, nil, err
		}
		info, statErr := os.Lstat(entry.SourcePath)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, nil, statErr
		}
		preID := canonicalDigest(map[string]any{"path": entry.SourcePath, "kind": "dots"})[:24]
		fingerprint, scanErr := onboardTreeFingerprint(entry.SourcePath, entry.SourcePath)
		if scanErr != nil {
			fingerprint = digest("blocked", scanErr.Error())
		}
		preimages = append(preimages, OnboardSourcePreimage{ID: preID, AbsolutePath: entry.SourcePath, Kind: "dots", Size: info.Size(), Mode: uint32(info.Mode().Perm()), ContentFingerprint: fingerprint})
		walkErr := filepath.WalkDir(entry.SourcePath, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			kind, ok := apmResourceBoundary(entry.SourcePath, path, d)
			if !ok {
				return nil
			}
			rel, relErr := filepath.Rel(entry.SourcePath, path)
			if relErr != nil {
				return relErr
			}
			contentFingerprint, unsafeErr := onboardTreeFingerprint(path, entry.SourcePath)
			payload := map[string]any{"path": path}
			if unsafeErr != nil {
				payload["blocker"] = "unsafe-unowned-symlink"
				payload["detail"] = unsafeErr.Error()
				contentFingerprint = digest("blocked", unsafeErr.Error())
			}
			raw, _ := json.Marshal(payload)
			name := filepath.Base(path)
			id := digest("omni-dots-v1", kind, entry.Name, filepath.ToSlash(rel), contentFingerprint)
			candidates = append(candidates, OnboardCandidate{ID: id, Kind: kind, Name: name, SourceHandle: entry.Name + ":" + filepath.ToSlash(rel), Payload: raw, ContentFingerprint: contentFingerprint, SourcePreimageIDs: []string{preID}, Dots: &OnboardDotsRef{Entry: entry.Name, Subpath: filepath.ToSlash(rel), TargetPath: filepath.Join(entry.TargetPath, rel), SourcePath: path, OwnerRoot: dotsRoot, OwnershipFingerprint: digest(entry.SourcePath, entry.TargetPath, contentFingerprint)}})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
		if walkErr != nil {
			return nil, nil, fmt.Errorf("scan dots entry %q: %w", entry.Name, walkErr)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	sort.Slice(preimages, func(i, j int) bool { return preimages[i].ID < preimages[j].ID })
	return candidates, preimages, nil
}

func legacyOnboardDotsConfig(configPath string) (string, []config.DotEntry, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", nil, err
	}
	var docs []legacyDocument
	if err := readLegacyDocument(abs, true, map[string]bool{}, map[string]bool{}, &docs); err != nil {
		return "", nil, err
	}
	active := map[string]bool{currentMachineGroupName(): true, shortHostname(currentHostname()): true}
	for _, doc := range docs {
		var hosts map[string][]string
		if json.Unmarshal(doc.Raw["hosts"], &hosts) == nil {
			for host, groups := range hosts {
				if host == currentMachineGroupName() || host == shortHostname(currentHostname()) {
					for _, group := range groups {
						active[group] = true
					}
				}
			}
		}
	}
	var repo string
	var entries []config.DotEntry
	for _, doc := range docs {
		var settings struct {
			DotsRepo     string `json:"dots_repo"`
			DotsDisabled bool   `json:"dots_disabled"`
		}
		if json.Unmarshal(doc.Raw["settings"], &settings) == nil && settings.DotsRepo != "" {
			repo = settings.DotsRepo
		}
		if settings.DotsDisabled {
			return "", nil, nil
		}
		var groups []struct {
			Name string            `json:"name"`
			Dots []config.DotEntry `json:"dots"`
		}
		if err := json.Unmarshal(doc.Raw["groups"], &groups); err == nil {
			for _, group := range groups {
				if active[group.Name] {
					entries = append(entries, group.Dots...)
				}
			}
		}
		var hostSettings map[string]struct {
			DotsRepo     string `json:"dots_repo"`
			DotsDisabled bool   `json:"dots_disabled"`
		}
		if json.Unmarshal(doc.Raw["host_settings"], &hostSettings) == nil {
			for host, settings := range hostSettings {
				if host != currentMachineGroupName() && host != shortHostname(currentHostname()) {
					continue
				}
				if settings.DotsDisabled {
					return "", nil, nil
				}
				if settings.DotsRepo != "" {
					repo = settings.DotsRepo
				}
			}
		}
	}
	return repo, entries, nil
}

func apmResourceBoundary(root, path string, d fs.DirEntry) (string, bool) {
	if d.IsDir() {
		for marker, kind := range map[string]string{"apm.yml": "package", "SKILL.md": "skill", ".codex-plugin/plugin.json": "plugin", ".claude-plugin/plugin.json": "plugin"} {
			if info, err := os.Lstat(filepath.Join(path, filepath.FromSlash(marker))); err == nil && info.Mode().IsRegular() {
				return kind, true
			}
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return "", false
	}
	primitive := parts[len(parts)-2]
	if len(parts) != 2 && primitive != "hooks" {
		return "", false
	}
	switch primitive {
	case "agents", "prompts", "commands":
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return strings.TrimSuffix(primitive, "s"), true
		}
	case "hooks":
		if d.IsDir() || strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return "hook", true
		}
	}
	return "", false
}

// onboardTreeFingerprint validates descendants without following links while
// enumeration is in progress. Only links resolving inside the owning dots tree
// are accepted.
func onboardTreeFingerprint(path, ownerRoot string) (string, error) {
	var rows []string
	err := filepath.WalkDir(path, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return fmt.Errorf("%s: broken or cyclic symlink", current)
			}
			if !onboardPathWithin(ownerRoot, resolved) {
				return fmt.Errorf("%s resolves outside %s", current, ownerRoot)
			}
			resolvedInfo, err := os.Stat(resolved)
			if err != nil {
				return fmt.Errorf("%s: resolve symlink target: %w", current, err)
			}
			if resolvedInfo.IsDir() && onboardPathWithin(resolved, current) {
				return fmt.Errorf("%s: cyclic symlink resolves to ancestor %s", current, resolved)
			}
			rows = append(rows, filepath.ToSlash(rel)+"\x00link\x00"+resolved)
			return nil
		}
		row := filepath.ToSlash(rel) + "\x00" + info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			row += "\x00" + digestBytes(data)
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(rows)
	return digest(strings.Join(rows, "\n")), nil
}

func onboardPathWithin(root, path string) bool {
	canonical := func(value string) string {
		value = filepath.Clean(value)
		if absolute, err := filepath.Abs(value); err == nil {
			value = absolute
		}
		if resolved, err := filepath.EvalSymlinks(value); err == nil {
			value = resolved
		}
		value = filepath.Clean(value)
		if runtime.GOOS == "windows" {
			value = strings.ToLower(value)
		}
		return value
	}
	rel, err := filepath.Rel(canonical(root), canonical(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateOnboardSourceAncestors(root, source string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(source))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("dots source %q is outside root %q", source, root)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	current := canonicalRoot
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("dots source ancestor %q is a symlink", current)
		}
	}
	return nil
}

func onboardContentFingerprint(path string) (string, error) {
	return onboardContentFingerprintIgnoring(path, "")
}

func onboardContentFingerprintIgnoring(path, ignoredRootFile string) (string, error) {
	var rows []string
	err := filepath.WalkDir(path, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("materialized path still contains symlink: %s", current)
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == "." && d.IsDir() {
			return nil
		}
		if relSlash == ignoredRootFile || (ignoredRootFile == "plugin-generated" && (relSlash == "apm.yml" || relSlash == ".apm" || strings.HasPrefix(relSlash, ".apm/"))) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		row := relSlash + "\x00" + info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			row += "\x00" + digestBytes(data)
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(rows)
	return digest(strings.Join(rows, "\n")), nil
}

func (a *App) materializeOnboardDot(ctx context.Context, ref OnboardDotsRef) error {
	if err := validateOnboardSourceAncestors(ref.OwnerRoot, ref.SourcePath); err != nil {
		return err
	}
	if _, err := onboardTreeFingerprint(ref.SourcePath, ref.SourcePath); err != nil {
		return err
	}
	dirModes, err := onboardDirectoryModes(ref.SourcePath)
	if err != nil {
		return err
	}
	if ref.Subpath == "." || ref.Subpath == "" {
		if err := materializeWholeOnboardDot(ref); err != nil {
			return err
		}
		if err := a.DotsSetEntryIgnoredContext(ctx, ref.Entry, ref.TargetPath, true); err != nil {
			return err
		}
		if _, err := os.Stat(ref.SourcePath); err == nil {
			if err := a.removeWholeOnboardDotSource(ref.SourcePath); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := applyOnboardDirectoryModes(ref.TargetPath, dirModes); err != nil {
			return err
		}
		info, err := os.Lstat(ref.TargetPath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("whole dots entry %q was not materialized", ref.Entry)
		}
		if _, err := os.Stat(ref.SourcePath); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("whole dots source %q was not removed", ref.SourcePath)
		}
		return nil
	}
	if err := a.DotsAddIgnorePatternContext(ctx, ref.Entry, ref.Subpath); err != nil {
		return err
	}
	info, err := os.Lstat(ref.TargetPath)
	if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		leaves, err := onboardDotLeafPatterns(ref.SourcePath, ref.Subpath)
		if err != nil {
			return err
		}
		for _, pattern := range leaves {
			if _, err := a.DotsEjectIgnoredPathsContext(ctx, ref.Entry, pattern); err != nil {
				return err
			}
		}
		if err := applyOnboardDirectoryModes(ref.TargetPath, dirModes); err != nil {
			return err
		}
		return os.RemoveAll(ref.SourcePath)
	}
	_, err = a.DotsEjectIgnoredPathsContext(ctx, ref.Entry, ref.Subpath)
	if err != nil {
		return err
	}
	return applyOnboardDirectoryModes(ref.TargetPath, dirModes)
}

func onboardDirectoryModes(root string) (map[string]os.FileMode, error) {
	out := map[string]os.FileMode{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = info.Mode().Perm()
		return nil
	})
	return out, err
}
func applyOnboardDirectoryModes(root string, modes map[string]os.FileMode) error {
	for rel, mode := range modes {
		path := root
		if rel != "." {
			path = filepath.Join(root, rel)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("materialized directory %q is unsafe", path)
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func onboardDotOwnershipReleased(configPath string, ref OnboardDotsRef) bool {
	if _, err := os.Lstat(ref.SourcePath); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	if strings.TrimSpace(configPath) == "" {
		return true
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return false
	}
	var docs []legacyDocument
	if readLegacyDocument(abs, true, map[string]bool{}, map[string]bool{}, &docs) != nil {
		return false
	}
	for _, doc := range docs {
		var groups []struct {
			Name string            `json:"name"`
			Dots []config.DotEntry `json:"dots"`
		}
		if json.Unmarshal(doc.Raw["groups"], &groups) != nil {
			continue
		}
		for _, group := range groups {
			for _, entry := range group.Dots {
				if entry.Name != ref.Entry {
					continue
				}
				if ref.Subpath == "" || ref.Subpath == "." {
					if entry.Ignored {
						return true
					}
				} else if slices.Contains(entry.Ignore, ref.Subpath) {
					return true
				}
			}
		}
	}
	return false
}

func onboardDotLeafPatterns(source, subpath string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if d.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(filepath.Join(subpath, rel)))
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func (a *App) removeWholeOnboardDotSource(source string) error {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return err
	}
	root, err := existingDotsContentPath(repoPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, source)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove dots source %q outside %q", source, root)
	}
	return os.RemoveAll(source)
}

func materializeWholeOnboardDot(ref OnboardDotsRef) (retErr error) {
	if err := dots.ValidateHomeTargetPath(ref.TargetPath); err != nil {
		return err
	}
	stagingRoot, err := os.MkdirTemp(filepath.Dir(ref.TargetPath), ".omni-onboard-materialize-*")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(stagingRoot)) }()
	staged := filepath.Join(stagingRoot, "content")
	if err := dots.CopyDotPath(ref.SourcePath, staged, nil); err != nil {
		return err
	}
	want, err := onboardContentFingerprint(staged)
	if err != nil {
		return err
	}
	info, err := os.Lstat(ref.TargetPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		if !dots.SameResolvedPath(ref.TargetPath, ref.SourcePath) {
			return fmt.Errorf("whole dots target %q is not the managed Stow link", ref.TargetPath)
		}
		if err := os.Remove(ref.TargetPath); err != nil {
			return err
		}
	case info.Mode().IsRegular() || info.IsDir():
		got, hashErr := onboardContentFingerprint(ref.TargetPath)
		if hashErr != nil || got != want {
			return fmt.Errorf("whole dots target %q differs from staged content", ref.TargetPath)
		}
		return nil
	default:
		return fmt.Errorf("whole dots target %q has unsupported file type", ref.TargetPath)
	}
	if err := os.Rename(staged, ref.TargetPath); err != nil {
		if restoreErr := dots.WriteStowShapedSymlink(ref.TargetPath, ref.SourcePath); restoreErr != nil {
			return fmt.Errorf("materialize %q: %w (restore link failed: %v)", ref.TargetPath, err, restoreErr)
		}
		return err
	}
	return nil
}
