package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

// Capability method carriers, mixed into fakeProvider to exercise the priority
// ladder in ProbeBulkInstalled. Optional capabilities are detected by method
// presence, so each combination needs a distinct type.

type byManagerCap struct{ err error }

func (c byManagerCap) InstalledByManager(context.Context) (map[string]provider.InstalledEntry, error) {
	return map[string]provider.InstalledEntry{"tool": {Version: "1", ConcreteManager: "brew"}}, c.err
}

type metadataCap struct{}

func (metadataCap) InstalledMetadataMap(context.Context) (map[string]provider.InstalledMetadata, error) {
	return map[string]provider.InstalledMetadata{"tool": {Version: "1"}}, nil
}

type simpleCap struct{}

func (simpleCap) InstalledMap(context.Context) (map[string]string, error) {
	return map[string]string{"tool": "1"}, nil
}

type provAll struct {
	*fakeProvider
	byManagerCap
	metadataCap
	simpleCap
}

type provMetaSimple struct {
	*fakeProvider
	metadataCap
	simpleCap
}

type provSimple struct {
	*fakeProvider
	simpleCap
}

type provByManagerErr struct {
	*fakeProvider
	byManagerCap
}

func TestProbeBulkInstalled_Priority(t *testing.T) {
	base := &fakeProvider{name: "p"}
	tests := []struct {
		name string
		p    provider.Provider
		want provider.BulkInstalledKind
	}{
		{"all capabilities → by-manager wins", provAll{fakeProvider: base}, provider.BulkInstalledByManager},
		{"metadata beats simple", provMetaSimple{fakeProvider: base}, provider.BulkInstalledMetadata},
		{"simple only", provSimple{fakeProvider: base}, provider.BulkInstalledSimple},
		{"no bulk capability", base, provider.BulkInstalledNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scan, err := provider.ProbeBulkInstalled(context.Background(), tt.p)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scan.Kind != tt.want {
				t.Fatalf("Kind = %v, want %v", scan.Kind, tt.want)
			}
		})
	}
}

func TestProbeBulkInstalled_ReturnsMatchedKindWithError(t *testing.T) {
	base := &fakeProvider{name: "p"}
	wantErr := errors.New("scan failed")
	scan, err := provider.ProbeBulkInstalled(
		context.Background(),
		provByManagerErr{fakeProvider: base, byManagerCap: byManagerCap{err: wantErr}},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	// The matched Kind is reported even on failure, so callers can tell "no
	// capability" from "capability ran and failed".
	if scan.Kind != provider.BulkInstalledByManager {
		t.Fatalf("Kind = %v, want BulkInstalledByManager", scan.Kind)
	}
}
