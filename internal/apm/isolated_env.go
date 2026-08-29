package apm

import (
	"fmt"
	"os"
	"path/filepath"
)

// IsolatedEnv runs apm against a throwaway HOME so a probe cannot read, create, or migrate real workspace state.
func IsolatedEnv(prefix string) (env []string, cleanup func(), err error) {
	home, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, nil, fmt.Errorf("create isolated APM home: %w", err)
	}
	return []string{
		"APM_E2E_TESTS=1",
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".state"),
	}, func() { _ = os.RemoveAll(home) }, nil
}
