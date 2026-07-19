package provider_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestPrivilegePlan_RequiresPrivilege(t *testing.T) {
	for _, tc := range []struct {
		req  provider.PrivilegeRequirement
		want bool
	}{
		{provider.PrivilegeRequired, true},
		{provider.PrivilegeMaybe, true},
		{provider.PrivilegeNone, false},
	} {
		if got := (provider.PrivilegePlan{Requirement: tc.req}).RequiresPrivilege(); got != tc.want {
			t.Errorf("RequiresPrivilege(%q) = %v, want %v", tc.req, got, tc.want)
		}
	}
}
