package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestVerifyFallbackChecksum_ReusesStoredOnlyWhenAssetScopeMatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assetPath := filepath.Join(dir, "tool.tar.gz")
	if err := os.WriteFile(assetPath, []byte("payload bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	const wrongDigest = "0000000000000000000000000000000000000000000000000000000000000000"

	a := &App{}
	ctx := context.Background()

	matching := &config.FallbackSpec{Recipe: config.FallbackRecipe{
		Checksum:        wrongDigest,
		ChecksumAssetID: "asset-1",
		AssetID:         "asset-1",
	}}
	if err := a.verifyFallbackChecksum(ctx, "tool", matching, assetPath, "tool.tar.gz"); err == nil {
		t.Fatal("matching asset scope: expected mismatch error from the stored digest, got nil")
	}

	stale := &config.FallbackSpec{Recipe: config.FallbackRecipe{
		Checksum:        wrongDigest,
		ChecksumAssetID: "old-asset",
		AssetID:         "asset-1",
	}}
	if err := a.verifyFallbackChecksum(ctx, "tool", stale, assetPath, "tool.tar.gz"); err != nil {
		t.Fatalf("mismatched asset scope: stale digest must be ignored (nil), got %v", err)
	}
}
