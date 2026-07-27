package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

func (a *App) ImportConfigFile(sourcePath string) error {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return fmt.Errorf("settings import path is required")
	}
	sourcePath = expandHomePath(sourcePath)
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve settings import path: %w", err)
	}
	info, err := os.Stat(absSource)
	if err != nil {
		return fmt.Errorf("settings import %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("settings import %q is a directory", sourcePath)
	}
	if a.ConfigPath != "" {
		if absTarget, err := filepath.Abs(a.ConfigPath); err == nil && absTarget == absSource {
			return fmt.Errorf("settings import source is already the active config")
		}
	}
	cfg, err := config.Load(absSource)
	if err != nil {
		return fmt.Errorf("load settings import: %w", err)
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if err := config.Save(a.ConfigPath, cfg); err != nil {
		return fmt.Errorf("save imported settings: %w", err)
	}
	a.initProviderRegistry(a.effectiveSettings(cfg))
	return nil
}

func expandHomePath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
