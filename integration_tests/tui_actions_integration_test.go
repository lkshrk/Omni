//go:build integration

package integration_test

import (
	"os"
	"testing"
)

func writeFakeBulkUpgradeBrew(t *testing.T, path string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${OMNI_TEST_BREW_LOG:?}"
state="${OMNI_TEST_BREW_STATE:?}"
version() { cat "$state/$1"; }
case "$*" in
	"--version") echo "Homebrew 4.0.0" ;;
	"leaves --installed-on-request") printf 'omni-old-one\nomni-old-two\n' ;;
	"list --versions --formula") printf 'omni-old-one %s\nomni-old-two %s\n' "$(version omni-old-one)" "$(version omni-old-two)" ;;
	"list --cask") ;;
	"info --json=v2 --installed") printf '{"formulae":[{"name":"omni-old-one","full_name":"omni-old-one","installed":[{"version":"%s","installed_on_request":true}]},{"name":"omni-old-two","full_name":"omni-old-two","installed":[{"version":"%s","installed_on_request":true}]}],"casks":[]}\n' "$(version omni-old-one)" "$(version omni-old-two)" ;;
	"outdated --json=v2 --greedy")
		printf '{"formulae":['
		sep=""
		for name in omni-old-one omni-old-two; do
			if [ "$(version "$name")" != "2.0.0" ]; then
				printf '%s{"name":"%s","installed_versions":["1.0.0"],"current_version":"2.0.0","pinned":false}' "$sep" "$name"
				sep=,
			fi
		done
		printf '],"casks":[]}\n'
		;;
	"update --quiet") ;;
	"upgrade --formula omni-old-one") printf '2.0.0\n' > "$state/omni-old-one" ;;
	"upgrade --formula omni-old-two") printf '2.0.0\n' > "$state/omni-old-two" ;;
	info\ --json=v2*) echo '{"formulae":[],"casks":[]}' ;;
	list\ --versions\ omni-old-*) echo "$3 $(version "$3")" ;;
	"list --versions --cask omni-old-one"|"list --versions --cask omni-old-two") exit 1 ;;
	"tap") ;;
	*) echo "unexpected fake brew command: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
}
