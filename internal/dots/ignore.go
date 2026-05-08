package dots

import (
	"fmt"
	"path/filepath"
	"strings"
)

// defaultIgnores is the built-in list of patterns always skipped when walking
// a dots entry. Patterns are matched against the base file name using
// filepath.Match semantics.
var defaultIgnores = []string{
	// ── VCS ───────────────────────────────────────────────────────────────────
	".git",

	// ── macOS artifacts ───────────────────────────────────────────────────────
	".DS_Store",
	".Spotlight-V100",
	".fseventsd",
	".localized",

	// ── Windows artifacts ─────────────────────────────────────────────────────
	"Thumbs.db",
	"desktop.ini",

	// ── Editor / tool artifacts ───────────────────────────────────────────────
	"*.swp", // Vim swap files
	"*.swo",
	"*~",    // emacs/generic backup files
	"*.bak", // generic backup files (omni's settings.json.bak, others)
	"*.log",

	// ── Python ────────────────────────────────────────────────────────────────
	"__pycache__",
	"*.pyc",

	// ── Cache directories ─────────────────────────────────────────────────────
	".cache",
	"cache",
	"caches",
	"node_modules",

	// ── Runtime sockets ───────────────────────────────────────────────────────
	"*.sock",

	// ── SSH private keys ──────────────────────────────────────────────────────
	// Explicit private-key filenames only; .pub public keys are intentionally
	// allowed through so they can be tracked as a legitimate sync target.
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"id_ecdsa_sk",
	"id_ed25519_sk",
	"authorized_keys",

	// ── Certificate and encryption file formats ───────────────────────────────
	"*.pem",
	"*.key",
	"*.secret",
	"*.age",   // age-encrypted files
	"*.pgp",   // PGP-encrypted files
	"*.gpg",   // GPG-encrypted files
	"*.p12",   // PKCS#12 certificate bundles (contain private keys)
	"*.pfx",   // PFX certificate bundles (contain private keys)
	"*.token", // token files

	// ── Shell completion caches ───────────────────────────────────────────────
	".zcompdump*",    // zsh completion dump (machine-specific: hostname + version)
	"fish_variables", // fish universal variables (machine-specific paths)

	// ── macOS AppleDouble resource forks ─────────────────────────────────────
	"._*", // resource fork sidecar files created by macOS on foreign fs

	// ── Lock files (reproducible, machine-state) ──────────────────────────────
	"Brewfile.lock.json", // Homebrew Bundle lock (machine-specific formulae state)
	"lazy-lock.json",     // Neovim lazy.nvim plugin lock

	// ── Databases (always machine-state) ─────────────────────────────────────
	"*.sqlite",
	"*.sqlite3",

	// ── Credential file names ─────────────────────────────────────────────────
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

	// Agent-tool restrictive sync proposals are intentionally not listed here.
	// They are attached as per-entry allowlists so unrelated config trees named
	// projects, plugins, tasks, etc. remain trackable.
}

// DefaultIgnores returns a copy of the built-in ignore patterns.
func DefaultIgnores() []string {
	cp := make([]string, len(defaultIgnores))
	copy(cp, defaultIgnores)
	return cp
}

// ShouldIgnore reports whether basename is ignored by the pattern list.
// Uses filepath.Match for glob patterns. Later matches override earlier ones;
// patterns prefixed with "!" include a path after an earlier ignore.
//
// Deprecated: prefer ShouldIgnoreChecked which surfaces malformed patterns.
// This wrapper hides malformed-pattern errors so callers using DefaultIgnores()
// (which are pre-validated) can continue to work without error handling.
func ShouldIgnore(basename string, patterns []string) bool {
	matched, _ := ShouldIgnoreChecked(basename, patterns)
	return matched
}

// ShouldIgnorePath reports whether relPath is ignored by the pattern list.
// Patterns containing a path separator are matched against relPath;
// basename-only patterns keep their historical basename semantics. A leading
// "/" anchors a pattern to relPath from the entry root. A trailing "/" matches
// that directory and all descendants.
func ShouldIgnorePath(relPath, basename string, patterns []string) bool {
	matched, _ := ShouldIgnorePathChecked(relPath, basename, patterns)
	return matched
}

// ShouldIgnoreAnyPath reports whether any relative path candidate is ignored by
// the pattern list. It is useful when a caller walks a subtree and needs to
// evaluate patterns both relative to the subtree and to the original entry root.
func ShouldIgnoreAnyPath(relPaths []string, basename string, patterns []string) bool {
	matched, _ := ShouldIgnoreAnyPathChecked(relPaths, basename, patterns)
	return matched
}

// ShouldIgnoreChecked reports whether basename is ignored by the pattern list
// and returns an error if any pattern is syntactically invalid.
// The first bad pattern encountered is returned; matching stops at that point.
// Use this variant when evaluating user-supplied (per-entry) patterns so that
// typos are surfaced rather than silently causing files to be synced or skipped.
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
	if len(relPaths) == 0 {
		relPaths = []string{basename}
	}
	candidates := make([]string, 0, len(relPaths))
	seen := make(map[string]struct{}, len(relPaths))
	for _, relPath := range relPaths {
		rel := cleanIgnoreRelPath(relPath, basename)
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		candidates = append(candidates, rel)
	}
	if len(candidates) == 0 {
		candidates = append(candidates, basename)
	}
	ignored := false
	for _, p := range patterns {
		pattern, err := parseIgnorePattern(p)
		if err != nil {
			return false, err
		}
		for _, target := range ignorePatternTargets(pattern, candidates, basename) {
			matched, err := matchIgnorePattern(pattern, target)
			if err != nil {
				return false, invalidIgnorePatternError(p, err)
			}
			if matched {
				ignored = !pattern.include
				break
			}
		}
	}
	return ignored, nil
}

func cleanIgnoreRelPath(relPath, basename string) string {
	rel := filepath.ToSlash(filepath.Clean(relPath))
	if rel == "." {
		rel = basename
	}
	return rel
}

func ignorePatternTargets(pattern ignorePattern, relPaths []string, basename string) []string {
	if !pattern.pathScoped {
		return []string{basename}
	}
	return relPaths
}

func matchIgnorePattern(pattern ignorePattern, target string) (bool, error) {
	if !pattern.dirScoped {
		return filepath.Match(pattern.glob, target)
	}
	if target == pattern.glob || strings.HasPrefix(target, pattern.glob+"/") {
		return true, nil
	}
	return false, nil
}

// HasIncludedDescendant reports whether any include pattern names a path below
// relPath. Walkers use it to descend ignored directories that contain an
// explicitly included child.
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

// ValidateIgnorePattern returns an error if pattern is not a valid dots ignore
// glob. It understands omni's include ("!") and root anchor ("/") prefixes,
// plus trailing "/" directory patterns.
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

type ignorePattern struct {
	glob       string
	include    bool
	pathScoped bool
	dirScoped  bool
}

func parseIgnorePattern(raw string) (ignorePattern, error) {
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
	return ignorePattern{glob: pattern, include: include, pathScoped: pathScoped, dirScoped: dirScoped}, nil
}

func invalidIgnorePatternError(pattern string, err error) error {
	return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
}

// combinedIgnores merges defaultIgnores with per-entry ignores and validates
// the per-entry patterns. Returns an error if any per-entry pattern is invalid.
// defaultIgnores are assumed valid (enforced at compile time by unit tests).
func combinedIgnores(perEntry []string) ([]string, error) {
	// Validate per-entry patterns before merging so bad patterns are caught early.
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
