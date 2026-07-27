package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

const (
	// The verdict is read on every render, so without a cadence a table redraw becomes one network call per row.
	skillOutdatedRecheckInterval = 6 * time.Hour
	// A source that does not answer promptly degrades to unknown rather than stalling a whole table.
	skillProbeTimeout = 10 * time.Second
)

// Selects which recorded baseline a probe result may be compared against.
type skillProbeKind int

const (
	// No cheap identity exists: a Git subpath's repo HEAD moves for commits that never touch it, yielding only false positives.
	skillProbeNone skillProbeKind = iota
	skillProbeGitRemote
	skillProbeLocalTree
	skillProbeIndexDigest
)

// Persisted, so it must stay stable across releases.
func probeKindName(kind skillProbeKind) string {
	switch kind {
	case skillProbeGitRemote:
		return "git-remote"
	case skillProbeLocalTree:
		return "local-tree"
	case skillProbeIndexDigest:
		return "index-digest"
	default:
		return ""
	}
}

// OutdatedState — Never touches the network; nil means unknown.
func (s *Skills) OutdatedState(ctx context.Context, source string) *bool {
	parsed, err := ParseSkillSource(source)
	if err != nil {
		return nil
	}
	metadata, ok := s.loadMetadata(ctx, s.packageDir(parsed.Source))
	if !ok {
		return nil
	}
	return metadata.Outdated
}

// CheckOutdated — Never errors: an unreachable source is simply unknown, because an unknown row must still render.
func (s *Skills) CheckOutdated(ctx context.Context, pkg config.SkillPackage, force bool) *bool {
	if s == nil {
		return nil
	}
	pkg, err := normalizeSkillPackage(pkg)
	if err != nil {
		return nil
	}
	packageDir := s.packageDir(pkg.Source)
	metadata, ok := s.loadMetadata(ctx, packageDir)
	if !ok {
		return nil
	}
	if !force && !metadata.OutdatedCheckedAt.IsZero() &&
		time.Since(metadata.OutdatedCheckedAt) < skillOutdatedRecheckInterval {
		return metadata.Outdated
	}
	probe, kind := s.probeSource(ctx, pkg)
	baseline := outdatedBaseline(metadata, kind)
	var verdict *bool
	if kind != skillProbeNone && baseline != "" {
		behind := probe != baseline
		verdict = &behind
	}
	metadata.Outdated = verdict
	metadata.OutdatedCheckedAt = time.Now().UTC()
	if kind != skillProbeNone {
		switch {
		case baseline == "" && probe != "":
			// The recorded probe belongs to another value space; re-baselining here is what stops a kind switch from reading as outdated forever.
			metadata.SourceProbe = probe
			metadata.SourceProbeKind = probeKindName(kind)
		case metadata.SourceProbe == "" && baseline != "":
			metadata.SourceProbe = baseline
			metadata.SourceProbeKind = probeKindName(kind)
		}
	}
	s.writeMetadata(ctx, packageDir, metadata)
	return verdict
}

// Falls back to SourceFingerprint only for a Git remote, where a recorded `rev-parse HEAD` and a probed `ls-remote` sha are the same value.
func outdatedBaseline(metadata skillMetadata, kind skillProbeKind) string {
	if probe := recordedProbe(metadata, kind); probe != "" {
		return probe
	}
	if kind == skillProbeGitRemote && isGitSha(metadata.SourceFingerprint) {
		return metadata.SourceFingerprint
	}
	return ""
}

// A record written before the kind was persisted is trusted only where its shape is unambiguous: a 40-hex sha can have come from no probe but a Git remote, while both other kinds emit 64 hex.
func recordedProbe(metadata skillMetadata, kind skillProbeKind) string {
	if metadata.SourceProbe == "" {
		return ""
	}
	if metadata.SourceProbeKind == "" {
		if kind == skillProbeGitRemote && isGitSha(metadata.SourceProbe) {
			return metadata.SourceProbe
		}
		return ""
	}
	if metadata.SourceProbeKind != probeKindName(kind) {
		return ""
	}
	return metadata.SourceProbe
}

