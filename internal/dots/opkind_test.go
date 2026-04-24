package dots_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestOpKind_String(t *testing.T) {
	cases := []struct {
		kind dots.OpKind
		want string
	}{
		{dots.OpSkip, "skip"},
		{dots.OpLink, "link"},
		{dots.OpRepair, "repair"},
		{dots.OpAdopt, "adopt"},
		{dots.OpConflict, "conflict"},
		{dots.OpDryLink, "dry:link"},
		{dots.OpDryRepair, "dry:repair"},
		{dots.OpDryAdopt, "dry:adopt"},
		{dots.OpUnlink, "unlink"},
		{dots.OpUnlinkSkip, "unlink:skip"},
		{dots.OpUnlinkConflict, "unlink:conflict"},
		{dots.OpKind(999), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("OpKind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
		}
	}
}
