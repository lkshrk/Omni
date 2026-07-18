package provider

import "context"

// BulkInstalledKind names which optional bulk-installed capability a provider
// satisfied when probed by ProbeBulkInstalled.
type BulkInstalledKind int

const (
	// BulkInstalledNone means the provider implements no bulk-installed capability.
	BulkInstalledNone BulkInstalledKind = iota
	// BulkInstalledByManager: MultiManagerBulkChecker (per-backend attribution).
	BulkInstalledByManager
	// BulkInstalledMetadata: MetadataBulkChecker (version + cached metadata).
	BulkInstalledMetadata
	// BulkInstalledSimple: BulkChecker (name→version only).
	BulkInstalledSimple
)

// BulkInstalledScan holds the result of probing a provider's best available
// bulk-installed capability. Exactly one of ByManager/Metadata/Installed is set,
// selected by Kind.
type BulkInstalledScan struct {
	Kind      BulkInstalledKind
	ByManager map[string]InstalledEntry    // Kind == BulkInstalledByManager
	Metadata  map[string]InstalledMetadata // Kind == BulkInstalledMetadata
	Installed map[string]string            // Kind == BulkInstalledSimple
}

// ProbeBulkInstalled probes p for the highest-priority bulk-installed capability
// it implements — MultiManagerBulkChecker > MetadataBulkChecker > BulkChecker —
// and returns its data. This is the single definition of that priority order;
// callers switch on the returned Kind instead of re-deriving the assertion
// ladder at each scan site.
//
// The returned error is whatever the matched capability returned; it is paired
// with the matched Kind, so a caller can distinguish "provider has no bulk
// capability" (Kind == BulkInstalledNone, nil error) from "capability ran and
// failed" (Kind set, non-nil error).
func ProbeBulkInstalled(ctx context.Context, p Provider) (BulkInstalledScan, error) {
	if mbc, ok := p.(MultiManagerBulkChecker); ok {
		entries, err := mbc.InstalledByManager(ctx)
		return BulkInstalledScan{Kind: BulkInstalledByManager, ByManager: entries}, err
	}
	if mbc, ok := p.(MetadataBulkChecker); ok {
		metadata, err := mbc.InstalledMetadataMap(ctx)
		return BulkInstalledScan{Kind: BulkInstalledMetadata, Metadata: metadata}, err
	}
	if bc, ok := p.(BulkChecker); ok {
		m, err := bc.InstalledMap(ctx)
		return BulkInstalledScan{Kind: BulkInstalledSimple, Installed: m}, err
	}
	return BulkInstalledScan{Kind: BulkInstalledNone}, nil
}
