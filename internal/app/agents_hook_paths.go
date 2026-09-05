package app

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

// Only hook command values are shell-expanded by Claude Code; env or permission paths must keep their literal form.
var hookCommandValue = regexp.MustCompile(`"command"\s*:\s*"(?:[^"\\]|\\.)*"`)

// apm rewrites user-scope hook commands to absolute paths (microsoft/apm#1394); both files are kept in sync so apm still adopts them.
var portableHookFiles = []string{
	filepath.Join(".claude", "settings.json"),
	filepath.Join(".claude", "apm-hooks.json"),
}

func apmMutatesHooks(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "install", "update", "uninstall", "prune":
		return true
	}
	return false
}

// portableHookPaths anchors apm's hook script paths to $HOME so the dotfiles-tracked settings file is identical on every host.
func portableHookPaths(home string) ([]string, error) {
	if runtime.GOOS == "windows" || home == "" {
		return nil, nil
	}
	absolute := []byte(filepath.ToSlash(filepath.Clean(home)) + "/.claude/hooks/")
	portable := []byte("$HOME/.claude/hooks/")
	var changed []string
	for _, rel := range portableHookFiles {
		path := filepath.Join(home, rel)
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return changed, err
		}
		out := hookCommandValue.ReplaceAllFunc(raw, func(match []byte) []byte {
			return bytes.ReplaceAll(match, absolute, portable)
		})
		if bytes.Equal(out, raw) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return changed, err
		}
		// Written in place: settings.json is usually a dotfiles symlink and a rename would replace the link.
		if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
			return changed, err
		}
		changed = append(changed, path)
	}
	return changed, nil
}
