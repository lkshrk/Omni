package dots

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Matched against the base file name with filepath.Match semantics.
var defaultIgnores = []string{
	".git",

	".DS_Store",
	".Spotlight-V100",
	".fseventsd",
	".localized",

	"Thumbs.db",
	"desktop.ini",

	"*.swp", // Vim swap files
	"*.swo",
	"*~",    // emacs/generic backup files
	"*.bak", // generic backup files (omni's settings.json.bak, others)
	"*.log",

	"__pycache__",
	"*.pyc",

	".cache",
	"cache",
	"caches",
	"node_modules",

	"*.sock",

	// Private-key filenames only: .pub keys stay syncable on purpose.
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"id_ecdsa_sk",
	"id_ed25519_sk",
	"authorized_keys",

	"*.pem",
	"*.key",
	"*.secret",
	"*.age",
	"*.pgp",
	"*.gpg",
	"*.p12", // PKCS#12 certificate bundles (contain private keys)
	"*.pfx", // PFX certificate bundles (contain private keys)
	"*.token",

	".zcompdump*",    // zsh completion dump (machine-specific: hostname + version)
	"fish_variables", // fish universal variables (machine-specific paths)

	"._*", // resource fork sidecar files created by macOS on foreign fs

	// .skill-lock.json stays unignored: a stow --ignore on it silently empties any entry targeting it.
	"Brewfile.lock.json", // Homebrew Bundle lock (machine-specific formulae state)
	"lazy-lock.json",     // Neovim lazy.nvim plugin lock

	"*.sqlite",
	"*.sqlite3",

	"credentials",                          // AWS CLI (~/.aws/credentials), etc.
	"credentials.json",                     // Google Cloud, other OAuth credential files
	"credentials.db",                       // generic credential database
	"auth.json",                            // OAuth state files (Claude Code, OpenAI Codex, etc.)
	"oauth_creds.json",                     // Gemini CLI Google OAuth tokens
	".git-credentials",                     // git plain-text credential store
	".netrc",                               // netrc authentication file (FTP, curl, etc.)
	"application_default_credentials.json", // Google Cloud Application Default Creds
	"accessTokens.json",                    // VS Code / OpenAI Codex access token store
	"msal_token_cache.json",                // Microsoft Authentication Library token cache
	".vault-token",                         // HashiCorp Vault session token
	"secring.gpg",                          // GnuPG secret keyring (legacy format, GnuPG < 2.1)
	"private-keys-v1.d",                    // GnuPG private keys directory (GnuPG >= 2.1)

	// Agent-tool proposals stay per-entry allowlists so trees named projects, plugins, tasks stay trackable.
}

// DefaultIgnores returns a copy of the built-in ignore patterns.
func DefaultIgnores() []string {
	cp := make([]string, len(defaultIgnores))
	copy(cp, defaultIgnores)
	return cp
}

// ShouldIgnore — Later matches override earlier ones; a "!" prefix re-includes after an earlier ignore.
//
// Deprecated: prefer ShouldIgnoreChecked which surfaces malformed patterns.
func ShouldIgnore(basename string, patterns []string) bool {
	matched, _ := ShouldIgnoreChecked(basename, patterns)
	return matched
}

// ShouldIgnorePath — Patterns with a separator match relPath; a leading "/" anchors to the entry root, a trailing "/" covers descendants.
func ShouldIgnorePath(relPath, basename string, patterns []string) bool {
	matched, _ := ShouldIgnorePathChecked(relPath, basename, patterns)
	return matched
}

// ShouldIgnoreAnyPath — For walkers needing patterns evaluated against both the subtree and the original entry root.
func ShouldIgnoreAnyPath(relPaths []string, basename string, patterns []string) bool {
	matched, _ := ShouldIgnoreAnyPathChecked(relPaths, basename, patterns)
	return matched
}

