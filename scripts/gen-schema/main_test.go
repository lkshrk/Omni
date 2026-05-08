package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCurrentSchemaPathTracksConfigVersion(t *testing.T) {
	want := fmt.Sprintf("spec/omni.settings.v%d.schema.json", config.CurrentVersion)
	if got := currentOutput(); got != want {
		t.Fatalf("current output = %q, want %q", got, want)
	}
	if !strings.Contains(config.SchemaURL, fmt.Sprintf(".v%d.", config.CurrentVersion)) {
		t.Fatalf("SchemaURL %q does not include current version %d", config.SchemaURL, config.CurrentVersion)
	}
}

func TestRootSchemaRequiresConfigVersion(t *testing.T) {
	root := build()
	version := root.Properties["version"]
	if version == nil {
		t.Fatal("root schema missing version property")
	}
	if version.Const != config.CurrentVersion {
		t.Fatalf("version const = %v, want %d", version.Const, config.CurrentVersion)
	}
	if !hasRequired(root.Required, "version") {
		t.Fatalf("root required fields = %v, want version", root.Required)
	}
}

func TestWriteVersionedSchemaRefusesToRewriteExistingVersion(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "omni.settings.v1.schema.json")
	if err := os.WriteFile(out, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeVersionedSchema(out, build())
	if err == nil {
		t.Fatal("expected existing versioned schema with different content to be rejected")
	}
	if !strings.Contains(err.Error(), "bump config.CurrentVersion") {
		t.Fatalf("error = %v, want CurrentVersion guidance", err)
	}
}

func TestWriteVersionedSchemaCreatesMissingVersion(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "omni.settings.v1.schema.json")
	if err := writeVersionedSchema(out, build()); err != nil {
		t.Fatalf("writeVersionedSchema: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), fmt.Sprintf(`"const": %d`, config.CurrentVersion)) {
		t.Fatalf("schema does not include current version const:\n%s", data)
	}
}

func TestDotEntrySchemaMatchesConfigShape(t *testing.T) {
	dotEntry := build().Defs["DotEntry"]
	if dotEntry == nil {
		t.Fatal("DotEntry schema missing")
	}
	if _, ok := dotEntry.Properties["path"]; !ok {
		t.Fatal("DotEntry schema missing path property")
	}
	if _, ok := dotEntry.Properties["package"]; !ok {
		t.Fatal("DotEntry schema missing package property")
	}
	if _, ok := dotEntry.Properties["hosts"]; !ok {
		t.Fatal("DotEntry schema missing hosts property")
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
