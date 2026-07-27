package executor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ResolvedUnderNvm(absPath string) bool {
	if absPath == "" || runtime.GOOS == "windows" {
		return false
	}
	absPath = filepath.Clean(absPath)
	for _, dir := range nvmBinDirs() {
		dir = filepath.Clean(dir)
		if dir == "" {
			continue
		}
		if absPath == dir || strings.HasPrefix(absPath, dir+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func nvmBinDirs() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var dirs []string
	add := func(path string) {
		if path == "" || !filepath.IsAbs(path) || !isDir(path) {
			return
		}
		for _, existing := range dirs {
			if existing == path {
				return
			}
		}
		dirs = append(dirs, path)
	}
	add(os.Getenv("NVM_BIN"))
	add(nvmDefaultBinDir(home))
	nvmDir := os.Getenv("NVM_DIR")
	if nvmDir == "" {
		nvmDir = filepath.Join(home, ".nvm")
	}
	versionsDir := filepath.Join(nvmDir, "versions", "node")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return dirs
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		add(filepath.Join(versionsDir, e.Name(), "bin"))
	}
	return dirs
}
