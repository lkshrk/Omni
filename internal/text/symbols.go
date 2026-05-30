package text

import (
	"io"
	"os"
	"strings"
)

type SymbolMode string

const (
	SymbolModeUnicode SymbolMode = "unicode"
	SymbolModeASCII   SymbolMode = "ascii"
)

type Symbols struct {
	mode SymbolMode
}

func SymbolsFromEnv() Symbols {
	mode := SymbolModeUnicode
	if os.Getenv("NO_EMOJI") != "" || os.Getenv("TERM") == "dumb" || !localeSupportsUnicode() {
		mode = SymbolModeASCII
	}
	return Symbols{mode: mode}
}

func localeSupportsUnicode() bool {
	locale := firstNonEmpty(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
	if locale == "" {
		return true
	}
	normalized := strings.ToUpper(strings.TrimSpace(locale))
	if normalized == "C" || normalized == "POSIX" {
		return false
	}
	return strings.Contains(normalized, "UTF-8") || strings.Contains(normalized, "UTF8")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s Symbols) Mode() SymbolMode {
	return s.mode
}

func (s Symbols) Apply(text string) string {
	if s.mode != SymbolModeASCII {
		return text
	}
	return asciiReplacer.Replace(text)
}

func (s Symbols) Writer(w io.Writer) io.Writer {
	if s.mode != SymbolModeASCII {
		return w
	}
	return symbolWriter{w: w, symbols: s}
}

type symbolWriter struct {
	w       io.Writer
	symbols Symbols
}

func (w symbolWriter) Write(p []byte) (int, error) {
	_, err := io.WriteString(w.w, w.symbols.Apply(string(p)))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

var asciiReplacer = strings.NewReplacer(
	"✓", "v",
	"✗", "x",
	"⚠", "!",
	"◷", "~",
	"⚿", "#",
	"↑", "^",
	"↓", "v",
	"×", "x",
	"–", "-",
	"—", "-",
	"…", ".",
	"→", ">",
	"←", "<",
	"•", "*",
	"·", ".",
	"↳", ">",
	"›", ">",
	"‣", ">",
	"▸", ">",
	"▹", ">",
	"▾", "v",
	"◇", "*",
	"🌐", "*",
	"🌍", "*",
	"🌎", "*",
	"🌏", "*",
	"─", "-",
	"│", "|",
	"┌", "+",
	"┐", "+",
	"└", "+",
	"┘", "+",
	"├", "+",
)
