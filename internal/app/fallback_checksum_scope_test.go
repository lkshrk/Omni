package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// TestVerifyFallbackChecksum_ReusesStoredOnlyWhenAssetScopeMatches is a security
// guard: a previously-stored checksum digest may only be trusted when it was
// recorded against the exact asset now being installed (ChecksumAssetID ==
// AssetID). On a version bump or rotated asset the stale digest must NOT be
// trusted; verification re-fetches the authoritative checksum instead.
func TestVerifyFallbackChecksum_ReusesStoredOnlyWhenAssetScopeMatches(t *testing.T) {
	dir := t.TempDir()
	assetPath := filepath.Join(dir, "tool.tar.gz")
	if err := os.WriteFile(assetPath, []byte("payload bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A digest that does not match the file, so "trust the stored digest" is
	// observable as a checksum-mismatch error.
	const wrongDigest = "0000000000000000000000000000000000000000000000000000000000000000"

	a := &App{}
	ctx := context.Background()

	// Matching asset scope → the stored digest is trusted and verified against
	// the file, which does not match → hard-fail. This proves the reuse path is
	// taken when the scope matches.
	matching := &config.FallbackSpec{Recipe: config.FallbackRecipe{
		Checksum:        wrongDigest,
		ChecksumAssetID: "asset-1",
		AssetID:         "asset-1",
	}}
	if err := a.verifyFallbackChecksum(ctx, "tool", matching, assetPath, "tool.tar.gz"); err == nil {
		t.Fatal("matching asset scope: expected mismatch error from the stored digest, got nil")
	}

	// Mismatched asset scope (rotated/bumped asset) with no fetchable source →
	// the stale digest is NOT used; the best-effort re-fetch is skipped (empty
	// owner/repo/tag) and install proceeds. If the stale digest were trusted this
	// would fail like the matching case above.
	stale := &config.FallbackSpec{Recipe: config.FallbackRecipe{
		Checksum:        wrongDigest,
		ChecksumAssetID: "old-asset",
		AssetID:         "asset-1",
	}}
	if err := a.verifyFallbackChecksum(ctx, "tool", stale, assetPath, "tool.tar.gz"); err != nil {
		t.Fatalf("mismatched asset scope: stale digest must be ignored (nil), got %v", err)
	}
}
