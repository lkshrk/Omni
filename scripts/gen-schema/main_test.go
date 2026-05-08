package main

import "testing"

func TestDotEntrySchemaMatchesConfigShape(t *testing.T) {
	dotEntry := build().Defs["DotEntry"]
	if dotEntry == nil {
		t.Fatal("DotEntry schema missing")
	}
	if _, ok := dotEntry.Properties["path"]; !ok {
		t.Fatal("DotEntry schema missing path property")
	}
	for _, removed := range []string{"source", "target"} {
		if _, ok := dotEntry.Properties[removed]; ok {
			t.Fatalf("DotEntry schema includes stale %q property", removed)
		}
	}
	if !hasRequired(dotEntry.Required, "path") {
		t.Fatalf("DotEntry required fields = %v, want path", dotEntry.Required)
	}
}

func TestGroupSchemaDoesNotAdvertiseNonPersistedIgnore(t *testing.T) {
	group := build().Defs["GroupConfig"]
	if group == nil {
		t.Fatal("GroupConfig schema missing")
	}
	if _, ok := group.Properties["ignore"]; ok {
		t.Fatal("GroupConfig schema includes non-persisted ignore property")
	}
}

func hasRequired(required []string, want string) bool {
	for _, got := range required {
		if got == want {
			return true
		}
	}
	return false
}