// ShouldIgnoreChecked — Use for user-supplied patterns so typos surface instead of silently skipping or syncing files.
func ShouldIgnoreChecked(basename string, patterns []string) (bool, error) {
	ignored := false
	for _, p := range patterns {
		pattern, err := parseIgnorePattern(p)
		if err != nil {
			return false, err
		}
		if pattern.pathScoped {
			continue
		}
		matched, err := matchIgnorePattern(pattern, basename)
		if err != nil {
			return false, invalidIgnorePatternError(p, err)
		}
		if matched {
			ignored = !pattern.include
		}
	}
	return ignored, nil
}

func ShouldIgnorePathChecked(relPath, basename string, patterns []string) (bool, error) {
	return ShouldIgnoreAnyPathChecked([]string{relPath}, basename, patterns)
}

func ShouldIgnoreAnyPathChecked(relPaths []string, basename string, patterns []string) (bool, error) {
	matcher, err := CompileIgnores(patterns)
	if err != nil {
		return false, err
	}
	return matcher.MatchAnyPath(relPaths, basename)
}

// IgnoreMatcher — Parsed once so tree walks skip re-parsing ~80 patterns per visited file.
type IgnoreMatcher struct {
	raw         []string
	patterns    []ignorePattern
	hasIncluded bool
	literalBase map[string][]int // basename-scoped exact globs → pattern indices
	scanned     []int            // indices of patterns needing a per-target scan
}

// CompileIgnores — Compile once per walk; per-file evaluation then only runs the match itself.
func CompileIgnores(patterns []string) (*IgnoreMatcher, error) {
	m := &IgnoreMatcher{
		raw:         patterns,
		patterns:    make([]ignorePattern, len(patterns)),
		literalBase: make(map[string][]int),
	}
	for i, p := range patterns {
		parsed, err := parseIgnorePattern(p)
		if err != nil {
			return nil, err
		}
		m.patterns[i] = parsed
		if parsed.include && parsed.pathScoped {
			m.hasIncluded = true
		}
		if parsed.kind == ignorePatternLiteral && !parsed.pathScoped {
			m.literalBase[parsed.glob] = append(m.literalBase[parsed.glob], i)
		} else {
			m.scanned = append(m.scanned, i)
		}
	}
	return m, nil
}

// CompileIgnoresLenient — Degrades to an empty matcher when any pattern is invalid.
func CompileIgnoresLenient(patterns []string) *IgnoreMatcher {
	m, err := CompileIgnores(patterns)
	if err != nil {
		m, _ = CompileIgnores(nil)
	}
	return m
}

func (m *IgnoreMatcher) Raw() []string {
	return m.raw
}

func (m *IgnoreMatcher) Ignored(relPath, basename string) bool {
	matched, _ := m.MatchAnyPath([]string{relPath}, basename)
	return matched
}

func (m *IgnoreMatcher) MatchAnyPath(relPaths []string, basename string) (bool, error) {
	if len(relPaths) == 0 {
		relPaths = []string{basename}
	}
	candidates := make([]string, 0, len(relPaths))
	for _, relPath := range relPaths {
		rel := cleanIgnoreRelPath(relPath, basename)
		if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || rel == ".." || strings.HasPrefix(rel, "../") {
			return false, fmt.Errorf("ignore path %q escapes the logical root", relPath)
		}
		if !containsString(candidates, rel) {
			candidates = append(candidates, rel)
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, basename)
	}
	basenameTargets := ignoreBasenameTargets(candidates[0], basename)
	// Later patterns win, so the verdict is the highest matching index regardless of evaluation order.
	last := -1
	for _, target := range basenameTargets {
		for _, i := range m.literalBase[target] {
			if i > last {
				last = i
			}
		}
	}
	for _, i := range m.scanned {
		pattern := m.patterns[i]
		targets := basenameTargets
		if pattern.pathScoped {
			targets = candidates
		}
		for _, target := range targets {
			matched, err := matchIgnorePattern(pattern, target)
			if err != nil {
				return false, invalidIgnorePatternError(m.raw[i], err)
			}
			if matched {
				if i > last {
					last = i
				}
				break
			}
		}
	}
	if last < 0 {
		return false, nil
	}
	return !m.patterns[last].include, nil
}

