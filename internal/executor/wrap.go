package executor

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// WrapError attaches captured output to a failed command's error; many CLIs report failures on stdout with an empty stderr.
func WrapError(err error, label, stdout, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if detail == "" {
		return fmt.Errorf("%s: %w", label, err)
	}
	return fmt.Errorf("%s: %w: %s", label, err, tailDetail(detail))
}

func tailDetail(detail string) string {
	const limit = 2048
	if len(detail) <= limit {
		return detail
	}
	detail = detail[len(detail)-limit:]
	for len(detail) > 0 && !utf8.RuneStart(detail[0]) {
		detail = detail[1:]
	}
	if i := strings.IndexByte(detail, '\n'); i >= 0 && i+1 < len(detail) {
		detail = detail[i+1:]
	}
	return "… " + detail
}
