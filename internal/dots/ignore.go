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
	"*~",   // emacs/generic backup files
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

	// Claude Code runtime data is intentionally not listed here. Those names
	// are attached as per-entry ignores for the ~/.claude dot entry so unrelated
	// config trees named projects, plugins, tasks, etc. remain trackable.
}

// DefaultIgnores returns a copy of the built-in ignore patterns.
func DefaultIgnores() []string {
	cp := make([]string, len(defaultIgnores))
	copy(cp, defaultIgnores)
	return cp
}

// ShouldIgnore reports whether basename matches any pattern in the list.
// Uses filepath.Match for glob patterns; an exact string match is also accepted.
//
// Deprecated: prefer ShouldIgnoreChecked which surfaces malformed patterns.
// This wrapper silently skips bad patterns so callers using DefaultIgnores()
// (which are pre-validated) continue to work without error handling.
func ShouldIgnore(basename string, patterns []string) bool {
	matched, _ := ShouldIgnoreChecked(basename, patterns)
	return matched
}

// ShouldIgnorePath reports whether either relPath or basename matches any
// pattern. Patterns containing a path separator are matched against relPath;
// basename-only patterns keep their historical basename semantics.
func ShouldIgnorePath(relPath, basename string, patterns []string) bool {
	matched, _ := ShouldIgnorePathChecked(relPath, basename, patterns)
	return matched
}

// ShouldIgnoreChecked reports whether basename matches any pattern in the list
// and returns an error if any pattern is syntactically invalid.
// The first bad pattern encountered is returned; matching stops at that point.
// Use this variant when evaluating user-supplied (per-entry) patterns so that
// typos are surfaced rather than silently causing files to be synced or skipped.
func ShouldIgnoreChecked(basename string, patterns []string) (bool, error) {
	for _, p := range patterns {
		matched, err := filepath.Match(p, basename)
		if err != nil {
			return false, fmt.Errorf("invalid glob pattern %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func ShouldIgnorePathChecked(relPath, basename string, patterns []string) (bool, error) {
	rel := filepath.ToSlash(filepath.Clean(relPath))
	if rel == "." {
		rel = basename
	}
	for _, p := range patterns {
		pattern := filepath.ToSlash(p)
		target := basename
		if strings.Contains(pattern, "/") {
			target = rel
		}
		matched, err := filepath.Match(pattern, target)
		if err != nil {
			return false, fmt.Errorf("invalid glob pattern %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// combinedIgnores merges defaultIgnores with per-entry ignores and validates
// the per-entry patterns. Returns an error if any per-entry pattern is invalid.
// defaultIgnores are assumed valid (enforced at compile time by unit tests).
func combinedIgnores(perEntry []string) ([]string, error) {
	// Validate per-entry patterns before merging so bad patterns are caught early.
	var badPatterns []string
	for _, p := range perEntry {
		if _, err := filepath.Match(p, ""); err != nil {
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
