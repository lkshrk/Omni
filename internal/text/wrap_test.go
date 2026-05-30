package text

import (
	"strings"
	"testing"
)

func TestWrapText_ShortLineFitsOnOne(t *testing.T) {
	out := WrapText("hello world", 80)
	if len(out) != 1 || out[0] != "hello world" {
		t.Errorf("WrapText = %v, want [hello world]", out)
	}
}

func TestWrapText_LongLineWraps(t *testing.T) {
	out := WrapText("one two three four five six seven eight nine ten", 20)
	if len(out) < 2 {
		t.Fatalf("WrapText should produce multiple lines, got %v", out)
	}
	for _, line := range out {
		if len([]rune(line)) > 20 {
			t.Errorf("line %q exceeds wrap width 20", line)
		}
	}
	// Reassembled text must match original words.
	got := strings.Join(out, " ")
	want := "one two three four five six seven eight nine ten"
	if got != want {
		t.Errorf("WrapText reassembled = %q, want %q", got, want)
	}
}

func TestWrapText_ExactWidth(t *testing.T) {
	// "hello" is exactly 5 runes — fits in width 5.
	out := WrapText("hello", 5)
	if len(out) != 1 || out[0] != "hello" {
		t.Errorf("WrapText exact width = %v, want [hello]", out)
	}
}

func TestWrapText_Empty_ReturnsNil(t *testing.T) {
	if out := WrapText("", 80); out != nil {
		t.Errorf("WrapText(\"\") = %v, want nil", out)
	}
}

func TestWrapText_WhitespaceOnly_ReturnsNil(t *testing.T) {
	if out := WrapText("   \t  ", 80); out != nil {
		t.Errorf("WrapText(whitespace) = %v, want nil", out)
	}
}

func TestWrapText_ZeroWidth_ReturnsNil(t *testing.T) {
	if out := WrapText("hello", 0); out != nil {
		t.Errorf("WrapText(width=0) = %v, want nil", out)
	}
}

func TestWrapText_NegativeWidth_ReturnsNil(t *testing.T) {
	if out := WrapText("hello", -1); out != nil {
		t.Errorf("WrapText(width=-1) = %v, want nil", out)
	}
}

func TestWrapText_MultibyteRunes(t *testing.T) {
	// Each Japanese character is 1 rune; "日本語 テスト" = 3 + 1(space) + 3 = 7 runes.
	// Width 4 → should wrap between the two words.
	out := WrapText("日本語 テスト", 4)
	if len(out) != 2 {
		t.Fatalf("WrapText multibyte = %v, want 2 lines", out)
	}
	if out[0] != "日本語" {
		t.Errorf("WrapText multibyte line 0 = %q, want %q", out[0], "日本語")
	}
	if out[1] != "テスト" {
		t.Errorf("WrapText multibyte line 1 = %q, want %q", out[1], "テスト")
	}
}

func TestWrapText_SingleLongWord_NoBreak(t *testing.T) {
	// A single word longer than width — cannot break, so it stays on one line.
	out := WrapText("superlongword", 5)
	if len(out) != 1 || out[0] != "superlongword" {
		t.Errorf("WrapText (long word) = %v, want [superlongword]", out)
	}
}

func TestPluralCount(t *testing.T) {
	if got := PluralCount(1, "tool", "tools"); got != "1 tool" {
		t.Fatalf("PluralCount singular = %q, want 1 tool", got)
	}
	if got := PluralCount(2, "tool", "tools"); got != "2 tools" {
		t.Fatalf("PluralCount plural = %q, want 2 tools", got)
	}
	if got := PluralCount(0, "tool", "tools"); got != "0 tools" {
		t.Fatalf("PluralCount zero = %q, want 0 tools", got)
	}
}
