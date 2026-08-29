package app

import (
	"strings"
	"testing"
)

const shadowedInstallOutput = `[>] Installing 14 package(s)...
  [!] 24 files skipped -- local files exist, not managed by APM
    Use 'apm install --force' to overwrite
  [!] [code-simplifier] Rejected agent target path: Cannot verify containment of
'/home/coder/.claude/agents/code-simplifier.md' within
'/home/coder/.claude/agents': Path
'/home/coder/.claude/agents/code-simplifier.md' resolves to
'/home/coder/dotfiles/dotfiles/claude/.claude/agents/code-simplifier.md' which
is outside the allowed base directory '/home/coder/.claude/agents'
  [!] [codex] Rejected command target path: Cannot verify containment of
'/home/coder/.claude/commands/adversarial-review.md' within
'/home/coder/.claude/commands': Path
'/home/coder/.claude/commands/adversarial-review.md' resolves to
'/home/coder/dotfiles/dotfiles/claude/.claude/commands/adversarial-review.md'
which is outside the allowed base directory '/home/coder/.claude/commands'
  [!] 12 dependencies unpinned: anthropics/claude-plugins-official
[+] Installed 14 package(s)
`

func TestCollapseShadowWarnings(t *testing.T) {
	body, shadowed := collapseShadowWarnings(shadowedInstallOutput)
	got := body + shadowedFilesNote(shadowed) + "\n"

	if strings.Contains(got, "Rejected") || strings.Contains(got, "allowed base directory") {
		t.Fatalf("shadow warning survived:\n%s", got)
	}
	if !strings.HasSuffix(got, "note: 2 package file(s) shadowed by user-managed files\n") {
		t.Fatalf("missing summary:\n%s", got)
	}
	for _, want := range []string{
		"[>] Installing 14 package(s)...",
		"  [!] 24 files skipped -- local files exist, not managed by APM",
		"    Use 'apm install --force' to overwrite",
		"  [!] 12 dependencies unpinned: anthropics/claude-plugins-official",
		"[+] Installed 14 package(s)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unrelated line %q dropped:\n%s", want, got)
		}
	}
}

func TestCollapseShadowWarningsLeavesCleanOutputAlone(t *testing.T) {
	clean := "[>] Installing 1 package(s)...\n[+] Installed 1 package(s)\n"
	if got, shadowed := collapseShadowWarnings(clean); got != clean || shadowed != 0 {
		t.Fatalf("clean output rewritten:\n%q", got)
	}
}

func TestCollapseShadowWarningsStopsAtTheNextMarker(t *testing.T) {
	truncated := "  [!] [pkg] Rejected agent target path: Cannot verify containment of\n'/home/x' within\n[+] Installed 1 package(s)\n"
	body, shadowed := collapseShadowWarnings(truncated)
	got := body + shadowedFilesNote(shadowed) + "\n"
	if !strings.Contains(got, "[+] Installed 1 package(s)") {
		t.Fatalf("collapse ate the following marker line:\n%s", got)
	}
	if strings.Contains(got, "Rejected") || strings.Contains(got, "'/home/x' within") {
		t.Fatalf("unterminated warning survived:\n%s", got)
	}
}
