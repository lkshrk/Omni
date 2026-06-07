package main

import (
	"bytes"
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

func TestWriteVersionedSchemaAllowsDescriptionOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "omni.settings.v1.schema.json")
	doc := build()
	oldDoc := build()
	oldDoc.Description = "old generated wording"
	data, err := encodeSchema(oldDoc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeVersionedSchema(out, doc); err != nil {
		t.Fatalf("writeVersionedSchema description-only change: %v", err)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, data) {
		t.Fatal("description-only versioned schema comparison rewrote existing schema")
	}
}

func TestWriteVersionedSchemaRejectsStructuralChanges(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "omni.settings.v1.schema.json")
	doc := build()
	oldDoc := build()
	oldDoc.Type = "array"
	data, err := encodeSchema(oldDoc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatal(err)
	}
	err = writeVersionedSchema(out, doc)
	if err == nil {
		t.Fatal("expected structural schema change to be rejected")
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

func TestToolSchemaUsesConcreteProviderCandidates(t *testing.T) {
	defs := build().Defs
	tool := defs["ToolSpec"]
	if tool == nil {
		t.Fatal("ToolSpec schema missing")
	}
	if hasRequired(tool.Required, "provider") {
		t.Fatalf("ToolSpec required fields = %v, want provider optional for legacy migration only", tool.Required)
	}
	providers := tool.Properties["providers"]
	if providers == nil || providers.Type != "array" || providers.Items == nil || providers.Items.Ref != "#/$defs/ToolInstallSpec" {
		t.Fatalf("providers schema = %+v, want array of ToolInstallSpec refs", providers)
	}

	install := defs["ToolInstallSpec"]
	if install == nil {
		t.Fatal("ToolInstallSpec schema missing")
	}
	providerProp := install.Properties["provider"]
	if providerProp == nil {
		t.Fatal("ToolInstallSpec provider property missing")
	}
	for _, concrete := range []string{"brew", "apt", "npm", "pip", "script"} {
		if !hasEnum(providerProp.Enum, concrete) {
			t.Fatalf("ToolInstallSpec provider enum missing concrete provider %q: %v", concrete, providerProp.Enum)
		}
	}
	for _, ecosystem := range []string{"system", "node", "python"} {
		if hasEnum(providerProp.Enum, ecosystem) {
			t.Fatalf("ToolInstallSpec provider enum includes provider family %q: %v", ecosystem, providerProp.Enum)
		}
	}
}

func TestLegacyEcosystemSchemaDescriptionsUseProviderFamilyWording(t *testing.T) {
	root := build()
	settings := root.Defs["Settings"]
	if settings == nil {
		t.Fatal("Settings schema missing")
	}
	ecosystems := settings.Properties["ecosystems"]
	if ecosystems == nil {
		t.Fatal("settings.ecosystems schema missing")
	}
	if !strings.Contains(ecosystems.Description, "Legacy provider-family settings") {
		t.Fatalf("settings.ecosystems description = %q, want legacy provider-family wording", ecosystems.Description)
	}
	if strings.Contains(ecosystems.Description, "ecosystem provider") {
		t.Fatalf("settings.ecosystems description contains stale wording: %q", ecosystems.Description)
	}

	legacy := root.Defs["EcosystemSettings"]
	if legacy == nil {
		t.Fatal("EcosystemSettings schema missing")
	}
	if !strings.Contains(legacy.Description, "provider family") {
		t.Fatalf("EcosystemSettings description = %q, want provider-family wording", legacy.Description)
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

func hasEnum(values []any, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}
