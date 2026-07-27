//go:build canary

package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The schema identifier's own URL does not resolve, so upstream's source file is the only live artifact carrying the string.
const upstreamWellKnownSource = "https://raw.githubusercontent.com/vercel-labs/skills/main/src/providers/wellknown.ts"

// Only the string is compared at runtime, so nothing in the normal suite notices when upstream moves versions; this canary does.
func TestCanaryWellKnownDiscoverySchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamWellKnownSource, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", upstreamWellKnownSource, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %s (moved or renamed — relocate the canary)", upstreamWellKnownSource, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if !strings.Contains(source, wellKnownSchemaV02) {
		t.Fatalf("upstream %s no longer contains %q: the discovery contract moved — check DISCOVERY_SCHEMA_* there and update the installer",
			upstreamWellKnownSource, wellKnownSchemaV02)
	}
	if strings.Contains(source, "DISCOVERY_SCHEMA_V3") {
		t.Fatalf("upstream declares DISCOVERY_SCHEMA_V3: a newer discovery format exists; review whether the installer should accept it")
	}
}