func (s *Skills) probeSource(ctx context.Context, pkg config.SkillPackage) (string, skillProbeKind) {
	raw, err := s.resolveSource(pkg.Source)
	if err != nil {
		return "", skillProbeNone
	}
	if info, statErr := os.Stat(raw); statErr == nil && info.IsDir() {
		if dirExists(filepath.Join(raw, ".git")) {
			return probeGitRemote(ctx, raw, pkg.Ref)
		}
		if pkg.Ref != "" {
			return "", skillProbeNone
		}
		hash, hashErr := hashTree(raw)
		if hashErr != nil {
			return "", skillProbeNone
		}
		return hash, skillProbeLocalTree
	}
	if strings.HasPrefix(pkg.Source, ".") || strings.HasPrefix(pkg.Source, "/") ||
		strings.HasPrefix(pkg.Source, "~") || strings.HasPrefix(pkg.Source, "file://") {
		return "", skillProbeNone
	}
	return s.probeRemote(ctx, pkg)
}

func (s *Skills) probeRemote(ctx context.Context, pkg config.SkillPackage) (string, skillProbeKind) {
	source := pkg.Source
	if !strings.Contains(source, "://") && !strings.Contains(source, ":") {
		parts := strings.Split(source, "/")
		if len(parts) != 2 {
			return "", skillProbeNone
		}
		return probeGitRemote(ctx, "https://github.com/"+parts[0]+"/"+parts[1]+".git", pkg.Ref)
	}
	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if digest, ok := s.probeWellKnown(ctx, pkg); ok {
			return digest, skillProbeIndexDigest
		}
	}
	if remote, subpath := splitGitRemoteSubpath(source); subpath == "" {
		return probeGitRemote(ctx, remote, pkg.Ref)
	}
	return "", skillProbeNone
}

// Reports ok=false for any host without a recognized catalog so the caller falls through to the Git probe.
func (s *Skills) probeWellKnown(ctx context.Context, pkg config.SkillPackage) (string, bool) {
	if pkg.Ref != "" {
		return "", false
	}
	indexURL, err := discoveryIndexURL(pkg.Source, false)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, skillProbeTimeout)
	defer cancel()
	index, _, err := s.fetchIndex(ctx, indexURL)
	if errors.Is(err, errNoWellKnown) {
		indexURL, err = discoveryIndexURL(pkg.Source, true)
		if err != nil {
			return "", false
		}
		index, _, err = s.fetchIndex(ctx, indexURL)
	}
	if err != nil || index.Schema != wellKnownSchemaV02 {
		return "", false
	}
	entries, err := selectWellKnownSkills(index, pkg.Skills)
	if err != nil {
		return "", false
	}
	pairs := make([]string, 0, len(entries))
	for _, entry := range entries {
		pairs = append(pairs, entry.Name+"\x00"+entry.Digest)
	}
	sort.Strings(pairs)
	hash := sha256.New()
	for _, pair := range pairs {
		if _, err := io.WriteString(hash, pair); err != nil {
			return "", false
		}
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), true
}

// A ref pinned to a full commit sha resolves to itself, so such a package is outdated only if the installed fingerprint drifted off it.
func probeGitRemote(ctx context.Context, remote, ref string) (string, skillProbeKind) {
	if strings.HasPrefix(remote, "-") {
		return "", skillProbeNone
	}
	if ref != "" && !validGitRef(ref) {
		return "", skillProbeNone
	}
	pattern := ref
	if pattern == "" {
		pattern = "HEAD"
	}
	ctx, cancel := context.WithTimeout(ctx, skillProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "ls-remote", remote, pattern).Output()
	if err != nil {
		if isGitSha(ref) {
			return ref, skillProbeGitRemote
		}
		return "", skillProbeNone
	}
	for _, line := range strings.Split(string(out), "\n") {
		sha, _, found := strings.Cut(strings.TrimSpace(line), "\t")
		if found && isGitSha(sha) {
			return sha, skillProbeGitRemote
		}
	}
	if isGitSha(ref) {
		return ref, skillProbeGitRemote
	}
	return "", skillProbeNone
}

