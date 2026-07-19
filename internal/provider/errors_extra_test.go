package provider_test

import (
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestActionError_Error(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *provider.ActionError
		want string
	}{
		{"nil receiver", nil, ""},
		{"summary wins", &provider.ActionError{Summary: "concise", Code: "x", Cause: errors.New("deep")}, "concise"},
		{"code fallback", &provider.ActionError{Code: provider.ErrorExternallyManagedPython}, string(provider.ErrorExternallyManagedPython)},
		{"cause fallback", &provider.ActionError{Cause: errors.New("boom")}, "boom"},
		{"default", &provider.ActionError{}, "provider action failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActionError_Unwrap(t *testing.T) {
	var nilErr *provider.ActionError
	if nilErr.Unwrap() != nil {
		t.Fatal("nil ActionError should unwrap to nil")
	}
	cause := errors.New("underlying")
	if got := (&provider.ActionError{Cause: cause}).Unwrap(); got != cause {
		t.Fatalf("Unwrap = %v, want %v", got, cause)
	}
	if got := (&provider.ActionError{}).Unwrap(); got != nil {
		t.Fatalf("Unwrap with no cause = %v, want nil", got)
	}
}
