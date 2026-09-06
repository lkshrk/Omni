package app

import (
	"context"
	"fmt"
	"strings"
)

// nativeRemoveCommand maps one artifact to the client command that removes it. Removal is by
// identity through the client's own CLI, so no path is ever derived from package-supplied names.
func nativeRemoveCommand(target, kind, identity string) ([]string, error) {
	if strings.TrimSpace(identity) == "" {
		return nil, fmt.Errorf("identity is required")
	}
	if strings.HasPrefix(identity, "-") {
		return nil, fmt.Errorf("identity %q starts with a dash; refusing to pass it as a flag", identity)
	}
	switch target + "/" + kind {
	case "claude/plugin":
		return []string{"claude", "plugin", "uninstall", identity}, nil
	case "claude/mcp":
		return []string{"claude", "mcp", "remove", "-s", "user", identity}, nil
	case "claude/marketplace":
		return []string{"claude", "plugin", "marketplace", "remove", identity}, nil
	case "codex/plugin":
		return []string{"codex", "plugin", "remove", identity}, nil
	case "codex/mcp":
		return []string{"codex", "mcp", "remove", identity}, nil
	case "codex/marketplace":
		return []string{"codex", "plugin", "marketplace", "remove", identity}, nil
	}
	return nil, fmt.Errorf("no removal command for %s %s", target, kind)
}

// AgentsNativeRemove uninstalls one native artifact through its client. An artifact covered by an
// ignore entry is refused: the entry exists to say this one stays.
func (a *App) AgentsNativeRemove(ctx context.Context, row AgentsNativeRow) error {
	if row.Ignored {
		return fmt.Errorf("%s %s %s is ignored; unignore it before removing", row.Target, row.Kind, row.Identity)
	}
	argv, err := nativeRemoveCommand(row.Target, row.Kind, row.Identity)
	if err != nil {
		return err
	}
	if _, stderr, err := a.fallbackExecutor().Run(ctx, argv[0], argv[1:]...); err != nil {
		if msg := strings.TrimSpace(stderr); msg != "" {
			return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, msg)
		}
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}
