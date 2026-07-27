package provider_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

// Implements every forwarded capability plus both family interfaces, covering each delegator's "base implements it" branch.
type fullCapBase struct {
	namedBaseProvider
}

func (fullCapBase) InstalledMap(context.Context) (map[string]string, error) {
	return map[string]string{"typescript": "5.4.0"}, nil
}
func (fullCapBase) InstalledMetadataMap(context.Context) (map[string]provider.InstalledMetadata, error) {
	return map[string]provider.InstalledMetadata{"typescript": {Version: "5.4.0"}}, nil
}
func (fullCapBase) OutdatedMap(context.Context) (map[string]string, error) {
	return map[string]string{"typescript": "5.5.0"}, nil
}
func (fullCapBase) OutdatedInfoMap(context.Context) (map[string]provider.OutdatedInfo, error) {
	return map[string]provider.OutdatedInfo{"typescript": {LatestVersion: "5.5.0"}}, nil
}
func (fullCapBase) RefreshMetadata(context.Context) error { return nil }
func (fullCapBase) Describe(context.Context, provider.Tool) (string, error) {
	return "the TypeScript compiler", nil
}
func (fullCapBase) BulkDescribe(context.Context, []provider.Tool) (map[string]string, error) {
	return map[string]string{"typescript": "the TypeScript compiler"}, nil
}
func (fullCapBase) CLIToolSet(context.Context) (map[string]bool, error) {
	return map[string]bool{"typescript": true}, nil
}
func (fullCapBase) PrivilegePlan(context.Context, provider.PrivilegeAction, provider.Tool) (provider.PrivilegePlan, error) {
	return provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "needs root"}, nil
}
func (fullCapBase) PrivilegeCommand(provider.PrivilegeAction, provider.Tool) (string, []string, bool) {
	return "apt-get", []string{"install", "vim"}, true
}
func (fullCapBase) InstalledByManager(context.Context) (map[string]provider.InstalledEntry, error) {
	return map[string]provider.InstalledEntry{"typescript": {Version: "5.4.0", ConcreteManager: "bun"}}, nil
}
func (fullCapBase) OutdatedInfoByManager(context.Context) (map[string]map[string]provider.OutdatedInfo, error) {
	return map[string]map[string]provider.OutdatedInfo{"bun": {"typescript": {LatestVersion: "5.5.0"}}}, nil
}

func TestNamed_ForwardsAllCapabilities(t *testing.T) {
	ctx := context.Background()
	p := provider.Named("bun", fullCapBase{})

	if m, err := p.(provider.BulkChecker).InstalledMap(ctx); err != nil || m["typescript"] != "5.4.0" {
		t.Fatalf("InstalledMap = %v/%v", m, err)
	}
	if m, err := p.(provider.MetadataBulkChecker).InstalledMetadataMap(ctx); err != nil || m["typescript"].Version != "5.4.0" {
		t.Fatalf("InstalledMetadataMap = %v/%v", m, err)
	}
	if m, err := p.(provider.OutdatedChecker).OutdatedMap(ctx); err != nil || m["typescript"] != "5.5.0" {
		t.Fatalf("OutdatedMap = %v/%v", m, err)
	}
	if m, err := p.(provider.OutdatedInfoChecker).OutdatedInfoMap(ctx); err != nil || m["typescript"].LatestVersion != "5.5.0" {
		t.Fatalf("OutdatedInfoMap = %v/%v", m, err)
	}
	if err := p.(provider.MetadataRefresher).RefreshMetadata(ctx); err != nil {
		t.Fatalf("RefreshMetadata = %v", err)
	}
	if d, err := p.(provider.Descriptor).Describe(ctx, provider.Tool{Name: "typescript"}); err != nil || d != "the TypeScript compiler" {
		t.Fatalf("Describe = %q/%v", d, err)
	}
	if m, err := p.(provider.BulkDescriber).BulkDescribe(ctx, []provider.Tool{{Name: "typescript"}}); err != nil || m["typescript"] == "" {
		t.Fatalf("BulkDescribe = %v/%v", m, err)
	}
	if m, err := p.(provider.CLIToolProvider).CLIToolSet(ctx); err != nil || !m["typescript"] {
		t.Fatalf("CLIToolSet = %v/%v", m, err)
	}
	if plan, err := p.(provider.PrivilegePlanner).PrivilegePlan(ctx, provider.PrivilegeActionInstall, provider.Tool{Name: "vim"}); err != nil || plan.Requirement != provider.PrivilegeRequired {
		t.Fatalf("PrivilegePlan = %+v/%v", plan, err)
	}
	if cmd, args, ok := p.(provider.PrivilegeCommandPlanner).PrivilegeCommand(provider.PrivilegeActionInstall, provider.Tool{Name: "vim"}); !ok || cmd != "apt-get" || len(args) != 2 {
		t.Fatalf("PrivilegeCommand = %q/%v/%v", cmd, args, ok)
	}
	if _, ok := p.(provider.MultiManagerBulkChecker); !ok {
		t.Fatal("combined wrapper should expose MultiManagerBulkChecker")
	}
	m, err := p.(provider.ManagerOutdatedInfoChecker).OutdatedInfoByManager(ctx)
	if err != nil || m["bun"]["typescript"].LatestVersion != "5.5.0" {
		t.Fatalf("OutdatedInfoByManager = %v/%v", m, err)
	}
}

