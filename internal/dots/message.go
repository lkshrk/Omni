package dots

import (
	"fmt"
	"strings"
)

// CommitMessageFromStatus generates a short commit message from the output of
// "git status --short". It extracts unique top-level path segments (e.g.
// "nvim", "zshrc") and formats them as "dots: update nvim, zshrc" or
// "dots: update nvim, zshrc + N more".
func CommitMessageFromStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return "dots: update"
	}
	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[2:])
		// Renames: "old path -> new path"
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		// Take only the first path segment.
		seg := strings.SplitN(path, "/", 2)[0]
		seg = strings.TrimPrefix(seg, ".")
		if seg == "" || seen[seg] {
			continue
		}
		seen[seg] = true
		names = append(names, seg)
	}
	if len(names) == 0 {
		return "dots: update"
	}
	const maxShow = 3
	if len(names) <= maxShow {
		return "dots: update " + strings.Join(names, ", ")
	}
	shown := names[:maxShow]
	rest := len(names) - maxShow
	return fmt.Sprintf("dots: update %s + %d more", strings.Join(shown, ", "), rest)
}
