package provider

import "context"

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

// BulkInstalledScan — Exactly one of ByManager/Metadata/Installed is set, selected by Kind.
type BulkInstalledScan struct {
	Kind      BulkInstalledKind
	ByManager map[string]InstalledEntry    // Kind == BulkInstalledByManager
	Metadata  map[string]InstalledMetadata // Kind == BulkInstalledMetadata
	Installed map[string]string            // Kind == BulkInstalledSimple
}

// ProbeBulkInstalled — The single definition of the priority MultiManager > Metadata > Bulk; Kind None with nil error means no capability.
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
