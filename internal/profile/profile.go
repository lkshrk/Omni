package profile

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const EnvName = "OMNI_PROFILE"

var (
	mu         sync.Mutex
	testWriter io.Writer
)

func Enabled() bool {
	return enabledValue(os.Getenv(EnvName))
}

func Start(name string) func() {
	if !Enabled() {
		return func() {}
	}
	start := time.Now()
	return func() {
		Log(name, time.Since(start))
	}
}

func Log(name string, elapsed time.Duration) {
	if !Enabled() {
		return
	}
	writeLine(fmt.Sprintf("[omni profile] %s %s\n", name, formatDuration(elapsed)))
}

func enabledValue(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func formatDuration(d time.Duration) string {
	if d >= time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Microsecond).String()
}

func writeLine(line string) {
	target := strings.TrimSpace(os.Getenv(EnvName))
	if !enabledValue(target) {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if testWriter != nil {
		_, _ = io.WriteString(testWriter, line)
		return
	}

	switch strings.ToLower(target) {
	case "1", "true", "yes", "on", "stderr":
		_, _ = io.WriteString(os.Stderr, line)
	case "stdout":
		_, _ = io.WriteString(os.Stdout, line)
	default:
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "[omni profile] profile.write_error %v\n", err)
			return
		}
		defer func() {
			_ = f.Close()
		}()
		_, _ = io.WriteString(f, line)
	}
}

func setWriterForTest(w io.Writer) func() {
	mu.Lock()
	previous := testWriter
	testWriter = w
	mu.Unlock()
	return func() {
		mu.Lock()
		testWriter = previous
		mu.Unlock()
	}
}
