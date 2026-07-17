package buildinfo

import (
	"strings"
	"testing"
)

func TestShortNonEmpty(t *testing.T) {
	if Short() == "" {
		t.Fatal("Short() must never be empty")
	}
}

func TestFullContainsShort(t *testing.T) {
	if !strings.HasPrefix(Full(), Short()) {
		t.Fatalf("Full() = %q must start with Short() = %q", Full(), Short())
	}
}
