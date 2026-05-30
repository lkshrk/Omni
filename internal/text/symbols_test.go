package text

import (
	"bytes"
	"testing"
)

func TestSymbolsFromEnvDefaultsToUnicode(t *testing.T) {
	t.Setenv("NO_EMOJI", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")

	symbols := SymbolsFromEnv()
	if symbols.Mode() != SymbolModeUnicode {
		t.Fatalf("Mode() = %q, want %q", symbols.Mode(), SymbolModeUnicode)
	}
	if got := symbols.Apply("✓ synced → ~/.zshrc…"); got != "✓ synced → ~/.zshrc…" {
		t.Fatalf("Apply() = %q", got)
	}
}

func TestSymbolsFromEnvNoEmojiRewritesDisplayGlyphs(t *testing.T) {
	t.Setenv("NO_EMOJI", "1")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")

	got := SymbolsFromEnv().Apply("✓ synced → ~/.zshrc… ⚠ ◷ ⚿ ┌─┐")
	want := "v synced > ~/.zshrc. ! ~ # +-+"
	if got != want {
		t.Fatalf("Apply() = %q, want %q", got, want)
	}
}

func TestSymbolsFromEnvHonorsNoEmoji(t *testing.T) {
	t.Setenv("NO_EMOJI", "1")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")

	if got := SymbolsFromEnv().Apply("🌍 checking…"); got != "* checking." {
		t.Fatalf("Apply() = %q", got)
	}
}

func TestSymbolsFromEnvFallsBackForNonUTF8Locale(t *testing.T) {
	t.Setenv("NO_EMOJI", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "C")

	if got := SymbolsFromEnv().Apply("✓ checking…"); got != "v checking." {
		t.Fatalf("Apply() = %q", got)
	}
}

func TestSymbolWriterReportsOriginalByteCount(t *testing.T) {
	var buf bytes.Buffer
	n, err := Symbols{mode: SymbolModeASCII}.Writer(&buf).Write([]byte("✓ ok"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("✓ ok") {
		t.Fatalf("Write count = %d, want %d", n, len("✓ ok"))
	}
	if got := buf.String(); got != "v ok" {
		t.Fatalf("written = %q", got)
	}
}
