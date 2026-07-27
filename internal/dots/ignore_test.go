package dots_test

import (
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestShouldIgnore_DefaultPatterns(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{".git", true},
		{".DS_Store", true},
		{".Spotlight-V100", true},
		{".fseventsd", true},
		{".localized", true},
		{"Thumbs.db", true},
		{"desktop.ini", true},

		{"init.lua.swp", true},
		{"foo.swo", true},
		{"zshrc~", true},
		{"debug.log", true},
		{"omni.log", true},
		{"settings.json.bak", true},
		{"foo.bak", true},

		{"__pycache__", true},
		{"module.pyc", true},

		{".cache", true},
		{"cache", true},
		{"caches", true},
		{"node_modules", true},

		{"agent.sock", true},

		// SSH private keys — blocked by exact name
		{"id_rsa", true},
		{"id_ed25519", true},
		{"id_ecdsa", true},
		{"id_dsa", true},
		{"id_ecdsa_sk", true},   // FIDO2 resident key
		{"id_ed25519_sk", true}, // FIDO2 resident key
		{"authorized_keys", true},
		// .pub public keys — allowed through (not sensitive)
		{"id_rsa.pub", false},
		{"id_ed25519.pub", false},
		{"id_ecdsa.pub", false},
		{"id_dsa.pub", false},

		{"cert.pem", true},
		{"server.key", true},
		{"file.secret", true},
		{"backup.age", true},
		{"data.pgp", true},
		{"data.gpg", true},
		{"keystore.p12", true},
		{"bundle.pfx", true},
		{"github.token", true},

		{"credentials", true},
		{"credentials.json", true},
		{"auth.json", true},

		{".skill-lock.json", false},
		{"init.lua", false},
		{"config.toml", false},
		{".zshrc", false},
		{"settings.json", false},
		{"CLAUDE.md", false},
		{"keybindings.json", false},
		{"plugins.json", false}, // plugin manifest — worth tracking
		{"plugins", false},      // NOT globally ignored — collides with nvim/lua/plugins/
		{"projects", false},     // Claude-specific runtime dir; only ignored on the ~/.claude entry.
		{"history.jsonl", false},
		{"starship.toml", false},
		{"gitconfig", false},
	}
	for _, tc := range cases {
		got := dots.ShouldIgnore(tc.name, dots.DefaultIgnores())
		if got != tc.want {
			t.Errorf("ShouldIgnore(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldIgnore_CustomPattern(t *testing.T) {
	patterns := []string{"*.zwc", "local.zsh"}
	if !dots.ShouldIgnore("history.zwc", patterns) {
		t.Error("expected *.zwc to match history.zwc")
	}
	if !dots.ShouldIgnore("local.zsh", patterns) {
		t.Error("expected local.zsh to match")
	}
	if dots.ShouldIgnore("zshrc", patterns) {
		t.Error("zshrc should not match *.zwc or local.zsh")
	}
}

func TestShouldIgnorePath_RelativePatterns(t *testing.T) {
	patterns := []string{"workspaces/work/auth.json", "node_modules", "cache", "*.log"}
	if !dots.ShouldIgnorePath("workspaces/work/auth.json", "auth.json", patterns) {
		t.Error("expected relative path pattern to match nested auth.json")
	}
	if !dots.ShouldIgnorePath("workspaces/work/node_modules", "node_modules", patterns) {
		t.Error("expected basename pattern to match nested node_modules")
	}
	if !dots.ShouldIgnorePath("workspaces/work/cache", "cache", patterns) {
		t.Error("expected basename pattern to match nested cache dir")
	}
	if !dots.ShouldIgnorePath("workspaces/work/debug.log", "debug.log", patterns) {
		t.Error("expected basename pattern to match nested .log file")
	}
	if dots.ShouldIgnorePath("workspaces/home/auth.json", "auth.json", patterns) {
		t.Error("relative path pattern should not match other directories")
	}
}

func TestShouldIgnoreAnyPath_DirectoryIgnoreAppliesToDescendants(t *testing.T) {
	for _, patterns := range [][]string{
		{".claude/projects", "!/.claude/projects/session.json"},
		{"projects", "!/projects/session.json"},
	} {
		if dots.ShouldIgnoreAnyPath(
			[]string{"projects/session.json", ".claude/projects/session.json"},
			"session.json",
			patterns,
		) {
			t.Fatalf("explicitly included descendant should not be ignored with %v", patterns)
		}
		if !dots.ShouldIgnoreAnyPath(
			[]string{"projects/private.json", ".claude/projects/private.json"},
			"private.json",
			patterns,
		) {
			t.Fatalf("sibling below ignored directory should stay ignored with %v", patterns)
		}
	}
}

func TestShouldIgnoreAnyPath_BasenameAncestorDoesNotEscapeLogicalRoot(t *testing.T) {
	if dots.ShouldIgnoreAnyPath(
		[]string{"data.db", "cache/data.db"},
		"data.db",
		[]string{"cache"},
	) {
		t.Fatal("synthetic rooted candidate must not re-ignore content copied from an explicitly selected cache directory")
	}
}

func TestShouldIgnoreAnyPathChecked_RejectsPathsOutsideLogicalRoot(t *testing.T) {
	for _, rel := range []string{"/tmp/session.json", "../session.json", "projects/../../session.json"} {
		if _, err := dots.ShouldIgnoreAnyPathChecked([]string{rel}, filepath.Base(rel), []string{"projects"}); err == nil {
			t.Errorf("ShouldIgnoreAnyPathChecked(%q) error = nil, want logical-root error", rel)
		}
	}
}

func TestShouldIgnorePath_IncludesOverrideEarlierIgnores(t *testing.T) {
	patterns := []string{
		"*",
		"!/settings.json",
		"!/CLAUDE.md",
		"!/mcp.json",
		"!/plugins",
		"!/plugins/installed_plugins.json",
		"!/.omc-config.json",
		"!/keybindings.json",
		"!/statusline-command.sh",
		"!/rules/",
		"!/skills/",
	}

	for _, rel := range []string{
		"settings.json",
		"CLAUDE.md",
		"mcp.json",
		"plugins",
		"plugins/installed_plugins.json",
		".omc-config.json",
		"keybindings.json",
		"statusline-command.sh",
		"rules",
		"rules/global.md",
		"rules/nested/rule.md",
		"skills",
		"skills/example/SKILL.md",
	} {
		if dots.ShouldIgnorePath(rel, filepath.Base(rel), patterns) {
			t.Errorf("%s should be included by a later ! pattern", rel)
		}
	}
	for _, rel := range []string{
		"projects/session.json",
		"history.jsonl",
		"plugins/cache.json",
		"nested/settings.json",
		"nested/rules/global.md",
	} {
		if !dots.ShouldIgnorePath(rel, filepath.Base(rel), patterns) {
			t.Errorf("%s should stay ignored", rel)
		}
	}
}

func TestShouldIgnore_NoPatterns(t *testing.T) {
	if dots.ShouldIgnore("anything", nil) {
		t.Error("expected false with no patterns")
	}
}

func TestShouldIgnoreChecked_BadPattern_ReturnsError(t *testing.T) {
	// "[secret*" is syntactically invalid: unclosed character class.
	matched, err := dots.ShouldIgnoreChecked("secret_key", []string{"[secret*"})
	if err == nil {
		t.Error("ShouldIgnoreChecked([secret*): expected an error for bad pattern, got nil")
	}
	if matched {
		t.Error("ShouldIgnoreChecked([secret*): expected matched=false on error")
	}
}

func TestShouldIgnoreChecked_ValidPattern_NoError(t *testing.T) {
	matched, err := dots.ShouldIgnoreChecked("server.key", []string{"*.key"})
	if err != nil {
		t.Errorf("ShouldIgnoreChecked(*.key): unexpected error: %v", err)
	}
	if !matched {
		t.Error("ShouldIgnoreChecked(*.key): expected server.key to match *.key")
	}
}

func TestShouldIgnoreChecked_BadPatternSkipsGoodPatterns(t *testing.T) {
	matched, err := dots.ShouldIgnoreChecked("foo", []string{"[bad", "foo"})
	if err == nil {
		t.Error("expected error for bad pattern [bad")
	}
	if matched {
		t.Error("expected matched=false when error occurs before match")
	}
}
