//go:build integration

// Canary against the REAL skills CLI, part of the normal integration suite.
// omni invokes the CLI via npx unpinned, so upstream releases change behavior
// under us: the failure markers in internal/app.skillsCLIFailureMarkers were
// verified against vercel-labs/skills source (2026-07), and this test detects
// wording drift in whatever version npx currently resolves. Skips (rather
// than fails) when npx or the npm registry is unavailable — absence of a
// Node toolchain or network is an environment gap, not marker drift.
package integration_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
)

// TestSkillsCLICanary_FailureOutputStillMatchesMarkers drives the current
// published skills CLI into a failure (nonexistent repo) and asserts the
// marker detection in internal/app still recognizes its output. If this
// fails, upstream reworded its failure text — update
// skillsCLIFailureMarkers and the txtar fixtures' fake output together.
func TestSkillsCLICanary_FailureOutputStillMatchesMarkers(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH; canary needs a real Node toolchain")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Isolated HOME so the real CLI cannot touch the developer's agent
	// configs or global lockfile; bogus repo forces the failure path.
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "add",
		"omni-canary-nonexistent-owner/omni-canary-nonexistent-repo", "-g", "-y")
	cmd.Env = append(cmd.Environ(),
		"HOME="+t.TempDir(),
		"XDG_STATE_HOME="+t.TempDir(),
		"CI=1",
	)
	out, err := cmd.CombinedOutput()
	output := string(out)

	marked := app.SkillsCLIOutputIndicatesFailure(output, "")

	// Generic markers ("Failed to") could occur in pre-CLI noise (npm/npx
	// breakage), so they do not prove the skills CLI ran. Require one of the
	// CLI's own add-failure signatures (src/add.ts: the "Installation
	// failed" outro and the GitCloneError header) before asserting anything;
	// the bogus repo legitimately exits 1 through exactly that path.
	cliRan := strings.Contains(output, "Installation failed") ||
		strings.Contains(output, "Failed to clone repository")

	switch {
	case err == nil && !marked:
		// The one dangerous contract: failure reported as exit 0 with no
		// known marker — omni would treat this install as a success.
		t.Fatalf("real skills CLI exited 0 for a nonexistent repo AND printed no known failure marker (%q) — omni would report success; update skillsCLIFailureMarkers.\noutput:\n%s",
			[]string{"Failed to", "✗", "✘"}, output)
	case !cliRan:
		// npx exited non-zero without any skills-CLI signature: a pre-CLI
		// environment failure (registry outage, node breakage). Nothing
		// about the CLI was observed — skip rather than pass vacuously.
		t.Skipf("skills CLI run not confirmed (no CLI output signature); cannot assert markers.\noutput:\n%s", output)
	case !marked:
		// The CLI demonstrably ran and failed, but no marker matched its
		// output — upstream reworded failure text. Production still catches
		// this run via the non-zero exit, but the exit-0 partial-failure
		// paths rely on these markers, so drift must be fixed, not logged.
		t.Fatalf("skills CLI failure output no longer matches any marker (%q) — update skillsCLIFailureMarkers and the txtar fakes.\noutput:\n%s",
			[]string{"Failed to", "✗", "✘"}, output)
	}
}