func TestNamed_CapabilitiesDegradeWhenBaseLacksThem(t *testing.T) {
	ctx := context.Background()
	p := provider.Named("npm", namedBaseProvider{})

	if m, err := p.(provider.BulkChecker).InstalledMap(ctx); err != nil || m != nil {
		t.Fatalf("InstalledMap = %v/%v, want nil/nil", m, err)
	}
	if m, err := p.(provider.MetadataBulkChecker).InstalledMetadataMap(ctx); err != nil || m != nil {
		t.Fatalf("InstalledMetadataMap = %v/%v, want nil/nil", m, err)
	}
	if m, err := p.(provider.OutdatedChecker).OutdatedMap(ctx); err != nil || m != nil {
		t.Fatalf("OutdatedMap = %v/%v, want nil/nil", m, err)
	}
	if m, err := p.(provider.OutdatedInfoChecker).OutdatedInfoMap(ctx); err != nil || m != nil {
		t.Fatalf("OutdatedInfoMap = %v/%v, want nil/nil", m, err)
	}
	if err := p.(provider.MetadataRefresher).RefreshMetadata(ctx); err != nil {
		t.Fatalf("RefreshMetadata = %v, want nil", err)
	}
	if d, err := p.(provider.Descriptor).Describe(ctx, provider.Tool{Name: "x"}); err != nil || d != "" {
		t.Fatalf("Describe = %q/%v, want empty/nil", d, err)
	}
	if m, err := p.(provider.BulkDescriber).BulkDescribe(ctx, nil); err != nil || m != nil {
		t.Fatalf("BulkDescribe = %v/%v, want nil/nil", m, err)
	}
	if m, err := p.(provider.CLIToolProvider).CLIToolSet(ctx); err != nil || m != nil {
		t.Fatalf("CLIToolSet = %v/%v, want nil/nil", m, err)
	}
	if plan, err := p.(provider.PrivilegePlanner).PrivilegePlan(ctx, provider.PrivilegeActionInstall, provider.Tool{}); err != nil || plan != (provider.PrivilegePlan{}) {
		t.Fatalf("PrivilegePlan = %+v/%v, want zero/nil", plan, err)
	}
	if cmd, args, ok := p.(provider.PrivilegeCommandPlanner).PrivilegeCommand(provider.PrivilegeActionInstall, provider.Tool{}); ok || cmd != "" || args != nil {
		t.Fatalf("PrivilegeCommand = %q/%v/%v, want empty/nil/false", cmd, args, ok)
	}
}
