package system_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/system"
)

// describingProvider wraps fakeProvider and implements provider.Descriptor.
type describingProvider struct {
	fakeProvider
	desc string
}

func (d *describingProvider) Describe(_ context.Context, _ provider.Tool) (string, error) {
	return d.desc, nil
}

func TestDescribe_DelegatesToFirstAvailable(t *testing.T) {
	dp := &describingProvider{fakeProvider: fakeProvider{name: "apt", available: true}, desc: "a curl description"}
	p := system.New(dp)
	got, err := p.Describe(context.Background(), provider.Tool{Name: "curl"})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got != "a curl description" {
		t.Errorf("Describe() = %q, want 'a curl description'", got)
	}
}

func TestDescribe_NoDelegateAvailable(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: false})
	got, err := p.Describe(context.Background(), provider.Tool{Name: "curl"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty desc when no delegate available, got %q", got)
	}
}

func TestDescribe_DelegateNoDescriptor(t *testing.T) {
	p := system.New(&fakeProvider{name: "apt", available: true})
	got, err := p.Describe(context.Background(), provider.Tool{Name: "curl"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty desc when delegate has no Describe, got %q", got)
	}
}
