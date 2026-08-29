package executor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Several apm paths block on getpass, so a subprocess must never inherit the caller's stdin.
func TestRealExecutorGivesSubprocessesTheNullDevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	fed := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(fed, []byte("secret-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(fed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = saved })

	r := &RealExecutor{}
	for name, run := range map[string]func() (string, string, error){
		"Run":    func() (string, string, error) { return r.Run(context.Background(), "sh", "-c", "cat") },
		"RunEnv": func() (string, string, error) { return r.RunEnv(context.Background(), nil, "sh", "-c", "cat") },
		"RunDirEnv": func() (string, string, error) {
			return r.RunDirEnv(context.Background(), t.TempDir(), nil, "sh", "-c", "cat")
		},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, _, err := run()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Fatalf("subprocess inherited the caller's stdin: %q", stdout)
			}
		})
	}
}
