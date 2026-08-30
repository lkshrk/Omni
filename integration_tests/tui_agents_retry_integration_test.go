//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vttest"
)

func TestTUIAgentsRefreshRunsAnotherOutdatedCheck(t *testing.T) {
	sandbox, markers := agentsRetrySandbox(t)
	before := 0
	runAgentsActionsTUI(t, buildOmniBinary(t), sandbox, func(term *vttest.Terminal) {
		before = agentsRetryCount(markers.refreshCount)
		sendAgentsActionsKeyUntil(t, term, "R", func(string) bool {
			return agentsRetryCount(markers.refreshCount) > before
		}, "TUI refresh did not run another outdated check")
	}, func(*paritySandbox) bool {
		return agentsRetryCount(markers.refreshCount) > before
	})
	assertFileContains(t, markers.log, "|outdated -g --parallel-checks 4")
}

type agentsRetryMarkers struct {
	log          string
	updated      string
	refreshCount string
}

func agentsRetrySandbox(t *testing.T) (*paritySandbox, agentsRetryMarkers) {
	t.Helper()
	sandbox := newParitySandbox(t, t.TempDir())
	seedAgentsActionsParity(t, sandbox)
	markers := agentsRetryMarkers{
		log:     filepath.Join(sandbox.root, "apm.log"),
		updated: filepath.Join(sandbox.root, "apm-state", "updated-selected"), refreshCount: filepath.Join(sandbox.root, "apm-state", "refresh-count"),
	}
	writeExecutable(t, filepath.Join(sandbox.root, "bin", "apm"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo 'Agent Package Manager (APM) CLI version 0.29.0'
  exit 0
fi
printf '%s|%s\n' "$PWD" "$*" >> "${OMNI_TEST_APM_LOG:?}"
case "$*" in
  'outdated -g --parallel-checks 4')
    count=0; [ ! -f "${OMNI_TEST_APM_REFRESH_COUNT:?}" ] || count=$(cat "$OMNI_TEST_APM_REFRESH_COUNT")
    echo $((count + 1)) > "$OMNI_TEST_APM_REFRESH_COUNT"
    echo '[✓] All dependencies are up-to-date'
    ;;
  'update -g --yes https://github.com/acme/tool') touch "${OMNI_TEST_APM_UPDATED:?}"; echo '[✓] updated selected package' ;;
  *) echo "delegated: $*" ;;
esac
`)
	sandbox.env = append(sandbox.env,
		"OMNI_TEST_APM_UPDATED="+markers.updated,
		"OMNI_TEST_APM_REFRESH_COUNT="+markers.refreshCount,
	)
	return sandbox, markers
}

func agentsRetryCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return count
}