func (m *IgnoreMatcher) HasIncludedDescendant(relPath string) bool {
	if !m.hasIncluded {
		return false
	}
	rel := filepath.ToSlash(filepath.Clean(relPath))
	if rel == "." || rel == "" {
		return false
	}
	prefix := strings.TrimSuffix(rel, "/") + "/"
	for _, pattern := range m.patterns {
		if !pattern.include || !pattern.pathScoped {
			continue
		}
		if pattern.dirScoped && rel == pattern.glob {
			return true
		}
		if strings.HasPrefix(pattern.glob, prefix) {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func cleanIgnoreRelPath(relPath, basename string) string {
	rel := filepath.ToSlash(filepath.Clean(relPath))
	if rel == "." {
		rel = basename
	}
	return rel
}

// Ancestors come from the primary path only: treating a synthetic prefix as an ancestor would re-ignore content a caller deliberately started inside.
func ignoreBasenameTargets(primary, basename string) []string {
	targets := []string{basename}
	rest := primary
	for {
		i := strings.LastIndexByte(rest, '/')
		if i < 0 {
			return targets
		}
		rest = rest[:i]
		name := rest
		if j := strings.LastIndexByte(rest, '/'); j >= 0 {
			name = rest[j+1:]
		}
		if !containsString(targets, name) {
			targets = append(targets, name)
		}
	}
}

func matchIgnorePattern(pattern ignorePattern, target string) (bool, error) {
	if pattern.dirScoped && (target == pattern.glob || strings.HasPrefix(target, pattern.globSlash)) {
		return true, nil
	}
	matched, err := matchIgnoreGlob(pattern, target)
	if err != nil || matched || pattern.include || !pattern.pathScoped {
		return matched, err
	}
	// Walkers may descend an ignored dir to reach a later include, so descendant checks keep the ancestor's state.
	for ancestor := target; ; {
		i := strings.LastIndexByte(ancestor, '/')
		if i < 0 {
			return false, nil
		}
		ancestor = ancestor[:i]
		matched, err = matchIgnoreGlob(pattern, ancestor)
		if err != nil || matched {
			return matched, err
		}
	}
}

func matchIgnoreGlob(pattern ignorePattern, target string) (bool, error) {
	switch pattern.kind {
	case ignorePatternLiteral:
		return pattern.glob == target, nil
	case ignorePatternSuffix:
		return strings.HasSuffix(target, pattern.affix) &&
			!strings.Contains(target[:len(target)-len(pattern.affix)], "/"), nil
	case ignorePatternPrefix:
		return strings.HasPrefix(target, pattern.affix) &&
			!strings.Contains(target[len(pattern.affix):], "/"), nil
	}
	return filepath.Match(pattern.glob, target)
}

// HasIncludedDescendant — Lets walkers descend ignored directories that contain an explicitly included child.
func HasIncludedDescendant(relPath string, patterns []string) bool {
	rel := filepath.ToSlash(filepath.Clean(relPath))
	if rel == "." || rel == "" {
		return false
	}
	prefix := strings.TrimSuffix(rel, "/") + "/"
	for _, raw := range patterns {
		pattern, err := parseIgnorePattern(raw)
		if err != nil || !pattern.include || !pattern.pathScoped {
			continue
		}
		if pattern.dirScoped && rel == pattern.glob {
			return true
		}
		if strings.HasPrefix(pattern.glob, prefix) {
			return true
		}
	}
	return false
}

// ValidateIgnorePattern — Understands omni's "!" include prefix, "/" root anchor, and trailing "/" directory patterns.
func ValidateIgnorePattern(pattern string) error {
	parsed, err := parseIgnorePattern(pattern)
	if err != nil {
		return err
	}
	if _, err := filepath.Match(parsed.glob, ""); err != nil {
		return invalidIgnorePatternError(pattern, err)
	}
	return nil
}

type ignorePatternKind uint8

const (
	ignorePatternGlob ignorePatternKind = iota
	ignorePatternLiteral
	ignorePatternSuffix // "*X" — X after a single leading star
	ignorePatternPrefix // "X*" — X before a single trailing star
)

type ignorePattern struct {
	glob       string
	globSlash  string // glob + "/", precomputed for dir-scoped prefix checks
	include    bool
	pathScoped bool
	dirScoped  bool
	kind       ignorePatternKind
	affix      string
}

// The pattern universe is defaults plus per-entry globs, so the cache stays small.
var ignorePatternCache sync.Map

func parseIgnorePattern(raw string) (ignorePattern, error) {
	if cached, ok := ignorePatternCache.Load(raw); ok {
		return cached.(ignorePattern), nil
	}
	parsed, err := parseIgnorePatternUncached(raw)
	if err != nil {
		return parsed, err
	}
	ignorePatternCache.Store(raw, parsed)
	return parsed, nil
}

func parseIgnorePatternUncached(raw string) (ignorePattern, error) {
	pattern := raw
	include := false
	if strings.HasPrefix(pattern, "!") {
		include = true
		pattern = strings.TrimPrefix(pattern, "!")
	}
	pathScoped := false
	if strings.HasPrefix(pattern, "/") {
		pathScoped = true
		pattern = strings.TrimPrefix(pattern, "/")
	}
	pattern = filepath.ToSlash(pattern)
	dirScoped := strings.HasSuffix(pattern, "/")
	if dirScoped {
		pathScoped = true
		pattern = strings.TrimSuffix(pattern, "/")
	}
	if pattern == "" {
		return ignorePattern{}, invalidIgnorePatternError(raw, fmt.Errorf("empty pattern"))
	}
	if strings.Contains(pattern, "/") {
		pathScoped = true
	}
	parsed := ignorePattern{glob: pattern, include: include, pathScoped: pathScoped, dirScoped: dirScoped}
	if dirScoped {
		parsed.globSlash = pattern + "/"
	}
	parsed.kind, parsed.affix = classifyIgnoreGlob(pattern)
	return parsed, nil
}

// Only a single edge "*" qualifies: Match's star never crosses "/", which the string fast paths reproduce exactly.
func classifyIgnoreGlob(glob string) (ignorePatternKind, string) {
	switch strings.Count(glob, "*") {
	case 0:
		if !strings.ContainsAny(glob, `?[\`) {
			return ignorePatternLiteral, ""
		}
	case 1:
		if strings.ContainsAny(glob, `?[\`) {
			break
		}
		if rest, ok := strings.CutPrefix(glob, "*"); ok {
			return ignorePatternSuffix, rest
		}
		if rest, ok := strings.CutSuffix(glob, "*"); ok {
			return ignorePatternPrefix, rest
		}
	}
	return ignorePatternGlob, ""
}

func invalidIgnorePatternError(pattern string, err error) error {
	return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
}

// defaultIgnores are assumed valid; unit tests enforce that.
func combinedIgnores(perEntry []string) ([]string, error) {
	var badPatterns []string
	for _, p := range perEntry {
		if err := ValidateIgnorePattern(p); err != nil {
			badPatterns = append(badPatterns, p)
		}
	}
	if len(badPatterns) > 0 {
		return nil, fmt.Errorf("invalid glob pattern(s) in ignore list: %s",
			strings.Join(badPatterns, ", "))
	}
	merged := make([]string, len(defaultIgnores)+len(perEntry))
	copy(merged, defaultIgnores)
	copy(merged[len(defaultIgnores):], perEntry)
	return merged, nil
}