func isGitSha(value string) bool {
	if len(value) != 40 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Skills) loadMetadata(ctx context.Context, packageDir string) (skillMetadata, bool) {
	if s == nil || s.state == nil {
		return skillMetadata{}, false
	}
	raw, err := s.state.GetState(ctx, metadataStateKey(packageDir))
	if err != nil {
		return skillMetadata{}, false
	}
	var metadata skillMetadata
	if json.Unmarshal([]byte(raw), &metadata) != nil {
		return skillMetadata{}, false
	}
	return metadata, true
}

// Best-effort: the store is advisory and a failed write costs only one re-probe.
func (s *Skills) writeMetadata(ctx context.Context, packageDir string, metadata skillMetadata) {
	if s == nil || s.state == nil {
		return
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	_ = s.state.SetState(ctx, metadataStateKey(packageDir), string(data))
}

func (s *Skills) PackageStore(source string) (string, string, error) {
	parsed, err := ParseSkillSource(source)
	if err != nil {
		return "", "", err
	}
	dir := s.packageDir(parsed.Source)
	hash, err := hashTree(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return dir, "", nil
		}
		return dir, "", fmt.Errorf("hashing installed package %s: %w", dir, err)
	}
	return dir, hash, nil
}

type SkillEntryKind string

const (
	SkillEntryOwnedLink SkillEntryKind = "owned-link"
	// A real directory matching the package byte for byte; restore converges it to a link on its own.
	SkillEntryCopy SkillEntryKind = "copy"
	// A real directory whose content differs.
	SkillEntryDrifted SkillEntryKind = "drifted"
	// A symlink pointing outside the package store.
	SkillEntryForeign SkillEntryKind = "foreign"
	SkillEntryMissing SkillEntryKind = "missing"
)

// SkillEntryState — Link carries the symlink target for the link-shaped kinds and is empty otherwise.
type SkillEntryState struct {
	Target string
	Dir    string
	Skill  string
	Kind   SkillEntryKind
	Link   string
}

// EntryStates — The per-entry detail behind the aggregate Inventory reports; Names narrows it to those skills.
func (s *Skills) EntryStates(source string, targetIDs, names []string) ([]SkillEntryState, error) {
	parsed, err := ParseSkillSource(source)
	if err != nil {
		return nil, err
	}
	packageDir := s.packageDir(parsed.Source)
	skills, err := skillDirNames(packageDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	targets, unknown := s.knownTargets(targetIDs)
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown agent target %q", unknown[0])
	}
	var out []SkillEntryState
	for _, target := range targets {
		for _, dir := range target.skillDirs(s.home) {
			for _, name := range skills {
				if len(wanted) > 0 && !wanted[name] {
					continue
				}
				out = append(out, entryState(target.ID, dir, name, packageDir))
			}
		}
	}
	return out, nil
}

func entryState(targetID, dir, name, packageDir string) SkillEntryState {
	state := SkillEntryState{Target: targetID, Dir: dir, Skill: name, Kind: SkillEntryMissing}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return state
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, linkErr := os.Readlink(path)
		if linkErr == nil {
			state.Link = link
		}
		state.Kind = SkillEntryForeign
		if ownedLink(path, packageDir) {
			state.Kind = SkillEntryOwnedLink
		}
		return state
	}
	if !info.IsDir() {
		state.Kind = SkillEntryDrifted
		return state
	}
	state.Kind = SkillEntryDrifted
	if same, sameErr := sameTree(path, filepath.Join(packageDir, "skills", name)); sameErr == nil && same {
		state.Kind = SkillEntryCopy
	}
	return state
}
